package alerter

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/domainexpiry"
	"github.com/yassinebenameur/probara/shared/maintenance"
	sharedmodels "github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/tlscert"
)

// expirySpec is one expiry alert kind: a per-monitor threshold in days
// (a monitors.config key) compared against the expiry the monitor's latest
// check recorded in metrics_data. Every kind shares the evaluation below
// (latest-row walk, idempotent open, resolve on clearance, orphan resolve,
// maintenance quiet); a new kind is one spec in expirySpecs.
type expirySpec struct {
	kind string // alerts.kind
	name string // in log lines, e.g. "tls expiry"
	// thresholdKey is the monitors.config key carrying the threshold; only
	// monitors whose config has it are evaluated.
	thresholdKey string
	// monitorType restricts the spec to one monitor type ("" = any type
	// whose config carries thresholdKey).
	monitorType string
	// parse reads the threshold and the boolean option (expiryExprs' second
	// input, e.g. tls_check_chain) from a monitor's config. A nil threshold
	// skips the monitor; an error skips it with a warning.
	parse func(config []byte) (minDays *int, option bool, err error)
	// objectExpr selects the snapshot object from a metrics_data column
	// (the owning package's SQLExpr), expiryExprs its (expires-at, days)
	// text expressions given the per-monitor option column.
	objectExpr  func(col string) string
	expiryExprs func(obj, option string) (expiresAt, days string)
	// lookback bounds the walk back to the latest row with a snapshot (an
	// SQL interval literal); it must exceed the type's longest interval.
	lookback string
	// lastError renders the alert's last_error.
	lastError func(days, minDays int) string
}

// tlsExpirySpec: an HTTPS, SIP-over-TLS or TLS-wrapped TCP check's
// certificate (located through shared/tlscert; the chain's earliest with
// tls_check_chain) against tls_min_days_valid. Like latency anomalies and
// host metrics, this is orthogonal to the availability state machine: a
// healthy endpoint with an aging certificate stays 'up' and pages as a
// certificate warning, not as an outage. (A fully expired certificate still
// fails the TLS handshake and surfaces as a real availability alert.)
var tlsExpirySpec = expirySpec{
	kind:         notifications.KindTLSExpiry,
	name:         "tls expiry",
	thresholdKey: "tls_min_days_valid",
	parse: func(config []byte) (*int, bool, error) {
		var cfg struct {
			TLSMinDaysValid *int  `json:"tls_min_days_valid"`
			TLSCheckChain   *bool `json:"tls_check_chain"`
		}
		if err := json.Unmarshal(config, &cfg); err != nil {
			return nil, false, err
		}
		return cfg.TLSMinDaysValid, cfg.TLSCheckChain != nil && *cfg.TLSCheckChain, nil
	},
	objectExpr: tlscert.SQLExpr,
	// tlscert.ExpirySQL is the twin of the status page's Info.Expiry.
	expiryExprs: tlscert.ExpirySQL,
	lookback:    "24 hours",
	lastError: func(days, minDays int) string {
		return fmt.Sprintf("tls: expires in %dd (< %dd)", days, minDays)
	},
}

// domainExpirySpec: a domain monitor's registration expiry (located through
// shared/domainexpiry) against min_days_valid. Orthogonal to availability
// the same way: the check fails only once the domain has expired.
var domainExpirySpec = expirySpec{
	kind:         notifications.KindDomainExpiry,
	name:         "domain expiry",
	thresholdKey: "min_days_valid",
	monitorType:  "domain",
	parse: func(config []byte) (*int, bool, error) {
		var cfg sharedmodels.DomainMonitorConfig
		if err := json.Unmarshal(config, &cfg); err != nil {
			return nil, false, err
		}
		return cfg.MinDaysValid, false, nil
	},
	objectExpr: domainexpiry.SQLExpr,
	expiryExprs: func(obj, _ string) (string, string) {
		return domainexpiry.ExpirySQL(obj)
	},
	// Domain checks run at most every 24h (at least every hour).
	lookback: "48 hours",
	lastError: func(days, minDays int) string {
		if days < 0 {
			return fmt.Sprintf("domain: expired %dd ago (< %dd)", -days, minDays)
		}
		return fmt.Sprintf("domain: expires in %dd (< %dd)", days, minDays)
	},
}

// expirySpecs are evaluated in order every tick.
var expirySpecs = []expirySpec{tlsExpirySpec, domainExpirySpec}

// expiryConfig is the effective expiry threshold for one monitor, parsed
// from its monitors.config JSON.
type expiryConfig struct {
	monitorID   uuid.UUID
	tenantID    uuid.UUID
	monitorName string
	minDays     int
	// option is the spec's per-monitor boolean (tls_check_chain measures
	// the earliest expiry across the presented chain instead of the leaf's,
	// tlscert.Info.Expiry).
	option bool
}

// evaluateExpiryAlerts runs every expiry spec. Expiry alerts are advisory:
// a failing spec is logged and never blocks availability alerting, the
// other specs, or notification dispatch further down the lifecycle.
func (a *Alerter) evaluateExpiryAlerts(ctx context.Context) {
	for _, spec := range expirySpecs {
		if err := a.evaluateExpiry(ctx, spec); err != nil {
			a.logger.WithError(err).WithField("kind", spec.kind).Error("Expiry evaluation failed")
		}
	}
}

// evaluateTLSExpiry evaluates tls_expiry alerts alone.
func (a *Alerter) evaluateTLSExpiry(ctx context.Context) error {
	return a.evaluateExpiry(ctx, tlsExpirySpec)
}

// evaluateDomainExpiry evaluates domain_expiry alerts alone.
func (a *Alerter) evaluateDomainExpiry(ctx context.Context) error {
	return a.evaluateExpiry(ctx, domainExpirySpec)
}

// evaluateExpiry compares each monitor's latest recorded expiry against its
// threshold and opens or resolves the spec's alert.
func (a *Alerter) evaluateExpiry(ctx context.Context, spec expirySpec) error {
	configs, err := a.loadExpiryConfigs(ctx, spec)
	if err != nil {
		return fmt.Errorf("load %s configs: %w", spec.name, err)
	}
	if len(configs) == 0 {
		// Nothing configured — still resolve any lingering alerts.
		return a.resolveOrphanExpiryAlerts(ctx, spec, nil)
	}

	monitorIDs := make([]uuid.UUID, 0, len(configs))
	options := make([]bool, 0, len(configs))
	for _, c := range configs {
		monitorIDs = append(monitorIDs, c.monitorID)
		options = append(options, c.option)
	}

	latest, err := a.loadLatestExpiries(ctx, spec, monitorIDs, options)
	if err != nil {
		return fmt.Errorf("load latest %s: %w", spec.name, err)
	}

	now := time.Now()
	for _, c := range configs {
		days, ok := latest[c.monitorID]
		if !ok {
			// Nothing observed yet; leave any open alert untouched.
			continue
		}
		if days < c.minDays {
			if err := a.openExpiryAlert(ctx, spec, c, days); err != nil {
				a.logger.WithError(err).WithFields(map[string]interface{}{
					"monitor_id": c.monitorID,
				}).Errorf("Failed to open %s alert", spec.name)
			}
			continue
		}
		// Renewed (or threshold lowered) — resolve any open alert.
		if err := a.resolveClearedExpiryAlert(ctx, spec, c, now); err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{
				"monitor_id": c.monitorID,
			}).Errorf("Failed to resolve cleared %s alert", spec.name)
		}
	}

	// Resolve alerts for monitors that dropped out of the evaluated set
	// entirely (threshold removed, monitor deleted/disabled).
	return a.resolveOrphanExpiryAlerts(ctx, spec, monitorIDs)
}

// loadExpiryConfigs returns one config per enabled monitor whose
// monitors.config carries the spec's threshold. Monitors in an active
// maintenance window are excluded so planned work stays quiet.
func (a *Alerter) loadExpiryConfigs(ctx context.Context, spec expirySpec) ([]expiryConfig, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT m.id, m.tenant_id, m.name, m.config
		FROM monitors m
		WHERE m.enabled = TRUE AND m.deleted_at IS NULL
		  AND m.config ? $1
		  AND ($2 = '' OR m.type = $2)
		  AND NOT `+maintenance.InMaintenancePredicate("m")+`
	`, spec.thresholdKey, spec.monitorType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []expiryConfig
	for rows.Next() {
		var (
			id, tenantID uuid.UUID
			name         string
			configBytes  []byte
		)
		if err := rows.Scan(&id, &tenantID, &name, &configBytes); err != nil {
			return nil, err
		}
		minDays, option, err := spec.parse(configBytes)
		if err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{"monitor_id": id}).Warn("Skipping monitor with unparseable config")
			continue
		}
		if minDays == nil {
			continue
		}
		out = append(out, expiryConfig{
			monitorID:   id,
			tenantID:    tenantID,
			monitorName: name,
			minDays:     *minDays,
			option:      option,
		})
	}
	return out, rows.Err()
}

// loadLatestExpiries returns days-until-expiry per monitor, computed at
// evaluation time from the expiry recorded on the monitor's most recent
// check result that carries a snapshot. Falls back to the worker's
// days_until_expiry snapshot when the date is unparseable. options[i] is
// monitorIDs[i]'s spec option (tls_check_chain swaps in the chain's
// earliest expiry).
//
// The per-monitor LATERAL walks idx_check_results_monitor_id_created_at
// backwards and stops at the first row with a snapshot; the lookback bound
// caps the walk for monitors whose recent results never carry one (a
// DISTINCT ON over `monitor_id = ANY(...)` planned a sort over every
// historical row and timed out on large check_results tables). Monitors
// with nothing observed in the window are skipped this tick, which leaves
// any open alert untouched — same as "no data yet".
func (a *Alerter) loadLatestExpiries(ctx context.Context, spec expirySpec, monitorIDs []uuid.UUID, options []bool) (map[uuid.UUID]int, error) {
	result := make(map[uuid.UUID]int)
	if len(monitorIDs) == 0 {
		return result, nil
	}
	// Where the snapshot sits in metrics_data is the owning package's to
	// say; Go readers use the same location through its Extract (each
	// package's TestSQLExprMatchesExtract pins the pair).
	objExpr := spec.objectExpr("metrics_data")
	expiresExpr, daysExpr := spec.expiryExprs("cr.obj", "m.opt")
	rows, err := a.db.QueryContext(ctx, `
		SELECT m.id, `+expiresExpr+`, `+daysExpr+`
		FROM unnest($1::uuid[], $2::boolean[]) AS m(id, opt)
		JOIN LATERAL (
			SELECT `+objExpr+` AS obj
			FROM check_results
			WHERE monitor_id = m.id
			  AND result_source = 'monitor'
			  AND created_at > NOW() - INTERVAL '`+spec.lookback+`'
			  AND `+objExpr+` IS NOT NULL
			ORDER BY created_at DESC
			LIMIT 1
		) cr ON TRUE
	`, pq.Array(monitorIDs), pq.Array(options))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var mid uuid.UUID
		var expiresAt, daysSnapshot sql.NullString
		if err := rows.Scan(&mid, &expiresAt, &daysSnapshot); err != nil {
			return nil, err
		}
		if expiresAt.Valid {
			if t, err := time.Parse(time.RFC3339, expiresAt.String); err == nil {
				result[mid] = int(math.Floor(time.Until(t).Hours() / 24))
				continue
			}
		}
		if daysSnapshot.Valid {
			var days int
			if _, err := fmt.Sscanf(daysSnapshot.String, "%d", &days); err == nil {
				result[mid] = days
			}
		}
	}
	return result, rows.Err()
}

// openExpiryAlert opens the spec's alert if one is not already open.
// Idempotent via the (monitor_id, kind, COALESCE(metric_name, ”)) partial
// unique index, so it is safe across alerter replicas.
func (a *Alerter) openExpiryAlert(ctx context.Context, spec expirySpec, c expiryConfig, days int) error {
	value := float64(days)
	threshold := float64(c.minDays)
	lastError := spec.lastError(days, c.minDays)

	var alertID uuid.UUID
	err := a.db.QueryRowContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, kind, status,
			triggered_at, failure_count, last_error, metric_value, threshold_value,
			created_at, updated_at)
		VALUES ($1, $2, $3, NULL, $7, 'active', NOW(), 0, $4, $5, $6, NOW(), NOW())
		ON CONFLICT (monitor_id, kind, (COALESCE(metric_name, ''))) WHERE status IN ('active', 'acknowledged') DO NOTHING
		RETURNING id
	`, uuid.New(), c.tenantID, c.monitorID, lastError, value, threshold, spec.kind).Scan(&alertID)
	if err == sql.ErrNoRows {
		return nil // already open
	}
	if err != nil {
		return fmt.Errorf("insert %s alert: %w", spec.name, err)
	}

	record := alertRecord{
		ID: alertID, TenantID: c.tenantID, MonitorID: c.monitorID,
		Kind: spec.kind, TriggeredAt: time.Now(), LastError: &lastError,
		MetricValue: &value, ThresholdValue: &threshold,
	}
	binding := policyBinding{MonitorID: c.monitorID, TenantID: c.tenantID, MonitorName: c.monitorName}
	a.publishAlertEvent(ctx, "created", binding, &record, nil)
	return nil
}

// resolveClearedExpiryAlert resolves the open alert of a monitor whose
// expiry is no longer inside the threshold window.
func (a *Alerter) resolveClearedExpiryAlert(ctx context.Context, spec expirySpec, c expiryConfig, now time.Time) error {
	var alertID uuid.UUID
	var triggeredAt time.Time
	err := a.db.QueryRowContext(ctx, `
		SELECT id, triggered_at FROM alerts
		WHERE monitor_id = $1 AND kind = $2 AND status IN ('active', 'acknowledged')
	`, c.monitorID, spec.kind).Scan(&alertID, &triggeredAt)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("query open %s alert: %w", spec.name, err)
	}
	a.resolveExpiryAlert(ctx, spec, alertID, c.tenantID, c.monitorID, c.monitorName, triggeredAt, now)
	return nil
}

// resolveOrphanExpiryAlerts resolves the spec's open alerts whose monitor is
// no longer in the evaluated set (threshold removed, monitor
// deleted/disabled). keep is the set of monitor IDs evaluated this tick.
func (a *Alerter) resolveOrphanExpiryAlerts(ctx context.Context, spec expirySpec, keep []uuid.UUID) error {
	keep = nonNilIDs(keep)
	rows, err := a.db.QueryContext(ctx, `
		SELECT al.id, al.tenant_id, al.monitor_id, m.name, al.triggered_at
		FROM alerts al
		JOIN monitors m ON m.id = al.monitor_id
		WHERE al.kind = $2
		  AND al.status IN ('active', 'acknowledged')
		  AND NOT (al.monitor_id = ANY($1))
		  AND NOT `+maintenance.InMaintenancePredicate("m")+`
	`, pq.Array(keep), spec.kind)
	if err != nil {
		return fmt.Errorf("query orphan %s alerts: %w", spec.name, err)
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
			return fmt.Errorf("scan orphan %s alert: %w", spec.name, err)
		}
		orphans = append(orphans, o)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	now := time.Now()
	for _, o := range orphans {
		a.resolveExpiryAlert(ctx, spec, o.id, o.tenantID, o.monitorID, o.monitorName, o.triggeredAt, now)
	}
	return nil
}

// resolveExpiryAlert resolves a single expiry alert and notifies any
// channels that already fired for it.
func (a *Alerter) resolveExpiryAlert(ctx context.Context, spec expirySpec, alertID, tenantID, monitorID uuid.UUID, monitorName string, triggeredAt, now time.Time) {
	resolvedAt, err := a.resolveAlert(ctx, alertID, now)
	if err != nil {
		a.logger.WithError(err).WithFields(map[string]interface{}{"alert_id": alertID}).Errorf("Failed to resolve %s alert", spec.name)
		return
	}
	record := alertRecord{
		ID: alertID, TenantID: tenantID, MonitorID: monitorID,
		Kind: spec.kind, TriggeredAt: triggeredAt,
	}
	binding := policyBinding{MonitorID: monitorID, TenantID: tenantID, MonitorName: monitorName}
	a.publishAlertEvent(ctx, "resolved", binding, &record, &resolvedAt)
	a.notifyFiredChannels(ctx, "resolved", binding, &record)
}
