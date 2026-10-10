package opsgenie

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/present"
)

func TestMain(m *testing.M) {
	// httptest servers listen on loopback, which the default egress policy
	// refuses; the policy itself is covered in package plugin.
	plugin.Configure(plugin.Runtime{AppBaseURL: "https://probara.example.com"})
	os.Exit(m.Run())
}

type call struct {
	method, path, query, auth string
	body                      map[string]any
}

func recordServer(t *testing.T, status int) (*httptest.Server, *[]call) {
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		calls = append(calls, call{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, auth: r.Header.Get("Authorization"), body: body})
		if status != 0 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"message":"Key format is not valid!"}`))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func sampleEvent() notifications.AlertEvent {
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	return notifications.AlertEvent{
		Type:      "created",
		TenantID:  "tenant-1",
		Timestamp: now,
		Alert: notifications.AlertDetails{
			ID:           "a-1",
			MonitorID:    "3f2a91cc-11de-4b7a-9a2e-6c5f8d0e1234",
			MonitorName:  "API health",
			PolicyName:   "Critical",
			Status:       "active",
			TriggeredAt:  now,
			FailureCount: 3,
		},
	}
}

func send(p *Plugin, eventType string, test bool) error {
	return p.Send(context.Background(), plugin.DispatchRequest{
		Channel:   plugin.ChannelRef{Config: map[string]any{"api_key": "k-123", "priority": "P2"}},
		Event:     sampleEvent(),
		EventType: eventType,
		Test:      test,
	})
}

func TestValidate(t *testing.T) {
	cases := []struct {
		raw     string
		wantErr bool
	}{
		{`{"api_key":"abc"}`, false},
		{`{"api_key":"abc","site":"jsm","priority":"p3"}`, false},
		{`{}`, true},
		{`{"api_key":"a b"}`, true},
		{`{"api_key":"abc","site":"moon"}`, true},
		{`{"api_key":"abc","priority":"P9"}`, true},
	}
	for _, tc := range cases {
		if err := New().Validate(json.RawMessage(tc.raw)); (err != nil) != tc.wantErr {
			t.Errorf("Validate(%s) err=%v wantErr=%v", tc.raw, err, tc.wantErr)
		}
	}
}

func TestSend_Lifecycle(t *testing.T) {
	srv, calls := recordServer(t, 0)
	p := New()
	p.base = srv.URL + "/v2/alerts"

	for _, et := range []string{"created", "reminder", "acknowledged", "resolved"} {
		if err := send(p, et, false); err != nil {
			t.Fatalf("Send(%s): %v", et, err)
		}
	}
	if len(*calls) != 3 {
		t.Fatalf("calls = %d, want 3 (reminder skipped)", len(*calls))
	}
	create, ack, closeCall := (*calls)[0], (*calls)[1], (*calls)[2]
	if create.path != "/v2/alerts" || create.body["alias"] != "probara-a-1" || create.body["priority"] != "P2" {
		t.Errorf("create = %+v", create)
	}
	if !strings.HasPrefix(create.body["message"].(string), "Alert Triggered: API health") {
		t.Errorf("message = %v", create.body["message"])
	}
	if ack.path != "/v2/alerts/probara-a-1/acknowledge" || ack.query != "identifierType=alias" {
		t.Errorf("ack = %+v", ack)
	}
	if closeCall.path != "/v2/alerts/probara-a-1/close" {
		t.Errorf("close = %+v", closeCall)
	}
	for _, c := range *calls {
		if c.auth != "GenieKey k-123" {
			t.Errorf("Authorization = %q", c.auth)
		}
	}
}

func TestSend_TestCreatesThenCloses(t *testing.T) {
	srv, calls := recordServer(t, 0)
	p := New()
	p.base = srv.URL + "/v2/alerts"
	if err := send(p, "created", true); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(*calls) != 3 || (*calls)[1].method != http.MethodGet || !strings.HasSuffix((*calls)[2].path, "/close") {
		t.Fatalf("calls = %+v, want create, lookup, close", *calls)
	}
	if (*calls)[0].body["priority"] != "P5" {
		t.Errorf("test priority = %v, want P5", (*calls)[0].body["priority"])
	}
}

// The Alert API processes requests asynchronously, so the test send must not
// close the alert until a lookup finds it — a close processed before the
// create fails and leaves the test alert open.
func TestSend_TestWaitsForCreateBeforeClosing(t *testing.T) {
	var calls []string
	lookups := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodGet {
			lookups++
			if lookups < 3 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)
	p := New()
	p.base = srv.URL + "/v2/alerts"
	p.pollInterval = time.Millisecond
	if err := send(p, "created", true); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(calls) != 5 || !strings.HasSuffix(calls[4], "/close") || lookups != 3 {
		t.Fatalf("calls = %v, want create, 3 lookups, close", calls)
	}
}

func TestSend_TestReportsAlertThatNeverAppeared(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)
	p := New()
	p.base = srv.URL + "/v2/alerts"
	p.pollInterval = time.Millisecond
	err := send(p, "created", true)
	if err == nil || plugin.IsPermanent(err) {
		t.Fatalf("err = %v, want a non-permanent 'not processed' error", err)
	}
}

func TestSend_InvalidPriorityIsPermanent(t *testing.T) {
	p := New()
	p.base = "http://unused.invalid/v2/alerts"
	err := p.Send(context.Background(), plugin.DispatchRequest{
		Channel:   plugin.ChannelRef{Config: map[string]any{"api_key": "k-123", "priority": "P"}},
		Event:     sampleEvent(),
		EventType: "created",
	})
	if !plugin.IsPermanent(err) {
		t.Fatalf("err = %v, want permanent", err)
	}
}

func TestSend_UnauthorizedIsPermanent(t *testing.T) {
	srv, _ := recordServer(t, http.StatusUnauthorized)
	p := New()
	p.base = srv.URL + "/v2/alerts"
	if err := send(p, "created", false); !plugin.IsPermanent(err) {
		t.Fatalf("err = %v, want permanent", err)
	}
}

func TestPriorityFor(t *testing.T) {
	cases := []struct {
		tone   present.Tone
		outage string
		want   string
	}{
		{present.ToneDown, "P1", "P1"},
		{present.ToneWarn, "P1", "P3"},
		{present.ToneWarn, "P4", "P5"},
		{present.ToneWarn, "P5", "P5"},
		{present.ToneInfo, "P1", "P5"},
	}
	for _, tc := range cases {
		if got := priorityFor(tc.tone, tc.outage); got != tc.want {
			t.Errorf("priorityFor(%s, %s) = %s, want %s", tc.tone, tc.outage, got, tc.want)
		}
	}
}
