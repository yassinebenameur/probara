package ailabels

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// alert opens an alert on monitor.
func (f *fixture) alert(tenant, monitor uuid.UUID) uuid.UUID {
	f.t.Helper()
	id := uuid.New()
	f.exec(`INSERT INTO alerts (id, tenant_id, monitor_id, status, triggered_at, failure_count, created_at, updated_at)
		VALUES ($1,$2,$3,'active',NOW()-INTERVAL '5 minutes',1,NOW(),NOW())`, id, tenant, monitor)
	return id
}

// triage writes an alert_triage row as the worker leaves it.
func (f *fixture) triage(tenant, alert uuid.UUID, status string, snap *ai.DecisionSnapshot) uuid.UUID {
	f.t.Helper()
	id := uuid.New()
	var b []byte
	if snap != nil {
		b, _ = json.Marshal(snap)
	}
	completed := "NOW()"
	if status == "pending" {
		completed = "NULL"
	}
	var skip any
	if status == "skipped" {
		skip = "resolved"
	}
	f.exec(`INSERT INTO alert_triage (id, tenant_id, alert_id, status, skip_reason, hypothesis, decision_snapshot, completed_at)
		VALUES ($1,$2,$3,$4,$5,'dns_failure',$6,`+completed+`)`, id, tenant, alert, status, skip, nullable(b))
	return id
}

func TestLabelAlertTriage(t *testing.T) {
	f := newFixture(t)
	tenant := testutil.InsertTenant(f.ctx, t, f.db, "triage labels")
	other := testutil.InsertTenant(f.ctx, t, f.db, "other")
	m1 := testutil.InsertHTTPMonitor(f.ctx, t, f.db, tenant, "api-gateway")
	snap := testSnapshot(m1, m1)
	snap.Decisions = snap.Decisions[:1]
	alert := f.alert(tenant, m1)
	triage := f.triage(tenant, alert, "ready", &snap)
	alice := uuid.New()

	label, err := f.svc.LabelAlertTriage(f.ctx, tenant, alert, models.AlertTriageLabelRequest{Verdict: "confirmed"}, &alice)
	if err != nil {
		t.Fatal(err)
	}
	if label.SubjectType != "alert_triage" || label.SubjectID == nil || *label.SubjectID != triage || label.IncidentLabel != "dns_failure" ||
		label.ModelVersion != "probara-laya-v1" || *label.LabeledBy != alice {
		t.Fatalf("confirm %+v", label)
	}
	var (
		subjectKey string
		incidentID *uuid.UUID
		reqRaw     []byte
		predRaw    []byte
	)
	if err := f.db.QueryRowContext(f.ctx, `SELECT subject_key, incident_id, request, predicted FROM ai_labels WHERE tenant_id=$1`, tenant).
		Scan(&subjectKey, &incidentID, &reqRaw, &predRaw); err != nil {
		t.Fatal(err)
	}
	if subjectKey != triage.String() || incidentID != nil {
		t.Fatalf("subject %s incident %v", subjectKey, incidentID)
	}
	// request is the stored snapshot without predictions, copied server-side.
	var request ai.DecisionSnapshot
	_ = json.Unmarshal(reqRaw, &request)
	want := snap.WithoutPredictions()
	if !jsonEqual(request.Decisions[0].Evidence, want.Decisions[0].Evidence) {
		t.Fatalf("evidence %s", request.Decisions[0].Evidence)
	}
	request.Decisions[0].Evidence, want.Decisions[0].Evidence = nil, nil
	if !reflect.DeepEqual(request, want) {
		t.Fatalf("request\n got %+v\nwant %+v", request, want)
	}
	var predicted ai.LabelPredictions
	_ = json.Unmarshal(predRaw, &predicted)
	if predicted.Hypothesis != "dns_failure" || predicted.Decisions[snap.Decisions[0].ID].Final != "dns_failure" {
		t.Fatalf("predicted %+v", predicted)
	}

	// Re-label upserts the same row; per-decision answers follow
	// ai.ResolveIncidentAnswers (the alert-level correction propagates).
	time.Sleep(5 * time.Millisecond)
	label, err = f.svc.LabelAlertTriage(f.ctx, tenant, alert, models.AlertTriageLabelRequest{Verdict: "corrected", Label: "connection_failure"}, nil)
	if err != nil || label.IncidentLabel != "connection_failure" || label.Verdict != "corrected" || label.LabeledBy != nil ||
		label.Answers[snap.Decisions[0].ID].Source != "propagated" {
		t.Fatalf("correct %+v %v", label, err)
	}
	var count int
	_ = f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM ai_labels`).Scan(&count)
	if count != 1 {
		t.Fatalf("%d label rows after re-label", count)
	}

	// Errors.
	m2 := testutil.InsertHTTPMonitor(f.ctx, t, f.db, tenant, "billing")
	m3 := testutil.InsertHTTPMonitor(f.ctx, t, f.db, tenant, "search")
	m4 := testutil.InsertHTTPMonitor(f.ctx, t, f.db, tenant, "auth")
	m5 := testutil.InsertHTTPMonitor(f.ctx, t, f.db, tenant, "cdn")
	m6 := testutil.InsertHTTPMonitor(f.ctx, t, f.db, tenant, "queue")
	noRow := f.alert(tenant, m2)
	pending := f.alert(tenant, m3)
	f.triage(tenant, pending, "pending", nil)
	failed := f.alert(tenant, m4)
	f.triage(tenant, failed, "failed", nil)
	noSnap := f.alert(tenant, m5)
	f.triage(tenant, noSnap, "ready", nil)
	emptySnap := f.alert(tenant, m6)
	empty := ai.DecisionSnapshot{V: 1, Catalogue: ai.CatalogueVersion, Decisions: []ai.SnapshotDecision{}}
	f.triage(tenant, emptySnap, "ready", &empty)
	o1 := testutil.InsertHTTPMonitor(f.ctx, t, f.db, other, "other")
	foreign := f.alert(other, o1)
	f.triage(other, foreign, "ready", &snap)
	for name, c := range map[string]struct {
		alert uuid.UUID
		req   models.AlertTriageLabelRequest
		want  error
	}{
		"no triage row":       {noRow, models.AlertTriageLabelRequest{Verdict: "confirmed"}, ErrTriageNotReady},
		"pending":             {pending, models.AlertTriageLabelRequest{Verdict: "confirmed"}, ErrTriageNotReady},
		"failed":              {failed, models.AlertTriageLabelRequest{Verdict: "confirmed"}, ErrTriageNotReady},
		"no snapshot":         {noSnap, models.AlertTriageLabelRequest{Verdict: "confirmed"}, ErrTriageNotLabelable},
		"empty decisions":     {emptySnap, models.AlertTriageLabelRequest{Verdict: "confirmed"}, ErrTriageNotLabelable},
		"other tenant alert":  {foreign, models.AlertTriageLabelRequest{Verdict: "confirmed"}, ErrNotFound},
		"missing alert":       {uuid.New(), models.AlertTriageLabelRequest{Verdict: "confirmed"}, ErrNotFound},
		"bad verdict":         {alert, models.AlertTriageLabelRequest{Verdict: "rejected"}, &ValidationError{}},
		"label not in choice": {alert, models.AlertTriageLabelRequest{Verdict: "corrected", Label: "made_up"}, &ai.LabelValidationError{}},
		"confirm mismatched":  {alert, models.AlertTriageLabelRequest{Verdict: "confirmed", Label: "tls_problem"}, &ai.LabelValidationError{}},
		"unknown decision":    {alert, models.AlertTriageLabelRequest{Verdict: "corrected", Label: "dns_failure", Answers: map[string]string{"x": "dns_failure"}}, &ai.LabelValidationError{}},
	} {
		if _, err := f.svc.LabelAlertTriage(f.ctx, tenant, c.alert, c.req, nil); !matches(err, c.want) {
			t.Errorf("%s: got %v, want %T %v", name, err, c.want, c.want)
		}
	}
	// The 400 text names the triage body field, not the incident endpoint's.
	_, err = f.svc.LabelAlertTriage(f.ctx, tenant, alert, models.AlertTriageLabelRequest{Verdict: "corrected"}, nil)
	var ve *ai.LabelValidationError
	if !errors.As(err, &ve) || ve.Msg != "label is required when correcting a triage" {
		t.Errorf("corrected without label: got %v", err)
	}
	_ = f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM ai_labels`).Scan(&count)
	if count != 1 {
		t.Fatalf("rejected labels wrote rows: %d", count)
	}
}

func TestExportIncludesTriageLabels(t *testing.T) {
	f := newFixture(t)
	tenant := testutil.InsertTenant(f.ctx, t, f.db, "triage export")
	m1 := testutil.InsertHTTPMonitor(f.ctx, t, f.db, tenant, "api-gateway")
	m2 := testutil.InsertHTTPMonitor(f.ctx, t, f.db, tenant, "billing")
	snap := testSnapshot(m1, m2)

	// Incident analysis + a triage of an alert linked to that incident (the
	// earliest link wins), and an unlinked triage.
	incident := f.incident(tenant)
	later := f.incident(tenant)
	f.analysis(tenant, incident, "ready", &snap, 0)
	if _, err := f.svc.LabelIncidentAnalysis(f.ctx, tenant, incident, models.IncidentAILabelRequest{Verdict: "confirmed"}, nil); err != nil {
		t.Fatal(err)
	}
	one := snap
	one.Decisions = snap.Decisions[:1]
	linked := f.alert(tenant, m1)
	f.exec(`INSERT INTO incident_alerts (incident_id, alert_id, created_at) VALUES ($1,$2,NOW()-INTERVAL '1 minute'),($3,$2,NOW())`, incident, linked, later)
	linkedTriage := f.triage(tenant, linked, "ready", &one)
	unlinked := f.alert(tenant, m2)
	unlinkedTriage := f.triage(tenant, unlinked, "ready", &one)
	for _, a := range []uuid.UUID{linked, unlinked} {
		if _, err := f.svc.LabelAlertTriage(f.ctx, tenant, a, models.AlertTriageLabelRequest{Verdict: "confirmed"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	var group *uuid.UUID
	if err := f.db.QueryRowContext(f.ctx, `SELECT incident_id FROM ai_labels WHERE subject_key=$1`, linkedTriage.String()).Scan(&group); err != nil || group == nil || *group != incident {
		t.Fatalf("linked triage group %v %v", group, err)
	}

	all, err := f.svc.Export(f.ctx, tenant, models.AILabelExportFilter{})
	if err != nil {
		t.Fatal(err)
	}
	rows := exportLines(t, all)
	if all.Rows != 4 || len(rows) != 4 {
		t.Fatalf("rows=%d skipped=%d", all.Rows, all.Skipped)
	}
	groups := map[string]map[string]bool{} // subject type -> groups
	for _, r := range rows {
		st, _ := r["provenance"].(map[string]any)["subject_type"].(string)
		g, _ := r["group"].(string)
		if groups[st] == nil {
			groups[st] = map[string]bool{}
		}
		groups[st][g] = true
	}
	if len(groups["incident_analysis"]) != 1 || len(groups["alert_triage"]) != 2 {
		t.Fatalf("groups %v", groups)
	}
	var incidentGroup string
	for g := range groups["incident_analysis"] {
		incidentGroup = g
	}
	if !groups["alert_triage"][incidentGroup] {
		t.Fatalf("linked triage must share the incident's split group: %v", groups)
	}
	for _, id := range []uuid.UUID{linkedTriage, unlinkedTriage, incident} {
		if bytes.Contains(all.Body, []byte(id.String())) {
			t.Fatalf("identifier %s leaked", id)
		}
	}

	only, err := f.svc.Export(f.ctx, tenant, models.AILabelExportFilter{SubjectTypes: []string{"alert_triage"}})
	if err != nil || only.Rows != 2 {
		t.Fatalf("alert_triage filter: %+v %v", only, err)
	}
}
