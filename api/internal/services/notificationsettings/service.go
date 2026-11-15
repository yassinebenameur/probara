// Package notificationsettings manages the workspace default routing,
// reminder cadence, and auto-incident toggle (spec §4.2, §4.6).
package notificationsettings

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/alertrouting"
	shareddb "github.com/yassinebenameur/probara/shared/db"
)

// ErrChannelNotFound is returned by Update when a requested channel_id does
// not exist or does not belong to the tenant. The handler maps this to HTTP 400.
var ErrChannelNotFound = errors.New("channel not found")

// ErrInvalidSetting wraps an out-of-range setting value Update refuses before
// writing anything. The handler maps it to HTTP 400.
var ErrInvalidSetting = errors.New("invalid notification setting")

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

	// Latency anomaly detection (workspace-wide).
	LatencyAnomalyEnabled          bool    `json:"latency_anomaly_enabled"`
	LatencyBaselineWindowHours     int     `json:"latency_baseline_window_hours"`
	LatencyAnomalySensitivity      float64 `json:"latency_anomaly_sensitivity"`
	LatencyAnomalyMinBreachSeconds int     `json:"latency_anomaly_min_breach_seconds"`
	LatencyAnomalyMinDeltaPct      float64 `json:"latency_anomaly_min_delta_pct"`

	// Dependency-aware alerting (workspace default; monitors can override
	// with their own dependency_suppression). When enabled, a monitor whose
	// upstream dependency is down opens its alert but sends no notification;
	// the root cause's notification lists it instead. After the upstream
	// recovers the downstream stays quiet for the grace period, then pages if
	// still down.
	DependencySuppressionEnabled      bool `json:"dependency_suppression_enabled"`
	DependencySuppressionGraceSeconds int  `json:"dependency_suppression_grace_seconds"`

	// AlertReopenCooldownMinutes is relapse reopen (shared/alertrouting
	// relapse.go): when an alert the alerter resolved on its own fails again
	// within this many minutes, it reopens (relapse_count + 1) instead of a
	// new alert opening. 0 is off (the default); 1-1440 otherwise.
	AlertReopenCooldownMinutes int `json:"alert_reopen_cooldown_minutes"`
}

// MaxDependencySuppressionGraceSeconds bounds the grace period at one day: a
// longer hold would mean a genuinely broken downstream is never announced.
const MaxDependencySuppressionGraceSeconds = 86400

// UpdateRequest carries the fields to update; nil pointer fields are left unchanged.
type UpdateRequest struct {
	DefaultChannels      []ChannelAssignment `json:"default_channels"`
	AlertReminderSeconds *int                `json:"alert_reminder_seconds,omitempty"`
	AutoCreateIncident   *bool               `json:"auto_create_incident,omitempty"`

	LatencyAnomalyEnabled          *bool    `json:"latency_anomaly_enabled,omitempty"`
	LatencyBaselineWindowHours     *int     `json:"latency_baseline_window_hours,omitempty"`
	LatencyAnomalySensitivity      *float64 `json:"latency_anomaly_sensitivity,omitempty"`
	LatencyAnomalyMinBreachSeconds *int     `json:"latency_anomaly_min_breach_seconds,omitempty"`
	LatencyAnomalyMinDeltaPct      *float64 `json:"latency_anomaly_min_delta_pct,omitempty"`

	DependencySuppressionEnabled      *bool `json:"dependency_suppression_enabled,omitempty"`
	DependencySuppressionGraceSeconds *int  `json:"dependency_suppression_grace_seconds,omitempty"`

	// AlertReopenCooldownMinutes: 0 turns relapse reopen off, 1-1440 sets
	// the cooldown.
	AlertReopenCooldownMinutes *int `json:"alert_reopen_cooldown_minutes,omitempty"`
}

// Service provides workspace notification settings CRUD.
type Service struct{ db *shareddb.Client }

// NewService creates a new Service backed by the given DB client.
func NewService(db *shareddb.Client) *Service { return &Service{db: db} }

// Get loads the current workspace notification settings for the given tenant.
func (s *Service) Get(ctx context.Context, tenantID uuid.UUID) (*Settings, error) {
	var settings Settings
	if err := s.db.QueryRowContext(ctx, `
		SELECT alert_reminder_seconds, auto_create_incident,
			latency_anomaly_enabled, latency_baseline_window_hours, latency_anomaly_sensitivity,
			latency_anomaly_min_breach_seconds, latency_anomaly_min_delta_pct,
			dependency_suppression_enabled, dependency_suppression_grace_seconds,
			alert_reopen_cooldown_minutes
		FROM tenants WHERE id = $1
	`, tenantID).Scan(&settings.AlertReminderSeconds, &settings.AutoCreateIncident,
		&settings.LatencyAnomalyEnabled, &settings.LatencyBaselineWindowHours, &settings.LatencyAnomalySensitivity,
		&settings.LatencyAnomalyMinBreachSeconds, &settings.LatencyAnomalyMinDeltaPct,
		&settings.DependencySuppressionEnabled, &settings.DependencySuppressionGraceSeconds,
		&settings.AlertReopenCooldownMinutes); err != nil {
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
	if req.AlertReopenCooldownMinutes != nil &&
		(*req.AlertReopenCooldownMinutes < 0 || *req.AlertReopenCooldownMinutes > alertrouting.MaxReopenCooldownMinutes) {
		return nil, fmt.Errorf("%w: alert_reopen_cooldown_minutes must be between 0 (off) and %d",
			ErrInvalidSetting, alertrouting.MaxReopenCooldownMinutes)
	}

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
			result, err := tx.ExecContext(ctx, `
				INSERT INTO tenant_default_channels (tenant_id, channel_id, delay_seconds, position)
				SELECT $1, $2, $3, $4
				WHERE EXISTS (SELECT 1 FROM alert_channels WHERE id = $2 AND tenant_id = $1)
			`, tenantID, a.ChannelID, a.DelaySeconds, i)
			if err != nil {
				return nil, fmt.Errorf("insert default channel: %w", err)
			}
			n, err := result.RowsAffected()
			if err != nil {
				return nil, fmt.Errorf("check rows affected: %w", err)
			}
			if n == 0 {
				return nil, fmt.Errorf("channel %s not found for tenant: %w", a.ChannelID, ErrChannelNotFound)
			}
		}
	}
	if req.LatencyBaselineWindowHours != nil && *req.LatencyBaselineWindowHours <= 0 {
		return nil, fmt.Errorf("latency_baseline_window_hours must be greater than 0")
	}
	if req.LatencyAnomalySensitivity != nil && *req.LatencyAnomalySensitivity <= 0 {
		return nil, fmt.Errorf("latency_anomaly_sensitivity must be greater than 0")
	}
	if req.LatencyAnomalyMinBreachSeconds != nil && *req.LatencyAnomalyMinBreachSeconds < 0 {
		return nil, fmt.Errorf("latency_anomaly_min_breach_seconds must be >= 0")
	}
	if req.LatencyAnomalyMinDeltaPct != nil && *req.LatencyAnomalyMinDeltaPct < 0 {
		return nil, fmt.Errorf("latency_anomaly_min_delta_pct must be >= 0")
	}
	if req.DependencySuppressionGraceSeconds != nil &&
		(*req.DependencySuppressionGraceSeconds < 0 || *req.DependencySuppressionGraceSeconds > MaxDependencySuppressionGraceSeconds) {
		return nil, fmt.Errorf("dependency_suppression_grace_seconds must be between 0 and %d", MaxDependencySuppressionGraceSeconds)
	}

	if req.AlertReminderSeconds != nil || req.AutoCreateIncident != nil ||
		req.LatencyAnomalyEnabled != nil || req.LatencyBaselineWindowHours != nil ||
		req.LatencyAnomalySensitivity != nil || req.LatencyAnomalyMinBreachSeconds != nil ||
		req.LatencyAnomalyMinDeltaPct != nil ||
		req.DependencySuppressionEnabled != nil || req.DependencySuppressionGraceSeconds != nil ||
		req.AlertReopenCooldownMinutes != nil {
		if _, err := tx.ExecContext(ctx, `
			UPDATE tenants SET
				alert_reminder_seconds = COALESCE($1, alert_reminder_seconds),
				auto_create_incident = COALESCE($2, auto_create_incident),
				latency_anomaly_enabled = COALESCE($3, latency_anomaly_enabled),
				latency_baseline_window_hours = COALESCE($4, latency_baseline_window_hours),
				latency_anomaly_sensitivity = COALESCE($5, latency_anomaly_sensitivity),
				latency_anomaly_min_breach_seconds = COALESCE($6, latency_anomaly_min_breach_seconds),
				latency_anomaly_min_delta_pct = COALESCE($7, latency_anomaly_min_delta_pct),
				dependency_suppression_enabled = COALESCE($9, dependency_suppression_enabled),
				dependency_suppression_grace_seconds = COALESCE($10, dependency_suppression_grace_seconds),
				alert_reopen_cooldown_minutes = COALESCE($11, alert_reopen_cooldown_minutes),
				updated_at = NOW()
			WHERE id = $8
		`, req.AlertReminderSeconds, req.AutoCreateIncident,
			req.LatencyAnomalyEnabled, req.LatencyBaselineWindowHours, req.LatencyAnomalySensitivity,
			req.LatencyAnomalyMinBreachSeconds, req.LatencyAnomalyMinDeltaPct, tenantID,
			req.DependencySuppressionEnabled, req.DependencySuppressionGraceSeconds,
			req.AlertReopenCooldownMinutes); err != nil {
			return nil, fmt.Errorf("update tenant alert settings: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit settings transaction: %w", err)
	}
	return s.Get(ctx, tenantID)
}
