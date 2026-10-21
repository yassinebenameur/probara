//go:build !unit

package maintenancewindows_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	maintenancewindows "github.com/yassinebenameur/probara/api/internal/services/maintenancewindows"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestMaintenanceWindowCRUD(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "mw")
	monitorA := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "api")
	monitorB := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "web")
	otherTenant := testutil.InsertTenant(ctx, t, dbClient, "mw-other")
	foreignMonitor := testutil.InsertHTTPMonitor(ctx, t, dbClient, otherTenant, "foreign")

	svc := maintenancewindows.NewService(dbClient)
	now := time.Now().UTC()

	t.Run("Create validates input", func(t *testing.T) {
		_, err := svc.Create(ctx, tenantID, &models.CreateMaintenanceWindowRequest{
			Title: "", StartsAt: now, EndsAt: now.Add(time.Hour), MonitorIDs: []string{monitorA.String()},
		})
		require.ErrorContains(t, err, "title")

		_, err = svc.Create(ctx, tenantID, &models.CreateMaintenanceWindowRequest{
			Title: "x", StartsAt: now.Add(time.Hour), EndsAt: now, MonitorIDs: []string{monitorA.String()},
		})
		require.ErrorContains(t, err, "ends_at must be after starts_at")

		_, err = svc.Create(ctx, tenantID, &models.CreateMaintenanceWindowRequest{
			Title: "x", StartsAt: now.Add(-2 * time.Hour), EndsAt: now.Add(-time.Hour), MonitorIDs: []string{monitorA.String()},
		})
		require.ErrorContains(t, err, "future")

		_, err = svc.Create(ctx, tenantID, &models.CreateMaintenanceWindowRequest{
			Title: "x", StartsAt: now, EndsAt: now.Add(time.Hour), MonitorIDs: nil,
		})
		require.ErrorContains(t, err, "at least one monitor")

		_, err = svc.Create(ctx, tenantID, &models.CreateMaintenanceWindowRequest{
			Title: "x", StartsAt: now, EndsAt: now.Add(time.Hour), MonitorIDs: []string{uuid.NewString()},
		})
		require.ErrorContains(t, err, "monitors not found")

		// monitor belonging to another tenant must be rejected
		_, err = svc.Create(ctx, tenantID, &models.CreateMaintenanceWindowRequest{
			Title: "x", StartsAt: now, EndsAt: now.Add(time.Hour), MonitorIDs: []string{foreignMonitor.String()},
		})
		require.ErrorContains(t, err, "monitors not found")
	})

	var windowID uuid.UUID
	t.Run("Create persists window with monitors", func(t *testing.T) {
		w, err := svc.Create(ctx, tenantID, &models.CreateMaintenanceWindowRequest{
			Title:       "DB upgrade",
			Description: "planned",
			StartsAt:    now.Add(-time.Minute),
			EndsAt:      now.Add(time.Hour),
			MonitorIDs:  []string{monitorA.String(), monitorB.String()},
		})
		require.NoError(t, err)
		require.Equal(t, models.MaintenanceWindowStatusActive, w.Status)
		require.Len(t, w.MonitorIDs, 2)
		require.Len(t, w.Monitors, 2)
		windowID = w.ID
	})

	t.Run("List filters by status", func(t *testing.T) {
		_, err := svc.Create(ctx, tenantID, &models.CreateMaintenanceWindowRequest{
			Title: "future", StartsAt: now.Add(24 * time.Hour), EndsAt: now.Add(26 * time.Hour),
			MonitorIDs: []string{monitorA.String()},
		})
		require.NoError(t, err)

		active, err := svc.List(ctx, tenantID, "active", uuid.Nil, 1, 20)
		require.NoError(t, err)
		require.Equal(t, 1, active.Total)
		require.Equal(t, "DB upgrade", active.Items[0].Title)

		upcoming, err := svc.List(ctx, tenantID, "upcoming", uuid.Nil, 1, 20)
		require.NoError(t, err)
		require.Equal(t, 1, upcoming.Total)
		require.Equal(t, "future", upcoming.Items[0].Title)

		past, err := svc.List(ctx, tenantID, "past", uuid.Nil, 1, 20)
		require.NoError(t, err)
		require.Equal(t, 0, past.Total)

		_, err = svc.List(ctx, tenantID, "bogus", uuid.Nil, 1, 20)
		require.Error(t, err)
	})

	t.Run("List filters by monitor", func(t *testing.T) {
		forB, err := svc.List(ctx, tenantID, "", monitorB, 1, 20)
		require.NoError(t, err)
		require.Equal(t, 1, forB.Total)
		require.Equal(t, "DB upgrade", forB.Items[0].Title)
	})

	t.Run("Update replaces monitor set", func(t *testing.T) {
		ids := []string{monitorB.String()}
		w, err := svc.Update(ctx, tenantID, windowID, &models.UpdateMaintenanceWindowRequest{
			MonitorIDs: &ids,
		})
		require.NoError(t, err)
		require.Len(t, w.MonitorIDs, 1)
		require.Equal(t, monitorB, w.MonitorIDs[0])
	})

	t.Run("Update rejects inverted time range", func(t *testing.T) {
		badEnd := now.Add(-2 * time.Hour)
		_, err := svc.Update(ctx, tenantID, windowID, &models.UpdateMaintenanceWindowRequest{
			EndsAt: &badEnd,
		})
		require.ErrorContains(t, err, "ends_at must be after starts_at")
	})

	t.Run("Cross-tenant access is not found", func(t *testing.T) {
		_, err := svc.Get(ctx, otherTenant, windowID)
		require.ErrorIs(t, err, maintenancewindows.ErrNotFound)
		err = svc.Delete(ctx, otherTenant, windowID)
		require.ErrorIs(t, err, maintenancewindows.ErrNotFound)
	})

	t.Run("Delete cascades join rows", func(t *testing.T) {
		require.NoError(t, svc.Delete(ctx, tenantID, windowID))
		_, err := svc.Get(ctx, tenantID, windowID)
		require.ErrorIs(t, err, maintenancewindows.ErrNotFound)

		var joinCount int
		require.NoError(t, dbClient.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM maintenance_window_monitors WHERE maintenance_window_id = $1`,
			windowID).Scan(&joinCount))
		require.Equal(t, 0, joinCount)
	})
}

func TestSnoozeMonitorCreatesWindow(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "snooze")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "api")
	svc := maintenancewindows.NewService(dbClient)

	until := time.Now().UTC().Add(30 * time.Minute)
	w, err := svc.SnoozeMonitor(ctx, tenantID, monitorID, until)
	require.NoError(t, err)
	require.Equal(t, models.MaintenanceWindowStatusActive, w.Status)
	require.Equal(t, []uuid.UUID{monitorID}, w.MonitorIDs)
	require.Contains(t, w.Title, "Snooze")
	require.WithinDuration(t, until, w.EndsAt, time.Second)

	_, err = svc.SnoozeMonitor(ctx, tenantID, monitorID, time.Now().Add(-time.Minute))
	require.ErrorContains(t, err, "future")

	_, err = svc.SnoozeMonitor(ctx, tenantID, uuid.New(), until)
	require.ErrorContains(t, err, "monitor not found")
}

// Each status tab lists the window an operator needs next first, so the API's
// page cut never drops it: upcoming by soonest start, active by soonest end,
// past by most recent end.
func TestMaintenanceWindowListOrderPerStatus(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "mw-order")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "api")
	now := time.Now().UTC()

	// Inserted directly: Create refuses windows that already ended.
	insert := func(title string, start, end time.Time) {
		t.Helper()
		id := uuid.New()
		_, err := dbClient.ExecContext(ctx, `
			INSERT INTO maintenance_windows (id, tenant_id, title, description, starts_at, ends_at)
			VALUES ($1, $2, $3, '', $4, $5)`, id, tenantID, title, start, end)
		require.NoError(t, err)
		_, err = dbClient.ExecContext(ctx, `
			INSERT INTO maintenance_window_monitors (maintenance_window_id, monitor_id) VALUES ($1, $2)`, id, monitorID)
		require.NoError(t, err)
	}
	insert("upcoming-far", now.Add(72*time.Hour), now.Add(73*time.Hour))
	insert("upcoming-soon", now.Add(2*time.Hour), now.Add(3*time.Hour))
	insert("active-ends-late", now.Add(-time.Hour), now.Add(5*time.Hour))
	insert("active-ends-soon", now.Add(-3*time.Hour), now.Add(30*time.Minute))
	insert("past-old", now.Add(-72*time.Hour), now.Add(-71*time.Hour))
	insert("past-recent", now.Add(-5*time.Hour), now.Add(-time.Hour))

	svc := maintenancewindows.NewService(dbClient)
	titles := func(status string) []string {
		t.Helper()
		res, err := svc.List(ctx, tenantID, status, uuid.Nil, 1, 20)
		require.NoError(t, err)
		out := make([]string, 0, len(res.Items))
		for _, w := range res.Items {
			out = append(out, w.Title)
		}
		return out
	}

	require.Equal(t, []string{"upcoming-soon", "upcoming-far"}, titles("upcoming"))
	require.Equal(t, []string{"active-ends-soon", "active-ends-late"}, titles("active"))
	require.Equal(t, []string{"past-recent", "past-old"}, titles("past"))

	// A one-row page keeps the most relevant window.
	first, err := svc.List(ctx, tenantID, "upcoming", uuid.Nil, 1, 1)
	require.NoError(t, err)
	require.Equal(t, 2, first.Total)
	require.Equal(t, "upcoming-soon", first.Items[0].Title)
}
