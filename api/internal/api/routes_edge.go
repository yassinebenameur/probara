package api

import (
	"net/http"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	apimiddleware "github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/shared/clientip"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/securityheaders"
)

// useEdgeSecurity mounts the router-wide browser and abuse controls:
// security headers (shared/securityheaders), the API_ALLOWED_ORIGINS CORS
// allowlist, and the anonymous stage of the general rate limiter. It returns
// the limiter; the auth middleware must be wrapped with its Authenticate so
// authenticated routes are charged per credential (see RateLimiter). A nil
// limiter (API_RATE_LIMIT_PER_MINUTE=0) passes everything through.
func useEdgeSecurity(r chi.Router, cfg *config.APIConfig, log *logger.Logger) *apimiddleware.RateLimiter {
	limiter := apimiddleware.NewRateLimiter(cfg.RateLimitPerMinute, cfg.RateLimitBurst)
	if limiter != nil {
		limiter.SetSessionSecret(cfg.AdminJWTSecret)
		if mux, ok := r.(*chi.Mux); ok {
			limiter.SetRouteClassifier(authRouteClassifier(mux, limiter))
		}
		if len(cfg.TrustedProxyCIDRs) == 0 {
			log.Warn("API rate limiting is on but TRUSTED_PROXY_CIDRS is empty: anonymous requests and authentication failures are limited per TCP peer, which behind a proxy or ingress is shared by every client; list your proxies in TRUSTED_PROXY_CIDRS")
		}
	}
	r.Use(securityheaders.API(clientip.NewResolver(cfg.TrustedProxyCIDRs), cfg.HSTSMaxAge))
	r.Use(apimiddleware.CORS(cfg.AllowedOrigins))
	r.Use(limiter.Anonymous)
	return limiter
}

// mcpRoute authenticates inside its handler (mcp.Handler.Protect) rather
// than through a chi middleware, so the router walk cannot see it.
const mcpRoute = "/api/v1/mcp"

// authRouteClassifier reports whether a request's route runs behind the
// limiter's Authenticate wrapper. It is derived once, on first use, from the
// finished router: every route whose middleware chain contains Authenticate,
// plus the MCP endpoint.
func authRouteClassifier(mux *chi.Mux, limiter *apimiddleware.RateLimiter) func(*http.Request) bool {
	var once sync.Once
	authed := map[string]bool{}
	return func(r *http.Request) bool {
		once.Do(func() {
			_ = chi.Walk(mux, func(method, route string, _ http.Handler, mws ...func(http.Handler) http.Handler) error {
				for _, mw := range mws {
					if limiter.IsAuthenticate(mw) {
						authed[method+" "+normalizeRoutePattern(route)] = true
						break
					}
				}
				return nil
			})
		})
		if strings.TrimSuffix(r.URL.Path, "/") == mcpRoute {
			return true
		}
		pattern := mux.Find(chi.NewRouteContext(), r.Method, r.URL.Path)
		return pattern != "" && authed[r.Method+" "+normalizeRoutePattern(pattern)]
	}
}

// normalizeRoutePattern brings chi.Walk's and Mux.Find's spellings of one
// route together: Walk keeps the "/*" mount segments of sub-routers and the
// trailing "/" of a sub-router's root route, Find drops both.
func normalizeRoutePattern(p string) string {
	for strings.Contains(p, "/*/") {
		p = strings.ReplaceAll(p, "/*/", "/")
	}
	if len(p) > 1 {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}
