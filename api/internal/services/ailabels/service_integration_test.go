package ailabels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/depsuggest"
	"github.com/yassinebenameur/probara/shared/ai"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

type fixture struct {
	t   *testing.T
	ctx context.Context
	db  *shareddb.Client
	svc *Service
	dep *depsuggest.Service
}

type disabledAI struct{}

func (disabledAI) EffectiveConfig(context.Context, uuid.UUID) (ai.Config, bool, error) {
	return ai.Config{}, false, nil // labels must not need local AI
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	dep := depsuggest.NewService(dbc, disabledAI{})
	return &fixture{t: t, ctx: ctx, db: dbc, svc: NewService(dbc, dep), dep: dep}
}

func (f *fixture) exec(q string, args ...any) {
	f.t.Helper()
	if _, err := f.db.ExecContext(f.ctx, q, args...); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) incident(tenant uuid.UUID) uuid.UUID {
	f.t.Helper()
	id := uuid.New()
	f.exec(`INSERT INTO incidents(id,tenant_id,title) VALUES($1,$2,'outage')`, id, tenant)
	return id
}

// testSnapshot is what the worker stores for a two-monitor DNS incident: m1
// has DNS evidence, m2 only connection failures (its dns answer was
// downgraded by the guard).
func testSnapshot(m1, m2 uuid.UUID) ai.DecisionSnapshot {
	ev := func(e ai.MonitorEvidence) json.RawMessage { b, _ := json.Marshal(e); return b }
	return ai.DecisionSnapshot{
		V: 1, Catalogue: ai.CatalogueVersion,
		Model:      ai.ModelIdentity{Engine: "laya-cpu", Version: "probara-laya-v1", SHA256: strings.Repeat("c", 64), Catalogue: ai.CatalogueVersion},
		Thresholds: map[string]float64{"rca": .6}, Hypothesis: "dns_failure", Score: .9,
		Decisions: []ai.SnapshotDecision{
			{ID: uuid.NewString(), Task: "rca", MonitorRef: m1.String(), Evidence: ev(ai.MonitorEvidence{Checks: 10, Failures: 4, DNSFailures: 4}), Choices: ai.Choices("rca"),
				Probabilities: map[string]float64{"dns_failure": .9, "insufficient_evidence": .1}, Predicted: "dns_failure", Final: "dns_failure"},
			{ID: uuid.NewString(), Task: "rca", MonitorRef: m2.String(), Evidence: ev(ai.MonitorEvidence{Checks: 10, Failures: 4, ConnectionFailures: 4}), Choices: ai.Choices("rca"),
				Probabilities: map[string]float64{"dns_failure": .9, "insufficient_evidence": .1}, Predicted: "dns_failure", Final: "insufficient_evidence"},
		},
	}
}

func (f *fixture) analysis(tenant, incident uuid.UUID, status string, snap *ai.DecisionSnapshot, age time.Duration) uuid.UUID {
	f.t.Helper()
	id := uuid.New()
	var b []byte
	if snap != nil {
		b, _ = json.Marshal(snap)
	}
	f.exec(`INSERT INTO incident_ai_analyses(id,tenant_id,incident_id,status,metadata,decision_snapshot,created_at)
		VALUES($1,$2,$3,$4,'{"engine":"laya-cpu","outcome":"supported"}',$5,NOW()-$6::interval)`,
		id, tenant, incident, status, nullable(b), age.String())
	return id
}

func nullable(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func (f *fixture) column(q string, args ...any) []byte {
	f.t.Helper()
	var b []byte
	if err := f.db.QueryRowContext(f.ctx, q, args...).Scan(&b); err != nil {
		f.t.Fatal(err)
	}
	return b
}

func TestLabelIncidentAnalysis(t *testing.T) {
	f := newFixture(t)
	tenant := testutil.InsertTenant(f.ctx, t, f.db, "labels")
	other := testutil.InsertTenant(f.ctx, t, f.db, "other")
	m1 := testutil.InsertHTTPMonitor(f.ctx, t, f.db, tenant, "api-gateway")
	m2 := testutil.InsertHTTPMonitor(f.ctx, t, f.db, tenant, "billing")
	incident := f.incident(tenant)
	snap := testSnapshot(m1, m2)
	older := f.analysis(tenant, incident, "ready", &snap, 2*time.Hour)
	newest := f.analysis(tenant, incident, "ready", &snap, time.Hour)
	f.analysis(tenant, incident, "pending", nil, 0) // newer, but not ready
	alice, bob := uuid.New(), uuid.New()

	label, err := f.svc.LabelIncidentAnalysis(f.ctx, tenant, incident, models.IncidentAILabelRequest{Verdict: "confirmed"}, &alice)
	if err != nil {
		t.Fatal(err)
	}
	if label.SubjectID == nil || *label.SubjectID != newest || label.IncidentLabel != "dns_failure" || label.ModelVersion != "probara-laya-v1" || *label.LabeledBy != alice {
		t.Fatalf("default target %+v", label)
	}
	if label.Answers[snap.Decisions[1].ID] != (ai.LabelAnswer{Label: "insufficient_evidence", Source: "confirmed"}) {
		t.Fatalf("answers %+v", label.Answers)
	}
	var id1 uuid.UUID
	var created1, updated1 time.Time
	if err := f.db.QueryRowContext(f.ctx, `SELECT id,created_at,updated_at FROM ai_labels WHERE subject_key=$1`, newest.String()).Scan(&id1, &created1, &updated1); err != nil {
		t.Fatal(err)
	}
	// request is the stored snapshot without predictions, copied server-side.
	var request ai.DecisionSnapshot
	if err := json.Unmarshal(f.column(`SELECT request FROM ai_labels WHERE id=$1`, id1), &request); err != nil {
		t.Fatal(err)
	}
	want := snap.WithoutPredictions()
	for i := range want.Decisions {
		if !jsonEqual(request.Decisions[i].Evidence, want.Decisions[i].Evidence) {
			t.Fatalf("evidence %s", request.Decisions[i].Evidence)
		}
		request.Decisions[i].Evidence, want.Decisions[i].Evidence = nil, nil
	}
	if !reflect.DeepEqual(request, want) {
		t.Fatalf("request\n got %+v\nwant %+v", request, want)
	}
	var predicted ai.LabelPredictions
	_ = json.Unmarshal(f.column(`SELECT predicted FROM ai_labels WHERE id=$1`, id1), &predicted)
	if predicted.Hypothesis != "dns_failure" || predicted.Decisions[snap.Decisions[1].ID].Final != "insufficient_evidence" || predicted.Decisions[snap.Decisions[0].ID].Probabilities["dns_failure"] != .9 {
		t.Fatalf("predicted %+v", predicted)
	}

	// Latest wins: same row id and created_at, replaced verdict, answers, user.
	time.Sleep(10 * time.Millisecond)
	label, err = f.svc.LabelIncidentAnalysis(f.ctx, tenant, incident, models.IncidentAILabelRequest{
		AnalysisID: newest.String(), Verdict: "corrected", IncidentLabel: "connection_failure",
		Answers: map[string]string{snap.Decisions[0].ID: "dns_failure"},
	}, &bob)
	if err != nil {
		t.Fatal(err)
	}
	var id2 uuid.UUID
	var created2, updated2 time.Time
	var verdict string
	var labeledBy uuid.UUID
	if err := f.db.QueryRowContext(f.ctx, `SELECT id,created_at,updated_at,verdict,labeled_by FROM ai_labels WHERE subject_key=$1`, newest.String()).Scan(&id2, &created2, &updated2, &verdict, &labeledBy); err != nil {
		t.Fatal(err)
	}
	if id2 != id1 || !created2.Equal(created1) || !updated2.After(updated1) || verdict != "corrected" || labeledBy != bob {
		t.Fatalf("upsert: id %v/%v created %v/%v updated %v/%v verdict %s by %v", id1, id2, created1, created2, updated1, updated2, verdict, labeledBy)
	}
	wantAnswers := map[string]ai.LabelAnswer{
		snap.Decisions[0].ID: {Label: "dns_failure", Source: "operator"},
		snap.Decisions[1].ID: {Label: "connection_failure", Source: "propagated"},
	}
	if !reflect.DeepEqual(label.Answers, wantAnswers) || !label.LabeledAt.Equal(updated2) {
		t.Fatalf("relabel %+v", label)
	}
	var count int
	_ = f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM ai_labels`).Scan(&count)
	if count != 1 {
		t.Fatalf("%d label rows", count)
	}

	// Explicit analysis id targets an older run; a nil labeler (API key) is NULL.
	label, err = f.svc.LabelIncidentAnalysis(f.ctx, tenant, incident, models.IncidentAILabelRequest{AnalysisID: older.String(), Verdict: "confirmed"}, nil)
	if err != nil || *label.SubjectID != older || label.LabeledBy != nil {
		t.Fatalf("explicit target %+v %v", label, err)
	}

	// Errors.
	pending := f.analysis(tenant, incident, "pending", nil, 0)
	failed := f.analysis(tenant, incident, "failed", nil, 0)
	legacy := uuid.New()
	f.exec(`INSERT INTO incident_ai_analyses(id,tenant_id,incident_id,status,summary,metadata) VALUES($1,$2,$3,'ready','x','{"engine":"legacy-llm","outcome":"legacy"}')`, legacy, tenant, incident)
	otherIncident := f.incident(tenant)
	foreignAnalysis := f.analysis(tenant, otherIncident, "ready", &snap, 0)
	foreignIncident := f.incident(other)
	f.analysis(other, foreignIncident, "ready", &snap, 0)
	noReady := f.incident(tenant)
	f.analysis(tenant, noReady, "failed", nil, 0)
	for name, c := range map[string]struct {
		incident uuid.UUID
		req      models.IncidentAILabelRequest
		want     error
	}{
		"pending":              {incident, models.IncidentAILabelRequest{AnalysisID: pending.String(), Verdict: "confirmed"}, ErrAnalysisNotReady},
		"failed":               {incident, models.IncidentAILabelRequest{AnalysisID: failed.String(), Verdict: "confirmed"}, ErrAnalysisNotReady},
		"no ready analysis":    {noReady, models.IncidentAILabelRequest{Verdict: "confirmed"}, ErrAnalysisNotReady},
		"legacy newest ready":  {incident, models.IncidentAILabelRequest{Verdict: "confirmed"}, ErrAnalysisNotLabelable},
		"legacy":               {incident, models.IncidentAILabelRequest{AnalysisID: legacy.String(), Verdict: "confirmed"}, ErrAnalysisNotLabelable},
		"other tenant":         {foreignIncident, models.IncidentAILabelRequest{Verdict: "confirmed"}, ErrNotFound},
		"analysis of another":  {incident, models.IncidentAILabelRequest{AnalysisID: foreignAnalysis.String(), Verdict: "confirmed"}, ErrNotFound},
		"unknown analysis":     {incident, models.IncidentAILabelRequest{AnalysisID: uuid.NewString(), Verdict: "confirmed"}, ErrNotFound},
		"missing incident":     {uuid.New(), models.IncidentAILabelRequest{Verdict: "confirmed"}, ErrNotFound},
		"confirmed mismatched": {incident, models.IncidentAILabelRequest{AnalysisID: newest.String(), Verdict: "confirmed", IncidentLabel: "tls_problem"}, &ai.LabelValidationError{}},
		"bad verdict":          {incident, models.IncidentAILabelRequest{Verdict: "rejected"}, &ValidationError{}},
		"bad analysis id":      {incident, models.IncidentAILabelRequest{AnalysisID: "nope", Verdict: "confirmed"}, &ValidationError{}},
		"unknown decision":     {incident, models.IncidentAILabelRequest{AnalysisID: newest.String(), Verdict: "corrected", IncidentLabel: "dns_failure", Answers: map[string]string{"x": "dns_failure"}}, &ai.LabelValidationError{}},
	} {
		_, err := f.svc.LabelIncidentAnalysis(f.ctx, tenant, c.incident, c.req, nil)
		if !matches(err, c.want) {
			t.Errorf("%s: got %v, want %T %v", name, err, c.want, c.want)
		}
	}
}

func matches(err, want error) bool {
	switch want.(type) {
	case *ai.LabelValidationError:
		var ve *ai.LabelValidationError
		return errors.As(err, &ve)
	case *ValidationError:
		var ve *ValidationError
		return errors.As(err, &ve)
	}
	return errors.Is(err, want)
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func jsonEqual(a, b []byte) bool {
	var x, y any
	_ = json.Unmarshal(a, &x)
	_ = json.Unmarshal(b, &y)
	return reflect.DeepEqual(x, y)
}

// seedCoFiring gives (earlier, later) three co-firing windows with earlier
// leading by 30s, like depsuggest's own integration test.
func (f *fixture) seedCoFiring(tenant, earlier, later uuid.UUID) {
	anchor := time.Now().UTC().Truncate(10 * time.Minute).Add(-2 * time.Hour)
	for i := 0; i < 3; i++ {
		at := anchor.Add(time.Duration(i) * 10 * time.Minute)
		for _, v := range []struct {
			id uuid.UUID
			at time.Time
		}{{earlier, at}, {later, at.Add(30 * time.Second)}} {
			f.exec(`INSERT INTO alerts(id,tenant_id,monitor_id,triggered_at,resolved_at,failure_count,status,kind) VALUES($1,$2,$3,$4,$4::timestamptz+INTERVAL '1 minute',1,'resolved','availability')`, uuid.New(), tenant, v.id, v.at)
		}
	}
}

// wireCapture is a local AI service that records the dependency decisions
// Suggest sends and abstains on all of them.
func wireCapture(t *testing.T, sent *[]map[string]json.RawMessage) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Decisions []map[string]json.RawMessage `json:"decisions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		*sent = append(*sent, req.Decisions...)
		out := ai.BatchResult{Model: ai.ModelIdentity{Engine: "laya-cpu", Version: "v", SHA256: strings.Repeat("d", 64), Catalogue: ai.CatalogueVersion}, Thresholds: map[string]float64{"dependency": .9}}
		for _, d := range req.Decisions {
			var id string
			var choices map[string]string
			_ = json.Unmarshal(d["id"], &id)
			_ = json.Unmarshal(d["choices"], &choices)
			p := map[string]float64{}
			for k := range choices {
				p[k] = 0
			}
			p["insufficient_evidence"] = 1
			out.Results = append(out.Results, ai.DecisionResult{ID: id, Probabilities: p})
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
}

type enabledAI struct{ url string }

func (e enabledAI) EffectiveConfig(context.Context, uuid.UUID) (ai.Config, bool, error) {
	return ai.Config{BaseURL: e.url, APIKey: "test-internal-token"}, true, nil
}

func TestLabelDependencySuggestion(t *testing.T) {
	f := newFixture(t)
	tenant := testutil.InsertTenant(f.ctx, t, f.db, "pairs")
	other := testutil.InsertTenant(f.ctx, t, f.db, "other")
	db := testutil.InsertHTTPMonitor(f.ctx, t, f.db, tenant, "checkout-db")
	api := testutil.InsertHTTPMonitor(f.ctx, t, f.db, tenant, "checkout-api")
	lonely := testutil.InsertHTTPMonitor(f.ctx, t, f.db, tenant, "zz-unrelated")
	disabled := testutil.InsertHTTPMonitor(f.ctx, t, f.db, tenant, "checkout-worker")
	foreign := testutil.InsertHTTPMonitor(f.ctx, t, f.db, other, "checkout-foreign")
	f.exec(`UPDATE monitors SET tags=ARRAY['service:checkout'] WHERE id=ANY($1)`, "{"+db.String()+","+api.String()+","+disabled.String()+"}")
	f.exec(`UPDATE monitors SET enabled=FALSE WHERE id=$1`, disabled)
	f.seedCoFiring(tenant, db, api)

	// What Suggest sends for this pair: the only candidate in the workspace.
	var sent []map[string]json.RawMessage
	srv := wireCapture(t, &sent)
	defer srv.Close()
	if _, err := depsuggest.NewService(f.db, enabledAI{srv.URL}).Suggest(f.ctx, tenant); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 {
		t.Fatalf("expected exactly one candidate pair on the wire, got %d", len(sent))
	}

	user := uuid.New()
	label, err := f.svc.LabelDependencySuggestion(f.ctx, tenant, models.DependencyLabelRequest{MonitorID: api.String(), DependsOnID: db.String(), Verdict: "confirmed"}, &user)
	if err != nil {
		t.Fatal(err)
	}
	if label.SubjectType != "dependency_pair" || label.SubjectID != nil || label.IncidentLabel != "" || label.ModelVersion != "" || len(label.Answers) != 1 {
		t.Fatalf("%+v", label)
	}
	var request ai.DecisionSnapshot
	_ = json.Unmarshal(f.column(`SELECT request FROM ai_labels WHERE tenant_id=$1`, tenant), &request)
	d := request.Decisions[0]
	if !jsonEqual(d.Evidence, sent[0]["evidence"]) || !jsonEqual(mustJSON(d.Choices), sent[0]["choices"]) || d.Task != "dependency" {
		t.Fatalf("label evidence %s, Suggest sent %s", d.Evidence, sent[0]["evidence"])
	}
	var got ai.PairEvidence
	_ = json.Unmarshal(d.Evidence, &got)
	// db failed first: a positive lead when db is A, negative when it is B.
	if got.CoFires != 3 || (d.Pair.A == db.String()) != (got.ALeadsSeconds > 0) || (d.Pair.A != db.String() && d.Pair.A != api.String()) {
		t.Fatalf("orientation %+v %+v", got, d.Pair)
	}
	// api depends on db; db failed first. Whatever the orientation, the answer
	// must name api as the dependent.
	wantLabel := "a_depends_b"
	if d.Pair.A != api.String() {
		wantLabel = "b_depends_a"
	}
	if label.Answers[d.ID] != (ai.LabelAnswer{Label: wantLabel, Source: "operator"}) {
		t.Fatalf("answer %+v want %s", label.Answers, wantLabel)
	}
	if b := f.column(`SELECT predicted FROM ai_labels WHERE tenant_id=$1`, tenant); b != nil {
		t.Fatalf("pair predicted = %s, want NULL", b)
	}

	// Either request orientation hits the same subject (latest wins).
	if _, err := f.svc.LabelDependencySuggestion(f.ctx, tenant, models.DependencyLabelRequest{MonitorID: db.String(), DependsOnID: api.String(), Verdict: "rejected", Corrected: "reversed"}, &user); err != nil {
		t.Fatal(err)
	}
	var count int
	var key, verdict string
	_ = f.db.QueryRowContext(f.ctx, `SELECT COUNT(*), MIN(subject_key), MIN(verdict) FROM ai_labels WHERE tenant_id=$1`, tenant).Scan(&count, &key, &verdict)
	lo, hi := db.String(), api.String()
	if lo > hi {
		lo, hi = hi, lo
	}
	if count != 1 || key != lo+":"+hi || verdict != "rejected" {
		t.Fatalf("count=%d key=%s verdict=%s", count, key, verdict)
	}

	for name, c := range map[string]struct {
		req  models.DependencyLabelRequest
		want error
	}{
		"cross-tenant monitor": {models.DependencyLabelRequest{MonitorID: api.String(), DependsOnID: foreign.String(), Verdict: "confirmed"}, ErrNotFound},
		"unknown monitor":      {models.DependencyLabelRequest{MonitorID: api.String(), DependsOnID: uuid.NewString(), Verdict: "confirmed"}, ErrNotFound},
		"non-candidate":        {models.DependencyLabelRequest{MonitorID: api.String(), DependsOnID: lonely.String(), Verdict: "rejected"}, ErrPairUnavailable},
		"disabled":             {models.DependencyLabelRequest{MonitorID: api.String(), DependsOnID: disabled.String(), Verdict: "rejected"}, ErrPairUnavailable},
		"same monitor":         {models.DependencyLabelRequest{MonitorID: api.String(), DependsOnID: api.String(), Verdict: "confirmed"}, &ValidationError{}},
		"bad uuid":             {models.DependencyLabelRequest{MonitorID: "x", DependsOnID: api.String(), Verdict: "confirmed"}, &ValidationError{}},
		"corrected+confirmed":  {models.DependencyLabelRequest{MonitorID: api.String(), DependsOnID: db.String(), Verdict: "confirmed", Corrected: "reversed"}, &ai.LabelValidationError{}},
		"bad corrected":        {models.DependencyLabelRequest{MonitorID: api.String(), DependsOnID: db.String(), Verdict: "rejected", Corrected: "maybe"}, &ai.LabelValidationError{}},
		"bad verdict":          {models.DependencyLabelRequest{MonitorID: api.String(), DependsOnID: db.String(), Verdict: "corrected"}, &ai.LabelValidationError{}},
	} {
		if _, err := f.svc.LabelDependencySuggestion(f.ctx, tenant, c.req, nil); !matches(err, c.want) {
			t.Errorf("%s: got %v, want %v", name, err, c.want)
		}
	}
	// Soft-deleted monitors are not live.
	f.exec(`UPDATE monitors SET deleted_at=NOW() WHERE id=$1`, db)
	if _, err := f.svc.LabelDependencySuggestion(f.ctx, tenant, models.DependencyLabelRequest{MonitorID: api.String(), DependsOnID: db.String(), Verdict: "confirmed"}, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("soft-deleted monitor: %v", err)
	}
}

func exportLines(t *testing.T, r *ExportResult) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(r.Body), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal(line, &row); err != nil {
			t.Fatal(err)
		}
		out = append(out, row)
	}
	return out
}

func TestExportIsolationRetentionAndSalt(t *testing.T) {
	f := newFixture(t)
	tenant := testutil.InsertTenant(f.ctx, t, f.db, "exporter")
	other := testutil.InsertTenant(f.ctx, t, f.db, "other")
	m1 := testutil.InsertHTTPMonitor(f.ctx, t, f.db, tenant, "api-gateway")
	m2 := testutil.InsertHTTPMonitor(f.ctx, t, f.db, tenant, "billing")
	o1 := testutil.InsertHTTPMonitor(f.ctx, t, f.db, other, "other-1")
	incident := f.incident(tenant)
	snap := testSnapshot(m1, m2)
	analysis := f.analysis(tenant, incident, "ready", &snap, 0)
	if _, err := f.svc.LabelIncidentAnalysis(f.ctx, tenant, incident, models.IncidentAILabelRequest{Verdict: "confirmed"}, nil); err != nil {
		t.Fatal(err)
	}
	otherIncident := f.incident(other)
	otherSnap := testSnapshot(o1, o1)
	f.analysis(other, otherIncident, "ready", &otherSnap, 0)
	if _, err := f.svc.LabelIncidentAnalysis(f.ctx, other, otherIncident, models.IncidentAILabelRequest{Verdict: "corrected", IncidentLabel: "connection_failure"}, nil); err != nil {
		t.Fatal(err)
	}

	first, err := f.svc.Export(f.ctx, tenant, models.AILabelExportFilter{})
	if err != nil {
		t.Fatal(err)
	}
	rows := exportLines(t, first)
	if first.Rows != 2 || first.Skipped != 0 || len(rows) != 2 {
		t.Fatalf("rows=%d skipped=%d", first.Rows, first.Skipped)
	}
	for _, r := range rows {
		if r["expected"].(map[string]any)["rca"] == "connection_failure" {
			t.Fatal("another tenant's label exported")
		}
	}
	for _, id := range []uuid.UUID{tenant, incident, analysis, m1, m2} {
		if bytes.Contains(first.Body, []byte(id.String())) {
			t.Fatalf("identifier %s leaked", id)
		}
	}

	// Purging monitors and deleting the incident keeps labels exportable, with
	// stable group and row ids (same salt).
	f.exec(`DELETE FROM monitors WHERE id=ANY($1)`, "{"+m1.String()+","+m2.String()+"}")
	f.exec(`DELETE FROM incidents WHERE id=$1`, incident)
	second, err := f.svc.Export(f.ctx, tenant, models.AILabelExportFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Body, second.Body) {
		t.Fatalf("export changed after purge:\n%s\n%s", first.Body, second.Body)
	}

	// Window filtering is half-open on updated_at.
	var updated time.Time
	_ = f.db.QueryRowContext(f.ctx, `SELECT updated_at FROM ai_labels WHERE tenant_id=$1`, tenant).Scan(&updated)
	after := updated.Add(time.Microsecond)
	for _, c := range []struct {
		filter models.AILabelExportFilter
		rows   int
	}{
		{models.AILabelExportFilter{Since: &updated}, 2},
		{models.AILabelExportFilter{Until: &updated}, 0},
		{models.AILabelExportFilter{Since: &after}, 0},
		{models.AILabelExportFilter{Until: &after}, 2},
		{models.AILabelExportFilter{SubjectTypes: []string{"dependency_pair"}}, 0},
		{models.AILabelExportFilter{SubjectTypes: []string{"incident_analysis"}}, 2},
	} {
		r, err := f.svc.Export(f.ctx, tenant, c.filter)
		if err != nil || r.Rows != c.rows {
			t.Fatalf("filter %+v: %+v %v", c.filter, r, err)
		}
	}
	var ve *ValidationError
	if _, err := f.svc.Export(f.ctx, tenant, models.AILabelExportFilter{Since: &after, Until: &updated}); !errors.As(err, &ve) {
		t.Fatalf("since >= until: %v", err)
	}
	if _, err := f.svc.Export(f.ctx, tenant, models.AILabelExportFilter{SubjectTypes: []string{"alert_triage"}}); !errors.As(err, &ve) {
		t.Fatalf("unknown subject: %v", err)
	}

	// Export cap.
	f.svc.exportCap = 0
	if _, err := f.svc.Export(f.ctx, tenant, models.AILabelExportFilter{}); !errors.Is(err, ErrExportTooLarge) {
		t.Fatalf("cap: %v", err)
	}
	f.svc.exportCap = DefaultExportCap

	// Tenant delete cascades labels and salt.
	var salts int
	_ = f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM ai_label_salts WHERE tenant_id=$1`, tenant).Scan(&salts)
	if salts != 1 {
		t.Fatalf("salts=%d", salts)
	}
	f.exec(`DELETE FROM tenants WHERE id=$1`, tenant)
	var labels int
	_ = f.db.QueryRowContext(f.ctx, `SELECT (SELECT COUNT(*) FROM ai_labels WHERE tenant_id=$1)+(SELECT COUNT(*) FROM ai_label_salts WHERE tenant_id=$1)`, tenant).Scan(&labels)
	if labels != 0 {
		t.Fatalf("%d label/salt rows survived tenant delete", labels)
	}
}

// The export bound counts training rows, not labels: one incident label
// carries a decision per affected monitor (the worker sends up to ~100).
func TestExportRowCapCountsDecisions(t *testing.T) {
	f := newFixture(t)
	tenant := testutil.InsertTenant(f.ctx, t, f.db, "wide")
	other := testutil.InsertTenant(f.ctx, t, f.db, "other")
	incident := f.incident(tenant)
	snap := testSnapshot(uuid.New(), uuid.New())
	base := snap.Decisions[0]
	snap.Decisions = nil
	for i := 0; i < 101; i++ {
		d := base
		d.ID, d.MonitorRef = uuid.NewString(), uuid.NewString()
		snap.Decisions = append(snap.Decisions, d)
	}
	f.analysis(tenant, incident, "ready", &snap, 0)
	if _, err := f.svc.LabelIncidentAnalysis(f.ctx, tenant, incident, models.IncidentAILabelRequest{Verdict: "confirmed"}, nil); err != nil {
		t.Fatal(err)
	}
	// Another tenant's wide label must not count against this tenant.
	otherIncident := f.incident(other)
	f.analysis(other, otherIncident, "ready", &snap, 0)
	if _, err := f.svc.LabelIncidentAnalysis(f.ctx, other, otherIncident, models.IncidentAILabelRequest{Verdict: "confirmed"}, nil); err != nil {
		t.Fatal(err)
	}

	f.svc.exportRowCap = 101
	r, err := f.svc.Export(f.ctx, tenant, models.AILabelExportFilter{})
	if err != nil || r.Rows != 101 || len(exportLines(t, r)) != 101 {
		t.Fatalf("at the row cap: %+v %v", r, err)
	}
	f.svc.exportRowCap = 100 // one label, well under the label cap
	if _, err := f.svc.Export(f.ctx, tenant, models.AILabelExportFilter{}); !errors.Is(err, ErrExportTooLarge) {
		t.Fatalf("row cap: %v", err)
	}
	// A narrowed window that excludes the label fits again.
	future := time.Now().Add(time.Hour)
	if r, err := f.svc.Export(f.ctx, tenant, models.AILabelExportFilter{Since: &future}); err != nil || r.Rows != 0 {
		t.Fatalf("narrowed window: %+v %v", r, err)
	}
}

func TestLoadIncidentContext(t *testing.T) {
	f := newFixture(t)
	tenant := testutil.InsertTenant(f.ctx, t, f.db, "context")
	other := testutil.InsertTenant(f.ctx, t, f.db, "other")
	mine := testutil.InsertHTTPMonitor(f.ctx, t, f.db, tenant, "api-gateway")
	theirs := testutil.InsertHTTPMonitor(f.ctx, t, f.db, other, "secret-name")
	incident := f.incident(tenant)
	snap := testSnapshot(mine, theirs)
	analysis := f.analysis(tenant, incident, "ready", &snap, 0)
	raw, _ := json.Marshal(snap)

	ctx, err := LoadIncidentContext(f.ctx, f.db, tenant, analysis, raw)
	if err != nil || ctx == nil {
		t.Fatalf("%+v %v", ctx, err)
	}
	if ctx.Predicted != "dns_failure" || ctx.CatalogueVersion != ai.CatalogueVersion || ctx.Label != nil {
		t.Fatalf("%+v", ctx)
	}
	if len(ctx.Choices) != len(ai.Choices("rca")) || ctx.Choices[len(ctx.Choices)-1].Key != "insufficient_evidence" {
		t.Fatalf("choices %+v", ctx.Choices)
	}
	if ctx.Decisions[0].MonitorName == nil || *ctx.Decisions[0].MonitorName != "api-gateway" || ctx.Decisions[1].MonitorName != nil || *ctx.Decisions[1].MonitorID != theirs {
		t.Fatalf("names must be tenant-scoped: %+v", ctx.Decisions)
	}
	if ctx.Decisions[1].Predicted != "insufficient_evidence" {
		t.Fatalf("decision predicted is the final answer: %+v", ctx.Decisions[1])
	}
	// Soft-deleted monitors keep their name.
	f.exec(`UPDATE monitors SET deleted_at=NOW() WHERE id=$1`, mine)
	user := uuid.New()
	if _, err := f.svc.LabelIncidentAnalysis(f.ctx, tenant, incident, models.IncidentAILabelRequest{Verdict: "confirmed"}, &user); err != nil {
		t.Fatal(err)
	}
	ctx, err = LoadIncidentContext(f.ctx, f.db, tenant, analysis, raw)
	if err != nil || ctx.Label == nil || ctx.Label.Verdict != "confirmed" || *ctx.Label.LabeledBy != user || *ctx.Decisions[0].MonitorName != "api-gateway" {
		t.Fatalf("%+v %v", ctx, err)
	}
	// Another tenant never sees this label.
	if c, _ := LoadIncidentContext(f.ctx, f.db, other, analysis, raw); c == nil || c.Label != nil {
		t.Fatalf("label crossed tenants: %+v", c)
	}
	if c, err := LoadIncidentContext(f.ctx, f.db, tenant, analysis, nil); c != nil || err != nil {
		t.Fatal("no snapshot must mean no context")
	}
}
