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
// Targeting a group monitor covers all of its members. A window is a one-off
// (starts_at..ends_at) or repeats weekly (recurrence); either way what covers
// monitors is its occurrences (docs/state-semantics.md S-M5).
type MaintenanceWindow struct {
	ID          uuid.UUID `json:"id"`
	TenantID    uuid.UUID `json:"tenant_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	// StartsAt/EndsAt are a one-off's span as it actually covers: after an
	// edit or end-now they are its occurrence's times, not the times sent
	// (a start that has passed never moves; ending stores the moment it
	// ended). For a repeating window they are the first occurrence of its
	// current schedule; read NextOccurrence and the occurrence list for the
	// rest.
	StartsAt   time.Time                     `json:"starts_at"`
	EndsAt     time.Time                     `json:"ends_at"`
	MonitorIDs []uuid.UUID                   `json:"monitor_ids"`
	Monitors   []MaintenanceWindowMonitorRef `json:"monitors,omitempty"`
	// Status comes from the occurrences: active while one is in progress,
	// upcoming while one that is not skipped is ahead, past otherwise.
	Status MaintenanceWindowStatus `json:"status"`
	// Recurrence is set for a repeating window.
	Recurrence *MaintenanceRecurrence `json:"recurrence,omitempty"`
	// NextOccurrence is the occurrence in progress, else the next one that
	// is not skipped; absent when nothing is left.
	NextOccurrence *MaintenanceOccurrence `json:"next_occurrence,omitempty"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
}

// Recurrence frequencies. Weekly on every day is daily.
const (
	MaintenanceRecurrenceNone   = "none"
	MaintenanceRecurrenceWeekly = "weekly"
)

// MaintenanceRecurrence repeats a window weekly: on Weekdays at StartTime
// local time in Timezone, for DurationMinutes, until the local date Until.
type MaintenanceRecurrence struct {
	// Frequency is "weekly", or "none" to make a repeating window a one-off
	// again (update only).
	Frequency string `json:"frequency"`
	// Weekdays are 0 = Sunday ... 6 = Saturday.
	Weekdays []int `json:"weekdays,omitempty"`
	// StartTime is the local start, 24-hour "HH:MM".
	StartTime string `json:"start_time,omitempty"`
	// DurationMinutes is each occurrence's elapsed length.
	DurationMinutes int `json:"duration_minutes,omitempty"`
	// Timezone is the IANA zone Weekdays and StartTime are read in.
	Timezone string `json:"timezone,omitempty"`
	// Until (YYYY-MM-DD) is the last local date an occurrence may start on.
	Until string `json:"until,omitempty"`
}

// Occurrence statuses.
const (
	MaintenanceOccurrenceActive   = "active"
	MaintenanceOccurrenceUpcoming = "upcoming"
	MaintenanceOccurrencePast     = "past"
	MaintenanceOccurrenceSkipped  = "skipped"
)

// MaintenanceOccurrence is one concrete maintenance span of a window.
type MaintenanceOccurrence struct {
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
	// Status is active, upcoming, past or skipped.
	Status string `json:"status"`
}

// MaintenanceOccurrenceListResponse is a paginated list of a window's
// occurrences.
type MaintenanceOccurrenceListResponse struct {
	Items    []MaintenanceOccurrence `json:"items"`
	Page     int                     `json:"page"`
	PageSize int                     `json:"page_size"`
	Total    int                     `json:"total"`
}

// PreviewMaintenanceRecurrenceRequest asks for a recurrence's next
// occurrences without saving anything.
type PreviewMaintenanceRecurrenceRequest struct {
	Recurrence MaintenanceRecurrence `json:"recurrence"`
	// Count is how many occurrences to return (default 5, max 20).
	Count int `json:"count,omitempty"`
}

// PreviewMaintenanceRecurrenceResponse lists the occurrences that end after
// now.
type PreviewMaintenanceRecurrenceResponse struct {
	Occurrences []MaintenanceOccurrence `json:"occurrences"`
}

// CreateMaintenanceWindowRequest is the payload for creating a window: a
// one-off (starts_at, ends_at) or a repeating one (recurrence, no times).
type CreateMaintenanceWindowRequest struct {
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	StartsAt    time.Time              `json:"starts_at"`
	EndsAt      time.Time              `json:"ends_at"`
	MonitorIDs  []string               `json:"monitor_ids"`
	Recurrence  *MaintenanceRecurrence `json:"recurrence,omitempty"`
}

// UpdateMaintenanceWindowRequest is the payload for partially updating a
// window. Recurrence, when sent, replaces the whole rule. Schedule and
// monitor changes apply from the moment they are saved: what an occurrence
// already covered never changes.
type UpdateMaintenanceWindowRequest struct {
	Title       *string                `json:"title,omitempty"`
	Description *string                `json:"description,omitempty"`
	StartsAt    *time.Time             `json:"starts_at,omitempty"`
	EndsAt      *time.Time             `json:"ends_at,omitempty"`
	MonitorIDs  *[]string              `json:"monitor_ids,omitempty"`
	Recurrence  *MaintenanceRecurrence `json:"recurrence,omitempty"`
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
