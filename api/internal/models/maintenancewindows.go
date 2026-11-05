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
	// Recurrence is the window's rule; frequency "none" is a one-off whose
	// single occurrence is starts_at..ends_at. For a repeating window
	// starts_at/ends_at are the template occurrence (time of day, first
	// date, duration), not a span that is itself in maintenance.
	Recurrence MaintenanceRecurrence `json:"recurrence"`
	// Occurrences are the materialized spans that end after now — the one
	// in progress first, if any — at most five. Past occurrences stay as
	// history but are not listed here.
	Occurrences []MaintenanceOccurrence `json:"occurrences"`
	CreatedAt   time.Time               `json:"created_at"`
	UpdatedAt   time.Time               `json:"updated_at"`
}

// MaintenanceRecurrence is a window's repeat rule (docs/state-semantics.md
// S-M5). Wall-clock frequencies run at starts_at's time of day in Timezone.
type MaintenanceRecurrence struct {
	// Frequency is none (one-off), daily, weekly, monthly or cron.
	Frequency string `json:"frequency"`
	// Interval repeats every N days/weeks/months (daily, weekly, monthly).
	Interval int `json:"interval,omitempty"`
	// Weekdays are the weekly days, 0 = Sunday ... 6 = Saturday (default:
	// starts_at's weekday).
	Weekdays []int `json:"weekdays,omitempty"`
	// MonthDay is the monthly day, 1-31 (default: starts_at's day); a day
	// past the end of a shorter month runs on its last day.
	MonthDay int `json:"month_day,omitempty"`
	// Cron is a standard 5-field expression (cron frequency only).
	Cron string `json:"cron,omitempty"`
	// Timezone is the IANA zone the rule's wall-clock times are read in
	// (default UTC).
	Timezone string `json:"timezone,omitempty"`
	// Until stops the series: no occurrence starts after it.
	Until *time.Time `json:"until,omitempty"`
}

// MaintenanceOccurrence is one concrete maintenance span.
type MaintenanceOccurrence struct {
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
}

// PreviewMaintenanceWindowRequest asks for a rule's next occurrences
// without saving anything.
type PreviewMaintenanceWindowRequest struct {
	StartsAt   time.Time              `json:"starts_at"`
	EndsAt     time.Time              `json:"ends_at"`
	Recurrence *MaintenanceRecurrence `json:"recurrence,omitempty"`
	// Count is how many occurrences to return (default 10, max 50).
	Count int `json:"count,omitempty"`
}

// PreviewMaintenanceWindowResponse lists the occurrences that end after now.
type PreviewMaintenanceWindowResponse struct {
	Recurrence  MaintenanceRecurrence   `json:"recurrence"`
	Occurrences []MaintenanceOccurrence `json:"occurrences"`
}

// CreateMaintenanceWindowRequest is the payload for creating a window.
type CreateMaintenanceWindowRequest struct {
	Title       string    `json:"title"`
	Description string    `json:"description"`
	StartsAt    time.Time `json:"starts_at"`
	EndsAt      time.Time `json:"ends_at"`
	MonitorIDs  []string  `json:"monitor_ids"`
	// Recurrence makes the window repeat; omitted means a one-off.
	Recurrence *MaintenanceRecurrence `json:"recurrence,omitempty"`
}

// UpdateMaintenanceWindowRequest is the payload for partially updating a window.
type UpdateMaintenanceWindowRequest struct {
	Title       *string    `json:"title,omitempty"`
	Description *string    `json:"description,omitempty"`
	StartsAt    *time.Time `json:"starts_at,omitempty"`
	EndsAt      *time.Time `json:"ends_at,omitempty"`
	MonitorIDs  *[]string  `json:"monitor_ids,omitempty"`
	// Recurrence, when sent, replaces the whole rule ({"frequency":"none"}
	// makes the window a one-off). Edits never change occurrences that
	// have ended.
	Recurrence *MaintenanceRecurrence `json:"recurrence,omitempty"`
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
