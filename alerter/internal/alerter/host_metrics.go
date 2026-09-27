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
	"github.com/yassinebenameur/probara/shared/metricstore"
	"github.com/yassinebenameur/probara/shared/monitorstate"
	"github.com/yassinebenameur/probara/shared/notifications"
)

// Host-metric alerting over the generic metric store: each agent monitor's
// config carries metric_rules ([{metric_name, attribute_filters, operator,
// threshold, for_duration_seconds}], native units — ratio 0-1 for
// *.utilization). A rule fans out to every matching series; each breaching
// series opens its own host_metric alert keyed by its CANONICAL SERIES KEY
// (metricstore.SeriesKeyString) in alerts.metric_name — per-mountpoint disk
// alerts coexist under the (monitor_id, kind, metric_name) unique index
// unchanged since migration 000059.
//
// Evaluation is freshness-bounded (monitorstate.FreshnessHorizonSeconds): a
// silent agent's last readings stop being evaluated and their alerts resolve
// — the availability watchdog owns paging for the outage itself. This
// replaces the pre-OTel evaluator whose unbounded DISTINCT ON kept a dead
// host's last breach alerting forever.

// hostMetricConfig is one agent monitor's effective rule set.
type hostMetricConfig struct {
	monitorID       uuid.UUID
	tenantID        uuid.UUID
	monitorName     string
	intervalSeconds int
	rules           []metricRule
}

// metricRule mirrors the metric_rules entries in monitors.config (the API's
// models.MetricRule; the alerter keeps its own decode struct like every
// other config slice it reads).
type metricRule struct {
	MetricName         string            `json:"metric_name"`
	AttributeFilters   map[string]string `json:"attribute_filters"`
	Operator           string            `json:"operator"`
	Threshold          float64           `json:"threshold"`
	ForDurationSeconds int               `json:"for_duration_seconds"`
}

func (r metricRule) breaches(value float64) bool {
	if r.Operator == "<=" {
		return value <= r.Threshold
	}
	return value >= r.Threshold
}

// breachingSeries is one series currently in breach of a rule.
type breachingSeries struct {
	seriesKey string
	value     float64
	threshold float64
}

// evaluateHostMetricThresholds compares each agent monitor's fresh series
// against its metric rules and opens or resolves host_metric alerts. Like
// latency anomalies, this is orthogonal to availability: an 'up' host can
// still be flagged for a breaching metric.
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

	now := time.Now()
	for _, c := range configs {
		breaching, err := a.evaluateMonitorRules(ctx, c)
		if err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{
				"monitor_id": c.monitorID,
			}).Error("Failed to evaluate host metric rules")
			continue
		}

		open := make(map[string]bool, len(breaching))
		for _, b := range breaching {
			open[b.seriesKey] = true
			if err := a.openHostMetricAlert(ctx, c, b); err != nil {
				a.logger.WithError(err).WithFields(map[string]interface{}{
					"monitor_id": c.monitorID, "series": b.seriesKey,
				}).Error("Failed to open host metric alert")
			}
		}

		// Resolve open alerts whose series is no longer breaching —
		// recovered, rule removed, or series gone stale (freshness bound).
		if err := a.resolveClearedHostMetricAlerts(ctx, c, open, now); err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{
				"monitor_id": c.monitorID,
			}).Error("Failed to resolve cleared host metric alerts")
		}
	}

	// Resolve host_metric alerts for monitors that dropped out of the
	// evaluated set entirely (rules removed, monitor deleted/disabled).
	return a.resolveOrphanHostMetricAlerts(ctx, monitorIDs)
}

// evaluateMonitorRules returns the monitor's currently breaching series.
// Only series seen within the freshness horizon participate; a stale or
// absent series contributes nothing (its alert then resolves).
func (a *Alerter) evaluateMonitorRules(ctx context.Context, c hostMetricConfig) ([]breachingSeries, error) {
	names := make([]string, 0, len(c.rules))
	seen := map[string]bool{}
	for _, r := range c.rules {
		if !seen[r.MetricName] {
			seen[r.MetricName] = true
			names = append(names, r.MetricName)
		}
	}
	freshness := time.Duration(monitorstate.FreshnessHorizonSeconds(c.intervalSeconds)) * time.Second
	latest, err := metricstore.LatestSamples(ctx, a.db, c.tenantID, c.monitorID, names, freshness)
	if err != nil {
		return nil, fmt.Errorf("load latest samples: %w", err)
	}

	var out []breachingSeries
	for _, rule := range c.rules {
		for _, ls := range latest {
			if ls.MetricName != rule.MetricName || !attrsMatch(rule.AttributeFilters, ls.Attributes) {
				continue
			}
			if !rule.breaches(ls.Value) {
				continue
			}
			if rule.ForDurationSeconds > 0 {
				sustained, err := a.sustainedBreach(ctx, rule, ls.SeriesID, time.Now())
				if err != nil {
					return nil, err
				}
				if !sustained {
					continue
				}
			}
			out = append(out, breachingSeries{
				seriesKey: metricstore.SeriesKeyString(ls.MetricName, ls.Attributes),
				value:     ls.Value,
				threshold: rule.Threshold,
			})
		}
	}
	return out, nil
}

// sustainedBreach implements for_duration with Prometheus `for` semantics,
// statelessly (correct across alerter replicas and restarts): every sample
// inside [now-for_duration, now] breaches AND a breaching anchor sample
// exists at or before the window start — a breach younger than the window,
// or any in-window recovery, does not open.
func (a *Alerter) sustainedBreach(ctx context.Context, rule metricRule, seriesID int64, now time.Time) (bool, error) {
	window := time.Duration(rule.ForDurationSeconds) * time.Second
	// Lookback beyond the window so the anchor sample is in range.
	from := now.Add(-window - 2*window - time.Minute)
	samples, err := metricstore.RangeSamples(ctx, a.db, seriesID, from, now)
	if err != nil {
		return false, fmt.Errorf("load range samples: %w", err)
	}
	windowStart := now.Add(-window)
	anchored := false
	for _, s := range samples {
		if !s.TS.After(windowStart) {
			// The newest sample at or before the window start is the anchor.
			anchored = rule.breaches(s.Value)
			continue
		}
		if !rule.breaches(s.Value) {
			return false, nil
		}
	}
	return anchored, nil
}

// attrsMatch reports whether every filter entry is present in the series
// attributes (subset match; nil filters match everything).
func attrsMatch(filters, attrs map[string]string) bool {
	for k, v := range filters {
		if attrs[k] != v {
			return false
		}
	}
	return true
}

// loadHostMetricConfigs returns one config per enabled agent monitor whose
// monitors.config carries metric_rules. Monitors in an active maintenance
// window are excluded so planned work stays quiet.
func (a *Alerter) loadHostMetricConfigs(ctx context.Context) ([]hostMetricConfig, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT m.id, m.tenant_id, m.name, m.interval_seconds, m.config->'metric_rules'
		FROM monitors m
		WHERE m.type = 'agent'
		  AND m.enabled = TRUE AND m.deleted_at IS NULL
		  AND m.config ? 'metric_rules'
		  AND NOT `+maintenance.InMaintenancePredicate("m")+`
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []hostMetricConfig
	for rows.Next() {
		var (
			id, tenantID    uuid.UUID
			name            string
			intervalSeconds int
			rulesBytes      []byte
		)
		if err := rows.Scan(&id, &tenantID, &name, &intervalSeconds, &rulesBytes); err != nil {
			return nil, err
		}
		var rules []metricRule
		if err := json.Unmarshal(rulesBytes, &rules); err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{"monitor_id": id}).Warn("Skipping monitor with unparseable metric_rules")
			continue
		}
		if len(rules) == 0 {
			continue
		}
		out = append(out, hostMetricConfig{
			monitorID:       id,
			tenantID:        tenantID,
			monitorName:     name,
			intervalSeconds: intervalSeconds,
			rules:           rules,
		})
	}
	return out, rows.Err()
}

// openHostMetricAlert opens a host_metric alert for one breaching series if
// one is not already open. Idempotent via the (monitor_id, kind,
// metric_name) partial unique index, so it is safe across alerter replicas.
func (a *Alerter) openHostMetricAlert(ctx context.Context, c hostMetricConfig, b breachingSeries) error {
	var alertID uuid.UUID
	err := a.db.QueryRowContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, kind, status,
			triggered_at, failure_count, metric_name, metric_value, threshold_value,
			created_at, updated_at)
		VALUES ($1, $2, $3, NULL, 'host_metric', 'active', NOW(), 0, $4, $5, $6, NOW(), NOW())
		ON CONFLICT (monitor_id, kind, (COALESCE(metric_name, ''))) WHERE status IN ('active', 'acknowledged') DO NOTHING
		RETURNING id
	`, uuid.New(), c.tenantID, c.monitorID, b.seriesKey, b.value, b.threshold).Scan(&alertID)
	if err == sql.ErrNoRows {
		return nil // already open
	}
	if err != nil {
		return fmt.Errorf("insert host metric alert: %w", err)
	}

	seriesKey := b.seriesKey
	value := b.value
	threshold := b.threshold
	record := alertRecord{
		ID: alertID, TenantID: c.tenantID, MonitorID: c.monitorID,
		Kind: notifications.KindHostMetric, TriggeredAt: time.Now(),
		MetricName: &seriesKey, MetricValue: &value, ThresholdValue: &threshold,
	}
	binding := policyBinding{MonitorID: c.monitorID, TenantID: c.tenantID, MonitorName: c.monitorName}
	a.publishAlertEvent(ctx, "created", binding, &record, nil)
	return nil
}

// resolveClearedHostMetricAlerts resolves open host_metric alerts for a
// monitor whose series key is no longer in the breaching set (recovered,
// rule removed, or series stale/absent), notifying channels that fired.
func (a *Alerter) resolveClearedHostMetricAlerts(ctx context.Context, c hostMetricConfig, breaching map[string]bool, now time.Time) error {
	rows, err := a.db.QueryContext(ctx, `
		SELECT id, metric_name, threshold_value, triggered_at FROM alerts
		WHERE monitor_id = $1 AND kind = 'host_metric' AND status IN ('active', 'acknowledged')
	`, c.monitorID)
	if err != nil {
		return fmt.Errorf("query open host metric alerts: %w", err)
	}
	defer rows.Close()

	type openAlert struct {
		id          uuid.UUID
		metricName  sql.NullString
		threshold   sql.NullFloat64
		triggeredAt time.Time
	}
	var open []openAlert
	for rows.Next() {
		var oa openAlert
		if err := rows.Scan(&oa.id, &oa.metricName, &oa.threshold, &oa.triggeredAt); err != nil {
			return fmt.Errorf("scan open host metric alert: %w", err)
		}
		open = append(open, oa)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, oa := range open {
		if breaching[oa.metricName.String] {
			continue // still breaching; keep open
		}
		a.resolveHostMetricAlert(ctx, oa.id, c.tenantID, c.monitorID, c.monitorName, oa.metricName.String, oa.threshold, oa.triggeredAt, now)
	}
	return nil
}

// resolveOrphanHostMetricAlerts resolves open host_metric alerts whose
// monitor is no longer in the evaluated set (rules removed, monitor
// deleted/disabled). keep is the set of monitor IDs evaluated this tick.
func (a *Alerter) resolveOrphanHostMetricAlerts(ctx context.Context, keep []uuid.UUID) error {
	keep = nonNilIDs(keep)
	rows, err := a.db.QueryContext(ctx, `
		SELECT al.id, al.tenant_id, al.monitor_id, m.name, al.metric_name, al.threshold_value, al.triggered_at
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
		threshold   sql.NullFloat64
		triggeredAt time.Time
	}
	var orphans []orphan
	for rows.Next() {
		var o orphan
		if err := rows.Scan(&o.id, &o.tenantID, &o.monitorID, &o.monitorName, &o.metricName, &o.threshold, &o.triggeredAt); err != nil {
			return fmt.Errorf("scan orphan host metric alert: %w", err)
		}
		orphans = append(orphans, o)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	now := time.Now()
	for _, o := range orphans {
		a.resolveHostMetricAlert(ctx, o.id, o.tenantID, o.monitorID, o.monitorName, o.metricName.String, o.threshold, o.triggeredAt, now)
	}
	return nil
}

// resolveHostMetricAlert resolves a single host_metric alert and notifies
// any channels that already fired for it. The threshold rides the resolve
// event so wording can say what range the metric returned to.
func (a *Alerter) resolveHostMetricAlert(ctx context.Context, alertID, tenantID, monitorID uuid.UUID, monitorName, seriesKey string, threshold sql.NullFloat64, triggeredAt, now time.Time) {
	resolvedAt, err := a.resolveAlert(ctx, alertID, now)
	if err != nil {
		a.logger.WithError(err).WithFields(map[string]interface{}{"alert_id": alertID}).Error("Failed to resolve host metric alert")
		return
	}
	metricName := seriesKey
	record := alertRecord{
		ID: alertID, TenantID: tenantID, MonitorID: monitorID,
		Kind: notifications.KindHostMetric, TriggeredAt: triggeredAt, MetricName: &metricName,
	}
	if threshold.Valid {
		t := threshold.Float64
		record.ThresholdValue = &t
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
