package monitors

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/maintenance"
	"github.com/yassinebenameur/probara/shared/monitorstate"
)

// Repository defines the interface for monitor data access
type Repository interface {
	Create(ctx context.Context, monitor *models.Monitor) error
	GetByID(ctx context.Context, tenantID, monitorID uuid.UUID) (*models.Monitor, error)
	List(ctx context.Context, tenantID uuid.UUID, tag *string, enabled *bool, page, pageSize int) ([]models.Monitor, int, error)
	Update(ctx context.Context, monitor *models.Monitor, fields []string, values []interface{}) error
	Delete(ctx context.Context, tenantID, monitorID uuid.UUID) error
	BulkSoftDelete(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID) (int64, error)
	DeleteHistory(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID) error
	HardDelete(ctx context.Context, monitorID uuid.UUID) error
	VerifyAlertPolicy(ctx context.Context, tenantID, policyID uuid.UUID) error
	VerifyMonitorsBelongToTenant(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID) error
	SetAlertPolicies(ctx context.Context, monitorID uuid.UUID, policyIDs []uuid.UUID) error
	GetAlertPolicyIDs(ctx context.Context, monitorID uuid.UUID) ([]uuid.UUID, error)
	GetAlertPolicyIDsForMonitors(ctx context.Context, monitorIDs []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error)
	GetMemberIDs(ctx context.Context, groupID uuid.UUID) ([]uuid.UUID, error)
	GetDependsOnIDs(ctx context.Context, monitorID uuid.UUID) ([]uuid.UUID, error)
	// monitor_channels management
	ReplaceMonitorChannels(ctx context.Context, tenantID, monitorID uuid.UUID, channels []models.MonitorChannelAssignment) error
	DeleteMonitorChannels(ctx context.Context, monitorID uuid.UUID) error
	GetChannelsForMonitors(ctx context.Context, monitorIDs []uuid.UUID) (map[uuid.UUID][]models.MonitorChannelAssignment, error)
	// monitor_locations management
	SetLocations(ctx context.Context, tenantID, monitorID uuid.UUID, locationIDs []uuid.UUID) error
	// SetEnabled toggles pause/resume with the S-P2 state reset.
	SetEnabled(ctx context.Context, tenantID, monitorID uuid.UUID, enabled bool) error
	GetLocationIDsForMonitors(ctx context.Context, monitorIDs []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error)
	GetLocationStatuses(ctx context.Context, monitorID uuid.UUID) ([]models.MonitorLocationStatus, error)
}

// PostgresRepository implements Repository for PostgreSQL
type PostgresRepository struct {
	db db.DB
}

// NewPostgresRepository creates a new PostgreSQL repository
func NewPostgresRepository(database db.DB) *PostgresRepository {
	return &PostgresRepository{db: database}
}

// Create inserts a new monitor into the database
func (r *PostgresRepository) Create(ctx context.Context, monitor *models.Monitor) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin create transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	query := `
		INSERT INTO monitors (
			id, tenant_id, name, type, config,
			interval_seconds, timeout_seconds, alert_policy_id, enabled, tags,
			agent_id, push_token, next_run_at, created_at, updated_at, deleted_at,
			consecutive_failures_threshold, notification_mode, member_alert_rollup, location_quorum
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, NULL, $16, $17, $18, $19)
		RETURNING id, tenant_id, name, type, config,
			interval_seconds, timeout_seconds, alert_policy_id, enabled, tags,
			agent_id, push_token, next_run_at, created_at, updated_at, deleted_at,
			consecutive_failures_threshold, notification_mode, member_alert_rollup, location_quorum, current_state
	`

	var tags []string

	err = tx.QueryRowContext(ctx, query,
		monitor.ID, monitor.TenantID, monitor.Name, monitor.Type, monitor.Config,
		monitor.IntervalSeconds, monitor.TimeoutSeconds, monitor.AlertPolicyID,
		monitor.Enabled, pq.Array(monitor.Tags), monitor.AgentID, monitor.PushToken, monitor.NextRunAt,
		monitor.CreatedAt, monitor.UpdatedAt,
		monitor.ConsecutiveFailuresThreshold, monitor.NotificationMode, monitor.MemberAlertRollup, monitor.LocationQuorum,
	).Scan(
		&monitor.ID, &monitor.TenantID, &monitor.Name, &monitor.Type,
		&monitor.Config, &monitor.IntervalSeconds, &monitor.TimeoutSeconds,
		&monitor.AlertPolicyID, &monitor.Enabled,
		pq.Array(&tags), &monitor.AgentID, &monitor.PushToken, &monitor.NextRunAt, &monitor.CreatedAt, &monitor.UpdatedAt, &monitor.DeletedAt,
		&monitor.ConsecutiveFailuresThreshold, &monitor.NotificationMode, &monitor.MemberAlertRollup, &monitor.LocationQuorum, &monitor.CurrentState,
	)

	if err != nil {
		return fmt.Errorf("failed to create monitor: %w", err)
	}

	// The timeline starts at creation: every non-group monitor always has
	// exactly one open state interval (S-U4, docs/state-semantics.md). Group
	// state is derived by the alerter's SQL roll-up and has no timeline yet.
	if monitor.Type != models.MonitorTypeGroup {
		if err := monitorstate.RecordIntervalTx(ctx, tx, monitor.TenantID, monitor.ID,
			monitorstate.State(monitor.CurrentState), monitorstate.IntervalReasonCreated); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit create transaction: %w", err)
	}

	monitor.Tags = tags
	return nil
}

// GetByID retrieves a monitor by ID
func (r *PostgresRepository) GetByID(ctx context.Context, tenantID, monitorID uuid.UUID) (*models.Monitor, error) {
	query := `
		SELECT id, tenant_id, name, type, config,
			interval_seconds, timeout_seconds, alert_policy_id, enabled, tags,
			agent_id, push_token, next_run_at, created_at, updated_at, deleted_at,
			consecutive_failures_threshold, notification_mode, member_alert_rollup, location_quorum, current_state,
			` + maintenance.InMaintenancePredicate("monitors") + ` AS in_maintenance,
			` + maintenance.MaintenanceUntilExpr("monitors") + ` AS maintenance_until
		FROM monitors
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`

	var monitor models.Monitor
	var tags []string

	err := r.db.QueryRowContext(ctx, query, monitorID, tenantID).Scan(
		&monitor.ID, &monitor.TenantID, &monitor.Name, &monitor.Type,
		&monitor.Config, &monitor.IntervalSeconds, &monitor.TimeoutSeconds,
		&monitor.AlertPolicyID, &monitor.Enabled,
		pq.Array(&tags), &monitor.AgentID, &monitor.PushToken, &monitor.NextRunAt, &monitor.CreatedAt, &monitor.UpdatedAt, &monitor.DeletedAt,
		&monitor.ConsecutiveFailuresThreshold, &monitor.NotificationMode, &monitor.MemberAlertRollup, &monitor.LocationQuorum, &monitor.CurrentState,
		&monitor.InMaintenance, &monitor.MaintenanceUntil,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("monitor not found")
		}
		return nil, fmt.Errorf("failed to get monitor: %w", err)
	}

	monitor.Tags = tags
	return &monitor, nil
}

// List retrieves monitors with optional filters and pagination
func (r *PostgresRepository) List(ctx context.Context, tenantID uuid.UUID, tag *string, enabled *bool, page, pageSize int) ([]models.Monitor, int, error) {
	offset := (page - 1) * pageSize

	// Build query with filters
	whereClause := "WHERE tenant_id = $1 AND deleted_at IS NULL"
	args := []interface{}{tenantID}
	argIndex := 2

	if tag != nil {
		whereClause += fmt.Sprintf(" AND $%d = ANY(tags)", argIndex)
		args = append(args, *tag)
		argIndex++
	}

	if enabled != nil {
		whereClause += fmt.Sprintf(" AND enabled = $%d", argIndex)
		args = append(args, *enabled)
		argIndex++
	}

	// Count total
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM monitors %s", whereClause)
	var total int
	err := r.db.QueryRowContext(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count monitors: %w", err)
	}

	// Get monitors
	query := fmt.Sprintf(`
		SELECT id, tenant_id, name, type, config,
			interval_seconds, timeout_seconds, alert_policy_id, enabled, tags,
			agent_id, push_token, next_run_at, created_at, updated_at, deleted_at,
			consecutive_failures_threshold, notification_mode, member_alert_rollup, location_quorum, current_state,
			%s AS in_maintenance,
			%s AS maintenance_until
		FROM monitors
		%s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d
	`, maintenance.InMaintenancePredicate("monitors"), maintenance.MaintenanceUntilExpr("monitors"), whereClause, argIndex, argIndex+1)

	args = append(args, pageSize, offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list monitors: %w", err)
	}
	defer rows.Close()

	var monitors []models.Monitor
	for rows.Next() {
		var monitor models.Monitor
		var tags []string

		err := rows.Scan(
			&monitor.ID, &monitor.TenantID, &monitor.Name, &monitor.Type,
			&monitor.Config, &monitor.IntervalSeconds, &monitor.TimeoutSeconds,
			&monitor.AlertPolicyID, &monitor.Enabled,
			pq.Array(&tags), &monitor.AgentID, &monitor.PushToken, &monitor.NextRunAt, &monitor.CreatedAt, &monitor.UpdatedAt, &monitor.DeletedAt,
			&monitor.ConsecutiveFailuresThreshold, &monitor.NotificationMode, &monitor.MemberAlertRollup, &monitor.LocationQuorum, &monitor.CurrentState,
			&monitor.InMaintenance, &monitor.MaintenanceUntil,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan monitor: %w", err)
		}

		monitor.Tags = tags
		monitors = append(monitors, monitor)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error iterating monitors: %w", err)
	}

	return monitors, total, nil
}

// Update updates a monitor with the given fields
func (r *PostgresRepository) Update(ctx context.Context, monitor *models.Monitor, setParts []string, args []interface{}) error {
	if len(setParts) == 0 {
		return nil
	}

	// Add updated_at
	argIndex := len(args) + 1
	setParts = append(setParts, fmt.Sprintf("updated_at = $%d", argIndex))
	args = append(args, time.Now())
	argIndex++

	// Add WHERE clause
	whereArgIndex := argIndex
	args = append(args, monitor.ID, monitor.TenantID)

	setClause := ""
	for i, part := range setParts {
		if i > 0 {
			setClause += ", "
		}
		setClause += part
	}

	query := fmt.Sprintf(`
		UPDATE monitors
		SET %s
		WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL
		RETURNING id, tenant_id, name, type, config,
			interval_seconds, timeout_seconds, alert_policy_id, enabled, tags,
			agent_id, push_token, next_run_at, created_at, updated_at, deleted_at,
			consecutive_failures_threshold, notification_mode, member_alert_rollup, location_quorum, current_state
	`, setClause, whereArgIndex, whereArgIndex+1)

	var tags []string

	err := r.db.QueryRowContext(ctx, query, args...).Scan(
		&monitor.ID, &monitor.TenantID, &monitor.Name, &monitor.Type,
		&monitor.Config, &monitor.IntervalSeconds, &monitor.TimeoutSeconds,
		&monitor.AlertPolicyID, &monitor.Enabled,
		pq.Array(&tags), &monitor.AgentID, &monitor.PushToken, &monitor.NextRunAt, &monitor.CreatedAt, &monitor.UpdatedAt, &monitor.DeletedAt,
		&monitor.ConsecutiveFailuresThreshold, &monitor.NotificationMode, &monitor.MemberAlertRollup, &monitor.LocationQuorum, &monitor.CurrentState,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("monitor not found")
		}
		return fmt.Errorf("failed to update monitor: %w", err)
	}

	monitor.Tags = tags
	return nil
}

// Delete marks a monitor as deleted (soft delete). The background purger
// removes child rows and the monitor row itself.
func (r *PostgresRepository) Delete(ctx context.Context, tenantID, monitorID uuid.UUID) error {
	query := `UPDATE monitors SET deleted_at = NOW(), updated_at = NOW()
	          WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`
	result, err := r.db.ExecContext(ctx, query, monitorID, tenantID)
	if err != nil {
		return fmt.Errorf("failed to soft delete monitor: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("monitor not found")
	}
	return nil
}

// BulkSoftDelete tombstones every monitor in monitorIDs that belongs to the
// tenant and is not already deleted. Returns the number of newly tombstoned rows.
func (r *PostgresRepository) BulkSoftDelete(
	ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID,
) (int64, error) {
	if len(monitorIDs) == 0 {
		return 0, nil
	}
	query := `UPDATE monitors SET deleted_at = NOW(), updated_at = NOW()
	          WHERE tenant_id = $1 AND id = ANY($2) AND deleted_at IS NULL`
	result, err := r.db.ExecContext(ctx, query, tenantID, pq.Array(monitorIDs))
	if err != nil {
		return 0, fmt.Errorf("failed to bulk soft delete monitors: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}
	return rows, nil
}

// HardDelete removes the monitor row itself. Used by the purger after it has
// drained all child tables. Tenant-agnostic because the caller already loaded
// the row.
func (r *PostgresRepository) HardDelete(ctx context.Context, monitorID uuid.UUID) error {
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM monitors WHERE id = $1`, monitorID,
	); err != nil {
		return fmt.Errorf("failed to hard delete monitor: %w", err)
	}
	return nil
}

// DeleteHistory removes monitor-scoped checks, alerts, and persisted analytics state.
func (r *PostgresRepository) DeleteHistory(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID) error {
	if len(monitorIDs) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin history deletion transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	deleteStatements := []string{
		`DELETE FROM alert_notification_states WHERE alert_id IN (SELECT id FROM alerts WHERE tenant_id = $1 AND monitor_id = ANY($2))`,
		`DELETE FROM alerts WHERE tenant_id = $1 AND monitor_id = ANY($2)`,
		`DELETE FROM monitor_downtime_open WHERE tenant_id = $1 AND monitor_id = ANY($2)`,
		`DELETE FROM monitor_downtime_periods WHERE tenant_id = $1 AND monitor_id = ANY($2)`,
		`DELETE FROM monitor_hourly_rollups WHERE tenant_id = $1 AND monitor_id = ANY($2)`,
		`DELETE FROM monitor_daily_rollups WHERE tenant_id = $1 AND monitor_id = ANY($2)`,
		`DELETE FROM check_results WHERE tenant_id = $1 AND monitor_id = ANY($2)`,
		// Closed intervals are history; the open one stays — a live monitor
		// always keeps exactly one open interval (S-U4).
		`DELETE FROM monitor_state_intervals WHERE tenant_id = $1 AND monitor_id = ANY($2) AND ended_at IS NOT NULL`,
	}

	for _, stmt := range deleteStatements {
		if _, err := tx.ExecContext(ctx, stmt, tenantID, pq.Array(monitorIDs)); err != nil {
			return fmt.Errorf("failed to delete monitor history: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit monitor history deletion: %w", err)
	}

	return nil
}

// VerifyAlertPolicy checks if an alert policy exists and belongs to the tenant
func (r *PostgresRepository) VerifyAlertPolicy(ctx context.Context, tenantID, policyID uuid.UUID) error {
	var id uuid.UUID
	query := `SELECT id FROM alert_policies WHERE id = $1 AND tenant_id = $2`
	err := r.db.QueryRowContext(ctx, query, policyID, tenantID).Scan(&id)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("alert policy not found or does not belong to tenant")
		}
		return fmt.Errorf("failed to verify alert policy: %w", err)
	}
	return nil
}

// VerifyMonitorsBelongToTenant returns nil only if every provided monitor ID
// exists and belongs to the tenant. Otherwise it returns a descriptive error
// whose message contains "not found or do not belong to tenant".
func (r *PostgresRepository) VerifyMonitorsBelongToTenant(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID) error {
	if len(monitorIDs) == 0 {
		return fmt.Errorf("monitor IDs cannot be empty")
	}
	var count int
	query := `SELECT COUNT(*) FROM monitors
	          WHERE id = ANY($1) AND tenant_id = $2 AND deleted_at IS NULL`
	if err := r.db.QueryRowContext(ctx, query, pq.Array(monitorIDs), tenantID).Scan(&count); err != nil {
		return fmt.Errorf("failed to verify monitors: %w", err)
	}
	if count != len(monitorIDs) {
		return fmt.Errorf("one or more monitors not found or do not belong to tenant")
	}
	return nil
}

// SetAlertPolicies replaces the alert policies for a monitor
func (r *PostgresRepository) SetAlertPolicies(ctx context.Context, monitorID uuid.UUID, policyIDs []uuid.UUID) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, `DELETE FROM monitor_alert_policies WHERE monitor_id = $1`, monitorID); err != nil {
		return fmt.Errorf("failed to clear monitor alert policies: %w", err)
	}

	if len(policyIDs) > 0 {
		query := `
			INSERT INTO monitor_alert_policies (monitor_id, alert_policy_id, created_at)
			VALUES ($1, $2, NOW())
			ON CONFLICT (monitor_id, alert_policy_id) DO NOTHING
		`
		for _, policyID := range policyIDs {
			if _, err := tx.ExecContext(ctx, query, monitorID, policyID); err != nil {
				return fmt.Errorf("failed to add monitor alert policy: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit monitor alert policies: %w", err)
	}

	return nil
}

// GetAlertPolicyIDs retrieves alert policy IDs for a monitor
func (r *PostgresRepository) GetAlertPolicyIDs(ctx context.Context, monitorID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT alert_policy_id FROM monitor_alert_policies WHERE monitor_id = $1 ORDER BY created_at`, monitorID)
	if err != nil {
		return nil, fmt.Errorf("failed to get monitor alert policies: %w", err)
	}
	defer rows.Close()

	var policyIDs []uuid.UUID
	for rows.Next() {
		var policyID uuid.UUID
		if err := rows.Scan(&policyID); err != nil {
			return nil, fmt.Errorf("failed to scan monitor alert policy: %w", err)
		}
		policyIDs = append(policyIDs, policyID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating monitor alert policies: %w", err)
	}
	return policyIDs, nil
}

// GetAlertPolicyIDsForMonitors retrieves alert policy IDs for a set of monitors
func (r *PostgresRepository) GetAlertPolicyIDsForMonitors(ctx context.Context, monitorIDs []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	result := make(map[uuid.UUID][]uuid.UUID)
	if len(monitorIDs) == 0 {
		return result, nil
	}

	query := `
		SELECT monitor_id, alert_policy_id
		FROM monitor_alert_policies
		WHERE monitor_id = ANY($1)
		ORDER BY created_at
	`
	rows, err := r.db.QueryContext(ctx, query, pq.Array(monitorIDs))
	if err != nil {
		return nil, fmt.Errorf("failed to list monitor alert policies: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var monitorID uuid.UUID
		var policyID uuid.UUID
		if err := rows.Scan(&monitorID, &policyID); err != nil {
			return nil, fmt.Errorf("failed to scan monitor alert policy: %w", err)
		}
		result[monitorID] = append(result[monitorID], policyID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating monitor alert policies: %w", err)
	}

	return result, nil
}

// GetMemberIDs retrieves all member IDs for a group monitor, excluding tombstoned members.
func (r *PostgresRepository) GetMemberIDs(ctx context.Context, groupID uuid.UUID) ([]uuid.UUID, error) {
	query := `
		SELECT mg.monitor_id
		FROM monitor_groups mg
		JOIN monitors m ON m.id = mg.monitor_id
		WHERE mg.group_id = $1 AND m.deleted_at IS NULL
		ORDER BY mg.created_at
	`
	rows, err := r.db.QueryContext(ctx, query, groupID)
	if err != nil {
		return nil, fmt.Errorf("failed to get group members: %w", err)
	}
	defer rows.Close()

	var memberIDs []uuid.UUID
	for rows.Next() {
		var memberID uuid.UUID
		if err := rows.Scan(&memberID); err != nil {
			return nil, fmt.Errorf("failed to scan member ID: %w", err)
		}
		memberIDs = append(memberIDs, memberID)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating member IDs: %w", err)
	}

	return memberIDs, nil
}

// GetDependsOnIDs retrieves the upstream dependency IDs for a monitor,
// excluding tombstoned targets.
func (r *PostgresRepository) GetDependsOnIDs(ctx context.Context, monitorID uuid.UUID) ([]uuid.UUID, error) {
	query := `
		SELECT md.depends_on_id
		FROM monitor_dependencies md
		JOIN monitors m ON m.id = md.depends_on_id
		WHERE md.monitor_id = $1 AND m.deleted_at IS NULL
		ORDER BY md.created_at
	`
	rows, err := r.db.QueryContext(ctx, query, monitorID)
	if err != nil {
		return nil, fmt.Errorf("failed to get monitor dependencies: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan dependency ID: %w", err)
		}
		ids = append(ids, id)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating dependency IDs: %w", err)
	}

	return ids, nil
}

// ReplaceMonitorChannels atomically replaces all channel assignments for a monitor.
// Each channel is validated against alert_channels to prevent cross-tenant assignments.
func (r *PostgresRepository) ReplaceMonitorChannels(ctx context.Context, tenantID, monitorID uuid.UUID, channels []models.MonitorChannelAssignment) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM monitor_channels WHERE monitor_id = $1`, monitorID); err != nil {
		return fmt.Errorf("failed to clear monitor channels: %w", err)
	}

	for _, c := range channels {
		channelID, err := uuid.Parse(c.ChannelID)
		if err != nil {
			return fmt.Errorf("invalid channel_id %q: %w", c.ChannelID, err)
		}
		result, err := tx.ExecContext(ctx, `
			INSERT INTO monitor_channels (monitor_id, channel_id, delay_seconds)
			SELECT $1, $2, $3
			WHERE EXISTS (SELECT 1 FROM alert_channels WHERE id = $2 AND tenant_id = $4)
		`, monitorID, channelID, c.DelaySeconds, tenantID)
		if err != nil {
			return fmt.Errorf("failed to insert monitor channel: %w", err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("failed to check rows affected: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("channel %s not found or does not belong to tenant", c.ChannelID)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit monitor channels: %w", err)
	}
	return nil
}

// DeleteMonitorChannels removes all channel assignments for a monitor.
func (r *PostgresRepository) DeleteMonitorChannels(ctx context.Context, monitorID uuid.UUID) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM monitor_channels WHERE monitor_id = $1`, monitorID); err != nil {
		return fmt.Errorf("failed to delete monitor channels: %w", err)
	}
	return nil
}

// GetChannelsForMonitors loads monitor_channels rows for a set of monitors,
// returning a map keyed by monitor ID.
func (r *PostgresRepository) GetChannelsForMonitors(ctx context.Context, monitorIDs []uuid.UUID) (map[uuid.UUID][]models.MonitorChannelAssignment, error) {
	result := make(map[uuid.UUID][]models.MonitorChannelAssignment)
	if len(monitorIDs) == 0 {
		return result, nil
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT mc.monitor_id, mc.channel_id, ac.name, ac.type, mc.delay_seconds
		FROM monitor_channels mc
		JOIN alert_channels ac ON ac.id = mc.channel_id
		WHERE mc.monitor_id = ANY($1)
		ORDER BY mc.monitor_id, mc.channel_id
	`, pq.Array(monitorIDs))
	if err != nil {
		return nil, fmt.Errorf("failed to query monitor channels: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var monitorID uuid.UUID
		var channelID uuid.UUID
		var channelName string
		var channelType string
		var delaySeconds int
		if err := rows.Scan(&monitorID, &channelID, &channelName, &channelType, &delaySeconds); err != nil {
			return nil, fmt.Errorf("failed to scan monitor channel: %w", err)
		}
		result[monitorID] = append(result[monitorID], models.MonitorChannelAssignment{
			ChannelID:    channelID.String(),
			DelaySeconds: delaySeconds,
			ChannelName:  channelName,
			ChannelType:  channelType,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating monitor channels: %w", err)
	}
	return result, nil
}

// SetLocations atomically replaces a monitor's private-location set. Every
// location must be a live location of the tenant. Per-location state rows for
// removed locations are deleted so they can never resurface in the quorum
// aggregate; when the set becomes empty, the monitor returns to the default
// fleet and restarts the legacy state machine from 'unknown'.
//
// Lock ordering (S-O1, docs/state-semantics.md): the monitor row is locked
// first, matching result ingest — which holds the same lock while touching
// monitor_location_state — so the child-row deletes below can neither
// deadlock against an in-flight result nor lose to one that re-upserts a
// removed location's state row after its membership check.
func (r *PostgresRepository) SetLocations(ctx context.Context, tenantID, monitorID uuid.UUID, locationIDs []uuid.UUID) error {
	// A nil slice must behave like an empty one: pq.Array(nil) encodes SQL
	// NULL and `!= ALL(NULL)` matches nothing, silently keeping every
	// membership and state row while the monitor still resets to 'unknown'.
	if locationIDs == nil {
		locationIDs = []uuid.UUID{}
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var prevState string
	if err := tx.QueryRowContext(ctx, `
		SELECT current_state FROM monitors WHERE id = $1 AND tenant_id = $2 FOR UPDATE
	`, monitorID, tenantID).Scan(&prevState); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("monitor not found")
		}
		return fmt.Errorf("failed to lock monitor: %w", err)
	}

	if len(locationIDs) > 0 {
		var count int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM locations
			WHERE id = ANY($1) AND tenant_id = $2 AND deleted_at IS NULL
		`, pq.Array(locationIDs), tenantID).Scan(&count); err != nil {
			return fmt.Errorf("failed to validate locations: %w", err)
		}
		if count != len(locationIDs) {
			return fmt.Errorf("one or more locations not found")
		}
	}

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM monitor_locations WHERE monitor_id = $1 AND location_id != ALL($2)
	`, monitorID, pq.Array(locationIDs)); err != nil {
		return fmt.Errorf("failed to clear monitor locations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM monitor_location_state WHERE monitor_id = $1 AND location_id != ALL($2)
	`, monitorID, pq.Array(locationIDs)); err != nil {
		return fmt.Errorf("failed to clear monitor location state: %w", err)
	}

	for _, locationID := range locationIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO monitor_locations (monitor_id, location_id)
			VALUES ($1, $2) ON CONFLICT DO NOTHING
		`, monitorID, locationID); err != nil {
			return fmt.Errorf("failed to add monitor location: %w", err)
		}
	}

	if len(locationIDs) == 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE monitors
			SET current_state = 'unknown', consecutive_failures = 0, updated_at = NOW()
			WHERE id = $1 AND tenant_id = $2
		`, monitorID, tenantID); err != nil {
			return fmt.Errorf("failed to reset monitor state: %w", err)
		}
		if prevState != string(monitorstate.StateUnknown) {
			if err := monitorstate.RecordIntervalTx(ctx, tx, tenantID, monitorID,
				monitorstate.StateUnknown, monitorstate.IntervalReasonLocationChange); err != nil {
				return err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit monitor locations: %w", err)
	}
	return nil
}

// SetEnabled toggles a monitor's enabled flag. A real toggle resets observed
// state to 'unknown' with the per-location machinery cleared (S-P2,
// docs/state-semantics.md): the pre-pause state must not survive into resume
// — a monitor paused while down would otherwise re-open an availability
// alert on unpause before any fresh check runs. Lock order per S-O1.
func (r *PostgresRepository) SetEnabled(ctx context.Context, tenantID, monitorID uuid.UUID, enabled bool) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var current bool
	var monitorType string
	if err := tx.QueryRowContext(ctx, `
		SELECT enabled, type FROM monitors WHERE id = $1 AND tenant_id = $2 FOR UPDATE
	`, monitorID, tenantID).Scan(&current, &monitorType); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("monitor not found")
		}
		return fmt.Errorf("failed to lock monitor: %w", err)
	}
	if current == enabled {
		return tx.Commit()
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE monitors
		SET enabled = $2,
			current_state = 'unknown',
			consecutive_failures = 0,
			last_state_change_at = NOW(),
			updated_at = NOW()
		WHERE id = $1
	`, monitorID, enabled); err != nil {
		return fmt.Errorf("failed to toggle monitor: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM monitor_location_state WHERE monitor_id = $1
	`, monitorID); err != nil {
		return fmt.Errorf("failed to clear location state: %w", err)
	}
	if monitorType != string(models.MonitorTypeGroup) {
		reason := monitorstate.IntervalReasonPause
		if enabled {
			reason = monitorstate.IntervalReasonResume
		}
		if err := monitorstate.RecordIntervalTx(ctx, tx, tenantID, monitorID,
			monitorstate.StateUnknown, reason); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit enabled toggle: %w", err)
	}
	return nil
}

// GetLocationIDsForMonitors loads the selected location IDs for a set of
// monitors, keyed by monitor ID. Deleted locations are excluded.
func (r *PostgresRepository) GetLocationIDsForMonitors(ctx context.Context, monitorIDs []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	result := make(map[uuid.UUID][]uuid.UUID)
	if len(monitorIDs) == 0 {
		return result, nil
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT ml.monitor_id, ml.location_id
		FROM monitor_locations ml
		JOIN locations l ON l.id = ml.location_id AND l.deleted_at IS NULL
		WHERE ml.monitor_id = ANY($1)
		ORDER BY ml.created_at
	`, pq.Array(monitorIDs))
	if err != nil {
		return nil, fmt.Errorf("failed to list monitor locations: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var monitorID, locationID uuid.UUID
		if err := rows.Scan(&monitorID, &locationID); err != nil {
			return nil, fmt.Errorf("failed to scan monitor location: %w", err)
		}
		result[monitorID] = append(result[monitorID], locationID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating monitor locations: %w", err)
	}
	return result, nil
}

// GetLocationStatuses loads the per-location breakdown for one monitor:
// each selected location with its connection freshness and check state.
func (r *PostgresRepository) GetLocationStatuses(ctx context.Context, monitorID uuid.UUID) ([]models.MonitorLocationStatus, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT l.id, l.name,
			l.last_seen_at IS NOT NULL AND l.last_seen_at > NOW() - INTERVAL '60 seconds',
			COALESCE(mls.current_state, 'unknown'),
			mls.last_latency_ms, mls.last_check_at
		FROM monitor_locations ml
		JOIN locations l ON l.id = ml.location_id AND l.deleted_at IS NULL
		LEFT JOIN monitor_location_state mls
			ON mls.monitor_id = ml.monitor_id AND mls.location_id = ml.location_id
		WHERE ml.monitor_id = $1
		ORDER BY l.name
	`, monitorID)
	if err != nil {
		return nil, fmt.Errorf("failed to list location statuses: %w", err)
	}
	defer rows.Close()

	var statuses []models.MonitorLocationStatus
	for rows.Next() {
		var s models.MonitorLocationStatus
		if err := rows.Scan(&s.ID, &s.Name, &s.Connected, &s.CurrentState, &s.LastLatencyMs, &s.LastCheckAt); err != nil {
			return nil, fmt.Errorf("failed to scan location status: %w", err)
		}
		statuses = append(statuses, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating location statuses: %w", err)
	}
	return statuses, nil
}

// Ensure PostgresRepository implements Repository
var _ Repository = (*PostgresRepository)(nil)
