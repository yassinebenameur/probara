package statuspage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"github.com/yassinebenameur/probara/shared/statustemplate"
	"github.com/yassinebenameur/probara/shared/webpush"
)

// pushRequestTimeout bounds a subscribe/unsubscribe round trip. Both are a
// single small statement, so this is a safety net rather than a budget.
const pushRequestTimeout = 5 * time.Second

func pushRequestContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), pushRequestTimeout)
}

const (
	// pushSubscribeMaxBody bounds the request body. A real subscription is a
	// few hundred bytes; anything larger is not one.
	pushSubscribeMaxBody = 4096

	// pushSubscriptionCapEnvVar bounds how many subscriptions one page may
	// accumulate, so a public unauthenticated endpoint cannot become
	// unbounded storage.
	pushSubscriptionCapEnvVar  = "STATUS_PAGE_PUSH_MAX_SUBSCRIPTIONS_PER_PAGE"
	defaultPushSubscriptionCap = 10000

	// pushTrustProxyEnvVar opts into reading X-Forwarded-For for rate-limit
	// keying. Off by default: trusting a client-settable header would make
	// the limiter free to bypass by anyone who can spoof it.
	pushTrustProxyEnvVar = "STATUS_PAGE_TRUSTED_PROXY"

	// Rate limit per client IP. Subscribing is a once-per-browser action, so
	// this is generous for real use and still bounds abuse.
	pushRatePerMinute = 10
	pushRateBurst     = 5
)

// pushRateLimiter is a per-IP token bucket, mirroring the OTLP ingest limiter
// (api/internal/services/otlp/service.go). In-memory and per-replica, which is
// fine: this is a spam control, not a quota.
type pushRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	seen     map[string]time.Time
}

func newPushRateLimiter() *pushRateLimiter {
	return &pushRateLimiter{
		limiters: make(map[string]*rate.Limiter),
		seen:     make(map[string]time.Time),
	}
}

func (l *pushRateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Bound the map: without this, one host cycling source addresses would
	// grow it without limit. Eviction is coarse on purpose -- an evicted
	// client simply gets a fresh bucket.
	if len(l.limiters) > 10000 {
		cutoff := time.Now().Add(-time.Hour)
		for k, at := range l.seen {
			if at.Before(cutoff) {
				delete(l.limiters, k)
				delete(l.seen, k)
			}
		}
	}

	lim, ok := l.limiters[key]
	if !ok {
		lim = rate.NewLimiter(rate.Limit(float64(pushRatePerMinute)/60.0), pushRateBurst)
		l.limiters[key] = lim
	}
	l.seen[key] = time.Now()
	return lim.Allow()
}

// pushSubscribeRequest is the W3C PushSubscriptionJSON shape, so the page can
// post `subscription.toJSON()` with no reshaping.
//
// ExpirationTime is declared but unused. The browser always includes it (as
// null in practice, since no push service currently sets one), and the
// decoder rejects unknown fields -- deliberately, for an unauthenticated
// endpoint -- so omitting it here makes every real browser subscription fail
// with 400 while hand-written test payloads pass.
type pushSubscribeRequest struct {
	Endpoint       string `json:"endpoint"`
	ExpirationTime *int64 `json:"expirationTime"`
	Keys           struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

type pushUnsubscribeRequest struct {
	Endpoint string `json:"endpoint"`
}

// HandlePushSubscribe handles POST /public/status/{slug}/push/subscribe.
//
// This is the first unauthenticated write in the status-page service, so the
// guards are load-bearing rather than defensive habit: body cap, per-IP rate
// limit, per-page subscription cap, an https-only host allowlist on the stored
// endpoint, and cryptographic validation of the browser's keys.
func (h *Handlers) HandlePushSubscribe(w http.ResponseWriter, r *http.Request, slug string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Requiring JSON means a cross-origin browser POST must preflight, and no
	// CORS headers are emitted anywhere on these routes, so it fails. Same
	// origin XHR from the page is unaffected.
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	if !h.pushOriginOK(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if !h.pushLimiter.allow(clientIP(r, pushTrustProxy())) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "Too many requests", http.StatusTooManyRequests)
		return
	}

	var req pushSubscribeRequest
	if !decodePushBody(w, r, &req) {
		return
	}

	endpoint := strings.TrimSpace(req.Endpoint)
	if err := validatePushEndpoint(endpoint, pushEndpointHosts()); err != nil {
		http.Error(w, "Invalid push endpoint", http.StatusBadRequest)
		return
	}
	// Validate the keys now rather than discovering at send time that a
	// stored subscription can never be encrypted for.
	if _, err := webpush.Encrypt(webpush.Subscription{
		Endpoint: endpoint,
		P256dh:   strings.TrimSpace(req.Keys.P256dh),
		Auth:     strings.TrimSpace(req.Keys.Auth),
	}, []byte("{}")); err != nil {
		http.Error(w, "Invalid subscription keys", http.StatusBadRequest)
		return
	}

	ctx, cancel := pushRequestContext(r)
	defer cancel()

	pageID, ok := h.resolvePushPage(ctx, w, slug)
	if !ok {
		return
	}

	stored, err := h.pushStore.Subscribe(ctx, pageID, pushSubscriptionRow{
		Endpoint: endpoint,
		P256dh:   strings.TrimSpace(req.Keys.P256dh),
		Auth:     strings.TrimSpace(req.Keys.Auth),
	}, truncate(r.UserAgent(), 512), pushSubscriptionCap())
	if err != nil {
		h.logger.WithFields(map[string]interface{}{"error": err.Error(), "slug": slug}).
			Error("Failed to store push subscription")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if !stored {
		w.Header().Set("Retry-After", "3600")
		http.Error(w, "This status page is not accepting more notification subscriptions", http.StatusTooManyRequests)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// HandlePushUnsubscribe handles POST /public/status/{slug}/push/unsubscribe.
//
// Deliberately permissive compared to subscribe: no endpoint allowlist and no
// key validation, because refusing to forget something is worse than
// forgetting something that was never there. It always answers 204, so it
// cannot be used to probe which endpoints exist.
func (h *Handlers) HandlePushUnsubscribe(w http.ResponseWriter, r *http.Request, slug string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.pushOriginOK(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if !h.pushLimiter.allow(clientIP(r, pushTrustProxy())) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "Too many requests", http.StatusTooManyRequests)
		return
	}

	var req pushUnsubscribeRequest
	if !decodePushBody(w, r, &req) {
		return
	}

	ctx, cancel := pushRequestContext(r)
	defer cancel()

	// Unsubscribe stays available even when the page has since turned push
	// off, so existing subscribers can always opt out. It is a no-op when the
	// deployment has no push configured at all -- there is nothing stored to
	// remove, and the visitor still gets a clean 204.
	if h.pushStore == nil {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	pageID, _, err := h.pushStore.PageIDForSlug(ctx, slug)
	if err == nil && pageID != uuid.Nil {
		if err := h.pushStore.Unsubscribe(ctx, pageID, strings.TrimSpace(req.Endpoint)); err != nil {
			h.logger.WithFields(map[string]interface{}{"error": err.Error(), "slug": slug}).
				Warn("Failed to remove push subscription")
		}
	}

	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// resolvePushPage returns the page id when the slug exists, the page has opted
// in, and the deployment has VAPID keys. It answers 404 for every failure so
// an unauthenticated caller cannot tell "no such page" from "push is off".
func (h *Handlers) resolvePushPage(ctx context.Context, w http.ResponseWriter, slug string) (uuid.UUID, bool) {
	if h.webPushPublicKey() == "" || h.pushStore == nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return uuid.Nil, false
	}
	pageID, enabled, err := h.pushStore.PageIDForSlug(ctx, slug)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{"error": err.Error(), "slug": slug}).
			Error("Failed to resolve status page for push subscription")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return uuid.Nil, false
	}
	if pageID == uuid.Nil || !enabled {
		http.Error(w, "Not found", http.StatusNotFound)
		return uuid.Nil, false
	}
	return pageID, true
}

// pushOriginOK rejects a cross-origin Origin header. Requests without one
// (curl, same-origin form posts) are allowed through to the other guards.
func (h *Handlers) pushOriginOK(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func decodePushBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, pushSubscribeMaxBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
			return false
		}
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return false
	}
	return true
}

// clientIP keys the rate limiter. X-Forwarded-For is honoured only behind an
// explicit opt-in, because it is client-settable.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			if first := strings.TrimSpace(strings.Split(fwd, ",")[0]); first != "" {
				return first
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func pushTrustProxy() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(pushTrustProxyEnvVar)), "true")
}

func pushSubscriptionCap() int {
	if raw := strings.TrimSpace(os.Getenv(pushSubscriptionCapEnvVar)); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return defaultPushSubscriptionCap
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// HandlePushServiceWorker serves the push service worker at
// /public/status/sw.js.
//
// It is served even when push is not configured: a browser that already
// registered a worker polls this URL for updates, and a 404 there is not a
// clean way to retire it. The worker is inert without a subscription.
func (h *Handlers) HandlePushServiceWorker(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body := statustemplate.ServiceWorkerSource
	etag := `"` + serviceWorkerETag + `"`

	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	// Browsers byte-compare the worker script on every update check, so it
	// must revalidate rather than be served from cache -- otherwise a fixed
	// worker never reaches browsers that already have the old one.
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", etag)
	// Allows the client to register with a scope broader than this file's own
	// directory if it ever needs to; the client currently narrows instead.
	w.Header().Set("Service-Worker-Allowed", "/public/status/")

	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, serviceWorkerETag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.WriteString(w, body)
}

// serviceWorkerETag is the content hash of the embedded worker, computed once.
var serviceWorkerETag = func() string {
	sum := sha256.Sum256([]byte(statustemplate.ServiceWorkerSource))
	return hex.EncodeToString(sum[:16])
}()
