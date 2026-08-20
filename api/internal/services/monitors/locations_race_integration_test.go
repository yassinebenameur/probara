package monitors

// Lock-order race tests for S-O1 / S-E4 (docs/state-semantics.md):
// location-set mutations must lock the monitor row before touching
// monitor_locations / monitor_location_state, because result ingest
// (scheduler/internal/ingest/quorum.go) holds the monitor lock while
// upserting per-location state. The ingest side is replicated here as raw
// SQL in the same transaction shape — the real consumer lives in another
// service's internal package and cannot be imported.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func insertRaceTestLocation(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug) VALUES ($1, $2, $3, $3)
	`, id, tenantID, name); err != nil {
		t.Fatalf("insert location: %v", err)
	}
	return id
}

func attachRaceTestLocation(ctx context.Context, t *testing.T, db *shareddb.Client, monitorID, locationID uuid.UUID) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO monitor_locations (monitor_id, location_id) VALUES ($1, $2)
	`, monitorID, locationID); err != nil {
		t.Fatalf("attach location: %v", err)
	}
}

func insertRaceTestLocationState(ctx context.Context, t *testing.T, db *shareddb.Client, monitorID, locationID, tenantID uuid.UUID, state string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO monitor_location_state (monitor_id, location_id, tenant_id, current_state, consecutive_failures, last_check_at)
		VALUES ($1, $2, $3, $4, 0, NOW())
	`, monitorID, locationID, tenantID, state); err != nil {
		t.Fatalf("insert location state: %v", err)
	}
}

// Replace-to-empty races an in-flight ingest transaction. Without the
// monitor-first lock order SetLocations locks the state row, then blocks on
// the monitor row the ingest holds, while ingest blocks on the state row —
// a deadlock Postgres resolves by aborting one side.
func TestSetLocationsRespectsIngestLockOrder_Integration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "tenant-lock-order")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "lock-order-target")
	locID := insertRaceTestLocation(ctx, t, dbClient, tenantID, "race-loc-a")
	attachRaceTestLocation(ctx, t, dbClient, monitorID, locID)
	insertRaceTestLocationState(ctx, t, dbClient, monitorID, locID, tenantID, "up")

	repo := NewPostgresRepository(dbClient)

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

	done := make(chan error, 1)
	go func() { done <- repo.SetLocations(ctx, tenantID, monitorID, nil) }()
	time.Sleep(300 * time.Millisecond) // let SetLocations reach the monitor lock

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO monitor_location_state (monitor_id, location_id, tenant_id, current_state, consecutive_failures, last_check_at)
		VALUES ($1, $2, $3, 'up', 0, NOW())
		ON CONFLICT (monitor_id, location_id) DO UPDATE SET current_state = 'up', last_check_at = NOW()
	`, monitorID, locID, tenantID); err != nil {
		t.Fatalf("ingest state upsert (deadlock against SetLocations?): %v", err)
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
	case err := <-done:
		if err != nil {
			t.Fatalf("SetLocations: %v [S-O1]", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("SetLocations never completed — lock-order deadlock [S-O1]")
	}

	var stateRows int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM monitor_location_state WHERE monitor_id = $1
	`, monitorID).Scan(&stateRows); err != nil {
		t.Fatalf("count state rows: %v", err)
	}
	if stateRows != 0 {
		t.Fatalf("monitor_location_state rows = %d, want 0 after replace-to-empty [S-O1]", stateRows)
	}
	var state string
	if err := dbClient.QueryRowContext(ctx, `
		SELECT current_state FROM monitors WHERE id = $1
	`, monitorID).Scan(&state); err != nil {
		t.Fatalf("read monitor state: %v", err)
	}
	if state != "unknown" {
		t.Fatalf("current_state = %q, want unknown after replace-to-empty", state)
	}
}

// A location swap races an ingest that already passed its membership check
// for the removed location. Without the monitor-first lock order the swap's
// deletes run between that check and the state upsert, so the removed
// location's state row is resurrected — and counts again if the location is
// ever re-added (S-E4).
func TestSetLocationsCannotResurrectRemovedLocationState_Integration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "tenant-resurrect")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "resurrect-target")
	locA := insertRaceTestLocation(ctx, t, dbClient, tenantID, "race-loc-swap-a")
	locB := insertRaceTestLocation(ctx, t, dbClient, tenantID, "race-loc-swap-b")
	attachRaceTestLocation(ctx, t, dbClient, monitorID, locA)
	attachRaceTestLocation(ctx, t, dbClient, monitorID, locB)
	insertRaceTestLocationState(ctx, t, dbClient, monitorID, locA, tenantID, "up")

	repo := NewPostgresRepository(dbClient)

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
	var selected bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM monitor_locations WHERE monitor_id = $1 AND location_id = $2)
	`, monitorID, locA).Scan(&selected); err != nil {
		t.Fatalf("membership check: %v", err)
	}
	if !selected {
		t.Fatal("precondition: location A must be selected")
	}

	done := make(chan error, 1)
	go func() { done <- repo.SetLocations(ctx, tenantID, monitorID, []uuid.UUID{locB}) }()
	time.Sleep(300 * time.Millisecond)

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO monitor_location_state (monitor_id, location_id, tenant_id, current_state, consecutive_failures, last_check_at)
		VALUES ($1, $2, $3, 'up', 0, NOW())
		ON CONFLICT (monitor_id, location_id) DO UPDATE SET current_state = 'up', last_check_at = NOW()
	`, monitorID, locA, tenantID); err != nil {
		t.Fatalf("ingest state upsert: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit ingest tx: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SetLocations: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("SetLocations never completed — lock-order deadlock [S-O1]")
	}

	var staleRows int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM monitor_location_state WHERE monitor_id = $1 AND location_id = $2
	`, monitorID, locA).Scan(&staleRows); err != nil {
		t.Fatalf("count stale state rows: %v", err)
	}
	if staleRows != 0 {
		t.Fatalf("removed location's state row resurrected (%d rows) [S-E4]", staleRows)
	}
	var membership int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM monitor_locations WHERE monitor_id = $1 AND location_id = $2
	`, monitorID, locB).Scan(&membership); err != nil {
		t.Fatalf("count membership: %v", err)
	}
	if membership != 1 {
		t.Fatalf("location B membership = %d, want 1", membership)
	}
}
