package alerter

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/maintenance"
	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/notifications"
)

// hostMetricKinds is the canonical, ordered set of metrics a threshold can
// target. Order keeps alert creation deterministic when several breach at once.
var hostMetricKinds = []string{"cpu", "memory", "disk", "swap"}

// hostMetricConfig is the effective host-metric threshold config for one agent
// monitor, parsed from its monitors.config JSON.
type hostMetricConfig struct {
	monitorID   uuid.UUID
	tenantID    uuid.UUID
	monitorName string
	// thresholds maps a metric name ("cpu", "memory", ...) to its percent
	// threshold (0-100). Only metrics with a positive threshold are present.
	thresholds map[string]float64
}

// metricThresholdConfig mirrors the metric_thresholds block stored in
// monitors.config for agent monitors. All fields are optional; a nil or
// non-positive value means "no threshold for this metric".
type metricThresholdConfig struct {
	CPUPercent    *float64 `json:"cpu_percent"`
	MemoryPercent *float64 `json:"memory_percent"`
	DiskPercent   *float64 `json:"disk_percent"`
	SwapPercent   *float64 `json:"swap_percent"`
}

// agentThresholdConfig is the slice of monitors.config the alerter cares about.
type agentThresholdConfig struct {
	MetricThresholds *metricThresholdConfig `json:"metric_thresholds"`
}

// evaluateHostMetricThresholds compares each agent monitor's latest reported
// host metrics against its configured thresholds and opens or resolves a
// host_metric alert per breaching metric. Like latency anomalies, this is
// orthogonal to the availability state machine: an 'up' host can still be
// flagged for high CPU/memory/disk/swap.
func (a *Alerter) evaluateHostMetricThresholds(ctx context.Context) error {
	configs, err := a.loadHostMetricConfigs(ctx)
	if err != nil {
		return fmt.Errorf("load host metric configs: %w", err)
	}
	if len(configs) == 0 {
		// Nothing configured — still resolve any lingering host_metric alerts.
		return a.resolveOrphanHostMetricAlerts(ctx, nil)
	}

	monitorIDs := make([]uuid.UUID, 0, len(configs))
	for _, c := range configs {
		monitorIDs = append(monitorIDs, c.monitorID)
	}

	latest, err := a.loadLatestAgentMetrics(ctx, monitorIDs)
	if err != nil {
		return fmt.Errorf("load latest agent metrics: %w", err)
	}

	now := time.Now()
	for _, c := range configs {
		m, ok := latest[c.monitorID]
		if !ok {
			// No metrics reported yet; leave any open alerts untouched.
			continue
		}

		breaching := make(map[string]bool, len(c.thresholds))
		for _, metric := range hostMetricKinds {
			threshold, configured := c.thresholds[metric]
			if !configured {
				continue
			}
			value, available := metricUsage(m, metric)
			if !available {
				continue
			}
			if value >= threshold {
				breaching[metric] = true
				if err := a.openHostMetricAlert(ctx, c, metric, value, threshold); err != nil {
					a.logger.WithError(err).WithFields(map[string]interface{}{
						"monitor_id": c.monitorID, "metric": metric,
					}).Error("Failed to open host metric alert")
				}
			}
		}

		// Resolve open host_metric alerts whose metric is no longer breaching
		// (recovered, or threshold removed from config).
		if err := a.resolveClearedHostMetricAlerts(ctx, c, breaching, now); err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{
				"monitor_id": c.monitorID,
			}).Error("Failed to resolve cleared host metric alerts")
		}
	}

	// Resolve host_metric alerts for monitors that dropped out of the evaluated
	// set entirely (thresholds removed, monitor deleted/disabled).
	return a.resolveOrphanHostMetricAlerts(ctx, monitorIDs)
}

// loadHostMetricConfigs returns one config per enabled agent monitor whose
// monitors.config carries a metric_thresholds block. Monitors in an active
// maintenance window are excluded so planned work stays quiet.
func (a *Alerter) loadHostMetricConfigs(ctx context.Context) ([]hostMetricConfig, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT m.id, m.tenant_id, m.name, m.config
		FROM monitors m
		WHERE m.type = 'agent'
		  AND m.enabled = TRUE AND m.deleted_at IS NULL
		  AND m.config ? 'metric_thresholds'
		  AND NOT `+maintenance.InMaintenancePredicate("m")+`
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []hostMetricConfig
	for rows.Next() {
		var (
			id, tenantID uuid.UUID
			name         string
			configBytes  []byte
		)
		if err := rows.Scan(&id, &tenantID, &name, &configBytes); err != nil {
			return nil, err
		}

		var cfg agentThresholdConfig
		if err := json.Unmarshal(configBytes, &cfg); err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{"monitor_id": id}).Warn("Skipping monitor with unparseable config")
			continue
		}
		thresholds := normalizeThresholds(cfg.MetricThresholds)
		if len(thresholds) == 0 {
			continue
		}
		out = append(out, hostMetricConfig{
			monitorID:   id,
			tenantID:    tenantID,
			monitorName: name,
			thresholds:  thresholds,
		})
	}
	return out, rows.Err()
}

// normalizeThresholds turns the optional config block into a metric→threshold
// map, keeping only positive values (a 0 or negative threshold is "disabled").
func normalizeThresholds(c *metricThresholdConfig) map[string]float64 {
	if c == nil {
		return nil
	}
	out := make(map[string]float64, 4)
	add := func(metric string, v *float64) {
		if v != nil && *v > 0 {
			out[metric] = *v
		}
	}
	add("cpu", c.CPUPercent)
	add("memory", c.MemoryPercent)
	add("disk", c.DiskPercent)
	add("swap", c.SwapPercent)
	return out
}

// loadLatestAgentMetrics returns the most recent reported metrics per monitor.
func (a *Alerter) loadLatestAgentMetrics(ctx context.Context, monitorIDs []uuid.UUID) (map[uuid.UUID]*models.AgentMetrics, error) {
	result := make(map[uuid.UUID]*models.AgentMetrics)
	if len(monitorIDs) == 0 {
		return result, nil
	}
	rows, err := a.db.QueryContext(ctx, `
		SELECT DISTINCT ON (monitor_id) monitor_id, metrics_data
		FROM check_results
		WHERE monitor_id = ANY($1)
		  AND result_source = 'monitor'
		  AND status = 'success'
		  AND metrics_data IS NOT NULL
		ORDER BY monitor_id, created_at DESC
	`, pq.Array(monitorIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var mid uuid.UUID
		var data []byte
		if err := rows.Scan(&mid, &data); err != nil {
			return nil, err
		}
		var metrics models.AgentMetrics
		if err := json.Unmarshal(data, &metrics); err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{"monitor_id": mid}).Warn("Skipping monitor with unparseable metrics_data")
			continue
		}
		result[mid] = &metrics
	}
	return result, rows.Err()
}

// metricUsage returns the current percent value (0-100) for a metric and
// whether it is available on this report. Metrics with no denominator (e.g. a
// host without swap) or an unavailable CPU sample return available=false.
func metricUsage(m *models.AgentMetrics, metric string) (float64, bool) {
	switch metric {
	case "cpu":
		if m.CPUPercent < 0 {
			return 0, false
		}
		return m.CPUPercent, true
	case "memory":
		if m.MemoryTotal == 0 {
			return 0, false
		}
		return float64(m.MemoryUsed) / float64(m.MemoryTotal) * 100, true
	case "disk":
		if m.DiskTotal == 0 {
			return 0, false
		}
		return float64(m.DiskUsed) / float64(m.DiskTotal) * 100, true
	case "swap":
		if m.SwapTotal == 0 {
			return 0, false
		}
		return float64(m.SwapUsed) / float64(m.SwapTotal) * 100, true
	}
	return 0, false
}

// openHostMetricAlert opens a host_metric alert for one breaching metric if one
// is not already open. Idempotent via the (monitor_id, kind, metric_name)
// partial unique index, so it is safe across alerter replicas.
func (a *Alerter) openHostMetricAlert(ctx context.Context, c hostMetricConfig, metric string, value, threshold float64) error {
	var alertID uuid.UUID
	err := a.db.QueryRowContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, kind, status,
			triggered_at, failure_count, metric_name, metric_value, threshold_value,
			created_at, updated_at)
		VALUES ($1, $2, $3, NULL, 'host_metric', 'active', NOW(), 0, $4, $5, $6, NOW(), NOW())
		ON CONFLICT (monitor_id, kind, (COALESCE(metric_name, ''))) WHERE status IN ('active', 'acknowledged') DO NOTHING
		RETURNING id
	`, uuid.New(), c.tenantID, c.monitorID, metric, value, threshold).Scan(&alertID)
	if err == sql.ErrNoRows {
		return nil // already open
	}
	if err != nil {
		return fmt.Errorf("insert host metric alert: %w", err)
	}

	metricName := metric
	record := alertRecord{
		ID: alertID, TenantID: c.tenantID, MonitorID: c.monitorID,
		Kind: notifications.KindHostMetric, TriggeredAt: time.Now(),
		MetricName: &metricName, MetricValue: &value, ThresholdValue: &threshold,
	}
	binding := policyBinding{MonitorID: c.monitorID, TenantID: c.tenantID, MonitorName: c.monitorName}
	a.publishAlertEvent(ctx, "created", binding, &record, nil)
	return nil
}

// resolveClearedHostMetricAlerts resolves open host_metric alerts for a monitor
// whose metric is no longer in the breaching set (recovered, or threshold
// removed), notifying channels that fired.
func (a *Alerter) resolveClearedHostMetricAlerts(ctx context.Context, c hostMetricConfig, breaching map[string]bool, now time.Time) error {
	rows, err := a.db.QueryContext(ctx, `
		SELECT id, metric_name, triggered_at FROM alerts
		WHERE monitor_id = $1 AND kind = 'host_metric' AND status IN ('active', 'acknowledged')
	`, c.monitorID)
	if err != nil {
		return fmt.Errorf("query open host metric alerts: %w", err)
	}
	defer rows.Close()

	type openAlert struct {
		id          uuid.UUID
		metricName  sql.NullString
		triggeredAt time.Time
	}
	var open []openAlert
	for rows.Next() {
		var oa openAlert
		if err := rows.Scan(&oa.id, &oa.metricName, &oa.triggeredAt); err != nil {
			return fmt.Errorf("scan open host metric alert: %w", err)
		}
		open = append(open, oa)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, oa := range open {
		metric := oa.metricName.String
		if breaching[metric] {
			continue // still breaching; keep open
		}
		a.resolveHostMetricAlert(ctx, oa.id, c.tenantID, c.monitorID, c.monitorName, metric, oa.triggeredAt, now)
	}
	return nil
}

// resolveOrphanHostMetricAlerts resolves open host_metric alerts whose monitor
// is no longer in the evaluated set (thresholds removed, monitor
// deleted/disabled). keep is the set of monitor IDs evaluated this tick.
func (a *Alerter) resolveOrphanHostMetricAlerts(ctx context.Context, keep []uuid.UUID) error {
	rows, err := a.db.QueryContext(ctx, `
		SELECT al.id, al.tenant_id, al.monitor_id, m.name, al.metric_name, al.triggered_at
		FROM alerts al
		JOIN monitors m ON m.id = al.monitor_id
		WHERE al.kind = 'host_metric'
		  AND al.status IN ('active', 'acknowledged')
		  AND NOT (al.monitor_id = ANY($1))
		  AND NOT `+maintenance.InMaintenancePredicate("m")+`
	`, pq.Array(keep))
	if err != nil {
		return fmt.Errorf("query orphan host metric alerts: %w", err)
	}
	defer rows.Close()

	type orphan struct {
		id          uuid.UUID
		tenantID    uuid.UUID
		monitorID   uuid.UUID
		monitorName string
		metricName  sql.NullString
		triggeredAt time.Time
	}
	var orphans []orphan
	for rows.Next() {
		var o orphan
		if err := rows.Scan(&o.id, &o.tenantID, &o.monitorID, &o.monitorName, &o.metricName, &o.triggeredAt); err != nil {
			return fmt.Errorf("scan orphan host metric alert: %w", err)
		}
		orphans = append(orphans, o)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	now := time.Now()
	for _, o := range orphans {
		a.resolveHostMetricAlert(ctx, o.id, o.tenantID, o.monitorID, o.monitorName, o.metricName.String, o.triggeredAt, now)
	}
	return nil
}

// resolveHostMetricAlert resolves a single host_metric alert and notifies any
// channels that already fired for it.
func (a *Alerter) resolveHostMetricAlert(ctx context.Context, alertID, tenantID, monitorID uuid.UUID, monitorName, metric string, triggeredAt, now time.Time) {
	resolvedAt, err := a.resolveAlert(ctx, alertID, now)
	if err != nil {
		a.logger.WithError(err).WithFields(map[string]interface{}{"alert_id": alertID}).Error("Failed to resolve host metric alert")
		return
	}
	metricName := metric
	record := alertRecord{
		ID: alertID, TenantID: tenantID, MonitorID: monitorID,
		Kind: notifications.KindHostMetric, TriggeredAt: triggeredAt, MetricName: &metricName,
	}
	binding := policyBinding{MonitorID: monitorID, TenantID: tenantID, MonitorName: monitorName}
	a.publishAlertEvent(ctx, "resolved", binding, &record, &resolvedAt)
	a.notifyFiredChannels(ctx, "resolved", binding, &record)
}

// setHostMetric copies nullable host-metric scan columns onto an alertRecord.
func setHostMetric(record *alertRecord, name sql.NullString, value, threshold sql.NullFloat64) {
	if name.Valid {
		n := name.String
		record.MetricName = &n
	}
	if value.Valid {
		v := value.Float64
		record.MetricValue = &v
	}
	if threshold.Valid {
		t := threshold.Float64
		record.ThresholdValue = &t
	}
}
