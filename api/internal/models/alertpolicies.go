package models

import (
	"time"

	"github.com/google/uuid"
)

// AlertPolicy represents an alert policy
type AlertPolicy struct {
	ID                   uuid.UUID   `json:"id"`
	TenantID             uuid.UUID   `json:"tenant_id"`
	Name                 string      `json:"name"`
	Description          *string     `json:"description,omitempty"`
	FailureThreshold     int         `json:"failure_threshold"`
	FailureWindowSeconds int         `json:"failure_window_seconds"`
	CreateIncidentOnFire bool        `json:"create_incident_on_fire"`
	ChannelIDs           []uuid.UUID `json:"channel_ids,omitempty"`
	EmailSubjectTemplate *string     `json:"email_subject_template,omitempty"`
	EmailBodyTemplate    *string     `json:"email_body_template,omitempty"`
	CreatedAt            time.Time   `json:"created_at"`
	UpdatedAt            time.Time   `json:"updated_at"`
}

// CreateAlertPolicyRequest represents a request to create an alert policy
type CreateAlertPolicyRequest struct {
	Name                 string   `json:"name"`
	Description          *string  `json:"description,omitempty"`
	FailureThreshold     int      `json:"failure_threshold"`
	FailureWindowSeconds int      `json:"failure_window_seconds"`
	CreateIncidentOnFire *bool    `json:"create_incident_on_fire,omitempty"`
	ChannelIDs           []string `json:"channel_ids,omitempty"`
	EmailSubjectTemplate *string  `json:"email_subject_template,omitempty"`
	EmailBodyTemplate    *string  `json:"email_body_template,omitempty"`
}

// UpdateAlertPolicyRequest represents a request to update an alert policy
type UpdateAlertPolicyRequest struct {
	Name                 *string   `json:"name,omitempty"`
	Description          *string   `json:"description,omitempty"`
	FailureThreshold     *int      `json:"failure_threshold,omitempty"`
	FailureWindowSeconds *int      `json:"failure_window_seconds,omitempty"`
	CreateIncidentOnFire *bool     `json:"create_incident_on_fire,omitempty"`
	ChannelIDs           *[]string `json:"channel_ids,omitempty"`
	EmailSubjectTemplate *string   `json:"email_subject_template,omitempty"`
	EmailBodyTemplate    *string   `json:"email_body_template,omitempty"`
}

// AlertPolicyListResponse represents a paginated list of alert policies
type AlertPolicyListResponse struct {
	Items    []AlertPolicy `json:"items"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
	Total    int           `json:"total"`
}
