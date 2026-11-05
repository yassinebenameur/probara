package alertactions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	alertservice "github.com/yassinebenameur/probara/api/internal/services/alerts"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/shared/actionlink"
	"github.com/yassinebenameur/probara/shared/logger"
)

type keyring struct{ keys map[int][]byte }

func (k keyring) CurrentKey() ([]byte, int, error) { return k.keys[1], 1, nil }
func (k keyring) KeyForVersion(v int) ([]byte, error) {
	if key, ok := k.keys[v]; ok {
		return key, nil
	}
	return nil, fmt.Errorf("no key %d", v)
}

// fakeAlerts holds alerts per tenant and acknowledges like the service:
// only an active alert, only in its own tenant.
type fakeAlerts struct {
	mu     sync.Mutex
	alerts map[uuid.UUID]*models.AlertWithDetails
	actors []alertservice.Actor
}

func (f *fakeAlerts) GetAlert(_ context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.alerts[alertID]
	if !ok || a.TenantID != tenantID {
		return nil, errors.New("alert not found")
	}
	cp := *a
	return &cp, nil
}

func (f *fakeAlerts) AcknowledgeAlert(_ context.Context, tenantID, alertID uuid.UUID, actor alertservice.Actor) (*models.AlertWithDetails, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.alerts[alertID]
	if !ok || a.TenantID != tenantID || a.Status != models.AlertStatusActive {
		return nil, errors.New("alert not found or not active")
	}
	now := time.Now()
	a.Status, a.AcknowledgedAt = models.AlertStatusAcknowledged, &now
	via := actor.Via
	a.AcknowledgedVia = &via
	f.actors = append(f.actors, actor)
	cp := *a
	return &cp, nil
}

type fakeScope struct{ dead map[uuid.UUID]bool }

func (s fakeScope) Live(_ context.Context, tenantID, channelID uuid.UUID) (bool, error) {
	return !s.dead[tenantID] && !s.dead[channelID], nil
}

type fakeHub struct{ acked []uuid.UUID }

func (h *fakeHub) BroadcastAlertAcknowledged(_ uuid.UUID, a *models.AlertWithDetails) {
	h.acked = append(h.acked, a.ID)
}

type fakeAudit struct{ events []audit.Event }

func (a *fakeAudit) Record(e audit.Event) { a.events = append(a.events, e) }

type fixture struct {
	signer  *actionlink.Signer
	alerts  *fakeAlerts
	scope   fakeScope
	hub     *fakeHub
	audit   *fakeAudit
	router  http.Handler
	tenant  uuid.UUID
	channel uuid.UUID
	now     time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		alerts:  &fakeAlerts{alerts: map[uuid.UUID]*models.AlertWithDetails{}},
		scope:   fakeScope{dead: map[uuid.UUID]bool{}},
		hub:     &fakeHub{},
		audit:   &fakeAudit{},
		tenant:  uuid.New(),
		channel: uuid.New(),
		now:     time.Now(),
	}
	f.signer = actionlink.New(keyring{keys: map[int][]byte{1: bytes.Repeat([]byte{1}, 32)}}).
		WithClock(func() time.Time { return f.now })
	h := NewHandlers(f.signer, f.alerts, f.scope, f.hub, f.audit, logger.New("test", "error"))
	r := chi.NewRouter()
	r.Get("/api/v1/alert-actions/{token}", h.Describe)
	r.Post("/api/v1/alert-actions/{token}", h.Acknowledge)
	f.router = r
	return f
}

func (f *fixture) addAlert(tenant uuid.UUID, status models.AlertStatus) uuid.UUID {
	id := uuid.New()
	name := "Checkout API"
	f.alerts.alerts[id] = &models.AlertWithDetails{
		Alert:       models.Alert{ID: id, TenantID: tenant, Status: status, Kind: "availability", TriggeredAt: f.now.Add(-time.Minute)},
		MonitorName: &name,
	}
	return id
}

func (f *fixture) token(t *testing.T, tenant, alert, channel uuid.UUID) string {
	t.Helper()
	tok, err := f.signer.ActionToken(actionlink.ActionAcknowledge, tenant.String(), alert.String(), channel.String())
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func (f *fixture) do(t *testing.T, method, token string) (int, Response) {
	t.Helper()
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, httptest.NewRequest(method, "/api/v1/alert-actions/"+token, nil))
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	var resp Response
	if w.Code != http.StatusInternalServerError {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode %s: %v", w.Body, err)
		}
	}
	return w.Code, resp
}

// GET reports and never acts (mail scanners prefetch); POST acknowledges
// as a magic-link actor, broadcasts and audits; a replay reports "already".
func TestGetDescribesPostAcknowledges(t *testing.T) {
	f := newFixture(t)
	alert := f.addAlert(f.tenant, models.AlertStatusActive)
	tok := f.token(t, f.tenant, alert, f.channel)

	for range 3 {
		code, resp := f.do(t, http.MethodGet, tok)
		if code != http.StatusOK || resp.State != StatePending || resp.Alert == nil || resp.Alert.Subject != "Checkout API" {
			t.Fatalf("GET = %d %+v", code, resp)
		}
		if resp.ExpiresAt == nil || !resp.ExpiresAt.After(f.now.Add(71*time.Hour)) {
			t.Fatalf("GET expires_at = %v", resp.ExpiresAt)
		}
	}
	if len(f.alerts.actors) != 0 || len(f.audit.events) != 0 {
		t.Fatal("GET acknowledged or audited")
	}

	code, resp := f.do(t, http.MethodPost, tok)
	if code != http.StatusOK || resp.State != StateAcknowledged || resp.Alert.Status != "acknowledged" {
		t.Fatalf("POST = %d %+v", code, resp)
	}
	if len(f.alerts.actors) != 1 || f.alerts.actors[0].Via != alertservice.ViaMagicLink || f.alerts.actors[0].UserID != nil {
		t.Fatalf("actors = %+v", f.alerts.actors)
	}
	if len(f.hub.acked) != 1 || f.hub.acked[0] != alert {
		t.Fatalf("broadcasts = %v", f.hub.acked)
	}
	if len(f.audit.events) != 1 {
		t.Fatalf("audit events = %d", len(f.audit.events))
	}
	e := f.audit.events[0]
	if e.Action != "alert.acknowledge" || e.Outcome != audit.OutcomeSuccess || e.ResourceID != alert.String() ||
		e.TenantID == nil || *e.TenantID != f.tenant || e.ActorType != audit.ActorAnonymous ||
		e.Details["via"] != "magic_link" || e.Details["channel_id"] != f.channel.String() {
		t.Fatalf("audit event = %+v", e)
	}

	// Replay: the link still works but there is nothing left to do.
	code, resp = f.do(t, http.MethodPost, tok)
	if code != http.StatusConflict || resp.State != StateAlready {
		t.Fatalf("replayed POST = %d %+v", code, resp)
	}
	if len(f.alerts.actors) != 1 {
		t.Fatal("replay acknowledged twice")
	}
	if code, resp := f.do(t, http.MethodGet, tok); code != http.StatusOK || resp.State != StateAlready {
		t.Fatalf("GET after ack = %d %+v", code, resp)
	}
}

func TestResolvedAlert(t *testing.T) {
	f := newFixture(t)
	alert := f.addAlert(f.tenant, models.AlertStatusResolved)
	tok := f.token(t, f.tenant, alert, f.channel)
	if code, resp := f.do(t, http.MethodGet, tok); code != http.StatusOK || resp.State != StateResolved {
		t.Fatalf("GET = %d %+v", code, resp)
	}
	if code, resp := f.do(t, http.MethodPost, tok); code != http.StatusConflict || resp.State != StateResolved {
		t.Fatalf("POST = %d %+v", code, resp)
	}
	if len(f.alerts.actors) != 0 {
		t.Fatal("acknowledged a resolved alert")
	}
	if len(f.audit.events) != 1 || f.audit.events[0].Outcome != audit.OutcomeFailure || f.audit.events[0].Details["reason"] != StateResolved {
		t.Fatalf("audit = %+v", f.audit.events)
	}
}

func TestExpiredToken(t *testing.T) {
	f := newFixture(t)
	alert := f.addAlert(f.tenant, models.AlertStatusActive)
	tok := f.token(t, f.tenant, alert, f.channel)
	f.now = f.now.Add(actionlink.DefaultTTL + time.Second)

	if code, resp := f.do(t, http.MethodGet, tok); code != http.StatusGone || resp.State != StateExpired || resp.Alert != nil {
		t.Fatalf("GET = %d %+v", code, resp)
	}
	if code, resp := f.do(t, http.MethodPost, tok); code != http.StatusGone || resp.State != StateExpired {
		t.Fatalf("POST = %d %+v", code, resp)
	}
	if len(f.alerts.actors) != 0 {
		t.Fatal("an expired link acknowledged")
	}
	// Authentic but expired: attributed to its workspace.
	if len(f.audit.events) != 1 || *f.audit.events[0].TenantID != f.tenant || f.audit.events[0].StatusCode != http.StatusGone {
		t.Fatalf("audit = %+v", f.audit.events)
	}
}

func TestInvalidTokens(t *testing.T) {
	f := newFixture(t)
	alert := f.addAlert(f.tenant, models.AlertStatusActive)
	good := f.token(t, f.tenant, alert, f.channel)
	forged := []byte(good)
	forged[len(forged)-3] ^= 0x01

	for name, tok := range map[string]string{
		"garbage": "not-a-token",
		"forged":  string(forged),
	} {
		code, resp := f.do(t, http.MethodPost, tok)
		if code != http.StatusNotFound || resp.State != StateInvalid || resp.Alert != nil || resp.ExpiresAt != nil {
			t.Fatalf("%s: POST = %d %+v", name, code, resp)
		}
	}
	if len(f.audit.events) != 0 {
		t.Fatal("an unauthentic token was audited against a workspace it cannot prove")
	}
	if len(f.alerts.actors) != 0 {
		t.Fatal("an invalid token acknowledged")
	}
}

// A token is bound to its tenant: one naming another workspace's alert
// finds nothing, and never acknowledges it.
func TestCrossTenantToken(t *testing.T) {
	f := newFixture(t)
	otherTenant := uuid.New()
	theirs := f.addAlert(otherTenant, models.AlertStatusActive)
	tok := f.token(t, f.tenant, theirs, f.channel)
	if code, resp := f.do(t, http.MethodPost, tok); code != http.StatusNotFound || resp.State != StateInvalid {
		t.Fatalf("POST = %d %+v", code, resp)
	}
	if f.alerts.alerts[theirs].Status != models.AlertStatusActive {
		t.Fatal("cross-tenant token acknowledged another workspace's alert")
	}
}

func TestRevokedByChannelOrWorkspace(t *testing.T) {
	f := newFixture(t)
	alert := f.addAlert(f.tenant, models.AlertStatusActive)
	tok := f.token(t, f.tenant, alert, f.channel)

	f.scope.dead[f.channel] = true
	if code, resp := f.do(t, http.MethodPost, tok); code != http.StatusNotFound || resp.State != StateInvalid {
		t.Fatalf("deleted channel: POST = %d %+v", code, resp)
	}
	delete(f.scope.dead, f.channel)
	f.scope.dead[f.tenant] = true
	if code, resp := f.do(t, http.MethodGet, tok); code != http.StatusNotFound || resp.State != StateInvalid {
		t.Fatalf("deleted workspace: GET = %d %+v", code, resp)
	}
	if len(f.alerts.actors) != 0 {
		t.Fatal("a revoked link acknowledged")
	}
}

// Without PROBARA_SECRETS_KEY there is no signer: nothing verifies.
func TestNoSignerRejectsEverything(t *testing.T) {
	f := newFixture(t)
	alert := f.addAlert(f.tenant, models.AlertStatusActive)
	tok := f.token(t, f.tenant, alert, f.channel)
	var none *actionlink.Signer
	h := NewHandlers(none, f.alerts, f.scope, f.hub, f.audit, logger.New("test", "error"))
	r := chi.NewRouter()
	r.Post("/api/v1/alert-actions/{token}", h.Acknowledge)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/alert-actions/"+tok, nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
}
