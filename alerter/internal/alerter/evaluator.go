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
	ID                   uuid.UUID
	TenantID             uuid.UUID
	MonitorID            uuid.UUID
	PolicyID             uuid.UUID
	Status               string
	TriggeredAt          time.Time
	FailureCount         int
	LastError            *string
	RootCauseMonitorID   *uuid.UUID
	RootCauseMonitorName *string
	RootCauseDownSince   *time.Time
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

type groupMember struct {
	ID   uuid.UUID
	Name string
}

type checkSummary struct {
	Status    string
	Error     *string
	CreatedAt time.Time
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
	monitorTypeGroup        = "group"
	alertsRecoveredMessage  = "All linked alerts recovered"
	incidentSystemEntryType = "system"
)

var failureStatuses = map[string]bool{
	"failure": true,
	"error":   true,
}

func (a *Alerter) evaluateAlerts(ctx context.Context) error {
	bindings, err := a.loadPolicyBindings(ctx)
	if err != nil {
		return err
	}
	if len(bindings) == 0 {
		return nil
	}

	policyIDs := uniquePolicyIDs(bindings)
	policyChannels, err := a.loadPolicyChannels(ctx, policyIDs)
	if err != nil {
		return err
	}

	activeAlerts, activeAlertIDs, err := a.loadActiveAlerts(ctx, policyIDs)
	if err != nil {
		return err
	}

	notificationStates, err := a.loadNotificationStates(ctx, activeAlertIDs)
	if err != nil {
		return err
	}

	groupMembers, groupTenants, err := a.loadGroupMembers(ctx, bindings)
	if err != nil {
		return err
	}

	groupLatestResults, err := a.loadGroupLatestResults(ctx, groupMembers, groupTenants)
	if err != nil {
		return err
	}

	suppressedMonitors := map[uuid.UUID]bool{}
	for _, members := range groupMembers {
		for _, member := range members {
			suppressedMonitors[member.ID] = true
		}
	}

	reminderInterval := time.Duration(a.config.AlertReminderIntervalSeconds) * time.Second
	if reminderInterval <= 0 {
		reminderInterval = time.Hour
	}

	now := time.Now()

	for _, binding := range bindings {
		key := alertKey(binding.MonitorID, binding.PolicyID)
		active := activeAlerts[key]

		var (
			failureCount int
			lastError    *string
			groupInfo    *groupDetail
			recovered    bool
		)

		if binding.MonitorType == monitorTypeGroup {
			members := groupMembers[binding.MonitorID]
			results := groupLatestResults[binding.MonitorID]
			failureCount, lastError, groupInfo = a.evaluateGroupFailures(now, binding, members, results)
			recovered = groupRecovered(results)
		} else {
			failureCount, lastError, recovered, err = a.getMonitorFailureSummary(ctx, binding, now)
			if err != nil {
				a.logger.WithError(err).WithFields(map[string]interface{}{
					"monitor_id": binding.MonitorID,
					"policy_id":  binding.PolicyID,
				}).Error("Failed to evaluate monitor failures")
				continue
			}
		}

		shouldBeActive := failureCount >= binding.FailureThreshold
		if shouldBeActive {
			if active == nil {
				newAlert, err := a.createAlert(ctx, binding, failureCount, lastError, now)
				if err != nil {
					a.logger.WithError(err).WithFields(map[string]interface{}{
						"monitor_id": binding.MonitorID,
						"policy_id":  binding.PolicyID,
					}).Error("Failed to create alert")
					continue
				}
				active = newAlert
				activeAlerts[key] = newAlert

				a.publishAlertEvent(ctx, "created", binding, newAlert, nil)
				a.dispatchNotifications(ctx, "created", binding, newAlert, policyChannels[binding.PolicyID], notificationStates, groupInfo, suppressedMonitors[binding.MonitorID], now, reminderInterval)
			} else {
				if err := a.updateAlertMetadata(ctx, active.ID, failureCount, lastError); err != nil {
					a.logger.WithError(err).WithFields(map[string]interface{}{
						"alert_id":   active.ID,
						"monitor_id": binding.MonitorID,
						"policy_id":  binding.PolicyID,
					}).Warn("Failed to update alert metadata")
				}
				active.FailureCount = failureCount
				active.LastError = lastError
				a.dispatchNotifications(ctx, "reminder", binding, active, policyChannels[binding.PolicyID], notificationStates, groupInfo, suppressedMonitors[binding.MonitorID], now, reminderInterval)
			}
		} else if active != nil {
			if !recovered {
				// The failure window slid past the last failure without an
				// observed success — when checks run less often than the
				// window, resolving here flaps the alert created/resolved on
				// every cycle while the monitor is still down.
				continue
			}
			resolvedAt, err := a.resolveAlert(ctx, active.ID, now)
			if err != nil {
				a.logger.WithError(err).WithFields(map[string]interface{}{
					"alert_id":   active.ID,
					"monitor_id": binding.MonitorID,
					"policy_id":  binding.PolicyID,
				}).Error("Failed to resolve alert")
				continue
			}

			a.publishAlertEvent(ctx, "resolved", binding, active, &resolvedAt)
			a.dispatchNotifications(ctx, "resolved", binding, active, policyChannels[binding.PolicyID], notificationStates, nil, suppressedMonitors[binding.MonitorID], now, reminderInterval)
			delete(activeAlerts, key)
		}
	}

	return nil
}

func (a *Alerter) loadPolicyBindings(ctx context.Context) ([]policyBinding, error) {
	query := `
		SELECT m.id, m.tenant_id, m.name, m.type,
			ap.id, ap.name, ap.failure_threshold, ap.failure_window_seconds, ap.create_incident_on_fire,
			ap.email_subject_template, ap.email_body_template
		FROM monitors m
		JOIN alert_policies ap ON m.alert_policy_id = ap.id AND m.tenant_id = ap.tenant_id
		WHERE m.alert_policy_id IS NOT NULL AND m.enabled = true AND m.deleted_at IS NULL
		UNION
		SELECT m.id, m.tenant_id, m.name, m.type,
			ap.id, ap.name, ap.failure_threshold, ap.failure_window_seconds, ap.create_incident_on_fire,
			ap.email_subject_template, ap.email_body_template
		FROM monitor_alert_policies map
		JOIN monitors m ON map.monitor_id = m.id
		JOIN alert_policies ap ON map.alert_policy_id = ap.id AND m.tenant_id = ap.tenant_id
		WHERE m.enabled = true AND m.deleted_at IS NULL
	`

	rows, err := a.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to load policy bindings: %w", err)
	}
	defer rows.Close()

	var bindings []policyBinding
	for rows.Next() {
		var binding policyBinding
		if err := rows.Scan(
			&binding.MonitorID,
			&binding.TenantID,
			&binding.MonitorName,
			&binding.MonitorType,
			&binding.PolicyID,
			&binding.PolicyName,
			&binding.FailureThreshold,
			&binding.FailureWindowSeconds,
			&binding.CreateIncidentOnFire,
			&binding.EmailSubjectTemplate,
			&binding.EmailBodyTemplate,
		); err != nil {
			return nil, fmt.Errorf("failed to scan policy binding: %w", err)
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating policy bindings: %w", err)
	}
	return bindings, nil
}

func uniquePolicyIDs(bindings []policyBinding) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(bindings))
	var ids []uuid.UUID
	for _, binding := range bindings {
		if _, ok := seen[binding.PolicyID]; ok {
			continue
		}
		seen[binding.PolicyID] = struct{}{}
		ids = append(ids, binding.PolicyID)
	}
	return ids
}

func (a *Alerter) loadPolicyChannels(ctx context.Context, policyIDs []uuid.UUID) (map[uuid.UUID][]alertChannel, error) {
	result := make(map[uuid.UUID][]alertChannel)
	if len(policyIDs) == 0 {
		return result, nil
	}

	query := `
		SELECT apc.alert_policy_id, ac.id, ac.name, ac.type, ac.config, ac.is_active
		FROM alert_policy_channels apc
		JOIN alert_channels ac ON apc.channel_id = ac.id
		WHERE apc.alert_policy_id = ANY($1)
	`

	rows, err := a.db.QueryContext(ctx, query, pq.Array(policyIDs))
	if err != nil {
		return nil, fmt.Errorf("failed to load alert policy channels: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var policyID uuid.UUID
		var channel alertChannel
		var configBytes []byte
		if err := rows.Scan(&policyID, &channel.ID, &channel.Name, &channel.Type, &configBytes, &channel.IsActive); err != nil {
			return nil, fmt.Errorf("failed to scan alert channel: %w", err)
		}
		channel.Config = json.RawMessage(configBytes)
		result[policyID] = append(result[policyID], channel)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating alert channels: %w", err)
	}
	return result, nil
}

func (a *Alerter) loadActiveAlerts(ctx context.Context, policyIDs []uuid.UUID) (map[string]*alertRecord, []uuid.UUID, error) {
	if len(policyIDs) == 0 {
		return map[string]*alertRecord{}, nil, nil
	}

	query := `
		SELECT id, tenant_id, monitor_id, alert_policy_id, status, triggered_at, failure_count, last_error
		FROM alerts
		WHERE status IN ('active', 'acknowledged') AND alert_policy_id = ANY($1)
	`

	rows, err := a.db.QueryContext(ctx, query, pq.Array(policyIDs))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load active alerts: %w", err)
	}
	defer rows.Close()

	alerts := make(map[string]*alertRecord)
	var alertIDs []uuid.UUID
	for rows.Next() {
		var record alertRecord
		var lastError sql.NullString
		if err := rows.Scan(
			&record.ID,
			&record.TenantID,
			&record.MonitorID,
			&record.PolicyID,
			&record.Status,
			&record.TriggeredAt,
			&record.FailureCount,
			&lastError,
		); err != nil {
			return nil, nil, fmt.Errorf("failed to scan alert: %w", err)
		}
		if lastError.Valid {
			record.LastError = &lastError.String
		}
		alerts[alertKey(record.MonitorID, record.PolicyID)] = &record
		alertIDs = append(alertIDs, record.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("error iterating alerts: %w", err)
	}
	return alerts, alertIDs, nil
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

func (a *Alerter) loadGroupMembers(ctx context.Context, bindings []policyBinding) (map[uuid.UUID][]groupMember, map[uuid.UUID]uuid.UUID, error) {
	groupIDs := make([]uuid.UUID, 0)
	groupTenants := make(map[uuid.UUID]uuid.UUID)
	seen := make(map[uuid.UUID]struct{})
	for _, binding := range bindings {
		if binding.MonitorType != monitorTypeGroup {
			continue
		}
		if _, ok := seen[binding.MonitorID]; ok {
			continue
		}
		seen[binding.MonitorID] = struct{}{}
		groupIDs = append(groupIDs, binding.MonitorID)
		groupTenants[binding.MonitorID] = binding.TenantID
	}

	if len(groupIDs) == 0 {
		return map[uuid.UUID][]groupMember{}, groupTenants, nil
	}

	query := `
		WITH RECURSIVE member_tree AS (
			SELECT root.id AS root_group_id, root.tenant_id, mg.monitor_id
			FROM monitors root
			JOIN monitor_groups mg ON mg.group_id = root.id
			JOIN monitors child ON child.id = mg.monitor_id AND child.tenant_id = root.tenant_id
			WHERE root.id = ANY($1)
			  AND root.type = 'group'
			  AND root.deleted_at IS NULL
			  AND child.deleted_at IS NULL
			UNION
			SELECT mt.root_group_id, mt.tenant_id, mg.monitor_id
			FROM member_tree mt
			JOIN monitors parent ON parent.id = mt.monitor_id AND parent.tenant_id = mt.tenant_id
			JOIN monitor_groups mg ON mg.group_id = parent.id
			JOIN monitors child ON child.id = mg.monitor_id AND child.tenant_id = mt.tenant_id
			WHERE parent.type = 'group'
			  AND parent.deleted_at IS NULL
			  AND child.deleted_at IS NULL
		)
		SELECT mt.root_group_id, m.id, m.name
		FROM member_tree mt
		JOIN monitors m ON m.id = mt.monitor_id AND m.tenant_id = mt.tenant_id
		WHERE m.type <> 'group'
		  AND m.deleted_at IS NULL
		ORDER BY mt.root_group_id, m.name
	`

	rows, err := a.db.QueryContext(ctx, query, pq.Array(groupIDs))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load group members: %w", err)
	}
	defer rows.Close()

	groupMembers := make(map[uuid.UUID][]groupMember)
	for rows.Next() {
		var groupID uuid.UUID
		var member groupMember
		if err := rows.Scan(&groupID, &member.ID, &member.Name); err != nil {
			return nil, nil, fmt.Errorf("failed to scan group member: %w", err)
		}
		groupMembers[groupID] = append(groupMembers[groupID], member)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("error iterating group members: %w", err)
	}

	return groupMembers, groupTenants, nil
}

func (a *Alerter) loadGroupLatestResults(ctx context.Context, groupMembers map[uuid.UUID][]groupMember, groupTenants map[uuid.UUID]uuid.UUID) (map[uuid.UUID]map[uuid.UUID]checkSummary, error) {
	result := make(map[uuid.UUID]map[uuid.UUID]checkSummary)
	for groupID, members := range groupMembers {
		if len(members) == 0 {
			result[groupID] = map[uuid.UUID]checkSummary{}
			continue
		}

		tenantID, ok := groupTenants[groupID]
		if !ok {
			continue
		}

		memberIDs := make([]uuid.UUID, 0, len(members))
		for _, member := range members {
			memberIDs = append(memberIDs, member.ID)
		}

		query := `
			SELECT DISTINCT ON (monitor_id) monitor_id, status, error_message, created_at
			FROM check_results
			WHERE tenant_id = $1 AND monitor_id = ANY($2)
			ORDER BY monitor_id, created_at DESC
		`

		rows, err := a.db.QueryContext(ctx, query, tenantID, pq.Array(memberIDs))
		if err != nil {
			return nil, fmt.Errorf("failed to load group check results: %w", err)
		}

		summaries := make(map[uuid.UUID]checkSummary)
		for rows.Next() {
			var monitorID uuid.UUID
			var summary checkSummary
			var errorMessage sql.NullString
			if err := rows.Scan(&monitorID, &summary.Status, &errorMessage, &summary.CreatedAt); err != nil {
				rows.Close()
				return nil, fmt.Errorf("failed to scan group check result: %w", err)
			}
			if errorMessage.Valid {
				summary.Error = &errorMessage.String
			}
			summaries[monitorID] = summary
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("error iterating group check results: %w", err)
		}
		result[groupID] = summaries
	}
	return result, nil
}

func (a *Alerter) evaluateGroupFailures(now time.Time, binding policyBinding, members []groupMember, results map[uuid.UUID]checkSummary) (int, *string, *groupDetail) {
	if len(members) == 0 {
		return 0, nil, nil
	}

	windowCutoff := now.Add(-time.Duration(binding.FailureWindowSeconds) * time.Second)
	detailCutoff := now.Add(-time.Duration(a.config.AlertGroupWindowSeconds) * time.Second)

	var failures []groupFailure
	var lastFailure *groupFailure
	failureCount := 0

	for _, member := range members {
		summary, ok := results[member.ID]
		if !ok {
			continue
		}
		if !failureStatuses[summary.Status] {
			continue
		}
		if summary.CreatedAt.Before(windowCutoff) {
			continue
		}
		failureCount++

		if lastFailure == nil || summary.CreatedAt.After(lastFailure.CreatedAt) {
			lastFailure = &groupFailure{
				Name:      member.Name,
				Status:    summary.Status,
				Error:     summary.Error,
				CreatedAt: summary.CreatedAt,
			}
		}

		if summary.CreatedAt.Before(detailCutoff) {
			continue
		}

		failures = append(failures, groupFailure{
			Name:      member.Name,
			Status:    summary.Status,
			Error:     summary.Error,
			CreatedAt: summary.CreatedAt,
		})
	}

	var lastError *string
	if lastFailure != nil {
		lastError = lastFailure.Error
		if lastError == nil {
			msg := fmt.Sprintf("%s reported %s", lastFailure.Name, lastFailure.Status)
			lastError = &msg
		}
	}

	if failureCount == 0 {
		return 0, nil, nil
	}

	sort.Slice(failures, func(i, j int) bool {
		return failures[i].CreatedAt.After(failures[j].CreatedAt)
	})

	maxChildren := a.config.AlertGroupMaxChildren
	if maxChildren <= 0 {
		maxChildren = 5
	}

	if len(failures) > maxChildren {
		failures = failures[:maxChildren]
	}
	extraCount := failureCount - len(failures)

	return failureCount, lastError, &groupDetail{
		Failures:   failures,
		ExtraCount: extraCount,
	}
}

// getMonitorFailureSummary counts failures within the policy window and
// reports whether the monitor has recovered — i.e. its latest check result
// (regardless of window) is a success. Failures aging out of the window do
// not count as recovery on their own.
func (a *Alerter) getMonitorFailureSummary(ctx context.Context, binding policyBinding, now time.Time) (int, *string, bool, error) {
	cutoff := now.Add(-time.Duration(binding.FailureWindowSeconds) * time.Second)
	query := `
		SELECT COUNT(*) FILTER (WHERE status IN ('failure', 'error')) AS failure_count,
			(
				SELECT error_message
				FROM check_results
				WHERE monitor_id = $1 AND tenant_id = $2
					AND status IN ('failure', 'error')
					AND created_at >= $3
				ORDER BY created_at DESC
				LIMIT 1
			) AS last_error,
			(
				SELECT status
				FROM check_results
				WHERE monitor_id = $1 AND tenant_id = $2
				ORDER BY created_at DESC
				LIMIT 1
			) AS latest_status
		FROM check_results
		WHERE monitor_id = $1 AND tenant_id = $2 AND created_at >= $3
	`

	var failureCount int
	var lastError sql.NullString
	var latestStatus sql.NullString
	if err := a.db.QueryRowContext(ctx, query, binding.MonitorID, binding.TenantID, cutoff).Scan(&failureCount, &lastError, &latestStatus); err != nil {
		return 0, nil, false, fmt.Errorf("failed to query monitor failures: %w", err)
	}

	recovered := latestStatus.Valid && !failureStatuses[latestStatus.String]
	if lastError.Valid {
		return failureCount, &lastError.String, recovered, nil
	}
	return failureCount, nil, recovered, nil
}

// groupRecovered reports whether no group member's latest known check result
// is a failure. A member whose latest result is failing keeps the group alert
// active even after that result ages out of the failure window.
func groupRecovered(results map[uuid.UUID]checkSummary) bool {
	for _, summary := range results {
		if failureStatuses[summary.Status] {
			return false
		}
	}
	return true
}

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
		metadata := map[string]interface{}{
			"alert_id":   alert.ID.String(),
			"monitor_id": binding.MonitorID.String(),
		}
		if binding.PolicyID != uuid.Nil {
			metadata["alert_policy_id"] = binding.PolicyID.String()
		}
		if err := a.insertIncidentTimelineEntryTx(ctx, tx, binding.TenantID, incidentID, "system", message, metadata); err != nil {
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
	// policyParam is nil (SQL NULL) when there is no policy (lifecycle alerts),
	// or the policy UUID when called from the policy-based createAlert path.
	// IS NOT DISTINCT FROM handles both NULL and non-NULL equality correctly.
	var policyParam interface{}
	if binding.PolicyID != uuid.Nil {
		policyParam = binding.PolicyID
	}

	var incidentID uuid.UUID
	err := tx.QueryRowContext(ctx, `
		SELECT id
		FROM incidents
		WHERE tenant_id = $1
			AND is_auto_created = TRUE
			AND auto_monitor_id = $2
			AND auto_alert_policy_id IS NOT DISTINCT FROM $3
			AND state <> 'resolved'
		ORDER BY created_at DESC, id DESC
		LIMIT 1
		FOR UPDATE
	`, binding.TenantID, binding.MonitorID, policyParam).Scan(&incidentID)
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
	`, incidentID, binding.TenantID, title, summary, binding.MonitorID, policyParam); err != nil {
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

func (a *Alerter) updateAlertMetadata(ctx context.Context, alertID uuid.UUID, failureCount int, lastError *string) error {
	query := `
		UPDATE alerts
		SET failure_count = $1, last_error = $2, updated_at = NOW()
		WHERE id = $3
	`

	_, err := a.db.ExecContext(ctx, query, failureCount, lastError, alertID)
	if err != nil {
		return fmt.Errorf("failed to update alert metadata: %w", err)
	}
	return nil
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

func (a *Alerter) dispatchNotifications(
	ctx context.Context,
	eventType string,
	binding policyBinding,
	alert *alertRecord,
	channels []alertChannel,
	states map[uuid.UUID]map[uuid.UUID]*notificationState,
	groupInfo *groupDetail,
	suppressed bool,
	now time.Time,
	reminderInterval time.Duration,
) {
	if suppressed || len(channels) == 0 {
		return
	}

	for _, channel := range channels {
		if !channel.IsActive {
			continue
		}

		state := getNotificationState(states, alert.ID, channel.ID)
		if !shouldSendNotification(eventType, state, now, reminderInterval) {
			continue
		}

		if err := a.sendChannelNotification(ctx, channel, eventType, binding, alert, groupInfo, now); err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{
				"alert_id":   alert.ID,
				"channel_id": channel.ID,
				"event_type": eventType,
			}).Warn("Failed to send alert notification")
			continue
		}

		if err := a.upsertNotificationState(ctx, alert.ID, channel.ID, eventType, now); err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{
				"alert_id":   alert.ID,
				"channel_id": channel.ID,
			}).Warn("Failed to update notification state")
		}

		if _, ok := states[alert.ID]; !ok {
			states[alert.ID] = make(map[uuid.UUID]*notificationState)
		}
		states[alert.ID][channel.ID] = &notificationState{
			LastSentAt:    now,
			LastEventType: eventType,
		}
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

func alertKey(monitorID, policyID uuid.UUID) string {
	return monitorID.String() + "|" + policyID.String()
}

func buildAlertEvent(eventType string, binding policyBinding, alert *alertRecord, resolvedAt *time.Time, timestamp time.Time) notifications.AlertEvent {
	status := "active"
	if eventType == "resolved" {
		status = "resolved"
	}

	var rootCauseID *string
	if alert.RootCauseMonitorID != nil {
		id := alert.RootCauseMonitorID.String()
		rootCauseID = &id
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
			RootCauseMonitorID:   rootCauseID,
			RootCauseMonitorName: alert.RootCauseMonitorName,
			RootCauseDownSince:   alert.RootCauseDownSince,
		},
	}
}
