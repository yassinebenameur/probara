package ingest

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func insertLocation(ctx context.Context, t testing.TB, dbClient *db.Client, tenantID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug)
		VALUES ($1, $2, $3, $4)
	`, id, tenantID, name, name); err != nil {
		t.Fatalf("insert location %s: %v", name, err)
	}
	return id
}

func selectLocations(ctx context.Context, t testing.TB, dbClient *db.Client, monitorID uuid.UUID, quorum int, locationIDs ...uuid.UUID) {
	t.Helper()
	for _, locationID := range locationIDs {
		if _, err := dbClient.ExecContext(ctx, `
			INSERT INTO monitor_locations (monitor_id, location_id) VALUES ($1, $2)
		`, monitorID, locationID); err != nil {
			t.Fatalf("insert monitor_location: %v", err)
		}
	}
	if _, err := dbClient.ExecContext(ctx, `
		UPDATE monitors SET location_quorum = $1 WHERE id = $2
	`, quorum, monitorID); err != nil {
		t.Fatalf("set quorum: %v", err)
	}
}

func locationMessage(tenantID, monitorID, locationID uuid.UUID, status string) models.CheckResultMessage {
	m := monitorMessage(tenantID, monitorID, status)
	m.LocationID = locationID.String()
	return m
}

func monitorState(ctx context.Context, t testing.TB, dbClient *db.Client, monitorID uuid.UUID) string {
	t.Helper()
	var state string
	if err := dbClient.QueryRowContext(ctx,
		`SELECT current_state FROM monitors WHERE id = $1`, monitorID).Scan(&state); err != nil {
		t.Fatalf("query monitor state: %v", err)
	}
	return state
}

func TestQuorumDegradedThenDownThenRecovery(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "quorum")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	locA := insertLocation(ctx, t, dbClient, tenantID, "vpc-a")
	locB := insertLocation(ctx, t, dbClient, tenantID, "vpc-b")
	selectLocations(ctx, t, dbClient, monitorID, 2, locA, locB)
	// consecutive_failures_threshold = 2 (column default, migration 000044)

	i := newTestIngest(t, dbClient, nil)

	// Both locations up.
	ingestMessage(t, i, locationMessage(tenantID, monitorID, locA, "success"))
	ingestMessage(t, i, locationMessage(tenantID, monitorID, locB, "success"))
	if got := monitorState(ctx, t, dbClient, monitorID); got != "up" {
		t.Fatalf("state = %s, want up", got)
	}

	// Location A fails once: temporal machine says suspect → monitor suspect.
	ingestMessage(t, i, locationMessage(tenantID, monitorID, locA, "failure"))
	if got := monitorState(ctx, t, dbClient, monitorID); got != "suspect" {
		t.Fatalf("state = %s, want suspect (one location mid-confirmation)", got)
	}

	// Location A confirms down (threshold 2). Quorum is 2 → degraded.
	ingestMessage(t, i, locationMessage(tenantID, monitorID, locA, "failure"))
	if got := monitorState(ctx, t, dbClient, monitorID); got != "degraded" {
		t.Fatalf("state = %s, want degraded (1 of 2 down, quorum 2)", got)
	}

	// Location B confirms down too → quorum tripped → down.
	ingestMessage(t, i, locationMessage(tenantID, monitorID, locB, "failure"))
	ingestMessage(t, i, locationMessage(tenantID, monitorID, locB, "failure"))
	if got := monitorState(ctx, t, dbClient, monitorID); got != "down" {
		t.Fatalf("state = %s, want down (2 of 2 down)", got)
	}

	// Location A recovers → back below quorum → degraded (B still down).
	ingestMessage(t, i, locationMessage(tenantID, monitorID, locA, "success"))
	if got := monitorState(ctx, t, dbClient, monitorID); got != "degraded" {
		t.Fatalf("state = %s, want degraded (1 of 2 down after recovery)", got)
	}

	// Location B recovers → up.
	ingestMessage(t, i, locationMessage(tenantID, monitorID, locB, "success"))
	if got := monitorState(ctx, t, dbClient, monitorID); got != "up" {
		t.Fatalf("state = %s, want up (all recovered)", got)
	}
}

func TestQuorumOneTripsWithDefaultQuorum(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "quorum-one")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	locA := insertLocation(ctx, t, dbClient, tenantID, "vpc-a")
	locB := insertLocation(ctx, t, dbClient, tenantID, "vpc-b")
	selectLocations(ctx, t, dbClient, monitorID, 1, locA, locB) // default quorum: any location down alerts

	i := newTestIngest(t, dbClient, nil)

	ingestMessage(t, i, locationMessage(tenantID, monitorID, locA, "success"))
	ingestMessage(t, i, locationMessage(tenantID, monitorID, locB, "failure"))
	ingestMessage(t, i, locationMessage(tenantID, monitorID, locB, "failure"))
	if got := monitorState(ctx, t, dbClient, monitorID); got != "down" {
		t.Fatalf("state = %s, want down (quorum 1: one location down)", got)
	}
}

func TestQuorumStaleLocationResultKeepsHistoryButNoState(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "quorum-stale")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	locA := insertLocation(ctx, t, dbClient, tenantID, "vpc-a")
	locStale := insertLocation(ctx, t, dbClient, tenantID, "vpc-old")
	selectLocations(ctx, t, dbClient, monitorID, 1, locA)

	i := newTestIngest(t, dbClient, nil)

	// A job published before an edit arrives for a no-longer-selected location.
	ingestMessage(t, i, locationMessage(tenantID, monitorID, locStale, "failure"))
	ingestMessage(t, i, locationMessage(tenantID, monitorID, locStale, "failure"))

	var count int
	if err := dbClient.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM check_results WHERE monitor_id = $1 AND location_id = $2`,
		monitorID, locStale).Scan(&count); err != nil {
		t.Fatalf("count results: %v", err)
	}
	if count != 2 {
		t.Fatalf("stale-location check_results = %d, want 2 (history kept)", count)
	}

	if err := dbClient.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM monitor_location_state WHERE monitor_id = $1 AND location_id = $2`,
		monitorID, locStale).Scan(&count); err != nil {
		t.Fatalf("count state rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("stale-location state rows = %d, want 0", count)
	}
	if got := monitorState(ctx, t, dbClient, monitorID); got != "unknown" {
		t.Fatalf("state = %s, want unknown (stale location must not move the monitor)", got)
	}
}

func TestQuorumConcurrentResultsNoDeadlock(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "quorum-conc")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	locations := make([]uuid.UUID, 4)
	for n := range locations {
		locations[n] = insertLocation(ctx, t, dbClient, tenantID, "vpc-"+uuid.NewString()[:8])
	}
	selectLocations(ctx, t, dbClient, monitorID, 2, locations...)

	i := newTestIngest(t, dbClient, nil)

	// Hammer the same monitor from all locations concurrently: the monitor
	// FOR UPDATE lock serializes them; nothing may deadlock or error.
	var wg sync.WaitGroup
	errs := make(chan error, len(locations)*5)
	for _, locationID := range locations {
		wg.Add(1)
		go func(loc uuid.UUID) {
			defer wg.Done()
			for n := 0; n < 5; n++ {
				m := locationMessage(tenantID, monitorID, loc, "failure")
				ids, err := i.buildResult(m)
				if err != nil {
					errs <- err
					return
				}
				if _, err := i.persist(ctx, m, ids); err != nil {
					errs <- err
					return
				}
			}
		}(locationID)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent persist: %v", err)
	}

	if got := monitorState(ctx, t, dbClient, monitorID); got != "down" {
		t.Fatalf("state = %s, want down (all locations failing)", got)
	}
}
