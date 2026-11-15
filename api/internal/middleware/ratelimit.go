package middleware

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"net/http"
	"net/netip"
	"sort"
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
// API replica. A presented credential is never judged on an address bucket,
// because one address is often many people (an office NAT, the frontend's
// /api proxy, an ingress pod):
//
//   - Authenticated routes (behind AuthMiddleware, wrapped by Authenticate):
//     a request whose credential validates spends from that credential's
//     bucket -- API key id, MCP grant (the granting user, via=mcp), or session
//     user. A request answered 401 spends from its address's auth-failure
//     bucket afterwards, and once that is empty its 401 becomes a 429, so
//     guessing keys cannot buy unlimited lookups -- except when it carried a
//     session cookie this server signed (only expired): that 401 always goes
//     out, so the client refreshes.
//   - The refresh route with a refresh cookie: keyed on a hash of the cookie;
//     a 401 (the cookie was not valid) is a failure charged as above.
//   - Token-path routes (push heartbeats, discovery collectors): a valid
//     token is not limited here; a 401/404 answer is a failure charged as
//     above.
//   - Every other route (Anonymous, router-wide): a session cookie this
//     server signed keys the request by its user; otherwise the client
//     address bucket.
//
// Address buckets -- the anonymous bucket and the auth-failure bucket -- are
// used only for attributable addresses (clientip.Attributable): forwarded by
// a trusted proxy, or a public TCP peer. A private peer we cannot see through
// would pool every visitor behind it into one bucket that a single attacker
// could empty for everyone, so its traffic skips the address buckets (the
// credential buckets still apply). IPv6 addresses share a bucket per /64.
//
// The authenticated-route classification comes from the real router
// (SetRouteClassifier), so a new unauthenticated route is limited by address
// without being listed here. It bounds request volume; it is not an account
// lockout.
type RateLimiter struct {
	limit rate.Limit
	burst int
	now   func() time.Time

	// isAuthRoute reports whether a request's route is behind Authenticate.
	// Nil treats every route as anonymous.
	isAuthRoute func(*http.Request) bool
	// sessionSecret verifies admin_access cookies.
	sessionSecret string

	mu         sync.Mutex
	buckets    map[string]*rateBucket
	lastSweep  time.Time
	maxBuckets int
}

type rateBucket struct {
	lim  *rate.Limiter
	seen time.Time
}

// rateLimiterMaxBuckets caps the bucket map. Past it, buckets that have
// refilled are dropped first (a fresh bucket behaves identically), then the
// least recently used, so filling the map never resets an active bucket.
const rateLimiterMaxBuckets = 100_000

// Credential carriers the limiter reads (the auth handlers own them).
const (
	sessionCookieName = "admin_access"
	refreshCookieName = "admin_refresh"
	refreshPath       = "/api/v1/auth/refresh"
)

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
		limit:      rate.Limit(float64(perMinute) / 60),
		burst:      burst,
		now:        time.Now,
		buckets:    make(map[string]*rateBucket),
		maxBuckets: rateLimiterMaxBuckets,
	}
}

// SetRouteClassifier installs the authenticated-route test (see
// RateLimiter). Call it before serving.
func (l *RateLimiter) SetRouteClassifier(isAuthRoute func(*http.Request) bool) {
	if l != nil {
		l.isAuthRoute = isAuthRoute
	}
}

// SetSessionSecret lets the limiter recognize session cookies this server
// signed (ADMIN_JWT_SECRET).
func (l *RateLimiter) SetSessionSecret(secret string) {
	if l != nil {
		l.sessionSecret = secret
	}
}

// rateLimitExempt lists the routes whose validated callers are not limited
// here: health and metrics probes, static downloads, OTLP export
// (OTLP_MONITOR_RATE_PER_MIN per monitor) and the long-lived alert stream.
// On an authenticated route the exemption covers only requests that
// authenticate; their failures still spend from the failure bucket.
func rateLimitExempt(path string) bool {
	switch path {
	case "/healthz", "/readyz", "/metrics", "/api/v1/alerts/stream":
		return true
	}
	for _, prefix := range []string{"/static/", "/api/v1/otlp/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// tokenPathRoute reports the unauthenticated routes that carry their own
// credential (a token in the path, or a collector token): machines call them
// at a steady rate, often from shared addresses, so a valid token is never
// limited here, but a 401/404 answer is a failure.
func tokenPathRoute(path string) bool {
	return strings.HasPrefix(path, "/api/v1/push/") || strings.HasPrefix(path, "/api/v1/discovery-collectors/")
}

// mcpEndpointUncharged: the MCP endpoint authenticates each request, but
// every tool call is re-dispatched through the router and charged there, so
// the outer request is not charged a second time.
func mcpEndpointUncharged(path string) bool {
	return strings.TrimSuffix(path, "/") == mcpEndpointPath
}

// identityKey is the bucket key for an authenticated request, or "" when it
// carries no identity (nothing to charge).
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
	return ""
}

// signedSessionUser returns the user of a session cookie this server signed,
// expired or not.
func (l *RateLimiter) signedSessionUser(r *http.Request) (string, bool) {
	if l.sessionSecret == "" {
		return "", false
	}
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return "", false
	}
	return auth.AdminIDFromSignedToken(c.Value, l.sessionSecret)
}

// addressKey returns the client address's bucket key with prefix, or false
// when the address is not attributable (see RateLimiter).
func addressKey(prefix string, r *http.Request) (string, bool) {
	if !clientip.Attributable(r) {
		return "", false
	}
	return prefix + bucketAddress(clientip.FromRequest(r)), true
}

// bucketAddress groups IPv6 clients by /64: one host is usually handed a
// whole /64, so per-address buckets would let it rotate to a fresh one on
// every request.
func bucketAddress(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	a = a.WithZone("").Unmap()
	if a.Is6() {
		if p, err := a.Prefix(64); err == nil {
			return p.String()
		}
	}
	return a.String()
}

func hashCredential(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:16])
}

// Anonymous is the router-wide stage: it limits every request whose route is
// not behind Authenticate.
func (l *RateLimiter) Anonymous(next http.Handler) http.Handler {
	if l == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if rateLimitExempt(path) || (l.isAuthRoute != nil && l.isAuthRoute(r)) {
			next.ServeHTTP(w, r)
			return
		}
		if tokenPathRoute(path) {
			l.serveChargingFailures(w, r, next)
			return
		}
		if path == refreshPath {
			if c, err := r.Cookie(refreshCookieName); err == nil && c.Value != "" {
				if ok, wait := l.take("refresh:" + hashCredential(c.Value)); !ok {
					writeRateLimited(w, wait)
					return
				}
				l.serveChargingFailures(w, r, next)
				return
			}
		}
		key, ok := "", false
		if user, signed := l.signedSessionUser(r); signed {
			key, ok = "user:"+user, true
		} else {
			key, ok = addressKey("ip:", r)
		}
		if ok {
			if allowed, wait := l.take(key); !allowed {
				writeRateLimited(w, wait)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// serveChargingFailures runs next with its response held. A 401 or 404 --
// the presented token or cookie was not valid -- spends from the address's
// auth-failure bucket, and is answered 429 once that is empty. Any other
// answer goes out unchanged and uncharged.
func (l *RateLimiter) serveChargingFailures(w http.ResponseWriter, r *http.Request, next http.Handler) {
	held := &heldResponse{header: http.Header{}}
	next.ServeHTTP(held, r)
	if held.status == http.StatusUnauthorized || held.status == http.StatusNotFound {
		if ok, wait := l.chargeFailure(r); !ok {
			writeRateLimited(w, wait)
			return
		}
	}
	held.flush(w)
}

// chargeFailure spends one token from the address's auth-failure bucket. An
// unattributable address has no such bucket and is always allowed.
func (l *RateLimiter) chargeFailure(r *http.Request) (bool, time.Duration) {
	key, ok := addressKey("authfail:", r)
	if !ok {
		return true, 0
	}
	return l.take(key)
}

// Authenticate wraps the authentication middleware. A request it lets
// through is charged to its credential; a request it answers 401 is a
// failure (serveChargingFailures), unless it carried a session cookie this
// server signed. Nothing is refused before authentication has run.
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
			if key := identityKey(ar); key != "" {
				if ok, wait := g.l.take(key); !ok {
					writeRateLimited(w, wait)
					return
				}
			}
		}
		g.next.ServeHTTP(w, ar)
	})).ServeHTTP(held, r)
	if passed {
		return
	}
	if held.status == http.StatusUnauthorized {
		// An expired session cookie of ours is not a guess: its 401 is what
		// makes the client refresh, so it must never become a 429.
		if _, signed := g.l.signedSessionUser(r); !signed {
			if ok, wait := g.l.chargeFailure(r); !ok {
				writeRateLimited(w, wait)
				return
			}
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

// heldResponse buffers a response until the limiter decides whether to send
// it or answer 429 instead.
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
		if len(l.buckets) >= l.maxBuckets {
			l.evictLocked(now)
		}
		b = &rateBucket{lim: rate.NewLimiter(l.limit, l.burst)}
		l.buckets[key] = b
	}
	b.seen = now
	return b.lim
}

// sweepLocked drops buckets idle long enough to have refilled completely: a
// fresh bucket behaves identically, so forgetting them changes nothing.
func (l *RateLimiter) sweepLocked(now time.Time) {
	if now.Sub(l.lastSweep) < time.Minute {
		return
	}
	l.lastSweep = now
	refill := time.Duration(float64(l.burst)/float64(l.limit)*float64(time.Second)) + time.Minute
	for key, b := range l.buckets {
		if now.Sub(b.seen) > refill {
			delete(l.buckets, key)
		}
	}
}

// evictLocked makes room in a full map without resetting anyone's limit
// more than it must: buckets that have refilled go first (dropping one
// changes nothing), then the least recently used until a tenth of the cap is
// free. A bucket in active use is the last to go, so flooding the map with
// new keys cannot be used to wipe one's own or someone else's spent tokens.
func (l *RateLimiter) evictLocked(now time.Time) {
	for key, b := range l.buckets {
		if b.lim.TokensAt(now) >= float64(l.burst) {
			delete(l.buckets, key)
		}
	}
	target := l.maxBuckets - l.maxBuckets/10
	if target >= l.maxBuckets {
		target = l.maxBuckets - 1
	}
	if len(l.buckets) <= target {
		return
	}
	type aged struct {
		key  string
		seen time.Time
	}
	all := make([]aged, 0, len(l.buckets))
	for key, b := range l.buckets {
		all = append(all, aged{key, b.seen})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].seen.Before(all[j].seen) })
	for _, a := range all[:len(all)-target] {
		delete(l.buckets, a.key)
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
