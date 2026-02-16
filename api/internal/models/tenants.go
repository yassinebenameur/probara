package models

import (
	"time"

	"github.com/google/uuid"
)

// Tenant represents a tenant.
type Tenant struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TenantListResponse represents a list of tenants.
type TenantListResponse struct {
	Items []Tenant `json:"items"`
}
