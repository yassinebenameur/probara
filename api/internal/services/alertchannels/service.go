package alertchannels

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/notifications"
)

// Service handles alert channel business logic
type Service struct {
	db *db.Client
}

// NewService creates a new alert channel service
func NewService(db *db.Client) *Service {
	return &Service{db: db}
}

// CreateAlertChannel creates a new alert channel
func (s *Service) CreateAlertChannel(ctx context.Context, tenantID uuid.UUID, req *models.CreateAlertChannelRequest) (*models.AlertChannel, error) {
	channelID := uuid.New()
	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	query := `
		INSERT INTO alert_channels (
			id, tenant_id, name, type, config, is_active, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
		RETURNING id, tenant_id, name, type, config, is_active, created_at, updated_at
	`

	var channel models.AlertChannel
	var configBytes []byte
	err := s.db.QueryRowContext(ctx, query,
		channelID, tenantID, req.Name, req.Type, req.Config, isActive,
	).Scan(
		&channel.ID, &channel.TenantID, &channel.Name, &channel.Type,
		&configBytes, &channel.IsActive, &channel.CreatedAt, &channel.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create alert channel: %w", err)
	}

	channel.Config = json.RawMessage(configBytes)
	return &channel, nil
}

// GetAlertChannel retrieves an alert channel by ID (tenant-scoped)
func (s *Service) GetAlertChannel(ctx context.Context, tenantID, channelID uuid.UUID) (*models.AlertChannel, error) {
	query := `
		SELECT id, tenant_id, name, type, config, is_active, created_at, updated_at
		FROM alert_channels
		WHERE id = $1 AND tenant_id = $2
	`

	var channel models.AlertChannel
	var configBytes []byte
	err := s.db.QueryRowContext(ctx, query, channelID, tenantID).Scan(
		&channel.ID, &channel.TenantID, &channel.Name, &channel.Type,
		&configBytes, &channel.IsActive, &channel.CreatedAt, &channel.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("alert channel not found")
		}
		return nil, fmt.Errorf("failed to get alert channel: %w", err)
	}

	channel.Config = json.RawMessage(configBytes)
	return &channel, nil
}

// ListAlertChannels lists alert channels with pagination
func (s *Service) ListAlertChannels(ctx context.Context, tenantID uuid.UUID, page, pageSize int) (*models.AlertChannelListResponse, error) {
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
	countQuery := `SELECT COUNT(*) FROM alert_channels WHERE tenant_id = $1`
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, tenantID).Scan(&total); err != nil {
		return nil, fmt.Errorf("failed to count alert channels: %w", err)
	}

	query := `
		SELECT id, tenant_id, name, type, config, is_active, created_at, updated_at
		FROM alert_channels
		WHERE tenant_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := s.db.QueryContext(ctx, query, tenantID, pageSize, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list alert channels: %w", err)
	}
	defer rows.Close()

	var channels []models.AlertChannel
	for rows.Next() {
		var channel models.AlertChannel
		var configBytes []byte
		if err := rows.Scan(
			&channel.ID, &channel.TenantID, &channel.Name, &channel.Type,
			&configBytes, &channel.IsActive, &channel.CreatedAt, &channel.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan alert channel: %w", err)
		}
		channel.Config = json.RawMessage(configBytes)
		channels = append(channels, channel)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating alert channels: %w", err)
	}

	return &models.AlertChannelListResponse{
		Items:    channels,
		Page:     page,
		PageSize: pageSize,
		Total:    total,
	}, nil
}

// UpdateAlertChannel updates an alert channel (partial update)
func (s *Service) UpdateAlertChannel(ctx context.Context, tenantID, channelID uuid.UUID, req *models.UpdateAlertChannelRequest) (*models.AlertChannel, error) {
	setParts := []string{}
	args := []interface{}{}
	argIndex := 1

	if req.Name != nil {
		setParts = append(setParts, fmt.Sprintf("name = $%d", argIndex))
		args = append(args, *req.Name)
		argIndex++
	}

	if len(req.Config) > 0 {
		setParts = append(setParts, fmt.Sprintf("config = $%d", argIndex))
		args = append(args, req.Config)
		argIndex++
	}

	if req.IsActive != nil {
		setParts = append(setParts, fmt.Sprintf("is_active = $%d", argIndex))
		args = append(args, *req.IsActive)
		argIndex++
	}

	if len(setParts) == 0 {
		return s.GetAlertChannel(ctx, tenantID, channelID)
	}

	setParts = append(setParts, "updated_at = NOW()")
	whereArgIndex := argIndex
	args = append(args, channelID, tenantID)

	setClause := ""
	for i, part := range setParts {
		if i > 0 {
			setClause += ", "
		}
		setClause += part
	}

	query := fmt.Sprintf(`
		UPDATE alert_channels
		SET %s
		WHERE id = $%d AND tenant_id = $%d
		RETURNING id, tenant_id, name, type, config, is_active, created_at, updated_at
	`, setClause, whereArgIndex, whereArgIndex+1)

	var channel models.AlertChannel
	var configBytes []byte
	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&channel.ID, &channel.TenantID, &channel.Name, &channel.Type,
		&configBytes, &channel.IsActive, &channel.CreatedAt, &channel.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("alert channel not found")
		}
		return nil, fmt.Errorf("failed to update alert channel: %w", err)
	}

	channel.Config = json.RawMessage(configBytes)
	return &channel, nil
}

// DeleteAlertChannel deletes an alert channel
func (s *Service) DeleteAlertChannel(ctx context.Context, tenantID, channelID uuid.UUID) error {
	query := `DELETE FROM alert_channels WHERE id = $1 AND tenant_id = $2`
	result, err := s.db.ExecContext(ctx, query, channelID, tenantID)
	if err != nil {
		return fmt.Errorf("failed to delete alert channel: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("alert channel not found")
	}
	return nil
}

// TestAlertChannel sends a test notification for the given channel
func (s *Service) TestAlertChannel(ctx context.Context, tenantID, channelID uuid.UUID) error {
	channel, err := s.GetAlertChannel(ctx, tenantID, channelID)
	if err != nil {
		return err
	}

	if channel.Type != models.AlertChannelTypeTeams {
		return fmt.Errorf("alert channel type not supported for tests")
	}

	config, err := notifications.ParseTeamsWebhookConfig(channel.Config)
	if err != nil {
		return err
	}

	message := notifications.TeamsMessage{
		Type:    "MessageCard",
		Context: "https://schema.org/extensions",
		Summary: "Probara alert channel test",
		Title:   "Probara alert channel test",
		Text:    fmt.Sprintf("Test notification for channel **%s** at %s.", channel.Name, time.Now().Format(time.RFC1123)),
	}

	return notifications.SendTeamsWebhook(ctx, config.WebhookURL, message)
}
