package middleware

import (
	"net/http"
	"strings"

	"github.com/yassinebenameur/probara/api/internal/errors"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

// postReadPaths are POST endpoints that perform no persistent writes —
// ephemeral checks/previews that viewers and read-scope keys may call.
//
// KEEP THIS LIST IN SYNC with the router: any new "test"/"preview"/"probe"
// style POST endpoint added to server.go will return 403 for viewers until
// it is added here. Deliberately NOT listed (they mutate or side-effect):
// /alert-channels/{id}/test (sends a real notification),
// /monitors/{id}/run (persists a result, can fire alerts),
// alert acknowledge/resolve (state mutations owned by editors).
var postReadPaths = map[string]struct{}{
	"/api/v1/monitors/test":                   {},
	"/api/v1/monitors/import/preview":         {},
	"/api/v1/monitors/dependency-suggestions": {},
	"/api/v1/mesh/probe":                      {},
	"/api/v1/ai-settings/test":                {},
}

// RequireWrite blocks mutating methods for read-only identities (viewer
// members, read-scope API keys). Reads always pass; the postReadPaths
// allowlist exempts compute-only POST endpoints.
func RequireWrite(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}

		if r.Method == http.MethodPost {
			if _, ok := postReadPaths[strings.TrimSuffix(r.URL.Path, "/")]; ok {
				next.ServeHTTP(w, r)
				return
			}
		}

		if !ctxpkg.CanWrite(r.Context()) {
			errors.WriteForbiddenError(w, "read-only access: this action requires write permission")
			return
		}

		next.ServeHTTP(w, r)
	})
}
