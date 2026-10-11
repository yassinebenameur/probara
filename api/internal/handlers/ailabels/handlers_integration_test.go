package ailabels

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apimiddleware "github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/services/ailabels"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/api/internal/services/depsuggest"
	"github.com/yassinebenameur/probara/shared/ai"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/testutil"
)

type noAI struct{}

func (noAI) EffectiveConfig(context.Context, uuid.UUID) (ai.Config, bool, error) {
	return ai.Config{}, false, nil
}

// identity mirrors what AuthMiddleware injects per credential.
func identity(tenant uuid.UUID, kind string) func(context.Context) context.Context {
	return func(ctx context.Context) context.Context {
		ctx = ctxpkg.WithTenantID(ctx, tenant.String())
		switch kind {
		case "superadmin":
			ctx = ctxpkg.WithAdminID(ctx, "00000000-0000-0000-0000-00000000000a")
			ctx = ctxpkg.WithPlatformRole(ctx, "superadmin")
			return ctxpkg.WithRole(ctx, "admin")
		case "admin", "editor", "viewer":
			ctx = ctxpkg.WithAdminID(ctx, "00000000-0000-0000-0000-00000000000b")
			ctx = ctxpkg.WithPlatformRole(ctx, "member")
			return ctxpkg.WithRole(ctx, kind)
		case "write-key", "read-key":
			ctx = ctxpkg.WithActorType(ctx, ctxpkg.ActorTypeAPIKey)
			ctx = ctxpkg.WithAPIKeyID(ctx, "00000000-0000-0000-0000-00000000000c")
			return ctxpkg.WithAuthScope(ctx, strings.TrimSuffix(kind, "-key"))
		}
		return ctx
	}
}

type ctxKey struct{}

// router mirrors server.go: identity -> AuditMutations -> RequireWrite ->
// routes, with RequireTenantAdmin on /ai-labels.
func router(h *Handlers, recorder *audit.Recorder) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if f, ok := req.Context().Value(ctxKey{}).(func(context.Context) context.Context); ok {
				req = req.WithContext(f(req.Context()))
			}
			next.ServeHTTP(w, req)
		})
	})
	r.Use(apimiddleware.AuditMutations(recorder))
	r.Use(apimiddleware.RequireWrite)
	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/monitors/dependency-suggestions/label", h.LabelDependencySuggestion)
		r.Post("/incidents/{id}/ai-analysis/label", h.LabelIncidentAnalysis)
		r.Post("/alerts/{id}/triage/label", h.LabelAlertTriage)
		r.Route("/ai-labels", func(r chi.Router) {
			r.Use(apimiddleware.RequireTenantAdmin)
			r.Post("/export", h.Export)
		})
	})
	return r
}

func TestLabelRoutesAuthorizationAndAudit(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "label routes")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := dbc.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	monitor := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "checkout-api")
	upstream := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "checkout-db")
	exec(`UPDATE monitors SET tags=ARRAY['service:checkout'] WHERE id=ANY($1)`, "{"+monitor.String()+","+upstream.String()+"}")
	incident := uuid.New()
	exec(`INSERT INTO incidents(id,tenant_id,title) VALUES($1,$2,'outage')`, incident, tenant)
	ev, _ := json.Marshal(ai.MonitorEvidence{Checks: 10, Failures: 4, DNSFailures: 4})
	snap, _ := json.Marshal(ai.DecisionSnapshot{V: 1, Catalogue: ai.CatalogueVersion, Hypothesis: "dns_failure",
		Model:     ai.ModelIdentity{Engine: "laya-cpu", Version: "probara-laya-v1", SHA256: strings.Repeat("f", 64), Catalogue: ai.CatalogueVersion},
		Decisions: []ai.SnapshotDecision{{ID: uuid.NewString(), Task: "rca", MonitorRef: monitor.String(), Evidence: ev, Choices: ai.Choices("rca"), Predicted: "dns_failure", Final: "dns_failure"}}})
	exec(`INSERT INTO incident_ai_analyses(id,tenant_id,incident_id,status,metadata,decision_snapshot) VALUES($1,$2,$3,'ready','{"engine":"laya-cpu","outcome":"supported"}',$4)`, uuid.New(), tenant, incident, snap)

	log := logger.New("test", "error")
	recorder := audit.NewRecorder(dbc, log)
	recorder.Start()
	h := NewHandlers(ailabels.NewService(dbc, depsuggest.NewService(dbc, noAI{})), log)
	h.now = func() time.Time { return time.Date(2026, 10, 11, 23, 30, 0, 0, time.FixedZone("x", -5*3600)) }
	srv := router(h, recorder)

	incidentPath := "/api/v1/incidents/" + incident.String() + "/ai-analysis/label"
	pairPath := "/api/v1/monitors/dependency-suggestions/label"
	exportPath := "/api/v1/ai-labels/export"
	incidentBody := `{"verdict":"confirmed"}`
	pairBody := `{"monitor_id":"` + monitor.String() + `","depends_on_id":"` + upstream.String() + `","verdict":"rejected"}`
	do := func(kind, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, identity(tenant, kind)))
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w
	}
	labelCount := func() int {
		var n int
		_ = dbc.QueryRowContext(ctx, `SELECT COUNT(*) FROM ai_labels`).Scan(&n)
		return n
	}

	for _, c := range []struct {
		kind, path, body string
		want             int
	}{
		{"viewer", incidentPath, incidentBody, http.StatusForbidden},
		{"viewer", pairPath, pairBody, http.StatusForbidden},
		{"read-key", incidentPath, incidentBody, http.StatusForbidden},
		{"read-key", pairPath, pairBody, http.StatusForbidden},
		{"editor", exportPath, ``, http.StatusForbidden},
		{"write-key", exportPath, ``, http.StatusForbidden},
		{"viewer", exportPath, ``, http.StatusForbidden},
	} {
		if w := do(c.kind, c.path, c.body); w.Code != c.want {
			t.Fatalf("%s %s: %d %s", c.kind, c.path, w.Code, w.Body)
		}
	}
	if n := labelCount(); n != 0 {
		t.Fatalf("denied requests wrote %d labels", n)
	}

	// Client evidence is never accepted.
	if w := do("editor", incidentPath, `{"verdict":"confirmed","evidence":{"checks":1}}`); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "validation_error") {
		t.Fatalf("evidence field: %d %s", w.Code, w.Body)
	}
	if w := do("editor", incidentPath, `{"verdict":"corrected","incident_label":"made_up"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad label: %d %s", w.Code, w.Body)
	}
	if w := do("editor", "/api/v1/incidents/"+uuid.NewString()+"/ai-analysis/label", incidentBody); w.Code != http.StatusNotFound {
		t.Fatalf("missing incident: %d %s", w.Code, w.Body)
	}

	w := do("editor", incidentPath, incidentBody)
	if w.Code != http.StatusOK {
		t.Fatalf("editor label: %d %s", w.Code, w.Body)
	}
	var label map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &label)
	if label["subject_type"] != "incident_analysis" || label["incident_label"] != "dns_failure" || label["labeled_by"] != "00000000-0000-0000-0000-00000000000b" || label["catalogue_version"] != ai.CatalogueVersion {
		t.Fatalf("label response %s", w.Body)
	}
	// No co-firing, but a shared service tag and name token keep the pair a
	// candidate, so a write key can record the rejection.
	w = do("write-key", pairPath, pairBody)
	if w.Code != http.StatusOK {
		t.Fatalf("write key pair label: %d %s", w.Code, w.Body)
	}
	label = map[string]any{}
	_ = json.Unmarshal(w.Body.Bytes(), &label)
	if label["subject_type"] != "dependency_pair" || label["labeled_by"] != nil || label["subject_id"] != nil || label["model_version"] != nil {
		t.Fatalf("pair label response %s", w.Body)
	}
	if w := do("editor", pairPath, `{"monitor_id":"`+monitor.String()+`","depends_on_id":"`+uuid.NewString()+`","verdict":"confirmed"}`); w.Code != http.StatusNotFound {
		t.Fatalf("unknown monitor: %d %s", w.Code, w.Body)
	}

	for _, kind := range []string{"admin", "superadmin"} {
		w = do(kind, exportPath, ``)
		if w.Code != http.StatusOK {
			t.Fatalf("%s export: %d %s", kind, w.Code, w.Body)
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/x-ndjson" {
			t.Fatalf("content type %q", ct)
		}
		if cd := w.Header().Get("Content-Disposition"); cd != `attachment; filename="probara-ai-labels-20261012.jsonl"` {
			t.Fatalf("content disposition %q", cd)
		}
		if w.Header().Get("X-Probara-Export-Rows") != "2" || w.Header().Get("X-Probara-Export-Skipped") != "0" {
			t.Fatalf("count headers %v", w.Header())
		}
		if lines := bytes.Count(w.Body.Bytes(), []byte("\n")); lines != 2 {
			t.Fatalf("%d lines", lines)
		}
	}
	if w := do("admin", exportPath, `{}`); w.Code != http.StatusOK {
		t.Fatalf("empty object body: %d", w.Code)
	}
	if w := do("admin", exportPath, `{"since":"2026-11-01T00:00:00Z","until":"2026-10-01T00:00:00Z"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("inverted window: %d %s", w.Code, w.Body)
	}
	if w := do("admin", exportPath, `{"subject_types":["alert_triage"]}`); w.Code != http.StatusOK {
		t.Fatalf("alert_triage subject: %d %s", w.Code, w.Body)
	}
	if w := do("admin", exportPath, `{"subject_types":["incident"]}`); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown subject: %d %s", w.Code, w.Body)
	}

	recorder.Stop()
	var denied, exported int
	if err := dbc.QueryRowContext(ctx, `SELECT
		COUNT(*) FILTER (WHERE action IN ('incident.label','monitor.label') AND outcome='denied'),
		COUNT(*) FILTER (WHERE action='ai-label.export' AND outcome='success')
		FROM audit_log WHERE tenant_id=$1`, tenant).Scan(&denied, &exported); err != nil {
		t.Fatal(err)
	}
	if denied != 4 || exported != 4 {
		t.Fatalf("audit: denied=%d exported=%d", denied, exported)
	}
}

func TestAlertTriageLabelRoute(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "triage label route")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := dbc.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	openAlert := func(name string) uuid.UUID {
		t.Helper()
		monitor := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, name)
		id := uuid.New()
		exec(`INSERT INTO alerts (id, tenant_id, monitor_id, status, triggered_at, failure_count, created_at, updated_at)
			VALUES ($1,$2,$3,'active',NOW(),1,NOW(),NOW())`, id, tenant, monitor)
		return id
	}
	monitorAlert := openAlert("checkout-api")
	var monitor uuid.UUID
	_ = dbc.QueryRowContext(ctx, `SELECT monitor_id FROM alerts WHERE id=$1`, monitorAlert).Scan(&monitor)
	ev, _ := json.Marshal(ai.MonitorEvidence{Checks: 10, Failures: 4, DNSFailures: 4})
	snap, _ := json.Marshal(ai.DecisionSnapshot{V: 1, Catalogue: ai.CatalogueVersion, Hypothesis: "dns_failure",
		Model:     ai.ModelIdentity{Engine: "laya-cpu", Version: "probara-laya-v1", SHA256: strings.Repeat("f", 64), Catalogue: ai.CatalogueVersion},
		Decisions: []ai.SnapshotDecision{{ID: uuid.NewString(), Task: "rca", MonitorRef: monitor.String(), Evidence: ev, Choices: ai.Choices("rca"), Predicted: "dns_failure", Final: "dns_failure"}}})
	triage := uuid.New()
	exec(`INSERT INTO alert_triage (id, tenant_id, alert_id, status, hypothesis, decision_snapshot, completed_at)
		VALUES ($1,$2,$3,'ready','dns_failure',$4,NOW())`, triage, tenant, monitorAlert, snap)
	pendingAlert := openAlert("billing")
	exec(`INSERT INTO alert_triage (tenant_id, alert_id, status) VALUES ($1,$2,'pending')`, tenant, pendingAlert)
	noSnapAlert := openAlert("search")
	exec(`INSERT INTO alert_triage (tenant_id, alert_id, status, completed_at) VALUES ($1,$2,'ready',NOW())`, tenant, noSnapAlert)

	log := logger.New("test", "error")
	recorder := audit.NewRecorder(dbc, log)
	recorder.Start()
	srv := router(NewHandlers(ailabels.NewService(dbc, depsuggest.NewService(dbc, noAI{})), log), recorder)
	path := func(alert string) string { return "/api/v1/alerts/" + alert + "/triage/label" }
	do := func(kind, p, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, p, strings.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, identity(tenant, kind)))
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w
	}
	confirm := `{"verdict":"confirmed"}`

	for _, c := range []struct {
		kind, path, body string
		want             int
		code             string
	}{
		{"viewer", path(monitorAlert.String()), confirm, http.StatusForbidden, ""},
		{"read-key", path(monitorAlert.String()), confirm, http.StatusForbidden, ""},
		{"editor", path(monitorAlert.String()), `{"verdict":"confirmed","evidence":{"checks":1}}`, http.StatusBadRequest, "validation_error"},
		{"editor", path(monitorAlert.String()), `{"verdict":"confirmed"} {}`, http.StatusBadRequest, "validation_error"},
		{"editor", path(monitorAlert.String()), `{"verdict":"maybe"}`, http.StatusBadRequest, "validation_error"},
		{"editor", path(monitorAlert.String()), `{"verdict":"corrected","label":"made_up"}`, http.StatusBadRequest, "validation_error"},
		{"editor", path("not-a-uuid"), confirm, http.StatusBadRequest, "validation_error"},
		{"editor", path(uuid.NewString()), confirm, http.StatusNotFound, ""},
		{"editor", path(pendingAlert.String()), confirm, http.StatusConflict, "triage_not_ready"},
		{"editor", path(openAlert("no-row").String()), confirm, http.StatusConflict, "triage_not_ready"},
		{"editor", path(noSnapAlert.String()), confirm, http.StatusConflict, "triage_not_labelable"},
	} {
		w := do(c.kind, c.path, c.body)
		if w.Code != c.want || !strings.Contains(w.Body.String(), c.code) {
			t.Fatalf("%s %s %s: %d %s", c.kind, c.path, c.body, w.Code, w.Body)
		}
	}
	var n int
	_ = dbc.QueryRowContext(ctx, `SELECT COUNT(*) FROM ai_labels`).Scan(&n)
	if n != 0 {
		t.Fatalf("rejected requests wrote %d labels", n)
	}

	w := do("editor", path(monitorAlert.String()), `{"verdict":"corrected","label":"connection_failure"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("editor label: %d %s", w.Code, w.Body)
	}
	var label map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &label)
	if label["subject_type"] != "alert_triage" || label["subject_id"] != triage.String() || label["incident_label"] != "connection_failure" ||
		label["labeled_by"] != "00000000-0000-0000-0000-00000000000b" {
		t.Fatalf("label response %s", w.Body)
	}
	if w := do("write-key", path(monitorAlert.String()), confirm); w.Code != http.StatusOK {
		t.Fatalf("write key relabel: %d %s", w.Code, w.Body)
	}

	recorder.Stop()
	var denied, ok int
	var resourceID string
	if err := dbc.QueryRowContext(ctx, `SELECT
		COUNT(*) FILTER (WHERE outcome='denied'),
		COUNT(*) FILTER (WHERE outcome='success'),
		COALESCE(MAX(resource_id) FILTER (WHERE outcome='success' AND resource_type='alert'), '')
		FROM audit_log WHERE tenant_id=$1 AND action='alert.label'`, tenant).Scan(&denied, &ok, &resourceID); err != nil {
		t.Fatal(err)
	}
	if denied != 2 || ok != 2 || resourceID != monitorAlert.String() {
		t.Fatalf("audit: denied=%d success=%d resource=%q", denied, ok, resourceID)
	}
}
