package alertpolicies

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/db"
)

// Service handles alert policy business logic
type Service struct {
	db *db.Client
}

// NewService creates a new alert policy service
func NewService(db *db.Client) *Service {
	return &Service{db: db}
}

// CreateAlertPolicy creates a new alert policy
func (s *Service) CreateAlertPolicy(ctx context.Context, tenantID uuid.UUID, req *models.CreateAlertPolicyRequest) (*models.AlertPolicy, error) {
	policyID := uuid.New()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	query := `
		INSERT INTO alert_policies (
			id, tenant_id, name, description, failure_threshold,
			failure_window_seconds, email_subject_template, email_body_template,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW())
		RETURNING id, tenant_id, name, description, failure_threshold,
			failure_window_seconds, email_subject_template, email_body_template,
			created_at, updated_at
	`

	var policy models.AlertPolicy
	err = tx.QueryRowContext(ctx, query,
		policyID, tenantID, req.Name, req.Description,
		req.FailureThreshold, req.FailureWindowSeconds,
		normalizeOptionalTemplatePointer(req.EmailSubjectTemplate),
		normalizeOptionalTemplatePointer(req.EmailBodyTemplate),
	).Scan(
		&policy.ID, &policy.TenantID, &policy.Name, &policy.Description,
		&policy.FailureThreshold, &policy.FailureWindowSeconds,
		&policy.EmailSubjectTemplate, &policy.EmailBodyTemplate,
		&policy.CreatedAt, &policy.UpdatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to create alert policy: %w", err)
	}

	channelIDs, err := parseChannelIDs(req.ChannelIDs)
	if err != nil {
		return nil, err
	}

	if len(channelIDs) > 0 {
		if err := verifyChannelOwnership(ctx, tx, tenantID, channelIDs); err != nil {
			return nil, err
		}
		if err := insertPolicyChannels(ctx, tx, policy.ID, channelIDs); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit alert policy: %w", err)
	}

	policy.ChannelIDs = channelIDs
	return &policy, nil
}

// GetAlertPolicy retrieves an alert policy by ID (tenant-scoped)
func (s *Service) GetAlertPolicy(ctx context.Context, tenantID, policyID uuid.UUID) (*models.AlertPolicy, error) {
	query := `
		SELECT ap.id, ap.tenant_id, ap.name, ap.description, ap.failure_threshold,
			ap.failure_window_seconds, ap.email_subject_template, ap.email_body_template,
			ap.created_at, ap.updated_at,
			COALESCE(array_agg(apc.channel_id) FILTER (WHERE apc.channel_id IS NOT NULL), '{}') AS channel_ids
		FROM alert_policies ap
		LEFT JOIN alert_policy_channels apc ON ap.id = apc.alert_policy_id
		WHERE ap.id = $1 AND ap.tenant_id = $2
		GROUP BY ap.id
	`

	var policy models.AlertPolicy
	var channelIDs []uuid.UUID
	err := s.db.QueryRowContext(ctx, query, policyID, tenantID).Scan(
		&policy.ID, &policy.TenantID, &policy.Name, &policy.Description,
		&policy.FailureThreshold, &policy.FailureWindowSeconds,
		&policy.EmailSubjectTemplate, &policy.EmailBodyTemplate,
		&policy.CreatedAt, &policy.UpdatedAt, pq.Array(&channelIDs),
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("alert policy not found")
		}
		return nil, fmt.Errorf("failed to get alert policy: %w", err)
	}

	policy.ChannelIDs = channelIDs
	return &policy, nil
}

// ListAlertPolicies lists alert policies with pagination
func (s *Service) ListAlertPolicies(ctx context.Context, tenantID uuid.UUID, page, pageSize int) (*models.AlertPolicyListResponse, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	offset := (page - 1) * pageSize

	// Count total
	countQuery := `SELECT COUNT(*) FROM alert_policies WHERE tenant_id = $1`
	var total int
	err := s.db.QueryRowContext(ctx, countQuery, tenantID).Scan(&total)
	if err != nil {
		return nil, fmt.Errorf("failed to count alert policies: %w", err)
	}

	// Get policies
	query := `
		SELECT ap.id, ap.tenant_id, ap.name, ap.description, ap.failure_threshold,
			ap.failure_window_seconds, ap.email_subject_template, ap.email_body_template,
			ap.created_at, ap.updated_at,
			COALESCE(array_agg(apc.channel_id) FILTER (WHERE apc.channel_id IS NOT NULL), '{}') AS channel_ids
		FROM alert_policies ap
		LEFT JOIN alert_policy_channels apc ON ap.id = apc.alert_policy_id
		WHERE ap.tenant_id = $1
		GROUP BY ap.id
		ORDER BY ap.created_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := s.db.QueryContext(ctx, query, tenantID, pageSize, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list alert policies: %w", err)
	}
	defer rows.Close()

	var policies []models.AlertPolicy
	for rows.Next() {
		var policy models.AlertPolicy
		var channelIDs []uuid.UUID
		err := rows.Scan(
			&policy.ID, &policy.TenantID, &policy.Name, &policy.Description,
			&policy.FailureThreshold, &policy.FailureWindowSeconds,
			&policy.EmailSubjectTemplate, &policy.EmailBodyTemplate,
			&policy.CreatedAt, &policy.UpdatedAt, pq.Array(&channelIDs),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan alert policy: %w", err)
		}
		policy.ChannelIDs = channelIDs
		policies = append(policies, policy)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating alert policies: %w", err)
	}

	return &models.AlertPolicyListResponse{
		Items:    policies,
		Page:     page,
		PageSize: pageSize,
		Total:    total,
	}, nil
}

// UpdateAlertPolicy updates an alert policy (partial update)
func (s *Service) UpdateAlertPolicy(ctx context.Context, tenantID, policyID uuid.UUID, req *models.UpdateAlertPolicyRequest) (*models.AlertPolicy, error) {
	// Build update query dynamically
	setParts := []string{}
	args := []interface{}{}
	argIndex := 1

	if req.Name != nil {
		setParts = append(setParts, fmt.Sprintf("name = $%d", argIndex))
		args = append(args, *req.Name)
		argIndex++
	}

	if req.Description != nil {
		setParts = append(setParts, fmt.Sprintf("description = $%d", argIndex))
		args = append(args, *req.Description)
		argIndex++
	}

	if req.FailureThreshold != nil {
		setParts = append(setParts, fmt.Sprintf("failure_threshold = $%d", argIndex))
		args = append(args, *req.FailureThreshold)
		argIndex++
	}

	if req.FailureWindowSeconds != nil {
		setParts = append(setParts, fmt.Sprintf("failure_window_seconds = $%d", argIndex))
		args = append(args, *req.FailureWindowSeconds)
		argIndex++
	}

	if req.EmailSubjectTemplate != nil {
		setParts = append(setParts, fmt.Sprintf("email_subject_template = $%d", argIndex))
		args = append(args, normalizeOptionalTemplate(*req.EmailSubjectTemplate))
		argIndex++
	}

	if req.EmailBodyTemplate != nil {
		setParts = append(setParts, fmt.Sprintf("email_body_template = $%d", argIndex))
		args = append(args, normalizeOptionalTemplate(*req.EmailBodyTemplate))
		argIndex++
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var policy models.AlertPolicy
	if len(setParts) > 0 {
		setParts = append(setParts, fmt.Sprintf("updated_at = NOW()"))

		// Add WHERE clause
		whereArgIndex := argIndex
		args = append(args, policyID, tenantID)

		setClause := ""
		for i, part := range setParts {
			if i > 0 {
				setClause += ", "
			}
			setClause += part
		}

		query := fmt.Sprintf(`
			UPDATE alert_policies
			SET %s
			WHERE id = $%d AND tenant_id = $%d
			RETURNING id, tenant_id, name, description, failure_threshold,
				failure_window_seconds, email_subject_template, email_body_template,
				created_at, updated_at
		`, setClause, whereArgIndex, whereArgIndex+1)

		err := tx.QueryRowContext(ctx, query, args...).Scan(
			&policy.ID, &policy.TenantID, &policy.Name, &policy.Description,
			&policy.FailureThreshold, &policy.FailureWindowSeconds,
			&policy.EmailSubjectTemplate, &policy.EmailBodyTemplate,
			&policy.CreatedAt, &policy.UpdatedAt,
		)

		if err != nil {
			if err == sql.ErrNoRows {
				return nil, fmt.Errorf("alert policy not found")
			}
			return nil, fmt.Errorf("failed to update alert policy: %w", err)
		}
	} else {
		existing, err := s.GetAlertPolicy(ctx, tenantID, policyID)
		if err != nil {
			return nil, err
		}
		policy = *existing
	}

	if req.ChannelIDs != nil {
		channelIDs, err := parseChannelIDs(*req.ChannelIDs)
		if err != nil {
			return nil, err
		}
		if err := verifyChannelOwnership(ctx, tx, tenantID, channelIDs); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM alert_policy_channels WHERE alert_policy_id = $1`, policyID); err != nil {
			return nil, fmt.Errorf("failed to clear alert policy channels: %w", err)
		}
		if len(channelIDs) > 0 {
			if err := insertPolicyChannels(ctx, tx, policyID, channelIDs); err != nil {
				return nil, err
			}
		}
		policy.ChannelIDs = channelIDs
	} else if policy.ChannelIDs == nil {
		channelIDs, err := getPolicyChannelIDs(ctx, tx, policyID)
		if err != nil {
			return nil, err
		}
		policy.ChannelIDs = channelIDs
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit alert policy update: %w", err)
	}

	return &policy, nil
}

func normalizeOptionalTemplate(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}

func normalizeOptionalTemplatePointer(value *string) interface{} {
	if value == nil {
		return nil
	}
	return normalizeOptionalTemplate(*value)
}

// DeleteAlertPolicy deletes an alert policy
func (s *Service) DeleteAlertPolicy(ctx context.Context, tenantID, policyID uuid.UUID) error {
	query := `DELETE FROM alert_policies WHERE id = $1 AND tenant_id = $2`
	result, err := s.db.ExecContext(ctx, query, policyID, tenantID)
	if err != nil {
		return fmt.Errorf("failed to delete alert policy: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("alert policy not found")
	}

	return nil
}

func parseChannelIDs(ids []string) ([]uuid.UUID, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	unique := make(map[uuid.UUID]struct{}, len(ids))
	result := make([]uuid.UUID, 0, len(ids))
	for _, raw := range ids {
		if raw == "" {
			continue
		}
		parsed, err := uuid.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid channel_id: %w", err)
		}
		if _, exists := unique[parsed]; exists {
			continue
		}
		unique[parsed] = struct{}{}
		result = append(result, parsed)
	}
	return result, nil
}

func verifyChannelOwnership(ctx context.Context, querier db.Querier, tenantID uuid.UUID, channelIDs []uuid.UUID) error {
	if len(channelIDs) == 0 {
		return nil
	}

	query := `
		SELECT COUNT(*)
		FROM alert_channels
		WHERE tenant_id = $1 AND id = ANY($2)
	`
	var count int
	if err := querier.QueryRowContext(ctx, query, tenantID, pq.Array(channelIDs)).Scan(&count); err != nil {
		return fmt.Errorf("failed to verify alert channels: %w", err)
	}
	if count != len(channelIDs) {
		return fmt.Errorf("one or more alert channels not found")
	}
	return nil
}

func insertPolicyChannels(ctx context.Context, exec db.Executer, policyID uuid.UUID, channelIDs []uuid.UUID) error {
	if len(channelIDs) == 0 {
		return nil
	}

	query := `
		INSERT INTO alert_policy_channels (alert_policy_id, channel_id, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (alert_policy_id, channel_id) DO NOTHING
	`

	for _, channelID := range channelIDs {
		if _, err := exec.ExecContext(ctx, query, policyID, channelID); err != nil {
			return fmt.Errorf("failed to add alert policy channel: %w", err)
		}
	}

	return nil
}

func getPolicyChannelIDs(ctx context.Context, querier db.Querier, policyID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := querier.QueryContext(ctx, `SELECT channel_id FROM alert_policy_channels WHERE alert_policy_id = $1`, policyID)
	if err != nil {
		return nil, fmt.Errorf("failed to get alert policy channels: %w", err)
	}
	defer rows.Close()

	var channelIDs []uuid.UUID
	for rows.Next() {
		var channelID uuid.UUID
		if err := rows.Scan(&channelID); err != nil {
			return nil, fmt.Errorf("failed to scan alert policy channel: %w", err)
		}
		channelIDs = append(channelIDs, channelID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating alert policy channels: %w", err)
	}
	return channelIDs, nil
}
