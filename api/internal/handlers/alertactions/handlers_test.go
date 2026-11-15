package alertactions

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	apimiddleware "github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	alertservice "github.com/yassinebenameur/probara/api/internal/services/alerts"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/shared/actionlink"
	"github.com/yassinebenameur/probara/shared/logger"
)

type keyring map[int][]byte

func (k keyring) CurrentKey() ([]byte, int, error) { return k[1], 1, nil }
func (k keyring) KeyForVersion(v int) ([]byte, error) {
	if key, ok := k[v]; ok {
		return key, nil
	}
	return nil, fmt.Errorf("no key %d", v)
}

var (
	signer   = actionlink.New(keyring{1: bytes.Repeat([]byte{7}, 32)})
	tenantA  = uuid.MustParse("8f14e45f-ceea-467a-9f0b-2c1a3b4d5e6f")
	tenantB  = uuid.MustParse("11111111-2222-4333-8444-555555555555")
	alertID  = uuid.MustParse("b17c2e90-4a1d-4f3e-9c88-71f0a2d5c311")
	channel  = uuid.MustParse("3f2a91cc-11de-4b7a-9a2e-6c5f8d0e1234")
	monitorN = "Checkout API"
)

// fakeAlerts is one alert in tenantA.
type fakeAlerts struct {
	status models.AlertStatus
	acks   int
	ackErr error
	panic  bool
}

func (f *fakeAlerts) GetAlert(_ context.Context, tenantID, id uuid.UUID) (*models.AlertWithDetails, error) {
	if f.panic {
		panic("boom")
	}
	if tenantID != tenantA || id != alertID {
		return nil, alertservice.ErrAlertNotFound
	}
	a := &models.AlertWithDetails{MonitorName: &monitorN}
	a.ID, a.TenantID, a.Status, a.Kind = alertID, tenantA, f.status, "availability"
	a.TriggeredAt = time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	return a, nil
}

func (f *fakeAlerts) AcknowledgeAlert(ctx context.Context, tenantID, id uuid.UUID) (*models.AlertWithDetails, error) {
	f.acks++
	if f.ackErr != nil {
		return nil, f.ackErr
	}
	if tenantID != tenantA || id != alertID || f.status != models.AlertStatusActive {
		return nil, alertservice.ErrAlertNotActive
	}
	f.status = models.AlertStatusAcknowledged
	return f.GetAlert(ctx, tenantID, id)
}

type fakeChannels struct {
	gone  bool
	calls int
}

func (f *fakeChannels) ChannelName(_ context.Context, tenantID, channelID uuid.UUID) (string, bool, error) {
	f.calls++
	if f.gone || tenantID != tenantA || channelID != channel {
		return "", false, nil
	}
	return "On-call email", true, nil
}

type fakeHub struct{ acked int }

func (f *fakeHub) BroadcastAlertAcknowledged(uuid.UUID, *models.AlertWithDetails) { f.acked++ }

type fakeAudit struct{ events []audit.Event }

func (f *fakeAudit) Record(e audit.Event) { f.events = append(f.events, e) }

type harness struct {
	alerts   *fakeAlerts
	channels *fakeChannels
	hub      *fakeHub
	audit    *fakeAudit
	router   http.Handler
	logs     *bytes.Buffer
}

// newHarness mounts the routes behind the API's own request-ID, recovery,
// logging and metrics middleware, so a test sees exactly what they record.
func newHarness(status models.AlertStatus) *harness {
	h := &harness{
		alerts:   &fakeAlerts{status: status},
		channels: &fakeChannels{},
		hub:      &fakeHub{},
		audit:    &fakeAudit{},
		logs:     &bytes.Buffer{},
	}
	log := logger.New("api-test", "debug")
	log.SetOutput(h.logs)
	r := chi.NewRouter()
	r.Use(apimiddleware.RequestIDMiddleware)
	r.Use(apimiddleware.RecoveryMiddleware(log))
	r.Use(apimiddleware.LoggingMiddleware(log))
	r.Use(apimiddleware.MetricsMiddleware("api-test"))
	handlers := NewHandlers(signer, h.alerts, h.channels, h.hub, h.audit, log)
	r.Route("/api/v1/alert-actions", handlers.Routes)
	h.router = r
	return h
}

func (h *harness) do(t *testing.T, method, token string) (int, Response) {
	t.Helper()
	req := httptest.NewRequest(method, "/api/v1/alert-actions/acknowledge", nil)
	if token != "" {
		req.Header.Set(TokenHeader, token)
	}
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	if w.Header().Get("Cache-Control") != "no-store" && w.Code != http.StatusInternalServerError {
		t.Fatalf("missing Cache-Control: no-store")
	}
	var resp Response
	if w.Code != http.StatusInternalServerError {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("body %q: %v", w.Body.String(), err)
		}
	}
	return w.Code, resp
}

func tokenFor(t *testing.T, tenant, alert uuid.UUID, issued time.Time) string {
	t.Helper()
	tok, err := signer.ActionToken(actionlink.ActionAcknowledge, tenant.String(), alert.String(), channel.String(), issued)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestGetDescribesAndNeverAcknowledges(t *testing.T) {
	h := newHarness(models.AlertStatusActive)
	tok := tokenFor(t, tenantA, alertID, time.Now())
	for i := 0; i < 3; i++ {
		code, resp := h.do(t, http.MethodGet, tok)
		if code != http.StatusOK || resp.State != StatePending {
			t.Fatalf("GET = %d %+v", code, resp)
		}
		if resp.Alert == nil || resp.Alert.Subject != monitorN || resp.Alert.Label != "Alert Triggered" || resp.Alert.Status != "active" || resp.ExpiresAt == nil {
			t.Fatalf("summary = %+v", resp)
		}
	}
	if h.alerts.acks != 0 || h.alerts.status != models.AlertStatusActive || len(h.audit.events) != 0 || h.hub.acked != 0 {
		t.Fatalf("GET acted: acks=%d status=%s audit=%d hub=%d", h.alerts.acks, h.alerts.status, len(h.audit.events), h.hub.acked)
	}
}

func TestPostAcknowledgesOnceAndIsIdempotent(t *testing.T) {
	h := newHarness(models.AlertStatusActive)
	tok := tokenFor(t, tenantA, alertID, time.Now())

	code, resp := h.do(t, http.MethodPost, tok)
	if code != http.StatusOK || resp.State != StateAcknowledged || resp.Alert.Status != "acknowledged" {
		t.Fatalf("first POST = %d %+v", code, resp)
	}
	code, resp = h.do(t, http.MethodPost, tok)
	if code != http.StatusOK || resp.State != StateAlready {
		t.Fatalf("replayed POST = %d %+v, want 200 already", code, resp)
	}
	if code, resp := h.do(t, http.MethodGet, tok); code != http.StatusOK || resp.State != StateAlready {
		t.Fatalf("GET after ack = %d %+v", code, resp)
	}
	if h.alerts.acks != 1 || h.hub.acked != 1 {
		t.Fatalf("acks = %d, broadcasts = %d; want exactly one each", h.alerts.acks, h.hub.acked)
	}

	// Both POSTs are audited against the alert, attributed to the channel's
	// link; only the first succeeded.
	if len(h.audit.events) != 2 {
		t.Fatalf("audit events = %d, want 2", len(h.audit.events))
	}
	ok, replay := h.audit.events[0], h.audit.events[1]
	if ok.Action != "alert.acknowledge" || ok.ResourceType != "alert" || ok.ResourceID != alertID.String() ||
		ok.TenantID == nil || *ok.TenantID != tenantA || ok.Outcome != audit.OutcomeSuccess ||
		ok.ActorType != audit.ActorAnonymous || ok.ActorLabel != "link sent to On-call email" ||
		ok.Details["channel_id"] != channel.String() || ok.Details["channel_name"] != "On-call email" || ok.Details["via"] != ViaLink {
		t.Fatalf("success audit = %+v", ok)
	}
	if replay.Outcome != audit.OutcomeFailure || replay.Details["reason"] != StateAlready {
		t.Fatalf("replay audit = %+v", replay)
	}
}

func TestResolvedAlert(t *testing.T) {
	h := newHarness(models.AlertStatusResolved)
	tok := tokenFor(t, tenantA, alertID, time.Now())
	if code, resp := h.do(t, http.MethodGet, tok); code != http.StatusOK || resp.State != StateResolved {
		t.Fatalf("GET = %d %+v", code, resp)
	}
	if code, resp := h.do(t, http.MethodPost, tok); code != http.StatusConflict || resp.State != StateResolved {
		t.Fatalf("POST = %d %+v", code, resp)
	}
	if h.alerts.acks != 0 {
		t.Fatal("tried to acknowledge a resolved alert")
	}
}

// Losing a race to another acknowledge reports the alert's state now.
func TestRaceReportsCurrentState(t *testing.T) {
	h := newHarness(models.AlertStatusActive)
	h.alerts.ackErr = alertservice.ErrAlertNotActive
	tok := tokenFor(t, tenantA, alertID, time.Now())
	h.alerts.status = models.AlertStatusActive
	code, resp := h.do(t, http.MethodPost, tok)
	if code != http.StatusOK || resp.State != StatePending && resp.State != StateAlready {
		t.Fatalf("POST after a lost race = %d %+v", code, resp)
	}
	if h.hub.acked != 0 {
		t.Fatal("broadcast an acknowledgement this request did not make")
	}
}

func TestExpired(t *testing.T) {
	h := newHarness(models.AlertStatusActive)
	tok := tokenFor(t, tenantA, alertID, time.Now().Add(-actionlink.TTL-time.Minute))
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		code, resp := h.do(t, method, tok)
		if code != http.StatusGone || resp.State != StateExpired || resp.Alert != nil || resp.ExpiresAt != nil {
			t.Fatalf("%s = %d %+v; want 410 with no alert details", method, code, resp)
		}
	}
	if h.alerts.acks != 0 || h.channels.calls != 0 {
		t.Fatal("an expired token reached the database")
	}
}

// Unknown, tampered, wrong-action and wrong-tenant tokens, a deleted
// channel and a missing header all answer the same 404 body.
func TestInvalidIsUniform(t *testing.T) {
	good := tokenFor(t, tenantA, alertID, time.Now())
	raw, _ := base64.RawURLEncoding.DecodeString(good)
	raw[20] ^= 1
	tampered := base64.RawURLEncoding.EncodeToString(raw)
	otherKey, _ := actionlink.New(keyring{1: bytes.Repeat([]byte{9}, 32)}).
		ActionToken(actionlink.ActionAcknowledge, tenantA.String(), alertID.String(), channel.String(), time.Now())

	cases := map[string]struct {
		token       string
		channelGone bool
		reachesDB   bool
	}{
		"missing":           {token: ""},
		"garbage":           {token: "not-a-token"},
		"tampered":          {token: tampered},
		"another key":       {token: otherKey},
		"another tenant":    {token: tokenFor(t, tenantB, alertID, time.Now()), reachesDB: true},
		"unknown alert":     {token: tokenFor(t, tenantA, uuid.New(), time.Now()), reachesDB: true},
		"deleted channel":   {token: good, channelGone: true, reachesDB: true},
		"whitespace padded": {token: "   "},
	}
	var first []byte
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(models.AlertStatusActive)
			h.channels.gone = tc.channelGone
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				req := httptest.NewRequest(method, "/api/v1/alert-actions/acknowledge", nil)
				if tc.token != "" {
					req.Header.Set(TokenHeader, tc.token)
				}
				w := httptest.NewRecorder()
				h.router.ServeHTTP(w, req)
				if w.Code != http.StatusNotFound {
					t.Fatalf("%s = %d %s", method, w.Code, w.Body.String())
				}
				if first == nil {
					first = bytes.Clone(w.Body.Bytes())
				}
				if !bytes.Equal(w.Body.Bytes(), first) {
					t.Fatalf("%s body %q differs from %q", method, w.Body.String(), first)
				}
			}
			if h.alerts.acks != 0 || h.alerts.status != models.AlertStatusActive {
				t.Fatal("an invalid token acknowledged")
			}
			if !tc.reachesDB && h.channels.calls != 0 {
				t.Fatal("an unauthenticated token reached the database")
			}
			// Tokens that do not verify name no trustworthy tenant.
			if !tc.reachesDB && len(h.audit.events) != 0 {
				t.Fatalf("audited a forged token: %+v", h.audit.events)
			}
		})
	}
	if string(first) != `{"state":"invalid"}`+"\n" {
		t.Fatalf("invalid body = %q", first)
	}
}

// The token never appears in the request log, the panic log or a metric
// label, on success, refusal or panic.
func TestTokenStaysOutOfLogsAndMetrics(t *testing.T) {
	tok := tokenFor(t, tenantA, alertID, time.Now())
	expired := tokenFor(t, tenantA, alertID, time.Now().Add(-100*time.Hour))
	h := newHarness(models.AlertStatusActive)
	for _, token := range []string{tok, expired, "forged-" + tok[:20]} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			h.do(t, method, token)
		}
	}
	panicking := newHarness(models.AlertStatusActive)
	panicking.alerts.panic = true
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alert-actions/acknowledge", nil)
	req.Header.Set(TokenHeader, tok)
	w := httptest.NewRecorder()
	panicking.router.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("panic answered %d", w.Code)
	}

	logs := h.logs.String() + panicking.logs.String()
	if !strings.Contains(logs, "HTTP request") || !strings.Contains(panicking.logs.String(), "Panic recovered") {
		t.Fatalf("the middleware did not log; the check would prove nothing:\n%s", logs)
	}
	for _, secret := range []string{tok, expired, tok[:20]} {
		if strings.Contains(logs, secret) {
			t.Fatalf("token in logs:\n%s", logs)
		}
	}

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	sawRoute := false
	for _, f := range families {
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if strings.Contains(l.GetValue(), tok[:20]) || strings.Contains(l.GetValue(), expired[:20]) {
					t.Fatalf("token in metric %s label %s=%q", f.GetName(), l.GetName(), l.GetValue())
				}
				if l.GetValue() == "/api/v1/alert-actions/acknowledge" {
					sawRoute = true
				}
			}
		}
	}
	if !sawRoute {
		t.Fatal("no metric recorded for the route; the check would prove nothing")
	}
}

// Without PROBARA_SECRETS_KEY every token is invalid.
func TestNoKeyMeansInvalid(t *testing.T) {
	var none *actionlink.Signer
	h := NewHandlers(none, &fakeAlerts{status: models.AlertStatusActive}, &fakeChannels{}, nil, nil, nil)
	r := chi.NewRouter()
	r.Route("/api/v1/alert-actions", h.Routes)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alert-actions/acknowledge", nil)
	req.Header.Set(TokenHeader, tokenFor(t, tenantA, alertID, time.Now()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("no key: %d %s", w.Code, w.Body.String())
	}
}

// A database failure answers 500 and is audited for an authentic POST.
func TestInternalError(t *testing.T) {
	h := newHarness(models.AlertStatusActive)
	h.alerts.ackErr = errors.New("connection refused")
	code, _ := h.do(t, http.MethodPost, tokenFor(t, tenantA, alertID, time.Now()))
	if code != http.StatusInternalServerError {
		t.Fatalf("code = %d", code)
	}
	if len(h.audit.events) != 1 || h.audit.events[0].Details["reason"] != "internal_error" {
		t.Fatalf("audit = %+v", h.audit.events)
	}
}
