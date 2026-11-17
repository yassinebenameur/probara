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
	ID                 uuid.UUID `json:"id"`
	Name               string    `json:"name"`
	DataRetentionDays  int       `json:"data_retention_days"`
	DashboardGroupTags []string  `json:"dashboard_group_tags"`
	// Role is the caller's membership role in this tenant ("admin" for
	// superadmins); only set on list responses.
	Role      string    `json:"role,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TenantListResponse represents a list of tenants.
type TenantListResponse struct {
	Items []Tenant `json:"items"`
}

// TenantSettings represents editable tenant-level settings.
type TenantSettings struct {
	DataRetentionDays  int      `json:"data_retention_days"`
	DashboardGroupTags []string `json:"dashboard_group_tags"`
}

// UpdateTenantSettingsRequest represents a partial tenant settings update.
type UpdateTenantSettingsRequest struct {
	DataRetentionDays  *int      `json:"data_retention_days,omitempty"`
	DashboardGroupTags *[]string `json:"dashboard_group_tags,omitempty"`
}

// CreateWorkspaceRequest creates a workspace (superadmins only).
type CreateWorkspaceRequest struct {
	Name string `json:"name"`
}

// RenameWorkspaceRequest renames a workspace (superadmins only).
type RenameWorkspaceRequest struct {
	Name string `json:"name"`
}

// DeleteWorkspaceRequest confirms a workspace delete by repeating its name.
type DeleteWorkspaceRequest struct {
	ConfirmName string `json:"confirm_name"`
}

// DeleteWorkspaceResponse reports a deleted workspace. It is gone at once;
// its data is purged in the background.
type DeleteWorkspaceResponse struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	DeletedAt time.Time `json:"deleted_at"`
}

// WorkspaceOverview is one live workspace on the superadmin Workspaces page.
type WorkspaceOverview struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	MemberCount  int       `json:"member_count"`
	MonitorCount int       `json:"monitor_count"`
	// Protected workspaces (the default one, the SSO provisioning one)
	// cannot be deleted.
	Protected bool      `json:"protected"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// WorkspaceOverviewResponse lists the live workspaces with their counts.
type WorkspaceOverviewResponse struct {
	Items []WorkspaceOverview `json:"items"`
}
