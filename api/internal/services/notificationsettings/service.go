// Package notificationsettings manages the workspace default routing,
// reminder cadence, and auto-incident toggle (spec §4.2, §4.6).
package notificationsettings

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	shareddb "github.com/yassinebenameur/probara/shared/db"
)

// ChannelAssignment describes a channel in the workspace default routing list.
type ChannelAssignment struct {
	ChannelID    uuid.UUID `json:"channel_id"`
	ChannelName  string    `json:"channel_name,omitempty"`
	ChannelType  string    `json:"channel_type,omitempty"`
	DelaySeconds int       `json:"delay_seconds"`
}

// Settings is the full workspace notification settings returned to the caller.
type Settings struct {
	DefaultChannels      []ChannelAssignment `json:"default_channels"`
	AlertReminderSeconds int                 `json:"alert_reminder_seconds"`
	AutoCreateIncident   bool                `json:"auto_create_incident"`
}

// UpdateRequest carries the fields to update; nil pointer fields are left unchanged.
type UpdateRequest struct {
	DefaultChannels      []ChannelAssignment `json:"default_channels"`
	AlertReminderSeconds *int                `json:"alert_reminder_seconds,omitempty"`
	AutoCreateIncident   *bool               `json:"auto_create_incident,omitempty"`
}

// Service provides workspace notification settings CRUD.
type Service struct{ db *shareddb.Client }

// NewService creates a new Service backed by the given DB client.
func NewService(db *shareddb.Client) *Service { return &Service{db: db} }

// Get loads the current workspace notification settings for the given tenant.
func (s *Service) Get(ctx context.Context, tenantID uuid.UUID) (*Settings, error) {
	var settings Settings
	if err := s.db.QueryRowContext(ctx, `
		SELECT alert_reminder_seconds, auto_create_incident FROM tenants WHERE id = $1
	`, tenantID).Scan(&settings.AlertReminderSeconds, &settings.AutoCreateIncident); err != nil {
		return nil, fmt.Errorf("load tenant alert settings: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT tdc.channel_id, ac.name, ac.type, tdc.delay_seconds
		FROM tenant_default_channels tdc
		JOIN alert_channels ac ON ac.id = tdc.channel_id
		WHERE tdc.tenant_id = $1
		ORDER BY tdc.position, tdc.channel_id
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("load default channels: %w", err)
	}
	defer rows.Close()
	settings.DefaultChannels = []ChannelAssignment{}
	for rows.Next() {
		var a ChannelAssignment
		if err := rows.Scan(&a.ChannelID, &a.ChannelName, &a.ChannelType, &a.DelaySeconds); err != nil {
			return nil, fmt.Errorf("scan default channel: %w", err)
		}
		settings.DefaultChannels = append(settings.DefaultChannels, a)
	}
	return &settings, rows.Err()
}

// Update applies partial updates to the workspace notification settings.
// When DefaultChannels is non-nil (including an empty slice), the entire
// default-channel list is replaced atomically.
func (s *Service) Update(ctx context.Context, tenantID uuid.UUID, req UpdateRequest) (*Settings, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin settings transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if req.DefaultChannels != nil {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM tenant_default_channels WHERE tenant_id = $1`, tenantID); err != nil {
			return nil, fmt.Errorf("clear default channels: %w", err)
		}
		for i, a := range req.DefaultChannels {
			if a.DelaySeconds < 0 {
				return nil, fmt.Errorf("delay_seconds must be >= 0")
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO tenant_default_channels (tenant_id, channel_id, delay_seconds, position)
				SELECT $1, $2, $3, $4
				WHERE EXISTS (SELECT 1 FROM alert_channels WHERE id = $2 AND tenant_id = $1)
			`, tenantID, a.ChannelID, a.DelaySeconds, i); err != nil {
				return nil, fmt.Errorf("insert default channel: %w", err)
			}
		}
	}
	if req.AlertReminderSeconds != nil || req.AutoCreateIncident != nil {
		if _, err := tx.ExecContext(ctx, `
			UPDATE tenants SET
				alert_reminder_seconds = COALESCE($1, alert_reminder_seconds),
				auto_create_incident = COALESCE($2, auto_create_incident),
				updated_at = NOW()
			WHERE id = $3
		`, req.AlertReminderSeconds, req.AutoCreateIncident, tenantID); err != nil {
			return nil, fmt.Errorf("update tenant alert settings: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit settings transaction: %w", err)
	}
	return s.Get(ctx, tenantID)
}
