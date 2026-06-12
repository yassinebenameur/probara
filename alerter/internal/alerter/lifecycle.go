package alerter

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// runLifecycle is the transition-driven replacement for evaluateAlerts (spec §6):
// refresh group states, open alerts for down monitors, resolve alerts for
// recovered monitors, dispatch notifications by escalation delay + reminders.
func (a *Alerter) runLifecycle(ctx context.Context) error {
	if err := a.refreshGroupStates(ctx); err != nil {
		return err
	}
	if err := a.openAlertsForDownMonitors(ctx); err != nil {
		return err
	}
	if err := a.resolveAlertsForRecoveredMonitors(ctx); err != nil {
		return err
	}
	if err := a.annotateOpenAlertRootCauses(ctx); err != nil {
		return err
	}
	return a.dispatchOpenAlerts(ctx)
}

// rootCauseLateral returns a LATERAL subquery selecting the deepest
// currently-down upstream dependency of the monitor referenced by monitorCol.
// Ties break toward the earliest outage. The depth cap guards against
// race-created dependency cycles; application-level validation forbids them.
func rootCauseLateral(monitorCol string) string {
	return fmt.Sprintf(`
		LEFT JOIN LATERAL (
			WITH RECURSIVE upstream AS (
				SELECT md.depends_on_id AS id, 1 AS depth
				FROM monitor_dependencies md
				WHERE md.monitor_id = %[1]s
				UNION
				SELECT md.depends_on_id, u.depth + 1
				FROM upstream u
				JOIN monitor_dependencies md ON md.monitor_id = u.id
				WHERE u.depth < 25
			)
			SELECT um.id, um.name, um.last_state_change_at
			FROM upstream u
			JOIN monitors um ON um.id = u.id
			WHERE um.current_state = 'down'
			  AND um.deleted_at IS NULL AND um.enabled = TRUE
			  AND um.id <> %[1]s
			ORDER BY u.depth DESC, um.last_state_change_at ASC
			LIMIT 1
		) rc ON TRUE`, monitorCol)
}

// annotateOpenAlertRootCauses recomputes the root-cause annotation for every
// open alert each tick: it catches upstreams detected after the alert opened
// and clears the annotation when the upstream recovers.
func (a *Alerter) annotateOpenAlertRootCauses(ctx context.Context) error {
	_, err := a.db.ExecContext(ctx, `
		UPDATE alerts al
		SET root_cause_monitor_id = x.rc_id,
			root_cause_down_since = x.rc_down_since,
			updated_at = NOW()
		FROM (
			SELECT al2.id AS alert_id, rc.id AS rc_id, rc.last_state_change_at AS rc_down_since
			FROM alerts al2`+rootCauseLateral("al2.monitor_id")+`
			WHERE al2.status IN ('active', 'acknowledged')
		) x
		WHERE al.id = x.alert_id
		  AND (al.root_cause_monitor_id IS DISTINCT FROM x.rc_id
			OR al.root_cause_down_since IS DISTINCT FROM x.rc_down_since)
	`)
	if err != nil {
		return fmt.Errorf("annotate alert root causes: %w", err)
	}
	return nil
}

// refreshGroupStates derives group monitor state from members: down if any
// non-deleted enabled member is down, else up if all known up/suspect, else unknown.
func (a *Alerter) refreshGroupStates(ctx context.Context) error {
	_, err := a.db.ExecContext(ctx, `
		WITH member_states AS (
			SELECT g.id AS group_id,
				BOOL_OR(child.current_state = 'down') AS any_down,
				BOOL_AND(child.current_state IN ('up', 'suspect')) AS all_known_up
			FROM monitors g
			JOIN monitor_groups mg ON mg.group_id = g.id
			JOIN monitors child ON child.id = mg.monitor_id
				AND child.deleted_at IS NULL AND child.enabled = TRUE
			WHERE g.type = 'group' AND g.deleted_at IS NULL
			GROUP BY g.id
		)
		UPDATE monitors m
		SET current_state = CASE
				WHEN ms.any_down THEN 'down'
				WHEN ms.all_known_up THEN 'up'
				ELSE 'unknown' END,
			last_state_change_at = CASE
				WHEN m.current_state IS DISTINCT FROM (CASE
					WHEN ms.any_down THEN 'down'
					WHEN ms.all_known_up THEN 'up'
					ELSE 'unknown' END) THEN NOW()
				ELSE m.last_state_change_at END,
			updated_at = NOW()
		FROM member_states ms
		WHERE m.id = ms.group_id
	`)
	if err != nil {
		return fmt.Errorf("refresh group states: %w", err)
	}
	return nil
}

// openAlertsForDownMonitors creates the single outage alert for each down
// monitor without one. The partial unique index idx_alerts_one_open_per_monitor
// makes this race-safe across alerter replicas.
func (a *Alerter) openAlertsForDownMonitors(ctx context.Context) error {
	rows, err := a.db.QueryContext(ctx, `
		SELECT m.id, m.tenant_id, m.name, m.consecutive_failures,
			(SELECT cr.error_message FROM check_results cr
			 WHERE cr.monitor_id = m.id AND cr.status IN ('failure', 'error')
			 ORDER BY cr.created_at DESC LIMIT 1) AS last_error,
			rc.id, rc.name, rc.last_state_change_at
		FROM monitors m`+rootCauseLateral("m.id")+`
		WHERE m.current_state = 'down' AND m.enabled = TRUE AND m.deleted_at IS NULL
		  AND NOT EXISTS (
			SELECT 1 FROM alerts al
			WHERE al.monitor_id = m.id AND al.status IN ('active', 'acknowledged'))
	`)
	if err != nil {
		return fmt.Errorf("query down monitors: %w", err)
	}
	defer rows.Close()

	var monitors []downMonitor
	for rows.Next() {
		var dm downMonitor
		if err := rows.Scan(&dm.id, &dm.tenantID, &dm.name, &dm.failCount, &dm.lastError,
			&dm.rootCauseID, &dm.rootCauseName, &dm.rootCauseDownSince); err != nil {
			return fmt.Errorf("scan down monitor: %w", err)
		}
		monitors = append(monitors, dm)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, dm := range monitors {
		if err := a.openOutageAlert(ctx, dm); err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{"monitor_id": dm.id}).Error("Failed to open outage alert")
		}
	}
	return nil
}

type downMonitor struct {
	id                 uuid.UUID
	tenantID           uuid.UUID
	name               string
	failCount          int
	lastError          sql.NullString
	rootCauseID        uuid.NullUUID
	rootCauseName      sql.NullString
	rootCauseDownSince sql.NullTime
}

// setRootCause copies nullable root-cause scan columns onto an alertRecord.
func setRootCause(record *alertRecord, id uuid.NullUUID, name sql.NullString, downSince sql.NullTime) {
	if !id.Valid {
		return
	}
	rcID := id.UUID
	record.RootCauseMonitorID = &rcID
	if name.Valid {
		record.RootCauseMonitorName = &name.String
	}
	if downSince.Valid {
		record.RootCauseDownSince = &downSince.Time
	}
}

// rootCause converts the nullable scan fields into alertRecord pointers.
func (dm downMonitor) rootCause() (*uuid.UUID, *string, *time.Time) {
	if !dm.rootCauseID.Valid {
		return nil, nil, nil
	}
	id := dm.rootCauseID.UUID
	var name *string
	if dm.rootCauseName.Valid {
		name = &dm.rootCauseName.String
	}
	var downSince *time.Time
	if dm.rootCauseDownSince.Valid {
		downSince = &dm.rootCauseDownSince.Time
	}
	return &id, name, downSince
}

func (a *Alerter) openOutageAlert(ctx context.Context, dm downMonitor) error {
	var lastError *string
	if dm.lastError.Valid {
		lastError = &dm.lastError.String
	}
	rootCauseID, rootCauseName, rootCauseDownSince := dm.rootCause()

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin outage alert transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var alertID uuid.UUID
	err = tx.QueryRowContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, status,
			triggered_at, failure_count, last_error, root_cause_monitor_id, root_cause_down_since,
			created_at, updated_at)
		VALUES ($1, $2, $3, NULL, 'active', NOW(), $4, $5, $6, $7, NOW(), NOW())
		ON CONFLICT (monitor_id) WHERE status IN ('active', 'acknowledged') DO NOTHING
		RETURNING id
	`, uuid.New(), dm.tenantID, dm.id, dm.failCount, lastError, rootCauseID, rootCauseDownSince).Scan(&alertID)
	if err == sql.ErrNoRows {
		return nil // another replica won the race
	}
	if err != nil {
		return fmt.Errorf("insert outage alert: %w", err)
	}

	var autoIncident bool
	if err := tx.QueryRowContext(ctx,
		`SELECT auto_create_incident FROM tenants WHERE id = $1`, dm.tenantID).Scan(&autoIncident); err != nil {
		return fmt.Errorf("load incident toggle: %w", err)
	}
	if autoIncident {
		record := alertRecord{ID: alertID, TenantID: dm.tenantID, MonitorID: dm.id, FailureCount: dm.failCount, LastError: lastError}
		binding := policyBinding{MonitorID: dm.id, TenantID: dm.tenantID, MonitorName: dm.name, CreateIncidentOnFire: true}
		if err := a.ensureAutoIncidentForAlertTx(ctx, tx, binding, &record); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit outage alert transaction: %w", err)
	}

	record := alertRecord{
		ID: alertID, TenantID: dm.tenantID, MonitorID: dm.id,
		FailureCount: dm.failCount, LastError: lastError, TriggeredAt: time.Now(),
		RootCauseMonitorID: rootCauseID, RootCauseMonitorName: rootCauseName, RootCauseDownSince: rootCauseDownSince,
	}
	binding := policyBinding{MonitorID: dm.id, TenantID: dm.tenantID, MonitorName: dm.name}
	a.publishAlertEvent(ctx, "created", binding, &record, nil)
	return nil
}

// resolveAlertsForRecoveredMonitors resolves open alerts whose monitor is up,
// disabled, or deleted.
func (a *Alerter) resolveAlertsForRecoveredMonitors(ctx context.Context) error {
	rows, err := a.db.QueryContext(ctx, `
		SELECT al.id, al.tenant_id, al.monitor_id, m.name, al.triggered_at, al.failure_count, al.last_error,
			al.root_cause_monitor_id, al.root_cause_down_since, rcm.name
		FROM alerts al
		JOIN monitors m ON m.id = al.monitor_id
		LEFT JOIN monitors rcm ON rcm.id = al.root_cause_monitor_id
		WHERE al.status IN ('active', 'acknowledged')
		  AND (m.current_state = 'up' OR m.deleted_at IS NOT NULL OR m.enabled = FALSE)
	`)
	if err != nil {
		return fmt.Errorf("query recovered alerts: %w", err)
	}
	defer rows.Close()

	type openAlert struct {
		record      alertRecord
		monitorName string
	}
	var toResolve []openAlert
	for rows.Next() {
		var oa openAlert
		var lastError sql.NullString
		var rcID uuid.NullUUID
		var rcDownSince sql.NullTime
		var rcName sql.NullString
		if err := rows.Scan(&oa.record.ID, &oa.record.TenantID, &oa.record.MonitorID,
			&oa.monitorName, &oa.record.TriggeredAt, &oa.record.FailureCount, &lastError,
			&rcID, &rcDownSince, &rcName); err != nil {
			return fmt.Errorf("scan recovered alert: %w", err)
		}
		if lastError.Valid {
			oa.record.LastError = &lastError.String
		}
		setRootCause(&oa.record, rcID, rcName, rcDownSince)
		toResolve = append(toResolve, oa)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, oa := range toResolve {
		resolvedAt, err := a.resolveAlert(ctx, oa.record.ID, time.Now())
		if err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{"alert_id": oa.record.ID}).Error("Failed to resolve alert")
			continue
		}
		binding := policyBinding{MonitorID: oa.record.MonitorID, TenantID: oa.record.TenantID, MonitorName: oa.monitorName}
		a.publishAlertEvent(ctx, "resolved", binding, &oa.record, &resolvedAt)
		a.notifyFiredChannels(ctx, "resolved", binding, &oa.record)
	}
	return nil
}

// channelTarget is a routed channel with its escalation delay.
type channelTarget struct {
	channel alertChannel
	delay   time.Duration
}

// resolveChannelTargets returns the effective routing for a monitor:
// monitor_channels when mode=custom, else tenant_default_channels.
func (a *Alerter) resolveChannelTargets(ctx context.Context, tenantID, monitorID uuid.UUID) ([]channelTarget, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT ac.id, ac.name, ac.type, ac.config, ac.is_active, x.delay_seconds
		FROM (
			SELECT mc.channel_id, mc.delay_seconds, 0 AS pos
			FROM monitor_channels mc
			JOIN monitors m ON m.id = mc.monitor_id
			WHERE mc.monitor_id = $2 AND m.notification_mode = 'custom'
			UNION ALL
			SELECT tdc.channel_id, tdc.delay_seconds, tdc.position
			FROM tenant_default_channels tdc
			JOIN monitors m ON m.tenant_id = tdc.tenant_id
			WHERE tdc.tenant_id = $1 AND m.id = $2 AND m.notification_mode = 'default'
		) x
		JOIN alert_channels ac ON ac.id = x.channel_id
		ORDER BY x.pos, ac.id
	`, tenantID, monitorID)
	if err != nil {
		return nil, fmt.Errorf("resolve channel targets: %w", err)
	}
	defer rows.Close()

	var targets []channelTarget
	for rows.Next() {
		var t channelTarget
		var configBytes []byte
		var delaySeconds int
		if err := rows.Scan(&t.channel.ID, &t.channel.Name, &t.channel.Type, &configBytes, &t.channel.IsActive, &delaySeconds); err != nil {
			return nil, fmt.Errorf("scan channel target: %w", err)
		}
		t.channel.Config = json.RawMessage(configBytes)
		t.delay = time.Duration(delaySeconds) * time.Second
		targets = append(targets, t)
	}
	return targets, rows.Err()
}

// dispatchOpenAlerts fires due escalation tiers and reminders for open alerts.
// Group members are suppressed: the group's own alert speaks for them.
func (a *Alerter) dispatchOpenAlerts(ctx context.Context) error {
	rows, err := a.db.QueryContext(ctx, `
		SELECT al.id, al.tenant_id, al.monitor_id, m.name, al.triggered_at, al.failure_count, al.last_error,
			al.root_cause_monitor_id, al.root_cause_down_since, rcm.name,
			te.alert_reminder_seconds
		FROM alerts al
		JOIN monitors m ON m.id = al.monitor_id
		JOIN tenants te ON te.id = al.tenant_id
		LEFT JOIN monitors rcm ON rcm.id = al.root_cause_monitor_id
		WHERE al.status IN ('active', 'acknowledged')
		  AND m.current_state = 'down' AND m.deleted_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM monitor_groups mg WHERE mg.monitor_id = al.monitor_id)
	`)
	if err != nil {
		return fmt.Errorf("query open alerts: %w", err)
	}
	defer rows.Close()

	type openAlert struct {
		record          alertRecord
		monitorName     string
		reminderSeconds int
	}
	var open []openAlert
	for rows.Next() {
		var oa openAlert
		var lastError sql.NullString
		var rcID uuid.NullUUID
		var rcDownSince sql.NullTime
		var rcName sql.NullString
		if err := rows.Scan(&oa.record.ID, &oa.record.TenantID, &oa.record.MonitorID, &oa.monitorName,
			&oa.record.TriggeredAt, &oa.record.FailureCount, &lastError,
			&rcID, &rcDownSince, &rcName, &oa.reminderSeconds); err != nil {
			return fmt.Errorf("scan open alert: %w", err)
		}
		if lastError.Valid {
			oa.record.LastError = &lastError.String
		}
		setRootCause(&oa.record, rcID, rcName, rcDownSince)
		open = append(open, oa)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	now := time.Now()
	for _, oa := range open {
		targets, err := a.resolveChannelTargets(ctx, oa.record.TenantID, oa.record.MonitorID)
		if err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{"alert_id": oa.record.ID}).Error("Failed to resolve channels")
			continue
		}
		states, err := a.loadNotificationStates(ctx, []uuid.UUID{oa.record.ID})
		if err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{"alert_id": oa.record.ID}).Error("Failed to load notification states")
			continue
		}
		binding := policyBinding{MonitorID: oa.record.MonitorID, TenantID: oa.record.TenantID, MonitorName: oa.monitorName}
		alertAge := now.Sub(oa.record.TriggeredAt)
		reminderInterval := time.Duration(oa.reminderSeconds) * time.Second

		for _, target := range targets {
			if !target.channel.IsActive {
				continue
			}
			state := getNotificationState(states, oa.record.ID, target.channel.ID)
			switch {
			case state == nil && alertAge >= target.delay:
				a.fireChannel(ctx, "created", binding, &oa.record, target.channel, now)
			case state != nil && state.LastEventType != "resolved" && reminderInterval > 0 &&
				now.Sub(state.LastSentAt) >= reminderInterval:
				a.fireChannel(ctx, "reminder", binding, &oa.record, target.channel, now)
			}
		}
	}
	return nil
}

// fireChannel sends one event and records the notification state.
func (a *Alerter) fireChannel(ctx context.Context, eventType string, binding policyBinding, alert *alertRecord, channel alertChannel, now time.Time) {
	if err := a.sendFunc(ctx, channel, eventType, binding, alert, nil, now); err != nil {
		a.logger.WithError(err).WithFields(map[string]interface{}{
			"alert_id": alert.ID, "channel_id": channel.ID, "event_type": eventType,
		}).Warn("Failed to send alert notification")
		return
	}
	if err := a.upsertNotificationState(ctx, alert.ID, channel.ID, eventType, now); err != nil {
		a.logger.WithError(err).WithFields(map[string]interface{}{
			"alert_id": alert.ID, "channel_id": channel.ID,
		}).Warn("Failed to update notification state")
	}
}

// notifyFiredChannels sends an event (e.g. resolved) to every channel that
// already fired for this alert.
func (a *Alerter) notifyFiredChannels(ctx context.Context, eventType string, binding policyBinding, alert *alertRecord) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT ac.id, ac.name, ac.type, ac.config, ac.is_active, ans.last_event_type
		FROM alert_notification_states ans
		JOIN alert_channels ac ON ac.id = ans.channel_id
		WHERE ans.alert_id = $1
	`, alert.ID)
	if err != nil {
		a.logger.WithError(err).Warn("Failed to load fired channels")
		return
	}
	defer rows.Close()
	now := time.Now()
	type firedChannel struct {
		channel       alertChannel
		lastEventType string
	}
	var fired []firedChannel
	for rows.Next() {
		var fc firedChannel
		var configBytes []byte
		if err := rows.Scan(&fc.channel.ID, &fc.channel.Name, &fc.channel.Type, &configBytes, &fc.channel.IsActive, &fc.lastEventType); err != nil {
			a.logger.WithError(err).Warn("Failed to scan fired channel")
			continue
		}
		fc.channel.Config = json.RawMessage(configBytes)
		fired = append(fired, fc)
	}
	for _, fc := range fired {
		if fc.lastEventType == "resolved" || !fc.channel.IsActive {
			continue
		}
		a.fireChannel(ctx, eventType, binding, alert, fc.channel, now)
	}
}
