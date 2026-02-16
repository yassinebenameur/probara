package middleware

import (
	"net/http"

	"github.com/yassinebenameur/probara/api/internal/errors"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

// RequireAdmin ensures the request is made by an admin.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ctxpkg.IsAdmin(r.Context()) {
			errors.WriteUnauthorizedError(w, "admin access required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
