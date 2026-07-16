package middleware

import (
	"net/http"

	"github.com/yassinebenameur/probara/api/internal/errors"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

// RequireTenantAdmin ensures the request is made by a superadmin or by a
// member whose role in the selected tenant is admin. API keys never pass.
func RequireTenantAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if ctxpkg.IsSuperadmin(ctx) {
			next.ServeHTTP(w, r)
			return
		}
		if !ctxpkg.IsAdmin(ctx) {
			errors.WriteForbiddenError(w, "admin session required")
			return
		}
		if role, ok := ctxpkg.GetRole(ctx); !ok || role != "admin" {
			errors.WriteForbiddenError(w, "tenant admin role required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireSuperadmin ensures the request is made by a platform superadmin.
func RequireSuperadmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ctxpkg.IsAdmin(r.Context()) {
			errors.WriteUnauthorizedError(w, "admin access required")
			return
		}
		if !ctxpkg.IsSuperadmin(r.Context()) {
			errors.WriteForbiddenError(w, "superadmin access required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
