package audit

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestAuditRecordListPrune(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Docker")
	}

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	log := logger.New("audit-test", "error")
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "audit-tenant")
	otherTenantID := testutil.InsertTenant(ctx, t, dbClient, "other-tenant")
	actorID := uuid.New()

	recorder := NewRecorder(dbClient, log)
	recorder.Start()

	recorder.Record(Event{
		TenantID:   &tenantID,
		ActorType:  ActorAdminUser,
		ActorID:    &actorID,
		ActorLabel: "alice",
		Action:     "monitor.create",
		Outcome:    OutcomeSuccess,
		StatusCode: 201,
		Details:    map[string]any{"name": "checkout"},
	})
	recorder.Record(Event{
		TenantID:  &tenantID,
		ActorType: ActorAdminUser,
		ActorID:   &actorID,
		Action:    "monitor.delete",
		Outcome:   OutcomeDenied,
	})
	// Platform-level event (no tenant) and foreign-tenant event.
	recorder.Record(Event{
		ActorType:  ActorAnonymous,
		ActorLabel: "mallory",
		Action:     "auth.login",
		Outcome:    OutcomeFailure,
	})
	recorder.Record(Event{
		TenantID:  &otherTenantID,
		ActorType: ActorAPIKey,
		Action:    "monitor.create",
		Outcome:   OutcomeSuccess,
	})

	recorder.Stop() // flushes

	svc := NewService(dbClient)

	t.Run("tenant-scoped list excludes platform and foreign rows", func(t *testing.T) {
		resp, err := svc.List(ctx, tenantID, false, Filters{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if resp.Total != 2 {
			t.Fatalf("expected 2 entries, got %d", resp.Total)
		}
	})

	t.Run("superadmin list includes platform rows", func(t *testing.T) {
		resp, err := svc.List(ctx, tenantID, true, Filters{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if resp.Total != 3 {
			t.Fatalf("expected 3 entries (2 tenant + 1 platform), got %d", resp.Total)
		}
	})

	t.Run("filters by action and outcome", func(t *testing.T) {
		resp, err := svc.List(ctx, tenantID, false, Filters{Action: "monitor.create"})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if resp.Total != 1 {
			t.Fatalf("action filter: expected 1, got %d", resp.Total)
		}
		if resp.Items[0].ActorLabel != "alice" {
			t.Fatalf("expected actor label alice, got %q", resp.Items[0].ActorLabel)
		}
		if resp.Items[0].Details == nil {
			t.Fatal("expected details JSON to round-trip")
		}

		resp, err = svc.List(ctx, tenantID, false, Filters{Outcome: OutcomeDenied})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if resp.Total != 1 {
			t.Fatalf("outcome filter: expected 1, got %d", resp.Total)
		}
	})

	t.Run("distinct actions", func(t *testing.T) {
		actions, err := svc.DistinctActions(ctx, tenantID, true)
		if err != nil {
			t.Fatalf("DistinctActions: %v", err)
		}
		if len(actions) != 3 {
			t.Fatalf("expected 3 distinct actions, got %v", actions)
		}
	})

	t.Run("pruner deletes only old rows", func(t *testing.T) {
		// Age one row past the retention window.
		if _, err := dbClient.ExecContext(ctx, `
			UPDATE audit_log SET occurred_at = NOW() - INTERVAL '400 days'
			WHERE action = 'monitor.delete'
		`); err != nil {
			t.Fatalf("age row: %v", err)
		}

		pruner := NewPruner(dbClient, log, 365)
		pruner.pruneOnce()

		resp, err := svc.List(ctx, tenantID, false, Filters{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if resp.Total != 1 {
			t.Fatalf("expected 1 entry after prune, got %d", resp.Total)
		}
	})

	t.Run("time range filter", func(t *testing.T) {
		from := time.Now().Add(-time.Hour)
		resp, err := svc.List(ctx, tenantID, false, Filters{From: &from})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if resp.Total != 1 {
			t.Fatalf("expected 1 recent entry, got %d", resp.Total)
		}
	})
}
