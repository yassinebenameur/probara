package alerter

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/anomaly"
	"github.com/yassinebenameur/probara/shared/maintenance"
	"github.com/yassinebenameur/probara/shared/notifications"
)

// anomalyConfig is the effective latency-anomaly config for one monitor,
// sourced from its tenant's workspace settings.
type anomalyConfig struct {
	monitorID     uuid.UUID
	tenantID      uuid.UUID
	monitorName   string
	baselineHours int
	sensitivity   float64
	breachSeconds int
	minDeltaPct   float64
}

// evaluateLatencyAnomalies compares each anomaly-enabled monitor's recent
// latency against its rolling baseline and opens or resolves a latency_anomaly
// alert. It is orthogonal to the availability state machine: a monitor that is
// 'up' can still be flagged as degraded.
func (a *Alerter) evaluateLatencyAnomalies(ctx context.Context) error {
	if !a.config.LatencyAnomalyEnabled {
		return nil
	}

	configs, err := a.loadAnomalyConfigs(ctx)
	if err != nil {
		return fmt.Errorf("load anomaly configs: %w", err)
	}
	if len(configs) == 0 {
		// Nothing enabled — but still resolve any lingering latency alerts whose
		// monitor no longer has an enabled policy.
		return a.resolveOrphanLatencyAlerts(ctx, nil)
	}

	monitorIDs := make([]uuid.UUID, 0, len(configs))
	maxBaselineHours := 0
	maxBreachSeconds := 0
	for _, c := range configs {
		monitorIDs = append(monitorIDs, c.monitorID)
		if c.baselineHours > maxBaselineHours {
			maxBaselineHours = c.baselineHours
		}
		if c.breachSeconds > maxBreachSeconds {
			maxBreachSeconds = c.breachSeconds
		}
	}

	baselines, err := a.loadAnomalyBaselines(ctx, monitorIDs, maxBaselineHours)
	if err != nil {
		return fmt.Errorf("load anomaly baselines: %w", err)
	}
	recent, err := a.loadRecentLatencies(ctx, monitorIDs, maxBreachSeconds)
	if err != nil {
		return fmt.Errorf("load recent latencies: %w", err)
	}

	now := time.Now()
	for _, c := range configs {
		series := baselines[c.monitorID]
		// Restrict baseline to this monitor's own window.
		baseline := withinHours(series, c.baselineHours, now)
		mean, count := meanWithin(recent[c.monitorID], c.breachSeconds, now)

		verdict := anomaly.Detect(baseline, mean, count, anomaly.Config{
			Sensitivity: c.sensitivity,
			MinDeltaPct: c.minDeltaPct,
		})

		if verdict.Anomalous {
			if err := a.openLatencyAnomalyAlert(ctx, c, verdict); err != nil {
				a.logger.WithError(err).WithFields(map[string]interface{}{"monitor_id": c.monitorID}).Error("Failed to open latency anomaly alert")
			}
		} else {
			if err := a.resolveLatencyAnomalyAlert(ctx, c.monitorID, c.tenantID, c.monitorName, now); err != nil {
				a.logger.WithError(err).WithFields(map[string]interface{}{"monitor_id": c.monitorID}).Error("Failed to resolve latency anomaly alert")
			}
		}
	}

	// Resolve latency alerts for monitors that dropped out of the enabled set
	// entirely (policy disabled, monitor deleted/disabled).
	return a.resolveOrphanLatencyAlerts(ctx, monitorIDs)
}

// latencyPoint is a single recent successful check latency sample.
type latencyPoint struct {
	latencyMs float64
	createdAt time.Time
}

// loadAnomalyConfigs returns one config per monitor whose tenant has latency
// anomaly detection enabled. Monitors in an active maintenance window are
// excluded so degradation during planned work stays quiet.
func (a *Alerter) loadAnomalyConfigs(ctx context.Context) ([]anomalyConfig, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT m.id, m.tenant_id, m.name,
			t.latency_baseline_window_hours, t.latency_anomaly_sensitivity,
			t.latency_anomaly_min_breach_seconds, t.latency_anomaly_min_delta_pct
		FROM monitors m
		JOIN tenants t ON t.id = m.tenant_id
		WHERE t.latency_anomaly_enabled = TRUE
		  AND m.enabled = TRUE AND m.deleted_at IS NULL
		  AND m.type <> 'group'
		  AND NOT `+maintenance.InMaintenancePredicate("m")+`
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []anomalyConfig
	for rows.Next() {
		var c anomalyConfig
		if err := rows.Scan(&c.monitorID, &c.tenantID, &c.monitorName,
			&c.baselineHours, &c.sensitivity, &c.breachSeconds, &c.minDeltaPct); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// loadAnomalyBaselines returns per-monitor hourly average latencies over the
// trailing window, oldest-first.
func (a *Alerter) loadAnomalyBaselines(ctx context.Context, monitorIDs []uuid.UUID, hours int) (map[uuid.UUID][]latencyPoint, error) {
	result := make(map[uuid.UUID][]latencyPoint)
	if len(monitorIDs) == 0 || hours <= 0 {
		return result, nil
	}
	rows, err := a.db.QueryContext(ctx, `
		SELECT monitor_id, bucket_hour,
			latency_success_sum_ms / NULLIF(latency_success_count, 0)
		FROM monitor_hourly_rollups
		WHERE monitor_id = ANY($1)
		  AND bucket_hour >= NOW() - ($2 * INTERVAL '1 hour')
		  AND latency_success_count > 0
		ORDER BY monitor_id, bucket_hour
	`, pq.Array(monitorIDs), hours)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var mid uuid.UUID
		var bucket time.Time
		var avg sql.NullFloat64
		if err := rows.Scan(&mid, &bucket, &avg); err != nil {
			return nil, err
		}
		if !avg.Valid {
			continue
		}
		result[mid] = append(result[mid], latencyPoint{latencyMs: avg.Float64, createdAt: bucket})
	}
	return result, rows.Err()
}

// loadRecentLatencies returns per-monitor successful-check latencies over the
// trailing breach window.
func (a *Alerter) loadRecentLatencies(ctx context.Context, monitorIDs []uuid.UUID, seconds int) (map[uuid.UUID][]latencyPoint, error) {
	result := make(map[uuid.UUID][]latencyPoint)
	if len(monitorIDs) == 0 || seconds <= 0 {
		return result, nil
	}
	rows, err := a.db.QueryContext(ctx, `
		SELECT monitor_id, latency_ms, created_at
		FROM check_results
		WHERE monitor_id = ANY($1)
		  AND result_source = 'monitor'
		  AND status = 'success'
		  AND latency_ms IS NOT NULL
		  AND created_at >= NOW() - ($2 * INTERVAL '1 second')
	`, pq.Array(monitorIDs), seconds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var mid uuid.UUID
		var latency int
		var createdAt time.Time
		if err := rows.Scan(&mid, &latency, &createdAt); err != nil {
			return nil, err
		}
		result[mid] = append(result[mid], latencyPoint{latencyMs: float64(latency), createdAt: createdAt})
	}
	return result, rows.Err()
}

// withinHours returns the latency values of points within the trailing window.
func withinHours(points []latencyPoint, hours int, now time.Time) []float64 {
	if hours <= 0 {
		return nil
	}
	cutoff := now.Add(-time.Duration(hours) * time.Hour)
	out := make([]float64, 0, len(points))
	for _, p := range points {
		if p.createdAt.After(cutoff) {
			out = append(out, p.latencyMs)
		}
	}
	return out
}

// meanWithin returns the mean latency and sample count of points within the
// trailing breach window. A zero count means there is nothing recent to judge.
func meanWithin(points []latencyPoint, seconds int, now time.Time) (float64, int) {
	if seconds <= 0 {
		return 0, 0
	}
	cutoff := now.Add(-time.Duration(seconds) * time.Second)
	var sum float64
	var count int
	for _, p := range points {
		if p.createdAt.After(cutoff) {
			sum += p.latencyMs
			count++
		}
	}
	if count == 0 {
		return 0, 0
	}
	return sum / float64(count), count
}

// openLatencyAnomalyAlert opens a latency_anomaly alert for the monitor if one
// is not already open. Idempotent via the (monitor_id, kind) partial unique
// index, so it is safe across alerter replicas.
func (a *Alerter) openLatencyAnomalyAlert(ctx context.Context, c anomalyConfig, v anomaly.Verdict) error {
	baseline := v.BaselineMs
	observed := v.ObservedMs
	score := v.Score

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin latency alert transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var alertID uuid.UUID
	err = tx.QueryRowContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, kind, status,
			triggered_at, failure_count, baseline_latency_ms, observed_latency_ms, anomaly_score,
			created_at, updated_at)
		VALUES ($1, $2, $3, NULL, 'latency_anomaly', 'active', NOW(), 0, $4, $5, $6, NOW(), NOW())
		ON CONFLICT (monitor_id, kind, (COALESCE(metric_name, ''))) WHERE status IN ('active', 'acknowledged') DO NOTHING
		RETURNING id
	`, uuid.New(), c.tenantID, c.monitorID, baseline, observed, score).Scan(&alertID)
	if err == sql.ErrNoRows {
		return nil // already open
	}
	if err != nil {
		return fmt.Errorf("insert latency alert: %w", err)
	}

	var autoIncident bool
	if err := tx.QueryRowContext(ctx,
		`SELECT auto_create_incident FROM tenants WHERE id = $1`, c.tenantID).Scan(&autoIncident); err != nil {
		return fmt.Errorf("load incident toggle: %w", err)
	}
	if autoIncident {
		record := alertRecord{ID: alertID, TenantID: c.tenantID, MonitorID: c.monitorID, Kind: notifications.KindLatencyAnomaly}
		binding := policyBinding{MonitorID: c.monitorID, TenantID: c.tenantID, MonitorName: c.monitorName, CreateIncidentOnFire: true}
		if err := a.ensureAutoIncidentForAlertTx(ctx, tx, binding, &record); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit latency alert transaction: %w", err)
	}

	record := alertRecord{
		ID: alertID, TenantID: c.tenantID, MonitorID: c.monitorID,
		Kind: notifications.KindLatencyAnomaly, TriggeredAt: time.Now(),
		BaselineLatencyMs: &baseline, ObservedLatencyMs: &observed, AnomalyScore: &score,
	}
	binding := policyBinding{MonitorID: c.monitorID, TenantID: c.tenantID, MonitorName: c.monitorName}
	a.publishAlertEvent(ctx, "created", binding, &record, nil)
	return nil
}

// resolveLatencyAnomalyAlert resolves the open latency_anomaly alert for a
// monitor whose latency has returned to baseline, notifying channels that fired.
func (a *Alerter) resolveLatencyAnomalyAlert(ctx context.Context, monitorID, tenantID uuid.UUID, monitorName string, now time.Time) error {
	var alertID uuid.UUID
	var triggeredAt time.Time
	err := a.db.QueryRowContext(ctx, `
		SELECT id, triggered_at FROM alerts
		WHERE monitor_id = $1 AND kind = 'latency_anomaly' AND status IN ('active', 'acknowledged')
	`, monitorID).Scan(&alertID, &triggeredAt)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("query open latency alert: %w", err)
	}

	resolvedAt, err := a.resolveAlert(ctx, alertID, now)
	if err != nil {
		return err
	}
	record := alertRecord{ID: alertID, TenantID: tenantID, MonitorID: monitorID, Kind: notifications.KindLatencyAnomaly, TriggeredAt: triggeredAt}
	binding := policyBinding{MonitorID: monitorID, TenantID: tenantID, MonitorName: monitorName}
	a.publishAlertEvent(ctx, "resolved", binding, &record, &resolvedAt)
	a.notifyFiredChannels(ctx, "resolved", binding, &record)
	return nil
}

// resolveOrphanLatencyAlerts resolves open latency_anomaly alerts whose monitor
// is no longer in the evaluated set (policy disabled, monitor deleted/disabled).
// keep is the set of monitor IDs that were evaluated this tick.
func (a *Alerter) resolveOrphanLatencyAlerts(ctx context.Context, keep []uuid.UUID) error {
	rows, err := a.db.QueryContext(ctx, `
		SELECT al.id, al.tenant_id, al.monitor_id, m.name, al.triggered_at
		FROM alerts al
		JOIN monitors m ON m.id = al.monitor_id
		WHERE al.kind = 'latency_anomaly'
		  AND al.status IN ('active', 'acknowledged')
		  AND NOT (al.monitor_id = ANY($1))
		  AND NOT `+maintenance.InMaintenancePredicate("m")+`
	`, pq.Array(keep))
	if err != nil {
		return fmt.Errorf("query orphan latency alerts: %w", err)
	}
	defer rows.Close()

	type orphan struct {
		id          uuid.UUID
		tenantID    uuid.UUID
		monitorID   uuid.UUID
		monitorName string
		triggeredAt time.Time
	}
	var orphans []orphan
	for rows.Next() {
		var o orphan
		if err := rows.Scan(&o.id, &o.tenantID, &o.monitorID, &o.monitorName, &o.triggeredAt); err != nil {
			return fmt.Errorf("scan orphan latency alert: %w", err)
		}
		orphans = append(orphans, o)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	now := time.Now()
	for _, o := range orphans {
		resolvedAt, err := a.resolveAlert(ctx, o.id, now)
		if err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{"alert_id": o.id}).Error("Failed to resolve orphan latency alert")
			continue
		}
		record := alertRecord{ID: o.id, TenantID: o.tenantID, MonitorID: o.monitorID, Kind: notifications.KindLatencyAnomaly, TriggeredAt: o.triggeredAt}
		binding := policyBinding{MonitorID: o.monitorID, TenantID: o.tenantID, MonitorName: o.monitorName}
		a.publishAlertEvent(ctx, "resolved", binding, &record, &resolvedAt)
		a.notifyFiredChannels(ctx, "resolved", binding, &record)
	}
	return nil
}

// setLatencyMetrics copies nullable latency-anomaly scan columns onto a record.
func setLatencyMetrics(record *alertRecord, baseline, observed, score sql.NullFloat64) {
	if baseline.Valid {
		v := baseline.Float64
		record.BaselineLatencyMs = &v
	}
	if observed.Valid {
		v := observed.Float64
		record.ObservedLatencyMs = &v
	}
	if score.Valid {
		v := score.Float64
		record.AnomalyScore = &v
	}
}
