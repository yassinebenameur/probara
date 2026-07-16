package alerts_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	alertsvc "github.com/yassinebenameur/probara/api/internal/services/alerts"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// TestAlertReadPathsReturnRootCause verifies that every alert read path
// surfaces the dependency root-cause annotation (id, down-since, and the
// upstream monitor name resolved via join).
func TestAlertReadPathsReturnRootCause(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "root-cause")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "Backend API")
	upstreamID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "Postgres prod")

	downSince := time.Now().UTC().Add(-5 * time.Minute).Truncate(time.Millisecond)
	alertID := uuid.New()
	_, err := dbClient.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, status,
			triggered_at, failure_count, root_cause_monitor_id, root_cause_down_since,
			created_at, updated_at)
		VALUES ($1, $2, $3, NULL, 'active', NOW(), 1, $4, $5, NOW(), NOW())
	`, alertID, tenantID, monitorID, upstreamID, downSince)
	require.NoError(t, err)

	svc := alertsvc.NewService(dbClient, nil)

	assertRootCause := func(t *testing.T, alert models.AlertWithDetails) {
		t.Helper()
		require.NotNil(t, alert.RootCauseMonitorID)
		require.Equal(t, upstreamID, *alert.RootCauseMonitorID)
		require.NotNil(t, alert.RootCauseMonitorName)
		require.Equal(t, "Postgres prod", *alert.RootCauseMonitorName)
		require.NotNil(t, alert.RootCauseDownSince)
		require.True(t, alert.RootCauseDownSince.UTC().Truncate(time.Millisecond).Equal(downSince))
	}

	t.Run("ListAlerts", func(t *testing.T) {
		resp, err := svc.ListAlerts(ctx, tenantID, &models.AlertListParams{})
		require.NoError(t, err)
		require.Len(t, resp.Items, 1)
		assertRootCause(t, resp.Items[0])
	})

	t.Run("GetAlert", func(t *testing.T) {
		got, err := svc.GetAlert(ctx, tenantID, alertID)
		require.NoError(t, err)
		assertRootCause(t, *got)
	})

	t.Run("GetRecentAlerts", func(t *testing.T) {
		alerts, err := svc.GetRecentAlerts(ctx, tenantID, 10)
		require.NoError(t, err)
		require.Len(t, alerts, 1)
		assertRootCause(t, alerts[0])
	})

	t.Run("GetActiveAlertForMonitor", func(t *testing.T) {
		alert, err := svc.GetActiveAlertForMonitor(ctx, tenantID, monitorID)
		require.NoError(t, err)
		require.NotNil(t, alert)
		require.NotNil(t, alert.RootCauseMonitorID)
		require.Equal(t, upstreamID, *alert.RootCauseMonitorID)
	})
}
