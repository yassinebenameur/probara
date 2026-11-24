//go:build !unit

package statuspage

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/maintenance"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// A repeating window is listed by occurrence (docs/state-semantics.md S-M5):
// once, at its next occurrence, with the following starts, never at the
// date its schedule was first saved. A skipped occurrence is not listed.
func TestStatusPageListsRepeatingMaintenanceByOccurrence(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	var now time.Time
	if err := dbClient.QueryRowContext(ctx, `SELECT NOW()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "status-mw-recurring")
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "mw-recurring", "MW Recurring")
	onPage := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "on-page")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, onPage, 0)

	// Daily, starting six hours from now (UTC), for an hour.
	first := now.Add(6 * time.Hour).UTC()
	rule := &maintenance.Rule{
		Weekdays:    []time.Weekday{0, 1, 2, 3, 4, 5, 6},
		StartMinute: first.Hour()*60 + first.Minute(),
		Duration:    time.Hour,
		Timezone:    "UTC",
	}
	// The stored first occurrence is weeks old, as for a long-running series.
	sched := maintenance.Schedule{Start: now.Add(-40 * 24 * time.Hour), End: now.Add(-40*24*time.Hour + time.Hour), Repeat: rule}
	windowID := uuid.New()
	args := append([]interface{}{windowID, tenantID}, maintenance.ScheduleArgs(sched)...)
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO maintenance_windows (id, tenant_id, title, description, starts_at, ends_at,
			recurrence_weekdays, recurrence_start_time, recurrence_duration_minutes,
			recurrence_timezone, recurrence_until)
		VALUES ($1, $2, 'nightly backup', '', $3, $4, $5, $6, $7, $8, $9)`, args...); err != nil {
		t.Fatalf("insert window: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx,
		`INSERT INTO maintenance_window_monitors (maintenance_window_id, monitor_id) VALUES ($1, $2)`, windowID, onPage); err != nil {
		t.Fatal(err)
	}
	if _, err := maintenance.Materialize(ctx, dbClient, tenantID, windowID, sched, now); err != nil {
		t.Fatal(err)
	}
	skipped, err := maintenance.SkipNext(ctx, dbClient, tenantID, windowID, now)
	if err != nil || skipped == nil {
		t.Fatalf("SkipNext = %v, %v", skipped, err)
	}

	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient))
	page, err := svc.GetStatusPageBySlug(ctx, "mw-recurring")
	if err != nil {
		t.Fatalf("GetStatusPageBySlug() error = %v", err)
	}
	if len(page.MaintenanceWindows) != 1 {
		t.Fatalf("maintenance windows = %+v, want the repeating window once", page.MaintenanceWindows)
	}
	w := page.MaintenanceWindows[0]
	next := skipped.Start.Add(24 * time.Hour)
	if !w.Repeating || w.IsActive {
		t.Errorf("window = %+v, want repeating and not active", w)
	}
	if !w.StartsAt.Equal(next) || !w.EndsAt.Equal(next.Add(time.Hour)) {
		t.Errorf("listed occurrence %v–%v, want the one after the skipped one (%v)", w.StartsAt, w.EndsAt, next)
	}
	if len(w.NextStartsAt) != maxListedNextStarts || !w.NextStartsAt[0].Equal(next.Add(24*time.Hour)) {
		t.Errorf("next starts = %v, want %d daily starts after %v", w.NextStartsAt, maxListedNextStarts, next)
	}

	view := buildStatusPageMaintenanceView(w)
	if !strings.HasPrefix(view.RepeatText, "Repeats · next: ") || !strings.HasSuffix(view.RepeatText, " UTC") {
		t.Errorf("RepeatText = %q", view.RepeatText)
	}
	if buildStatusPageMaintenanceView(StatusPageMaintenanceWindow{StartsAt: next, EndsAt: next}).RepeatText != "" {
		t.Error("a one-off window must not say it repeats")
	}
}
