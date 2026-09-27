package locations

// Lock-order race test for S-O1 (docs/state-semantics.md): location deletion
// must lock the affected monitor rows before deleting membership/state rows,
// because result ingest (scheduler/internal/ingest/quorum.go) holds the
// monitor lock while upserting per-location state. The ingest side is
// replicated as raw SQL in the same transaction shape.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestDeleteRespectsIngestLockOrder_Integration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "tenant-loc-delete")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "loc-delete-target")
	locID := seedLocationWithState(ctx, t, dbClient, tenantID, monitorID)

	svc := NewService(dbClient)

	tx, err := dbClient.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin ingest tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	var cur string
	if err := tx.QueryRowContext(ctx, `
		SELECT current_state FROM monitors WHERE id = $1 FOR UPDATE
	`, monitorID).Scan(&cur); err != nil {
		t.Fatalf("lock monitor: %v", err)
	}

	type deleteResult struct {
		affected int
		err      error
	}
	done := make(chan deleteResult, 1)
	go func() {
		n, err := svc.Delete(ctx, tenantID, locID)
		done <- deleteResult{affected: n, err: err}
	}()
	time.Sleep(300 * time.Millisecond) // let Delete reach the monitor lock

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO monitor_location_state (monitor_id, location_id, tenant_id, current_state, consecutive_failures, last_check_at)
		VALUES ($1, $2, $3, 'up', 0, NOW())
		ON CONFLICT (monitor_id, location_id) DO UPDATE SET current_state = 'up', last_check_at = NOW()
	`, monitorID, locID, tenantID); err != nil {
		t.Fatalf("ingest state upsert (deadlock against Delete?): %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE monitors SET current_state = 'up' WHERE id = $1
	`, monitorID); err != nil {
		t.Fatalf("ingest monitor update: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit ingest tx: %v", err)
	}

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("Delete: %v [S-O1]", res.err)
		}
		if res.affected != 1 {
			t.Fatalf("Delete affected = %d, want 1", res.affected)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Delete never completed — lock-order deadlock [S-O1]")
	}

	var stateRows int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM monitor_location_state WHERE monitor_id = $1
	`, monitorID).Scan(&stateRows); err != nil {
		t.Fatalf("count state rows: %v", err)
	}
	if stateRows != 0 {
		t.Fatalf("monitor_location_state rows = %d, want 0 after location delete [S-O1]", stateRows)
	}
	var state string
	if err := dbClient.QueryRowContext(ctx, `
		SELECT current_state FROM monitors WHERE id = $1
	`, monitorID).Scan(&state); err != nil {
		t.Fatalf("read monitor state: %v", err)
	}
	if state != "unknown" {
		t.Fatalf("current_state = %q, want unknown after losing last location", state)
	}
}

func seedLocationWithState(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID, monitorID uuid.UUID) uuid.UUID {
	t.Helper()
	locID := uuid.New()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug) VALUES ($1, $2, 'race-del-loc', 'race-del-loc')
	`, locID, tenantID); err != nil {
		t.Fatalf("insert location: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO monitor_locations (monitor_id, location_id) VALUES ($1, $2)
	`, monitorID, locID); err != nil {
		t.Fatalf("attach location: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO monitor_location_state (monitor_id, location_id, tenant_id, current_state, consecutive_failures, last_check_at)
		VALUES ($1, $2, $3, 'up', 0, NOW())
	`, monitorID, locID, tenantID); err != nil {
		t.Fatalf("insert location state: %v", err)
	}
	return locID
}
