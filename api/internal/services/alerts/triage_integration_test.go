//go:build !unit

package alerts

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/ai"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func triageSnapshot(monitor uuid.UUID) []byte {
	ev, _ := json.Marshal(ai.MonitorEvidence{Checks: 10, Failures: 4, DNSFailures: 4})
	b, _ := json.Marshal(ai.DecisionSnapshot{
		V: 1, Catalogue: ai.CatalogueVersion, Hypothesis: "dns_failure", Score: .9,
		Model: ai.ModelIdentity{Engine: "laya-cpu", Version: "probara-laya-v1", SHA256: strings.Repeat("a", 64), Catalogue: ai.CatalogueVersion},
		Decisions: []ai.SnapshotDecision{{ID: uuid.NewString(), Task: "rca", MonitorRef: monitor.String(), Evidence: ev,
			Choices: ai.Choices("rca"), Predicted: "dns_failure", Final: "dns_failure"}},
	})
	return b
}

// insertTriageTestAlert opens an alert on a fresh monitor (one open alert per
// monitor and kind) unless monitor is set.
func insertTriageTestAlert(ctx context.Context, t *testing.T, dbc *shareddb.Client, tenant, monitor uuid.UUID, minutesAgo int) uuid.UUID {
	t.Helper()
	if monitor == uuid.Nil {
		monitor = testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "m-"+uuid.NewString()[:8])
	}
	id := uuid.New()
	if _, err := dbc.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, status, triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, 'active', NOW() - make_interval(mins => $4), 1, NOW(), NOW())`, id, tenant, monitor, minutesAgo); err != nil {
		t.Fatalf("insert alert: %v", err)
	}
	return id
}

// insertTriage writes an alert_triage row the way the worker would leave it.
func insertTriage(ctx context.Context, t *testing.T, dbc *shareddb.Client, tenant, alert uuid.UUID, status string, snapshot any) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var (
		hypothesis, confidence, summary, skip, errMsg any
		completed                                     = "NOW()"
	)
	switch status {
	case "ready":
		hypothesis, confidence, summary = "dns_failure", "high", "DNS lookups failed for the monitored host."
	case "failed":
		errMsg = "local AI unavailable or returned invalid decisions"
	case "skipped":
		skip = "cooldown"
	case "pending":
		completed = "NULL"
	}
	if _, err := dbc.ExecContext(ctx, `
		INSERT INTO alert_triage (id, tenant_id, alert_id, status, skip_reason, hypothesis, confidence, summary,
		                          error_message, decision_snapshot, completed_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,`+completed+`)`,
		id, tenant, alert, status, skip, hypothesis, confidence, summary, errMsg, snapshot); err != nil {
		t.Fatalf("insert triage: %v", err)
	}
	return id
}

func TestAlertsCarryTriage(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "triage")
	other := testutil.InsertTenant(ctx, t, dbc, "other")
	monitor := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "checkout-api")
	svc := NewService(dbc, nil)

	ready := insertTriageTestAlert(ctx, t, dbc, tenant, monitor, 10)
	readyTriage := insertTriage(ctx, t, dbc, tenant, ready, "ready", string(triageSnapshot(monitor)))
	none := insertTriageTestAlert(ctx, t, dbc, tenant, uuid.Nil, 9)
	pending := insertTriageTestAlert(ctx, t, dbc, tenant, uuid.Nil, 8)
	insertTriage(ctx, t, dbc, tenant, pending, "pending", nil)
	failed := insertTriageTestAlert(ctx, t, dbc, tenant, uuid.Nil, 7)
	insertTriage(ctx, t, dbc, tenant, failed, "failed", nil)
	skipped := insertTriageTestAlert(ctx, t, dbc, tenant, uuid.Nil, 6)
	insertTriage(ctx, t, dbc, tenant, skipped, "skipped", nil)
	// Ready rows whose snapshot carries no decision array are not labelable,
	// and must not make jsonb_array_length raise.
	nullSnap := insertTriageTestAlert(ctx, t, dbc, tenant, uuid.Nil, 5)
	insertTriage(ctx, t, dbc, tenant, nullSnap, "ready", nil)
	emptySnap := insertTriageTestAlert(ctx, t, dbc, tenant, uuid.Nil, 4)
	insertTriage(ctx, t, dbc, tenant, emptySnap, "ready", `{"v":1,"decisions":[]}`)
	objectSnap := insertTriageTestAlert(ctx, t, dbc, tenant, uuid.Nil, 3)
	insertTriage(ctx, t, dbc, tenant, objectSnap, "ready", `{"v":1,"decisions":{"a":1}}`)
	scalarSnap := insertTriageTestAlert(ctx, t, dbc, tenant, uuid.Nil, 2)
	insertTriage(ctx, t, dbc, tenant, scalarSnap, "ready", `{"v":1,"decisions":"x"}`)
	// A triage row stamped with another tenant never joins.
	foreign := insertTriageTestAlert(ctx, t, dbc, tenant, uuid.Nil, 1)
	insertTriage(ctx, t, dbc, other, foreign, "ready", string(triageSnapshot(monitor)))

	list, err := svc.ListAlerts(ctx, tenant, &models.AlertListParams{PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if list.Total != 10 || len(list.Items) != 10 {
		t.Fatalf("list total=%d items=%d", list.Total, len(list.Items))
	}
	byID := map[uuid.UUID]*models.AlertTriage{}
	for _, a := range list.Items {
		byID[a.ID] = a.Triage
		if a.Triage != nil && a.Triage.LabelContext != nil {
			t.Fatalf("list must not carry label_context: %+v", a.Triage)
		}
	}

	tr := byID[ready]
	if tr == nil || tr.ID != readyTriage || tr.Status != "ready" || tr.Hypothesis == nil || *tr.Hypothesis != "dns_failure" ||
		tr.Title == nil || *tr.Title != "DNS resolution failure" || tr.Confidence == nil || *tr.Confidence != "high" ||
		tr.Summary == nil || tr.CompletedAt == nil || tr.Labelled || !tr.Labelable || tr.SkipReason != nil || tr.ErrorMessage != nil {
		t.Fatalf("ready triage %+v", tr)
	}
	if byID[none] != nil || byID[foreign] != nil {
		t.Fatalf("no-row/foreign triage joined: %+v %+v", byID[none], byID[foreign])
	}
	if tr := byID[pending]; tr == nil || tr.Status != "pending" || tr.CompletedAt != nil || tr.Labelable || tr.Title != nil {
		t.Fatalf("pending triage %+v", tr)
	}
	if tr := byID[failed]; tr == nil || tr.ErrorMessage == nil || *tr.ErrorMessage != "local AI unavailable or returned invalid decisions" || tr.Labelable {
		t.Fatalf("failed triage %+v", tr)
	}
	if tr := byID[skipped]; tr == nil || tr.SkipReason == nil || *tr.SkipReason != "cooldown" || tr.Labelable {
		t.Fatalf("skipped triage %+v", tr)
	}
	for name, id := range map[string]uuid.UUID{"null": nullSnap, "empty": emptySnap, "object": objectSnap, "scalar": scalarSnap} {
		if tr := byID[id]; tr == nil || tr.Status != "ready" || tr.Labelable {
			t.Fatalf("%s snapshot triage %+v", name, tr)
		}
	}

	// Nullable fields serialize as null; a missing row omits the object.
	b, _ := json.Marshal(list.Items)
	var raw []map[string]any
	_ = json.Unmarshal(b, &raw)
	for _, item := range raw {
		switch item["id"] {
		case pending.String():
			tri := item["triage"].(map[string]any)
			for _, k := range []string{"hypothesis", "title", "confidence", "summary", "skip_reason", "error_message", "completed_at"} {
				if v, ok := tri[k]; !ok || v != nil {
					t.Fatalf("pending %s = %v (present %v), want null", k, v, ok)
				}
			}
			if _, ok := tri["label_context"]; ok {
				t.Fatal("label_context must be omitted")
			}
		case none.String():
			if _, ok := item["triage"]; ok {
				t.Fatal("alert without a triage row carried a triage key")
			}
		}
	}

	// GET /{id} adds the label context only when labelable.
	got, err := svc.GetAlert(ctx, tenant, ready)
	if err != nil {
		t.Fatal(err)
	}
	lc := got.Triage.LabelContext
	if lc == nil || lc.Predicted != "dns_failure" || len(lc.Decisions) != 1 || lc.Decisions[0].MonitorName == nil ||
		*lc.Decisions[0].MonitorName != "checkout-api" || lc.Label != nil {
		t.Fatalf("label context %+v", lc)
	}
	for _, id := range []uuid.UUID{pending, nullSnap, objectSnap} {
		got, err := svc.GetAlert(ctx, tenant, id)
		if err != nil || got.Triage == nil || got.Triage.LabelContext != nil {
			t.Fatalf("GET non-labelable %s: %+v %v", id, got, err)
		}
	}
	if got, err := svc.GetAlert(ctx, tenant, none); err != nil || got.Triage != nil {
		t.Fatalf("GET no row: %+v %v", got, err)
	}

	// labelled flips once an ai_labels row exists for the triage.
	if _, err := dbc.ExecContext(ctx, `
		INSERT INTO ai_labels (tenant_id, subject_type, subject_key, subject_id, verdict, incident_label, answers, request, catalogue_version)
		VALUES ($1, 'alert_triage', $2, $3, 'confirmed', 'dns_failure', '{}', '{}', $4)`,
		tenant, readyTriage.String(), readyTriage, ai.CatalogueVersion); err != nil {
		t.Fatal(err)
	}
	got, err = svc.GetAlert(ctx, tenant, ready)
	if err != nil || !got.Triage.Labelled || got.Triage.LabelContext == nil || got.Triage.LabelContext.Label == nil {
		t.Fatalf("labelled: %+v %v", got.Triage, err)
	}
	recent, err := svc.GetRecentAlerts(ctx, tenant, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range recent {
		if a.ID == ready && (a.Triage == nil || !a.Triage.Labelled) {
			t.Fatalf("recent alerts triage %+v", a.Triage)
		}
	}

	// Ack/resolve responses carry the compact triage without label context,
	// and triage never touches the alert row itself.
	acked, err := svc.AcknowledgeAlert(ctx, tenant, ready)
	if err != nil || acked.Triage == nil || acked.Triage.Status != "ready" || acked.Triage.LabelContext != nil {
		t.Fatalf("ack: %+v %v", acked, err)
	}
	resolved, err := svc.ResolveAlert(ctx, tenant, pending)
	if err != nil || resolved.Triage == nil || resolved.Triage.Status != "pending" {
		t.Fatalf("resolve: %+v %v", resolved, err)
	}
}

func TestTriageLabelledIsTenantScoped(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "triage")
	other := testutil.InsertTenant(ctx, t, dbc, "other")
	monitor := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "checkout-api")
	alert := insertTriageTestAlert(ctx, t, dbc, tenant, monitor, 10)
	triage := insertTriage(ctx, t, dbc, tenant, alert, "ready", string(triageSnapshot(monitor)))
	if _, err := dbc.ExecContext(ctx, `
		INSERT INTO ai_labels (tenant_id, subject_type, subject_key, subject_id, verdict, incident_label, answers, request, catalogue_version)
		VALUES ($1, 'alert_triage', $2, $3, 'confirmed', 'dns_failure', '{}', '{}', $4)`,
		other, triage.String(), triage, ai.CatalogueVersion); err != nil {
		t.Fatal(err)
	}
	got, err := NewService(dbc, nil).GetAlert(ctx, tenant, alert)
	if err != nil || got.Triage == nil || got.Triage.Labelled || got.Triage.LabelContext.Label != nil {
		t.Fatalf("foreign label counted: %+v %v", got.Triage, err)
	}
}
