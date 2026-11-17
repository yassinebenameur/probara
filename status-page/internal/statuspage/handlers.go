package statuspage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/clientip"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/pageaccess"
	"github.com/yassinebenameur/probara/shared/statustemplate"
)

// statusPageBuildTimeout bounds a single load-and-render of a status page.
// It is intentionally below the server's 60s WriteTimeout safety net.
const statusPageBuildTimeout = 30 * time.Second

// streamHeartbeatInterval paces the SSE heartbeat, which is also when a
// gated page's stream re-checks the visitor's session. A variable for tests.
var streamHeartbeatInterval = 15 * time.Second

// Handlers handles status page HTTP requests
type Handlers struct {
	service *Service
	config  *config.StatusPageConfig
	logger  *logger.Logger
	hub     *Hub
	cache   *renderCache
	// pushStore is the package's only Postgres write path (see push_store.go).
	// Nil when browser notifications are not configured, which makes the push
	// routes 404.
	pushStore   *pushStore
	pushLimiter *pushRateLimiter
	// clientIPs keys pushLimiter: forwarding headers count only from
	// TRUSTED_PROXY_CIDRS peers, so a visitor cannot spoof a fresh bucket.
	clientIPs *clientip.Resolver
	// access is the visibility gate every page route passes (access.go).
	access *accessGate
	// subscribes throttles the email subscription routes per client and
	// page; subscribeClient forwards them to the API (subscribe.go).
	subscribes      *subscribeThrottle
	subscribeClient *http.Client
}

// NewHandlers creates a new status page handlers
func NewHandlers(service *Service, cfg *config.StatusPageConfig, log *logger.Logger, hub *Hub, cache *renderCache, pushes *pushStore) *Handlers {
	var store accessQuery
	if service != nil {
		store = service
	}
	secret := ""
	cookieSecure := cookieSecureDetect
	if cfg != nil {
		secret = cfg.SessionSecret
		if secure, detect := cfg.SessionCookieSecure(); !detect {
			cookieSecure = cookieSecureNever
			if secure {
				cookieSecure = cookieSecureAlways
			}
		}
	}
	return &Handlers{
		service:     service,
		config:      cfg,
		logger:      log,
		hub:         hub,
		cache:       cache,
		pushStore:   pushes,
		pushLimiter: newPushRateLimiter(),
		clientIPs:   newClientIPResolver(cfg),
		access:      newAccessGate(store, secret, cookieSecure),
		subscribes:  newSubscribeThrottle(),
		// No redirects: the API's answer is the status and body it sent.
		subscribeClient: &http.Client{
			Timeout:       subscribeForwardTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func newClientIPResolver(cfg *config.StatusPageConfig) *clientip.Resolver {
	if cfg == nil {
		return clientip.NewResolver(nil)
	}
	return clientip.NewResolver(cfg.TrustedProxyCIDRs)
}

// HandleStatusPage handles GET /public/status/{slug} and every sub-route of a
// page: /data, /stream, /preview/draft, /push/subscribe, /push/unsubscribe
// and /unlock. Every one but /unlock passes the visibility gate first.
func (h *Handlers) HandleStatusPage(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/public/status/")
	slug, rest := splitStatusPath(path)
	if slug == "" {
		writeNotFound(w)
		return
	}

	// The password form's target is the one sub-route the gate does not
	// guard: the gate is what sends visitors there.
	if rest == unlockSuffix {
		h.HandleUnlock(w, r, slug)
		return
	}

	// The visibility gate runs before the method guard, every sub-route and
	// the render cache (access.go). From here on, routes write through aw,
	// which forces the gate's noindex and no-store headers.
	aw, acc, ok := h.authorize(w, r, slug, rest)
	if !ok {
		return
	}
	gated := pageaccess.Gated(acc.Visibility)
	// Every route below reads the visibility from the request: cachedPage
	// builds with it (no push key on gated pages, noindex unless public),
	// and the add-ons answer privately for a gated page.
	r = withPageVisibility(r, acc.Visibility)
	if gated {
		r = gatedPageRequest(r)
	}

	// The push routes are POST and do their own method checks. Every other
	// route below is read-only, so the GET guard sits here rather than above
	// the sub-routing.
	isPushRoute := rest == "/push/subscribe" || rest == "/push/unsubscribe"
	// The email subscription routes (subscribe.go) check their own methods.
	isSubscriptionRoute := rest == subscribeSuffix || rest == subscriptionSuffix || rest == confirmSuffix || rest == unsubscribeSuffix
	if !isPushRoute && !isSubscriptionRoute && r.Method != http.MethodGet {
		http.Error(aw, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	switch rest {
	case "", "/":
		// The page itself, below.
	case "/data":
		h.HandleStatusPageData(aw, r)
		return
	case "/stream":
		h.streamStatusPage(aw, r, slug, gated)
		return
	case previewSuffix:
		h.HandleDraftPreview(aw, r, slug, gated)
		return
	case "/push/subscribe":
		h.HandlePushSubscribe(aw, r, slug)
		return
	case "/push/unsubscribe":
		h.HandlePushUnsubscribe(aw, r, slug)
		return
	default:
		// Email subscriptions (subscribe.go): forwarded to the API.
		if h.handleSubscription(aw, r, slug, rest, acc) {
			return
		}
		// Page add-ons (addons.go): summary.json, badges and the Atom feed.
		// They read the slug-keyed render and feed caches, so they are
		// served only from here, after authorize; on a gated page r carries
		// gatedPageRequest, so their responses are private and never
		// readable cross-origin.
		if h.servePageAddon(aw, r, path) {
			return
		}
		writeNotFound(w)
		return
	}

	// Who may see the page was decided above, by authorize, never by the
	// cache (cachedPage is keyed by slug alone).
	page, etag, err := h.cachedPage(r.Context(), slug)
	if err != nil {
		h.writeStatusPageError(aw, slug, err, "Failed to build status page")
		return
	}

	// Sent on a 304 too: unlike the CSP it has no per-response part, and the
	// header protects a custom template that prints no robots meta.
	if page.noindex {
		aw.Header().Set("X-Robots-Tag", robotsNoIndex)
	}

	// no-cache (unlike no-store) lets the browser keep the body and
	// revalidate it with If-None-Match; a 304 then skips the ~150KB page.
	// A gated page is no-store and has no validator (accessWriter), so it
	// never answers 304.
	if !gated {
		if pageaccess.NoIndex(acc.Visibility) {
			// An unlisted page's body also carries the robots meta tag added
			// below, so it must not revalidate against a copy stored while
			// the page was public.
			etag = strings.TrimSuffix(etag, `"`) + `-noindex"`
		}
		aw.Header().Set("ETag", etag)
		aw.Header().Set("Cache-Control", "no-cache")
		if etagMatches(r.Header.Get("If-None-Match"), etag) {
			aw.WriteHeader(http.StatusNotModified)
			return
		}
	}

	// The CSP (and its per-response nonce) goes only on a full response; see
	// pageCSP.apply for why a 304 must not carry one. A gated page may be
	// framed only by the operator UI, never by another page of this origin.
	csp := page.csp
	if gated {
		csp.frameAncestors = gatedFrameAncestors(h.appBaseURL())
	}
	html := csp.apply(aw.Header(), page.html)
	if pageaccess.NoIndex(acc.Visibility) {
		html = noIndexMeta(html)
	}
	aw.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := aw.Write([]byte(html)); err != nil {
		h.logger.WithError(err).Error("Failed to write status page response")
	}
}

// cachedPage loads and renders slug through the per-slug render cache. The
// rendered HTML depends only on the slug (and process-constant config):
// theme, kiosk and filter handling are entirely client-side, and no query
// parameter or header reaches the renderer — so the slug is the whole cache
// key. The page and its summary.json and badges share one build. Every
// caller must have passed the page's access checks before calling it: the
// cache is keyed by slug alone.
//
// The build runs detached from this request's cancellation: with
// singleflight, one client disconnecting must not fail the render every
// concurrent waiter is collapsed onto. A timeout still bounds the load.
func (h *Handlers) cachedPage(parent context.Context, slug string) (renderedPage, string, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), statusPageBuildTimeout)
	defer cancel()
	// Visibility comes from the access gate that ran first. An access change
	// invalidates the slug, its summary and its feed
	// (status_page_access_changed); an entry built under another visibility
	// (the event not here yet, or lost) is rebuilt on the spot, so a page
	// that turned private never serves HTML that says index or offers push.
	visibility := pageVisibility(parent)
	build := func() (renderedPage, error) {
		data, err := h.service.GetStatusPageBySlug(ctx, slug)
		if err != nil {
			return renderedPage{}, err
		}
		// No visitor push on password and private pages: a subscription
		// would outlive the session that made it.
		if !pageaccess.Gated(visibility) {
			data.PushPublicKey = h.webPushPublicKey()
		}
		data.Visibility = visibility
		data.CanonicalURL = h.canonicalURL(slug)
		data.SubscribeFormEnabled = h.subscriptionsOffered(visibility, data.EmailSubscriptionsEnabled)
		view := buildStatusPageRenderView(data, h.apiProxyEnabled())
		html, customErr, err := renderStatusPageView(data, view)
		if customErr != nil {
			h.logger.WithFields(map[string]interface{}{
				"error": customErr.Error(),
				"slug":  slug,
			}).Warn("Custom status page template failed; serving built-in template")
		}
		if err != nil {
			return renderedPage{}, err
		}
		summary := buildPageSummaryFromView(data, view, h.service.now())
		summaryJSON, summaryETag, err := encodeSummary(summary)
		if err != nil {
			return renderedPage{}, err
		}
		return renderedPage{
			html:        html,
			csp:         pageCSPFor(data, h.appBaseURL(), strings.TrimSpace(data.CustomTemplateSource) != ""),
			noindex:     !pageIndexable(data),
			summary:     summary,
			summaryJSON: summaryJSON,
			summaryETag: summaryETag,
			visibility:  visibility,
		}, nil
	}
	page, etag, err := h.cache.GetPage(slug, build)
	if err == nil && page.visibility != visibility {
		h.cache.Invalidate(slug)
		page, etag, err = h.cache.GetPage(slug, build)
	}
	return page, etag, err
}

// canonicalURL is the page's absolute public URL from STATUS_PAGE_BASE_URL,
// or "" when it is unset.
func (h *Handlers) canonicalURL(slug string) string {
	if h.config == nil {
		return ""
	}
	return publicPageURL(h.config.StatusPageBaseURL, slug)
}

// appBaseURL is APP_BASE_URL, the operator UI origin allowed to frame pages.
func (h *Handlers) appBaseURL() string {
	if h.config == nil {
		return ""
	}
	return h.config.AppBaseURL
}

func (h *Handlers) apiProxyEnabled() bool {
	return h.config != nil && strings.TrimSpace(h.config.APIBaseURL) != ""
}

// webPushPublicKey returns the deployment's VAPID application server key, or ""
// when browser notifications are not configured. The service layer has no
// access to config, so the handler injects it into StatusPageData before
// rendering; buildStatusPageRenderView ANDs it with the page's own setting.
func (h *Handlers) webPushPublicKey() string {
	if h.config == nil || !h.config.WebPushConfigured() {
		return ""
	}
	return h.config.VAPIDPublicKey
}

// previewSecretEnvVar guards the draft-preview route. When set (it must match
// the API's value so minted tokens verify), previews require a ?token=
// minted by the admin API; when unset previews are open, which is acceptable
// because a draft only ever renders data already public on the live page.
const previewSecretEnvVar = "STATUS_PAGE_PREVIEW_SECRET"

// previewSecret is STATUS_PAGE_PREVIEW_SECRET, "" when unset.
func previewSecret() string {
	return strings.TrimSpace(os.Getenv(previewSecretEnvVar))
}

// HandleDraftPreview handles GET /public/status/{slug}/preview/draft.
// It renders the page's draft template against live data, uncached, so the
// template editor can show authors exactly what a publish would produce. A
// page with no draft renders normally (published custom or built-in), and a
// broken draft returns its parse/execute error instead of falling back.
//
// On a password or private page authorize lets this route through with a
// visitor session or a valid preview token; gated turns visitor push off as
// on the live page.
func (h *Handlers) HandleDraftPreview(w http.ResponseWriter, r *http.Request, slug string, gated bool) {
	if slug == "" {
		writeNotFound(w)
		return
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), statusPageBuildTimeout)
	defer cancel()

	pageID, source, hasDraft, err := h.service.GetDraftTemplateBySlug(ctx, slug)
	if err != nil {
		h.writeStatusPageError(w, slug, err, "Failed to load draft template")
		return
	}

	if secret := previewSecret(); secret != "" {
		if !statustemplate.VerifyPreviewToken(secret, pageID.String(), r.URL.Query().Get("token"), time.Now()) {
			http.Error(w, "Invalid or expired preview token", http.StatusForbidden)
			return
		}
	}

	data, err := h.service.GetStatusPageBySlug(ctx, slug)
	if err != nil {
		h.writeStatusPageError(w, slug, err, "Failed to build draft preview")
		return
	}
	if !gated {
		data.PushPublicKey = h.webPushPublicKey()
	}
	data.CanonicalURL = h.canonicalURL(slug)
	data.Visibility = pageVisibility(r.Context())
	data.SubscribeFormEnabled = h.subscriptionsOffered(data.Visibility, data.EmailSubscriptionsEnabled)

	var html string
	if hasDraft {
		html, err = renderStatusPageWithSource(data, h.apiProxyEnabled(), source)
		if err != nil {
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, "Draft template error:\n\n"+err.Error(), http.StatusUnprocessableEntity)
			return
		}
	} else {
		html, err = renderPublicStatusPage(data, h.apiProxyEnabled())
		if err != nil {
			h.writeStatusPageError(w, slug, err, "Failed to render draft preview")
			return
		}
	}

	// The template editor frames this route. Without APP_BASE_URL the
	// service cannot name the editor's origin, so any ancestor may frame the
	// preview (it shows only data the live page already publishes).
	csp := pageCSPFor(data, h.appBaseURL(), hasDraft || strings.TrimSpace(data.CustomTemplateSource) != "")
	if h.appBaseURL() == "" {
		csp.frameAncestors = []string{"*"}
	}
	html = csp.apply(w.Header(), html)

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write([]byte(html)); err != nil {
		h.logger.WithError(err).Error("Failed to write draft preview response")
	}
}

// slugFromPath extracts the page slug from /public/status/{slug}{suffix}.
// It returns "" when the path carries no slug.
func slugFromPath(r *http.Request, suffix string) string {
	path := strings.TrimPrefix(r.URL.Path, "/public/status/")
	return strings.TrimSuffix(path, suffix)
}

// writeStatusPageError answers a failed page load: 404 for an unknown slug,
// otherwise a logged 500. what is the log line for the failed operation.
func (h *Handlers) writeStatusPageError(w http.ResponseWriter, slug string, err error, what string) {
	if errors.Is(err, ErrStatusPageNotFound) {
		writeNotFound(w)
		return
	}
	h.logger.WithFields(map[string]interface{}{
		"error": err.Error(),
		"slug":  slug,
	}).Error(what)
	http.Error(w, "Internal server error", http.StatusInternalServerError)
}

// etagMatches reports whether the If-None-Match header value matches etag.
// Comma-separated candidate lists and the "*" wildcard are honored; a weak
// validator prefix (W/) is tolerated since the body comparison is exact.
func etagMatches(ifNoneMatch, etag string) bool {
	if ifNoneMatch == "" || etag == "" {
		return false
	}
	for _, candidate := range strings.Split(ifNoneMatch, ",") {
		candidate = strings.TrimSpace(candidate)
		candidate = strings.TrimPrefix(candidate, "W/")
		if candidate == "*" || candidate == etag {
			return true
		}
	}
	return false
}

// HandleStatusPageData handles GET /public/status/{slug}/data
func (h *Handlers) HandleStatusPageData(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	slug := slugFromPath(r, "/data")
	if slug == "" {
		http.Error(w, "Status page not found", http.StatusNotFound)
		return
	}

	data, err := h.service.GetStatusPageBySlug(r.Context(), slug)
	if err != nil {
		h.writeStatusPageError(w, slug, err, "Failed to get status page")
		return
	}

	// Only what the page's settings show (data_view.go).
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(publicPageData(data)); err != nil {
		h.logger.WithError(err).Error("Failed to encode JSON response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// streamStatusPage handles GET /public/status/{slug}/stream, after
// authorize. Every stream re-runs the gate on each heartbeat and ends once it
// no longer opens the page (the page turned password or private, the session
// expired, the password changed, the token was revoked), so an open stream
// never outlives its access even when the access-change event is lost.
func (h *Handlers) streamStatusPage(w http.ResponseWriter, r *http.Request, slug string, gated bool) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if slug == "" {
		writeNotFound(w)
		return
	}

	// Check if hub is initialized
	if h.hub == nil {
		http.Error(w, "Streaming not available", http.StatusServiceUnavailable)
		return
	}

	// Check if the ResponseWriter supports flushing
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	if !gated {
		// A gated stream is for the page's own (same-origin) visitors only.
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}
	w.Header().Set("X-Accel-Buffering", "no")

	// Register client
	clientChan := h.hub.Register(slug)
	defer h.hub.Unregister(slug, clientChan)

	// Send initial connection message
	fmt.Fprintf(w, "event: connected\ndata: {\"status\":\"connected\"}\n\n")
	flusher.Flush()

	// Heartbeat ticker
	heartbeatTicker := time.NewTicker(streamHeartbeatInterval)
	defer heartbeatTicker.Stop()

	for {
		select {
		case event, ok := <-clientChan:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, event.Data)
			flusher.Flush()
		case <-heartbeatTicker.C:
			if !h.stillAuthorized(r.Context(), r, slug) {
				return
			}
			fmt.Fprintf(w, "event: heartbeat\ndata: {\"time\":\"%s\"}\n\n", time.Now().UTC().Format(time.RFC3339))
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// proxyStatusPageID is the page a /_sp_api call acts on, which both the
// access gate and the injected X-Tenant-ID use. A status-pages path names its
// page in the path, and that is the page the API reads, so the path decides;
// the X-Status-Page-ID header only names the page for calls whose path names
// none (the monitors list). ok is false for a status-pages path whose first
// segment is not a page ID, for a header naming a different page than the
// path, and for a path with dot segments, so the gate can never check one
// page while the API acts on another.
func proxyStatusPageID(r *http.Request, upstreamPath string) (uuid.UUID, bool) {
	var headerID uuid.UUID
	if spIDHeader := r.Header.Get("X-Status-Page-ID"); spIDHeader != "" {
		if parsed, err := uuid.Parse(spIDHeader); err == nil {
			headerID = parsed
		}
	}
	for _, seg := range strings.Split(upstreamPath, "/") {
		if seg == "." || seg == ".." {
			return uuid.Nil, false
		}
	}
	rest, named := strings.CutPrefix(upstreamPath, "/api/v1/status-pages/")
	if !named {
		return headerID, true
	}
	first, _, _ := strings.Cut(rest, "/")
	pathID, err := uuid.Parse(first)
	if err != nil || pathID == uuid.Nil {
		return uuid.Nil, false
	}
	if headerID != uuid.Nil && headerID != pathID {
		return uuid.Nil, false
	}
	return pathID, true
}

// HandleAPIProxy proxies a small subset of API calls to the API service.
//
// This exists so the public status page can host an in-page "customizer" UI while the API
// runs on a different origin/port in dev (e.g. :8080 vs :8082), avoiding CORS issues.
//
// Route: /_sp_api/*
func (h *Handlers) HandleAPIProxy(w http.ResponseWriter, r *http.Request) {
	if h.config == nil || h.config.APIBaseURL == "" {
		http.NotFound(w, r)
		return
	}

	// Only allow a small set of endpoints/methods.
	if r.Method != http.MethodGet && r.Method != http.MethodPatch {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	upstreamPath := strings.TrimPrefix(r.URL.Path, "/_sp_api")
	if upstreamPath == "" || !strings.HasPrefix(upstreamPath, "/api/v1/") {
		http.NotFound(w, r)
		return
	}

	allowed := false
	if r.Method == http.MethodGet && strings.HasPrefix(upstreamPath, "/api/v1/monitors") {
		allowed = true
	}
	if r.Method == http.MethodPatch && strings.HasPrefix(upstreamPath, "/api/v1/status-pages/") {
		allowed = true
	}

	if !allowed {
		http.NotFound(w, r)
		return
	}

	target, err := url.Parse(h.config.APIBaseURL)
	if err != nil {
		h.logger.WithError(err).Error("Invalid STATUS_PAGE_API_BASE_URL")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// If the caller includes a status page id, resolve its tenant id and inject X-Tenant-ID
	// so admin-cookie auth (which is global) can operate tenant-scoped endpoints.
	statusPageID, ok := proxyStatusPageID(r, upstreamPath)
	if !ok {
		http.NotFound(w, r)
		return
	}
	// A password or private page is not reachable through the proxy.
	if !h.authorizeProxy(w, r, statusPageID) {
		return
	}

	tenantHeader := ""
	if statusPageID != uuid.Nil {
		if tenantID, err := h.service.GetTenantIDByStatusPageID(r.Context(), statusPageID); err == nil {
			tenantHeader = tenantID.String()
		}
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	defaultDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		// Rewrite path before the default director joins base/path.
		req.URL.Path = upstreamPath
		defaultDirector(req)
		req.Host = target.Host

		// Strip internal header and inject tenant scope if available.
		req.Header.Del("X-Status-Page-ID")
		if tenantHeader != "" {
			req.Header.Set("X-Tenant-ID", tenantHeader)
		}
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		h.logger.WithFields(map[string]interface{}{
			"error":  err.Error(),
			"path":   r.URL.Path,
			"method": r.Method,
		}).Error("API proxy error")
		http.Error(w, "Bad gateway", http.StatusBadGateway)
	}

	proxy.ServeHTTP(w, r)
}
