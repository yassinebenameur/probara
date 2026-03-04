package models

import (
	"time"

	"github.com/google/uuid"
)

const (
	DataRetentionUnlimited = 0
	MinDataRetentionDays   = 30
	MaxDataRetentionDays   = 3650
)

// Tenant represents a tenant.
type Tenant struct {
	ID                uuid.UUID `json:"id"`
	Name              string    `json:"name"`
	DataRetentionDays int       `json:"data_retention_days"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// TenantListResponse represents a list of tenants.
type TenantListResponse struct {
	Items []Tenant `json:"items"`
}

// TenantSettings represents editable tenant-level settings.
type TenantSettings struct {
	DataRetentionDays int `json:"data_retention_days"`
}

// UpdateTenantSettingsRequest represents a partial tenant settings update.
type UpdateTenantSettingsRequest struct {
	DataRetentionDays *int `json:"data_retention_days,omitempty"`
}
