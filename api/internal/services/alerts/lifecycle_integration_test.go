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

func TestListAlerts_SinceKeepsAlertsOpenDuringTheWindow(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "m")

	insert := func(status, triggeredAgo string, resolvedAgo *string) uuid.UUID {
		id := uuid.New()
		_, err := dbClient.ExecContext(ctx, `
			INSERT INTO alerts (id, tenant_id, monitor_id, status, triggered_at, resolved_at,
			    failure_count, created_at, updated_at)
			VALUES ($1, $2, $3, $4, NOW() - $5::interval,
			    CASE WHEN $6::text IS NULL THEN NULL ELSE NOW() - $6::interval END,
			    3, NOW(), NOW())
		`, id, tenantID, monitorID, status, triggeredAgo, resolvedAgo)
		require.NoError(t, err)
		return id
	}
	days := func(s string) *string { return &s }

	stillOpen := insert("active", "30 days", nil)
	resolvedInWindow := insert("resolved", "30 days", days("1 day"))
	triggeredInWindow := insert("resolved", "2 days", days("1 day"))
	insert("resolved", "30 days", days("20 days")) // closed before the window

	since := time.Now().Add(-7 * 24 * time.Hour)
	svc := alertsvc.NewService(dbClient, nil)
	resp, err := svc.ListAlerts(ctx, tenantID, &models.AlertListParams{Since: &since})
	require.NoError(t, err)

	got := map[uuid.UUID]bool{}
	for _, a := range resp.Items {
		got[a.ID] = true
	}
	require.True(t, got[stillOpen], "an alert still open must show in any window")
	require.True(t, got[resolvedInWindow], "an alert resolved inside the window was open during it")
	require.True(t, got[triggeredInWindow])
	require.Len(t, resp.Items, 3, "an alert closed before the window must be excluded")
	require.Equal(t, 3, resp.Total, "count query must apply the same window")
}
