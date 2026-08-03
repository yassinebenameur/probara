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

	"github.com/yassinebenameur/probara/shared/maintenance"
	"github.com/yassinebenameur/probara/shared/notifications"
)

// tlsExpiryConfig is the effective TLS-expiry threshold for one monitor,
// parsed from its monitors.config JSON.
type tlsExpiryConfig struct {
	monitorID   uuid.UUID
	tenantID    uuid.UUID
	monitorName string
	minDays     int
}

// evaluateTLSExpiry compares each monitor's latest recorded certificate expiry
// against its tls_min_days_valid threshold and opens or resolves a tls_expiry
// alert. Like latency anomalies and host metrics, this is orthogonal to the
// availability state machine: a healthy endpoint with an aging certificate
// stays 'up' and pages as a certificate warning, not as an outage. (A fully
// expired certificate still fails the TLS handshake and surfaces as a real
// availability alert.)
func (a *Alerter) evaluateTLSExpiry(ctx context.Context) error {
	configs, err := a.loadTLSExpiryConfigs(ctx)
	if err != nil {
		return fmt.Errorf("load tls expiry configs: %w", err)
	}
	if len(configs) == 0 {
		// Nothing configured — still resolve any lingering tls_expiry alerts.
		return a.resolveOrphanTLSExpiryAlerts(ctx, nil)
	}

	monitorIDs := make([]uuid.UUID, 0, len(configs))
	for _, c := range configs {
		monitorIDs = append(monitorIDs, c.monitorID)
	}

	latest, err := a.loadLatestCertExpiries(ctx, monitorIDs)
	if err != nil {
		return fmt.Errorf("load latest cert expiries: %w", err)
	}

	now := time.Now()
	for _, c := range configs {
		days, ok := latest[c.monitorID]
		if !ok {
			// No certificate observed yet; leave any open alert untouched.
			continue
		}
		if days < c.minDays {
			if err := a.openTLSExpiryAlert(ctx, c, days); err != nil {
				a.logger.WithError(err).WithFields(map[string]interface{}{
					"monitor_id": c.monitorID,
				}).Error("Failed to open tls expiry alert")
			}
			continue
		}
		// Certificate renewed (or threshold lowered) — resolve any open alert.
		if err := a.resolveClearedTLSExpiryAlert(ctx, c, now); err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{
				"monitor_id": c.monitorID,
			}).Error("Failed to resolve cleared tls expiry alert")
		}
	}

	// Resolve tls_expiry alerts for monitors that dropped out of the evaluated
	// set entirely (threshold removed, monitor deleted/disabled).
	return a.resolveOrphanTLSExpiryAlerts(ctx, monitorIDs)
}

// loadTLSExpiryConfigs returns one config per enabled monitor whose
// monitors.config carries a tls_min_days_valid threshold. Monitors in an
// active maintenance window are excluded so planned work stays quiet.
func (a *Alerter) loadTLSExpiryConfigs(ctx context.Context) ([]tlsExpiryConfig, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT m.id, m.tenant_id, m.name, m.config
		FROM monitors m
		WHERE m.enabled = TRUE AND m.deleted_at IS NULL
		  AND m.config ? 'tls_min_days_valid'
		  AND NOT `+maintenance.InMaintenancePredicate("m")+`
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []tlsExpiryConfig
	for rows.Next() {
		var (
			id, tenantID uuid.UUID
			name         string
			configBytes  []byte
		)
		if err := rows.Scan(&id, &tenantID, &name, &configBytes); err != nil {
			return nil, err
		}

		var cfg struct {
			TLSMinDaysValid *int `json:"tls_min_days_valid"`
		}
		if err := json.Unmarshal(configBytes, &cfg); err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{"monitor_id": id}).Warn("Skipping monitor with unparseable config")
			continue
		}
		if cfg.TLSMinDaysValid == nil {
			continue
		}
		out = append(out, tlsExpiryConfig{
			monitorID:   id,
			tenantID:    tenantID,
			monitorName: name,
			minDays:     *cfg.TLSMinDaysValid,
		})
	}
	return out, rows.Err()
}

// loadLatestCertExpiries returns days-until-expiry per monitor, computed at
// evaluation time from the not_after recorded on the monitor's most recent
// check result that carries certificate info. Falls back to the worker's
// days_until_expiry snapshot when not_after is unparseable.
func (a *Alerter) loadLatestCertExpiries(ctx context.Context, monitorIDs []uuid.UUID) (map[uuid.UUID]int, error) {
	result := make(map[uuid.UUID]int)
	if len(monitorIDs) == 0 {
		return result, nil
	}
	rows, err := a.db.QueryContext(ctx, `
		SELECT DISTINCT ON (monitor_id) monitor_id,
			metrics_data #>> '{http,tls,not_after}',
			metrics_data #>> '{http,tls,days_until_expiry}'
		FROM check_results
		WHERE monitor_id = ANY($1)
		  AND result_source = 'monitor'
		  AND metrics_data #> '{http,tls}' IS NOT NULL
		ORDER BY monitor_id, created_at DESC
	`, pq.Array(monitorIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var mid uuid.UUID
		var notAfter, daysSnapshot sql.NullString
		if err := rows.Scan(&mid, &notAfter, &daysSnapshot); err != nil {
			return nil, err
		}
		if notAfter.Valid {
			if t, err := time.Parse(time.RFC3339, notAfter.String); err == nil {
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

// openTLSExpiryAlert opens a tls_expiry alert if one is not already open.
// Idempotent via the (monitor_id, kind, COALESCE(metric_name, '')) partial
// unique index, so it is safe across alerter replicas.
func (a *Alerter) openTLSExpiryAlert(ctx context.Context, c tlsExpiryConfig, days int) error {
	value := float64(days)
	threshold := float64(c.minDays)
	lastError := fmt.Sprintf("tls: expires in %dd (< %dd)", days, c.minDays)

	var alertID uuid.UUID
	err := a.db.QueryRowContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, kind, status,
			triggered_at, failure_count, last_error, metric_value, threshold_value,
			created_at, updated_at)
		VALUES ($1, $2, $3, NULL, 'tls_expiry', 'active', NOW(), 0, $4, $5, $6, NOW(), NOW())
		ON CONFLICT (monitor_id, kind, (COALESCE(metric_name, ''))) WHERE status IN ('active', 'acknowledged') DO NOTHING
		RETURNING id
	`, uuid.New(), c.tenantID, c.monitorID, lastError, value, threshold).Scan(&alertID)
	if err == sql.ErrNoRows {
		return nil // already open
	}
	if err != nil {
		return fmt.Errorf("insert tls expiry alert: %w", err)
	}

	record := alertRecord{
		ID: alertID, TenantID: c.tenantID, MonitorID: c.monitorID,
		Kind: notifications.KindTLSExpiry, TriggeredAt: time.Now(), LastError: &lastError,
		MetricValue: &value, ThresholdValue: &threshold,
	}
	binding := policyBinding{MonitorID: c.monitorID, TenantID: c.tenantID, MonitorName: c.monitorName}
	a.publishAlertEvent(ctx, "created", binding, &record, nil)
	return nil
}

// resolveClearedTLSExpiryAlert resolves the open tls_expiry alert of a monitor
// whose certificate is no longer inside the threshold window.
func (a *Alerter) resolveClearedTLSExpiryAlert(ctx context.Context, c tlsExpiryConfig, now time.Time) error {
	var alertID uuid.UUID
	var triggeredAt time.Time
	err := a.db.QueryRowContext(ctx, `
		SELECT id, triggered_at FROM alerts
		WHERE monitor_id = $1 AND kind = 'tls_expiry' AND status IN ('active', 'acknowledged')
	`, c.monitorID).Scan(&alertID, &triggeredAt)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("query open tls expiry alert: %w", err)
	}
	a.resolveTLSExpiryAlert(ctx, alertID, c.tenantID, c.monitorID, c.monitorName, triggeredAt, now)
	return nil
}

// resolveOrphanTLSExpiryAlerts resolves open tls_expiry alerts whose monitor is
// no longer in the evaluated set (threshold removed, monitor deleted/disabled).
// keep is the set of monitor IDs evaluated this tick.
func (a *Alerter) resolveOrphanTLSExpiryAlerts(ctx context.Context, keep []uuid.UUID) error {
	if keep == nil {
		// A nil slice encodes as SQL NULL and `= ANY(NULL)` filters every row
		// out; an empty array keeps the "resolve everything" semantics.
		keep = []uuid.UUID{}
	}
	rows, err := a.db.QueryContext(ctx, `
		SELECT al.id, al.tenant_id, al.monitor_id, m.name, al.triggered_at
		FROM alerts al
		JOIN monitors m ON m.id = al.monitor_id
		WHERE al.kind = 'tls_expiry'
		  AND al.status IN ('active', 'acknowledged')
		  AND NOT (al.monitor_id = ANY($1))
		  AND NOT `+maintenance.InMaintenancePredicate("m")+`
	`, pq.Array(keep))
	if err != nil {
		return fmt.Errorf("query orphan tls expiry alerts: %w", err)
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
			return fmt.Errorf("scan orphan tls expiry alert: %w", err)
		}
		orphans = append(orphans, o)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	now := time.Now()
	for _, o := range orphans {
		a.resolveTLSExpiryAlert(ctx, o.id, o.tenantID, o.monitorID, o.monitorName, o.triggeredAt, now)
	}
	return nil
}

// resolveTLSExpiryAlert resolves a single tls_expiry alert and notifies any
// channels that already fired for it.
func (a *Alerter) resolveTLSExpiryAlert(ctx context.Context, alertID, tenantID, monitorID uuid.UUID, monitorName string, triggeredAt, now time.Time) {
	resolvedAt, err := a.resolveAlert(ctx, alertID, now)
	if err != nil {
		a.logger.WithError(err).WithFields(map[string]interface{}{"alert_id": alertID}).Error("Failed to resolve tls expiry alert")
		return
	}
	record := alertRecord{
		ID: alertID, TenantID: tenantID, MonitorID: monitorID,
		Kind: notifications.KindTLSExpiry, TriggeredAt: triggeredAt,
	}
	binding := policyBinding{MonitorID: monitorID, TenantID: tenantID, MonitorName: monitorName}
	a.publishAlertEvent(ctx, "resolved", binding, &record, &resolvedAt)
	a.notifyFiredChannels(ctx, "resolved", binding, &record)
}
