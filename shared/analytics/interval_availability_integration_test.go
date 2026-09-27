package analytics

// Time-based availability tests (docs/state-semantics.md S-U1–S-U5, S-M3):
// the headline integrates the state timeline; maintenance overlap is planned
// downtime; unknown time leaves the denominator and surfaces as coverage.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func insertInterval(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID, monitorID uuid.UUID, state string, start time.Time, end *time.Time) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO monitor_state_intervals (tenant_id, monitor_id, state, reason, started_at, ended_at)
		VALUES ($1, $2, $3, 'result', $4, $5)
	`, tenantID, monitorID, state, start, end); err != nil {
		t.Fatalf("insert interval: %v", err)
	}
}

func TestComputeIntervalAvailability_IntegratesTimeline(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	repo := NewRepository(dbClient)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "interval-avail")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "ia-monitor")

	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(-time.Hour)

	// Timeline: 30m up, 12m down (6m of it inside a maintenance window),
	// then unknown until now.
	tUp := start
	tDown := start.Add(30 * time.Minute)
	tUnknown := start.Add(42 * time.Minute)
	insertInterval(ctx, t, dbClient, tenantID, monitorID, "up", tUp, &tDown)
	insertInterval(ctx, t, dbClient, tenantID, monitorID, "down", tDown, &tUnknown)
	insertInterval(ctx, t, dbClient, tenantID, monitorID, "unknown", tUnknown, nil)

	mwID := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO maintenance_windows (id, tenant_id, title, starts_at, ends_at)
		VALUES ($1, $2, 'planned work', $3, $4)
	`, mwID, tenantID, tDown, tDown.Add(6*time.Minute)); err != nil {
		t.Fatalf("insert maintenance window: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO maintenance_window_monitors (maintenance_window_id, monitor_id)
		VALUES ($1, $2)
	`, mwID, monitorID); err != nil {
		t.Fatalf("attach maintenance window: %v", err)
	}

	ia, covered, err := repo.computeIntervalAvailability(ctx, tenantID, []uuid.UUID{monitorID}, start, now)
	if err != nil {
		t.Fatalf("computeIntervalAvailability: %v", err)
	}
	if !covered {
		t.Fatal("window should be timeline-covered [S-U5]")
	}
	if !ia.HasData {
		t.Fatal("expected data (observed time exists)")
	}
	// available = 1800s; unplanned down = 720 - 360 = 360s (S-M3)
	// availability = 1800/2160 = 83.3333% [S-U1, S-U2]
	assertClose(t, ia.AvailabilityPct, 83.3333333)
	// coverage = (1800 + 720) / 3600 = 70% — unknown time excluded [S-U2]
	assertClose(t, ia.CoveragePct, 70.0)

	// A scope containing a monitor whose timeline starts inside the window
	// falls back to sampled math (S-U5) — never silently mixed.
	lateMonitor := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "ia-late")
	insertInterval(ctx, t, dbClient, tenantID, lateMonitor, "up", start.Add(10*time.Minute), nil)
	_, covered, err = repo.computeIntervalAvailability(ctx, tenantID, []uuid.UUID{monitorID, lateMonitor}, start, now)
	if err != nil {
		t.Fatalf("computeIntervalAvailability (late): %v", err)
	}
	if covered {
		t.Fatal("scope with a late-starting timeline must not claim interval coverage [S-U5]")
	}
}

func TestGetScopeAnalytics_LabelsMethod(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	repo := NewRepository(dbClient)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "method-label")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "ml-monitor")
	now := time.Now().UTC()

	// No timeline: sampled fallback, availability = sampled SLA.
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-30*time.Minute), "success", "monitor", testutil.IntPtr(100))
	res, err := repo.GetScopeAnalytics(ctx, tenantID, []uuid.UUID{monitorID}, Range24h, now)
	if err != nil {
		t.Fatalf("GetScopeAnalytics: %v", err)
	}
	if res.Summary.Method != "sampled" || res.Summary.CoveragePct != nil {
		t.Fatalf("method = %q coverage=%v, want sampled/nil [S-U5]", res.Summary.Method, res.Summary.CoveragePct)
	}
	assertClose(t, res.Summary.AvailabilityPct, res.Summary.SLAPct)

	// Timeline covering the window: interval method with coverage.
	insertInterval(ctx, t, dbClient, tenantID, monitorID, "up", now.Add(-25*time.Hour), nil)
	res, err = repo.GetScopeAnalytics(ctx, tenantID, []uuid.UUID{monitorID}, Range24h, now)
	if err != nil {
		t.Fatalf("GetScopeAnalytics (interval): %v", err)
	}
	if res.Summary.Method != "interval" || res.Summary.CoveragePct == nil {
		t.Fatalf("method = %q coverage=%v, want interval with coverage [S-U1]", res.Summary.Method, res.Summary.CoveragePct)
	}
	assertClose(t, res.Summary.AvailabilityPct, 100.0)
	assertClose(t, *res.Summary.CoveragePct, 100.0)
}
