package monitors

// S-P2 (docs/state-semantics.md): an enabled toggle resets observed state to
// 'unknown', clears the per-location machinery, and records a pause/resume
// interval — a monitor paused while down must not re-open an alert on
// unpause from its stale pre-pause state.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

type timelineRow struct {
	State  string
	Reason string
	Open   bool
}

func loadTimeline(ctx context.Context, t *testing.T, db *shareddb.Client, monitorID uuid.UUID) []timelineRow {
	t.Helper()
	rows, err := db.QueryContext(ctx, `
		SELECT state, reason, ended_at IS NULL
		FROM monitor_state_intervals
		WHERE monitor_id = $1
		ORDER BY started_at, created_at
	`, monitorID)
	if err != nil {
		t.Fatalf("query timeline: %v", err)
	}
	defer rows.Close()
	var out []timelineRow
	for rows.Next() {
		var r timelineRow
		if err := rows.Scan(&r.State, &r.Reason, &r.Open); err != nil {
			t.Fatalf("scan timeline: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate timeline: %v", err)
	}
	return out
}

func TestSetEnabledResetsStateAndWritesTimeline_Integration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "tenant-pause")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "pause-target")
	locationID := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug) VALUES ($1, $2, 'pause-loc', 'pause-loc')
	`, locationID, tenantID); err != nil {
		t.Fatalf("insert location: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO monitor_location_state (monitor_id, location_id, tenant_id, current_state, consecutive_failures, last_check_at)
		VALUES ($1, $2, $3, 'down', 3, NOW())
	`, monitorID, locationID, tenantID); err != nil {
		t.Fatalf("insert location state: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		UPDATE monitors SET current_state = 'down', consecutive_failures = 3 WHERE id = $1
	`, monitorID); err != nil {
		t.Fatalf("force down state: %v", err)
	}

	repo := NewPostgresRepository(dbClient)

	// Pause while down: state resets, per-location machinery cleared.
	if err := repo.SetEnabled(ctx, tenantID, monitorID, false); err != nil {
		t.Fatalf("SetEnabled(false): %v", err)
	}
	var enabled bool
	var state string
	var fails int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT enabled, current_state, consecutive_failures FROM monitors WHERE id = $1
	`, monitorID).Scan(&enabled, &state, &fails); err != nil {
		t.Fatalf("query monitor: %v", err)
	}
	if enabled || state != "unknown" || fails != 0 {
		t.Fatalf("after pause: enabled=%v state=%s fails=%d, want false/unknown/0 [S-P2]", enabled, state, fails)
	}
	var locStates int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM monitor_location_state WHERE monitor_id = $1
	`, monitorID).Scan(&locStates); err != nil {
		t.Fatalf("count location state: %v", err)
	}
	if locStates != 0 {
		t.Fatalf("location state rows = %d, want 0 after pause [S-P2]", locStates)
	}

	// Same-value toggle is a no-op: no second interval.
	if err := repo.SetEnabled(ctx, tenantID, monitorID, false); err != nil {
		t.Fatalf("SetEnabled(false) repeat: %v", err)
	}
	timeline := loadTimeline(ctx, t, dbClient, monitorID)
	if len(timeline) != 1 || timeline[0].Reason != "pause" || timeline[0].State != "unknown" || !timeline[0].Open {
		t.Fatalf("timeline after pause = %+v, want one open unknown/pause interval [S-P2]", timeline)
	}

	// Resume: unknown until first fresh evidence, boundary recorded.
	if err := repo.SetEnabled(ctx, tenantID, monitorID, true); err != nil {
		t.Fatalf("SetEnabled(true): %v", err)
	}
	timeline = loadTimeline(ctx, t, dbClient, monitorID)
	if len(timeline) != 2 {
		t.Fatalf("timeline after resume = %+v, want pause then resume [S-P2]", timeline)
	}
	last := timeline[1]
	if last.Reason != "resume" || last.State != "unknown" || !last.Open {
		t.Fatalf("resume interval = %+v, want open unknown/resume [S-P2]", last)
	}
}

func TestSetLocationsEmptyWritesLocationChangeInterval_Integration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "tenant-locchange")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "locchange-target")
	locationID := insertRaceTestLocation(ctx, t, dbClient, tenantID, "locchange-loc")
	attachRaceTestLocation(ctx, t, dbClient, monitorID, locationID)
	if _, err := dbClient.ExecContext(ctx, `
		UPDATE monitors SET current_state = 'down', consecutive_failures = 2 WHERE id = $1
	`, monitorID); err != nil {
		t.Fatalf("force down state: %v", err)
	}

	repo := NewPostgresRepository(dbClient)
	if err := repo.SetLocations(ctx, tenantID, monitorID, []uuid.UUID{}); err != nil {
		t.Fatalf("SetLocations(empty): %v", err)
	}

	timeline := loadTimeline(ctx, t, dbClient, monitorID)
	if len(timeline) != 1 || timeline[0].Reason != "location_change" || timeline[0].State != "unknown" || !timeline[0].Open {
		t.Fatalf("timeline = %+v, want one open unknown/location_change interval", timeline)
	}

	var state string
	if err := dbClient.QueryRowContext(ctx,
		`SELECT current_state FROM monitors WHERE id = $1`, monitorID).Scan(&state); err != nil {
		t.Fatalf("query state: %v", err)
	}
	if state != "unknown" {
		t.Fatalf("current_state = %s, want unknown", state)
	}
}
