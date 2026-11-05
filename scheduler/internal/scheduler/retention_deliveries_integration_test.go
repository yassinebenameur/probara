//go:build !unit

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	sharetest "github.com/yassinebenameur/probara/shared/testutil"
)

// Delivery history is kept deliverylog.RetentionDays (90) for every
// workspace, including one that keeps telemetry forever.
func TestRetentionCleanup_PrunesDeliveryLogAfter90Days(t *testing.T) {
	ctx := context.Background()
	database, cleanup := sharetest.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	s := newTestScheduler(database)
	jobCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	t.Cleanup(cancel)
	s.ctx = jobCtx
	s.config.RetentionCleanupEnabled = true

	tenant := sharetest.InsertTenant(ctx, t, database, "delivery-retention")
	_, err := database.ExecContext(ctx, `UPDATE tenants SET data_retention_days = 0 WHERE id = $1`, tenant)
	require.NoError(t, err)
	monitor := sharetest.InsertHTTPMonitor(ctx, t, database, tenant, "delivery-retention")
	alertID, channelID := uuid.New(), uuid.New()
	_, err = database.ExecContext(ctx, `
		INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, 'ops', 'webhook', '{}'::jsonb, TRUE, NOW(), NOW())`, channelID, tenant)
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, kind, status,
			triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, NULL, 'availability', 'active', NOW(), 3, NOW(), NOW())`, alertID, tenant, monitor)
	require.NoError(t, err)
	for _, age := range []string{"100 days", "91 days", "89 days", "1 hour"} {
		_, err = database.ExecContext(ctx, `
			INSERT INTO alert_notification_deliveries (tenant_id, alert_id, channel_id, channel_name,
				event_type, delivery_key, attempt, path, outcome, created_at)
			VALUES ($1, $2, $3, 'ops', 'created', 'created', 1, 'sync', 'delivered', NOW() - $4::interval)`,
			tenant, alertID, channelID, age)
		require.NoError(t, err)
	}

	now := time.Now().UTC().Truncate(24 * time.Hour).Add(time.Duration(s.config.RetentionCleanupHourUTC+1) * time.Hour)
	require.True(t, s.beginRetentionCleanup(now))
	executed, deleted, pending, err := s.runRetentionCleanup()
	require.NoError(t, err)
	require.True(t, executed)
	require.False(t, pending)
	require.EqualValues(t, 2, deleted)
	require.Equal(t, 2, countRows(ctx, t, database, `SELECT COUNT(*) FROM alert_notification_deliveries`))
	require.Zero(t, countRows(ctx, t, database,
		`SELECT COUNT(*) FROM alert_notification_deliveries WHERE created_at < NOW() - INTERVAL '90 days'`))
}
