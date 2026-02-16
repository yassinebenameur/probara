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
