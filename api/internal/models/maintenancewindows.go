package models

import (
	"time"

	"github.com/google/uuid"
)

// MaintenanceWindowStatus is the derived lifecycle status of a window.
type MaintenanceWindowStatus string

const (
	MaintenanceWindowStatusActive   MaintenanceWindowStatus = "active"
	MaintenanceWindowStatusUpcoming MaintenanceWindowStatus = "upcoming"
	MaintenanceWindowStatusPast     MaintenanceWindowStatus = "past"
)

// MaintenanceWindowMonitorRef is a lightweight monitor reference for list display.
type MaintenanceWindowMonitorRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Type string    `json:"type"`
}

// MaintenanceWindow is a planned period during which alerts for the targeted
// monitors are suppressed. Checks keep running; only alerting is muted.
// Targeting a group monitor covers all of its members.
type MaintenanceWindow struct {
	ID          uuid.UUID                     `json:"id"`
	TenantID    uuid.UUID                     `json:"tenant_id"`
	Title       string                        `json:"title"`
	Description string                        `json:"description"`
	StartsAt    time.Time                     `json:"starts_at"`
	EndsAt      time.Time                     `json:"ends_at"`
	MonitorIDs  []uuid.UUID                   `json:"monitor_ids"`
	Monitors    []MaintenanceWindowMonitorRef `json:"monitors,omitempty"`
	Status      MaintenanceWindowStatus       `json:"status"`
	CreatedAt   time.Time                     `json:"created_at"`
	UpdatedAt   time.Time                     `json:"updated_at"`
}

// CreateMaintenanceWindowRequest is the payload for creating a window.
type CreateMaintenanceWindowRequest struct {
	Title       string    `json:"title"`
	Description string    `json:"description"`
	StartsAt    time.Time `json:"starts_at"`
	EndsAt      time.Time `json:"ends_at"`
	MonitorIDs  []string  `json:"monitor_ids"`
}

// UpdateMaintenanceWindowRequest is the payload for partially updating a window.
type UpdateMaintenanceWindowRequest struct {
	Title       *string    `json:"title,omitempty"`
	Description *string    `json:"description,omitempty"`
	StartsAt    *time.Time `json:"starts_at,omitempty"`
	EndsAt      *time.Time `json:"ends_at,omitempty"`
	MonitorIDs  *[]string  `json:"monitor_ids,omitempty"`
}

// MaintenanceWindowListResponse is a paginated list of maintenance windows.
type MaintenanceWindowListResponse struct {
	Items    []MaintenanceWindow `json:"items"`
	Page     int                 `json:"page"`
	PageSize int                 `json:"page_size"`
	Total    int                 `json:"total"`
}

// SnoozeMonitorRequest creates a single-monitor maintenance window from now
// until the given time. Exactly one of Until or DurationMinutes is required.
type SnoozeMonitorRequest struct {
	Until           *time.Time `json:"until,omitempty"`
	DurationMinutes *int       `json:"duration_minutes,omitempty"`
}
