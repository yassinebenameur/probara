package contacts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

// ChannelOption is an alert channel that can serve as the personal SMS
// channel (its plugin advertises plugin.CapabilityDirectRecipient).
type ChannelOption struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Type     string    `json:"type"`
	IsActive bool      `json:"is_active"`
}

// PeopleSettings is the workspace's people-layer configuration.
type PeopleSettings struct {
	// PersonalSMSChannelID texts people directly: SMS contact verification
	// codes and tests, and escalation-policy pages. Null disables SMS contacts
	// in this workspace.
	PersonalSMSChannelID *uuid.UUID `json:"personal_sms_channel_id"`
	// SMSChannelOptions are the workspace's channels that can fill it.
	SMSChannelOptions []ChannelOption `json:"sms_channel_options"`
}

// UpdatePeopleSettingsRequest sets the personal SMS channel; null clears it.
type UpdatePeopleSettingsRequest struct {
	PersonalSMSChannelID *uuid.UUID `json:"personal_sms_channel_id"`
}

// textsPeople reports whether a channel of plugin m can be the personal SMS
// channel: it addresses one person (plugin.CapabilityDirectRecipient), and
// is not the email plugin, which takes direct recipients too (escalation
// pages to email contacts go through the platform mailer, no channel).
func textsPeople(m plugin.Manifest) bool {
	return m.HasCapability(plugin.CapabilityDirectRecipient) && m.Type != "email"
}

// directRecipientTypes lists the plugin types that can text one person.
func directRecipientTypes() []string {
	var out []string
	for _, m := range plugin.DefaultRegistry.All() {
		if textsPeople(m) {
			out = append(out, m.Type)
		}
	}
	return out
}

// PeopleSettings returns the workspace's people settings.
func (s *Service) PeopleSettings(ctx context.Context, tenantID uuid.UUID) (*PeopleSettings, error) {
	out := &PeopleSettings{SMSChannelOptions: []ChannelOption{}}
	err := s.db.QueryRowContext(ctx, `SELECT personal_sms_channel_id FROM tenants WHERE id = $1`, tenantID).
		Scan(&out.PersonalSMSChannelID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load people settings: %w", err)
	}
	types := directRecipientTypes()
	if len(types) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, type, is_active FROM alert_channels
		WHERE tenant_id = $1 AND type = ANY($2) ORDER BY name, id
	`, tenantID, pq.Array(types))
	if err != nil {
		return nil, fmt.Errorf("list SMS channels: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var o ChannelOption
		if err := rows.Scan(&o.ID, &o.Name, &o.Type, &o.IsActive); err != nil {
			return nil, err
		}
		out.SMSChannelOptions = append(out.SMSChannelOptions, o)
	}
	return out, rows.Err()
}

// UpdatePeopleSettings sets or clears the personal SMS channel.
func (s *Service) UpdatePeopleSettings(ctx context.Context, tenantID uuid.UUID, req UpdatePeopleSettingsRequest) (*PeopleSettings, error) {
	if id := req.PersonalSMSChannelID; id != nil {
		var typ string
		err := s.db.QueryRowContext(ctx, `SELECT type FROM alert_channels WHERE id = $1 AND tenant_id = $2`, *id, tenantID).Scan(&typ)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, invalid("personal_sms_channel_id: channel not found in this workspace")
		}
		if err != nil {
			return nil, fmt.Errorf("load channel: %w", err)
		}
		p, ok := plugin.DefaultRegistry.Get(typ)
		if !ok || !textsPeople(p.Manifest()) {
			return nil, invalid("personal_sms_channel_id: a %s channel cannot text a single person; choose an SMS (Twilio) channel", typ)
		}
	}
	res, err := s.db.ExecContext(ctx, `UPDATE tenants SET personal_sms_channel_id = $2, updated_at = NOW() WHERE id = $1`,
		tenantID, req.PersonalSMSChannelID)
	if err != nil {
		return nil, fmt.Errorf("update people settings: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.PeopleSettings(ctx, tenantID)
}
