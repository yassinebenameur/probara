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
	ID             uuid.UUID   `json:"id"`
	TenantID       uuid.UUID   `json:"tenant_id"`
	MonitorID      uuid.UUID   `json:"monitor_id"`
	AlertPolicyID  uuid.UUID   `json:"alert_policy_id"`
	Status         AlertStatus `json:"status"`
	TriggeredAt    time.Time   `json:"triggered_at"`
	AcknowledgedAt *time.Time  `json:"acknowledged_at,omitempty"`
	ResolvedAt     *time.Time  `json:"resolved_at,omitempty"`
	FailureCount   int         `json:"failure_count"`
	LastError      *string     `json:"last_error,omitempty"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

// AlertWithDetails includes related entity names for display
type AlertWithDetails struct {
	Alert
	MonitorName string `json:"monitor_name"`
	PolicyName  string `json:"policy_name"`
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
