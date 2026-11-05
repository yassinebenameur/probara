package middleware

import (
	"bytes"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/clientip"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

// RateLimiter is the API's general request limiter: in-memory token buckets
// (API_RATE_LIMIT_PER_MINUTE, API_RATE_LIMIT_BURST), so the limit applies per
// API replica. Which bucket a request spends from depends on the route, never
// on an unverified header:
//
//   - Authenticated routes (those behind AuthMiddleware, wrapped by
//     Authenticate): a request whose credential validates spends from that
//     credential's bucket -- API key id, MCP grant (the granting user,
//     via=mcp), or session user. It never touches an address bucket, so a
//     valid credential is never refused because of other traffic from the
//     same address. A request that fails authentication (401) spends from
//     its address's failure bucket instead; once that is empty the failure
//     is answered 429, so guessing keys cannot buy unlimited lookups.
//   - Every other route (Anonymous, router-wide): the client address bucket
//     (clientip.FromRequest, which honors only TRUSTED_PROXY_CIDRS), whatever
//     headers the request carries. A session cookie whose JWT verifies keys
//     the request by its user instead, so signed-in users behind one proxy
//     do not share /auth/me and /auth/refresh capacity.
//
// The classification comes from the real router (SetRouteClassifier), so a
// new unauthenticated route is limited by address without being listed here.
// It bounds request volume; it is not an account lockout.
type RateLimiter struct {
	limit rate.Limit
	burst int
	now   func() time.Time

	// isAuthRoute reports whether a request's route is behind Authenticate.
	// Nil treats every route as anonymous.
	isAuthRoute func(*http.Request) bool
	// sessionSecret verifies admin_access cookies on anonymous routes.
	sessionSecret string

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

// SetRouteClassifier installs the authenticated-route test (see
// RateLimiter). Call it before serving.
func (l *RateLimiter) SetRouteClassifier(isAuthRoute func(*http.Request) bool) {
	if l != nil {
		l.isAuthRoute = isAuthRoute
	}
}

// SetSessionSecret lets anonymous routes key a verified session cookie by
// its user (ADMIN_JWT_SECRET).
func (l *RateLimiter) SetSessionSecret(secret string) {
	if l != nil {
		l.sessionSecret = secret
	}
}

// rateLimitExempt lists the routes whose validated callers are not limited
// here: health and metrics probes, static downloads, push heartbeats (token
// in the URL), OTLP export (OTLP_MONITOR_RATE_PER_MIN per monitor), discovery
// collectors (integration token) and the long-lived alert stream. On an
// authenticated route the exemption covers only requests that authenticate;
// their failures still spend from the address failure bucket.
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

// mcpEndpointUncharged: the MCP endpoint authenticates each request, but
// every tool call is re-dispatched through the router and charged there, so
// the outer request is not charged a second time.
func mcpEndpointUncharged(path string) bool {
	return strings.TrimSuffix(path, "/") == mcpEndpointPath
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

// anonymousKey keys a request on an unauthenticated route: its verified
// session user, else its address.
func (l *RateLimiter) anonymousKey(r *http.Request) string {
	if l.sessionSecret != "" {
		if c, err := r.Cookie("admin_access"); err == nil && c.Value != "" {
			if claims, err := auth.ParseAdminToken(c.Value, l.sessionSecret); err == nil && claims.AdminID != "" {
				return "user:" + claims.AdminID
			}
		}
	}
	return "ip:" + clientip.FromRequest(r)
}

// Anonymous is the router-wide stage: it limits every request whose route is
// not behind Authenticate, by anonymousKey.
func (l *RateLimiter) Anonymous(next http.Handler) http.Handler {
	if l == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rateLimitExempt(r.URL.Path) || (l.isAuthRoute != nil && l.isAuthRoute(r)) {
			next.ServeHTTP(w, r)
			return
		}
		if ok, wait := l.take(l.anonymousKey(r)); !ok {
			writeRateLimited(w, wait)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Authenticate wraps the authentication middleware. A request it lets
// through is charged to its credential; a request it answers 401 is charged
// to the address failure bucket, and turned into a 429 once that is empty.
// Nothing is refused before authentication has run.
func (l *RateLimiter) Authenticate(authenticate func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	if l == nil {
		return authenticate
	}
	return func(next http.Handler) http.Handler {
		return &authGate{l: l, authenticate: authenticate, next: next}
	}
}

// authGate is the handler Authenticate builds. It is a named type so the
// router walk can recognize the wrapper (IsAuthenticate).
type authGate struct {
	l            *RateLimiter
	authenticate func(http.Handler) http.Handler
	next         http.Handler
}

func (g *authGate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The auth middleware writes its own failures; hold them until we know
	// whether it let the request through.
	held := &heldResponse{header: http.Header{}}
	passed := false
	g.authenticate(http.HandlerFunc(func(_ http.ResponseWriter, ar *http.Request) {
		passed = true
		for k, v := range held.header {
			w.Header()[k] = v
		}
		if !rateLimitExempt(ar.URL.Path) && !mcpEndpointUncharged(ar.URL.Path) {
			if ok, wait := g.l.take(identityKey(ar)); !ok {
				writeRateLimited(w, wait)
				return
			}
		}
		g.next.ServeHTTP(w, ar)
	})).ServeHTTP(held, r)
	if passed {
		return
	}
	if held.status == http.StatusUnauthorized {
		if ok, wait := g.l.take("authfail:" + clientip.FromRequest(r)); !ok {
			writeRateLimited(w, wait)
			return
		}
	}
	held.flush(w)
}

// IsAuthenticate reports whether mw is a middleware returned by
// Authenticate, for classifying routes from the router's middleware chains.
// It builds mw around a no-op handler and checks the result's type.
func (l *RateLimiter) IsAuthenticate(mw func(http.Handler) http.Handler) bool {
	if l == nil || mw == nil {
		return false
	}
	_, ok := mw(http.NotFoundHandler()).(*authGate)
	return ok
}

// heldResponse buffers the auth middleware's own (failure) response.
type heldResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (h *heldResponse) Header() http.Header { return h.header }

func (h *heldResponse) WriteHeader(status int) {
	if h.status == 0 {
		h.status = status
	}
}

func (h *heldResponse) Write(b []byte) (int, error) {
	if h.status == 0 {
		h.status = http.StatusOK
	}
	return h.body.Write(b)
}

func (h *heldResponse) flush(w http.ResponseWriter) {
	for k, v := range h.header {
		w.Header()[k] = v
	}
	if h.status == 0 {
		h.status = http.StatusOK
	}
	w.WriteHeader(h.status)
	_, _ = w.Write(h.body.Bytes())
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
