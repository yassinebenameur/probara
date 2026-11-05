package models

import (
	"time"

	"github.com/google/uuid"
)

// AlertStatus represents the status of an alert
type AlertStatus string

const (
	AlertStatusActive       AlertStatus = "active"
	AlertStatusAcknowledged AlertStatus = "acknowledged"
	AlertStatusResolved     AlertStatus = "resolved"
)

// Alert represents an alert in the system
type Alert struct {
	ID       uuid.UUID `json:"id"`
	TenantID uuid.UUID `json:"tenant_id"`
	// MonitorID is nil for mesh_edge alerts, whose subject is a directed
	// location pair instead of a monitor.
	MonitorID      *uuid.UUID  `json:"monitor_id,omitempty"`
	AlertPolicyID  *uuid.UUID  `json:"alert_policy_id,omitempty"`
	Status         AlertStatus `json:"status"`
	TriggeredAt    time.Time   `json:"triggered_at"`
	AcknowledgedAt *time.Time  `json:"acknowledged_at,omitempty"`
	ResolvedAt     *time.Time  `json:"resolved_at,omitempty"`
	FailureCount   int         `json:"failure_count"`
	LastError      *string     `json:"last_error,omitempty"`
	// Kind distinguishes an availability outage ("availability") from the
	// orthogonal alert kinds: "latency_anomaly", "host_metric", "mesh_edge",
	// "tls_expiry" (certificate inside its expiry window, endpoint up) and
	// "domain_expiry" (domain registration inside its expiry window).
	Kind string `json:"kind"`
	// Latency-anomaly annotation: populated when Kind == "latency_anomaly".
	BaselineLatencyMs *float64 `json:"baseline_latency_ms,omitempty"`
	ObservedLatencyMs *float64 `json:"observed_latency_ms,omitempty"`
	AnomalyScore      *float64 `json:"anomaly_score,omitempty"`
	// Host-metric annotation: populated when Kind == "host_metric".
	MetricName     *string  `json:"metric_name,omitempty"`
	MetricValue    *float64 `json:"metric_value,omitempty"`
	ThresholdValue *float64 `json:"threshold_value,omitempty"`
	// Root-cause annotation: the upstream dependency that was down when this
	// alert fired (dependency-aware alerting).
	RootCauseMonitorID *uuid.UUID `json:"root_cause_monitor_id,omitempty"`
	RootCauseDownSince *time.Time `json:"root_cause_down_since,omitempty"`
	// Mesh-edge annotation: the directed location pair, populated when
	// Kind == "mesh_edge" (MonitorID is nil for these).
	SourceLocationID *uuid.UUID `json:"source_location_id,omitempty"`
	TargetLocationID *uuid.UUID `json:"target_location_id,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// AlertWithDetails includes related entity names for display
type AlertWithDetails struct {
	Alert
	MonitorName          *string `json:"monitor_name,omitempty"`
	PolicyName           *string `json:"policy_name,omitempty"`
	RootCauseMonitorName *string `json:"root_cause_monitor_name,omitempty"`
	SourceLocationName   *string `json:"source_location_name,omitempty"`
	TargetLocationName   *string `json:"target_location_name,omitempty"`
	// SuppressionReason is set while the alerter is deliberately sending no
	// notification for this open alert. Today the only value is "dependency":
	// an upstream dependency is down (or recovered less than the grace period
	// ago) and dependency suppression is on for the monitor. Computed with the
	// same SQL predicate the alerter dispatches with (shared/alertrouting).
	SuppressionReason *string `json:"suppression_reason,omitempty"`
	// ImpactedCount is how many open downstream alerts this alert's monitor is
	// the suppressed root cause of — the blast radius its notification lists.
	ImpactedCount int `json:"impacted_count,omitempty"`
	// Triage is the alert's automatic AI triage (read-only). Absent when
	// the alert has no triage row, and always absent on SSE events.
	Triage *AlertTriage `json:"triage,omitempty"`
	// AcknowledgedByUserID / AcknowledgedVia record who acknowledged the
	// alert and through which surface (ui, api, mcp, magic_link). Cleared
	// on unack.
	AcknowledgedByUserID *uuid.UUID `json:"acknowledged_by_user_id,omitempty"`
	AcknowledgedVia      *string    `json:"acknowledged_via,omitempty"`
	// UnacknowledgedAt is the last unacknowledge; escalation tiers and
	// reminders count from it.
	UnacknowledgedAt *time.Time `json:"unacknowledged_at,omitempty"`
	// SnoozedUntil mutes every tier, reminder and suspected-cause follow-up
	// while in the future; a past value is when the last snooze ended.
	SnoozedUntil    *time.Time `json:"snoozed_until,omitempty"`
	SnoozedByUserID *uuid.UUID `json:"snoozed_by_user_id,omitempty"`
	// MutedReason says why this open alert is not paging right now:
	// "snoozed", or "acknowledged" (the tenant stops paging on ack; routes
	// marked notify_after_ack still hear). Computed from the predicates the
	// alerter dispatches with (alertrouting.MutedReasonExpr).
	MutedReason *string `json:"muted_reason,omitempty"`
	// RelapseCount is how many times the alert reopened within the
	// workspace's alert_reopen_cooldown_seconds (each a new paging episode);
	// ReopenedAt is the last reopen.
	RelapseCount int        `json:"relapse_count"`
	ReopenedAt   *time.Time `json:"reopened_at,omitempty"`
	// ResolvedByUserID / ResolvedVia record a manual resolve (ui, api,
	// mcp); both stay empty when the alerter resolved it. A manually
	// resolved alert never reopens.
	ResolvedByUserID *uuid.UUID `json:"resolved_by_user_id,omitempty"`
	ResolvedVia      *string    `json:"resolved_via,omitempty"`
}

// AlertListResponse represents a paginated list of alerts
type AlertListResponse struct {
	Items    []AlertWithDetails `json:"items"`
	Page     int                `json:"page"`
	PageSize int                `json:"page_size"`
	Total    int                `json:"total"`
}

// AlertEvent represents an alert event for SSE streaming
type AlertEvent struct {
	Type  string           `json:"type"` // "created", "acknowledged", "resolved"
	Alert AlertWithDetails `json:"alert"`
}

// AlertListParams represents query parameters for listing alerts
type AlertListParams struct {
	Status    *AlertStatus
	MonitorID *uuid.UUID
	Since     *time.Time
	// Suppressed filters on whether the alerter is currently suppressing the
	// alert's notifications (see AlertWithDetails.SuppressionReason).
	Suppressed *bool
	// Query narrows to alerts whose subject (monitor name, or either mesh
	// location's name) contains it, case-insensitively, or whose ID it is.
	Query    string
	Page     int
	PageSize int
}
