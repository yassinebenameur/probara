//go:build !unit

package incidents

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestIncidentDetailAlertsCarryTriage(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "incident triage")
	other := testutil.InsertTenant(ctx, t, dbc, "other")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := dbc.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	incident := uuid.New()
	exec(`INSERT INTO incidents(id,tenant_id,title) VALUES($1,$2,'outage')`, incident, tenant)
	link := func(name string) uuid.UUID {
		t.Helper()
		monitor := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, name)
		id := uuid.New()
		exec(`INSERT INTO alerts (id, tenant_id, monitor_id, status, triggered_at, failure_count, created_at, updated_at)
			VALUES ($1,$2,$3,'active',NOW(),1,NOW(),NOW())`, id, tenant, monitor)
		exec(`INSERT INTO incident_alerts (incident_id, alert_id) VALUES ($1,$2)`, incident, id)
		return id
	}
	triaged := link("api-gateway")
	exec(`INSERT INTO alert_triage (tenant_id, alert_id, status, hypothesis, confidence, summary, decision_snapshot, completed_at)
		VALUES ($1,$2,'ready','dns_failure','medium','DNS failed.','{"decisions":[{"id":"d"}]}',NOW())`, tenant, triaged)
	plain := link("billing")
	foreign := link("search")
	exec(`INSERT INTO alert_triage (tenant_id, alert_id, status, completed_at, skip_reason) VALUES ($1,$2,'skipped',NOW(),'resolved')`, other, foreign)

	detail, err := NewService(dbc, nil).GetIncident(ctx, tenant, incident)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Alerts) != 3 {
		t.Fatalf("alerts %d", len(detail.Alerts))
	}
	for _, a := range detail.Alerts {
		switch a.ID {
		case triaged:
			tr := a.Triage
			if tr == nil || tr.Status != "ready" || tr.Title == nil || *tr.Title != "DNS resolution failure" ||
				tr.Confidence == nil || *tr.Confidence != "medium" || !tr.Labelable || tr.Labelled || tr.LabelContext != nil {
				t.Fatalf("triaged alert %+v", tr)
			}
		case plain, foreign:
			if a.Triage != nil {
				t.Fatalf("alert %s carried triage %+v", a.ID, a.Triage)
			}
		}
	}
}
