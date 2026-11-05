package api

import (
	"github.com/go-chi/chi/v5"

	apimiddleware "github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/shared/clientip"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/securityheaders"
)

// useEdgeSecurity mounts the router-wide browser and abuse controls:
// security headers (shared/securityheaders), the API_ALLOWED_ORIGINS CORS
// allowlist, and the anonymous stage of the general rate limiter. It returns
// the limiter so the protected group can mount its per-credential stage
// (RateLimiter.Identified) right after authentication. A nil limiter
// (API_RATE_LIMIT_PER_MINUTE=0) passes everything through.
func useEdgeSecurity(r chi.Router, cfg *config.APIConfig) *apimiddleware.RateLimiter {
	limiter := apimiddleware.NewRateLimiter(cfg.RateLimitPerMinute, cfg.RateLimitBurst)
	r.Use(securityheaders.API(clientip.NewResolver(cfg.TrustedProxyCIDRs), cfg.HSTSMaxAge))
	r.Use(apimiddleware.CORS(cfg.AllowedOrigins))
	r.Use(limiter.Anonymous)
	return limiter
}
