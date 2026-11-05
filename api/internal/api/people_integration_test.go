//go:build !unit

package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// The people routes through the real router: contacts are a signed-in
// user's own (any role, never an API key) and people settings are
// tenant-admin writes.
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
