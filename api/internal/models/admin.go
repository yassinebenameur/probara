package models

import (
	"time"

	"github.com/google/uuid"
)

// AdminUser represents an admin user.
type AdminUser struct {
	ID           uuid.UUID          `json:"id"`
	Username     string             `json:"username"`
	Email        *string            `json:"email,omitempty"`
	PlatformRole string             `json:"platform_role,omitempty"`
	AuthMethod   string             `json:"auth_method,omitempty"`
	Memberships  []TenantMembership `json:"memberships,omitempty"`
	CreatedAt    time.Time          `json:"created_at"`
	UpdatedAt    time.Time          `json:"updated_at"`
	LastLoginAt  *time.Time         `json:"last_login_at,omitempty"`
	DisabledAt   *time.Time         `json:"disabled_at,omitempty"`
	// LockedUntil is set while password sign-in is locked after repeated
	// failures; FailedLoginCount is the current failure streak. Both appear
	// only on the user-management reads (/users).
	LockedUntil      *time.Time `json:"locked_until,omitempty"`
	FailedLoginCount int        `json:"failed_login_count,omitempty"`
}

// TenantMembership represents a user's role within one tenant.
type TenantMembership struct {
	TenantID   uuid.UUID `json:"tenant_id"`
	TenantName string    `json:"tenant_name,omitempty"`
	Role       string    `json:"role"`
}

// AuthContextResponse describes the effective identity of the current request,
// for either credential type. The frontend uses it to gate UI affordances.
type AuthContextResponse struct {
	ActorType    string  `json:"actor_type"`
	AdminID      *string `json:"admin_id,omitempty"`
	APIKeyID     *string `json:"api_key_id,omitempty"`
	TenantID     *string `json:"tenant_id,omitempty"`
	PlatformRole *string `json:"platform_role,omitempty"`
	Role         *string `json:"role,omitempty"`
	Scope        *string `json:"scope,omitempty"`
	CanWrite     bool    `json:"can_write"`
	// StatusPageBaseURL is the deployment's public status-page origin
	// (STATUS_PAGE_BASE_URL), omitted when unset. Deployment configuration,
	// not identity: it rides here because every UI session already asks.
	StatusPageBaseURL string `json:"status_page_base_url,omitempty"`
}

// AdminLoginRequest represents a login request.
type AdminLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// AdminAuthResponse represents the authenticated admin user info.
type AdminAuthResponse struct {
	User AdminUser `json:"user"`
}

// AdminBootstrapStatusResponse indicates whether at least one active admin
// exists, and whether creating the first one needs the install's setup code
// (BOOTSTRAP_SETUP_CODE). The code itself is never returned.
type AdminBootstrapStatusResponse struct {
	HasAdminUsers     bool `json:"has_admin_users"`
	SetupCodeRequired bool `json:"setup_code_required"`
}

// AdminUserListResponse represents a paginated list of admin users.
type AdminUserListResponse struct {
	Items    []AdminUser `json:"items"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
	Total    int         `json:"total"`
}

// MembershipInput assigns a tenant role when creating/updating a user.
type MembershipInput struct {
	TenantID uuid.UUID `json:"tenant_id"`
	Role     string    `json:"role"`
}

// CreateAdminUserRequest represents a request to create an admin user.
// Password nil/empty means an OIDC-only user (requires email so the IdP
// identity can be linked on first login).
type CreateAdminUserRequest struct {
	Username     string            `json:"username"`
	Email        *string           `json:"email,omitempty"`
	Password     *string           `json:"password,omitempty"`
	PlatformRole string            `json:"platform_role,omitempty"`
	Memberships  []MembershipInput `json:"memberships,omitempty"`
}

// UpdateAdminUserRequest represents a partial admin user update request.
// Memberships, when present, replaces the user's full membership set.
type UpdateAdminUserRequest struct {
	Username     *string            `json:"username,omitempty"`
	Email        *string            `json:"email,omitempty"`
	Password     *string            `json:"password,omitempty"`
	PlatformRole *string            `json:"platform_role,omitempty"`
	Memberships  *[]MembershipInput `json:"memberships,omitempty"`
}
