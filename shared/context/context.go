package context

import "context"

// contextKey is a type-safe key for context values
type contextKey string

const (
	// RequestIDKey is the context key for request ID
	RequestIDKey contextKey = "request_id"
	// JobIDKey is the context key for job ID
	JobIDKey contextKey = "job_id"
	// TenantIDKey is the context key for tenant ID
	TenantIDKey contextKey = "tenant_id"
	// AdminIDKey is the context key for admin ID
	AdminIDKey contextKey = "admin_id"
	// RoleKey is the context key for the effective tenant role (admin/editor/viewer)
	RoleKey contextKey = "role"
	// PlatformRoleKey is the context key for the platform role (superadmin/member)
	PlatformRoleKey contextKey = "platform_role"
	// ActorTypeKey is the context key for the credential type (admin_user/api_key)
	ActorTypeKey contextKey = "actor_type"
	// APIKeyIDKey is the context key for the authenticated API key ID
	APIKeyIDKey contextKey = "api_key_id"
	// AuthScopeKey is the context key for the credential scope (read/write)
	AuthScopeKey contextKey = "auth_scope"
	// ViaKey marks a request dispatched on behalf of another channel (MCP)
	ViaKey contextKey = "via"
)

// ViaMCP is the ViaKey value of a REST request an MCP tool call dispatched.
const ViaMCP = "mcp"

// Actor types stored under ActorTypeKey.
const (
	ActorTypeAdminUser = "admin_user"
	ActorTypeAPIKey    = "api_key"
)

// WithRequestID adds a request ID to the context
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, RequestIDKey, requestID)
}

// GetRequestID retrieves the request ID from the context
func GetRequestID(ctx context.Context) (string, bool) {
	requestID, ok := ctx.Value(RequestIDKey).(string)
	return requestID, ok
}

// WithJobID adds a job ID to the context
func WithJobID(ctx context.Context, jobID string) context.Context {
	return context.WithValue(ctx, JobIDKey, jobID)
}

// GetJobID retrieves the job ID from the context
func GetJobID(ctx context.Context) (string, bool) {
	jobID, ok := ctx.Value(JobIDKey).(string)
	return jobID, ok
}

// WithTenantID adds a tenant ID to the context
func WithTenantID(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, TenantIDKey, tenantID)
}

// GetTenantID retrieves the tenant ID from the context
func GetTenantID(ctx context.Context) (string, bool) {
	tenantID, ok := ctx.Value(TenantIDKey).(string)
	return tenantID, ok
}

// WithAdminID adds an admin ID to the context
func WithAdminID(ctx context.Context, adminID string) context.Context {
	return context.WithValue(ctx, AdminIDKey, adminID)
}

// GetAdminID retrieves the admin ID from the context
func GetAdminID(ctx context.Context) (string, bool) {
	adminID, ok := ctx.Value(AdminIDKey).(string)
	return adminID, ok
}

// IsAdmin returns true if an admin ID exists in context
func IsAdmin(ctx context.Context) bool {
	_, ok := GetAdminID(ctx)
	return ok
}

// WithRole adds the effective tenant role to the context
func WithRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, RoleKey, role)
}

// GetRole retrieves the effective tenant role from the context
func GetRole(ctx context.Context) (string, bool) {
	role, ok := ctx.Value(RoleKey).(string)
	return role, ok
}

// WithPlatformRole adds the platform role to the context
func WithPlatformRole(ctx context.Context, platformRole string) context.Context {
	return context.WithValue(ctx, PlatformRoleKey, platformRole)
}

// GetPlatformRole retrieves the platform role from the context
func GetPlatformRole(ctx context.Context) (string, bool) {
	platformRole, ok := ctx.Value(PlatformRoleKey).(string)
	return platformRole, ok
}

// IsSuperadmin returns true if the context carries the superadmin platform role
func IsSuperadmin(ctx context.Context) bool {
	platformRole, ok := GetPlatformRole(ctx)
	return ok && platformRole == "superadmin"
}

// IsTenantAdmin reports a superadmin, or a signed-in user (never an API key:
// keys carry no admin ID) whose role in the selected tenant is admin. It is
// the predicate behind the API's RequireTenantAdmin middleware and the
// service-level gates that need the same answer outside a route, such as SIP
// test-call monitors.
func IsTenantAdmin(ctx context.Context) bool {
	if IsSuperadmin(ctx) {
		return true
	}
	if !IsAdmin(ctx) {
		return false
	}
	role, ok := GetRole(ctx)
	return ok && role == "admin"
}

// WithActorType adds the credential type to the context
func WithActorType(ctx context.Context, actorType string) context.Context {
	return context.WithValue(ctx, ActorTypeKey, actorType)
}

// GetActorType retrieves the credential type from the context
func GetActorType(ctx context.Context) (string, bool) {
	actorType, ok := ctx.Value(ActorTypeKey).(string)
	return actorType, ok
}

// WithAPIKeyID adds the authenticated API key ID to the context
func WithAPIKeyID(ctx context.Context, keyID string) context.Context {
	return context.WithValue(ctx, APIKeyIDKey, keyID)
}

// GetAPIKeyID retrieves the authenticated API key ID from the context
func GetAPIKeyID(ctx context.Context) (string, bool) {
	keyID, ok := ctx.Value(APIKeyIDKey).(string)
	return keyID, ok
}

// WithAuthScope adds the API key scope to the context
func WithAuthScope(ctx context.Context, scope string) context.Context {
	return context.WithValue(ctx, AuthScopeKey, scope)
}

// GetAuthScope retrieves the API key scope from the context
func GetAuthScope(ctx context.Context) (string, bool) {
	scope, ok := ctx.Value(AuthScopeKey).(string)
	return scope, ok
}

// CanWrite reports whether the request identity may perform mutations:
// superadmins, members with an admin/editor tenant role, and API keys with
// write scope. A scope only ever narrows: a read-scope MCP OAuth grant held
// by a superadmin stays read-only, so scope is checked before any role.
func CanWrite(ctx context.Context) bool {
	if scope, ok := GetAuthScope(ctx); ok && scope != "write" {
		return false
	}
	if IsSuperadmin(ctx) {
		return true
	}
	if role, ok := GetRole(ctx); ok {
		return role == "admin" || role == "editor"
	}
	if scope, ok := GetAuthScope(ctx); ok {
		return scope == "write"
	}
	return false
}

// WithVia records the channel a request was dispatched through (ViaMCP).
func WithVia(ctx context.Context, via string) context.Context {
	return context.WithValue(ctx, ViaKey, via)
}

// GetVia retrieves the dispatch channel, if any.
func GetVia(ctx context.Context) (string, bool) {
	via, ok := ctx.Value(ViaKey).(string)
	return via, ok
}
