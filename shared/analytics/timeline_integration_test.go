package analytics

// Timeline integration tests (docs/state-semantics.md S-U1–S-U7, S-M3,
// S-P3): per-monitor breakdowns, the serial composite, maintenance union and
// outage spans over hand-built intervals.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func insertReasonInterval(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID, monitorID uuid.UUID, state, reason string, start time.Time, end *time.Time) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO monitor_state_intervals (tenant_id, monitor_id, state, reason, started_at, ended_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, tenantID, monitorID, state, reason, start, end); err != nil {
		t.Fatalf("insert interval: %v", err)
	}
}

func insertMaintenance(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID, targetID uuid.UUID, start, end time.Time) {
	t.Helper()
	id := uuid.New()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO maintenance_windows (id, tenant_id, title, starts_at, ends_at)
		VALUES ($1, $2, 'planned work', $3, $4)
	`, id, tenantID, start, end); err != nil {
		t.Fatalf("insert maintenance window: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO maintenance_window_monitors (maintenance_window_id, monitor_id) VALUES ($1, $2)
	`, id, targetID); err != nil {
		t.Fatalf("attach maintenance window: %v", err)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestIntegrateTimeline_BreakdownSerialAndMaintenanceUnion(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	repo := NewRepository(dbClient)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "timeline")
	a := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "a")
	b := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "b")
	group := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "g")
	testutil.AddMonitorToGroup(ctx, t, dbClient, a, group)

	t0 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	at := func(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }

	// A: up 0-30, down 30-50, up 50-60. Maintenance 30-40 directly and 35-45
	// via its group: the union (30-45) is planned, 45-50 unplanned. The old
	// sum-and-clamp counted 20 planned minutes here (S-M3).
	insertInterval(ctx, t, dbClient, tenantID, a, "up", at(0), ptrTime(at(30)))
	insertInterval(ctx, t, dbClient, tenantID, a, "down", at(30), ptrTime(at(50)))
	insertInterval(ctx, t, dbClient, tenantID, a, "up", at(50), ptrTime(at(60)))
	insertMaintenance(ctx, t, dbClient, tenantID, a, at(30), at(40))
	insertMaintenance(ctx, t, dbClient, tenantID, group, at(35), at(45))

	// B: up 0-20, down 20-40, paused 40-60 (S-P3).
	insertInterval(ctx, t, dbClient, tenantID, b, "up", at(0), ptrTime(at(20)))
	insertInterval(ctx, t, dbClient, tenantID, b, "down", at(20), ptrTime(at(40)))
	insertReasonInterval(ctx, t, dbClient, tenantID, b, "unknown", "pause", at(40), ptrTime(at(60)))

	window := TimeRange{Start: at(0), End: at(60)}
	res, err := repo.IntegrateTimeline(ctx, tenantID, []uuid.UUID{a, b}, []TimeRange{window}, TimelineOpts{})
	if err != nil {
		t.Fatalf("IntegrateTimeline: %v", err)
	}

	sa := res.PerMonitor[a][0]
	assertClose(t, sa.Available, 40*60)
	assertClose(t, sa.PlannedDown, 15*60)
	assertClose(t, sa.UnplannedDown, 5*60)
	pct, ok := sa.AvailabilityPct()
	if !ok {
		t.Fatal("A should have eligible time")
	}
	assertClose(t, pct, 40.0/45.0*100)
	assertClose(t, sa.CoveragePct(), 100)

	sb := res.PerMonitor[b][0]
	assertClose(t, sb.Available, 20*60)
	assertClose(t, sb.UnplannedDown, 20*60)
	assertClose(t, sb.Paused, 20*60)
	assertClose(t, sb.Unknown, 0)
	pct, _ = sb.AvailabilityPct()
	assertClose(t, pct, 50) // paused time leaves the denominator [S-P3]

	// Serial composite [S-U6]: down = B 20-40 ∪ A 45-50 (25m); excluded =
	// A planned 30-45 ∪ B paused 40-60, minus down (40-45, 50-60: 15m);
	// available = both up, nothing excluded (0-20).
	s := res.Serial[0]
	assertClose(t, s.Down, 25*60)
	assertClose(t, s.Excluded, 15*60)
	assertClose(t, s.Available, 20*60)
	pct, _ = s.AvailabilityPct()
	assertClose(t, pct, 20.0/45.0*100)

	// Day-sized buckets split the same timeline; halves must add up.
	halves := []TimeRange{{Start: at(0), End: at(30)}, {Start: at(30), End: at(60)}}
	res, err = repo.IntegrateTimeline(ctx, tenantID, []uuid.UUID{a, b}, halves, TimelineOpts{})
	if err != nil {
		t.Fatalf("IntegrateTimeline (halves): %v", err)
	}
	assertClose(t, res.PerMonitor[a][0].Available+res.PerMonitor[a][1].Available, 40*60)
	assertClose(t, res.Serial[0].Down+res.Serial[1].Down, 25*60)

	outages, err := repo.Outages(ctx, tenantID, []uuid.UUID{a, b}, window, TimelineOpts{})
	if err != nil {
		t.Fatalf("Outages: %v", err)
	}
	var composite, perMonitor []Outage
	for _, o := range outages {
		if o.MonitorID == nil {
			composite = append(composite, o)
		} else {
			perMonitor = append(perMonitor, o)
		}
	}
	if len(perMonitor) != 2 || len(composite) != 2 {
		t.Fatalf("outages: %d per-monitor, %d composite; want 2 and 2", len(perMonitor), len(composite))
	}
	for _, o := range perMonitor {
		if *o.MonitorID == a {
			assertClose(t, o.Seconds(), 20*60)
			assertClose(t, o.PlannedSeconds, 15*60)
			assertClose(t, o.UnplannedSeconds(), 5*60)
		}
		if o.Ongoing || o.StartedBefore {
			t.Fatalf("outage %+v should be fully inside the window", o)
		}
	}
	if !composite[0].Start.Equal(at(20)) || !composite[0].End.Equal(at(40)) ||
		!composite[1].Start.Equal(at(45)) || !composite[1].End.Equal(at(50)) {
		t.Fatalf("composite outages = %+v, want 20-40 and 45-50", composite)
	}
}

func TestIntegrateTimeline_DegradedKnobAndLateTimeline(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	repo := NewRepository(dbClient)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "timeline-knob")
	degraded := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "degraded")
	late := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "late")

	now := time.Now().UTC().Truncate(time.Second)
	window := TimeRange{Start: now.Add(-time.Hour), End: now}
	// Open interval: the window is clipped at the database clock's now.
	insertInterval(ctx, t, dbClient, tenantID, degraded, "degraded", window.Start.Add(-time.Hour), nil)
	insertInterval(ctx, t, dbClient, tenantID, late, "up", window.Start.Add(30*time.Minute), nil)

	res, err := repo.IntegrateTimeline(ctx, tenantID, []uuid.UUID{degraded, late}, []TimeRange{window}, TimelineOpts{})
	if err != nil {
		t.Fatalf("IntegrateTimeline: %v", err)
	}
	pct, _ := res.PerMonitor[degraded][0].AvailabilityPct()
	assertClose(t, pct, 100) // degraded counts as available by default [S-U3]

	sl := res.PerMonitor[late][0]
	assertClose(t, sl.CoveragePct(), 50)
	assertClose(t, sl.Untracked(), 30*60)
	if ts := res.TimelineStart[late]; !ts.Equal(window.Start.Add(30 * time.Minute)) {
		t.Fatalf("timeline start = %v", ts)
	}

	res, err = repo.IntegrateTimeline(ctx, tenantID, []uuid.UUID{degraded}, []TimeRange{window}, TimelineOpts{DegradedCountsAsDown: true})
	if err != nil {
		t.Fatalf("IntegrateTimeline (knob): %v", err)
	}
	pct, _ = res.PerMonitor[degraded][0].AvailabilityPct()
	assertClose(t, pct, 0)

	outages, err := repo.Outages(ctx, tenantID, []uuid.UUID{degraded}, window, TimelineOpts{DegradedCountsAsDown: true})
	if err != nil {
		t.Fatalf("Outages: %v", err)
	}
	if len(outages) != 2 || !outages[0].StartedBefore || !outages[0].Ongoing {
		t.Fatalf("outages = %+v, want one clipped ongoing span plus its composite", outages)
	}
}
