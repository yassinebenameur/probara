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
	// Kind distinguishes an availability outage ("availability") from a latency
	// degradation ("latency_anomaly").
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
	Page      int
	PageSize  int
}
