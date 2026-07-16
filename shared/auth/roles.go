package auth

// Platform roles on admin_users.platform_role.
const (
	PlatformRoleSuperadmin = "superadmin"
	PlatformRoleMember     = "member"
)

// Tenant roles on tenant_memberships.role.
const (
	RoleAdmin  = "admin"
	RoleEditor = "editor"
	RoleViewer = "viewer"
)

// API key scopes on api_keys.scope.
const (
	ScopeRead  = "read"
	ScopeWrite = "write"
)

// ValidTenantRole reports whether role is one of the fixed tenant roles.
func ValidTenantRole(role string) bool {
	return role == RoleAdmin || role == RoleEditor || role == RoleViewer
}

// ValidPlatformRole reports whether role is a valid platform role.
func ValidPlatformRole(role string) bool {
	return role == PlatformRoleSuperadmin || role == PlatformRoleMember
}

// ValidScope reports whether scope is a valid API key scope.
func ValidScope(scope string) bool {
	return scope == ScopeRead || scope == ScopeWrite
}

// RoleAllowsWrite reports whether a tenant role permits mutations.
func RoleAllowsWrite(role string) bool {
	return role == RoleAdmin || role == RoleEditor
}
