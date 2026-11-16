package alerts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/apperr"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/pagination"
	"github.com/yassinebenameur/probara/api/internal/services/triageview"
	"github.com/yassinebenameur/probara/shared/alertrouting"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/notifications"
)

// Errors the handlers map to a status with errors.Is. The text reaches API
// clients, so it is part of the API.
var (
	// ErrAlertNotFound: the alert does not exist in the tenant.
	ErrAlertNotFound = apperr.ErrAlertNotFound
	// ErrAlertNotActive: acknowledge found no active alert with that ID.
	ErrAlertNotActive = errors.New("alert not found or not active")
	// ErrAlertAlreadyResolved: resolve found no open alert with that ID.
	ErrAlertAlreadyResolved = errors.New("alert not found or already resolved")
)

type incidentAutomation interface {
	RecordAlertRecoveryIfNeededTx(ctx context.Context, tx *sql.Tx, tenantID, alertID uuid.UUID) error
}

// Service handles alert business logic
type Service struct {
	db        *db.Client
	incidents incidentAutomation
}

// NewService creates a new alert service
func NewService(db *db.Client, incidents incidentAutomation) *Service {
	return &Service{db: db, incidents: incidents}
}

// alertDetailSelect is the shared projection for AlertWithDetails. Monitors
// are LEFT JOINed because mesh_edge alerts have no monitor — their subject is
// a directed location pair (sl → tl). The trailing triage columns and join
// come from triageview (read-only; triage never changes paging). snoozed_until,
// snoozed_at and who snoozed are reported only while the snooze holds
// (alertrouting.SnoozedPredicate, the alerter's own test), so a payload never
// reports a snooze that has ended.
var alertDetailSelect = `
	SELECT a.id, a.tenant_id, a.monitor_id, a.status,
		a.triggered_at, a.acknowledged_at, a.resolved_at, a.failure_count,
		a.last_error, a.kind, a.baseline_latency_ms, a.observed_latency_ms, a.anomaly_score,
		a.metric_name, a.metric_value, a.threshold_value,
		a.created_at, a.updated_at,
		m.name as monitor_name,
		a.root_cause_monitor_id, a.root_cause_down_since, rcm.name as root_cause_monitor_name,
		a.source_location_id, a.target_location_id, sl.name, tl.name,
		CASE WHEN ` + alertDependencySuppressedPredicate + ` THEN '` + alertrouting.SuppressionReasonDependency + `'
			WHEN ` + alertLocationSuppressedPredicate + ` THEN '` + alertrouting.SuppressionReasonLocationOffline + `' END,
		CASE WHEN a.status IN ('active', 'acknowledged') AND a.kind = 'availability'
			THEN json_array_length(` + alertrouting.ImpactedMonitorsSubquery("a.monitor_id") + `) ELSE 0 END,
		a.acknowledged_by_user_id, ackby.username,
		CASE WHEN a.status IN ('active', 'acknowledged') AND ` + alertrouting.SnoozedPredicate("a") + ` THEN a.snoozed_until END,
		CASE WHEN a.status IN ('active', 'acknowledged') AND ` + alertrouting.SnoozedPredicate("a") + ` THEN a.snoozed_at END,
		CASE WHEN a.status IN ('active', 'acknowledged') AND ` + alertrouting.SnoozedPredicate("a") + ` THEN a.snoozed_by_user_id END,
		CASE WHEN a.status IN ('active', 'acknowledged') AND ` + alertrouting.SnoozedPredicate("a") + ` THEN snoozeby.username END,
		a.relapse_count, a.reopened_at,
		` + triageview.Columns + `
	FROM alerts a
	LEFT JOIN monitors m ON a.monitor_id = m.id
	LEFT JOIN tenants te ON te.id = a.tenant_id
	LEFT JOIN monitors rcm ON rcm.id = a.root_cause_monitor_id
	LEFT JOIN locations sl ON sl.id = a.source_location_id
	LEFT JOIN locations tl ON tl.id = a.target_location_id
	LEFT JOIN admin_users ackby ON ackby.id = a.acknowledged_by_user_id
	LEFT JOIN admin_users snoozeby ON snoozeby.id = a.snoozed_by_user_id
	` + triageview.Join + `
`

// alertSuppressedPredicate is TRUE for an open alert whose notifications the
// alerter is currently suppressing. It is built from the very predicates the
// alerter's dispatch queries exclude on, so the API never calls an alert
// suppressed that would in fact page (or vice versa). Resolved alerts are
// never suppressed: there is nothing left to dispatch. Expects aliases a
// (alerts), m (monitors) and te (tenants); m is NULL for mesh alerts, which
// makes the dependency CASE fall through to the tenant default and the
// root-cause test NULL → not suppressed by a dependency.
var alertSuppressedPredicate = `(` + alertDependencySuppressedPredicate + ` OR ` + alertLocationSuppressedPredicate + `)`

// alertDependencySuppressedPredicate: an upstream dependency explains the
// alert (suppression_reason "dependency").
var alertDependencySuppressedPredicate = `(a.status IN ('active', 'acknowledged') AND COALESCE(` +
	alertrouting.SuppressedByDependencyPredicate("a", "m", "te") + `, FALSE))`

// alertLocationSuppressedPredicate: a mesh edge whose location stopped
// reporting (suppression_reason "location_offline").
var alertLocationSuppressedPredicate = `(a.status IN ('active', 'acknowledged') AND ` +
	alertrouting.SuppressedByOfflineLocationPredicate("a") + `)`

// alertVisibleClause hides alerts of soft-deleted monitors while keeping
// monitor-less (mesh) alerts visible.
const alertVisibleClause = `(a.monitor_id IS NULL OR m.deleted_at IS NULL)`

// alertSearchPredicate matches an alert's subject as the list shows it — the
// monitor's name, or either location of a mesh alert — by case-insensitive
// substring ($patternArg, already escaped and wrapped in %), or its exact ID
// ($idArg, lower-cased). It needs only the a and m aliases, so the count and
// page queries share it; the locations it reads are the alert's own, which the
// tenant filter on a already scopes.
func alertSearchPredicate(patternArg, idArg int) string {
	return fmt.Sprintf(`(m.name ILIKE $%[1]d
		OR EXISTS (
			SELECT 1 FROM locations l
			WHERE l.id IN (a.source_location_id, a.target_location_id)
			  AND l.name ILIKE $%[1]d
		)
		OR a.id::text = $%[2]d)`, patternArg, idArg)
}

// escapeLikePattern makes s match literally inside a LIKE pattern (backslash
// is Postgres's default LIKE escape).
func escapeLikePattern(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// scanAlertWithDetails scans one alertDetailSelect row.
func scanAlertWithDetails(scan func(dest ...interface{}) error) (*models.AlertWithDetails, error) {
	var alert models.AlertWithDetails
	var triage triageview.Scan
	err := scan(append([]any{
		&alert.ID, &alert.TenantID, &alert.MonitorID,
		&alert.Status, &alert.TriggeredAt, &alert.AcknowledgedAt, &alert.ResolvedAt,
		&alert.FailureCount, &alert.LastError, &alert.Kind, &alert.BaselineLatencyMs, &alert.ObservedLatencyMs, &alert.AnomalyScore,
		&alert.MetricName, &alert.MetricValue, &alert.ThresholdValue,
		&alert.CreatedAt, &alert.UpdatedAt,
		&alert.MonitorName,
		&alert.RootCauseMonitorID, &alert.RootCauseDownSince, &alert.RootCauseMonitorName,
		&alert.SourceLocationID, &alert.TargetLocationID, &alert.SourceLocationName, &alert.TargetLocationName,
		&alert.SuppressionReason, &alert.ImpactedCount,
		&alert.AcknowledgedByUserID, &alert.AcknowledgedBy, &alert.SnoozedUntil,
		&alert.SnoozedAt, &alert.SnoozedByUserID, &alert.SnoozedBy,
		&alert.RelapseCount, &alert.ReopenedAt,
	}, triage.Dest()...)...)
	if err != nil {
		return nil, err
	}
	alert.Triage = triage.Build()
	return &alert, nil
}

// ListAlerts lists alerts with filtering, sorting (ListSort; the zero value
// is newest first) and pagination.
func (s *Service) ListAlerts(ctx context.Context, tenantID uuid.UUID, params *models.AlertListParams, sort ListSort) (*models.AlertListResponse, error) {
	params.Page, params.PageSize = pagination.Normalize(params.Page, params.PageSize, pagination.DefaultSize, pagination.MaxSize)

	offset := pagination.Offset(params.Page, params.PageSize)

	// Build WHERE clause
	whereParts := []string{"a.tenant_id = $1"}
	args := []interface{}{tenantID}
	argIndex := 2

	if params.Status != nil {
		whereParts = append(whereParts, fmt.Sprintf("a.status = $%d", argIndex))
		args = append(args, *params.Status)
		argIndex++
	}

	if params.MonitorID != nil {
		whereParts = append(whereParts, fmt.Sprintf("a.monitor_id = $%d", argIndex))
		args = append(args, *params.MonitorID)
		argIndex++
	}

	// Since keeps every alert that was open at some point after it: triggered
	// since, resolved since, or still unresolved. Filtering on triggered_at
	// alone hid long-running outages from any recent window.
	if params.Since != nil {
		whereParts = append(whereParts, fmt.Sprintf(
			"(a.triggered_at >= $%d OR a.resolved_at IS NULL OR a.resolved_at >= $%d)", argIndex, argIndex))
		args = append(args, *params.Since)
		argIndex++
	}

	if params.Suppressed != nil {
		whereParts = append(whereParts, fmt.Sprintf("%s = $%d", alertSuppressedPredicate, argIndex))
		args = append(args, *params.Suppressed)
		argIndex++
	}

	if params.Query != "" {
		whereParts = append(whereParts, alertSearchPredicate(argIndex, argIndex+1))
		args = append(args, "%"+escapeLikePattern(params.Query)+"%", strings.ToLower(params.Query))
		argIndex += 2
	}

	whereClause := strings.Join(whereParts, " AND ")

	// Under a one-status filter every row ranks the same, so a status sort is
	// its tiebreak order (newest first): say so, and the triggered_at index
	// serves it instead of a sort over the computed rank.
	if params.Status != nil && sort.Column == SortStatus {
		sort = ListSort{}
	}

	// Count total
	countQuery := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM alerts a
		LEFT JOIN monitors m ON a.monitor_id = m.id
		LEFT JOIN tenants te ON te.id = a.tenant_id
		WHERE %s AND %s
	`, whereClause, alertVisibleClause)
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("failed to count alerts: %w", err)
	}

	// Get alerts with details
	query := fmt.Sprintf(`
		%s
		WHERE %s AND %s
		ORDER BY %s
		LIMIT $%d OFFSET $%d
	`, alertDetailSelect, whereClause, alertVisibleClause, sort.orderBy(), argIndex, argIndex+1)

	args = append(args, params.PageSize, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list alerts: %w", err)
	}
	defer rows.Close()

	var alerts []models.AlertWithDetails
	for rows.Next() {
		alert, err := scanAlertWithDetails(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("failed to scan alert: %w", err)
		}
		alerts = append(alerts, *alert)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating alerts: %w", err)
	}

	return &models.AlertListResponse{
		Items:    alerts,
		Page:     params.Page,
		PageSize: params.PageSize,
		Total:    total,
	}, nil
}

// GetAlert retrieves an alert by ID. A labelable triage also carries its
// label context (the triage label dialog reads it from this route only), and
// a relapsed alert its relapse history.
func (s *Service) GetAlert(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error) {
	alert, err := s.getAlert(ctx, tenantID, alertID)
	if err != nil {
		return nil, err
	}
	if alert.RelapseCount > 0 {
		relapses, err := s.loadRelapses(ctx, tenantID, alertID)
		if err != nil {
			return nil, err
		}
		alert.Relapses = relapses
	}
	if alert.Triage != nil && alert.Triage.Labelable {
		labelContext, err := triageview.LoadLabelContext(ctx, s.db, tenantID, alert.Triage.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to load triage label context: %w", err)
		}
		alert.Triage.LabelContext = labelContext
	}
	return alert, nil
}

// loadRelapses reads an alert's relapse history, oldest first.
func (s *Service) loadRelapses(ctx context.Context, tenantID, alertID uuid.UUID) ([]models.AlertRelapse, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.relapse_number, r.recovered_at, r.reopened_at
		FROM alert_relapses r
		JOIN alerts a ON a.id = r.alert_id
		WHERE r.alert_id = $1 AND a.tenant_id = $2
		ORDER BY r.relapse_number
	`, alertID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to load alert relapses: %w", err)
	}
	defer rows.Close()
	var relapses []models.AlertRelapse
	for rows.Next() {
		var r models.AlertRelapse
		if err := rows.Scan(&r.Number, &r.RecoveredAt, &r.ReopenedAt); err != nil {
			return nil, fmt.Errorf("failed to scan alert relapse: %w", err)
		}
		relapses = append(relapses, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating alert relapses: %w", err)
	}
	return relapses, nil
}

// getAlert retrieves an alert by ID with its compact triage.
func (s *Service) getAlert(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error) {
	query := alertDetailSelect + `
		WHERE a.id = $1 AND a.tenant_id = $2 AND ` + alertVisibleClause

	row := s.db.QueryRowContext(ctx, query, alertID, tenantID)
	alert, err := scanAlertWithDetails(row.Scan)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAlertNotFound
		}
		return nil, fmt.Errorf("failed to get alert: %w", err)
	}
	return alert, nil
}

// GetRecentAlerts retrieves the most recent alerts for dashboard
func (s *Service) GetRecentAlerts(ctx context.Context, tenantID uuid.UUID, limit int) ([]models.AlertWithDetails, error) {
	if limit < 1 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	query := alertDetailSelect + `
		WHERE a.tenant_id = $1 AND ` + alertVisibleClause + `
		ORDER BY a.triggered_at DESC
		LIMIT $2
	`

	rows, err := s.db.QueryContext(ctx, query, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get recent alerts: %w", err)
	}
	defer rows.Close()

	alerts := []models.AlertWithDetails{}
	for rows.Next() {
		alert, err := scanAlertWithDetails(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("failed to scan alert: %w", err)
		}
		alerts = append(alerts, *alert)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating alerts: %w", err)
	}

	return alerts, nil
}

// GetRecentAlertsForTags retrieves recent alerts for monitors carrying any of
// the selected tags (array overlap: the dashboard tag scope, see the comment
// in services/dashboard/service.go). Mesh alerts carry no monitor (and no
// tags), so the tag filter naturally excludes them.
func (s *Service) GetRecentAlertsForTags(ctx context.Context, tenantID uuid.UUID, tags []string, limit int) ([]models.AlertWithDetails, error) {
	if len(tags) == 0 {
		return s.GetRecentAlerts(ctx, tenantID, limit)
	}
	if limit < 1 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	query := alertDetailSelect + `
		WHERE a.tenant_id = $1
		  AND m.tenant_id = $1
		  AND m.tags && $2::text[]
		  AND m.deleted_at IS NULL
		ORDER BY a.triggered_at DESC
		LIMIT $3
	`

	rows, err := s.db.QueryContext(ctx, query, tenantID, pq.Array(tags), limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get recent alerts for tags: %w", err)
	}
	defer rows.Close()

	alerts := []models.AlertWithDetails{}
	for rows.Next() {
		alert, err := scanAlertWithDetails(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("failed to scan alert: %w", err)
		}
		alerts = append(alerts, *alert)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating alerts: %w", err)
	}

	return alerts, nil
}

// firstAckEpisodeStartSet is the SET item that records, with the first
// acknowledgement, when the paging episode it answered began: the trigger, or
// the relapse reopen it followed. SET expressions read the row as it was, so
// it sees first_acknowledged_at before the same statement sets it. SLA time
// to acknowledge is first_acknowledged_at minus this, so a first
// acknowledgement given during a relapse is not stretched across the
// recovered gap. alias qualifies the alerts row ("" for none).
func firstAckEpisodeStartSet(alias string) string {
	if alias != "" {
		alias += "."
	}
	return `first_ack_episode_started_at = CASE WHEN ` + alias + `first_acknowledged_at IS NULL
				THEN COALESCE(` + alias + `reopened_at, ` + alias + `triggered_at) ELSE ` + alias + `first_ack_episode_started_at END`
}

// AcknowledgeAlert marks an alert as acknowledged, by the acting user
// (actingUserID). From then on the alerter sends it no reminder and no
// further escalation (shared/alertrouting mute.go). The first acknowledgement
// is also kept in first_acknowledged_at, which neither an unacknowledge nor a
// relapse reopen ever clears: SLA reports measure time to acknowledge from it
// (firstAckEpisodeStartSet).
func (s *Service) AcknowledgeAlert(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error) {
	now := time.Now()
	query := `
		UPDATE alerts
		SET status = 'acknowledged', acknowledged_at = $1, acknowledged_by_user_id = $4, updated_at = $1,
			first_acknowledged_at = COALESCE(first_acknowledged_at, $1),
			` + firstAckEpisodeStartSet("") + `
		WHERE id = $2 AND tenant_id = $3 AND status = 'active'
		RETURNING id
	`

	var id uuid.UUID
	err := s.db.QueryRowContext(ctx, query, now, alertID, tenantID, actingUserID(ctx)).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAlertNotActive
		}
		return nil, fmt.Errorf("failed to acknowledge alert: %w", err)
	}

	return s.getAlert(ctx, tenantID, alertID)
}

// ResolveAlert marks an alert as resolved
func (s *Service) ResolveAlert(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error) {
	now := time.Now()
	// resolve_reason 'manual' (notifications.ResolveReasonManual): the
	// closing notice the alerter sends must not claim the condition cleared.
	query := `
		UPDATE alerts
		SET status = 'resolved', resolved_at = $1, updated_at = $1,
			resolve_reason = '` + notifications.ResolveReasonManual + `'
		WHERE id = $2 AND tenant_id = $3 AND status IN ('active', 'acknowledged')
		RETURNING id
	`

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin alert transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var id uuid.UUID
	err = tx.QueryRowContext(ctx, query, now, alertID, tenantID).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAlertAlreadyResolved
		}
		return nil, fmt.Errorf("failed to resolve alert: %w", err)
	}

	if s.incidents != nil {
		if err := s.incidents.RecordAlertRecoveryIfNeededTx(ctx, tx, tenantID, alertID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit alert transaction: %w", err)
	}

	return s.getAlert(ctx, tenantID, alertID)
}

// GetActiveAlertForMonitor gets the active alert for a monitor if one exists
func (s *Service) GetActiveAlertForMonitor(ctx context.Context, tenantID, monitorID uuid.UUID) (*models.Alert, error) {
	query := `
		SELECT id, tenant_id, monitor_id, status,
			triggered_at, acknowledged_at, resolved_at, failure_count,
			last_error, kind, baseline_latency_ms, observed_latency_ms, anomaly_score,
			metric_name, metric_value, threshold_value,
			root_cause_monitor_id, root_cause_down_since, relapse_count, reopened_at, created_at, updated_at
		FROM alerts
		WHERE tenant_id = $1 AND monitor_id = $2 AND status IN ('active', 'acknowledged')
		ORDER BY triggered_at DESC
		LIMIT 1
	`

	var alert models.Alert
	err := s.db.QueryRowContext(ctx, query, tenantID, monitorID).Scan(
		&alert.ID, &alert.TenantID, &alert.MonitorID,
		&alert.Status, &alert.TriggeredAt, &alert.AcknowledgedAt, &alert.ResolvedAt,
		&alert.FailureCount, &alert.LastError, &alert.Kind, &alert.BaselineLatencyMs, &alert.ObservedLatencyMs, &alert.AnomalyScore,
		&alert.MetricName, &alert.MetricValue, &alert.ThresholdValue,
		&alert.RootCauseMonitorID, &alert.RootCauseDownSince, &alert.RelapseCount, &alert.ReopenedAt,
		&alert.CreatedAt, &alert.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // No active alert
		}
		return nil, fmt.Errorf("failed to get active alert: %w", err)
	}

	return &alert, nil
}
