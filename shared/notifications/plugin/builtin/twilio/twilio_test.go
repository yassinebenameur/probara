package twilio

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

// Fake identifiers, assembled at runtime so secret scanners do not mistake a
// literal in the source for a real Twilio credential.
var (
	fakeHex32  = strings.Repeat("0123456789abcdef", 2)
	accountSID = "AC" + fakeHex32
	apiKeySID  = "SK" + fakeHex32
)

func TestMain(m *testing.M) {
	// httptest servers listen on loopback, which the default egress policy
	// refuses; the policy itself is covered in package plugin.
	plugin.Configure(plugin.Runtime{AppBaseURL: "https://probara.example.com"})
	os.Exit(m.Run())
}

func config(extra map[string]any) map[string]any {
	c := map[string]any{
		"account_sid": accountSID,
		"auth_token":  "tok",
		"from":        "+15551234567",
		"to":          "+15557654321,\n+447700900123",
	}
	for k, v := range extra {
		c[k] = v
	}
	return c
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     map[string]any
		wantErr bool
	}{
		{"valid", config(nil), false},
		{"messaging service sender", config(map[string]any{"from": "MG" + fakeHex32}), false},
		{"api key", config(map[string]any{"api_key_sid": apiKeySID}), false},
		{"bad account", config(map[string]any{"account_sid": "AC1"}), true},
		{"bad api key", config(map[string]any{"api_key_sid": "SK1"}), true},
		{"bad from", config(map[string]any{"from": "5551234567"}), true},
		{"bad recipient", config(map[string]any{"to": "+15557654321, 07700900123"}), true},
		{"no recipients", config(map[string]any{"to": " , "}), true},
		{"too many", config(map[string]any{"to": strings.Repeat("+15557654321,", 1) + "+15550000001,+15550000002,+15550000003,+15550000004,+15550000005,+15550000006,+15550000007,+15550000008,+15550000009,+15550000010"}), true},
		{"missing token", config(map[string]any{"auth_token": ""}), true},
	}
	for _, tc := range cases {
		raw, _ := json.Marshal(tc.cfg)
		if err := New().Validate(raw); (err != nil) != tc.wantErr {
			t.Errorf("%s: err=%v wantErr=%v", tc.name, err, tc.wantErr)
		}
	}
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
			Status:       "active",
			TriggeredAt:  now,
			FailureCount: 3,
		},
	}
}

func TestSend_OneFormPostPerRecipient(t *testing.T) {
	var mu sync.Mutex
	var forms []url.Values
	var auths, paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(raw))
		mu.Lock()
		forms = append(forms, form)
		auths = append(auths, r.Header.Get("Authorization"))
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	p := New()
	p.base = srv.URL
	err := p.Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: config(map[string]any{"api_key_sid": apiKeySID})},
		Event:   sampleEvent(),
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	// Recipients are texted concurrently, so arrival order is not fixed.
	sort.Slice(forms, func(i, j int) bool { return forms[i].Get("To") < forms[j].Get("To") })
	if len(forms) != 2 || forms[0].Get("To") != "+15557654321" || forms[1].Get("To") != "+447700900123" {
		t.Fatalf("forms = %+v", forms)
	}
	if forms[0].Get("From") != "+15551234567" {
		t.Errorf("From = %q", forms[0].Get("From"))
	}
	body := forms[0].Get("Body")
	if !strings.HasPrefix(body, "DOWN · Alert Triggered: API health") || !strings.Contains(body, "https://probara.example.com/monitors/") {
		t.Errorf("Body = %q", body)
	}
	if paths[0] != "/2010-04-01/Accounts/"+accountSID+"/Messages.json" {
		t.Errorf("path = %q", paths[0])
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(apiKeySID+":tok"))
	if auths[0] != wantAuth {
		t.Errorf("Authorization = %q, want API key basic auth", auths[0])
	}
}

func TestSend_PartialTransientFailureRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		raw, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(raw))
		if form.Get("To") == "+15557654321" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	p := New()
	p.base = srv.URL
	err := p.Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: config(nil)},
		Event:   sampleEvent(),
	})
	if err == nil || plugin.IsPermanent(err) {
		t.Fatalf("err = %v, want a transient error so the send is retried", err)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("calls = %d, want every recipient attempted", n)
	}
	if strings.Contains(err.Error(), "+15557654321") {
		t.Fatalf("error leaked a full phone number: %v", err)
	}
}

func TestSend_AllPermanentFailuresArePermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":20003,"message":"Authenticate"}`))
	}))
	defer srv.Close()
	p := New()
	p.base = srv.URL
	err := p.Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: config(nil)},
		Event:   sampleEvent(),
	})
	if !plugin.IsPermanent(err) {
		t.Fatalf("err = %v, want permanent", err)
	}
}

// One recipient rejected for good (invalid number) and another failing
// transiently must still be retried as a whole.
func TestSend_MixedPermanentAndTransientStaysRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(raw))
		if form.Get("To") == "+15557654321" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":21211,"message":"Invalid 'To' Phone Number"}`))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	p := New()
	p.base = srv.URL
	err := p.Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: config(nil)},
		Event:   sampleEvent(),
	})
	if err == nil || plugin.IsPermanent(err) {
		t.Fatalf("err = %v, want a transient error so the 503 recipient is retried", err)
	}
}

// A 429's Retry-After must survive aggregation: the longest requested delay
// wins, whether one recipient or several were throttled.
func TestSend_KeepsLongestRetryAfter(t *testing.T) {
	cases := []struct {
		name   string
		byTo   map[string]string // recipient -> Retry-After ("" = 400 invalid number)
		wantD  time.Duration
		wantOK bool
	}{
		{"single throttled", map[string]string{"+15557654321": "120", "+447700900123": "ok"}, 120 * time.Second, true},
		{"longest wins", map[string]string{"+15557654321": "30", "+447700900123": "120"}, 120 * time.Second, true},
		{"mixed with permanent", map[string]string{"+15557654321": "", "+447700900123": "90"}, 90 * time.Second, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				form, _ := url.ParseQuery(string(raw))
				switch ra := tc.byTo[form.Get("To")]; ra {
				case "ok":
					w.WriteHeader(http.StatusCreated)
				case "":
					w.WriteHeader(http.StatusBadRequest)
				default:
					w.Header().Set("Retry-After", ra)
					w.WriteHeader(http.StatusTooManyRequests)
				}
			}))
			defer srv.Close()
			p := New()
			p.base = srv.URL
			err := p.Send(context.Background(), plugin.DispatchRequest{
				Channel: plugin.ChannelRef{Config: config(nil)},
				Event:   sampleEvent(),
			})
			if plugin.IsPermanent(err) {
				t.Fatalf("err = %v, want retryable", err)
			}
			if d, ok := plugin.RetryAfterDelay(err); ok != tc.wantOK || d != tc.wantD {
				t.Fatalf("Retry-After = %v (ok=%v), want %v", d, ok, tc.wantD)
			}
		})
	}
}

// A slow Messages API must not serialize the fan-out: with recipients texted
// one after another, the dispatch deadline runs out part-way through and the
// redelivery re-texts everyone who already got the message.
func TestSend_TextsRecipientsConcurrently(t *testing.T) {
	var inFlight, peak atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		inFlight.Add(-1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	p := New()
	p.base = srv.URL
	if err := p.Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: config(nil)},
		Event:   sampleEvent(),
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if peak.Load() < 2 {
		t.Fatalf("peak concurrent sends = %d, want recipients texted in parallel", peak.Load())
	}
}
