package middleware

import (
	"net/http"
	"strings"
)

// corsAllowHeaders are the request headers a credentialed cross-origin
// caller may send: auth, tenant selection, request tracing and the MCP
// streamable-HTTP headers.
const corsAllowHeaders = "Authorization, Content-Type, X-Tenant-ID, X-Request-ID, Last-Event-ID, Mcp-Protocol-Version, Mcp-Session-Id"

// CORS answers cross-origin browser requests from the API_ALLOWED_ORIGINS
// allowlist only, with credentials. Any other Origin gets no CORS headers,
// so the browser withholds the response. The default, an empty list, means
// the API serves only same-origin callers (the operator UI's /api proxy).
//
// The public OAuth/MCP discovery routes keep their own wildcard policy
// (oauth.PublicCORS), which carries no credentials.
//
// This replaces the old behavior of echoing any Origin with
// Access-Control-Allow-Credentials: true.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[strings.ToLower(strings.TrimRight(strings.TrimSpace(o), "/"))] = true
	}
	return func(next http.Handler) http.Handler {
		if len(allowed) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}
			h := w.Header()
			h.Add("Vary", "Origin")
			if !allowed[strings.ToLower(origin)] {
				next.ServeHTTP(w, r)
				return
			}
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Expose-Headers", "Retry-After, X-Request-ID")
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Add("Vary", "Access-Control-Request-Method")
				h.Add("Vary", "Access-Control-Request-Headers")
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers", corsAllowHeaders)
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
