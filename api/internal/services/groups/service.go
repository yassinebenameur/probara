package groups

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/db"
)

var ErrGroupCycleDetected = errors.New("group nesting would create a cycle")

// Service handles group business logic
type Service struct {
	db db.DB
}

// NewService creates a new group service
func NewService(database db.DB) *Service {
	return &Service{db: database}
}

// AddMonitorsToGroup adds monitors to a group
func (s *Service) AddMonitorsToGroup(ctx context.Context, tenantID, groupID uuid.UUID, monitorIDs []uuid.UUID) error {
	// First verify the group exists and is of type 'group'
	group, err := s.getMonitor(ctx, tenantID, groupID)
	if err != nil {
		return err
	}
	if group.Type != models.MonitorTypeGroup {
		return fmt.Errorf("monitor is not a group")
	}

	// Verify all monitors exist and belong to the tenant.
	// For group members, reject operations that would create a cycle.
	for _, monitorID := range monitorIDs {
		monitor, err := s.getMonitor(ctx, tenantID, monitorID)
		if err != nil {
			return fmt.Errorf("monitor %s: %w", monitorID, err)
		}
		if monitor.Type == models.MonitorTypeGroup {
			cycle, err := s.wouldCreateCycle(ctx, tenantID, groupID, monitorID)
			if err != nil {
				return err
			}
			if cycle {
				return fmt.Errorf("%w: cannot add group %s to group %s", ErrGroupCycleDetected, monitorID, groupID)
			}
		}
	}

	// Insert into monitor_groups (ignore duplicates)
	for _, monitorID := range monitorIDs {
		query := `
			INSERT INTO monitor_groups (monitor_id, group_id, created_at)
			VALUES ($1, $2, NOW())
			ON CONFLICT (monitor_id, group_id) DO NOTHING
		`
		_, err := s.db.ExecContext(ctx, query, monitorID, groupID)
		if err != nil {
			return fmt.Errorf("failed to add monitor to group: %w", err)
		}
	}

	return nil
}

// RemoveMonitorsFromGroup removes monitors from a group
func (s *Service) RemoveMonitorsFromGroup(ctx context.Context, tenantID, groupID uuid.UUID, monitorIDs []uuid.UUID) error {
	// First verify the group exists and belongs to the tenant
	group, err := s.getMonitor(ctx, tenantID, groupID)
	if err != nil {
		return err
	}
	if group.Type != models.MonitorTypeGroup {
		return fmt.Errorf("monitor is not a group")
	}

	// Remove from monitor_groups
	for _, monitorID := range monitorIDs {
		query := `DELETE FROM monitor_groups WHERE monitor_id = $1 AND group_id = $2`
		_, err := s.db.ExecContext(ctx, query, monitorID, groupID)
		if err != nil {
			return fmt.Errorf("failed to remove monitor from group: %w", err)
		}
	}

	return nil
}

// GetGroupMembers retrieves all monitors in a group
func (s *Service) GetGroupMembers(ctx context.Context, tenantID, groupID uuid.UUID) ([]models.Monitor, error) {
	// First verify the group exists and belongs to the tenant
	group, err := s.getMonitor(ctx, tenantID, groupID)
	if err != nil {
		return nil, err
	}
	if group.Type != models.MonitorTypeGroup {
		return nil, fmt.Errorf("monitor is not a group")
	}

	query := `
		SELECT m.id, m.tenant_id, m.name, m.type, m.config,
			m.interval_seconds, m.timeout_seconds, m.alert_policy_id, m.enabled, m.tags,
			m.next_run_at, m.created_at, m.updated_at
		FROM monitors m
		INNER JOIN monitor_groups mg ON m.id = mg.monitor_id
		WHERE mg.group_id = $1 AND m.tenant_id = $2 AND m.deleted_at IS NULL
		ORDER BY m.name
	`

	rows, err := s.db.QueryContext(ctx, query, groupID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to get group members: %w", err)
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
			pq.Array(&tags), &monitor.NextRunAt, &monitor.CreatedAt, &monitor.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan monitor: %w", err)
		}

		monitor.Tags = tags
		monitors = append(monitors, monitor)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating monitors: %w", err)
	}

	return monitors, nil
}

// GetGroupLeafMembers retrieves all non-group members in a group recursively, deduplicated by monitor ID.
func (s *Service) GetGroupLeafMembers(ctx context.Context, tenantID, groupID uuid.UUID) ([]models.Monitor, error) {
	// First verify the group exists and belongs to the tenant
	group, err := s.getMonitor(ctx, tenantID, groupID)
	if err != nil {
		return nil, err
	}
	if group.Type != models.MonitorTypeGroup {
		return nil, fmt.Errorf("monitor is not a group")
	}

	query := `
		WITH RECURSIVE member_tree AS (
			SELECT mg.monitor_id
			FROM monitor_groups mg
			JOIN monitors m ON m.id = mg.monitor_id
			WHERE mg.group_id = $1 AND m.tenant_id = $2 AND m.deleted_at IS NULL
			UNION
			SELECT mg.monitor_id
			FROM member_tree mt
			JOIN monitors parent ON parent.id = mt.monitor_id AND parent.tenant_id = $2
			JOIN monitor_groups mg ON mg.group_id = parent.id
			JOIN monitors child ON child.id = mg.monitor_id AND child.tenant_id = $2
			WHERE parent.type = 'group'
			  AND parent.deleted_at IS NULL
			  AND child.deleted_at IS NULL
		)
		SELECT m.id, m.tenant_id, m.name, m.type, m.config,
			m.interval_seconds, m.timeout_seconds, m.alert_policy_id, m.enabled, m.tags,
			m.next_run_at, m.created_at, m.updated_at
		FROM monitors m
		JOIN member_tree mt ON mt.monitor_id = m.id
		WHERE m.tenant_id = $2 AND m.type <> 'group' AND m.deleted_at IS NULL
		ORDER BY m.name
	`

	rows, err := s.db.QueryContext(ctx, query, groupID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to get recursive group members: %w", err)
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
			pq.Array(&tags), &monitor.NextRunAt, &monitor.CreatedAt, &monitor.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan monitor: %w", err)
		}

		monitor.Tags = tags
		monitors = append(monitors, monitor)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating monitors: %w", err)
	}

	return monitors, nil
}

// GetMonitorGroups retrieves all groups a monitor belongs to
func (s *Service) GetMonitorGroups(ctx context.Context, tenantID, monitorID uuid.UUID) ([]models.Monitor, error) {
	// First verify the monitor exists and belongs to the tenant
	_, err := s.getMonitor(ctx, tenantID, monitorID)
	if err != nil {
		return nil, err
	}

	query := `
		SELECT m.id, m.tenant_id, m.name, m.type, m.config,
			m.interval_seconds, m.timeout_seconds, m.alert_policy_id, m.enabled, m.tags,
			m.next_run_at, m.created_at, m.updated_at
		FROM monitors m
		INNER JOIN monitor_groups mg ON m.id = mg.group_id
		WHERE mg.monitor_id = $1 AND m.tenant_id = $2 AND m.deleted_at IS NULL
		ORDER BY m.name
	`

	rows, err := s.db.QueryContext(ctx, query, monitorID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to get monitor groups: %w", err)
	}
	defer rows.Close()

	var groups []models.Monitor
	for rows.Next() {
		var group models.Monitor
		var tags []string

		err := rows.Scan(
			&group.ID, &group.TenantID, &group.Name, &group.Type,
			&group.Config, &group.IntervalSeconds, &group.TimeoutSeconds,
			&group.AlertPolicyID, &group.Enabled,
			pq.Array(&tags), &group.NextRunAt, &group.CreatedAt, &group.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan group: %w", err)
		}

		group.Tags = tags
		groups = append(groups, group)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating groups: %w", err)
	}

	return groups, nil
}

// GetGroupStatus calculates the aggregated status for a group
func (s *Service) GetGroupStatus(ctx context.Context, tenantID, groupID uuid.UUID) (string, error) {
	// Get all non-group member monitors recursively
	members, err := s.GetGroupLeafMembers(ctx, tenantID, groupID)
	if err != nil {
		return "", err
	}

	if len(members) == 0 {
		return "unknown", nil
	}

	// Query the latest check result for each member monitor
	successCount := 0
	failureCount := 0
	errorCount := 0

	for _, member := range members {
		query := `
			SELECT status
			FROM check_results
			WHERE monitor_id = $1 AND tenant_id = $2 AND result_source <> 'platform'
			ORDER BY created_at DESC
			LIMIT 1
		`

		var status string
		err := s.db.QueryRowContext(ctx, query, member.ID, tenantID).Scan(&status)
		if err != nil {
			if err == sql.ErrNoRows {
				// No results for this monitor yet, skip it
				continue
			}
			return "", fmt.Errorf("failed to get monitor status: %w", err)
		}

		switch status {
		case "success":
			successCount++
		case "failure":
			failureCount++
		case "error":
			errorCount++
		}
	}

	totalChecked := successCount + failureCount + errorCount

	if totalChecked == 0 {
		return "unknown", nil
	}

	// Apply status logic: all up = UP, all down = DOWN, otherwise = DEGRADED
	if successCount == totalChecked {
		return "success", nil
	}
	if failureCount+errorCount == totalChecked {
		return "failure", nil
	}
	return "degraded", nil
}

func (s *Service) wouldCreateCycle(ctx context.Context, tenantID, parentGroupID, candidateMemberID uuid.UUID) (bool, error) {
	if parentGroupID == candidateMemberID {
		return true, nil
	}

	candidate, err := s.getMonitor(ctx, tenantID, candidateMemberID)
	if err != nil {
		return false, fmt.Errorf("monitor %s: %w", candidateMemberID, err)
	}
	if candidate.Type != models.MonitorTypeGroup {
		return false, nil
	}

	query := `
		WITH RECURSIVE descendants AS (
			SELECT mg.monitor_id
			FROM monitor_groups mg
			JOIN monitors m ON m.id = mg.monitor_id
			WHERE mg.group_id = $1 AND m.tenant_id = $2 AND m.deleted_at IS NULL
			UNION
			SELECT mg.monitor_id
			FROM descendants d
			JOIN monitors parent ON parent.id = d.monitor_id AND parent.tenant_id = $2
			JOIN monitor_groups mg ON mg.group_id = parent.id
			JOIN monitors child ON child.id = mg.monitor_id AND child.tenant_id = $2
			WHERE parent.type = 'group'
			  AND parent.deleted_at IS NULL
			  AND child.deleted_at IS NULL
		)
		SELECT EXISTS (
			SELECT 1
			FROM descendants
			WHERE monitor_id = $3
		)
	`

	var exists bool
	if err := s.db.QueryRowContext(ctx, query, candidateMemberID, tenantID, parentGroupID).Scan(&exists); err != nil {
		return false, fmt.Errorf("failed to validate group cycle: %w", err)
	}
	return exists, nil
}

// getMonitor is a helper to retrieve a monitor by ID
func (s *Service) getMonitor(ctx context.Context, tenantID, monitorID uuid.UUID) (*models.Monitor, error) {
	query := `
		SELECT id, tenant_id, name, type, config,
			interval_seconds, timeout_seconds, alert_policy_id, enabled, tags,
			agent_id, next_run_at, created_at, updated_at
		FROM monitors
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`

	var monitor models.Monitor
	var tags []string

	err := s.db.QueryRowContext(ctx, query, monitorID, tenantID).Scan(
		&monitor.ID, &monitor.TenantID, &monitor.Name, &monitor.Type,
		&monitor.Config, &monitor.IntervalSeconds, &monitor.TimeoutSeconds,
		&monitor.AlertPolicyID, &monitor.Enabled,
		pq.Array(&tags), &monitor.AgentID, &monitor.NextRunAt, &monitor.CreatedAt, &monitor.UpdatedAt,
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
