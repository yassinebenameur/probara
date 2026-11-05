// Package notificationsettings manages the workspace default routing,
// reminder cadence, and auto-incident toggle (spec §4.2, §4.6).
package notificationsettings

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/monitorstate"
)

// ErrChannelNotFound is returned by Update when a requested channel_id does
// not exist or does not belong to the tenant. The handler maps this to HTTP 400.
var ErrChannelNotFound = errors.New("channel not found")

// ChannelAssignment describes a channel in the workspace default routing list.
type ChannelAssignment struct {
	ChannelID    uuid.UUID `json:"channel_id"`
	ChannelName  string    `json:"channel_name,omitempty"`
	ChannelType  string    `json:"channel_type,omitempty"`
	DelaySeconds int       `json:"delay_seconds"`
	// NotifyAfterAck keeps this route paging (tiers and reminders) while an
	// alert is acknowledged, when the workspace otherwise stops on ack.
	NotifyAfterAck bool `json:"notify_after_ack"`
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

	// Paging. AckStopsNotifications: acknowledging an alert stops its
	// unfired escalation tiers and reminders (routes marked
	// notify_after_ack excepted).
	AckStopsNotifications bool `json:"ack_stops_notifications"`

	// Offline locations (rule S-F6): alert when a check location that
	// reported before has been silent for LocationOfflineAfterSeconds.
	LocationOfflineAlertsEnabled bool `json:"location_offline_alerts_enabled"`
	LocationOfflineAfterSeconds  int  `json:"location_offline_after_seconds"`

	// Flap suppression: an availability alert the alerter auto-resolved less
	// than this many seconds ago is reopened (relapse_count+1) when its
	// monitor goes down again, instead of a new alert opening. 0 = off.
	AlertReopenCooldownSeconds int `json:"alert_reopen_cooldown_seconds"`
}

// MaxAlertReopenCooldownSeconds bounds the reopen cooldown at one day.
const MaxAlertReopenCooldownSeconds = 86400

// MaxLocationOfflineAfterSeconds bounds the offline threshold at one day; the
// floor is monitorstate.MinLocationOfflineAfterSeconds.
const MaxLocationOfflineAfterSeconds = 86400

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

	AckStopsNotifications *bool `json:"ack_stops_notifications,omitempty"`

	LocationOfflineAlertsEnabled *bool `json:"location_offline_alerts_enabled,omitempty"`
	LocationOfflineAfterSeconds  *int  `json:"location_offline_after_seconds,omitempty"`

	AlertReopenCooldownSeconds *int `json:"alert_reopen_cooldown_seconds,omitempty"`
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
			dependency_suppression_enabled, dependency_suppression_grace_seconds
		FROM tenants WHERE id = $1
	`, tenantID).Scan(&settings.AlertReminderSeconds, &settings.AutoCreateIncident,
		&settings.LatencyAnomalyEnabled, &settings.LatencyBaselineWindowHours, &settings.LatencyAnomalySensitivity,
		&settings.LatencyAnomalyMinBreachSeconds, &settings.LatencyAnomalyMinDeltaPct,
		&settings.DependencySuppressionEnabled, &settings.DependencySuppressionGraceSeconds); err != nil {
		return nil, fmt.Errorf("load tenant alert settings: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT ack_stops_notifications, location_offline_alerts_enabled, location_offline_after_seconds
		FROM tenants WHERE id = $1
	`, tenantID).Scan(&settings.AckStopsNotifications,
		&settings.LocationOfflineAlertsEnabled, &settings.LocationOfflineAfterSeconds); err != nil {
		return nil, fmt.Errorf("load tenant paging settings: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT alert_reopen_cooldown_seconds FROM tenants WHERE id = $1
	`, tenantID).Scan(&settings.AlertReopenCooldownSeconds); err != nil {
		return nil, fmt.Errorf("load tenant reopen cooldown: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT tdc.channel_id, ac.name, ac.type, tdc.delay_seconds, tdc.notify_after_ack
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
		if err := rows.Scan(&a.ChannelID, &a.ChannelName, &a.ChannelType, &a.DelaySeconds, &a.NotifyAfterAck); err != nil {
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
			result, err := tx.ExecContext(ctx, `
				INSERT INTO tenant_default_channels (tenant_id, channel_id, delay_seconds, position, notify_after_ack)
				SELECT $1, $2, $3, $4, $5
				WHERE EXISTS (SELECT 1 FROM alert_channels WHERE id = $2 AND tenant_id = $1)
			`, tenantID, a.ChannelID, a.DelaySeconds, i, a.NotifyAfterAck)
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
		req.DependencySuppressionEnabled != nil || req.DependencySuppressionGraceSeconds != nil {
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
				updated_at = NOW()
			WHERE id = $8
		`, req.AlertReminderSeconds, req.AutoCreateIncident,
			req.LatencyAnomalyEnabled, req.LatencyBaselineWindowHours, req.LatencyAnomalySensitivity,
			req.LatencyAnomalyMinBreachSeconds, req.LatencyAnomalyMinDeltaPct, tenantID,
			req.DependencySuppressionEnabled, req.DependencySuppressionGraceSeconds); err != nil {
			return nil, fmt.Errorf("update tenant alert settings: %w", err)
		}
	}
	if req.LocationOfflineAfterSeconds != nil &&
		(*req.LocationOfflineAfterSeconds < monitorstate.MinLocationOfflineAfterSeconds || *req.LocationOfflineAfterSeconds > MaxLocationOfflineAfterSeconds) {
		return nil, fmt.Errorf("location_offline_after_seconds must be between %d and %d",
			monitorstate.MinLocationOfflineAfterSeconds, MaxLocationOfflineAfterSeconds)
	}
	if req.AckStopsNotifications != nil || req.LocationOfflineAlertsEnabled != nil || req.LocationOfflineAfterSeconds != nil {
		if _, err := tx.ExecContext(ctx, `
			UPDATE tenants SET
				ack_stops_notifications = COALESCE($2, ack_stops_notifications),
				location_offline_alerts_enabled = COALESCE($3, location_offline_alerts_enabled),
				location_offline_after_seconds = COALESCE($4, location_offline_after_seconds),
				updated_at = NOW()
			WHERE id = $1
		`, tenantID, req.AckStopsNotifications, req.LocationOfflineAlertsEnabled, req.LocationOfflineAfterSeconds); err != nil {
			return nil, fmt.Errorf("update tenant paging settings: %w", err)
		}
	}
	if req.AlertReopenCooldownSeconds != nil {
		if *req.AlertReopenCooldownSeconds < 0 || *req.AlertReopenCooldownSeconds > MaxAlertReopenCooldownSeconds {
			return nil, fmt.Errorf("alert_reopen_cooldown_seconds must be between 0 and %d", MaxAlertReopenCooldownSeconds)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE tenants SET alert_reopen_cooldown_seconds = $2, updated_at = NOW() WHERE id = $1
		`, tenantID, *req.AlertReopenCooldownSeconds); err != nil {
			return nil, fmt.Errorf("update tenant reopen cooldown: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit settings transaction: %w", err)
	}
	return s.Get(ctx, tenantID)
}
