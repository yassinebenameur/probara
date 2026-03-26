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

func TestServiceCreateIncidentTimelineEntryAppendsNonSystemEntry(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	svc := NewService(dbClient, nil)

	incident, err := svc.CreateIncident(ctx, tenantID, &models.CreateIncidentRequest{
		Title:   "Database latency",
		Summary: "Read queries are slower than expected.",
	})
	if err != nil {
		t.Fatalf("CreateIncident() error = %v", err)
	}

	if _, err := svc.CreateIncidentTimelineEntry(ctx, tenantID, incident.ID, &models.CreateIncidentTimelineEntryRequest{
		EntryType: models.IncidentTimelineEntryTypeInternalNote,
		Message:   "Investigating replica lag.",
	}); err != nil {
		t.Fatalf("CreateIncidentTimelineEntry() error = %v", err)
	}

	detail, err := svc.GetIncident(ctx, tenantID, incident.ID)
	if err != nil {
		t.Fatalf("GetIncident() error = %v", err)
	}
	if len(detail.Timeline) != 2 {
		t.Fatalf("timeline length = %d, want %d", len(detail.Timeline), 2)
	}
	entry := detail.Timeline[1]
	if entry.EntryType != models.IncidentTimelineEntryTypeInternalNote {
		t.Fatalf("timeline entry type = %q, want %q", entry.EntryType, models.IncidentTimelineEntryTypeInternalNote)
	}
	if entry.Message != "Investigating replica lag." {
		t.Fatalf("timeline entry message = %q, want %q", entry.Message, "Investigating replica lag.")
	}
}
