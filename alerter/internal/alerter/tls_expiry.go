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
	"github.com/yassinebenameur/probara/shared/tenancy"
	"github.com/yassinebenameur/probara/shared/tlscert"
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
// (an HTTPS, SIP-over-TLS or TLS-wrapped TCP check's, located through
// shared/tlscert; the earliest across the presented chain, tlscert.Expiry)
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
		cert, ok := latest[c.monitorID]
		if !ok {
			// No certificate observed yet; leave any open alert untouched.
			continue
		}
		if cert.days < c.minDays {
			if err := a.openTLSExpiryAlert(ctx, c, cert); err != nil {
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
		  AND `+tenancy.LiveTenantSQL("m.tenant_id")+`
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

// certExpiry is the expiry a monitor's latest certificate snapshot reports
// (tlscert.Expiry: the chain's earliest, else the leaf's).
type certExpiry struct {
	days     int
	notAfter time.Time // zero when only the days snapshot parsed
	subject  string
	leaf     bool // the expiring certificate is the leaf itself
}

// loadLatestCertExpiries returns the expiry per monitor, days computed at
// evaluation time from the not_after recorded on the monitor's most recent
// check result that carries certificate info. Falls back to the worker's
// days snapshot when not_after is unparseable.
//
// The per-monitor LATERAL walks idx_check_results_monitor_id_created_at
// backwards and stops at the first row with certificate info; the 24h bound
// caps the walk for monitors whose recent results never carry TLS metrics
// (a DISTINCT ON over `monitor_id = ANY(...)` planned a sort over every
// historical row and timed out on large check_results tables). Monitors
// with no certificate observed in the window are skipped this tick, which
// leaves any open alert untouched — same as "no data yet".
func (a *Alerter) loadLatestCertExpiries(ctx context.Context, monitorIDs []uuid.UUID) (map[uuid.UUID]certExpiry, error) {
	result := make(map[uuid.UUID]certExpiry)
	if len(monitorIDs) == 0 {
		return result, nil
	}
	// Where the certificate sits in metrics_data ({http,tls}, {sip,tls}, …)
	// is shared/tlscert's to say; the status page reads the same location
	// through tlscert.Extract (TestSQLExprMatchesExtract pins the pair).
	// Which expiry counts (chain or leaf) is tlscert.ExpirySQL's to say.
	certExpr := tlscert.SQLExpr("metrics_data")
	notAfterExpr, daysExpr, subjectExpr := tlscert.ExpirySQL("cr.tls")
	rows, err := a.db.QueryContext(ctx, `
		SELECT m.id,
			`+notAfterExpr+`,
			`+daysExpr+`,
			`+subjectExpr+`,
			cr.tls #>> '{subject}'
		FROM unnest($1::uuid[]) AS m(id)
		JOIN LATERAL (
			SELECT `+certExpr+` AS tls
			FROM check_results
			WHERE monitor_id = m.id
			  AND result_source = 'monitor'
			  AND created_at > NOW() - INTERVAL '24 hours'
			  AND `+certExpr+` IS NOT NULL
			ORDER BY created_at DESC
			LIMIT 1
		) cr ON TRUE
	`, pq.Array(monitorIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var mid uuid.UUID
		var notAfter, daysSnapshot, subject, leafSubject sql.NullString
		if err := rows.Scan(&mid, &notAfter, &daysSnapshot, &subject, &leafSubject); err != nil {
			return nil, err
		}
		cert := certExpiry{subject: subject.String, leaf: subject.String == leafSubject.String}
		if notAfter.Valid {
			if t, err := time.Parse(time.RFC3339, notAfter.String); err == nil {
				cert.days = int(math.Floor(time.Until(t).Hours() / 24))
				cert.notAfter = t
				result[mid] = cert
				continue
			}
		}
		if daysSnapshot.Valid {
			if _, err := fmt.Sscanf(daysSnapshot.String, "%d", &cert.days); err == nil {
				result[mid] = cert
			}
		}
	}
	return result, rows.Err()
}

// openTLSExpiryAlert opens a tls_expiry alert if one is not already open.
// Idempotent via the (monitor_id, kind, COALESCE(metric_name, ”)) partial
// unique index, so it is safe across alerter replicas.
func (a *Alerter) openTLSExpiryAlert(ctx context.Context, c tlsExpiryConfig, cert certExpiry) error {
	value := float64(cert.days)
	threshold := float64(c.minDays)
	lastError := tlsExpiryMessage(cert, c.minDays)

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tls expiry alert transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	reopened, err := reopenRecentAlertTx(ctx, tx,
		alertIdentity{tenantID: c.tenantID, kind: notifications.KindTLSExpiry, monitorID: c.monitorID},
		[]alertColumn{{"last_error", lastError}, {"metric_value", value}, {"threshold_value", threshold}})
	if reopenSkipped(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var alertID uuid.UUID
	if reopened != nil {
		alertID = reopened.id
		if err := a.noteRelapseTx(ctx, tx, c.tenantID, reopened); err != nil {
			return err
		}
	} else {
		err = tx.QueryRowContext(ctx, `
			INSERT INTO alerts (id, tenant_id, monitor_id, kind, status,
				triggered_at, failure_count, last_error, metric_value, threshold_value,
				created_at, updated_at)
			VALUES ($1, $2, $3, 'tls_expiry', 'active', NOW(), 0, $4, $5, $6, NOW(), NOW())
			ON CONFLICT (monitor_id, kind, (COALESCE(metric_name, ''))) WHERE status IN ('active', 'acknowledged') DO NOTHING
			RETURNING id
		`, uuid.New(), c.tenantID, c.monitorID, lastError, value, threshold).Scan(&alertID)
		if err == sql.ErrNoRows {
			return nil // already open
		}
		if err != nil {
			return fmt.Errorf("insert tls expiry alert: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tls expiry alert transaction: %w", err)
	}

	record := alertRecord{
		ID: alertID, TenantID: c.tenantID, MonitorID: c.monitorID,
		Kind: notifications.KindTLSExpiry, TriggeredAt: time.Now(), LastError: &lastError,
		MetricValue: &value, ThresholdValue: &threshold,
	}
	if reopened != nil {
		reopened.apply(&record)
	}
	binding := alertSubject{MonitorID: c.monitorID, TenantID: c.tenantID, MonitorName: c.monitorName}
	a.publishAlertEvent(ctx, "created", binding, &record, nil)
	return nil
}

// tlsExpiryMessage is the alert's last_error, which every notification and
// the alerts page print: which certificate of the chain expires, and when.
// It is also how present learns whether the certificate is an intermediate
// (notifications.TLSExpiryCertificate reads it back), so it goes through the
// shared formatter.
func tlsExpiryMessage(cert certExpiry, minDays int) string {
	return notifications.FormatTLSExpiryError(notifications.TLSExpiryCertificate{
		Subject:      cert.subject,
		Intermediate: !cert.leaf,
		NotAfter:     cert.notAfter,
	}, cert.days, minDays)
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
	// Renewed (or now outside the window): a recovery.
	a.resolveTLSExpiryAlert(ctx, alertID, c.tenantID, c.monitorID, c.monitorName, triggeredAt, now, resolvedRecovered)
	return nil
}

// resolveOrphanTLSExpiryAlerts resolves open tls_expiry alerts whose monitor is
// no longer in the evaluated set (threshold removed, monitor deleted/disabled).
// keep is the set of monitor IDs evaluated this tick.
func (a *Alerter) resolveOrphanTLSExpiryAlerts(ctx context.Context, keep []uuid.UUID) error {
	keep = nonNilIDs(keep)
	rows, err := a.db.QueryContext(ctx, `
		SELECT al.id, al.tenant_id, al.monitor_id, m.name, al.triggered_at
		FROM alerts al
		JOIN monitors m ON m.id = al.monitor_id
		WHERE al.kind = 'tls_expiry'
		  AND al.status IN ('active', 'acknowledged')
		  AND `+tenancy.LiveTenantSQL("al.tenant_id")+`
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
		a.resolveTLSExpiryAlert(ctx, o.id, o.tenantID, o.monitorID, o.monitorName, o.triggeredAt, now, resolvedOutOfScope)
	}
	return nil
}

// resolveTLSExpiryAlert resolves a single tls_expiry alert and notifies any
// channels that already fired for it.
func (a *Alerter) resolveTLSExpiryAlert(ctx context.Context, alertID, tenantID, monitorID uuid.UUID, monitorName string, triggeredAt, now time.Time, reason resolveReason) {
	resolvedAt, err := a.resolveAlert(ctx, alertID, reason)
	if err != nil {
		a.logger.WithError(err).WithFields(map[string]interface{}{"alert_id": alertID}).Error("Failed to resolve tls expiry alert")
		return
	}
	record := alertRecord{
		ID: alertID, TenantID: tenantID, MonitorID: monitorID,
		Kind: notifications.KindTLSExpiry, TriggeredAt: triggeredAt,
	}
	binding := alertSubject{MonitorID: monitorID, TenantID: tenantID, MonitorName: monitorName}
	a.publishAlertEvent(ctx, "resolved", binding, &record, &resolvedAt)
	a.notifyFiredChannels(ctx, "resolved", binding, &record)
}
