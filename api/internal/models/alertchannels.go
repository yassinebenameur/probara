package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// AlertChannelType represents the type of alert channel
type AlertChannelType string

const (
	AlertChannelTypeTeams AlertChannelType = "teams"
	AlertChannelTypeEmail AlertChannelType = "email"
)

// AlertChannel represents a notification channel
type AlertChannel struct {
	ID        uuid.UUID        `json:"id"`
	TenantID  uuid.UUID        `json:"tenant_id"`
	Name      string           `json:"name"`
	Type      AlertChannelType `json:"type"`
	Config    json.RawMessage  `json:"config"`
	IsActive  bool             `json:"is_active"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
}

// CreateAlertChannelRequest represents a request to create an alert channel
type CreateAlertChannelRequest struct {
	Name     string           `json:"name"`
	Type     AlertChannelType `json:"type"`
	Config   json.RawMessage  `json:"config"`
	IsActive *bool            `json:"is_active,omitempty"`
}

// UpdateAlertChannelRequest represents a request to update an alert channel
type UpdateAlertChannelRequest struct {
	Name     *string         `json:"name,omitempty"`
	Config   json.RawMessage `json:"config,omitempty"`
	IsActive *bool           `json:"is_active,omitempty"`
}

// AlertChannelListResponse represents a paginated list of alert channels
type AlertChannelListResponse struct {
	Items    []AlertChannel `json:"items"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
	Total    int            `json:"total"`
}
