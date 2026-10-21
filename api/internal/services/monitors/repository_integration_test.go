//go:build !unit

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

// Monitors created together (imports, seeds) share created_at, and Postgres
// gives ties no order that holds between separate queries. List must order
// them by id DESC so pages fetched one by one stay disjoint and complete,
// even when the tied rows are rewritten between fetches (the demo seed and
// the scheduler update monitors constantly). The id-order assertion is what
// pins the fix; the rewrite between pages exercises the case it is for.
func TestPostgresRepository_List_OrdersCreatedAtTiesByID(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")
	const total = 25
	for i := 0; i < total; i++ {
		testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "")
	}
	if _, err := dbClient.ExecContext(ctx,
		`UPDATE monitors SET created_at = '2026-07-02T00:00:00Z' WHERE tenant_id = $1`, tenantID,
	); err != nil {
		t.Fatalf("tie created_at: %v", err)
	}
	// Rewrite rows so heap order no longer matches insert order, and again
	// between page fetches below.
	rewrite := func(filter string) {
		t.Helper()
		if _, err := dbClient.ExecContext(ctx,
			`UPDATE monitors SET updated_at = NOW() WHERE tenant_id = $1 AND `+filter, tenantID,
		); err != nil {
			t.Fatalf("rewrite rows: %v", err)
		}
	}
	rewrite(`id::text < '8'`)

	repo := NewPostgresRepository(dbClient)
	var listed []uuid.UUID
	for page := 1; page <= 3; page++ {
		if page > 1 {
			rewrite(`id::text >= '4'`)
		}
		monitors, count, err := repo.List(ctx, tenantID, nil, nil, page, 10)
		if err != nil {
			t.Fatalf("List page %d: %v", page, err)
		}
		if count != total {
			t.Fatalf("List page %d total = %d, want %d", page, count, total)
		}
		for _, m := range monitors {
			listed = append(listed, m.ID)
		}
	}
	if len(listed) != total {
		t.Fatalf("listed %d monitors across pages, want %d", len(listed), total)
	}
	seen := map[uuid.UUID]bool{}
	for i, id := range listed {
		if seen[id] {
			t.Fatalf("monitor %s listed on two pages", id)
		}
		seen[id] = true
		if i > 0 && listed[i-1].String() < id.String() {
			t.Fatalf("ties not ordered by id DESC at %d: %s before %s", i, listed[i-1], id)
		}
	}
}
