package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// IncidentState represents the lifecycle state of an incident.
type IncidentState string

const (
	IncidentStateInvestigating IncidentState = "investigating"
	IncidentStateIdentified    IncidentState = "identified"
	IncidentStateMonitoring    IncidentState = "monitoring"
	IncidentStateResolved      IncidentState = "resolved"
)

// IncidentTimelineEntryType represents the type of timeline entry.
type IncidentTimelineEntryType string

const (
	IncidentTimelineEntryTypeSystem       IncidentTimelineEntryType = "system"
	IncidentTimelineEntryTypeInternalNote IncidentTimelineEntryType = "internal_note"
	IncidentTimelineEntryTypePublicUpdate IncidentTimelineEntryType = "public_update"
)

// Incident represents the core incident record.
type Incident struct {
	ID                uuid.UUID     `json:"id"`
	TenantID          uuid.UUID     `json:"tenant_id"`
	Title             string        `json:"title"`
	Summary           string        `json:"summary"`
	State             IncidentState `json:"state"`
	ResolvedAt        *time.Time    `json:"resolved_at,omitempty"`
	IsAutoCreated     bool          `json:"is_auto_created"`
	AutoMonitorID     *uuid.UUID    `json:"auto_monitor_id,omitempty"`
	AutoAlertPolicyID *uuid.UUID    `json:"auto_alert_policy_id,omitempty"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
}

// IncidentTimelineEntry represents a single timeline event for an incident.
type IncidentTimelineEntry struct {
	ID         uuid.UUID                 `json:"id"`
	TenantID   uuid.UUID                 `json:"tenant_id"`
	IncidentID uuid.UUID                 `json:"incident_id"`
	EntryType  IncidentTimelineEntryType `json:"entry_type"`
	Message    string                    `json:"message"`
	Metadata   json.RawMessage           `json:"metadata,omitempty"`
	CreatedAt  time.Time                 `json:"created_at"`
}

// IncidentDetail represents an incident with its timeline.
type IncidentDetail struct {
	Incident
	Timeline []IncidentTimelineEntry `json:"timeline,omitempty"`
}

// IncidentListResponse represents a paginated list of incidents.
type IncidentListResponse struct {
	Items    []Incident `json:"items"`
	Page     int        `json:"page"`
	PageSize int        `json:"page_size"`
	Total    int        `json:"total"`
}

// CreateIncidentRequest represents a request to create an incident.
type CreateIncidentRequest struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

// UpdateIncidentRequest represents a partial incident update.
type UpdateIncidentRequest struct {
	Title   *string `json:"title,omitempty"`
	Summary *string `json:"summary,omitempty"`
}

// TransitionIncidentStateRequest represents a request to transition incident state.
type TransitionIncidentStateRequest struct {
	State IncidentState `json:"state"`
}

// CreateIncidentTimelineEntryRequest represents a request to append an incident timeline entry.
type CreateIncidentTimelineEntryRequest struct {
	EntryType IncidentTimelineEntryType `json:"entry_type"`
	Message   string                    `json:"message"`
	Metadata  map[string]interface{}    `json:"metadata,omitempty"`
}
