//go:build !unit

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/shared/notifications/deliverylog"
	sharetest "github.com/yassinebenameur/probara/shared/testutil"
)

// The delivery history is kept deliverylog.RetentionDays for every
// workspace, including one that keeps telemetry forever (0) and one with a
// shorter data_retention_days, and pruned in bounded batches.
func TestRetentionCleanup_PrunesDeliveryHistoryAfter90Days(t *testing.T) {
	ctx := context.Background()
	database, cleanup := sharetest.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	s := newTestScheduler(database)
	jobCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	t.Cleanup(cancel)
	s.ctx = jobCtx
	s.config.RetentionCleanupEnabled = true
	s.config.RetentionCleanupHourUTC = 2
	s.config.RetentionCleanupBatchSize = 2
	s.config.RetentionCleanupMaxRowsPerRun = 3

	insert := func(tenant, alertID, channelID uuid.UUID, age time.Duration, n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			_, err := database.ExecContext(ctx, `
				INSERT INTO alert_notification_deliveries (tenant_id, alert_id, channel_id, event_type, attempt, path, outcome, created_at)
				VALUES ($1, $2, $3, 'created', 1, 'sync', 'delivered', NOW() - ($4::double precision * INTERVAL '1 second'))`,
				tenant, alertID, channelID, age.Seconds())
			require.NoError(t, err)
		}
	}
	setup := func(name string, retentionDays int) uuid.UUID {
		tenant := sharetest.InsertTenant(ctx, t, database, name)
		_, err := database.ExecContext(ctx, `UPDATE tenants SET data_retention_days = $2 WHERE id = $1`, tenant, retentionDays)
		require.NoError(t, err)
		monitor := sharetest.InsertHTTPMonitor(ctx, t, database, tenant, name)
		alertID, channelID := uuid.New(), uuid.New()
		_, err = database.ExecContext(ctx, `INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, failure_count, created_at, updated_at)
			VALUES ($1, $2, $3, 'availability', 'active', NOW(), 1, NOW(), NOW())`, alertID, tenant, monitor)
		require.NoError(t, err)
		_, err = database.ExecContext(ctx, `INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
			VALUES ($1, $2, 'hook', 'webhook', '{}'::jsonb, TRUE, NOW(), NOW())`, channelID, tenant)
		require.NoError(t, err)
		day := 24 * time.Hour
		insert(tenant, alertID, channelID, time.Duration(deliverylog.RetentionDays+5)*day, 4)
		insert(tenant, alertID, channelID, time.Duration(deliverylog.RetentionDays-5)*day, 1)
		return tenant
	}
	forever := setup("deliveries-forever", 0)
	short := setup("deliveries-short", 30)

	now := time.Now().UTC().Truncate(24 * time.Hour).Add(3 * time.Hour)
	for pass := 0; ; pass++ {
		require.Less(t, pass, 3, "the backlog must drain within the per-run cap")
		require.True(t, s.beginRetentionCleanup(now.Add(time.Duration(pass)*time.Minute)))
		_, _, pending, err := s.runRetentionCleanup()
		require.NoError(t, err)
		s.finishRetentionCleanup(now.Format("2006-01-02"), !pending)
		if !pending {
			break
		}
	}
	for _, tenant := range []uuid.UUID{forever, short} {
		// The 85-day-old row stays: 90 days for everyone, whatever
		// data_retention_days says.
		require.Equal(t, 1, countRows(ctx, t, database,
			`SELECT COUNT(*) FROM alert_notification_deliveries WHERE tenant_id = $1`, tenant))
	}
}
