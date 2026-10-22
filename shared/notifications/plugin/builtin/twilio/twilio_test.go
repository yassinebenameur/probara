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
// rest wait a whole backoff step for the redelivery.
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

func TestSend_SuspectedCauseTextsShortForm(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(raw))
		mu.Lock()
		bodies = append(bodies, form.Get("Body"))
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	event := sampleEvent()
	event.Type = "suspected_cause"
	event.Alert.SuspectedCause = &notifications.SuspectedCause{Key: "dns_failure", Title: "DNS resolution failure", Confidence: "medium"}
	p := New()
	p.base = srv.URL
	if err := p.Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: config(nil)}, Event: event, EventType: "suspected_cause",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("texts = %d, want one per recipient", len(bodies))
	}
	b := bodies[0]
	if !strings.HasPrefix(b, "AI TRIAGE · Suspected Cause: API health") || !strings.Contains(b, "DNS resolution failure (medium confidence)") ||
		!strings.Contains(b, "https://probara.example.com/alerts?alert=a-1&tenant=tenant-1") {
		t.Fatalf("Body = %q", b)
	}
}

// memLedger is an in-memory plugin.RecipientLedger shared across Send calls,
// standing in for the Postgres ledger every retry path reads.
type memLedger struct {
	mu        sync.Mutex
	delivered map[string]bool
	loadErr   error
	recordErr error
}

func newMemLedger() *memLedger { return &memLedger{delivered: map[string]bool{}} }

func (l *memLedger) Delivered(_ context.Context, recipients []string) (map[string]bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.loadErr != nil {
		return nil, l.loadErr
	}
	out := map[string]bool{}
	for _, r := range recipients {
		if l.delivered[r] {
			out[r] = true
		}
	}
	return out, nil
}

func (l *memLedger) Record(_ context.Context, recipient string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.recordErr != nil {
		return l.recordErr
	}
	l.delivered[recipient] = true
	return nil
}

func (l *memLedger) has(r string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.delivered[r]
}

const (
	numA = "+15557654321"
	numB = "+447700900123"
	numC = "+15550000001"
)

// statusServer answers each recipient with the status in byTo (default 201)
// and counts the texts each recipient received.
type statusServer struct {
	mu    sync.Mutex
	byTo  map[string]int
	texts map[string]int
	srv   *httptest.Server
}

func newStatusServer(t *testing.T, byTo map[string]int) *statusServer {
	s := &statusServer{byTo: byTo, texts: map[string]int{}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(raw))
		to := form.Get("To")
		s.mu.Lock()
		code, ok := s.byTo[to]
		if !ok {
			code = http.StatusCreated
		}
		if code == http.StatusCreated {
			s.texts[to]++
		}
		s.mu.Unlock()
		w.WriteHeader(code)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *statusServer) set(to string, code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byTo[to] = code
}

func (s *statusServer) count(to string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.texts[to]
}

func sendWithLedger(t *testing.T, s *statusServer, l plugin.RecipientLedger, attempt int) error {
	t.Helper()
	p := New()
	p.base = s.srv.URL
	return p.Send(context.Background(), plugin.DispatchRequest{
		Channel:    plugin.ChannelRef{Config: config(map[string]any{"to": numA + "," + numB + "," + numC})},
		Event:      sampleEvent(),
		Attempt:    attempt,
		Recipients: l,
	})
}

// The regression: one recipient fails transiently, and the retry must text
// only that recipient — never again the two the provider already accepted.
func TestSend_RetryAfterPartialFailureSkipsDeliveredRecipients(t *testing.T) {
	s := newStatusServer(t, map[string]int{numB: http.StatusServiceUnavailable})
	l := newMemLedger()

	err := sendWithLedger(t, s, l, 1)
	if err == nil || plugin.IsPermanent(err) {
		t.Fatalf("first attempt err = %v, want transient", err)
	}
	if !l.has(numA) || !l.has(numC) || l.has(numB) {
		t.Errorf("ledger = %v, want A and C recorded, B not", l.delivered)
	}

	s.set(numB, http.StatusCreated)
	if err := sendWithLedger(t, s, l, 2); err != nil {
		t.Fatalf("retry err = %v", err)
	}
	for _, n := range []string{numA, numB, numC} {
		if got := s.count(n); got != 1 {
			t.Errorf("%s received %d texts, want exactly 1", n, got)
		}
	}

	// A further redelivery (a Send that succeeded but whose ack was lost)
	// reaches nobody.
	if err := sendWithLedger(t, s, l, 3); err != nil {
		t.Fatalf("redelivery err = %v", err)
	}
	for _, n := range []string{numA, numB, numC} {
		if got := s.count(n); got != 1 {
			t.Errorf("after redelivery %s received %d texts, want 1", n, got)
		}
	}
}

func TestSend_LedgerOutcomes(t *testing.T) {
	cases := []struct {
		name          string
		byTo          map[string]int
		wantErr       bool
		wantPermanent bool
		wantRecorded  []string
	}{
		{"all ok", map[string]int{}, false, false, []string{numA, numB, numC}},
		{"one transient", map[string]int{numA: 503}, true, false, []string{numB, numC}},
		{"all transient", map[string]int{numA: 503, numB: 500, numC: 429}, true, false, nil},
		{"one permanent", map[string]int{numA: 400}, true, true, []string{numB, numC}},
		{"all permanent", map[string]int{numA: 401, numB: 401, numC: 401}, true, true, nil},
		{"mixed permanent and transient", map[string]int{numA: 400, numB: 503}, true, false, []string{numC}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStatusServer(t, tc.byTo)
			l := newMemLedger()
			err := sendWithLedger(t, s, l, 1)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if plugin.IsPermanent(err) != tc.wantPermanent {
				t.Fatalf("IsPermanent(%v) = %v, want %v", err, plugin.IsPermanent(err), tc.wantPermanent)
			}
			want := map[string]bool{}
			for _, n := range tc.wantRecorded {
				want[n] = true
			}
			for _, n := range []string{numA, numB, numC} {
				if l.has(n) != want[n] {
					t.Errorf("recorded[%s] = %v, want %v", n, l.has(n), want[n])
				}
			}
		})
	}
}

// After a partial failure only the unreached recipients are tried again, so
// their classification alone decides the retry: a recipient that is now
// rejected for good ends it, even though an earlier attempt was transient.
func TestSend_RetryClassifiesOnlyRemainingRecipients(t *testing.T) {
	s := newStatusServer(t, map[string]int{numA: 503})
	l := newMemLedger()
	if err := sendWithLedger(t, s, l, 1); err == nil || plugin.IsPermanent(err) {
		t.Fatalf("first attempt err = %v, want transient", err)
	}
	s.set(numA, http.StatusBadRequest)
	err := sendWithLedger(t, s, l, 2)
	if !plugin.IsPermanent(err) {
		t.Fatalf("retry err = %v, want permanent (only the rejected recipient was left)", err)
	}
	if s.count(numB) != 1 || s.count(numC) != 1 {
		t.Fatalf("texts = %v, want B and C once each", s.texts)
	}
}

func TestSend_EveryoneAlreadyDeliveredSendsNothing(t *testing.T) {
	s := newStatusServer(t, map[string]int{})
	l := newMemLedger()
	for _, n := range []string{numA, numB, numC} {
		l.delivered[n] = true
	}
	if err := sendWithLedger(t, s, l, 4); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(s.texts) != 0 {
		t.Fatalf("texts = %v, want none", s.texts)
	}
}

// Unreadable progress must not turn into a re-text of everyone: the send
// fails transiently and the retry tries again.
func TestSend_LedgerLoadErrorRetriesWithoutTexting(t *testing.T) {
	s := newStatusServer(t, map[string]int{})
	l := newMemLedger()
	l.loadErr = context.DeadlineExceeded
	err := sendWithLedger(t, s, l, 2)
	if err == nil || plugin.IsPermanent(err) {
		t.Fatalf("err = %v, want transient", err)
	}
	if len(s.texts) != 0 {
		t.Fatalf("texts = %v, want none", s.texts)
	}
}

// A failed Record does not fail a send that otherwise succeeded (nothing is
// retried), and only rides along in the message when something else failed.
func TestSend_RecordErrorDoesNotChangeOutcome(t *testing.T) {
	s := newStatusServer(t, map[string]int{})
	l := newMemLedger()
	l.recordErr = context.DeadlineExceeded
	if err := sendWithLedger(t, s, l, 1); err != nil {
		t.Fatalf("all delivered, err = %v, want nil", err)
	}

	s = newStatusServer(t, map[string]int{numA: 503})
	err := sendWithLedger(t, s, l, 1)
	if err == nil || plugin.IsPermanent(err) {
		t.Fatalf("err = %v, want transient", err)
	}
	if !strings.Contains(err.Error(), "not recorded") {
		t.Fatalf("err = %v, want the unrecorded deliveries named", err)
	}

	s = newStatusServer(t, map[string]int{numA: 400})
	if err := sendWithLedger(t, s, l, 1); !plugin.IsPermanent(err) {
		t.Fatalf("err = %v, want permanent: a record failure is not a retryable send failure", err)
	}
}
