//go:build !unit

package maintenancewindows_test

// Recurring maintenance through the API service (docs/state-semantics.md
// S-M5): occurrences materialized on save, edits that apply from now on,
// target history, end-now, skip-next, preview and delete.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	maintenancewindows "github.com/yassinebenameur/probara/api/internal/services/maintenancewindows"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/maintenance"
	"github.com/yassinebenameur/probara/shared/testutil"
)

var everyDay = []int{0, 1, 2, 3, 4, 5, 6}

type occRow struct {
	start, end time.Time
	skipped    bool
}

func occurrenceRows(ctx context.Context, t *testing.T, db *shareddb.Client, windowID uuid.UUID) []occRow {
	t.Helper()
	rows, err := db.QueryContext(ctx, `
		SELECT starts_at, ends_at, skipped FROM maintenance_window_occurrences
		WHERE window_id = $1 ORDER BY starts_at`, windowID)
	require.NoError(t, err)
	defer rows.Close()
	var out []occRow
	for rows.Next() {
		var r occRow
		require.NoError(t, rows.Scan(&r.start, &r.end, &r.skipped))
		out = append(out, r)
	}
	require.NoError(t, rows.Err())
	return out
}

func inMaintenanceAt(ctx context.Context, t *testing.T, db *shareddb.Client, monitorID uuid.UUID, at time.Time) bool {
	t.Helper()
	var in bool
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT `+maintenance.InMaintenanceAtPredicate("m", "$2::timestamptz")+` FROM monitors m WHERE m.id = $1`,
		monitorID, at).Scan(&in))
	return in
}

func dbNow(ctx context.Context, t *testing.T, db *shareddb.Client) time.Time {
	t.Helper()
	var now time.Time
	require.NoError(t, db.QueryRowContext(ctx, `SELECT NOW()`).Scan(&now))
	return now
}

// startedMinutesAgo is a daily recurrence whose occurrence started `ago`
// before now (UTC) and lasts `minutes`.
func startedMinutesAgo(now time.Time, ago time.Duration, minutes int) *models.MaintenanceRecurrence {
	start := now.Add(-ago).UTC()
	return &models.MaintenanceRecurrence{
		Frequency:       models.MaintenanceRecurrenceWeekly,
		Weekdays:        everyDay,
		StartTime:       maintenance.FormatStartTime(start.Hour()*60 + start.Minute()),
		DurationMinutes: minutes,
		Timezone:        "UTC",
	}
}

func TestRecurringMaintenanceWindow(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "mw-recurring")
	monitorA := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "api")
	monitorB := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "web")
	svc := maintenancewindows.NewService(dbClient)
	now := dbNow(ctx, t, dbClient)

	t.Run("Create validates the recurrence", func(t *testing.T) {
		for _, tc := range []struct {
			req  models.CreateMaintenanceWindowRequest
			want string
		}{
			{models.CreateMaintenanceWindowRequest{Recurrence: &models.MaintenanceRecurrence{Frequency: "monthly"}}, "frequency"},
			{models.CreateMaintenanceWindowRequest{Recurrence: &models.MaintenanceRecurrence{Frequency: "weekly", StartTime: "02:00", DurationMinutes: 60, Timezone: "UTC"}}, "at least one weekday"},
			{models.CreateMaintenanceWindowRequest{Recurrence: &models.MaintenanceRecurrence{Frequency: "weekly", Weekdays: []int{1}, StartTime: "2am", DurationMinutes: 60, Timezone: "UTC"}}, "start_time"},
			{models.CreateMaintenanceWindowRequest{Recurrence: &models.MaintenanceRecurrence{Frequency: "weekly", Weekdays: []int{1}, StartTime: "02:00", DurationMinutes: 60, Timezone: "Nowhere/City"}}, "unknown timezone"},
			{models.CreateMaintenanceWindowRequest{Recurrence: &models.MaintenanceRecurrence{Frequency: "weekly", Weekdays: []int{1}, StartTime: "02:00", DurationMinutes: 60, Timezone: "UTC", Until: "2020-01-01"}}, "no occurrence after now"},
			{models.CreateMaintenanceWindowRequest{StartsAt: now, EndsAt: now.Add(time.Hour), Recurrence: &models.MaintenanceRecurrence{Frequency: "weekly", Weekdays: []int{1}, StartTime: "02:00", DurationMinutes: 60, Timezone: "UTC"}}, "starts_at and ends_at apply to one-off windows"},
		} {
			tc.req.Title = "x"
			tc.req.MonitorIDs = []string{monitorA.String()}
			_, err := svc.Create(ctx, tenantID, &tc.req)
			var invalid *maintenancewindows.InvalidError
			require.True(t, errors.As(err, &invalid), "want a 400 for %q, got %v", tc.want, err)
			require.Contains(t, err.Error(), tc.want)
		}
	})

	t.Run("Preview lists the next occurrences without saving", func(t *testing.T) {
		p, err := svc.Preview(ctx, &models.PreviewMaintenanceRecurrenceRequest{
			Recurrence: models.MaintenanceRecurrence{Frequency: "weekly", Weekdays: []int{2, 4}, StartTime: "02:00", DurationMinutes: 90, Timezone: "Europe/Paris"},
			Count:      4,
		})
		require.NoError(t, err)
		require.Len(t, p.Occurrences, 4)
		paris, _ := time.LoadLocation("Europe/Paris")
		for _, o := range p.Occurrences {
			l := o.StartsAt.In(paris)
			require.Equal(t, 2, l.Hour())
			require.Contains(t, []time.Weekday{time.Tuesday, time.Thursday}, l.Weekday())
			require.Equal(t, 90*time.Minute, o.EndsAt.Sub(o.StartsAt))
		}
		_, err = svc.Preview(ctx, &models.PreviewMaintenanceRecurrenceRequest{Recurrence: models.MaintenanceRecurrence{Frequency: "none"}})
		require.Error(t, err)
	})

	var windowID uuid.UUID
	t.Run("Create materializes to the horizon, the running one from now", func(t *testing.T) {
		w, err := svc.Create(ctx, tenantID, &models.CreateMaintenanceWindowRequest{
			Title:      "nightly",
			Recurrence: startedMinutesAgo(now, 30*time.Minute, 120),
			MonitorIDs: []string{monitorA.String()},
		})
		require.NoError(t, err)
		windowID = w.ID
		require.Equal(t, models.MaintenanceWindowStatusActive, w.Status)
		require.NotNil(t, w.Recurrence)
		require.Equal(t, "UTC", w.Recurrence.Timezone)
		require.NotNil(t, w.NextOccurrence)
		require.Equal(t, models.MaintenanceOccurrenceActive, w.NextOccurrence.Status)

		occ := occurrenceRows(ctx, t, dbClient, windowID)
		require.GreaterOrEqual(t, len(occ), 35)
		require.LessOrEqual(t, len(occ), 37)
		// Never declared retroactively: the running occurrence starts at
		// the save, not 30 minutes earlier.
		require.WithinDuration(t, now, occ[0].start, 5*time.Second)
		require.WithinDuration(t, now.Add(90*time.Minute), occ[0].end, 2*time.Minute)
		require.Equal(t, occ[0].start, w.StartsAt.UTC(), "a repeating window stores its first occurrence")
		require.True(t, inMaintenanceAt(ctx, t, dbClient, monitorA, dbNow(ctx, t, dbClient)))
		require.True(t, occ[len(occ)-1].start.Before(now.Add(maintenance.Horizon)))
	})

	// History the test cannot wait for: an occurrence that ran yesterday.
	yesterday := now.Add(-24 * time.Hour)
	_, err := dbClient.ExecContext(ctx, `
		INSERT INTO maintenance_window_occurrences (window_id, tenant_id, starts_at, ends_at)
		VALUES ($1, $2, $3, $4)`, windowID, tenantID, yesterday.Add(-30*time.Minute), yesterday.Add(90*time.Minute))
	require.NoError(t, err)

	t.Run("A title edit leaves the occurrences alone", func(t *testing.T) {
		before := occurrenceRows(ctx, t, dbClient, windowID)
		title := "nightly db"
		_, err := svc.Update(ctx, tenantID, windowID, &models.UpdateMaintenanceWindowRequest{Title: &title})
		require.NoError(t, err)
		require.Equal(t, before, occurrenceRows(ctx, t, dbClient, windowID))
	})

	t.Run("Times are refused on a repeating window", func(t *testing.T) {
		end := now.Add(time.Hour)
		_, err := svc.Update(ctx, tenantID, windowID, &models.UpdateMaintenanceWindowRequest{EndsAt: &end})
		require.ErrorContains(t, err, "apply to one-off windows")
	})

	t.Run("A schedule edit applies from now and never rewrites the past", func(t *testing.T) {
		rec := startedMinutesAgo(now, 30*time.Minute, 240) // the running one now lasts 4h
		edit := dbNow(ctx, t, dbClient)
		w, err := svc.Update(ctx, tenantID, windowID, &models.UpdateMaintenanceWindowRequest{Recurrence: rec})
		require.NoError(t, err)
		require.Equal(t, 240, w.Recurrence.DurationMinutes)

		occ := occurrenceRows(ctx, t, dbClient, windowID)
		require.True(t, occ[0].start.Equal(yesterday.Add(-30*time.Minute)) && occ[0].end.Equal(yesterday.Add(90*time.Minute)),
			"yesterday's occurrence is history: %+v", occ[0])
		// The running occurrence keeps its start and takes the new end.
		require.WithinDuration(t, now, occ[1].start, 5*time.Second)
		require.WithinDuration(t, now.Add(210*time.Minute), occ[1].end, 2*time.Minute)
		for _, o := range occ[2:] {
			require.True(t, o.start.After(edit))
			require.Equal(t, 4*time.Hour, o.end.Sub(o.start))
		}
	})

	t.Run("Removing a monitor keeps what it was covered for", func(t *testing.T) {
		ids := []string{monitorB.String()}
		_, err := svc.Update(ctx, tenantID, windowID, &models.UpdateMaintenanceWindowRequest{MonitorIDs: &ids})
		require.NoError(t, err)
		require.True(t, inMaintenanceAt(ctx, t, dbClient, monitorA, yesterday), "A stays covered for yesterday's occurrence")
		require.False(t, inMaintenanceAt(ctx, t, dbClient, monitorA, dbNow(ctx, t, dbClient).Add(time.Second)))
		require.False(t, inMaintenanceAt(ctx, t, dbClient, monitorB, yesterday), "B is covered only from the edit on")
		require.True(t, inMaintenanceAt(ctx, t, dbClient, monitorB, dbNow(ctx, t, dbClient)))
	})

	t.Run("End now ends the running occurrence and keeps the series", func(t *testing.T) {
		w, err := svc.EndOccurrence(ctx, tenantID, windowID)
		require.NoError(t, err)
		require.Equal(t, models.MaintenanceWindowStatusUpcoming, w.Status)
		require.NotNil(t, w.Recurrence)
		require.False(t, inMaintenanceAt(ctx, t, dbClient, monitorB, dbNow(ctx, t, dbClient)))
		require.NotNil(t, w.NextOccurrence)
		require.True(t, w.NextOccurrence.StartsAt.After(now.Add(12*time.Hour)))

		_, err = svc.EndOccurrence(ctx, tenantID, windowID)
		require.ErrorContains(t, err, "no occurrence of this window is in progress")
	})

	t.Run("Skip next skips one occurrence and survives a duration edit", func(t *testing.T) {
		before, err := svc.Get(ctx, tenantID, windowID)
		require.NoError(t, err)
		skippedStart := before.NextOccurrence.StartsAt

		w, err := svc.SkipNextOccurrence(ctx, tenantID, windowID)
		require.NoError(t, err)
		require.True(t, w.NextOccurrence.StartsAt.After(skippedStart))
		require.False(t, inMaintenanceAt(ctx, t, dbClient, monitorB, skippedStart.Add(time.Minute)))

		list, err := svc.ListOccurrences(ctx, tenantID, windowID, "", 1, 3)
		require.NoError(t, err)
		require.Equal(t, models.MaintenanceOccurrenceSkipped, list.Items[0].Status)
		require.True(t, list.Items[0].StartsAt.Equal(skippedStart))

		rec := startedMinutesAgo(now, 30*time.Minute, 60)
		_, err = svc.Update(ctx, tenantID, windowID, &models.UpdateMaintenanceWindowRequest{Recurrence: rec})
		require.NoError(t, err)
		list, err = svc.ListOccurrences(ctx, tenantID, windowID, "upcoming", 1, 3)
		require.NoError(t, err)
		require.Equal(t, models.MaintenanceOccurrenceSkipped, list.Items[0].Status, "the skip stays")
		require.Equal(t, time.Hour, list.Items[0].EndsAt.Sub(list.Items[0].StartsAt), "with the new duration")
		// Today's occurrence, ended early above, still covers the present
		// under the new duration; the edit does not revive it.
		require.False(t, inMaintenanceAt(ctx, t, dbClient, monitorB, dbNow(ctx, t, dbClient)))

		past, err := svc.ListOccurrences(ctx, tenantID, windowID, "past", 1, 10)
		require.NoError(t, err)
		require.GreaterOrEqual(t, past.Total, 2)
		require.Equal(t, models.MaintenanceOccurrencePast, past.Items[0].Status)
	})

	t.Run("Skip next is for repeating windows only", func(t *testing.T) {
		w, err := svc.Create(ctx, tenantID, &models.CreateMaintenanceWindowRequest{
			Title: "one-off", StartsAt: now.Add(time.Hour), EndsAt: now.Add(2 * time.Hour), MonitorIDs: []string{monitorA.String()},
		})
		require.NoError(t, err)
		require.Nil(t, w.Recurrence)
		_, err = svc.SkipNextOccurrence(ctx, tenantID, w.ID)
		require.ErrorContains(t, err, "only a repeating window")
	})

	t.Run("A repeating window becomes a one-off and back", func(t *testing.T) {
		start, end := now.Add(48*time.Hour), now.Add(50*time.Hour)
		w, err := svc.Update(ctx, tenantID, windowID, &models.UpdateMaintenanceWindowRequest{
			Recurrence: &models.MaintenanceRecurrence{Frequency: models.MaintenanceRecurrenceNone},
		})
		require.ErrorContains(t, err, "starts_at and ends_at are required")
		require.Nil(t, w)
		w, err = svc.Update(ctx, tenantID, windowID, &models.UpdateMaintenanceWindowRequest{
			Recurrence: &models.MaintenanceRecurrence{Frequency: models.MaintenanceRecurrenceNone},
			StartsAt:   &start, EndsAt: &end,
		})
		require.NoError(t, err)
		require.Nil(t, w.Recurrence)
		upcoming, err := svc.ListOccurrences(ctx, tenantID, windowID, "upcoming", 1, 50)
		require.NoError(t, err)
		require.Equal(t, 1, upcoming.Total)
		require.True(t, upcoming.Items[0].StartsAt.Equal(start))
		// Yesterday's and today's (ended early) stay; the rest was replaced.
		require.Len(t, occurrenceRows(ctx, t, dbClient, windowID), 2+upcoming.Total, "past occurrences stay")

		w, err = svc.Update(ctx, tenantID, windowID, &models.UpdateMaintenanceWindowRequest{
			Recurrence: &models.MaintenanceRecurrence{Frequency: "weekly", Weekdays: []int{6}, StartTime: "03:00", DurationMinutes: 30, Timezone: "America/New_York"},
		})
		require.NoError(t, err)
		require.Equal(t, []int{6}, w.Recurrence.Weekdays)
		upcoming, err = svc.ListOccurrences(ctx, tenantID, windowID, "upcoming", 1, 50)
		require.NoError(t, err)
		require.GreaterOrEqual(t, upcoming.Total, 5)
	})

	t.Run("Delete removes the future and keeps the past", func(t *testing.T) {
		require.NoError(t, svc.Delete(ctx, tenantID, windowID))
		_, err := svc.Get(ctx, tenantID, windowID)
		require.ErrorIs(t, err, maintenancewindows.ErrNotFound)
		_, err = svc.ListOccurrences(ctx, tenantID, windowID, "", 1, 10)
		require.ErrorIs(t, err, maintenancewindows.ErrNotFound)
		cut := dbNow(ctx, t, dbClient)
		for _, o := range occurrenceRows(ctx, t, dbClient, windowID) {
			require.True(t, o.start.Before(cut), "no future occurrence survives: %+v", o)
		}
		require.True(t, inMaintenanceAt(ctx, t, dbClient, monitorA, yesterday), "history still covers yesterday")
		list, err := svc.List(ctx, tenantID, "", uuid.Nil, 1, 50)
		require.NoError(t, err)
		for _, w := range list.Items {
			require.NotEqual(t, windowID, w.ID)
		}
	})
}

// S-M5: a one-off PATCHed to end now ends its occurrence at the edit; one
// that is over cannot be moved, which would rewrite history.
func TestOneOffEditsApplyFromNow(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "mw-oneoff")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "api")
	svc := maintenancewindows.NewService(dbClient)
	now := dbNow(ctx, t, dbClient)

	w, err := svc.Create(ctx, tenantID, &models.CreateMaintenanceWindowRequest{
		Title: "deploy", StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), MonitorIDs: []string{monitorID.String()},
	})
	require.NoError(t, err)
	// Created windows keep today's behaviour: a start in the past is kept.
	occ := occurrenceRows(ctx, t, dbClient, w.ID)
	require.Len(t, occ, 1)
	require.True(t, occ[0].start.Equal(now.Add(-time.Hour)))

	// Moving the start earlier does not reach into the past.
	earlier := now.Add(-3 * time.Hour)
	_, err = svc.Update(ctx, tenantID, w.ID, &models.UpdateMaintenanceWindowRequest{StartsAt: &earlier})
	require.NoError(t, err)
	occ = occurrenceRows(ctx, t, dbClient, w.ID)
	require.Len(t, occ, 1)
	require.True(t, occ[0].start.Equal(now.Add(-time.Hour)), "a started occurrence keeps its start")

	w, err = svc.EndOccurrence(ctx, tenantID, w.ID)
	require.NoError(t, err)
	require.Equal(t, models.MaintenanceWindowStatusPast, w.Status)
	require.False(t, inMaintenanceAt(ctx, t, dbClient, monitorID, dbNow(ctx, t, dbClient)))
	require.True(t, w.EndsAt.Before(now.Add(time.Minute)), "a one-off's ends_at follows end-now")

	later := now.Add(-30 * time.Minute)
	_, err = svc.Update(ctx, tenantID, w.ID, &models.UpdateMaintenanceWindowRequest{EndsAt: &later})
	require.ErrorContains(t, err, "ends_at must be in the future")
}
