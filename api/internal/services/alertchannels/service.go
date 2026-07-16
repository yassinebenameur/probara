package alertchannels

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// Service handles alert channel business logic. It is the encryption boundary
// for sensitive plugin config fields: secrets are encrypted before INSERT/UPDATE
// and masked on every read so plaintext never leaves the process boundary.
type Service struct {
	db        *db.Client
	encryptor secrets.Encryptor
}

// NewService creates a new alert channel service.
func NewService(db *db.Client, encryptor secrets.Encryptor) *Service {
	if encryptor == nil {
		encryptor = secrets.NoOpEncryptor{}
	}
	return &Service{db: db, encryptor: encryptor}
}

// CreateAlertChannel creates a new alert channel, encrypting secret fields
// per the plugin's manifest before persisting.
func (s *Service) CreateAlertChannel(ctx context.Context, tenantID uuid.UUID, req *models.CreateAlertChannelRequest) (*models.AlertChannel, error) {
	manifest := manifestFor(req.Type)
	configMap, err := unmarshalConfig(req.Config)
	if err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	encryptedMap, err := secrets.EncryptConfig(s.encryptor, manifest, configMap)
	if err != nil {
		return nil, fmt.Errorf("encrypt config: %w", err)
	}
	encryptedRaw, err := marshalConfig(encryptedMap)
	if err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}

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
	err = s.db.QueryRowContext(
		ctx, query,
		channelID, tenantID, req.Name, req.Type, encryptedRaw, isActive,
	).Scan(
		&channel.ID, &channel.TenantID, &channel.Name,
		&channel.Type, &channel.Config, &channel.IsActive,
		&channel.CreatedAt, &channel.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create alert channel: %w", err)
	}
	if err := maskInPlace(&channel); err != nil {
		return nil, err
	}
	return &channel, nil
}

// GetAlertChannel fetches a single alert channel for the tenant with secret
// fields masked.
func (s *Service) GetAlertChannel(ctx context.Context, tenantID, channelID uuid.UUID) (*models.AlertChannel, error) {
	channel, err := s.fetchAlertChannelRaw(ctx, tenantID, channelID)
	if err != nil {
		return nil, err
	}
	if err := maskInPlace(channel); err != nil {
		return nil, err
	}
	return channel, nil
}

// fetchAlertChannelRaw returns the channel exactly as stored (ciphertext
// preserved). Used internally where decryption is the next step.
func (s *Service) fetchAlertChannelRaw(ctx context.Context, tenantID, channelID uuid.UUID) (*models.AlertChannel, error) {
	query := `
		SELECT id, tenant_id, name, type, config, is_active, created_at, updated_at
		FROM alert_channels
		WHERE id = $1 AND tenant_id = $2
	`

	var channel models.AlertChannel
	err := s.db.QueryRowContext(ctx, query, channelID, tenantID).Scan(
		&channel.ID, &channel.TenantID, &channel.Name,
		&channel.Type, &channel.Config, &channel.IsActive,
		&channel.CreatedAt, &channel.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("alert channel not found")
		}
		return nil, fmt.Errorf("failed to fetch alert channel: %w", err)
	}
	return &channel, nil
}

// ListAlertChannels returns a paginated list with secret fields masked.
func (s *Service) ListAlertChannels(ctx context.Context, tenantID uuid.UUID, page, pageSize int) (*models.AlertChannelListResponse, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	countQuery := `SELECT COUNT(*) FROM alert_channels WHERE tenant_id = $1`
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, tenantID).Scan(&total); err != nil {
		return nil, fmt.Errorf("failed to count alert channels: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tenant_id, name, type, config, is_active, created_at, updated_at
		FROM alert_channels
		WHERE tenant_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, tenantID, pageSize, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list alert channels: %w", err)
	}
	defer rows.Close()

	items := make([]models.AlertChannel, 0)
	for rows.Next() {
		var ch models.AlertChannel
		if err := rows.Scan(
			&ch.ID, &ch.TenantID, &ch.Name,
			&ch.Type, &ch.Config, &ch.IsActive,
			&ch.CreatedAt, &ch.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan alert channel: %w", err)
		}
		if err := maskInPlace(&ch); err != nil {
			return nil, err
		}
		items = append(items, ch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating alert channels: %w", err)
	}

	return &models.AlertChannelListResponse{
		Items:    items,
		Page:     page,
		PageSize: pageSize,
		Total:    total,
	}, nil
}

// UpdateAlertChannel updates an alert channel. When the incoming config omits
// or empties a secret field, the existing ciphertext is preserved.
func (s *Service) UpdateAlertChannel(ctx context.Context, tenantID, channelID uuid.UUID, req *models.UpdateAlertChannelRequest) (*models.AlertChannel, error) {
	existing, err := s.fetchAlertChannelRaw(ctx, tenantID, channelID)
	if err != nil {
		return nil, err
	}
	manifest := manifestFor(existing.Type)

	name := existing.Name
	if req.Name != nil {
		name = *req.Name
	}
	isActive := existing.IsActive
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	cfgRaw := existing.Config
	if len(req.Config) > 0 {
		incoming, err := unmarshalConfig(req.Config)
		if err != nil {
			return nil, fmt.Errorf("decode config: %w", err)
		}
		current, err := unmarshalConfig(existing.Config)
		if err != nil {
			return nil, fmt.Errorf("decode existing config: %w", err)
		}
		merged := secrets.MergePreserveSecrets(manifest, incoming, current)
		encrypted, err := secrets.EncryptConfig(s.encryptor, manifest, merged)
		if err != nil {
			return nil, fmt.Errorf("encrypt config: %w", err)
		}
		cfgRaw, err = marshalConfig(encrypted)
		if err != nil {
			return nil, fmt.Errorf("encode config: %w", err)
		}
	}

	query := `
		UPDATE alert_channels
		SET name = $1, config = $2, is_active = $3, updated_at = NOW()
		WHERE id = $4 AND tenant_id = $5
		RETURNING id, tenant_id, name, type, config, is_active, created_at, updated_at
	`
	var ch models.AlertChannel
	err = s.db.QueryRowContext(ctx, query, name, cfgRaw, isActive, channelID, tenantID).Scan(
		&ch.ID, &ch.TenantID, &ch.Name,
		&ch.Type, &ch.Config, &ch.IsActive,
		&ch.CreatedAt, &ch.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to update alert channel: %w", err)
	}
	if err := maskInPlace(&ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

// DeleteAlertChannel removes an alert channel.
func (s *Service) DeleteAlertChannel(ctx context.Context, tenantID, channelID uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM alert_channels WHERE id = $1 AND tenant_id = $2`, channelID, tenantID)
	if err != nil {
		return fmt.Errorf("failed to delete alert channel: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("alert channel not found")
	}
	return nil
}

// TestAlertChannel sends a synthetic test notification through the channel's
// plugin. Secrets are decrypted in-process and never returned to the caller.
func (s *Service) TestAlertChannel(ctx context.Context, tenantID, channelID uuid.UUID) error {
	channel, err := s.fetchAlertChannelRaw(ctx, tenantID, channelID)
	if err != nil {
		return err
	}

	p, ok := plugin.DefaultRegistry.Get(string(channel.Type))
	if !ok {
		return fmt.Errorf("alert channel type %q is not registered", channel.Type)
	}
	manifest := p.Manifest()
	if !manifest.HasCapability(plugin.CapabilityTestable) {
		return fmt.Errorf("alert channel type %q does not support test notifications", channel.Type)
	}

	cfgMap, err := unmarshalConfig(channel.Config)
	if err != nil {
		return fmt.Errorf("decode channel config: %w", err)
	}
	cfgMap, err = secrets.DecryptConfig(s.encryptor, manifest, cfgMap)
	if err != nil {
		return fmt.Errorf("decrypt channel config: %w", err)
	}

	now := time.Now()
	event := notifications.AlertEvent{
		Type:      "created",
		TenantID:  tenantID.String(),
		Timestamp: now,
		Alert: notifications.AlertDetails{
			ID:           "test-" + uuid.NewString(),
			MonitorName:  "Probara Test Notification",
			PolicyName:   channel.Name,
			Status:       "active",
			TriggeredAt:  now,
			FailureCount: 1,
		},
	}

	sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	return p.Send(sendCtx, plugin.DispatchRequest{
		Channel: plugin.ChannelRef{
			ID:     channel.ID.String(),
			Name:   channel.Name,
			Config: cfgMap,
		},
		Event:     event,
		EventType: "created",
		Attempt:   1,
	})
}

func manifestFor(t models.AlertChannelType) plugin.Manifest {
	p, ok := plugin.DefaultRegistry.Get(string(t))
	if !ok {
		return plugin.Manifest{}
	}
	return p.Manifest()
}

func unmarshalConfig(raw json.RawMessage) (map[string]any, error) {
	out := map[string]any{}
	if len(raw) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func marshalConfig(m map[string]any) (json.RawMessage, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}

func maskInPlace(channel *models.AlertChannel) error {
	manifest := manifestFor(channel.Type)
	cfgMap, err := unmarshalConfig(channel.Config)
	if err != nil {
		return fmt.Errorf("decode config for masking: %w", err)
	}
	masked := secrets.MaskConfig(manifest, cfgMap)
	raw, err := marshalConfig(masked)
	if err != nil {
		return fmt.Errorf("encode masked config: %w", err)
	}
	channel.Config = raw
	return nil
}
