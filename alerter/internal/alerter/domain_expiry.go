package alerter

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/domainexpiry"
	"github.com/yassinebenameur/probara/shared/maintenance"
	sharedmodels "github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/notifications"
)

// Domain-expiry evaluation bounds.
const (
	// domainExpiryLookbackSQL is how far back a recorded expiry still
	// counts. A registration date only moves by renewal, so a few days of
	// failed lookups (a registry outage, a rate limit) do not lose it; it is
	// well over the longest interval (24 h), and the walk stays small (at
	// most one row an hour).
	domainExpiryLookbackSQL = "7 days"
	// domainExpiryUnknownAfter is how many check rounds in the lookback,
	// with no expiry among them, raise the "expiry unknown" warning: one
	// failed lookup never does.
	domainExpiryUnknownAfter = 2
	// domainRoundSeconds groups result rows into check rounds: the rows of
	// one dispatch (several locations, a run-now next to a scheduled check)
	// land within minutes, while scheduled rounds are at least an hour apart.
	domainRoundSeconds = 600
)

// domainExpiryMonitor is one enabled domain monitor and its warning window.
type domainExpiryMonitor struct {
	monitorID   uuid.UUID
	tenantID    uuid.UUID
	monitorName string
	minDays     int
}

// domainObservation is what the lookback holds for one monitor: the newest
// recorded expiry, and how many lookups could not determine one.
type domainObservation struct {
	expiresAt     sql.NullString
	registrar     sql.NullString
	renewalGrace  bool
	unknownRounds int
	unknownReason sql.NullString
	// noExpiryPublished: the newest lookup says the registry never publishes
	// a date (domainexpiry.PublishesNoExpiry), so "expiry unknown" is the
	// normal state and nobody could act on an alert about it.
	noExpiryPublished bool
}

// evaluateDomainExpiry opens, refreshes and resolves domain_expiry alerts.
// Like tls_expiry it is orthogonal to the availability state machine: a
// registration inside its warning window pages as a warning while the
// monitor stays up (the check only fails once the registration has expired,
// which is also an availability alert).
//
// Per monitor, from the check results of the lookback:
//   - a recorded expiry with fewer days left than min_days_valid opens the
//     alert (metric_value = days left, negative once expired);
//   - a recorded expiry outside the window resolves it (renewed, or the
//     window narrowed);
//   - a passed date inside the registry's renew grace period counts as 0
//     days left (registered, inside the window), not as expired;
//   - no recorded expiry but domainExpiryUnknownAfter or more check rounds
//     that could not determine one opens it with metric_value NULL ("expiry
//     unknown"): an undetermined expiry never pages as an outage, but it
//     does not stay silent forever either. Not for a registry known never
//     to publish a date: there it is the normal state, so no alert opens and
//     an open "unknown" one resolves;
//   - nothing else (no rows yet, a single failed lookup, only "not
//     registered" answers, which the availability alert covers) leaves any
//     open alert as it is.
//
// An open alert's value and last_error follow every evaluation, so reminders
// say how many days are left now (and that the registration has expired
// once it has). When its form changes between dated and "expiry unknown"
// it means something else, so it is resolved and a new one opened, and
// channels hear about both. The insert is idempotent through the open-alert unique index
// and the refresh and resolve are guarded updates, so two alerter replicas
// running this at once open one alert and resolve it once.
func (a *Alerter) evaluateDomainExpiry(ctx context.Context) error {
	monitors, err := a.loadDomainExpiryMonitors(ctx)
	if err != nil {
		return fmt.Errorf("load domain monitors: %w", err)
	}
	ids := make([]uuid.UUID, 0, len(monitors))
	for _, m := range monitors {
		ids = append(ids, m.monitorID)
	}
	observations, err := a.loadDomainObservations(ctx, ids)
	if err != nil {
		return fmt.Errorf("load domain observations: %w", err)
	}

	now := time.Now()
	for _, m := range monitors {
		obs := observations[m.monitorID]
		var err error
		if expires, ok := parseExpiry(obs.expiresAt); ok {
			days := domainexpiry.DaysUntil(expires, now)
			lastError := domainExpiryLastError(expires, days, m.minDays, obs.registrar.String)
			if days < 0 && obs.renewalGrace {
				days = 0
				lastError = fmt.Sprintf("domain: expired %s, in the registry's renew grace period", expires.UTC().Format("2006-01-02"))
				if obs.registrar.String != "" {
					lastError += "; registrar: " + obs.registrar.String
				}
			}
			if days < m.minDays {
				value := float64(days)
				err = a.openDomainExpiryAlert(ctx, m, &value, lastError)
			} else {
				// Renewed, or the window narrowed: a recovery.
				err = a.resolveDomainExpiryAlert(ctx, m.monitorID, m.tenantID, m.monitorName, now, resolvedRecovered)
			}
		} else if obs.noExpiryPublished {
			// "Expiry unknown" is this registry's normal state: nothing was
			// fixed, so a later alert is a new one.
			err = a.resolveDomainExpiryAlert(ctx, m.monitorID, m.tenantID, m.monitorName, now, resolvedOutOfScope)
		} else if obs.unknownRounds >= domainExpiryUnknownAfter {
			err = a.openDomainExpiryAlert(ctx, m, nil, "domain: expiry unknown: "+obs.unknownReason.String)
		}
		if err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{"monitor_id": m.monitorID}).Error("Failed to evaluate domain expiry alert")
		}
	}

	return a.resolveOrphanDomainExpiryAlerts(ctx, ids, now)
}

func parseExpiry(s sql.NullString) (time.Time, bool) {
	if !s.Valid {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s.String)
	return t, err == nil
}

// domainExpiryLastError is the alert's last_error: the date, the days left
// against the window, and the registrar, the facts a recipient renews with.
func domainExpiryLastError(expires time.Time, days, minDays int, registrar string) string {
	date := expires.UTC().Format("2006-01-02")
	s := fmt.Sprintf("domain: expires %s (%dd < %dd)", date, days, minDays)
	if days < 0 {
		s = fmt.Sprintf("domain: expired %s (%dd ago)", date, -days)
	}
	if registrar != "" {
		s += "; registrar: " + registrar
	}
	return s
}

// loadDomainExpiryMonitors returns every enabled domain monitor outside an
// active maintenance window, with its effective warning window.
func (a *Alerter) loadDomainExpiryMonitors(ctx context.Context) ([]domainExpiryMonitor, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT m.id, m.tenant_id, m.name, m.config
		FROM monitors m
		WHERE m.type = $1 AND m.enabled = TRUE AND m.deleted_at IS NULL
		  AND NOT `+maintenance.InMaintenancePredicate("m")+`
	`, string(sharedmodels.MonitorTypeDomain))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domainExpiryMonitor
	for rows.Next() {
		var (
			m           domainExpiryMonitor
			configBytes []byte
		)
		if err := rows.Scan(&m.monitorID, &m.tenantID, &m.monitorName, &configBytes); err != nil {
			return nil, err
		}
		var cfg sharedmodels.DomainMonitorConfig
		if err := json.Unmarshal(configBytes, &cfg); err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{"monitor_id": m.monitorID}).Warn("Skipping domain monitor with unparseable config")
			continue
		}
		m.minDays = cfg.EffectiveMinDaysValid()
		out = append(out, m)
	}
	return out, rows.Err()
}

// loadDomainObservations reads, per monitor, the newest recorded expiry in
// the lookback and the undetermined lookups in it, through shared/domainexpiry
// (SQLExpr locates the snapshot; TestSQLExprMatchesExtract pins it to the Go
// reader). Both walks use idx_check_results_monitor_id_created_at and stop
// at the lookback.
func (a *Alerter) loadDomainObservations(ctx context.Context, monitorIDs []uuid.UUID) (map[uuid.UUID]domainObservation, error) {
	out := make(map[uuid.UUID]domainObservation, len(monitorIDs))
	if len(monitorIDs) == 0 {
		return out, nil
	}
	obj := domainexpiry.SQLExpr("cr.metrics_data")
	rows, err := a.db.QueryContext(ctx, `
		SELECT m.id, k.expires_at, k.registrar, COALESCE(k.renewal_grace, FALSE),
			COALESCE(u.unknown_rounds, 0), u.unknown_reason, COALESCE(n.no_expiry_published, FALSE)
		FROM unnest($1::uuid[]) AS m(id)
		LEFT JOIN LATERAL (
			SELECT `+domainexpiry.ExpiresAtSQL(obj)+` AS expires_at, `+obj+` ->> 'registrar' AS registrar,
				(`+obj+` ->> 'renewal_grace') = 'true' AS renewal_grace
			FROM check_results cr
			WHERE cr.monitor_id = m.id
			  AND cr.result_source = 'monitor'
			  AND cr.created_at > NOW() - INTERVAL '`+domainExpiryLookbackSQL+`'
			  AND `+domainexpiry.ExpiresAtSQL(obj)+` IS NOT NULL
			ORDER BY cr.created_at DESC
			LIMIT 1
		) k ON TRUE
		LEFT JOIN LATERAL (
			SELECT COUNT(DISTINCT FLOOR(EXTRACT(EPOCH FROM cr.created_at) / `+fmt.Sprint(domainRoundSeconds)+`)) AS unknown_rounds,
				(ARRAY_AGG(`+domainexpiry.UnknownReasonSQL(obj)+` ORDER BY cr.created_at DESC))[1] AS unknown_reason
			FROM check_results cr
			WHERE cr.monitor_id = m.id
			  AND cr.result_source = 'monitor'
			  AND cr.created_at > NOW() - INTERVAL '`+domainExpiryLookbackSQL+`'
			  AND `+domainexpiry.UnknownReasonSQL(obj)+` IS NOT NULL
		) u ON TRUE
		LEFT JOIN LATERAL (
			SELECT (`+obj+` ->> 'no_expiry_published') = 'true' AS no_expiry_published
			FROM check_results cr
			WHERE cr.monitor_id = m.id
			  AND cr.result_source = 'monitor'
			  AND cr.created_at > NOW() - INTERVAL '`+domainExpiryLookbackSQL+`'
			  AND `+obj+` IS NOT NULL
			ORDER BY cr.created_at DESC
			LIMIT 1
		) n ON TRUE
	`, pq.Array(monitorIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id  uuid.UUID
			obs domainObservation
		)
		if err := rows.Scan(&id, &obs.expiresAt, &obs.registrar, &obs.renewalGrace, &obs.unknownRounds, &obs.unknownReason, &obs.noExpiryPublished); err != nil {
			return nil, err
		}
		out[id] = obs
	}
	return out, rows.Err()
}

// openDomainExpiryAlert opens the monitor's domain_expiry alert, or, when
// one is already open, brings its value, window and last_error up to date.
// value nil is "expiry unknown".
func (a *Alerter) openDomainExpiryAlert(ctx context.Context, m domainExpiryMonitor, value *float64, lastError string) error {
	threshold := float64(m.minDays)
	// An open alert of the other form ("expiry unknown" vs dated) is a
	// different message: close it so its channels hear it resolve, then
	// open the new one, which pages as created.
	var openUnknown bool
	err := a.db.QueryRowContext(ctx, `
		SELECT metric_value IS NULL FROM alerts
		WHERE monitor_id = $1 AND kind = 'domain_expiry' AND status IN ('active', 'acknowledged')
	`, m.monitorID).Scan(&openUnknown)
	switch {
	case err == nil && openUnknown != (value == nil):
		// A change of form, not a recovery: never reopenable as a relapse.
		if err := a.resolveDomainExpiryAlert(ctx, m.monitorID, m.tenantID, m.monitorName, time.Now(), resolvedOutOfScope); err != nil {
			return err
		}
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("query open domain expiry alert: %w", err)
	}

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin domain expiry alert transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	// A relapse of the same form only (relapse.go): a dated alert never
	// comes back as "expiry unknown", or the other way round.
	unknown := value == nil
	reopened, err := reopenRecentAlertTx(ctx, tx,
		alertIdentity{tenantID: m.tenantID, kind: notifications.KindDomainExpiry, monitorID: m.monitorID, metricValueNull: &unknown},
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
		if err := a.noteRelapseTx(ctx, tx, m.tenantID, reopened); err != nil {
			return err
		}
	} else {
		err = tx.QueryRowContext(ctx, `
			INSERT INTO alerts (id, tenant_id, monitor_id, kind, status,
				triggered_at, failure_count, last_error, metric_value, threshold_value,
				created_at, updated_at)
			VALUES ($1, $2, $3, 'domain_expiry', 'active', NOW(), 0, $4, $5, $6, NOW(), NOW())
			ON CONFLICT (monitor_id, kind, (COALESCE(metric_name, ''))) WHERE status IN ('active', 'acknowledged') DO NOTHING
			RETURNING id
		`, uuid.New(), m.tenantID, m.monitorID, lastError, value, threshold).Scan(&alertID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		if _, err := a.db.ExecContext(ctx, `
			UPDATE alerts
			SET metric_value = $2, threshold_value = $3, last_error = $4, updated_at = NOW()
			WHERE monitor_id = $1 AND kind = 'domain_expiry' AND status IN ('active', 'acknowledged')
			  -- Same form only: a replica that opened the other form a moment
			  -- ago keeps it until the next evaluation replaces it properly.
			  AND (metric_value IS NULL) = ($2::double precision IS NULL)
			  AND (metric_value IS DISTINCT FROM $2 OR threshold_value IS DISTINCT FROM $3 OR last_error IS DISTINCT FROM $4)
		`, m.monitorID, value, threshold, lastError); err != nil {
			return fmt.Errorf("refresh domain expiry alert: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("insert domain expiry alert: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit domain expiry alert transaction: %w", err)
	}

	record := alertRecord{
		ID: alertID, TenantID: m.tenantID, MonitorID: m.monitorID,
		Kind: notifications.KindDomainExpiry, TriggeredAt: time.Now(), LastError: &lastError,
		MetricValue: value, ThresholdValue: &threshold,
	}
	if reopened != nil {
		reopened.apply(&record)
	}
	binding := alertSubject{MonitorID: m.monitorID, TenantID: m.tenantID, MonitorName: m.monitorName}
	a.publishAlertEvent(ctx, "created", binding, &record, nil)
	return nil
}

// resolveDomainExpiryAlert resolves the monitor's open domain_expiry alert,
// if any, and notifies the channels it already reached. reason says whether
// the registration was renewed (a recovery a relapse may reopen) or the
// alert closed for any other reason.
func (a *Alerter) resolveDomainExpiryAlert(ctx context.Context, monitorID, tenantID uuid.UUID, monitorName string, now time.Time, reason resolveReason) error {
	var (
		alertID     uuid.UUID
		triggeredAt time.Time
		value       sql.NullFloat64
		threshold   sql.NullFloat64
	)
	err := a.db.QueryRowContext(ctx, `
		SELECT id, triggered_at, metric_value, threshold_value FROM alerts
		WHERE monitor_id = $1 AND kind = 'domain_expiry' AND status IN ('active', 'acknowledged')
	`, monitorID).Scan(&alertID, &triggeredAt, &value, &threshold)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("query open domain expiry alert: %w", err)
	}
	resolvedAt, err := a.resolveAlert(ctx, alertID, reason)
	if errors.Is(err, errAlertAlreadyResolved) {
		return nil // a sibling replica resolved it
	}
	if err != nil {
		return err
	}
	// The values the alert last carried: an "expiry unknown" alert (NULL)
	// resolves with neutral wording rather than "renewed".
	record := alertRecord{
		ID: alertID, TenantID: tenantID, MonitorID: monitorID,
		Kind: notifications.KindDomainExpiry, TriggeredAt: triggeredAt,
	}
	if value.Valid {
		record.MetricValue = &value.Float64
	}
	if threshold.Valid {
		record.ThresholdValue = &threshold.Float64
	}
	binding := alertSubject{MonitorID: monitorID, TenantID: tenantID, MonitorName: monitorName}
	a.publishAlertEvent(ctx, "resolved", binding, &record, &resolvedAt)
	a.notifyFiredChannels(ctx, "resolved", binding, &record)
	return nil
}

// resolveOrphanDomainExpiryAlerts resolves open domain_expiry alerts whose
// monitor left the evaluated set (deleted, disabled, retyped). Monitors in
// maintenance keep theirs, as for tls_expiry.
func (a *Alerter) resolveOrphanDomainExpiryAlerts(ctx context.Context, keep []uuid.UUID, now time.Time) error {
	rows, err := a.db.QueryContext(ctx, `
		SELECT al.monitor_id, al.tenant_id, m.name
		FROM alerts al
		JOIN monitors m ON m.id = al.monitor_id
		WHERE al.kind = 'domain_expiry'
		  AND al.status IN ('active', 'acknowledged')
		  AND NOT (al.monitor_id = ANY($1))
		  AND NOT `+maintenance.InMaintenancePredicate("m")+`
	`, pq.Array(nonNilIDs(keep)))
	if err != nil {
		return fmt.Errorf("query orphan domain expiry alerts: %w", err)
	}
	type orphan struct {
		monitorID, tenantID uuid.UUID
		name                string
	}
	var orphans []orphan
	for rows.Next() {
		var o orphan
		if err := rows.Scan(&o.monitorID, &o.tenantID, &o.name); err != nil {
			rows.Close()
			return fmt.Errorf("scan orphan domain expiry alert: %w", err)
		}
		orphans = append(orphans, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, o := range orphans {
		if err := a.resolveDomainExpiryAlert(ctx, o.monitorID, o.tenantID, o.name, now, resolvedOutOfScope); err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{"monitor_id": o.monitorID}).Error("Failed to resolve orphan domain expiry alert")
		}
	}
	return nil
}
