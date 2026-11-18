package middleware

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
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
//     bucket afterwards, and once that is empty its 401 becomes a 429 --
//     except a refreshing session: a session cookie this server signed that
//     expired less than the refresh lifetime ago, on a request carrying no
//     bearer credential. That 401 always goes out, so the client refreshes.
//   - The refresh route with a refresh cookie: keyed on a hash of the cookie;
//     a 401 (the cookie was not valid) is a failure charged as above. With a
//     signed access cookie alongside (live, or expired within the refresh
//     lifetime) it is keyed on that user instead, skips the failure
//     pre-check, and its 401 is never turned into a 429.
//   - Token-path routes (push heartbeats, discovery collectors): a valid
//     token is not limited here; a 401/404 answer is a failure charged as
//     above.
//   - Sign-in (POST /auth/login, and its second step POST /auth/login/mfa):
//     the address (or session user) bucket like any anonymous route, and a
//     401 is a failure charged as above.
//   - Every other route (Anonymous, router-wide): a session cookie this
//     server signed keys the request by its user; otherwise the client
//     address bucket.
//
// Failure buckets limit the work, not only the answers: while an address's
// auth-failure bucket is empty, every request from it that presents a
// credential only a lookup can verify (an API key or MCP token, a refresh
// cookie, a push or collector token, a sign-in) is answered 429 before the
// handler runs. A request on an authenticated route carrying a live session
// cookie (signed, unexpired) is authenticated by that cookie alone, whose
// signature the limiter checks without a lookup, so it is never refused this
// way; nor is a refreshing session. A sign-in is refused whatever cookie it
// carries. A key or token that verified successfully in the last ten minutes
// on this replica is not refused either (verifiedCredentials: a keyed hash,
// never the value; dropped at its first failure), nor a refresh cookie this
// replica issued within the access-token lifetime plus ten minutes, so valid
// machines and sessions behind a flooded address keep working. A valid credential the
// replica has not seen succeed recently still waits until the address has
// budget again: telling it from a guess would take the lookup.
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
	// sessionSecret verifies admin_access cookies; sessionRefreshLifetime is
	// how long after expiry one still counts as a refreshing session.
	sessionSecret          string
	sessionRefreshLifetime time.Duration
	// accessTokenLifetime is ADMIN_ACCESS_TTL_MINUTES: how long after a
	// refresh the browser presents the refresh cookie it was given.
	accessTokenLifetime time.Duration

	mu         sync.Mutex
	buckets    map[string]*rateBucket
	lastSweep  time.Time
	maxBuckets int

	// verified holds credentials that recently passed verification, which
	// the failure pre-check lets through (see RateLimiter).
	verified *verifiedCredentials
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
	loginPath         = "/api/v1/auth/login"
	// loginMFAPath is a sign-in's second step: an unverified credential (a
	// code) like a password, limited the same way.
	loginMFAPath = "/api/v1/auth/login/mfa"
)

// signInRoute reports the sign-in steps: each submits an unverified
// credential (a password, or a second-factor code).
func signInRoute(r *http.Request) bool {
	return r.Method == http.MethodPost && (r.URL.Path == loginPath || r.URL.Path == loginMFAPath)
}

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
		verified:   newVerifiedCredentials(verifiedCredentialTTL, verifiedCredentialsMax),
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
// signed (ADMIN_JWT_SECRET). refreshLifetime is the refresh-token lifetime
// (ADMIN_REFRESH_TTL_DAYS): a cookie that expired longer ago than that can no
// longer be refreshed, so it no longer earns a session's treatment.
func (l *RateLimiter) SetSessionSecret(secret string, refreshLifetime time.Duration) {
	if l != nil {
		l.sessionSecret = secret
		l.sessionRefreshLifetime = refreshLifetime
	}
}

// SetAccessTokenLifetime tells the limiter the access-token lifetime
// (ADMIN_ACCESS_TTL_MINUTES), which sizes how long an issued refresh cookie
// stays vouched for (issuedRefreshCookieTTL).
func (l *RateLimiter) SetAccessTokenLifetime(d time.Duration) {
	if l != nil {
		l.accessTokenLifetime = d
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

// addressKey returns the client address's bucket key with prefix, or false
// when the address is not attributable (see RateLimiter).
func addressKey(prefix string, r *http.Request) (string, bool) {
	if !clientip.Attributable(r) {
		return "", false
	}
	return prefix + bucketAddress(clientip.FromRequest(r)), true
}

// bucketAddress groups IPv6 clients by /64 (clientip.BucketAddress).
func bucketAddress(ip string) string {
	return clientip.BucketAddress(ip)
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
			cred := tokenPathCredential(r)
			if l.refuseWhileFailing(w, r, cred) {
				return
			}
			l.serveChargingFailures(w, r, next, cred)
			return
		}
		if path == refreshPath {
			if c, err := r.Cookie(refreshCookieName); err == nil && c.Value != "" {
				cred := refreshCredential(c.Value)
				// The refresh cookie is opaque: only the database tells a
				// real one from a guess, and a replica that did not issue it
				// has no record of it. The access cookie a browser sends with
				// its refresh is not opaque: one this server signed (live, or
				// expired within the refresh lifetime) names a user without a
				// lookup. Such a refresh skips the failure pre-check on every
				// replica and is charged to that user's refresh bucket, not
				// the cookie's, so a signed-in user cannot buy unlimited
				// lookups with random refresh cookies and an anonymous
				// guesser cannot forge the signature. Whether the two cookies
				// belong to the same session is the handler's lookup to
				// settle; the limiter only bounds how often it runs.
				user, session := l.sessionCookie(r)
				vouched := session != sessionNone
				// Checked before the cookie's own bucket exists, so a
				// stream of random cookies cannot mint buckets (or lookups)
				// once the address has failed enough.
				if !vouched && l.refuseWhileFailing(w, r, cred) {
					return
				}
				bucket := "refresh:" + hashCredential(c.Value)
				if vouched {
					bucket = "refresh-user:" + user
				}
				if ok, wait := l.take(bucket); !ok {
					writeRateLimited(w, wait)
					return
				}
				if vouched {
					l.serveChargingFailuresKeeping401(w, r, next, cred)
				} else {
					l.serveChargingFailures(w, r, next, cred)
				}
				return
			}
		}
		// A sign-in step is a password or code guess whatever cookie comes
		// with it, so a live session does not lift the failure check here.
		login := signInRoute(r)
		if login && l.refuseWhileFailing(w, r, "") {
			return
		}
		key, ok := "", false
		if user, state := l.sessionCookie(r); state != sessionNone {
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
		if login {
			l.serveChargingFailures(w, r, next, "")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// refuseWhileFailing answers 429 and reports true when the request's address
// has emptied its auth-failure bucket, before anything is looked up for the
// credential it presents (cred, "" for none or a sign-in). A credential that
// recently verified is let through. An unattributable address has no such
// bucket.
func (l *RateLimiter) refuseWhileFailing(w http.ResponseWriter, r *http.Request, cred string) bool {
	key, ok := addressKey("authfail:", r)
	if !ok {
		return false
	}
	if l.verified.known(cred, l.now()) {
		return false
	}
	if empty, wait := l.empty(key); empty {
		writeRateLimited(w, wait)
		return true
	}
	return false
}

// serveChargingFailures runs next with its response held. A 401 or 404 --
// the presented token or cookie (cred) was not valid -- forgets cred, spends
// from the address's auth-failure bucket, and is answered 429 once that is
// empty. A 2xx records cred as verified, and so does a refresh cookie the
// response issues (refresh rotates it, sign-in issues one): the next refresh
// presents that one. Any other answer goes out unchanged and uncharged.
func (l *RateLimiter) serveChargingFailures(w http.ResponseWriter, r *http.Request, next http.Handler, cred string) {
	l.serveCharging(w, r, next, cred, false)
}

// serveChargingFailuresKeeping401 is serveChargingFailures for a refresh a
// signed access cookie vouches for: a failure is still charged, but its 401
// always goes out -- it is what tells the browser the session is over, and a
// 429 there would leave a signed-out user retrying forever.
func (l *RateLimiter) serveChargingFailuresKeeping401(w http.ResponseWriter, r *http.Request, next http.Handler, cred string) {
	l.serveCharging(w, r, next, cred, true)
}

func (l *RateLimiter) serveCharging(w http.ResponseWriter, r *http.Request, next http.Handler, cred string, keep401 bool) {
	held := &heldResponse{header: http.Header{}}
	next.ServeHTTP(held, r)
	if held.status >= 200 && held.status < 300 {
		now := l.now()
		l.verified.remember(cred, now)
		l.verified.rememberFor(refreshCredential(issuedRefreshCookie(held.header)), now, l.issuedRefreshCookieTTL())
	}
	if held.status == http.StatusUnauthorized || held.status == http.StatusNotFound {
		l.verified.forget(cred)
		if ok, wait := l.chargeFailure(r); !ok && !(keep401 && held.status == http.StatusUnauthorized) {
			writeRateLimited(w, wait)
			return
		}
	}
	held.flush(w)
}

// issuedRefreshCookieTTL is how long a refresh cookie this replica issued is
// vouched for. The browser presents it only once its access token has
// expired, so the entry must outlive the access-token lifetime
// (ADMIN_ACCESS_TTL_MINUTES) -- with the default 15 minutes a 10-minute
// entry was always gone by then -- plus verifiedCredentialTTL of margin for a
// tab that was idle when the token lapsed.
func (l *RateLimiter) issuedRefreshCookieTTL() time.Duration {
	return l.accessTokenLifetime + verifiedCredentialTTL
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
// failure (serveChargingFailures), unless it is a refreshing session (see
// RateLimiter). While the address's failure bucket is empty, a request that
// is neither a live nor a refreshing session is refused before the auth
// middleware looks anything up.
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
	_, session := g.l.sessionCookie(r)
	// An expired cookie of ours is not a guess: its 401 is what makes the
	// client refresh, so it must never become a 429. Only on its own,
	// though: AuthMiddleware falls through from an expired cookie to a
	// bearer credential and looks that up, so a cookie must not shield one.
	refreshing := session == sessionExpired && !carriesCredentialHeader(r)
	// What AuthMiddleware verifies when no live cookie is present.
	cred := ""
	if session != sessionLive {
		cred = bearerCredential(r)
	}
	if session != sessionLive && !refreshing && g.l.refuseWhileFailing(w, r, cred) {
		return
	}

	// The auth middleware writes its own failures; hold them until we know
	// whether it let the request through.
	held := &heldResponse{header: http.Header{}}
	passed := false
	r, refused := withRefusalSlot(r)
	g.authenticate(http.HandlerFunc(func(_ http.ResponseWriter, ar *http.Request) {
		passed = true
		g.l.verified.remember(cred, g.l.now())
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
	// Authenticated, then refused (ratelimit_refusal.go): the credential's
	// own bucket pays, exempt route or not -- nothing was served.
	if refused.key != "" {
		if ok, wait := g.l.take(refused.key); !ok {
			writeRateLimited(w, wait)
			return
		}
	}
	if held.status == http.StatusUnauthorized && !refreshing {
		g.l.verified.forget(cred)
		if ok, wait := g.l.chargeFailure(r); !ok {
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

// empty reports whether key's bucket has no whole token left, and how long
// until it has one. It neither spends a token nor creates a bucket: a key
// with no bucket is full.
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
