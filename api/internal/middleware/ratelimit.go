package middleware

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
	"golang.org/x/time/rate"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/shared/clientip"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

// RateLimiter is the API's general request limiter: in-memory token buckets
// (API_RATE_LIMIT_PER_MINUTE, API_RATE_LIMIT_BURST), so the limit applies per
// API replica. It runs in two places:
//
//   - Anonymous, router-wide: requests that present no credential are keyed
//     by client address (clientip.FromRequest, which honors only
//     TRUSTED_PROXY_CIDRS). Requests that present one pass through here, but
//     a 401 charges an auth-failure bucket for their address, and an address
//     whose failure bucket is empty is refused before authentication runs --
//     so a stream of bad API keys cannot buy unlimited database lookups.
//   - Identified, right after AuthMiddleware: keyed by the verified
//     credential -- API key id, MCP grant (the granting user, via=mcp), or
//     session user -- never by the address, so users behind one NAT or proxy
//     do not share a bucket.
//
// Login lockout is a separate, database-backed control in the auth handlers;
// this limiter only bounds request volume.
type RateLimiter struct {
	limit rate.Limit
	burst int
	now   func() time.Time

	mu        sync.Mutex
	buckets   map[string]*rateBucket
	lastSweep time.Time
}

type rateBucket struct {
	lim  *rate.Limiter
	seen time.Time
}

// rateLimiterMaxBuckets caps the bucket map between sweeps; past it the map
// is reset wholesale, which only ever errs toward allowing requests.
const rateLimiterMaxBuckets = 100_000

// NewRateLimiter returns a limiter refilling perMinute tokens a minute with
// room for burst. perMinute <= 0 disables it: the returned nil limiter's
// middlewares pass every request through.
func NewRateLimiter(perMinute, burst int) *RateLimiter {
	if perMinute <= 0 {
		return nil
	}
	if burst <= 0 {
		burst = 1
	}
	return &RateLimiter{
		limit:   rate.Limit(float64(perMinute) / 60),
		burst:   burst,
		now:     time.Now,
		buckets: make(map[string]*rateBucket),
	}
}

// rateLimitExempt lists the routes with their own throttling or a caller
// that cannot back off: health and metrics probes, static downloads, push
// heartbeats (token in the URL), OTLP export (OTLP_MONITOR_RATE_PER_MIN per
// monitor), discovery collectors (integration token) and the long-lived
// alert stream.
func rateLimitExempt(path string) bool {
	switch path {
	case "/healthz", "/readyz", "/metrics", "/api/v1/alerts/stream":
		return true
	}
	for _, prefix := range []string{"/static/", "/api/v1/push/", "/api/v1/otlp/", "/api/v1/discovery-collectors/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// hasCredential reports whether the request presents a session cookie or an
// Authorization header, without validating either.
func hasCredential(r *http.Request) bool {
	if c, err := r.Cookie("admin_access"); err == nil && c.Value != "" {
		return true
	}
	return r.Header.Get("Authorization") != ""
}

// identityKey is the bucket key for an authenticated request.
func identityKey(r *http.Request) string {
	ctx := r.Context()
	if keyID, ok := ctxpkg.GetAPIKeyID(ctx); ok && keyID != "" {
		return "key:" + keyID
	}
	if adminID, ok := ctxpkg.GetAdminID(ctx); ok && adminID != "" {
		if via, _ := ctxpkg.GetVia(ctx); via == ctxpkg.ViaMCP {
			return "mcp:" + adminID
		}
		return "user:" + adminID
	}
	return "ip:" + clientip.FromRequest(r)
}

// Anonymous is the router-wide stage (see RateLimiter).
func (l *RateLimiter) Anonymous(next http.Handler) http.Handler {
	if l == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rateLimitExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		ip := clientip.FromRequest(r)
		if !hasCredential(r) {
			if ok, wait := l.take("ip:" + ip); !ok {
				writeRateLimited(w, wait)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		failKey := "authfail:" + ip
		if empty, wait := l.empty(failKey); empty {
			writeRateLimited(w, wait)
			return
		}
		ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		if ww.Status() == http.StatusUnauthorized {
			l.take(failKey)
		}
	})
}

// Identified is the per-credential stage; mount it after AuthMiddleware.
func (l *RateLimiter) Identified(next http.Handler) http.Handler {
	if l == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rateLimitExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if ok, wait := l.take(identityKey(r)); !ok {
			writeRateLimited(w, wait)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// take spends one token from key's bucket, or reports how long until one is
// available.
func (l *RateLimiter) take(key string) (bool, time.Duration) {
	now := l.now()
	lim := l.bucket(key, now)
	res := lim.ReserveN(now, 1)
	if delay := res.DelayFrom(now); delay > 0 {
		res.CancelAt(now)
		return false, delay
	}
	return true, 0
}

// empty reports, without spending, whether key's bucket has no whole token.
// A key never seen is not empty.
func (l *RateLimiter) empty(key string) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	b, ok := l.buckets[key]
	l.mu.Unlock()
	if !ok {
		return false, 0
	}
	tokens := b.lim.TokensAt(now)
	if tokens >= 1 {
		return false, 0
	}
	return true, time.Duration((1 - tokens) / float64(l.limit) * float64(time.Second))
}

func (l *RateLimiter) bucket(key string, now time.Time) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepLocked(now)
	b, ok := l.buckets[key]
	if !ok {
		b = &rateBucket{lim: rate.NewLimiter(l.limit, l.burst)}
		l.buckets[key] = b
	}
	b.seen = now
	return b.lim
}

// sweepLocked drops buckets idle long enough to have refilled completely: a
// fresh bucket behaves identically, so forgetting them changes nothing.
func (l *RateLimiter) sweepLocked(now time.Time) {
	if now.Sub(l.lastSweep) < time.Minute && len(l.buckets) < rateLimiterMaxBuckets {
		return
	}
	l.lastSweep = now
	refill := time.Duration(float64(l.burst)/float64(l.limit)*float64(time.Second)) + time.Minute
	for key, b := range l.buckets {
		if now.Sub(b.seen) > refill {
			delete(l.buckets, key)
		}
	}
	if len(l.buckets) >= rateLimiterMaxBuckets {
		l.buckets = make(map[string]*rateBucket)
	}
}

func writeRateLimited(w http.ResponseWriter, wait time.Duration) {
	seconds := int(math.Ceil(wait.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	apierrors.WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many requests; retry after "+strconv.Itoa(seconds)+"s")
}
