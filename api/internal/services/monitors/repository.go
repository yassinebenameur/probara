package monitors

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/db"
)

// Repository defines the interface for monitor data access
type Repository interface {
	Create(ctx context.Context, monitor *models.Monitor) error
	GetByID(ctx context.Context, tenantID, monitorID uuid.UUID) (*models.Monitor, error)
	List(ctx context.Context, tenantID uuid.UUID, tag *string, enabled *bool, page, pageSize int) ([]models.Monitor, int, error)
	Update(ctx context.Context, monitor *models.Monitor, fields []string, values []interface{}) error
	Delete(ctx context.Context, tenantID, monitorID uuid.UUID) error
	DeleteHistory(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID) error
	VerifyAlertPolicy(ctx context.Context, tenantID, policyID uuid.UUID) error
	VerifyMonitorsBelongToTenant(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID) error
	SetAlertPolicies(ctx context.Context, monitorID uuid.UUID, policyIDs []uuid.UUID) error
	BulkAttachAlertPolicy(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID, policyID uuid.UUID) ([]uuid.UUID, error)
	BulkDetachAlertPolicy(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID, policyID uuid.UUID) ([]uuid.UUID, error)
	GetAlertPolicyIDs(ctx context.Context, monitorID uuid.UUID) ([]uuid.UUID, error)
	GetAlertPolicyIDsForMonitors(ctx context.Context, monitorIDs []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error)
	GetMemberIDs(ctx context.Context, groupID uuid.UUID) ([]uuid.UUID, error)
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
	query := `
		INSERT INTO monitors (
			id, tenant_id, name, type, config,
			interval_seconds, timeout_seconds, alert_policy_id, enabled, tags,
			agent_id, push_token, next_run_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING id, tenant_id, name, type, config,
			interval_seconds, timeout_seconds, alert_policy_id, enabled, tags,
			agent_id, push_token, next_run_at, created_at, updated_at
	`

	var tags []string

	err := r.db.QueryRowContext(ctx, query,
		monitor.ID, monitor.TenantID, monitor.Name, monitor.Type, monitor.Config,
		monitor.IntervalSeconds, monitor.TimeoutSeconds, monitor.AlertPolicyID,
		monitor.Enabled, pq.Array(monitor.Tags), monitor.AgentID, monitor.PushToken, monitor.NextRunAt,
		monitor.CreatedAt, monitor.UpdatedAt,
	).Scan(
		&monitor.ID, &monitor.TenantID, &monitor.Name, &monitor.Type,
		&monitor.Config, &monitor.IntervalSeconds, &monitor.TimeoutSeconds,
		&monitor.AlertPolicyID, &monitor.Enabled,
		pq.Array(&tags), &monitor.AgentID, &monitor.PushToken, &monitor.NextRunAt, &monitor.CreatedAt, &monitor.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to create monitor: %w", err)
	}

	monitor.Tags = tags
	return nil
}

// GetByID retrieves a monitor by ID
func (r *PostgresRepository) GetByID(ctx context.Context, tenantID, monitorID uuid.UUID) (*models.Monitor, error) {
	query := `
		SELECT id, tenant_id, name, type, config,
			interval_seconds, timeout_seconds, alert_policy_id, enabled, tags,
			agent_id, push_token, next_run_at, created_at, updated_at
		FROM monitors
		WHERE id = $1 AND tenant_id = $2
	`

	var monitor models.Monitor
	var tags []string

	err := r.db.QueryRowContext(ctx, query, monitorID, tenantID).Scan(
		&monitor.ID, &monitor.TenantID, &monitor.Name, &monitor.Type,
		&monitor.Config, &monitor.IntervalSeconds, &monitor.TimeoutSeconds,
		&monitor.AlertPolicyID, &monitor.Enabled,
		pq.Array(&tags), &monitor.AgentID, &monitor.PushToken, &monitor.NextRunAt, &monitor.CreatedAt, &monitor.UpdatedAt,
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
	whereClause := "WHERE tenant_id = $1"
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
			agent_id, push_token, next_run_at, created_at, updated_at
		FROM monitors
		%s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIndex, argIndex+1)

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
			pq.Array(&tags), &monitor.AgentID, &monitor.PushToken, &monitor.NextRunAt, &monitor.CreatedAt, &monitor.UpdatedAt,
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
		WHERE id = $%d AND tenant_id = $%d
		RETURNING id, tenant_id, name, type, config,
			interval_seconds, timeout_seconds, alert_policy_id, enabled, tags,
			agent_id, push_token, next_run_at, created_at, updated_at
	`, setClause, whereArgIndex, whereArgIndex+1)

	var tags []string

	err := r.db.QueryRowContext(ctx, query, args...).Scan(
		&monitor.ID, &monitor.TenantID, &monitor.Name, &monitor.Type,
		&monitor.Config, &monitor.IntervalSeconds, &monitor.TimeoutSeconds,
		&monitor.AlertPolicyID, &monitor.Enabled,
		pq.Array(&tags), &monitor.AgentID, &monitor.PushToken, &monitor.NextRunAt, &monitor.CreatedAt, &monitor.UpdatedAt,
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

// Delete removes a monitor from the database
func (r *PostgresRepository) Delete(ctx context.Context, tenantID, monitorID uuid.UUID) error {
	query := `DELETE FROM monitors WHERE id = $1 AND tenant_id = $2`
	result, err := r.db.ExecContext(ctx, query, monitorID, tenantID)
	if err != nil {
		return fmt.Errorf("failed to delete monitor: %w", err)
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
	query := `SELECT COUNT(*) FROM monitors WHERE id = ANY($1) AND tenant_id = $2`
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

// GetMemberIDs retrieves all member IDs for a group monitor
func (r *PostgresRepository) GetMemberIDs(ctx context.Context, groupID uuid.UUID) ([]uuid.UUID, error) {
	query := `
		SELECT monitor_id
		FROM monitor_groups
		WHERE group_id = $1
		ORDER BY created_at
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

// BulkAttachAlertPolicy inserts (monitor, policy) rows into monitor_alert_policies for
// monitors that don't already have the link. It also seeds the legacy monitors.alert_policy_id
// column on rows where it is currently NULL. Returns the IDs of monitors actually changed.
//
// Caller is expected to have already verified that all monitorIDs belong to tenantID.
// The tenant filter is repeated in the SQL for defense-in-depth.
func (r *PostgresRepository) BulkAttachAlertPolicy(
	ctx context.Context,
	tenantID uuid.UUID,
	monitorIDs []uuid.UUID,
	policyID uuid.UUID,
) ([]uuid.UUID, error) {
	if len(monitorIDs) == 0 {
		return nil, nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Insert links for monitors that don't already have one.
	// RETURNING tells us which rows were actually inserted.
	insertQuery := `
		INSERT INTO monitor_alert_policies (monitor_id, alert_policy_id, created_at)
		SELECT m.id, $2, NOW()
		FROM monitors m
		WHERE m.id = ANY($1) AND m.tenant_id = $3
		ON CONFLICT (monitor_id, alert_policy_id) DO NOTHING
		RETURNING monitor_id
	`
	rows, err := tx.QueryContext(ctx, insertQuery, pq.Array(monitorIDs), policyID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to attach alert policy: %w", err)
	}
	defer rows.Close()

	var changed []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan changed monitor: %w", err)
		}
		changed = append(changed, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating changed monitors: %w", err)
	}

	// Keep the legacy monitors.alert_policy_id mirror in sync: seed it on rows
	// where it is currently NULL.
	if _, err := tx.ExecContext(ctx, `
		UPDATE monitors
		SET alert_policy_id = $2
		WHERE id = ANY($1) AND tenant_id = $3 AND alert_policy_id IS NULL
	`, pq.Array(monitorIDs), policyID, tenantID); err != nil {
		return nil, fmt.Errorf("failed to seed legacy alert_policy_id: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit bulk attach: %w", err)
	}
	return changed, nil
}

// BulkDetachAlertPolicy removes (monitor, policy) rows from monitor_alert_policies for the
// given monitors. It also keeps the legacy monitors.alert_policy_id mirror in sync:
// on any monitor where alert_policy_id equals the detached policy, replace it with the
// remaining "first" policy from the join table (or NULL).
// Returns the IDs of monitors actually changed.
func (r *PostgresRepository) BulkDetachAlertPolicy(
	ctx context.Context,
	tenantID uuid.UUID,
	monitorIDs []uuid.UUID,
	policyID uuid.UUID,
) ([]uuid.UUID, error) {
	if len(monitorIDs) == 0 {
		return nil, nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	deleteQuery := `
		DELETE FROM monitor_alert_policies map
		USING monitors m
		WHERE map.monitor_id = m.id
		  AND m.tenant_id = $3
		  AND map.monitor_id = ANY($1)
		  AND map.alert_policy_id = $2
		RETURNING map.monitor_id
	`
	rows, err := tx.QueryContext(ctx, deleteQuery, pq.Array(monitorIDs), policyID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to detach alert policy: %w", err)
	}
	defer rows.Close()

	var changed []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan changed monitor: %w", err)
		}
		changed = append(changed, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating changed monitors: %w", err)
	}

	// Sync legacy alert_policy_id where it matched the detached policy.
	// Pick the oldest remaining policy from the join table, or NULL if none.
	if _, err := tx.ExecContext(ctx, `
		UPDATE monitors m
		SET alert_policy_id = (
			SELECT map2.alert_policy_id
			FROM monitor_alert_policies map2
			WHERE map2.monitor_id = m.id
			ORDER BY map2.created_at ASC
			LIMIT 1
		)
		WHERE m.id = ANY($1) AND m.tenant_id = $3 AND m.alert_policy_id = $2
	`, pq.Array(monitorIDs), policyID, tenantID); err != nil {
		return nil, fmt.Errorf("failed to sync legacy alert_policy_id after detach: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit bulk detach: %w", err)
	}
	return changed, nil
}

// Ensure PostgresRepository implements Repository
var _ Repository = (*PostgresRepository)(nil)
