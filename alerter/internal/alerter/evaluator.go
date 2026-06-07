package alerter

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/secrets"
)

type policyBinding struct {
	MonitorID            uuid.UUID
	TenantID             uuid.UUID
	MonitorName          string
	MonitorType          string
	PolicyID             uuid.UUID
	PolicyName           string
	FailureThreshold     int
	FailureWindowSeconds int
	CreateIncidentOnFire bool
	EmailSubjectTemplate *string
	EmailBodyTemplate    *string
}

type alertRecord struct {
	ID           uuid.UUID
	TenantID     uuid.UUID
	MonitorID    uuid.UUID
	PolicyID     uuid.UUID
	Status       string
	TriggeredAt  time.Time
	FailureCount int
	LastError    *string
}

type alertChannel struct {
	ID       uuid.UUID
	Name     string
	Type     string
	Config   json.RawMessage
	IsActive bool
}

type notificationState struct {
	LastSentAt    time.Time
	LastEventType string
}

type groupDetail struct {
	Failures   []groupFailure
	ExtraCount int
}

type groupFailure struct {
	Name      string
	Status    string
	Error     *string
	CreatedAt time.Time
}

const (
	alertsRecoveredMessage  = "All linked alerts recovered"
	incidentSystemEntryType = "system"
)

func (a *Alerter) createAlert(ctx context.Context, binding policyBinding, failureCount int, lastError *string, now time.Time) (*alertRecord, error) {
	alertID := uuid.New()
	query := `
		INSERT INTO alerts (
			id, tenant_id, monitor_id, alert_policy_id, status,
			triggered_at, failure_count, last_error, created_at, updated_at
		) VALUES ($1, $2, $3, $4, 'active', $5, $6, $7, $5, $5)
		RETURNING id, tenant_id, monitor_id, alert_policy_id, status, triggered_at, failure_count, last_error
	`

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin alert transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var record alertRecord
	var lastErrorValue sql.NullString
	err = tx.QueryRowContext(ctx, query,
		alertID,
		binding.TenantID,
		binding.MonitorID,
		binding.PolicyID,
		now,
		failureCount,
		lastError,
	).Scan(
		&record.ID,
		&record.TenantID,
		&record.MonitorID,
		&record.PolicyID,
		&record.Status,
		&record.TriggeredAt,
		&record.FailureCount,
		&lastErrorValue,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert alert: %w", err)
	}
	if lastErrorValue.Valid {
		record.LastError = &lastErrorValue.String
	}

	if err := a.ensureAutoIncidentForAlertTx(ctx, tx, binding, &record); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit alert transaction: %w", err)
	}

	return &record, nil
}

func (a *Alerter) resolveAlert(ctx context.Context, alertID uuid.UUID, now time.Time) (time.Time, error) {
	query := `
		UPDATE alerts
		SET status = 'resolved', resolved_at = $1, updated_at = $1
		WHERE id = $2 AND status IN ('active', 'acknowledged')
		RETURNING resolved_at
	`

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return time.Time{}, fmt.Errorf("begin alert transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var resolvedAt time.Time
	if err := tx.QueryRowContext(ctx, query, now, alertID).Scan(&resolvedAt); err != nil {
		return time.Time{}, fmt.Errorf("failed to resolve alert: %w", err)
	}

	if err := a.recordAlertRecoveryIfNeededTx(ctx, tx, alertID); err != nil {
		return time.Time{}, err
	}
	if err := tx.Commit(); err != nil {
		return time.Time{}, fmt.Errorf("commit alert transaction: %w", err)
	}

	return resolvedAt, nil
}

func (a *Alerter) ensureAutoIncidentForAlertTx(ctx context.Context, tx *sql.Tx, binding policyBinding, alert *alertRecord) error {
	if !binding.CreateIncidentOnFire {
		return nil
	}

	if err := a.lockAutoIncidentKeyTx(ctx, tx, binding.TenantID, binding.MonitorID, binding.PolicyID); err != nil {
		return err
	}

	incidentID, created, err := a.findOrCreateAutoIncidentTx(ctx, tx, binding)
	if err != nil {
		return err
	}

	alertAttached, err := a.ensureIncidentAlertLinkTx(ctx, tx, incidentID, alert.ID)
	if err != nil {
		return err
	}
	monitorAttached, err := a.ensureIncidentMonitorLinkTx(ctx, tx, incidentID, binding.MonitorID)
	if err != nil {
		return err
	}

	if created || alertAttached || monitorAttached {
		message := "Alert auto-linked to existing incident"
		if created {
			message = "Incident auto-created from firing alert"
		}
		if err := a.insertIncidentTimelineEntryTx(ctx, tx, binding.TenantID, incidentID, "system", message, map[string]interface{}{
			"alert_id":        alert.ID.String(),
			"monitor_id":      binding.MonitorID.String(),
			"alert_policy_id": binding.PolicyID.String(),
		}); err != nil {
			return err
		}
	}

	return nil
}

func (a *Alerter) recordAlertRecoveryIfNeededTx(ctx context.Context, tx *sql.Tx, alertID uuid.UUID) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT i.id, i.tenant_id
		FROM incidents i
		JOIN incident_alerts ia ON ia.incident_id = i.id
		JOIN alerts a ON a.id = ia.alert_id AND a.tenant_id = i.tenant_id
		WHERE ia.alert_id = $1
	`, alertID)
	if err != nil {
		return fmt.Errorf("load incidents for alert recovery: %w", err)
	}
	defer rows.Close()

	type incidentTarget struct {
		incidentID uuid.UUID
		tenantID   uuid.UUID
	}

	targets := make([]incidentTarget, 0)
	for rows.Next() {
		var target incidentTarget
		if err := rows.Scan(&target.incidentID, &target.tenantID); err != nil {
			return fmt.Errorf("scan incident recovery target: %w", err)
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate incident recovery targets: %w", err)
	}
	sort.Slice(targets, func(i, j int) bool {
		return targets[i].incidentID.String() < targets[j].incidentID.String()
	})

	for _, target := range targets {
		if err := a.lockIncidentRecoveryKeyTx(ctx, tx, target.incidentID); err != nil {
			return err
		}
		allResolved, resolvedAfter, err := a.loadIncidentAlertRecoveryStateTx(ctx, tx, target.incidentID)
		if err != nil {
			return err
		}
		if !allResolved {
			continue
		}
		resolvedAfterMarker := recoveryResolutionMarker(resolvedAfter)
		alreadyRecorded, err := a.recoveryTimelineExistsTx(ctx, tx, target.incidentID, incidentSystemEntryType, alertsRecoveredMessage, resolvedAfterMarker)
		if err != nil {
			return err
		}
		if alreadyRecorded {
			continue
		}
		if err := a.insertIncidentTimelineEntryTx(ctx, tx, target.tenantID, target.incidentID, incidentSystemEntryType, alertsRecoveredMessage, map[string]interface{}{
			"alert_id":       alertID.String(),
			"resolved_after": resolvedAfterMarker,
		}); err != nil {
			return err
		}
	}

	return nil
}

func (a *Alerter) findOrCreateAutoIncidentTx(ctx context.Context, tx *sql.Tx, binding policyBinding) (uuid.UUID, bool, error) {
	var incidentID uuid.UUID
	err := tx.QueryRowContext(ctx, `
		SELECT id
		FROM incidents
		WHERE tenant_id = $1
			AND is_auto_created = TRUE
			AND auto_monitor_id = $2
			AND auto_alert_policy_id = $3
			AND state <> 'resolved'
		ORDER BY created_at DESC, id DESC
		LIMIT 1
		FOR UPDATE
	`, binding.TenantID, binding.MonitorID, binding.PolicyID).Scan(&incidentID)
	if err == nil {
		return incidentID, false, nil
	}
	if err != sql.ErrNoRows {
		return uuid.Nil, false, fmt.Errorf("load auto incident: %w", err)
	}

	incidentID = uuid.New()
	title, summary := buildAlerterAutoIncidentNarrative(binding.MonitorName, binding.PolicyName)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO incidents (
			id, tenant_id, title, summary, state, is_auto_created, auto_monitor_id, auto_alert_policy_id, created_at, updated_at
		) VALUES ($1, $2, $3, $4, 'investigating', TRUE, $5, $6, NOW(), NOW())
	`, incidentID, binding.TenantID, title, summary, binding.MonitorID, binding.PolicyID); err != nil {
		return uuid.Nil, false, fmt.Errorf("create auto incident: %w", err)
	}

	return incidentID, true, nil
}

func (a *Alerter) lockAutoIncidentKeyTx(ctx context.Context, tx *sql.Tx, tenantID, monitorID, policyID uuid.UUID) error {
	lockKey := fmt.Sprintf("%s:%s:%s", tenantID, monitorID, policyID)
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return fmt.Errorf("lock auto incident key: %w", err)
	}
	return nil
}

func (a *Alerter) lockIncidentRecoveryKeyTx(ctx context.Context, tx *sql.Tx, incidentID uuid.UUID) error {
	lockKey := fmt.Sprintf("incident-recovery:%s", incidentID)
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return fmt.Errorf("lock incident recovery key: %w", err)
	}
	return nil
}

func (a *Alerter) ensureIncidentAlertLinkTx(ctx context.Context, tx *sql.Tx, incidentID, alertID uuid.UUID) (bool, error) {
	result, err := tx.ExecContext(ctx, `
		INSERT INTO incident_alerts (incident_id, alert_id, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (incident_id, alert_id) DO NOTHING
	`, incidentID, alertID)
	if err != nil {
		return false, fmt.Errorf("attach incident alert: %w", err)
	}
	return alerterMutationChanged(result), nil
}

func (a *Alerter) ensureIncidentMonitorLinkTx(ctx context.Context, tx *sql.Tx, incidentID, monitorID uuid.UUID) (bool, error) {
	result, err := tx.ExecContext(ctx, `
		INSERT INTO incident_monitors (incident_id, monitor_id, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (incident_id, monitor_id) DO NOTHING
	`, incidentID, monitorID)
	if err != nil {
		return false, fmt.Errorf("attach incident monitor: %w", err)
	}
	return alerterMutationChanged(result), nil
}

func (a *Alerter) loadIncidentAlertRecoveryStateTx(ctx context.Context, tx *sql.Tx, incidentID uuid.UUID) (bool, time.Time, error) {
	var totalCount int
	var unresolvedCount int
	var resolvedAfter sql.NullTime
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*),
			COUNT(*) FILTER (WHERE a.status IN ('active', 'acknowledged')),
			MAX(COALESCE(a.resolved_at, a.updated_at))
		FROM incident_alerts ia
		JOIN alerts a ON a.id = ia.alert_id
		WHERE ia.incident_id = $1
	`, incidentID).Scan(&totalCount, &unresolvedCount, &resolvedAfter); err != nil {
		return false, time.Time{}, fmt.Errorf("load linked alert statuses: %w", err)
	}

	if totalCount == 0 || unresolvedCount != 0 || !resolvedAfter.Valid {
		return false, time.Time{}, nil
	}

	return true, resolvedAfter.Time.UTC(), nil
}

func (a *Alerter) recoveryTimelineExistsTx(ctx context.Context, tx *sql.Tx, incidentID uuid.UUID, entryType, message, resolvedAfter string) (bool, error) {
	var exists bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM incident_timeline_entries
			WHERE incident_id = $1
				AND entry_type = $2
				AND message = $3
				AND metadata->>'resolved_after' = $4
		)
	`, incidentID, entryType, message, resolvedAfter).Scan(&exists); err != nil {
		return false, fmt.Errorf("check incident recovery timeline entry: %w", err)
	}

	return exists, nil
}

func recoveryResolutionMarker(resolvedAfter time.Time) string {
	return resolvedAfter.UTC().Format(time.RFC3339Nano)
}

func (a *Alerter) insertIncidentTimelineEntryTx(ctx context.Context, tx *sql.Tx, tenantID, incidentID uuid.UUID, entryType, message string, metadata map[string]interface{}) error {
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshal incident timeline metadata: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO incident_timeline_entries (
			id, tenant_id, incident_id, entry_type, message, metadata, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, clock_timestamp())
	`, uuid.New(), tenantID, incidentID, entryType, message, metadataJSON); err != nil {
		return fmt.Errorf("insert incident timeline entry: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE incidents
		SET updated_at = NOW()
		WHERE id = $1 AND tenant_id = $2
	`, incidentID, tenantID); err != nil {
		return fmt.Errorf("touch incident updated_at: %w", err)
	}

	return nil
}

func buildAlerterAutoIncidentNarrative(monitorName, policyName string) (string, string) {
	monitorName = strings.TrimSpace(monitorName)
	policyName = strings.TrimSpace(policyName)

	title := "Auto-created incident"
	if monitorName != "" {
		title = fmt.Sprintf("%s incident", monitorName)
	}

	summary := "Automatically created from a firing alert policy."
	if policyName != "" {
		summary = fmt.Sprintf("Automatically created from alert policy %s.", policyName)
	}

	return title, summary
}

func alerterMutationChanged(result sql.Result) bool {
	if result == nil {
		return false
	}

	rowsAffected, err := result.RowsAffected()
	return err == nil && rowsAffected > 0
}

func (a *Alerter) publishAlertEvent(ctx context.Context, eventType string, binding policyBinding, alert *alertRecord, resolvedAt *time.Time) {
	if a.nats == nil {
		return
	}

	event := buildAlertEvent(eventType, binding, alert, resolvedAt, time.Now())

	subject := fmt.Sprintf("%s.%s", a.config.AlertSubject, eventType)
	if err := a.nats.PublishJSON(ctx, subject, event, nil); err != nil {
		a.logger.WithError(err).Warn("Failed to publish alert event")
	}
}

func (a *Alerter) sendChannelNotification(
	ctx context.Context,
	channel alertChannel,
	eventType string,
	binding policyBinding,
	alert *alertRecord,
	groupInfo *groupDetail,
	now time.Time,
) error {
	_ = groupInfo // group context plumbing follows in a later refactor

	p, ok := plugin.DefaultRegistry.Get(channel.Type)
	if !ok {
		return fmt.Errorf("unsupported alert channel type: %s", channel.Type)
	}

	var resolvedAt *time.Time
	if eventType == "resolved" {
		resolvedAt = &now
	}
	event := buildAlertEvent(eventType, binding, alert, resolvedAt, now)

	if a.config.AsyncDispatch && a.nats != nil {
		envelope := notifications.DispatchEnvelope{
			V:           1,
			ChannelID:   channel.ID.String(),
			ChannelType: channel.Type,
			AlertID:     alert.ID.String(),
			EventType:   eventType,
			Event:       event,
		}
		subject := fmt.Sprintf("alerts.dispatch.%s", channel.Type)
		headers := map[string][]string{"x-idempotency-key": {envelope.IdempotencyKey()}}
		return a.nats.PublishJSON(ctx, subject, envelope, headers)
	}

	configMap := map[string]any{}
	if len(channel.Config) > 0 {
		if err := json.Unmarshal(channel.Config, &configMap); err != nil {
			return fmt.Errorf("decode channel config: %w", err)
		}
	}
	configMap, err := secrets.DecryptConfig(a.encryptor, p.Manifest(), configMap)
	if err != nil {
		return fmt.Errorf("decrypt channel config: %w", err)
	}

	return p.Send(ctx, plugin.DispatchRequest{
		Channel: plugin.ChannelRef{
			ID:     channel.ID.String(),
			Name:   channel.Name,
			Config: configMap,
		},
		Event:     event,
		EventType: eventType,
		Attempt:   1,
	})
}

func (a *Alerter) upsertNotificationState(ctx context.Context, alertID, channelID uuid.UUID, eventType string, sentAt time.Time) error {
	query := `
		INSERT INTO alert_notification_states (
			alert_id, channel_id, last_sent_at, last_event_type, created_at, updated_at
		) VALUES ($1, $2, $3, $4, NOW(), NOW())
		ON CONFLICT (alert_id, channel_id)
		DO UPDATE SET last_sent_at = $3, last_event_type = $4, updated_at = NOW()
	`

	_, err := a.db.ExecContext(ctx, query, alertID, channelID, sentAt, eventType)
	if err != nil {
		return fmt.Errorf("failed to upsert notification state: %w", err)
	}
	return nil
}

func (a *Alerter) loadNotificationStates(ctx context.Context, alertIDs []uuid.UUID) (map[uuid.UUID]map[uuid.UUID]*notificationState, error) {
	states := make(map[uuid.UUID]map[uuid.UUID]*notificationState)
	if len(alertIDs) == 0 {
		return states, nil
	}

	query := `
		SELECT alert_id, channel_id, last_sent_at, last_event_type
		FROM alert_notification_states
		WHERE alert_id = ANY($1)
	`

	rows, err := a.db.QueryContext(ctx, query, pq.Array(alertIDs))
	if err != nil {
		return nil, fmt.Errorf("failed to load notification states: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var alertID uuid.UUID
		var channelID uuid.UUID
		var state notificationState
		if err := rows.Scan(&alertID, &channelID, &state.LastSentAt, &state.LastEventType); err != nil {
			return nil, fmt.Errorf("failed to scan notification state: %w", err)
		}
		if _, ok := states[alertID]; !ok {
			states[alertID] = make(map[uuid.UUID]*notificationState)
		}
		states[alertID][channelID] = &state
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating notification states: %w", err)
	}
	return states, nil
}

func shouldSendNotification(eventType string, state *notificationState, now time.Time, reminderInterval time.Duration) bool {
	switch eventType {
	case "created":
		return state == nil
	case "resolved":
		return state == nil || state.LastEventType != "resolved"
	case "reminder":
		if state == nil {
			return false
		}
		if state.LastEventType == "resolved" {
			return false
		}
		return now.Sub(state.LastSentAt) >= reminderInterval
	default:
		return false
	}
}

func getNotificationState(states map[uuid.UUID]map[uuid.UUID]*notificationState, alertID, channelID uuid.UUID) *notificationState {
	if alertStates, ok := states[alertID]; ok {
		return alertStates[channelID]
	}
	return nil
}

func buildAlertEvent(eventType string, binding policyBinding, alert *alertRecord, resolvedAt *time.Time, timestamp time.Time) notifications.AlertEvent {
	status := "active"
	if eventType == "resolved" {
		status = "resolved"
	}

	return notifications.AlertEvent{
		Type:      eventType,
		TenantID:  binding.TenantID.String(),
		Timestamp: timestamp,
		Alert: notifications.AlertDetails{
			ID:                   alert.ID.String(),
			MonitorID:            binding.MonitorID.String(),
			MonitorName:          binding.MonitorName,
			AlertPolicyID:        binding.PolicyID.String(),
			PolicyName:           binding.PolicyName,
			Status:               status,
			TriggeredAt:          alert.TriggeredAt,
			ResolvedAt:           resolvedAt,
			FailureCount:         alert.FailureCount,
			LastError:            alert.LastError,
			EmailSubjectTemplate: binding.EmailSubjectTemplate,
			EmailBodyTemplate:    binding.EmailBodyTemplate,
		},
	}
}
