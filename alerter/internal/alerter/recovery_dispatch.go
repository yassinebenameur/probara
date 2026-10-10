package alerter

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// dispatchPendingRecoveries uses the resolved alert and its fired-channel
// states as durable delivery intent. A crash after resolving, a failed Send,
// or a failed queue publish leaves last_event_type unchanged and is retried.
// Successful channels are excluded and the existing transactional claim
// serializes overlapping sweeps on different alerter replicas.
func (a *Alerter) dispatchPendingRecoveries(ctx context.Context) error {
	if !a.recoveryMu.TryLock() {
		return nil
	}
	defer a.recoveryMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	const batchSize = 100
	rows, err := a.db.QueryContext(ctx, `
			SELECT al.id, al.tenant_id, al.monitor_id, COALESCE(m.name, ''),
				al.kind, al.triggered_at, al.resolved_at, al.failure_count, al.last_error,
				al.root_cause_monitor_id, al.root_cause_down_since, rcm.name,
				al.baseline_latency_ms, al.observed_latency_ms, al.anomaly_score,
				al.metric_name, al.metric_value, al.threshold_value, al.failing_locations,
				al.source_location_id, al.target_location_id,
				COALESCE(sl.name, 'unknown'), COALESCE(tl.name, 'unknown')
			FROM alerts al
			LEFT JOIN monitors m ON m.id = al.monitor_id
			LEFT JOIN monitors rcm ON rcm.id = al.root_cause_monitor_id
			LEFT JOIN locations sl ON sl.id = al.source_location_id
			LEFT JOIN locations tl ON tl.id = al.target_location_id
			WHERE al.status = 'resolved' AND al.id > $1
			  AND EXISTS (
				SELECT 1 FROM alert_notification_states ans
				JOIN alert_channels ac ON ac.id = ans.channel_id
				WHERE ans.alert_id = al.id AND ans.last_event_type <> 'resolved'
				  AND ac.is_active = TRUE)
			ORDER BY al.id LIMIT $2
		`, a.recoveryCursor, batchSize)
	if err != nil {
		return fmt.Errorf("query pending recoveries: %w", err)
	}
	type pending struct {
		record  alertRecord
		binding policyBinding
	}
	batch := make([]pending, 0, batchSize)
	for rows.Next() {
		var p pending
		var monitorID, rcID, sourceID, targetID uuid.NullUUID
		var lastError, rcName, metricName sql.NullString
		var rcDownSince sql.NullTime
		var baseline, observed, score, metricValue, thresholdValue sql.NullFloat64
		var locations []byte
		var sourceName, targetName string
		r := &p.record
		if err := rows.Scan(&r.ID, &r.TenantID, &monitorID, &p.binding.MonitorName,
			&r.Kind, &r.TriggeredAt, &r.ResolvedAt, &r.FailureCount, &lastError,
			&rcID, &rcDownSince, &rcName, &baseline, &observed, &score,
			&metricName, &metricValue, &thresholdValue, &locations,
			&sourceID, &targetID, &sourceName, &targetName); err != nil {
			rows.Close()
			return fmt.Errorf("scan pending recovery: %w", err)
		}
		if monitorID.Valid {
			r.MonitorID = monitorID.UUID
		}
		p.binding.MonitorID, p.binding.TenantID = r.MonitorID, r.TenantID
		if lastError.Valid {
			r.LastError = &lastError.String
		}
		setRootCause(r, rcID, rcName, rcDownSince)
		setLatencyMetrics(r, baseline, observed, score)
		setHostMetric(r, metricName, metricValue, thresholdValue)
		r.FailingLocations = parseFailingLocations(locations)
		if sourceID.Valid && targetID.Valid {
			r.SourceLocationID, r.TargetLocationID = &sourceID.UUID, &targetID.UUID
			r.SourceLocationName, r.TargetLocationName = &sourceName, &targetName
			p.binding.MonitorName = fmt.Sprintf("mesh: %s → %s", sourceName, targetName)
		}
		batch = append(batch, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fmt.Errorf("read pending recoveries: %w", err)
	}
	for _, p := range batch {
		if err := ctx.Err(); err != nil {
			return err
		}
		a.notifyFiredChannels(ctx, "resolved", p.binding, &p.record)
		// Keep progress even if this provider exhausted the time budget.
		// Next tick starts after it so failing early IDs cannot starve later
		// alerts. Pending work itself remains durable across restarts.
		a.recoveryCursor = p.record.ID
	}
	if len(batch) < batchSize {
		a.recoveryCursor = uuid.Nil
	}
	return nil
}
