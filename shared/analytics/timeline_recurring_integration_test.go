//go:build !unit

package analytics

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/shared/maintenance"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// TestIntegrateTimeline_RecurringMaintenanceOccurrences: the interval engine
// reads a repeating window's materialized occurrences (S-M5) as planned
// downtime (S-M3). Overlapping occurrences count once, a skipped one covers
// nothing, and the window's own starts_at/ends_at are never read.
func TestIntegrateTimeline_RecurringMaintenanceOccurrences(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	repo := NewRepository(dbClient)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "timeline-recurring")
	m := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "db")

	// Down for four whole days.
	day0 := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	insertInterval(ctx, t, dbClient, tenantID, m, "down", day0, ptrTime(day0.Add(4*24*time.Hour)))

	// Daily at 02:00 for 30 hours, saved at 7 Sep 00:00: each occurrence
	// overlaps the next by six hours, and 6 Sep's, running at the save,
	// covers from the save. The 9 Sep occurrence is skipped, which leaves a
	// gap from 9 Sep 08:00 (end of 8 Sep's) to 10 Sep 02:00.
	rule := &maintenance.Rule{Weekdays: []time.Weekday{0, 1, 2, 3, 4, 5, 6}, StartMinute: 2 * 60, Duration: 30 * time.Hour, Timezone: "UTC"}
	windowID := uuid.New()
	// The stored first occurrence deliberately disagrees with the
	// occurrences: coverage must come from the occurrences only.
	sched := maintenance.Schedule{Start: day0.Add(-48 * time.Hour), End: day0.Add(-47 * time.Hour), Repeat: rule}
	args := append([]interface{}{windowID, tenantID}, maintenance.ScheduleArgs(sched)...)
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO maintenance_windows (id, tenant_id, title, starts_at, ends_at,
			recurrence_weekdays, recurrence_start_time, recurrence_duration_minutes,
			recurrence_timezone, recurrence_until)
		VALUES ($1, $2, 'nightly', $3, $4, $5, $6, $7, $8, $9)`, args...); err != nil {
		t.Fatalf("insert window: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx,
		`INSERT INTO maintenance_window_monitors (maintenance_window_id, monitor_id) VALUES ($1, $2)`, windowID, m); err != nil {
		t.Fatal(err)
	}
	if _, err := maintenance.Materialize(ctx, dbClient, tenantID, windowID, sched, day0); err != nil {
		t.Fatal(err)
	}
	if _, err := maintenance.SkipNext(ctx, dbClient, tenantID, windowID, day0.Add(2*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	window := TimeRange{Start: day0, End: day0.Add(4 * 24 * time.Hour)}
	res, err := repo.IntegrateTimeline(ctx, tenantID, []uuid.UUID{m}, []TimeRange{window}, TimelineOpts{})
	if err != nil {
		t.Fatalf("IntegrateTimeline: %v", err)
	}
	// Planned: 7 Sep 00:00 → 9 Sep 08:00 (56h) plus 10 Sep 02:00 → 11 Sep
	// 00:00 (22h, clipped by the window) = 78h of the 96h down; overlaps
	// count once.
	got := res.PerMonitor[m][0]
	assertClose(t, got.PlannedDown, 78*3600)
	assertClose(t, got.UnplannedDown, 18*3600)

	outages, err := repo.Outages(ctx, tenantID, []uuid.UUID{m}, window, TimelineOpts{})
	if err != nil {
		t.Fatalf("Outages: %v", err)
	}
	var planned float64
	for _, o := range outages {
		if o.MonitorID != nil {
			planned += o.PlannedSeconds
		}
	}
	assertClose(t, planned, 78*3600)
}
