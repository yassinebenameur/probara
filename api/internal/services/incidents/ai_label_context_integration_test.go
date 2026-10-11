package incidents

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/ailabels"
	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestAIAnalysisLabelContext(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "label context")
	other := testutil.InsertTenant(ctx, t, dbc, "other workspace")
	mine := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "api-gateway")
	theirs := testutil.InsertHTTPMonitor(ctx, t, dbc, other, "foreign-name")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := dbc.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	incident := uuid.New()
	exec(`INSERT INTO incidents(id,tenant_id,title) VALUES($1,$2,'outage')`, incident, tenant)
	ev, _ := json.Marshal(ai.MonitorEvidence{Checks: 10, Failures: 4, DNSFailures: 4})
	snap := ai.DecisionSnapshot{V: 1, Catalogue: ai.CatalogueVersion, Hypothesis: "dns_failure",
		Model: ai.ModelIdentity{Engine: "laya-cpu", Version: "probara-laya-v1", SHA256: strings.Repeat("e", 64), Catalogue: ai.CatalogueVersion},
		Decisions: []ai.SnapshotDecision{
			{ID: uuid.NewString(), Task: "rca", MonitorRef: mine.String(), Evidence: ev, Choices: ai.Choices("rca"), Predicted: "dns_failure", Final: "dns_failure"},
			{ID: uuid.NewString(), Task: "rca", MonitorRef: theirs.String(), Evidence: ev, Choices: ai.Choices("rca"), Predicted: "dns_failure", Final: "dns_failure"},
		}}
	raw, _ := json.Marshal(snap)
	svc := NewService(dbc, nil)
	labels := ailabels.NewService(dbc, nil)

	insert := func(status string, snapshot []byte, metadata string) uuid.UUID {
		t.Helper()
		id := uuid.New()
		var s any
		if snapshot != nil {
			s = snapshot
		}
		exec(`INSERT INTO incident_ai_analyses(id,tenant_id,incident_id,status,metadata,decision_snapshot) VALUES($1,$2,$3,$4,$5,$6)`, id, tenant, incident, status, metadata, s)
		return id
	}
	latest := func() *models.IncidentAIAnalysis {
		t.Helper()
		a, err := svc.GetLatestAIAnalysis(ctx, tenant, incident)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}

	insert("ready", nil, `{"engine":"legacy-llm","outcome":"legacy"}`)
	if a := latest(); a.LabelContext != nil {
		t.Fatalf("legacy analysis got a label context: %+v", a.LabelContext)
	}
	ready := insert("ready", raw, `{"engine":"laya-cpu","outcome":"supported"}`)
	a := latest()
	if a.ID != ready || a.LabelContext == nil || a.LabelContext.Predicted != "dns_failure" || a.LabelContext.Label != nil {
		t.Fatalf("ready analysis context %+v", a.LabelContext)
	}
	d := a.LabelContext.Decisions
	if len(d) != 2 || d[0].MonitorName == nil || *d[0].MonitorName != "api-gateway" || d[1].MonitorName != nil {
		t.Fatalf("names must be tenant-scoped: %+v", d)
	}
	body, _ := json.Marshal(a)
	if strings.Contains(string(body), "decision_snapshot") || strings.Contains(string(body), `"probabilities"`) || !strings.Contains(string(body), `"label_context"`) {
		t.Fatalf("serialized analysis: %s", body)
	}

	user := uuid.New()
	if _, err := labels.LabelIncidentAnalysis(ctx, tenant, incident, models.IncidentAILabelRequest{Verdict: "confirmed"}, &user); err != nil {
		t.Fatal(err)
	}
	detail, err := svc.GetIncident(ctx, tenant, incident)
	if err != nil {
		t.Fatal(err)
	}
	if detail.AIAnalysis == nil || detail.AIAnalysis.LabelContext == nil || detail.AIAnalysis.LabelContext.Label == nil || detail.AIAnalysis.LabelContext.Label.Verdict != "confirmed" {
		t.Fatalf("incident detail context %+v", detail.AIAnalysis)
	}

	cited := insert("ready", nil, `{"engine":"laya-cpu","outcome":"insufficient_evidence","evidence_references":[{"ref":"`+mine.String()+`/checks","monitor_ref":"`+mine.String()+`","fact":"0 of 0 checks failed in the analysis window."}],"incomplete_evidence":["`+mine.String()+`: configured upstream `+theirs.String()+` unavailable or excluded by monitor limit"]}`)
	if a := latest(); a.ID != cited || len(a.MonitorNames) != 1 || a.MonitorNames[mine.String()] != "api-gateway" {
		t.Fatalf("cited monitor names must resolve tenant-scoped: %+v", a.MonitorNames)
	}

	for _, status := range []string{"pending", "failed"} {
		insert(status, nil, `{"engine":"laya-cpu","outcome":"failed"}`)
		if a := latest(); a.LabelContext != nil || string(a.Status) != status {
			t.Fatalf("%s analysis got a label context", status)
		}
	}
}
