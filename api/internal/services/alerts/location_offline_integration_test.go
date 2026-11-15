//go:build !unit

package alerts_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	alertsvc "github.com/yassinebenameur/probara/api/internal/services/alerts"
	"github.com/yassinebenameur/probara/shared/alertrouting"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// A location_offline alert lists and reads back with its location as the
// subject (no monitor), is found by the location's name, and acknowledges
// and resolves like any other alert. A mesh edge with a location that
// stopped reporting reads back suppressed for "location_offline", the same
// predicate the alerter's mesh dispatch excludes on.
func TestLocationOfflineAlertReadPaths(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbc, "loc-offline-api")
	otherTenant := testutil.InsertTenant(ctx, t, dbc, "loc-offline-api-other")
	insertLocation := func(tenant uuid.UUID, name string, seenAgo time.Duration) uuid.UUID {
		t.Helper()
		id := uuid.New()
		_, err := dbc.ExecContext(ctx, `
			INSERT INTO locations (id, tenant_id, name, slug, last_seen_at)
			VALUES ($1, $2, $3, $3, NOW() - make_interval(secs => $4))`, id, tenant, name, seenAgo.Seconds())
		require.NoError(t, err)
		return id
	}
	factory := insertLocation(tenantID, "factory-floor", 10*time.Minute)
	hq := insertLocation(tenantID, "hq", 5*time.Second)
	foreign := insertLocation(otherTenant, "factory-floor", 10*time.Minute)

	insertOffline := func(tenant, location uuid.UUID) uuid.UUID {
		t.Helper()
		id := uuid.New()
		_, err := dbc.ExecContext(ctx, `
			INSERT INTO alerts (id, tenant_id, monitor_id, kind, source_location_id,
				status, triggered_at, failure_count, created_at, updated_at)
			VALUES ($1, $2, NULL, 'location_offline', $3, 'active', NOW(), 0, NOW(), NOW())`, id, tenant, location)
		require.NoError(t, err)
		return id
	}
	offlineAlert := insertOffline(tenantID, factory)
	insertOffline(otherTenant, foreign)

	// One open alert per location: a second insert collides.
	_, err := dbc.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, kind, source_location_id,
			status, triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, NULL, 'location_offline', $3, 'active', NOW(), 0, NOW(), NOW())`, uuid.New(), tenantID, factory)
	require.Error(t, err, "idx_alerts_one_open_location_offline must allow one open alert per location")

	meshAlert := uuid.New()
	_, err = dbc.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, kind, source_location_id, target_location_id,
			status, triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, NULL, 'mesh_edge', $3, $4, 'active', NOW(), 3, NOW(), NOW())`, meshAlert, tenantID, hq, factory)
	require.NoError(t, err)

	svc := alertsvc.NewService(dbc, nil)

	got, err := svc.GetAlert(ctx, tenantID, offlineAlert)
	require.NoError(t, err)
	require.Equal(t, "location_offline", got.Kind)
	require.Nil(t, got.MonitorID)
	require.NotNil(t, got.SourceLocationName)
	require.Equal(t, "factory-floor", *got.SourceLocationName)
	require.Nil(t, got.TargetLocationID)
	require.Nil(t, got.SuppressionReason, "the location alert itself pages")

	list, err := svc.ListAlerts(ctx, tenantID, &models.AlertListParams{Query: "factory", Page: 1, PageSize: 20}, alertsvc.ListSort{})
	require.NoError(t, err)
	require.Equal(t, 2, list.Total, "the location alert and the mesh edge into it, own tenant only")

	// Sorting by subject reads the location's name, not a NULL at the end.
	sorted, err := svc.ListAlerts(ctx, tenantID, &models.AlertListParams{Page: 1, PageSize: 20}, alertsvc.ListSort{Column: alertsvc.SortMonitor})
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{offlineAlert, meshAlert}, []uuid.UUID{sorted.Items[0].ID, sorted.Items[1].ID},
		"factory-floor sorts before hq → factory-floor")

	mesh, err := svc.GetAlert(ctx, tenantID, meshAlert)
	require.NoError(t, err)
	require.NotNil(t, mesh.SuppressionReason)
	require.Equal(t, alertrouting.SuppressionReasonLocationOffline, *mesh.SuppressionReason)

	suppressed := true
	held, err := svc.ListAlerts(ctx, tenantID, &models.AlertListParams{Suppressed: &suppressed, Page: 1, PageSize: 20}, alertsvc.ListSort{})
	require.NoError(t, err)
	require.Len(t, held.Items, 1)
	require.Equal(t, meshAlert, held.Items[0].ID)

	// The location reports again: the edge is no longer held.
	_, err = dbc.ExecContext(ctx, `UPDATE locations SET last_seen_at = NOW() WHERE id = $1`, factory)
	require.NoError(t, err)
	mesh, err = svc.GetAlert(ctx, tenantID, meshAlert)
	require.NoError(t, err)
	require.Nil(t, mesh.SuppressionReason)
}
