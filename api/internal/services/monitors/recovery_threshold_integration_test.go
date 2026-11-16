//go:build !unit

package monitors_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/api/internal/models"
	monitorsvc "github.com/yassinebenameur/probara/api/internal/services/monitors"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// recovery_threshold (docs/state-semantics.md S-T7) round-trips through
// create, read, update and bulk edit; consecutive_successes reads back the
// stored progress, per monitor and per location.
func TestMonitorRecoveryThresholdRoundTrip(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")
	svc := monitorsvc.NewService(monitorsvc.NewPostgresRepository(dbClient))
	n := func(v int) *int { return &v }
	create := func(name string, recovery *int) *models.Monitor {
		t.Helper()
		m, err := svc.CreateMonitor(ctx, tenantID, &models.CreateMonitorRequest{
			Name: name, Type: models.MonitorTypeHTTP,
			Config:          json.RawMessage(`{"url":"https://example.com","method":"GET"}`),
			IntervalSeconds: 60, TimeoutSeconds: 30, RecoveryThreshold: recovery,
		})
		require.NoError(t, err)
		return m
	}

	defaulted := create("default", nil)
	require.Equal(t, 1, defaulted.RecoveryThreshold, "omitted recovery_threshold is single-sample recovery")

	created := create("confirming", n(3))
	require.Equal(t, 3, created.RecoveryThreshold)
	got, err := svc.GetMonitor(ctx, tenantID, created.ID)
	require.NoError(t, err)
	require.Equal(t, 3, got.RecoveryThreshold)
	require.Equal(t, 0, got.ConsecutiveSuccesses)

	updated, err := svc.UpdateMonitor(ctx, tenantID, created.ID, &models.UpdateMonitorRequest{RecoveryThreshold: n(5)})
	require.NoError(t, err)
	require.Equal(t, 5, updated.RecoveryThreshold)

	count, err := svc.BulkUpdateAlerting(ctx, tenantID, []uuid.UUID{created.ID, defaulted.ID}, nil, n(2), nil, nil)
	require.NoError(t, err)
	require.Equal(t, 2, count)
	list, err := svc.ListMonitors(ctx, tenantID, nil, nil, 1, 10)
	require.NoError(t, err)
	for _, m := range list.Items {
		require.Equal(t, 2, m.RecoveryThreshold, "bulk edit sets every selected monitor (%s)", m.Name)
		require.Equal(t, 2, m.ConsecutiveFailuresThreshold, "bulk edit without a failure threshold leaves it (%s)", m.Name)
	}

	// Recovery progress reads back from the state rows the ingest writes.
	locationID := uuid.New()
	_, err = dbClient.ExecContext(ctx, `INSERT INTO locations (id, tenant_id, name, slug) VALUES ($1, $2, 'paris', 'paris')`, locationID, tenantID)
	require.NoError(t, err)
	_, err = dbClient.ExecContext(ctx, `INSERT INTO monitor_locations (monitor_id, location_id) VALUES ($1, $2)`, created.ID, locationID)
	require.NoError(t, err)
	_, err = dbClient.ExecContext(ctx, `
		INSERT INTO monitor_location_state (monitor_id, location_id, tenant_id, current_state, consecutive_successes, last_check_at)
		VALUES ($1, $2, $3, 'down', 1, NOW())`, created.ID, locationID, tenantID)
	require.NoError(t, err)
	_, err = dbClient.ExecContext(ctx, `UPDATE monitors SET current_state = 'down', consecutive_successes = 1 WHERE id = $1`, created.ID)
	require.NoError(t, err)
	got, err = svc.GetMonitor(ctx, tenantID, created.ID)
	require.NoError(t, err)
	require.Equal(t, 1, got.ConsecutiveSuccesses)
	require.Len(t, got.Locations, 1)
	require.Equal(t, 1, got.Locations[0].ConsecutiveSuccesses)
}

// S-T7 across location changes: a monitor that is down (or recovering) hands
// the confirmation it owes to every stream it moves to, in both directions,
// so adding a location cannot skip it.
func TestSetLocationsCarriesOwedRecovery_Integration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "moving")
	locationID := uuid.New()
	_, err := dbClient.ExecContext(ctx, `INSERT INTO locations (id, tenant_id, name, slug) VALUES ($1, $2, 'paris', 'paris')`, locationID, tenantID)
	require.NoError(t, err)
	// Location-less, down and recovering (1 of 3).
	_, err = dbClient.ExecContext(ctx, `
		UPDATE monitors SET current_state = 'down', consecutive_successes = 1, recovery_threshold = 3 WHERE id = $1`, monitorID)
	require.NoError(t, err)
	repo := monitorsvc.NewPostgresRepository(dbClient)

	require.NoError(t, repo.SetLocations(ctx, tenantID, monitorID, []uuid.UUID{locationID}))
	var state string
	var successes int
	var pending bool
	require.NoError(t, dbClient.QueryRowContext(ctx, `
		SELECT current_state, consecutive_successes, recovery_pending FROM monitor_location_state
		WHERE monitor_id = $1 AND location_id = $2`, monitorID, locationID).Scan(&state, &successes, &pending))
	require.Equal(t, "unknown", state)
	require.True(t, pending, "a location added to a down monitor owes the confirmation [S-T7]")
	require.NoError(t, dbClient.QueryRowContext(ctx,
		`SELECT consecutive_successes, recovery_pending FROM monitors WHERE id = $1`, monitorID).Scan(&successes, &pending))
	require.Equal(t, 0, successes, "progress now comes from the locations, none fresh yet")
	require.False(t, pending, "the memory moved to the location")

	// Back to the default fleet while still down: the monitor's own stream
	// owes the confirmation again.
	_, err = dbClient.ExecContext(ctx, `UPDATE monitors SET current_state = 'down' WHERE id = $1`, monitorID)
	require.NoError(t, err)
	require.NoError(t, repo.SetLocations(ctx, tenantID, monitorID, nil))
	require.NoError(t, dbClient.QueryRowContext(ctx,
		`SELECT current_state, recovery_pending FROM monitors WHERE id = $1`, monitorID).Scan(&state, &pending))
	require.Equal(t, "unknown", state)
	require.True(t, pending, "a down monitor returning to the default fleet owes the confirmation [S-T7]")

	// An up monitor gaining a location owes nothing.
	_, err = dbClient.ExecContext(ctx, `UPDATE monitors SET current_state = 'up', recovery_pending = FALSE WHERE id = $1`, monitorID)
	require.NoError(t, err)
	require.NoError(t, repo.SetLocations(ctx, tenantID, monitorID, []uuid.UUID{locationID}))
	var rows int
	require.NoError(t, dbClient.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM monitor_location_state WHERE monitor_id = $1`, monitorID).Scan(&rows))
	require.Equal(t, 0, rows, "no seeded state for an up monitor")
}
