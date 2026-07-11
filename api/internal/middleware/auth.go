package middleware

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/auth"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
)

// GetTenantID extracts the tenant ID from the request context
func GetTenantID(ctx context.Context) (string, error) {
	tenantID, ok := ctxpkg.GetTenantID(ctx)
	if !ok || tenantID == "" {
		return "", fmt.Errorf("tenant ID not found in context")
	}
	return tenantID, nil
}

// GetAdminID extracts the admin ID from the request context
func GetAdminID(ctx context.Context) (string, error) {
	adminID, ok := ctxpkg.GetAdminID(ctx)
	if !ok || adminID == "" {
		return "", fmt.Errorf("admin ID not found in context")
	}
	return adminID, nil
}

// identityQuery resolves the admin's platform role plus their membership role
// for the requested tenant in one round trip. $2 is uuid.Nil when no tenant
// was requested (m.role comes back NULL).
const identityQuery = `
	SELECT u.platform_role, u.disabled_at, m.role
	FROM admin_users u
	LEFT JOIN tenant_memberships m ON m.admin_user_id = u.id AND m.tenant_id = $2
	WHERE u.id = $1
`

// AuthMiddleware validates admin JWT cookies or API keys and injects
// identity (tenant, platform role, tenant role, scope) into context.
func AuthMiddleware(dbClient *db.Client, log *logger.Logger, jwtSecret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			applyCORSHeaders(w, r)

			// Prefer admin JWT cookie if present
			if cookie, err := r.Cookie("admin_access"); err == nil && cookie.Value != "" {
				claims, err := auth.ParseAdminToken(cookie.Value, jwtSecret)
				if err == nil && claims.AdminID != "" {
					if adminID, err := uuid.Parse(claims.AdminID); err == nil {
						handleAdminRequest(w, r, next, dbClient, log, adminID)
						return
					}
				}
			}

			// Extract API key from request
			apiKey, err := auth.ExtractAPIKey(r)
			if err != nil {
				log.WithFields(map[string]interface{}{
					"error": err.Error(),
					"path":  r.URL.Path,
				}).Warn("Failed to extract API key")
				writeAuthError(w, "missing or invalid Authorization header")
				return
			}

			// Validate API key and resolve its identity (tenant, scope, expiry)
			identity, err := auth.ValidateAPIKey(r.Context(), dbClient, apiKey)
			if err != nil {
				log.WithFields(map[string]interface{}{
					"error": err.Error(),
					"path":  r.URL.Path,
				}).Warn("Failed to validate API key")
				writeAuthError(w, "invalid API key")
				return
			}

			ctx := ctxpkg.WithTenantID(r.Context(), identity.TenantID)
			ctx = ctxpkg.WithActorType(ctx, ctxpkg.ActorTypeAPIKey)
			ctx = ctxpkg.WithAPIKeyID(ctx, identity.KeyID)
			ctx = ctxpkg.WithAuthScope(ctx, identity.Scope)

			touchAPIKeyLastUsed(dbClient, identity.KeyID)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func handleAdminRequest(w http.ResponseWriter, r *http.Request, next http.Handler, dbClient *db.Client, log *logger.Logger, adminID uuid.UUID) {
	tenantValue := r.Header.Get("X-Tenant-ID")
	if tenantValue == "" {
		tenantValue = r.URL.Query().Get("tenant_id")
	}
	tenantUUID := uuid.Nil
	if tenantValue != "" {
		parsed, err := uuid.Parse(tenantValue)
		if err != nil {
			writeBadRequestError(w, "invalid tenant identifier")
			return
		}
		tenantUUID = parsed
	}

	var platformRole string
	var disabledAt *time.Time
	var membershipRole sql.NullString

	err := dbClient.QueryRowContext(r.Context(), identityQuery, adminID, tenantUUID).
		Scan(&platformRole, &disabledAt, &membershipRole)
	switch {
	case isMissingSchema(err):
		// Deploy-ordering tolerance: migrations run as a post-upgrade job, so
		// this binary may briefly see the old schema. Pre-migration every
		// admin is a global superuser, so legacy behavior is correct here.
		log.WithError(err).Warn("RBAC schema not migrated yet; treating admin as superadmin")
		platformRole = auth.PlatformRoleSuperadmin
	case errors.Is(err, sql.ErrNoRows):
		writeAuthError(w, "unknown admin user")
		return
	case err != nil:
		log.WithError(err).Error("Failed to resolve admin identity")
		writeAuthError(w, "failed to resolve identity")
		return
	}

	if disabledAt != nil {
		writeAuthError(w, "account disabled")
		return
	}

	ctx := ctxpkg.WithAdminID(r.Context(), adminID.String())
	ctx = ctxpkg.WithActorType(ctx, ctxpkg.ActorTypeAdminUser)
	ctx = ctxpkg.WithPlatformRole(ctx, platformRole)

	if platformRole == auth.PlatformRoleSuperadmin {
		// Superadmins act on any tenant; effective role is admin everywhere.
		ctx = ctxpkg.WithRole(ctx, auth.RoleAdmin)
		if tenantUUID != uuid.Nil {
			ctx = ctxpkg.WithTenantID(ctx, tenantUUID.String())
		}
		next.ServeHTTP(w, r.WithContext(ctx))
		return
	}

	// Members: a tenant-scoped request requires a membership in that tenant.
	// Requests without a tenant (e.g. GET /tenants for the switcher) proceed
	// with identity only; tenant-dependent handlers fail via GetTenantID.
	if tenantUUID != uuid.Nil {
		if !membershipRole.Valid {
			writeForbiddenError(w, "no access to this tenant")
			return
		}
		ctx = ctxpkg.WithTenantID(ctx, tenantUUID.String())
		ctx = ctxpkg.WithRole(ctx, membershipRole.String)
	}

	next.ServeHTTP(w, r.WithContext(ctx))
}

// lastUsedTouch tracks per-key throttling of last_used_at updates so hot keys
// don't turn every request into an UPDATE.
var lastUsedTouch sync.Map

const lastUsedTouchInterval = 5 * time.Minute

func touchAPIKeyLastUsed(dbClient *db.Client, keyID string) {
	if keyID == "" {
		return
	}
	now := time.Now()
	if prev, ok := lastUsedTouch.Load(keyID); ok {
		if now.Sub(prev.(time.Time)) < lastUsedTouchInterval {
			return
		}
	}
	lastUsedTouch.Store(keyID, now)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Best-effort; ignores errors (including pre-migration missing column).
		_, _ = dbClient.ExecContext(ctx, `UPDATE api_keys SET last_used_at = NOW() WHERE id = $1`, keyID)
	}()
}

// isMissingSchema reports whether err is Postgres undefined_table (42P01) or
// undefined_column (42703) — i.e. this binary is ahead of the migrations job.
func isMissingSchema(err error) bool {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return false
	}
	return pqErr.Code == "42P01" || pqErr.Code == "42703"
}

func applyCORSHeaders(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Vary", "Origin")
	}
}

func writeAuthError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte(`{"error":"unauthorized","message":"` + message + `"}`))
}

func writeForbiddenError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	w.Write([]byte(`{"error":"forbidden","message":"` + message + `"}`))
}

func writeBadRequestError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	w.Write([]byte(`{"error":"bad_request","message":"` + message + `"}`))
}
