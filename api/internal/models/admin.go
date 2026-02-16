package models

import (
	"time"

	"github.com/google/uuid"
)

// AdminUser represents an admin user.
type AdminUser struct {
	ID          uuid.UUID  `json:"id"`
	Username    string     `json:"username"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	DisabledAt  *time.Time `json:"disabled_at,omitempty"`
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

// AdminBootstrapStatusResponse indicates whether at least one active admin exists.
type AdminBootstrapStatusResponse struct {
	HasAdminUsers bool `json:"has_admin_users"`
}

// AdminUserListResponse represents a paginated list of admin users.
type AdminUserListResponse struct {
	Items    []AdminUser `json:"items"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
	Total    int         `json:"total"`
}

// CreateAdminUserRequest represents a request to create an admin user.
type CreateAdminUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// UpdateAdminUserRequest represents a partial admin user update request.
type UpdateAdminUserRequest struct {
	Username *string `json:"username,omitempty"`
	Password *string `json:"password,omitempty"`
}
