package alerts_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	alertsvc "github.com/yassinebenameur/probara/api/internal/services/alerts"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// TestAlertsWithoutPolicyAreVisible is a regression test for the bug where
// alerts with alert_policy_id = NULL (lifecycle alerts) were silently dropped
// by INNER JOINs on alert_policies in every read path.
func TestAlertsWithoutPolicyAreVisible(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "null-policy")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "my-monitor")

	// INSERT an alert with alert_policy_id = NULL (lifecycle alert)
	alertID := uuid.New()
	_, err := dbClient.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, status,
			triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, NULL, 'active', NOW(), 1, NOW(), NOW())
	`, alertID, tenantID, monitorID)
	require.NoError(t, err, "insert NULL-policy alert")

	svc := alertsvc.NewService(dbClient, nil)

	t.Run("ListAlerts returns NULL-policy alert", func(t *testing.T) {
		resp, err := svc.ListAlerts(ctx, tenantID, &models.AlertListParams{})
		require.NoError(t, err)
		require.Equal(t, 1, resp.Total, "Total must include NULL-policy alert")
		require.Len(t, resp.Items, 1, "Items must include NULL-policy alert")
		require.Equal(t, alertID, resp.Items[0].ID)
	})

	t.Run("GetAlert returns NULL-policy alert", func(t *testing.T) {
		got, err := svc.GetAlert(ctx, tenantID, alertID)
		require.NoError(t, err)
		require.Equal(t, alertID, got.ID)
	})

	t.Run("GetRecentAlerts returns NULL-policy alert", func(t *testing.T) {
		alerts, err := svc.GetRecentAlerts(ctx, tenantID, 10)
		require.NoError(t, err)
		require.Len(t, alerts, 1)
		require.Equal(t, alertID, alerts[0].ID)
	})

	t.Run("AcknowledgeAlert succeeds and returns NULL-policy alert", func(t *testing.T) {
		got, err := svc.AcknowledgeAlert(ctx, tenantID, alertID)
		require.NoError(t, err, "AcknowledgeAlert must not fail for NULL-policy alert")
		require.Equal(t, alertID, got.ID)
		require.Equal(t, models.AlertStatusAcknowledged, got.Status)
	})

	t.Run("ResolveAlert succeeds and returns NULL-policy alert", func(t *testing.T) {
		got, err := svc.ResolveAlert(ctx, tenantID, alertID)
		require.NoError(t, err, "ResolveAlert must not fail for NULL-policy alert")
		require.Equal(t, alertID, got.ID)
		require.Equal(t, models.AlertStatusResolved, got.Status)
	})
}
