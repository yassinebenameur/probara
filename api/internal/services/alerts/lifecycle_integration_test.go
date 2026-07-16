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

func TestListAlerts_ExcludesAlertsForSoftDeletedMonitors(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")
	live := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "live")
	gone := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "gone")

	policyID := uuid.New()
	_, err := dbClient.ExecContext(ctx, `
		INSERT INTO alert_policies (id, tenant_id, name, failure_threshold, failure_window_seconds, created_at, updated_at)
		VALUES ($1, $2, 'p', 3, 300, NOW(), NOW())
	`, policyID, tenantID)
	require.NoError(t, err)

	for _, mid := range []uuid.UUID{live, gone} {
		_, err := dbClient.ExecContext(ctx, `
			INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, status,
			    triggered_at, failure_count, created_at, updated_at)
			VALUES (gen_random_uuid(), $1, $2, $3, 'active', NOW(), 3, NOW(), NOW())
		`, tenantID, mid, policyID)
		require.NoError(t, err)
	}

	_, err = dbClient.ExecContext(ctx,
		`UPDATE monitors SET deleted_at = NOW() WHERE id = $1`, gone)
	require.NoError(t, err)

	svc := alertsvc.NewService(dbClient, nil)
	resp, err := svc.ListAlerts(ctx, tenantID, &models.AlertListParams{})
	require.NoError(t, err)

	for _, a := range resp.Items {
		require.NotEqual(t, gone, a.MonitorID,
			"alerts for soft-deleted monitors must not appear in Items")
	}
	require.Len(t, resp.Items, 1, "only the live monitor's alert should be visible")
	require.Equal(t, 1, resp.Total,
		"Total must match filtered Items count — count query and list query must agree")
}
