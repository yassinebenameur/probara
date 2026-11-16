package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/db/pgerr"
	"github.com/yassinebenameur/probara/shared/notifications/deliverylog"
)

// deliveriesCutoff is the prune boundary of the notification delivery
// history: deliverylog.RetentionDays for every workspace, whatever its
// data_retention_days (0 included).
func deliveriesCutoff(_ context.Context, _ int) (time.Time, error) {
	return time.Now().UTC().AddDate(0, 0, -deliverylog.RetentionDays), nil
}

// pruneTenantDeliveries deletes a workspace's alert_notification_deliveries
// rows older than deliveriesCutoff, oldest first, in bounded batches like the
// other retention stores.
func (s *Scheduler) pruneTenantDeliveries(ctx context.Context, tenantID uuid.UUID, _ int) (int64, error) {
	cutoff, _ := deliveriesCutoff(ctx, 0)
	const deleteQuery = `
		DELETE FROM alert_notification_deliveries
		WHERE id IN (
			SELECT id FROM alert_notification_deliveries
			WHERE tenant_id = $1 AND created_at < $2
			ORDER BY created_at ASC
			LIMIT $3
		)`

	var totalDeleted int64
	maxRows := s.retentionMaxRows()
	batchSize := s.config.RetentionCleanupBatchSize
	if batchSize <= 0 {
		batchSize = 5000
	}
	for int(totalDeleted) < maxRows {
		limit := min(batchSize, maxRows-int(totalDeleted))
		result, err := s.db.ExecContext(ctx, deleteQuery, tenantID, cutoff, limit)
		if err != nil {
			if pgerr.IsUndefinedTable(err) {
				// This binary runs ahead of migration 000107.
				return totalDeleted, nil
			}
			return totalDeleted, err
		}
		rowsDeleted, err := result.RowsAffected()
		if err != nil {
			return totalDeleted, fmt.Errorf("failed to read rows affected: %w", err)
		}
		totalDeleted += rowsDeleted
		if rowsDeleted < int64(limit) {
			break
		}
	}
	return totalDeleted, nil
}
