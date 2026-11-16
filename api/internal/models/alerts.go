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
	Status         AlertStatus `json:"status"`
	TriggeredAt    time.Time   `json:"triggered_at"`
	AcknowledgedAt *time.Time  `json:"acknowledged_at,omitempty"`
	ResolvedAt     *time.Time  `json:"resolved_at,omitempty"`
	FailureCount   int         `json:"failure_count"`
	LastError      *string     `json:"last_error,omitempty"`
	// Kind distinguishes an availability outage ("availability") from the
	// orthogonal alert kinds: "latency_anomaly", "host_metric", "mesh_edge",
	// "tls_expiry" (certificate inside its expiry window, endpoint up),
	// "domain_expiry" and "location_offline" (notifications.Kind* lists them).
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
	// RelapseCount is how many times the alert reopened because its subject
	// failed again within the workspace's reopen cooldown after recovering
	// (0 = never); ReopenedAt is the latest reopen. TriggeredAt stays the
	// first trigger.
	RelapseCount int        `json:"relapse_count"`
	ReopenedAt   *time.Time `json:"reopened_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// AlertRelapse is one reopen of a relapsed alert: the recovery it
// interrupted and when the alert reopened.
type AlertRelapse struct {
	Number      int       `json:"number"`
	RecoveredAt time.Time `json:"recovered_at"`
	ReopenedAt  time.Time `json:"reopened_at"`
}

// AlertWithDetails includes related entity names for display
type AlertWithDetails struct {
	Alert
	MonitorName          *string `json:"monitor_name,omitempty"`
	RootCauseMonitorName *string `json:"root_cause_monitor_name,omitempty"`
	SourceLocationName   *string `json:"source_location_name,omitempty"`
	TargetLocationName   *string `json:"target_location_name,omitempty"`
	// SuppressionReason is set while the alerter is deliberately sending no
	// notification for this open alert: "dependency" when an upstream
	// dependency is down (or recovered less than the grace period ago) and
	// dependency suppression is on for the monitor; "location_offline" for a
	// mesh edge whose location stopped reporting, whose own location_offline
	// alert speaks for it. Computed with the same SQL predicates the alerter
	// dispatches with (shared/alertrouting).
	SuppressionReason *string `json:"suppression_reason,omitempty"`
	// ImpactedCount is how many open downstream alerts this alert's monitor is
	// the suppressed root cause of — the blast radius its notification lists.
	ImpactedCount int `json:"impacted_count,omitempty"`
	// Triage is the alert's automatic AI triage (read-only). Absent when
	// the alert has no triage row, and always absent on SSE events.
	Triage *AlertTriage `json:"triage,omitempty"`
	// AcknowledgedByUserID / AcknowledgedBy name the user who acknowledged
	// (with AcknowledgedAt, when). Absent while not acknowledged, and for an
	// acknowledgement made with an API key.
	AcknowledgedByUserID *uuid.UUID `json:"acknowledged_by_user_id,omitempty"`
	AcknowledgedBy       *string    `json:"acknowledged_by,omitempty"`
	// SnoozedUntil is set while the open alert is snoozed: until then it
	// sends no reminder and no further escalation. Absent once the snooze
	// ended, as are SnoozedAt (when the running snooze began) and
	// SnoozedByUserID / SnoozedBy (who set it; absent for an API key).
	SnoozedUntil    *time.Time `json:"snoozed_until,omitempty"`
	SnoozedAt       *time.Time `json:"snoozed_at,omitempty"`
	SnoozedByUserID *uuid.UUID `json:"snoozed_by_user_id,omitempty"`
	SnoozedBy       *string    `json:"snoozed_by,omitempty"`
	// Relapses is the alert's relapse history, oldest first. Only GET
	// /alerts/{id} reports it, and only for an alert that relapsed.
	Relapses []AlertRelapse `json:"relapses,omitempty"`
}

// SnoozeAlertRequest is the body of POST /alerts/{id}/snooze: exactly one of
// Until or DurationMinutes.
type SnoozeAlertRequest struct {
	Until           *time.Time `json:"until,omitempty"`
	DurationMinutes *int       `json:"duration_minutes,omitempty"`
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
