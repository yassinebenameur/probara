package monitors

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestPostgresRepository_Delete_IsSoft(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "")

	repo := NewPostgresRepository(dbClient)
	if err := repo.Delete(ctx, tenantID, monitorID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Row must still exist with deleted_at set (soft delete).
	var deletedAt *time.Time
	if err := dbClient.QueryRowContext(ctx,
		`SELECT deleted_at FROM monitors WHERE id = $1`, monitorID,
	).Scan(&deletedAt); err != nil {
		t.Fatalf("scan deleted_at: %v", err)
	}
	if deletedAt == nil {
		t.Fatal("expected deleted_at to be set after soft delete; got NULL")
	}

	// Second Delete on the same row should report "monitor not found" because
	// the UPDATE filters WHERE deleted_at IS NULL.
	if err := repo.Delete(ctx, tenantID, monitorID); err == nil || err.Error() != "monitor not found" {
		t.Fatalf("second Delete: got err=%v, want \"monitor not found\"", err)
	}
}

func TestPostgresRepository_BulkSoftDelete(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")
	a := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "a")
	b := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "b")
	c := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "c")

	repo := NewPostgresRepository(dbClient)
	deleted, err := repo.BulkSoftDelete(ctx, tenantID, []uuid.UUID{a, b, c})
	if err != nil {
		t.Fatalf("BulkSoftDelete: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("BulkSoftDelete deleted = %d, want 3", deleted)
	}

	// Idempotent: second call deletes 0.
	again, err := repo.BulkSoftDelete(ctx, tenantID, []uuid.UUID{a, b, c})
	if err != nil {
		t.Fatalf("BulkSoftDelete (second call): %v", err)
	}
	if again != 0 {
		t.Fatalf("idempotent BulkSoftDelete deleted = %d, want 0", again)
	}
}

func TestPostgresRepository_ListAndVerify_ExcludeSoftDeleted(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")
	live := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "live")
	gone := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "gone")

	repo := NewPostgresRepository(dbClient)
	if err := repo.Delete(ctx, tenantID, gone); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	monitors, total, err := repo.List(ctx, tenantID, nil, nil, 1, 50)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 || len(monitors) != 1 || monitors[0].ID != live {
		t.Fatalf("List = %+v (total=%d), want only live monitor", monitors, total)
	}

	if err := repo.VerifyMonitorsBelongToTenant(ctx, tenantID, []uuid.UUID{live, gone}); err == nil {
		t.Fatal("VerifyMonitorsBelongToTenant should reject a soft-deleted monitor")
	}
}
