//go:build !unit

package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/shared/maintenance"
)

// TestMaintenanceOccurrenceRoll covers the scheduler's horizon roll
// (docs/state-semantics.md S-M5): two replicas rolling at once add each
// occurrence exactly once, the horizon reaches now + maintenance.Horizon,
// and a second roll is a no-op.
func TestMaintenanceOccurrenceRoll(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx := context.Background()
	dbClient, cleanup := setupRollupTestDB(ctx, t)
	defer cleanup()

	tenantID := insertTenant(ctx, t, dbClient)
	monitorID := insertMonitor(ctx, t, dbClient, tenantID, "db")

	var now time.Time
	if err := dbClient.QueryRowContext(ctx, `SELECT NOW()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	// A daily window saved 32 days ago: its occurrences reach only ~3 days
	// past now, as if nothing had rolled since.
	rule := &maintenance.Rule{Weekdays: []time.Weekday{0, 1, 2, 3, 4, 5, 6}, StartMinute: 4 * 60, Duration: time.Hour, Timezone: "Europe/Paris"}
	saved := now.Add(-32 * 24 * time.Hour)
	windowID := uuid.New()
	sched := maintenance.Schedule{Start: saved, End: saved.Add(time.Hour), Repeat: rule}
	args := append([]interface{}{windowID, tenantID}, maintenance.ScheduleArgs(sched)...)
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO maintenance_windows (id, tenant_id, title, starts_at, ends_at,
			recurrence_weekdays, recurrence_start_time, recurrence_duration_minutes,
			recurrence_timezone, recurrence_until)
		VALUES ($1, $2, 'nightly', $3, $4, $5, $6, $7, $8, $9)`, args...); err != nil {
		t.Fatalf("insert window: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx,
		`INSERT INTO maintenance_window_monitors (maintenance_window_id, monitor_id) VALUES ($1, $2)`, windowID, monitorID); err != nil {
		t.Fatal(err)
	}
	if _, err := maintenance.Materialize(ctx, dbClient, tenantID, windowID, sched, saved); err != nil {
		t.Fatal(err)
	}
	count := func() (n int, last time.Time) {
		t.Helper()
		if err := dbClient.QueryRowContext(ctx, `
			SELECT COUNT(*), MAX(starts_at) FROM maintenance_window_occurrences WHERE window_id = $1`, windowID).Scan(&n, &last); err != nil {
			t.Fatal(err)
		}
		return n, last
	}
	before, _ := count()

	replicas := []*Scheduler{newTestScheduler(dbClient), newTestScheduler(dbClient)}
	added := make([]int, len(replicas))
	var wg sync.WaitGroup
	for i, s := range replicas {
		wg.Add(1)
		go func(i int, s *Scheduler) {
			defer wg.Done()
			n, err := s.rollMaintenanceOccurrences(ctx)
			if err != nil {
				t.Errorf("replica %d: %v", i, err)
			}
			added[i] = n
		}(i, s)
	}
	wg.Wait()

	after, last := count()
	if after-before != added[0]+added[1] {
		t.Fatalf("rows grew by %d but the replicas reported %d+%d", after-before, added[0], added[1])
	}
	if added[0]+added[1] < 30 {
		t.Fatalf("roll added %d occurrences, want about a month's worth", added[0]+added[1])
	}
	horizon := now.Add(maintenance.Horizon)
	if last.After(horizon) || last.Before(horizon.Add(-25*time.Hour)) {
		t.Fatalf("last occurrence starts %v, want within a day before the horizon %v", last, horizon)
	}

	n, err := replicas[0].rollMaintenanceOccurrences(ctx)
	if err != nil || n != 0 {
		t.Fatalf("second roll = %d, %v; want 0", n, err)
	}
	if again, _ := count(); again != after {
		t.Fatalf("second roll changed the occurrences: %d -> %d", after, again)
	}
}
