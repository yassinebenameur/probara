package incidents

import (
	"context"
	"testing"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestServiceCreateManualIncident(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	svc := NewService(dbClient, nil)

	incident, err := svc.CreateIncident(ctx, tenantID, &models.CreateIncidentRequest{
		Title:   "API outage",
		Summary: "Requests are failing for all regions.",
	})
	if err != nil {
		t.Fatalf("CreateIncident() error = %v", err)
	}
	if incident.State != models.IncidentStateInvestigating {
		t.Fatalf("state = %q, want %q", incident.State, models.IncidentStateInvestigating)
	}
}

func TestServiceTransitionIncidentStateRejectsResolvedReopen(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	svc := NewService(dbClient, nil)

	incident, err := svc.CreateIncident(ctx, tenantID, &models.CreateIncidentRequest{
		Title:   "DNS outage",
		Summary: "Resolvers are timing out.",
	})
	if err != nil {
		t.Fatalf("CreateIncident() error = %v", err)
	}
	if _, err := svc.TransitionIncidentState(ctx, tenantID, incident.ID, &models.TransitionIncidentStateRequest{
		State: models.IncidentStateResolved,
	}); err != nil {
		t.Fatalf("TransitionIncidentState(resolve) error = %v", err)
	}
	if _, err := svc.TransitionIncidentState(ctx, tenantID, incident.ID, &models.TransitionIncidentStateRequest{
		State: models.IncidentStateMonitoring,
	}); err == nil {
		t.Fatalf("expected resolved incident reopen to fail")
	}
}
