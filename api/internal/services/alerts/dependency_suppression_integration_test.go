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

// TestAlertReadPathsReportDependencySuppression pins the API side of the
// alerter/API agreement: an alert the alerter would suppress (policy on, root
// cause set) reads back with suppression_reason = "dependency", the root cause's
// own alert reads back with impacted_count = 1, and flipping the policy off
// clears both — the same fixture the alerter integration tests dispatch on.
func TestAlertReadPathsReportDependencySuppression(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "dep-suppression")
	upstreamID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "Postgres prod")
	downstreamID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "Backend API")
	_, err := dbClient.ExecContext(ctx,
		`UPDATE tenants SET dependency_suppression_enabled = TRUE WHERE id = $1`, tenantID)
	require.NoError(t, err)

	insertOpenAlert := func(monitorID uuid.UUID, rootCause *uuid.UUID) uuid.UUID {
		id := uuid.New()
		_, err := dbClient.ExecContext(ctx, `
			INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, kind, status,
				triggered_at, failure_count, root_cause_monitor_id, created_at, updated_at)
			VALUES ($1, $2, $3, NULL, 'availability', 'active', NOW(), 1, $4, NOW(), NOW())
		`, id, tenantID, monitorID, rootCause)
		require.NoError(t, err)
		return id
	}
	upstreamAlertID := insertOpenAlert(upstreamID, nil)
	downstreamAlertID := insertOpenAlert(downstreamID, &upstreamID)

	svc := alertsvc.NewService(dbClient, nil)

	byID := func(items []models.AlertWithDetails) map[uuid.UUID]models.AlertWithDetails {
		out := map[uuid.UUID]models.AlertWithDetails{}
		for _, a := range items {
			out[a.ID] = a
		}
		return out
	}

	t.Run("policy on", func(t *testing.T) {
		resp, err := svc.ListAlerts(ctx, tenantID, &models.AlertListParams{})
		require.NoError(t, err)
		got := byID(resp.Items)

		down := got[downstreamAlertID]
		require.NotNil(t, down.SuppressionReason)
		require.Equal(t, alertrouting.SuppressionReasonDependency, *down.SuppressionReason)
		require.Equal(t, 0, down.ImpactedCount)

		up := got[upstreamAlertID]
		require.Nil(t, up.SuppressionReason)
		require.Equal(t, 1, up.ImpactedCount)

		// The same shape on the single-alert and recent paths.
		one, err := svc.GetAlert(ctx, tenantID, downstreamAlertID)
		require.NoError(t, err)
		require.NotNil(t, one.SuppressionReason)
		recent, err := svc.GetRecentAlerts(ctx, tenantID, 10)
		require.NoError(t, err)
		require.Equal(t, 1, byID(recent)[upstreamAlertID].ImpactedCount)

		// suppressed=true / false filters.
		yes := true
		resp, err = svc.ListAlerts(ctx, tenantID, &models.AlertListParams{Suppressed: &yes})
		require.NoError(t, err)
		require.Len(t, resp.Items, 1)
		require.Equal(t, downstreamAlertID, resp.Items[0].ID)
		require.Equal(t, 1, resp.Total)
		no := false
		resp, err = svc.ListAlerts(ctx, tenantID, &models.AlertListParams{Suppressed: &no})
		require.NoError(t, err)
		require.Len(t, resp.Items, 1)
		require.Equal(t, upstreamAlertID, resp.Items[0].ID)
	})

	t.Run("grace window after the root cause clears", func(t *testing.T) {
		_, err := dbClient.ExecContext(ctx, `
			UPDATE alerts SET root_cause_monitor_id = NULL, root_cause_cleared_at = NOW() - interval '30 seconds'
			WHERE id = $1`, downstreamAlertID)
		require.NoError(t, err)
		one, err := svc.GetAlert(ctx, tenantID, downstreamAlertID)
		require.NoError(t, err)
		require.NotNil(t, one.SuppressionReason, "still inside the 120s default grace")

		_, err = dbClient.ExecContext(ctx, `
			UPDATE alerts SET root_cause_cleared_at = NOW() - interval '10 minutes' WHERE id = $1`, downstreamAlertID)
		require.NoError(t, err)
		one, err = svc.GetAlert(ctx, tenantID, downstreamAlertID)
		require.NoError(t, err)
		require.Nil(t, one.SuppressionReason, "grace elapsed")

		// Restore the annotated state for the remaining cases.
		_, err = dbClient.ExecContext(ctx, `
			UPDATE alerts SET root_cause_monitor_id = $2, root_cause_cleared_at = NULL WHERE id = $1`,
			downstreamAlertID, upstreamID)
		require.NoError(t, err)
	})

	t.Run("monitor override off", func(t *testing.T) {
		_, err := dbClient.ExecContext(ctx,
			`UPDATE monitors SET dependency_suppression = 'off' WHERE id = $1`, downstreamID)
		require.NoError(t, err)
		one, err := svc.GetAlert(ctx, tenantID, downstreamAlertID)
		require.NoError(t, err)
		require.Nil(t, one.SuppressionReason)
		up, err := svc.GetAlert(ctx, tenantID, upstreamAlertID)
		require.NoError(t, err)
		require.Equal(t, 0, up.ImpactedCount)
		_, err = dbClient.ExecContext(ctx,
			`UPDATE monitors SET dependency_suppression = 'inherit' WHERE id = $1`, downstreamID)
		require.NoError(t, err)
	})

	t.Run("policy off", func(t *testing.T) {
		_, err := dbClient.ExecContext(ctx,
			`UPDATE tenants SET dependency_suppression_enabled = FALSE WHERE id = $1`, tenantID)
		require.NoError(t, err)
		resp, err := svc.ListAlerts(ctx, tenantID, &models.AlertListParams{})
		require.NoError(t, err)
		for _, a := range resp.Items {
			require.Nil(t, a.SuppressionReason)
			require.Equal(t, 0, a.ImpactedCount)
		}
	})

	t.Run("resolved alerts are never suppressed", func(t *testing.T) {
		_, err := dbClient.ExecContext(ctx,
			`UPDATE tenants SET dependency_suppression_enabled = TRUE WHERE id = $1`, tenantID)
		require.NoError(t, err)
		_, err = dbClient.ExecContext(ctx,
			`UPDATE alerts SET status = 'resolved', resolved_at = $2 WHERE id = $1`, downstreamAlertID, time.Now())
		require.NoError(t, err)
		one, err := svc.GetAlert(ctx, tenantID, downstreamAlertID)
		require.NoError(t, err)
		require.Nil(t, one.SuppressionReason)
	})
}
