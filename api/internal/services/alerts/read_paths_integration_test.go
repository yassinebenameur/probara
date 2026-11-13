//go:build !unit

package alerts_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	alertsvc "github.com/yassinebenameur/probara/api/internal/services/alerts"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// TestLifecycleAlertIsVisibleOnEveryReadPath pins that an alert as the alerter
// writes it — a bare row with no optional references — comes back from every
// read path and can be acknowledged and resolved. An INNER JOIN on an optional
// reference once dropped these rows from all of them.
func TestLifecycleAlertIsVisibleOnEveryReadPath(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "lifecycle-alert")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "my-monitor")

	// The row shape the alerter inserts for a lifecycle alert.
	alertID := uuid.New()
	_, err := dbClient.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, status,
			triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, 'active', NOW(), 1, NOW(), NOW())
	`, alertID, tenantID, monitorID)
	require.NoError(t, err, "insert lifecycle alert")

	svc := alertsvc.NewService(dbClient, nil)

	t.Run("ListAlerts returns lifecycle alert", func(t *testing.T) {
		resp, err := svc.ListAlerts(ctx, tenantID, &models.AlertListParams{}, alertsvc.ListSort{})
		require.NoError(t, err)
		require.Equal(t, 1, resp.Total, "Total must include lifecycle alert")
		require.Len(t, resp.Items, 1, "Items must include lifecycle alert")
		require.Equal(t, alertID, resp.Items[0].ID)
	})

	t.Run("GetAlert returns lifecycle alert", func(t *testing.T) {
		got, err := svc.GetAlert(ctx, tenantID, alertID)
		require.NoError(t, err)
		require.Equal(t, alertID, got.ID)
	})

	t.Run("GetRecentAlerts returns lifecycle alert", func(t *testing.T) {
		alerts, err := svc.GetRecentAlerts(ctx, tenantID, 10)
		require.NoError(t, err)
		require.Len(t, alerts, 1)
		require.Equal(t, alertID, alerts[0].ID)
	})

	t.Run("AcknowledgeAlert succeeds and returns lifecycle alert", func(t *testing.T) {
		got, err := svc.AcknowledgeAlert(ctx, tenantID, alertID)
		require.NoError(t, err, "AcknowledgeAlert must not fail for lifecycle alert")
		require.Equal(t, alertID, got.ID)
		require.Equal(t, models.AlertStatusAcknowledged, got.Status)
	})

	t.Run("ResolveAlert succeeds and returns lifecycle alert", func(t *testing.T) {
		got, err := svc.ResolveAlert(ctx, tenantID, alertID)
		require.NoError(t, err, "ResolveAlert must not fail for lifecycle alert")
		require.Equal(t, alertID, got.ID)
		require.Equal(t, models.AlertStatusResolved, got.Status)
	})
}
