//go:build !unit

package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// The people routes through the real router: contacts are a signed-in
// user's own (any role, never an API key), people settings are tenant-admin
// writes, schedules sit behind the write gate, and the iCal feed is public.
func TestPeopleRoutes(t *testing.T) {
	f := newMCPFixture(t)
	f.createUser("lead", "member", []models.MembershipInput{{TenantID: f.tenantA, Role: "admin"}})
	f.createUser("watcher", "member", []models.MembershipInput{{TenantID: f.tenantA, Role: "viewer"}})
	lead, watcher := f.signIn("lead"), f.signIn("watcher")
	q := "?tenant_id=" + f.tenantA.String()

	// A viewer manages their own contacts.
	var list struct {
		Items      []map[string]any `json:"items"`
		Transports map[string]bool  `json:"transports"`
	}
	watcher.expect(http.MethodGet, "/api/v1/account/contacts"+q, http.StatusOK, &list)
	if list.Transports["email"] || list.Transports["sms"] {
		t.Fatalf("transports = %v, want none without SMTP or an SMS channel", list.Transports)
	}
	status, body := watcher.do(http.MethodPost, "/api/v1/account/contacts"+q, map[string]any{"kind": "sms", "address": "+15557654321"})
	if status != http.StatusCreated {
		t.Fatalf("create contact: %d %s", status, body)
	}
	var contact struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &contact)
	if status, body := watcher.do(http.MethodPost, "/api/v1/account/contacts/"+contact.ID+"/send-code"+q, nil); status != http.StatusConflict ||
		!strings.Contains(string(body), "SMS channel") {
		t.Fatalf("send code without an SMS channel: %d %s", status, body)
	}
	// Someone else's contact is not found.
	if status, _ := lead.do(http.MethodDelete, "/api/v1/account/contacts/"+contact.ID+q, nil); status != http.StatusNotFound {
		t.Fatalf("other user's contact: %d", status)
	}
	f.waitForAuditAction("account.contacts")

	// An API key cannot reach contacts.
	key := f.apiKey(f.tenantA, "write")
	req, _ := http.NewRequest(http.MethodGet, f.http.URL+"/api/v1/account/contacts", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("API key on contacts: %d", res.StatusCode)
	}

	// People settings: readable by members, written by tenant admins.
	watcher.expect(http.MethodGet, "/api/v1/people-settings"+q, http.StatusOK, nil)
	if status, _ := watcher.do(http.MethodPut, "/api/v1/people-settings"+q, map[string]any{"personal_sms_channel_id": nil}); status != http.StatusForbidden {
		t.Fatalf("viewer people settings write: %d", status)
	}
	if status, body := lead.do(http.MethodPut, "/api/v1/people-settings"+q, map[string]any{"personal_sms_channel_id": nil}); status != http.StatusOK {
		t.Fatalf("admin people settings write: %d %s", status, body)
	}

	// Schedules: viewers read, writers write.
	var people struct {
		Items []struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"items"`
	}
	watcher.expect(http.MethodGet, "/api/v1/oncall-schedules/people"+q, http.StatusOK, &people)
	ids := map[string]string{}
	for _, p := range people.Items {
		ids[p.Username] = p.ID
	}
	if ids["lead"] == "" || ids["watcher"] == "" {
		t.Fatalf("people = %+v", people.Items)
	}
	schedule := map[string]any{
		"name": "Primary", "timezone": "Europe/Paris",
		"rotations": []map[string]any{{
			"kind": "daily", "handoff_time": "09:00", "start_at": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
			"member_ids": []string{ids["lead"], ids["watcher"]},
		}},
	}
	if status, _ := watcher.do(http.MethodPost, "/api/v1/oncall-schedules"+q, schedule); status != http.StatusForbidden {
		t.Fatalf("viewer create schedule: %d", status)
	}
	status, body = lead.do(http.MethodPost, "/api/v1/oncall-schedules"+q, schedule)
	if status != http.StatusCreated {
		t.Fatalf("create schedule: %d %s", status, body)
	}
	var created struct {
		ID     string         `json:"id"`
		OnCall map[string]any `json:"on_call"`
	}
	_ = json.Unmarshal(body, &created)
	if created.OnCall == nil {
		t.Fatalf("created schedule has nobody on call: %s", body)
	}
	watcher.expect(http.MethodGet, "/api/v1/oncall-schedules/"+created.ID+"/preview"+q, http.StatusOK, nil)
	watcher.expect(http.MethodGet, "/api/v1/oncall-schedules/"+created.ID+"/on-call"+q, http.StatusOK, nil)

	// The calendar feed is public, by token.
	var tok struct {
		Path string `json:"path"`
	}
	status, body = lead.do(http.MethodPost, "/api/v1/oncall-schedules/"+created.ID+"/ical-token"+q, nil)
	if status != http.StatusOK {
		t.Fatalf("feed link: %d %s", status, body)
	}
	_ = json.Unmarshal(body, &tok)
	res, err = http.Get(f.http.URL + tok.Path)
	if err != nil {
		t.Fatal(err)
	}
	ics, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/calendar") ||
		!strings.Contains(string(ics), "BEGIN:VCALENDAR") {
		t.Fatalf("feed: %d %s %s", res.StatusCode, res.Header.Get("Content-Type"), ics)
	}
	res, err = http.Get(f.http.URL + "/api/v1/oncall-feeds/not-a-token.ics")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown feed token: %d", res.StatusCode)
	}
}

// waitForAuditAction polls the async audit writer for an action.
func (f *mcpFixture) waitForAuditAction(action string) {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		_ = f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM audit_log WHERE action = $1`, action).Scan(&n)
		if n > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	f.t.Fatalf("no audit record %s", action)
}
