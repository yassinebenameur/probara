package middleware

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"

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

// AuthMiddleware validates admin JWT cookies or API keys and injects tenant ID into context.
func AuthMiddleware(dbClient *db.Client, log *logger.Logger, jwtSecret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			applyCORSHeaders(w, r)

			// Prefer admin JWT cookie if present
			if cookie, err := r.Cookie("admin_access"); err == nil && cookie.Value != "" {
				claims, err := auth.ParseAdminToken(cookie.Value, jwtSecret)
				if err == nil && claims.AdminID != "" {
					ctx := ctxpkg.WithAdminID(r.Context(), claims.AdminID)

					tenantValue := r.Header.Get("X-Tenant-ID")
					if tenantValue == "" {
						tenantValue = r.URL.Query().Get("tenant_id")
					}
					if tenantValue != "" {
						if _, err := uuid.Parse(tenantValue); err != nil {
							writeBadRequestError(w, "invalid tenant identifier")
							return
						}
						ctx = ctxpkg.WithTenantID(ctx, tenantValue)
					}

					next.ServeHTTP(w, r.WithContext(ctx))
					return
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

			// Validate API key and get tenant ID
			tenantID, err := auth.ValidateAPIKey(r.Context(), dbClient, apiKey)
			if err != nil {
				log.WithFields(map[string]interface{}{
					"error": err.Error(),
					"path":  r.URL.Path,
				}).Warn("Failed to validate API key")
				writeAuthError(w, "invalid API key")
				return
			}

			// Inject tenant ID into context
			ctx := ctxpkg.WithTenantID(r.Context(), tenantID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
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

func writeBadRequestError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	w.Write([]byte(`{"error":"bad_request","message":"` + message + `"}`))
}
