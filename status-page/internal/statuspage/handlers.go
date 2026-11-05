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
	// access is the visibility gate (access.go).
	access accessGate
}

// NewHandlers creates a new status page handlers
func NewHandlers(service *Service, cfg *config.StatusPageConfig, log *logger.Logger, hub *Hub, cache *renderCache, pushes *pushStore) *Handlers {
	h := &Handlers{
		service:     service,
		config:      cfg,
		logger:      log,
		hub:         hub,
		cache:       cache,
		pushStore:   pushes,
		pushLimiter: newPushRateLimiter(),
		clientIPs:   newClientIPResolver(cfg),
	}
	h.initAccess(service)
	return h
}

func newClientIPResolver(cfg *config.StatusPageConfig) *clientip.Resolver {
	if cfg == nil {
		return clientip.NewResolver(nil)
	}
	return clientip.NewResolver(cfg.TrustedProxyCIDRs)
}

// HandleStatusPage handles GET /public/status/{slug} and GET /public/status/{slug}/data
func (h *Handlers) HandleStatusPage(w http.ResponseWriter, r *http.Request) {
	// Extract slug from path
	path := strings.TrimPrefix(r.URL.Path, "/public/status/")
	if path == "" {
		http.Error(w, "Status page not found", http.StatusNotFound)
		return
	}

	// The visibility gate runs before everything below -- the method guard,
	// every sub-route and the slug-keyed render cache -- so a route added
	// here is gated without further work. /unlock is the one exemption
	// (access.go).
	gateSlug, gateRest := splitStatusPath(path)
	if gateRest == unlockSuffix {
		h.HandleUnlock(w, r, gateSlug)
		return
	}
	gated, ok := h.authorize(w, r, gateSlug, gateRest)
	if !ok {
		return
	}
	w = gated

	// The push routes are POST and do their own method checks. Every other
	// route below is read-only, so the GET guard moved down here rather than
	// staying above the sub-routing.
	isPushRoute := strings.HasSuffix(path, "/push/subscribe") || strings.HasSuffix(path, "/push/unsubscribe")
	if !isPushRoute && r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check if this is a data request
	if strings.HasSuffix(path, "/data") {
		h.HandleStatusPageData(w, r)
		return
	}

	// Check if this is a stream request
	if strings.HasSuffix(path, "/stream") {
		h.StreamStatusPage(w, r)
		return
	}

	// Check if this is a draft template preview request
	if strings.HasSuffix(path, "/preview/draft") {
		h.HandleDraftPreview(w, r, strings.TrimSuffix(path, "/preview/draft"))
		return
	}

	// Push subscription management. These are POST, so they are dispatched
	// here rather than above the method guard being moved -- see the guard at
	// the top of this function, which only ever sees the GET routes.
	if slug, ok := strings.CutSuffix(path, "/push/subscribe"); ok {
		h.HandlePushSubscribe(w, r, slug)
		return
	}
	if slug, ok := strings.CutSuffix(path, "/push/unsubscribe"); ok {
		h.HandlePushUnsubscribe(w, r, slug)
		return
	}

	// Page add-ons: summary.json, badges, embed.js and the Atom feed
	// (addons.go). Read-only GETs, dispatched here like every other
	// sub-route so whatever gates this function gates them too.
	if h.servePageAddon(w, r, path) {
		return
	}

	// Extract slug (remove trailing slash if any)
	slug := strings.TrimSuffix(path, "/")
	if slug == "" {
		http.Error(w, "Status page not found", http.StatusNotFound)
		return
	}

	page, etag, err := h.cachedPage(r.Context(), slug)
	if err != nil {
		h.writeStatusPageError(w, slug, err, "Failed to build status page")
		return
	}

	// no-cache (unlike no-store) lets the browser keep the body and
	// revalidate it with If-None-Match; a 304 then skips the ~150KB page.
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	// The CSP (and its per-response nonce) goes only on a full response; see
	// pageCSP.apply for why a 304 must not carry one.
	html := noIndexHTML(w, page.csp.apply(w.Header(), page.html))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write([]byte(html)); err != nil {
		h.logger.WithError(err).Error("Failed to write status page response")
	}
}

// cachedPage loads and renders slug through the per-slug render cache. The
// rendered HTML depends only on the slug (and process-constant config):
// theme, kiosk and filter handling are entirely client-side, and no query
// parameter or header reaches the renderer — so the slug is the whole cache
// key. The page and its add-ons (summary.json, badges) share one build.
//
// The build runs detached from this request's cancellation: with
// singleflight, one client disconnecting must not fail the render every
// concurrent waiter is collapsed onto. A timeout still bounds the load.
func (h *Handlers) cachedPage(parent context.Context, slug string) (renderedPage, string, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), statusPageBuildTimeout)
	defer cancel()
	return h.cache.GetPage(slug, func() (renderedPage, error) {
		data, err := h.service.GetStatusPageBySlug(ctx, slug)
		if err != nil {
			return renderedPage{}, err
		}
		data.PushPublicKey = h.webPushPublicKey()
		if h.access.store != nil {
			// Browser notifications are for public and unlisted pages only.
			// Visibility belongs to the page, so deciding it here keeps the
			// per-slug cache correct for every caller (page and add-ons).
			if acc, aerr := h.access.store.PageAccess(ctx, slug); aerr == nil && pageaccess.Gated(acc.Visibility) {
				data.PushPublicKey = ""
			}
		}
		data.CanonicalURL = h.canonicalURL(slug)
		html, customErr, err := renderStatusPageHTML(data, h.apiProxyEnabled())
		if customErr != nil {
			h.logger.WithFields(map[string]interface{}{
				"error": customErr.Error(),
				"slug":  slug,
			}).Warn("Custom status page template failed; serving built-in template")
		}
		csp := pageCSPFor(data, h.appBaseURL(), strings.TrimSpace(data.CustomTemplateSource) != "")
		return renderedPage{html: html, csp: csp, summary: buildPageSummary(data, time.Now())}, err
	})
}

// canonicalURL is the page's absolute public URL from STATUS_PAGE_BASE_URL,
// or "" when it is unset (the head then carries no canonical link or og:url
// rather than a guessed host).
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

// HandleDraftPreview handles GET /public/status/{slug}/preview/draft.
// It renders the page's draft template against live data, uncached, so the
// template editor can show authors exactly what a publish would produce. A
// page with no draft renders normally (published custom or built-in), and a
// broken draft returns its parse/execute error instead of falling back.
func (h *Handlers) HandleDraftPreview(w http.ResponseWriter, r *http.Request, slug string) {
	if slug == "" {
		http.Error(w, "Status page not found", http.StatusNotFound)
		return
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), statusPageBuildTimeout)
	defer cancel()

	pageID, source, hasDraft, err := h.service.GetDraftTemplateBySlug(ctx, slug)
	if err != nil {
		h.writeStatusPageError(w, slug, err, "Failed to load draft template")
		return
	}

	if secret := strings.TrimSpace(os.Getenv(previewSecretEnvVar)); secret != "" {
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
	data.PushPublicKey = h.webPushPublicKey()
	data.CanonicalURL = h.canonicalURL(slug)

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
		http.Error(w, "Status page not found", http.StatusNotFound)
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

	// Return JSON
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.logger.WithError(err).Error("Failed to encode JSON response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// StreamStatusPage handles GET /public/status/{slug}/stream
func (h *Handlers) StreamStatusPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	slug := slugFromPath(r, "/stream")
	if slug == "" {
		http.Error(w, "Status page not found", http.StatusNotFound)
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
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("X-Accel-Buffering", "no")

	// Register client
	clientChan := h.hub.Register(slug)
	defer h.hub.Unregister(slug, clientChan)

	// Send initial connection message
	fmt.Fprintf(w, "event: connected\ndata: {\"status\":\"connected\"}\n\n")
	flusher.Flush()

	// Heartbeat ticker
	heartbeatTicker := time.NewTicker(15 * time.Second)
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
			fmt.Fprintf(w, "event: heartbeat\ndata: {\"time\":\"%s\"}\n\n", time.Now().UTC().Format(time.RFC3339))
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// proxyStatusPageID is the page a /_sp_api call acts on, which both the
// access gate and the injected X-Tenant-ID use. A status-pages path names its
// page in the path, and that is the page the API reads, so the path decides:
// the X-Status-Page-ID header only identifies the page for calls whose path
// names none (the monitors list). ok is false for a status-pages path whose
// first segment is not a page ID, and for a header naming a different page
// than the path (or a path with dot segments), so the gate can never check
// one page while the API acts on another.
func proxyStatusPageID(r *http.Request, upstreamPath string) (uuid.UUID, bool) {
	var headerID uuid.UUID
	if spIDHeader := r.Header.Get("X-Status-Page-ID"); spIDHeader != "" {
		if parsed, err := uuid.Parse(spIDHeader); err == nil {
			headerID = parsed
		}
	}
	// A dot segment would let the upstream resolve the path somewhere other
	// than the prefix checked here.
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
