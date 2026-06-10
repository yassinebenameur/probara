package statuspage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
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
}

// NewHandlers creates a new status page handlers
func NewHandlers(service *Service, cfg *config.StatusPageConfig, log *logger.Logger, hub *Hub, cache *renderCache) *Handlers {
	return &Handlers{
		service: service,
		config:  cfg,
		logger:  log,
		hub:     hub,
		cache:   cache,
	}
}

// HandleStatusPage handles GET /public/status/{slug} and GET /public/status/{slug}/data
func (h *Handlers) HandleStatusPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract slug from path
	path := strings.TrimPrefix(r.URL.Path, "/public/status/")
	if path == "" {
		http.Error(w, "Status page not found", http.StatusNotFound)
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

	// Extract slug (remove trailing slash if any)
	slug := strings.TrimSuffix(path, "/")
	if slug == "" {
		http.Error(w, "Status page not found", http.StatusNotFound)
		return
	}

	// Load and render through the per-slug render cache. The rendered HTML
	// depends only on the slug (and process-constant config): theme, kiosk and
	// filter handling are entirely client-side, and no query parameter or
	// header reaches the renderer — so the slug is the whole cache key.
	//
	// The build runs detached from this request's cancellation: with
	// singleflight, one client disconnecting must not fail the render every
	// concurrent waiter is collapsed onto. A timeout still bounds the load.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), statusPageBuildTimeout)
	defer cancel()
	html, etag, err := h.cache.Get(slug, func() (string, error) {
		data, err := h.service.GetStatusPageBySlug(ctx, slug)
		if err != nil {
			return "", err
		}
		return renderPublicStatusPage(data, h.config != nil && strings.TrimSpace(h.config.APIBaseURL) != "")
	})
	if err != nil {
		if err.Error() == "status page not found" {
			http.Error(w, "Status page not found", http.StatusNotFound)
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error": err.Error(),
			"slug":  slug,
		}).Error("Failed to build status page")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
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

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write([]byte(html)); err != nil {
		h.logger.WithError(err).Error("Failed to write status page response")
	}
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

	// Extract slug from path
	path := strings.TrimPrefix(r.URL.Path, "/public/status/")
	slug := strings.TrimSuffix(path, "/data")
	if slug == "" {
		http.Error(w, "Status page not found", http.StatusNotFound)
		return
	}

	// Get status page data
	ctx := r.Context()
	data, err := h.service.GetStatusPageBySlug(ctx, slug)
	if err != nil {
		if err.Error() == "status page not found" {
			http.Error(w, "Status page not found", http.StatusNotFound)
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error": err.Error(),
			"slug":  slug,
		}).Error("Failed to get status page")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
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

	// Extract slug from path
	path := strings.TrimPrefix(r.URL.Path, "/public/status/")
	slug := strings.TrimSuffix(path, "/stream")
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
	var statusPageID uuid.UUID
	if spIDHeader := r.Header.Get("X-Status-Page-ID"); spIDHeader != "" {
		if parsed, err := uuid.Parse(spIDHeader); err == nil {
			statusPageID = parsed
		}
	}
	if statusPageID == uuid.Nil && strings.HasPrefix(upstreamPath, "/api/v1/status-pages/") {
		parts := strings.Split(strings.TrimPrefix(upstreamPath, "/api/v1/status-pages/"), "/")
		if len(parts) > 0 {
			if parsed, err := uuid.Parse(parts[0]); err == nil {
				statusPageID = parsed
			}
		}
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

// statusPageTemplate is the embedded HTML template for status pages
const statusPageTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <title>{{.Title}} – Status</title>
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <link rel="preconnect" href="https://fonts.googleapis.com">
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
  <link href="https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@400;500&family=Outfit:wght@400;500;600;700&display=swap" rel="stylesheet">
  <style>
    :root {
      --bg: #0a0a0f;
      --bg-gradient: linear-gradient(145deg, #0f0f18 0%, #0a0a0f 50%, #050508 100%);
      --surface: #12121a;
      --surface-elevated: #18182a;
      --surface-hover: #1e1e32;
      --border: rgba(255, 255, 255, 0.06);
      --border-hover: rgba(255, 255, 255, 0.12);
      --border-accent: rgba(99, 102, 241, 0.4);
      --text: #f4f4f5;
      --text-secondary: #a1a1aa;
      --text-muted: #71717a;
      --accent: #6366f1;
      --accent-soft: rgba(99, 102, 241, 0.15);
      --success: #10b981;
      --success-soft: rgba(16, 185, 129, 0.12);
      --success-glow: rgba(16, 185, 129, 0.25);
      --danger: #ef4444;
      --danger-soft: rgba(239, 68, 68, 0.12);
      --danger-glow: rgba(239, 68, 68, 0.25);
      --warning: #f59e0b;
      --warning-soft: rgba(245, 158, 11, 0.12);
      --cyan: #06b6d4;
      --cyan-soft: rgba(6, 182, 212, 0.12);
      --radius-sm: 8px;
      --radius-md: 12px;
      --radius-lg: 16px;
      --radius-xl: 20px;
      --shadow-sm: 0 2px 8px rgba(0, 0, 0, 0.3);
      --shadow-md: 0 8px 24px rgba(0, 0, 0, 0.4);
      --shadow-lg: 0 16px 48px rgba(0, 0, 0, 0.5);
      --transition: 0.2s cubic-bezier(0.4, 0, 0.2, 1);

      /* Custom brand colors from status page settings */
      {{if .PrimaryColor}}--brand-primary: {{.PrimaryColor}};{{end}}
      {{if .SecondaryColor}}--brand-secondary: {{.SecondaryColor}};{{end}}

      /* Agent accent colors */
      --cyan: #06b6d4;
      --cyan-soft: rgba(6, 182, 212, 0.15);
      --font-sans: 'Outfit', system-ui, -apple-system, sans-serif;
      --font-mono: 'JetBrains Mono', ui-monospace, monospace;
    }

    * { box-sizing: border-box; margin: 0; padding: 0; }
    html, body { height: 100%; }

    body {
      font-family: var(--font-sans);
      background: var(--bg);
      background-image: var(--bg-gradient);
      color: var(--text);
      -webkit-font-smoothing: antialiased;
      line-height: 1.5;
    }

    .page {
      min-height: 100vh;
      position: relative;
    }

    .page::before {
      content: "";
      position: fixed;
      top: 0;
      left: 0;
      right: 0;
      height: 500px;
      background: radial-gradient(ellipse 80% 50% at 50% -20%, rgba(99, 102, 241, 0.15), transparent);
      pointer-events: none;
      z-index: 0;
    }

    .page-inner {
      max-width: 900px;
      margin: 0 auto;
      padding: 32px 20px 48px;
      position: relative;
      z-index: 1;
    }

    /* HEADER */
    .header {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 16px;
      margin-bottom: 28px;
    }

    .brand {
      display: flex;
      align-items: center;
      gap: 14px;
    }

    .brand-logo {
      width: 42px;
      height: 42px;
      border-radius: var(--radius-md);
      background: linear-gradient(135deg, var(--brand-primary, var(--accent)) 0%, var(--cyan) 100%);
      display: flex;
      align-items: center;
      justify-content: center;
      box-shadow: 0 0 20px color-mix(in srgb, var(--brand-primary, var(--accent)) 30%, transparent);
    }

    .brand-logo-inner {
      width: 20px;
      height: 20px;
      border-radius: 50%;
      background: var(--bg);
      box-shadow: inset 0 0 0 2px rgba(255,255,255,0.2);
    }

    .brand-logo-img {
      width: 42px;
      height: 42px;
      border-radius: var(--radius-md);
      object-fit: cover;
    }

    .brand-name {
      font-size: 1.25rem;
      font-weight: 600;
      letter-spacing: -0.02em;
    }

    .brand-subline {
      font-size: 0.8rem;
      color: var(--text-muted);
      margin-top: 2px;
    }

    .header-meta {
      text-align: right;
      font-size: 0.75rem;
      color: var(--text-muted);
    }

    .header-btn {
      margin-top: 6px;
      padding: 6px 12px;
      border-radius: var(--radius-sm);
      border: 1px solid var(--border);
      background: var(--surface);
      color: var(--text-secondary);
      font-size: 0.75rem;
      font-family: var(--font-sans);
      cursor: pointer;
      transition: var(--transition);
    }

    .header-btn:hover {
      border-color: var(--border-hover);
      background: var(--surface-elevated);
      color: var(--text);
    }

    /* STATUS BANNER */
    .status-banner {
      border-radius: var(--radius-xl);
      padding: 20px 24px;
      margin-bottom: 28px;
      background: linear-gradient(135deg, rgba(16, 185, 129, 0.08) 0%, var(--surface) 100%);
      border: 1px solid rgba(16, 185, 129, 0.2);
      box-shadow: var(--shadow-md), 0 0 40px var(--success-glow);
      display: flex;
      justify-content: space-between;
      align-items: center;
      gap: 16px;
      position: relative;
      overflow: hidden;
    }

    .status-banner.has-issues {
      background: linear-gradient(135deg, rgba(239, 68, 68, 0.08) 0%, var(--surface) 100%);
      border-color: rgba(239, 68, 68, 0.3);
      box-shadow: var(--shadow-md), 0 0 40px var(--danger-glow);
    }

    .status-banner-main {
      display: flex;
      align-items: center;
      gap: 14px;
    }

    .status-indicator {
      width: 14px;
      height: 14px;
      border-radius: 50%;
      background: var(--success);
      box-shadow: 0 0 0 4px var(--success-soft), 0 0 20px var(--success);
      animation: pulse 2s ease-in-out infinite;
    }

    .status-banner.has-issues .status-indicator {
      background: var(--danger);
      box-shadow: 0 0 0 4px var(--danger-soft), 0 0 20px var(--danger);
    }

    @keyframes pulse {
      0%, 100% { transform: scale(1); opacity: 1; }
      50% { transform: scale(1.1); opacity: 0.8; }
    }

    .status-title {
      font-size: 1rem;
      font-weight: 600;
      color: var(--text);
    }

    .status-subtitle {
      font-size: 0.8rem;
      color: var(--success);
      margin-top: 2px;
    }

    .status-banner.has-issues .status-subtitle {
      color: var(--danger);
    }

    .status-time {
      font-size: 0.75rem;
      color: var(--text-muted);
      font-family: var(--font-mono);
    }

    /* SECTION HEADER */
    .section-header {
      display: flex;
      justify-content: space-between;
      align-items: center;
      margin-bottom: 16px;
    }

    .section-title {
      font-size: 0.9rem;
      font-weight: 600;
      color: var(--text);
    }

    .section-subtitle {
      font-size: 0.75rem;
      color: var(--text-muted);
      margin-top: 2px;
    }

    .section-controls {
      display: flex;
      align-items: center;
      gap: 0;
    }

    .filter-pills {
      display: flex;
      gap: 6px;
    }

    .filter-pill {
      padding: 5px 10px;
      border-radius: var(--radius-sm);
      border: 1px solid var(--border);
      background: transparent;
      color: var(--text-muted);
      font-size: 0.7rem;
      font-family: var(--font-sans);
      cursor: pointer;
      transition: var(--transition);
    }

    .filter-pill:hover {
      border-color: var(--border-hover);
      color: var(--text-secondary);
    }

    .filter-pill.active {
      border-color: var(--border-accent);
      background: var(--accent-soft);
      color: var(--text);
    }

    /* VIEW TOGGLE */
    .view-toggle {
      display: flex;
      gap: 4px;
      margin-left: 12px;
      padding-left: 12px;
      border-left: 1px solid var(--border);
    }

    .view-btn {
      width: 32px;
      height: 32px;
      display: flex;
      align-items: center;
      justify-content: center;
      border-radius: var(--radius-sm);
      border: 1px solid var(--border);
      background: transparent;
      color: var(--text-muted);
      cursor: pointer;
      transition: var(--transition);
    }

    .view-btn:hover {
      border-color: var(--border-hover);
      color: var(--text-secondary);
    }

    .view-btn.active {
      border-color: var(--border-accent);
      background: var(--accent-soft);
      color: var(--text);
    }

    .view-btn svg {
      width: 16px;
      height: 16px;
    }

    /* COMPONENT CARDS */
    .components-section {
      margin-bottom: 28px;
    }

    .component-cards {
      display: flex;
      flex-direction: column;
      gap: 12px;
    }

    .component-card {
      background: var(--surface);
      border: 1px solid var(--border);
      border-radius: var(--radius-lg);
      padding: 16px 20px;
      transition: var(--transition);
    }

    .component-card:hover {
      border-color: var(--border-hover);
      background: var(--surface-elevated);
    }

    .component-header {
      display: flex;
      justify-content: space-between;
      align-items: flex-start;
      gap: 12px;
      margin-bottom: 14px;
    }

    .component-info {
      flex: 1;
      min-width: 0;
    }

    .component-name {
      font-size: 0.9rem;
      font-weight: 600;
      color: var(--text);
      margin-bottom: 2px;
    }

    .component-url {
      font-size: 0.72rem;
      color: var(--text-muted);
      font-family: var(--font-mono);
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }

    .component-tags {
      margin-top: 6px;
      display: flex;
      flex-wrap: wrap;
      gap: 6px;
    }

    .component-tags .tag {
      font-size: 0.68rem;
      color: var(--text-secondary);
      border: 1px solid var(--border);
      background: color-mix(in srgb, var(--surface-elevated) 80%, transparent);
      padding: 2px 8px;
      border-radius: 999px;
      line-height: 1.4;
    }

    .component-meta {
      display: flex;
      align-items: center;
      gap: 12px;
      flex-shrink: 0;
    }

    .component-latency {
      font-size: 0.75rem;
      font-family: var(--font-mono);
      color: var(--text-secondary);
      padding: 4px 8px;
      background: var(--surface-elevated);
      border-radius: var(--radius-sm);
    }

    .component-tls {
      display: flex;
      align-items: center;
      gap: 6px;
      font-size: 0.72rem;
      font-family: var(--font-mono);
      color: var(--text-secondary);
      padding: 4px 8px;
      background: var(--surface-elevated);
      border-radius: var(--radius-sm);
    }

    .component-tls-label {
      font-size: 0.6rem;
      letter-spacing: 0.08em;
      text-transform: uppercase;
      color: var(--text-muted);
    }

    .status-badge {
      display: inline-flex;
      align-items: center;
      gap: 6px;
      padding: 5px 10px;
      border-radius: var(--radius-sm);
      font-size: 0.72rem;
      font-weight: 500;
    }

    .status-badge .dot {
      width: 6px;
      height: 6px;
      border-radius: 50%;
      background: currentColor;
    }

    .status-badge.up {
      background: var(--success-soft);
      color: var(--success);
    }

    .status-badge.down {
      background: var(--danger-soft);
      color: var(--danger);
    }

    .status-badge.error {
      background: var(--warning-soft);
      color: var(--warning);
    }

    .status-badge.unknown {
      background: rgba(113, 113, 122, 0.15);
      color: var(--text-muted);
    }

    /* STATS ROW */
    .component-stats {
      display: grid;
      grid-template-columns: repeat(4, 1fr);
      gap: 12px;
      margin-bottom: 16px;
      padding: 12px;
      background: rgba(0, 0, 0, 0.2);
      border-radius: var(--radius-md);
    }

    .stat-item {
      text-align: center;
    }

    .stat-value {
      font-size: 1rem;
      font-weight: 600;
      font-family: var(--font-mono);
      color: var(--text);
    }

    .stat-value.success { color: var(--success); }
    .stat-value.warning { color: var(--warning); }
    .stat-value.danger { color: var(--danger); }

    .stat-label {
      font-size: 0.65rem;
      color: var(--text-muted);
      text-transform: uppercase;
      letter-spacing: 0.05em;
      margin-top: 2px;
    }

    /* UPTIME BARS */
    .component-uptime {
      margin-bottom: 14px;
    }

    .uptime-bars-label {
      font-size: 0.7rem;
      color: var(--text-muted);
      margin-bottom: 8px;
      display: flex;
      justify-content: space-between;
    }

    .uptime-bars {
      display: flex;
      gap: 2px;
      height: 28px;
    }

    .uptime-bar {
      flex: 1;
      border-radius: 3px;
      background: rgba(255, 255, 255, 0.05);
      position: relative;
      cursor: pointer;
      transition: var(--transition);
    }

    .uptime-bar:hover {
      transform: scaleY(1.1);
    }

    .uptime-bar-fill {
      position: absolute;
      bottom: 0;
      left: 0;
      right: 0;
      border-radius: 3px;
      transition: var(--transition);
    }

    .uptime-bar-fill.good { background: var(--success); }
    .uptime-bar-fill.warning { background: var(--warning); }
    .uptime-bar-fill.bad { background: var(--danger); }
    .uptime-bar-fill.nodata { background: rgba(113, 113, 122, 0.4); }

    /* UPTIME RANGE CONTROLS */
    .uptime-bars-header {
      display: flex;
      justify-content: space-between;
      align-items: center;
      margin-bottom: 8px;
    }

    .uptime-bars-label {
      display: flex;
      flex-direction: column;
      gap: 2px;
    }

    .uptime-range-label {
      font-size: 0.75rem;
      color: var(--text-secondary);
    }

    .uptime-bar-hint {
      font-size: 0.65rem;
      color: var(--text-muted);
    }

    .uptime-range-btns {
      display: flex;
      gap: 4px;
    }

    .range-btn {
      padding: 3px 8px;
      border-radius: var(--radius-sm);
      border: 1px solid var(--border);
      background: transparent;
      color: var(--text-muted);
      font-size: 0.65rem;
      font-family: var(--font-mono);
      cursor: pointer;
      transition: var(--transition);
    }

    .range-btn:hover {
      border-color: var(--border-hover);
      color: var(--text-secondary);
    }

    .range-btn.active {
      border-color: var(--border-accent);
      background: var(--accent-soft);
      color: var(--text);
    }

    /* COMPACT VIEW MODE */
    .components-section.compact-active {
      margin-left: -40px;
      margin-right: -40px;
      padding-left: 40px;
      padding-right: 40px;
    }

    .component-cards.compact {
      display: grid;
      grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
      gap: 6px;
    }

    .component-card.compact-mode {
      padding: 8px 10px;
      border-radius: var(--radius-sm);
    }

    .component-card.compact-mode .component-header {
      margin-bottom: 0;
      flex-direction: row;
      align-items: center;
    }

    .component-card.compact-mode .component-info {
      display: flex;
      flex-direction: column;
      gap: 1px;
    }

    .component-card.compact-mode .component-name {
      font-size: 0.72rem;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
      display: flex;
      align-items: center;
      gap: 4px;
    }

    .component-card.compact-mode .component-name .agent-badge {
      font-size: 0.55rem;
      padding: 1px 3px;
    }

    .component-card.compact-mode .component-url {
      display: none;
    }

    .component-card.compact-mode .component-meta {
      flex-direction: column;
      align-items: flex-end;
      gap: 2px;
    }

    .component-card.compact-mode .component-latency {
      display: none;
    }

    .component-card.compact-mode .status-badge {
      padding: 2px 5px;
      font-size: 0.6rem;
    }

    .component-card.compact-mode .status-badge .dot {
      width: 5px;
      height: 5px;
    }

    .component-card.compact-mode .compact-uptime {
      font-size: 0.62rem;
      font-family: var(--font-mono);
      color: var(--text-secondary);
    }

    .component-card.compact-mode .component-stats,
    .component-card.compact-mode .component-uptime,
    .component-card.compact-mode .latency-chart-container,
    .component-card.compact-mode .group-latency-summary,
    .component-card.compact-mode .component-tls,
    .component-card.compact-mode .agent-metrics {
      display: none;
    }

    /* Show compact uptime only in compact mode */
    .compact-uptime {
      display: none;
    }

    .component-card.compact-mode .compact-uptime {
      display: block;
    }

    /* Fullscreen button */
    .fullscreen-btn {
      width: 32px;
      height: 32px;
      display: none;
      align-items: center;
      justify-content: center;
      border-radius: var(--radius-sm);
      border: 1px solid var(--border);
      background: transparent;
      color: var(--text-muted);
      cursor: pointer;
      transition: var(--transition);
      margin-left: 8px;
    }

    .fullscreen-btn:hover {
      border-color: var(--border-hover);
      color: var(--text-secondary);
    }

    .fullscreen-btn svg {
      width: 16px;
      height: 16px;
    }

    .compact-active .fullscreen-btn {
      display: flex;
    }

    /* Fullscreen mode */
    .compact-fullscreen {
      position: fixed;
      top: 0;
      left: 0;
      right: 0;
      bottom: 0;
      z-index: 9999;
      background: var(--bg);
      background-image: var(--bg-gradient);
      padding: 16px;
      overflow: auto;
      display: flex;
      flex-direction: column;
    }

    .compact-fullscreen .section-header {
      flex-shrink: 0;
      margin-bottom: 12px;
    }

    .compact-fullscreen .component-cards {
      flex: 1;
      align-content: start;
    }

    .compact-fullscreen .fullscreen-btn svg.expand-icon {
      display: none;
    }

    .compact-fullscreen .fullscreen-btn svg.collapse-icon {
      display: block;
    }

    .fullscreen-btn svg.collapse-icon {
      display: none;
    }

    @media (max-width: 640px) {
      .component-cards.compact {
        grid-template-columns: repeat(auto-fill, minmax(120px, 1fr));
      }
      .components-section.compact-active {
        margin-left: -16px;
        margin-right: -16px;
        padding-left: 16px;
        padding-right: 16px;
      }
    }

    /* AGENT METRICS */
    .agent-metrics {
      display: grid;
      grid-template-columns: repeat(3, 1fr);
      gap: 12px;
      margin-bottom: 16px;
    }

    .agent-metric-card {
      background: rgba(0, 0, 0, 0.25);
      border-radius: var(--radius-md);
      padding: 14px;
      display: flex;
      flex-direction: column;
      gap: 8px;
    }

    .agent-metric-header {
      display: flex;
      justify-content: space-between;
      align-items: center;
    }

    .agent-metric-label {
      font-size: 0.7rem;
      color: var(--text-muted);
      text-transform: uppercase;
      letter-spacing: 0.05em;
    }

    .agent-metric-value {
      font-size: 1.25rem;
      font-weight: 600;
      font-family: var(--font-mono);
      color: var(--text);
    }

    .agent-metric-value.success { color: var(--success); }
    .agent-metric-value.warning { color: var(--warning); }
    .agent-metric-value.danger { color: var(--danger); }

    .agent-metric-bar {
      height: 4px;
      background: rgba(255, 255, 255, 0.1);
      border-radius: 2px;
      overflow: hidden;
    }

    .agent-metric-bar-fill {
      height: 100%;
      border-radius: 2px;
      transition: width 0.3s ease;
    }

    .agent-metric-bar-fill.success { background: var(--success); }
    .agent-metric-bar-fill.warning { background: var(--warning); }
    .agent-metric-bar-fill.danger { background: var(--danger); }

    .agent-metric-detail {
      font-size: 0.65rem;
      color: var(--text-muted);
      font-family: var(--font-mono);
    }

    .agent-load-values {
      display: flex;
      gap: 12px;
      margin-top: 4px;
    }

    .agent-load-item {
      display: flex;
      flex-direction: column;
      align-items: center;
      gap: 2px;
    }

    .agent-load-period {
      font-size: 0.6rem;
      color: var(--text-muted);
    }

    .agent-load-value {
      font-size: 0.85rem;
      font-family: var(--font-mono);
      color: var(--text-secondary);
    }

    .agent-badge {
      display: inline-flex;
      align-items: center;
      gap: 4px;
      padding: 2px 6px;
      border-radius: 4px;
      background: var(--cyan-soft);
      color: var(--cyan);
      font-size: 0.6rem;
      font-weight: 500;
      text-transform: uppercase;
      letter-spacing: 0.05em;
    }

    /* GROUP LATENCY SUMMARY */
    .group-latency-summary {
      background: rgba(0, 0, 0, 0.2);
      border-radius: var(--radius-md);
      padding: 12px 14px;
      border: 1px solid rgba(255, 255, 255, 0.05);
    }

    .group-latency-summary-header {
      display: flex;
      align-items: center;
      justify-content: space-between;
      margin-bottom: 10px;
    }

    .group-latency-summary-title {
      font-size: 0.7rem;
      color: var(--text-muted);
      text-transform: uppercase;
      letter-spacing: 0.03em;
    }

    .group-latency-summary-table {
      display: grid;
      gap: 6px;
    }

    .group-latency-summary-row {
      display: grid;
      grid-template-columns: 56px repeat(3, minmax(0, 1fr));
      align-items: center;
      gap: 10px;
    }

    .group-latency-summary-row--header {
      padding-bottom: 6px;
      border-bottom: 1px solid rgba(255, 255, 255, 0.06);
    }

    .group-latency-summary-cell {
      font-size: 0.75rem;
      font-family: var(--font-mono);
      color: var(--text-secondary);
      text-align: right;
    }

    .group-latency-summary-row--header .group-latency-summary-cell {
      font-size: 0.62rem;
      color: var(--text-muted);
      text-transform: uppercase;
      letter-spacing: 0.03em;
    }

    .group-latency-summary-cell--label {
      text-align: left;
    }

    .group-latency-summary-row:not(.group-latency-summary-row--header) .group-latency-summary-cell--label {
      color: var(--text);
      font-weight: 600;
      font-size: 0.68rem;
      letter-spacing: 0.04em;
      text-transform: uppercase;
    }

    .group-latency-summary-cell--min { color: var(--success); }
    .group-latency-summary-cell--avg { color: var(--cyan); }
    .group-latency-summary-cell--max { color: var(--warning); }

    .group-latency-empty {
      margin-top: 8px;
      font-size: 0.68rem;
      color: var(--text-muted);
    }

    /* LATENCY CHART */
    .latency-chart-container {
      position: relative;
      background: rgba(0, 0, 0, 0.2);
      border-radius: var(--radius-md);
      padding: 12px 14px;
    }

    .latency-chart-header {
      display: flex;
      justify-content: space-between;
      align-items: flex-start;
      margin-bottom: 10px;
    }

    .latency-chart-title {
      font-size: 0.7rem;
      color: var(--text-muted);
      text-transform: uppercase;
      letter-spacing: 0.03em;
    }

    .latency-chart-stats {
      display: flex;
      gap: 16px;
    }

    .latency-stat {
      text-align: right;
    }

    .latency-stat-value {
      font-size: 0.85rem;
      font-weight: 600;
      font-family: var(--font-mono);
      color: var(--text);
    }

    .latency-stat-value.min { color: var(--success); }
    .latency-stat-value.max { color: var(--warning); }
    .latency-stat-value.avg { color: var(--cyan); }

    .latency-stat-label {
      font-size: 0.6rem;
      color: var(--text-muted);
      text-transform: uppercase;
    }

    .latency-chart-wrapper {
      position: relative;
      height: 90px;
    }

    .latency-chart {
      width: 100%;
      height: 100%;
      display: block;
      overflow: visible;
    }

    .latency-grid-line {
      stroke: rgba(255, 255, 255, 0.05);
      stroke-width: 0.5;
      stroke-dasharray: 2 4;
    }

    .latency-line {
      fill: none;
      stroke: var(--cyan);
      stroke-width: 1.5;
      stroke-linecap: round;
      stroke-linejoin: round;
      vector-effect: non-scaling-stroke;
    }

    .latency-area {
      opacity: 0.4;
    }

    .latency-dot {
      fill: var(--cyan);
      stroke: var(--card);
      stroke-width: 1.5;
      opacity: 0;
      transition: opacity 0.15s ease;
      vector-effect: non-scaling-stroke;
    }

    .latency-dot:hover {
      opacity: 1 !important;
      fill: #fff;
    }

    .latency-chart-wrapper:hover .latency-dot.visible {
      opacity: 0.6;
    }

    .latency-tooltip {
      position: absolute;
      pointer-events: none;
      background: var(--surface);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
      padding: 6px 10px;
      font-size: 0.7rem;
      font-family: var(--font-mono);
      color: var(--text);
      opacity: 0;
      transform: translateX(-50%) translateY(-100%);
      transition: opacity 0.15s ease;
      white-space: nowrap;
      z-index: 10;
      box-shadow: var(--shadow-md);
    }

    .latency-tooltip::after {
      content: '';
      position: absolute;
      top: 100%;
      left: 50%;
      transform: translateX(-50%);
      border: 5px solid transparent;
      border-top-color: var(--border);
    }

    .latency-chart-wrapper:hover .latency-tooltip {
      opacity: 1;
    }

    .latency-no-data {
      display: flex;
      align-items: center;
      justify-content: center;
      height: 64px;
      color: var(--text-muted);
      font-size: 0.75rem;
    }

    .latency-area {
      fill: url(#latencyGradient);
    }

    .latency-dot {
      fill: var(--cyan);
    }

    /* Downtime shaded areas */
    .downtime-area {
      fill: rgba(239, 68, 68, 0.15);
      stroke: none;
    }

    .downtime-border-left,
    .downtime-border-right {
      stroke: var(--danger);
      stroke-width: 1.5;
      opacity: 0.6;
    }

    /* No data / gap areas */
    .no-data-area {
      pointer-events: none;
    }

    .no-data-line {
      pointer-events: none;
    }

    .latency-single-point {
      opacity: 0.8;
    }

    .latency-chart-legend {
      display: flex;
      gap: 16px;
      margin-top: 8px;
      font-size: 0.65rem;
      color: var(--text-muted);
    }

    .chart-legend-item {
      display: flex;
      align-items: center;
      gap: 6px;
    }

    .legend-line-response {
      width: 16px;
      height: 2px;
      background: var(--cyan);
      border-radius: 1px;
    }

    .legend-area-downtime {
      width: 16px;
      height: 10px;
      background: rgba(239, 68, 68, 0.2);
      border-left: 2px solid var(--danger);
      border-right: 2px solid var(--danger);
      border-radius: 2px;
    }

    .legend-area-nodata {
      width: 16px;
      height: 10px;
      background: rgba(113, 113, 122, 0.15);
      border-radius: 2px;
      position: relative;
    }

    .legend-area-nodata::after {
      content: '';
      position: absolute;
      top: 50%;
      left: 2px;
      right: 2px;
      height: 1px;
      background: repeating-linear-gradient(90deg, rgba(113,113,122,0.5) 0, rgba(113,113,122,0.5) 2px, transparent 2px, transparent 4px);
    }

    /* AXIS LABELS */
    .latency-axis-label {
      font-size: 9px;
      fill: var(--text-muted);
      font-family: var(--font-mono);
    }

    .latency-axis-label-y {
      text-anchor: end;
    }

    .latency-axis-label-x {
      text-anchor: middle;
    }

    /* FULLSCREEN BUTTON */
    .chart-fullscreen-btn {
      background: rgba(255, 255, 255, 0.05);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
      padding: 4px 8px;
      cursor: pointer;
      color: var(--text-muted);
      font-size: 0.65rem;
      display: flex;
      align-items: center;
      gap: 4px;
      transition: all 0.2s ease;
    }

    .chart-fullscreen-btn:hover {
      background: rgba(255, 255, 255, 0.1);
      color: var(--text);
      border-color: var(--primary);
    }

    .chart-fullscreen-btn svg {
      width: 12px;
      height: 12px;
    }

    /* FULLSCREEN MODAL */
    .chart-fullscreen-modal {
      position: fixed;
      top: 0;
      left: 0;
      width: 100vw;
      height: 100vh;
      background: #0a0a0f;
      z-index: 10000;
      display: flex;
      flex-direction: column;
      padding: 20px;
      box-sizing: border-box;
    }

    .chart-fullscreen-modal.hidden {
      display: none;
    }

    .chart-fullscreen-header {
      display: flex;
      justify-content: space-between;
      align-items: center;
      margin-bottom: 16px;
      flex-shrink: 0;
    }

    .chart-fullscreen-title {
      font-size: 1.2rem;
      color: var(--text);
      font-weight: 500;
    }

    .chart-fullscreen-stats {
      display: flex;
      gap: 24px;
    }

    .chart-fullscreen-stat {
      text-align: center;
    }

    .chart-fullscreen-stat-value {
      font-size: 1.1rem;
      font-weight: 600;
      font-family: var(--font-mono);
    }

    .chart-fullscreen-stat-value.min { color: var(--success); }
    .chart-fullscreen-stat-value.avg { color: var(--warning); }
    .chart-fullscreen-stat-value.max { color: var(--danger); }

    .chart-fullscreen-stat-label {
      font-size: 0.7rem;
      color: var(--text-muted);
      text-transform: uppercase;
      margin-top: 2px;
    }

    .chart-fullscreen-range-btns {
      display: flex;
      gap: 2px;
      background: rgba(255, 255, 255, 0.08);
      padding: 3px;
      border-radius: 6px;
    }

    .fs-range-btn {
      background: transparent;
      border: none;
      color: rgba(255, 255, 255, 0.5);
      padding: 6px 14px;
      cursor: pointer;
      border-radius: 4px;
      font-size: 0.85rem;
      font-weight: 500;
      transition: all 0.15s ease;
    }

    .fs-range-btn:hover {
      background: rgba(255, 255, 255, 0.1);
      color: #fff;
    }

    .fs-range-btn.active {
      background: #06b6d4;
      color: #fff;
      font-weight: 600;
    }

    .chart-fullscreen-close {
      background: rgba(255, 255, 255, 0.1);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
      padding: 8px 16px;
      cursor: pointer;
      color: var(--text);
      font-size: 0.8rem;
      transition: all 0.2s ease;
    }

    .chart-fullscreen-close:hover {
      background: rgba(255, 255, 255, 0.2);
      border-color: var(--primary);
    }

    .chart-fullscreen-body {
      flex: 1;
      min-height: 0;
      display: flex;
      flex-direction: column;
    }

    .chart-fullscreen-wrapper {
      flex: 1;
      min-height: 0;
      position: relative;
    }

    .chart-fullscreen-wrapper svg {
      width: 100%;
      height: 100%;
    }

    .chart-fullscreen-legend {
      display: flex;
      justify-content: center;
      gap: 24px;
      margin-top: 16px;
      font-size: 0.75rem;
      color: var(--text-muted);
      flex-shrink: 0;
    }

    /* GLOBAL UPTIME SECTION */
    .global-uptime-section {
      background: var(--surface);
      border: 1px solid var(--border);
      border-radius: var(--radius-lg);
      padding: 20px;
      margin-bottom: 28px;
    }

    .uptime-legend {
      display: flex;
      gap: 16px;
      margin-top: 12px;
      font-size: 0.7rem;
      color: var(--text-muted);
    }

    .legend-item {
      display: flex;
      align-items: center;
      gap: 6px;
    }

    .legend-dot {
      width: 8px;
      height: 8px;
      border-radius: 2px;
    }

    /* FOOTER */
    .footer {
      padding-top: 20px;
      border-top: 1px solid var(--border);
      font-size: 0.75rem;
      color: var(--text-muted);
      display: flex;
      justify-content: space-between;
      align-items: center;
    }

    /* CUSTOMIZER (opt-in via ?edit=1) */
    .customize-overlay {
      position: fixed;
      inset: 0;
      background: rgba(0, 0, 0, 0.55);
      backdrop-filter: blur(10px);
      z-index: 10000;
      display: none;
      align-items: stretch;
      justify-content: flex-end;
    }

    .customize-overlay.open { display: flex; }

    .customize-panel {
      width: min(480px, 96vw);
      height: 100%;
      background: color-mix(in srgb, var(--surface) 92%, black);
      border-left: 1px solid var(--border);
      box-shadow: var(--shadow-lg);
      display: flex;
      flex-direction: column;
    }

    .customize-header {
      padding: 16px 16px 12px;
      border-bottom: 1px solid var(--border);
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 12px;
    }

    .customize-title {
      font-weight: 700;
      letter-spacing: -0.02em;
    }

    .customize-close {
      padding: 8px 10px;
      border-radius: var(--radius-sm);
      border: 1px solid var(--border);
      background: var(--surface);
      color: var(--text-secondary);
      cursor: pointer;
      transition: var(--transition);
      font-size: 0.8rem;
    }
    .customize-close:hover { background: var(--surface-elevated); color: var(--text); border-color: var(--border-hover); }

    .customize-body {
      padding: 14px 16px;
      overflow: auto;
      display: flex;
      flex-direction: column;
      gap: 14px;
    }

    .customize-field label {
      display: block;
      font-size: 0.75rem;
      color: var(--text-muted);
      margin-bottom: 6px;
    }

    .customize-input, .customize-textarea {
      width: 100%;
      border-radius: var(--radius-sm);
      border: 1px solid var(--border);
      background: var(--surface);
      color: var(--text);
      padding: 10px 12px;
      font-family: var(--font-sans);
      outline: none;
      transition: var(--transition);
    }
    .customize-textarea { min-height: 80px; resize: vertical; }
    .customize-input:focus, .customize-textarea:focus { border-color: var(--border-accent); box-shadow: 0 0 0 3px rgba(99,102,241,0.15); }

    .customize-row { display: flex; gap: 10px; }
    .customize-row > * { flex: 1; }

    .customize-section-title {
      font-size: 0.85rem;
      font-weight: 700;
      color: var(--text);
      margin-bottom: 6px;
    }

    .customize-toggles {
      display: grid;
      grid-template-columns: 1fr;
      gap: 8px;
      padding: 10px 12px;
      border-radius: var(--radius-sm);
      border: 1px solid var(--border);
      background: var(--surface);
    }

    .customize-toggle {
      display: flex;
      align-items: center;
      gap: 10px;
      font-size: 0.82rem;
      color: var(--text-secondary);
      user-select: none;
    }

    .customize-toggle input[type="checkbox"] {
      width: 16px;
      height: 16px;
      accent-color: var(--brand-primary, var(--accent));
    }

    .customize-selected {
      display: flex;
      flex-direction: column;
      gap: 8px;
    }

    .customize-item {
      display: flex;
      align-items: center;
      gap: 10px;
      padding: 10px 10px;
      border-radius: var(--radius-sm);
      border: 1px solid var(--border);
      background: var(--surface);
    }

    .customize-item-main {
      flex: 1;
      min-width: 0;
      display: flex;
      flex-direction: column;
      gap: 6px;
    }

    .customize-item-input {
      width: 100%;
      border-radius: var(--radius-sm);
      border: 1px solid rgba(255,255,255,0.08);
      background: color-mix(in srgb, var(--surface-elevated) 70%, transparent);
      color: var(--text);
      padding: 8px 10px;
      font-family: var(--font-sans);
      font-size: 0.8rem;
      outline: none;
      transition: var(--transition);
    }
    .customize-item-input:focus { border-color: var(--border-accent); box-shadow: 0 0 0 3px rgba(99,102,241,0.12); }

    .customize-handle {
      width: 14px;
      height: 14px;
      opacity: 0.7;
      cursor: grab;
      display: inline-block;
      background:
        radial-gradient(circle at 25% 25%, rgba(255,255,255,0.35) 0 2px, transparent 3px),
        radial-gradient(circle at 75% 25%, rgba(255,255,255,0.35) 0 2px, transparent 3px),
        radial-gradient(circle at 25% 75%, rgba(255,255,255,0.35) 0 2px, transparent 3px),
        radial-gradient(circle at 75% 75%, rgba(255,255,255,0.35) 0 2px, transparent 3px);
    }

    .customize-item.dragging { opacity: 0.6; }

    .customize-item-name {
      flex: 1;
      min-width: 0;
      font-size: 0.85rem;
      color: var(--text);
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }

    .customize-item-badge {
      font-size: 0.7rem;
      color: var(--text-muted);
      border: 1px solid var(--border);
      border-radius: 999px;
      padding: 2px 8px;
      background: var(--surface-elevated);
    }

    .customize-monitors {
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
      overflow: hidden;
      background: var(--surface);
    }

    .customize-monitors-header {
      padding: 10px 12px;
      border-bottom: 1px solid var(--border);
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 10px;
    }

    .customize-monitors-list {
      max-height: 240px;
      overflow: auto;
    }

    .customize-monitor-row {
      display: flex;
      align-items: center;
      gap: 10px;
      padding: 10px 12px;
      border-top: 1px solid rgba(255,255,255,0.04);
      font-size: 0.85rem;
    }

    .customize-monitor-row:hover { background: var(--surface-hover); }

    .customize-footer {
      padding: 12px 16px 16px;
      border-top: 1px solid var(--border);
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 12px;
    }

    .customize-status {
      font-size: 0.75rem;
      color: var(--text-muted);
      min-height: 1em;
    }

    .customize-save {
      padding: 10px 14px;
      border-radius: var(--radius-sm);
      border: 1px solid color-mix(in srgb, var(--brand-primary, var(--accent)) 50%, var(--border));
      background: linear-gradient(135deg, color-mix(in srgb, var(--brand-primary, var(--accent)) 30%, var(--surface)) 0%, var(--surface) 100%);
      color: var(--text);
      cursor: pointer;
      transition: var(--transition);
      font-weight: 600;
      font-size: 0.85rem;
    }
    .customize-save:hover { transform: translateY(-1px); border-color: var(--border-hover); background: var(--surface-elevated); }

    /* RESPONSIVE */
    @media (max-width: 768px) {
      .agent-metrics { grid-template-columns: repeat(2, 1fr); }
    }
    @media (max-width: 640px) {
      .page-inner { padding: 20px 16px 32px; }
      .header { flex-direction: column; align-items: flex-start; gap: 12px; }
      .header-meta { text-align: left; }
      .status-banner { flex-direction: column; align-items: flex-start; gap: 12px; }
      .component-stats { grid-template-columns: repeat(2, 1fr); }
      .component-header { flex-direction: column; gap: 10px; }
      .component-meta { width: 100%; justify-content: space-between; }
      .filter-pills { flex-wrap: wrap; }
      .section-header { flex-direction: column; align-items: flex-start; gap: 10px; }
      .agent-metrics { grid-template-columns: 1fr; }
    }
  </style>
</head>
  <body data-status-page-id="{{.ID}}" data-status-page-slug="{{.Slug}}" data-status-page-primary-color="{{if .PrimaryColor}}{{.PrimaryColor}}{{end}}" data-status-page-secondary-color="{{if .SecondaryColor}}{{.SecondaryColor}}{{end}}" data-status-page-has-description="{{if .Description}}1{{else}}0{{end}}" data-sp-show-monitor-tags="{{if .ShowMonitorTags}}1{{else}}0{{end}}" data-sp-show-monitor-url="{{if .ShowMonitorURL}}1{{else}}0{{end}}" data-sp-show-monitor-uptime="{{if .ShowMonitorUptime}}1{{else}}0{{end}}" data-sp-show-monitor-tls="{{if .ShowMonitorTLS}}1{{else}}0{{end}}" data-sp-show-latency-charts="{{if .ShowLatencyCharts}}1{{else}}0{{end}}" data-sp-show-agent-metrics="{{if .ShowAgentMetrics}}1{{else}}0{{end}}" data-sp-show-global-uptime="{{if .ShowGlobalUptime}}1{{else}}0{{end}}" data-sp-show-footer="{{if .ShowFooter}}1{{else}}0{{end}}" data-sp-footer-text="{{if .CustomFooterText}}{{.CustomFooterText}}{{end}}">
  <div class="page">
    <div class="page-inner">
      <!-- HEADER -->
      <header class="header">
        <div class="brand">
          {{if .HasLogo}}
          <img src="{{.LogoURL}}" alt="{{.Title}}" class="brand-logo-img">
          {{else}}
          <div class="brand-logo">
            <div class="brand-logo-inner"></div>
          </div>
          {{end}}
          <div>
            <div class="brand-name" id="spTitle">{{.Title}}</div>
            {{if .Description}}
            <div class="brand-subline" id="spDescription">{{.Description}}</div>
            {{else}}
            <div class="brand-subline" id="spDescription">System Status</div>
            {{end}}
          </div>
        </div>
        <div class="header-meta">
          <div>All times UTC</div>
          <button class="header-btn" id="customizeBtn" style="display:none;">Customize</button>
          <button class="header-btn" id="copyStatusLink">Copy Status URL</button>
        </div>
      </header>

      <!-- STATUS BANNER -->
      <section class="status-banner {{if .HasIssues}}has-issues{{end}}">
        <div class="status-banner-main">
          <div class="status-indicator"></div>
          <div>
            <div class="status-title">
              {{if .HasIssues}}Some systems experiencing issues{{else}}All systems operational{{end}}
            </div>
            <div class="status-subtitle">
              {{if .HasIssues}}One or more services are degraded{{else}}Everything is running smoothly{{end}}
            </div>
          </div>
        </div>
        <div class="status-time" id="lastUpdated">Updated just now</div>
      </section>

      <!-- COMPONENTS -->
      <section class="components-section">
        <div class="section-header">
          <div>
            <div class="section-title">Service Components</div>
            <div class="section-subtitle">Real-time status with 24h uptime history and latency metrics</div>
          </div>
          <div class="section-controls">
            <div class="filter-pills">
              <button class="filter-pill active" data-filter="all">All</button>
              <button class="filter-pill" data-filter="up">Operational</button>
              <button class="filter-pill" data-filter="down">Down</button>
            </div>
            <div class="view-toggle">
              <button class="view-btn active" data-view="detailed" title="Detailed View">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <line x1="8" y1="6" x2="21" y2="6"></line>
                  <line x1="8" y1="12" x2="21" y2="12"></line>
                  <line x1="8" y1="18" x2="21" y2="18"></line>
                  <line x1="3" y1="6" x2="3.01" y2="6"></line>
                  <line x1="3" y1="12" x2="3.01" y2="12"></line>
                  <line x1="3" y1="18" x2="3.01" y2="18"></line>
                </svg>
              </button>
              <button class="view-btn" data-view="compact" title="Compact View">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <rect x="3" y="3" width="7" height="7"></rect>
                  <rect x="14" y="3" width="7" height="7"></rect>
                  <rect x="14" y="14" width="7" height="7"></rect>
                  <rect x="3" y="14" width="7" height="7"></rect>
                </svg>
              </button>
            </div>
            <button class="fullscreen-btn" id="fullscreenBtn" title="Toggle Fullscreen">
              <svg class="expand-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M8 3H5a2 2 0 0 0-2 2v3m18 0V5a2 2 0 0 0-2-2h-3m0 18h3a2 2 0 0 0 2-2v-3M3 16v3a2 2 0 0 0 2 2h3"/>
              </svg>
              <svg class="collapse-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M4 14h6v6m10-10h-6V4m0 6l7-7M3 21l7-7"/>
              </svg>
            </button>
          </div>
        </div>

        <div class="component-cards" id="componentCards">
          {{range .Monitors}}
          <div class="component-card" data-status="{{.Status}}" data-type="{{.MonitorType}}" data-monitor-id="{{.ID}}">
            <div class="component-header">
              <div class="component-info">
                <div class="component-name">
                  <span class="component-name-text">{{.Name}}</span>
                  {{if eq .MonitorType "agent"}}<span class="agent-badge">Agent</span>{{end}}
                  {{if eq .MonitorType "push"}}<span class="agent-badge" style="background: rgba(168, 85, 247, 0.15); color: #a855f7;">Push</span>{{end}}
                </div>
                {{if $.ShowMonitorURL}}<div class="component-url">{{.URL}}</div>{{end}}
                {{if and $.ShowMonitorTags (gt (len .Tags) 0)}}
                <div class="component-tags">
                  {{range .Tags}}<span class="tag">{{.}}</span>{{end}}
                </div>
                {{end}}
              </div>
              <div class="component-meta">
                {{if and (ne .MonitorType "agent") (ne .MonitorType "push")}}
                <div class="component-latency">{{formatLatencyInt .LastLatency}}</div>
                {{end}}
                {{if and $.ShowMonitorTLS (eq .MonitorType "http") (or .TLSDaysUntilExpiry .TLSNotAfter)}}
                <div class="component-tls">
                  <span class="component-tls-label">TLS</span>
                  <span class="component-tls-value">
                    {{if .TLSDaysUntilExpiry}}{{.TLSDaysUntilExpiry}}d left{{end}}{{if and .TLSDaysUntilExpiry .TLSNotAfter}} · {{end}}{{if .TLSNotAfter}}Expires {{formatDate .TLSNotAfter}}{{end}}
                  </span>
                </div>
                {{end}}
                <div class="status-badge {{.Status}}">
                  <span class="dot"></span>
                  {{if eq .Status "up"}}Operational{{else if eq .Status "down"}}Down{{else if eq .Status "error"}}Error{{else}}Unknown{{end}}
                </div>
                <div class="compact-uptime">{{.Uptime24hFormatted}}</div>
              </div>
            </div>

            {{if eq .MonitorType "agent"}}
            {{if $.ShowAgentMetrics}}
            {{if .AgentMetrics}}
            <!-- AGENT METRICS DISPLAY -->
            <div class="agent-metrics">
              <div class="agent-metric-card">
                <div class="agent-metric-header">
                  <span class="agent-metric-label">CPU</span>
                </div>
                <div class="agent-metric-value {{if lt .AgentMetrics.CPUPercent 70.0}}success{{else if lt .AgentMetrics.CPUPercent 90.0}}warning{{else}}danger{{end}}">{{formatPercent .AgentMetrics.CPUPercent}}</div>
                <div class="agent-metric-bar">
                  <div class="agent-metric-bar-fill {{if lt .AgentMetrics.CPUPercent 70.0}}success{{else if lt .AgentMetrics.CPUPercent 90.0}}warning{{else}}danger{{end}}" style="width: {{printf "%.0f" .AgentMetrics.CPUPercent}}%"></div>
                </div>
              </div>
              <div class="agent-metric-card">
                <div class="agent-metric-header">
                  <span class="agent-metric-label">Memory</span>
                </div>
                <div class="agent-metric-value {{if lt .AgentMetrics.MemoryPercent 70.0}}success{{else if lt .AgentMetrics.MemoryPercent 90.0}}warning{{else}}danger{{end}}">{{formatPercent .AgentMetrics.MemoryPercent}}</div>
                <div class="agent-metric-bar">
                  <div class="agent-metric-bar-fill {{if lt .AgentMetrics.MemoryPercent 70.0}}success{{else if lt .AgentMetrics.MemoryPercent 90.0}}warning{{else}}danger{{end}}" style="width: {{printf "%.0f" .AgentMetrics.MemoryPercent}}%"></div>
                </div>
                <div class="agent-metric-detail">{{formatBytes .AgentMetrics.MemoryUsed}} / {{formatBytes .AgentMetrics.MemoryTotal}}</div>
              </div>
              <div class="agent-metric-card">
                <div class="agent-metric-header">
                  <span class="agent-metric-label">Disk</span>
                </div>
                <div class="agent-metric-value {{if lt .AgentMetrics.DiskPercent 70.0}}success{{else if lt .AgentMetrics.DiskPercent 90.0}}warning{{else}}danger{{end}}">{{formatPercent .AgentMetrics.DiskPercent}}</div>
                <div class="agent-metric-bar">
                  <div class="agent-metric-bar-fill {{if lt .AgentMetrics.DiskPercent 70.0}}success{{else if lt .AgentMetrics.DiskPercent 90.0}}warning{{else}}danger{{end}}" style="width: {{printf "%.0f" .AgentMetrics.DiskPercent}}%"></div>
                </div>
                <div class="agent-metric-detail">{{formatBytes .AgentMetrics.DiskUsed}} / {{formatBytes .AgentMetrics.DiskTotal}}</div>
              </div>
              <div class="agent-metric-card">
                <div class="agent-metric-header">
                  <span class="agent-metric-label">Load Average</span>
                </div>
                <div class="agent-load-values">
                  <div class="agent-load-item">
                    <span class="agent-load-value">{{formatLoad .AgentMetrics.LoadAvg1}}</span>
                    <span class="agent-load-period">1m</span>
                  </div>
                  <div class="agent-load-item">
                    <span class="agent-load-value">{{formatLoad .AgentMetrics.LoadAvg5}}</span>
                    <span class="agent-load-period">5m</span>
                  </div>
                  <div class="agent-load-item">
                    <span class="agent-load-value">{{formatLoad .AgentMetrics.LoadAvg15}}</span>
                    <span class="agent-load-period">15m</span>
                  </div>
                </div>
              </div>
              <div class="agent-metric-card">
                <div class="agent-metric-header">
                  <span class="agent-metric-label">Network In</span>
                </div>
                <div class="agent-metric-value" style="font-size: 1rem;">{{formatBytes .AgentMetrics.NetworkBytesIn}}</div>
                <div class="agent-metric-detail">Total received</div>
              </div>
              <div class="agent-metric-card">
                <div class="agent-metric-header">
                  <span class="agent-metric-label">Network Out</span>
                </div>
                <div class="agent-metric-value" style="font-size: 1rem;">{{formatBytes .AgentMetrics.NetworkBytesOut}}</div>
                <div class="agent-metric-detail">Total sent</div>
              </div>
            </div>
            {{else}}
            <div class="component-stats">
              <div class="stat-item">
                <div class="stat-value">—</div>
                <div class="stat-label">No agent data</div>
              </div>
            </div>
            {{end}}
            {{end}}
            {{else if eq .MonitorType "push"}}
            {{if $.ShowAgentMetrics}}
            {{if .PushMetrics}}
            <!-- PUSH METRICS DISPLAY (Auto-detected) -->
            <div class="agent-metrics">
              {{range $key, $value := .PushMetrics}}
              <div class="agent-metric-card">
                <div class="agent-metric-header">
                  <span class="agent-metric-label">{{formatMetricName $key}}</span>
                </div>
                <div class="agent-metric-value" style="font-size: 1rem;">{{formatMetricValue $value}}</div>
              </div>
              {{end}}
            </div>
            {{else}}
            <div class="component-stats">
              <div class="stat-item">
                <div class="stat-value">—</div>
                <div class="stat-label">Awaiting first push</div>
              </div>
            </div>
            {{end}}
            {{end}}
            {{else}}
            <!-- STANDARD STATS FOR HTTP/PING MONITORS -->
            <div class="component-stats">
              <div class="stat-item">
                <div class="stat-value {{if .Uptime1h}}{{if ge (index (printf "%.0f" (index .Uptime1h)) 0) 99}}success{{else if ge (index (printf "%.0f" (index .Uptime1h)) 0) 95}}warning{{else}}danger{{end}}{{end}}">{{.Uptime1hFormatted}}</div>
                <div class="stat-label">1h Uptime</div>
              </div>
              <div class="stat-item">
                <div class="stat-value {{if .Uptime24h}}{{if ge (index (printf "%.0f" (index .Uptime24h)) 0) 99}}success{{else if ge (index (printf "%.0f" (index .Uptime24h)) 0) 95}}warning{{else}}danger{{end}}{{end}}">{{.Uptime24hFormatted}}</div>
                <div class="stat-label">24h Uptime</div>
              </div>
              <div class="stat-item">
                <div class="stat-value">{{formatLatency .AvgLatency1h}}</div>
                <div class="stat-label">Avg Latency 1h</div>
              </div>
              <div class="stat-item">
                <div class="stat-value">{{formatLatency .AvgLatency24h}}</div>
                <div class="stat-label">Avg Latency 24h</div>
              </div>
            </div>
            {{end}}

            {{if $.ShowMonitorUptime}}
            <div class="component-uptime" data-monitor-id="{{.ID}}">
              <div class="uptime-bars-header">
                <div class="uptime-bars-label">
                  <span class="uptime-range-label">Uptime History (1h)</span>
                  <span class="uptime-bar-hint">Each bar = 5 min</span>
                </div>
                <div class="uptime-range-btns">
                  <button class="range-btn active" data-range="1h">1h</button>
                  <button class="range-btn" data-range="24h">24h</button>
                  <button class="range-btn" data-range="30d">30d</button>
                  <button class="range-btn" data-range="90d">90d</button>
                  <button class="range-btn" data-range="1y">1y</button>
                </div>
              </div>
              <div class="uptime-bars" 
                data-history-1h='[{{range $idx, $m := .UptimeHistory1h}}{{if $idx}},{{end}}{"time":"{{$m.Time}}","uptime":{{$m.Uptime}}}{{end}}]'
                data-history-24h='[{{range $idx, $h := .UptimeHistory24h}}{{if $idx}},{{end}}{"time":"{{$h.Hour}}","uptime":{{$h.Uptime}}}{{end}}]'
                data-history-30d='[{{range $idx, $d := .UptimeHistory30d}}{{if $idx}},{{end}}{"time":"{{$d.Date}}","uptime":{{$d.Uptime}}}{{end}}]'
                data-history-90d='[{{range $idx, $d := .UptimeHistory90d}}{{if $idx}},{{end}}{"time":"{{$d.Date}}","uptime":{{$d.Uptime}}}{{end}}]'
                data-history-1y='[{{range $idx, $d := .UptimeHistory365d}}{{if $idx}},{{end}}{"time":"{{$d.Date}}","uptime":{{$d.Uptime}}}{{end}}]'>
                <!-- Bars rendered by JS -->
              </div>
            </div>
            {{end}}

            {{if and $.ShowLatencyCharts (ne .MonitorType "agent") (ne .MonitorType "push")}}
            {{if eq .MonitorType "group"}}
            <div class="group-latency-summary">
              <div class="group-latency-summary-header">
                <span class="group-latency-summary-title">Group Latency Summary</span>
              </div>
              <div class="group-latency-summary-table">
                <div class="group-latency-summary-row group-latency-summary-row--header">
                  <span class="group-latency-summary-cell group-latency-summary-cell--label">Range</span>
                  <span class="group-latency-summary-cell">Min</span>
                  <span class="group-latency-summary-cell">Avg</span>
                  <span class="group-latency-summary-cell">Max</span>
                </div>
                <div class="group-latency-summary-row">
                  <span class="group-latency-summary-cell group-latency-summary-cell--label">1h</span>
                  <span class="group-latency-summary-cell group-latency-summary-cell--min">{{formatLatencyMin .LatencyHistory1h}}</span>
                  <span class="group-latency-summary-cell group-latency-summary-cell--avg">{{formatLatency .AvgLatency1h}}</span>
                  <span class="group-latency-summary-cell group-latency-summary-cell--max">{{formatLatencyMax .LatencyHistory1h}}</span>
                </div>
                <div class="group-latency-summary-row">
                  <span class="group-latency-summary-cell group-latency-summary-cell--label">24h</span>
                  <span class="group-latency-summary-cell group-latency-summary-cell--min">{{formatLatencyMin .LatencyHistory}}</span>
                  <span class="group-latency-summary-cell group-latency-summary-cell--avg">{{formatLatency .AvgLatency24h}}</span>
                  <span class="group-latency-summary-cell group-latency-summary-cell--max">{{formatLatencyMax .LatencyHistory}}</span>
                </div>
              </div>
              {{if and (eq (len .LatencyHistory1h) 0) (eq (len .LatencyHistory) 0)}}
              <div class="group-latency-empty">No latency samples yet.</div>
              {{end}}
            </div>
            {{else}}
            <div class="latency-chart-container" data-monitor-id="{{.ID}}" data-monitor-name="{{.Name}}">
              <div class="latency-chart-header">
                <div style="display: flex; align-items: center; gap: 8px;">
                  <span class="latency-chart-title">Response Time (1h)</span>
                  <button class="chart-fullscreen-btn" onclick="openChartFullscreen(this)" title="Open in fullscreen">
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                      <path d="M8 3H5a2 2 0 0 0-2 2v3m18 0V5a2 2 0 0 0-2-2h-3m0 18h3a2 2 0 0 0 2-2v-3M3 16v3a2 2 0 0 0 2 2h3"/>
                    </svg>
                    Expand
                  </button>
                </div>
                <div class="latency-chart-stats">
                  <div class="latency-stat">
                    <div class="latency-stat-value min" data-stat="min">—</div>
                    <div class="latency-stat-label">Min</div>
                  </div>
                  <div class="latency-stat">
                    <div class="latency-stat-value avg" data-stat="avg">{{formatLatency .AvgLatency1h}}</div>
                    <div class="latency-stat-label">Avg</div>
                  </div>
                  <div class="latency-stat">
                    <div class="latency-stat-value max" data-stat="max">—</div>
                    <div class="latency-stat-label">Max</div>
                  </div>
                </div>
              </div>
              <div class="latency-chart-wrapper" 
                data-latency-1h='[{{range $idx, $l := .LatencyHistory1h}}{{if $idx}},{{end}}{"time":"{{$l.Time}}","latency":{{$l.LatencyMS}},"unix":{{$l.Unix}}}{{end}}]'
                data-latency-24h='[{{range $idx, $l := .LatencyHistory}}{{if $idx}},{{end}}{"time":"{{$l.Time}}","latency":{{$l.LatencyMS}},"unix":{{$l.Unix}}}{{end}}]'
                data-latency-30d='[{{range $idx, $l := .LatencyHistory30d}}{{if $idx}},{{end}}{"time":"{{$l.Time}}","latency":{{$l.LatencyMS}},"unix":{{$l.Unix}}}{{end}}]'
                data-latency-90d='[{{range $idx, $l := .LatencyHistory90d}}{{if $idx}},{{end}}{"time":"{{$l.Time}}","latency":{{$l.LatencyMS}},"unix":{{$l.Unix}}}{{end}}]'
                data-latency-1y='[{{range $idx, $l := .LatencyHistory365d}}{{if $idx}},{{end}}{"time":"{{$l.Time}}","latency":{{$l.LatencyMS}},"unix":{{$l.Unix}}}{{end}}]'
                data-downtime-1h='[{{range $idx, $d := .DowntimePeriods1h}}{{if $idx}},{{end}}{"start":"{{$d.StartTime}}","end":"{{$d.EndTime}}","startUnix":{{$d.StartUnix}},"endUnix":{{$d.EndUnix}}}{{end}}]'
                data-downtime-24h='[{{range $idx, $d := .DowntimePeriods24h}}{{if $idx}},{{end}}{"start":"{{$d.StartTime}}","end":"{{$d.EndTime}}","startUnix":{{$d.StartUnix}},"endUnix":{{$d.EndUnix}}}{{end}}]'
                data-downtime-30d='[{{range $idx, $d := .DowntimePeriods30d}}{{if $idx}},{{end}}{"start":"{{$d.StartTime}}","end":"{{$d.EndTime}}","startUnix":{{$d.StartUnix}},"endUnix":{{$d.EndUnix}}}{{end}}]'
                data-downtime-90d='[{{range $idx, $d := .DowntimePeriods90d}}{{if $idx}},{{end}}{"start":"{{$d.StartTime}}","end":"{{$d.EndTime}}","startUnix":{{$d.StartUnix}},"endUnix":{{$d.EndUnix}}}{{end}}]'
                data-downtime-1y='[{{range $idx, $d := .DowntimePeriods365d}}{{if $idx}},{{end}}{"start":"{{$d.StartTime}}","end":"{{$d.EndTime}}","startUnix":{{$d.StartUnix}},"endUnix":{{$d.EndUnix}}}{{end}}]'
                data-current-range="1h">
                <svg class="latency-chart" viewBox="0 0 400 80" preserveAspectRatio="none">
                  <defs>
                    <linearGradient id="latencyAreaGradient-{{.ID}}" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stop-color="rgba(6, 182, 212, 0.25)"/>
                      <stop offset="100%" stop-color="rgba(6, 182, 212, 0)"/>
                    </linearGradient>
                  </defs>
                </svg>
                <div class="latency-tooltip"></div>
              </div>
            </div>
            {{end}}
            {{end}}
          </div>
          {{end}}
        </div>
      </section>

      {{if .ShowGlobalUptime}}
      <!-- GLOBAL UPTIME -->
      <section class="global-uptime-section">
        <div class="section-header">
          <div>
            <div class="section-title">Global Uptime</div>
            <div class="section-subtitle">Aggregated uptime across all services</div>
          </div>
          <div class="filter-pills">
            <button class="filter-pill" data-range="1h">1h</button>
            <button class="filter-pill active" data-range="24h">24h</button>
            <button class="filter-pill" data-range="30d">30d</button>
            <button class="filter-pill" data-range="90d">90d</button>
            <button class="filter-pill" data-range="1y">1y</button>
          </div>
        </div>
        <div
          class="uptime-bars"
          id="globalUptimeBars"
          style="height: 36px;"
          data-uptime-1h='[{{range $idx, $m := .UptimeHistory1h}}{{if $idx}},{{end}}{"label":"{{$m.Time}}","uptime":{{$m.Uptime}}}{{end}}]'
          data-uptime-24h='[{{range $idx, $h := .UptimeHistory1}}{{if $idx}},{{end}}{"label":"{{$h.Hour}}","uptime":{{$h.Uptime}}}{{end}}]'
          data-uptime-30d='[{{range $idx, $h := .UptimeHistory30}}{{if $idx}},{{end}}{"label":"{{$h.Date}}","uptime":{{$h.Uptime}}}{{end}}]'
          data-uptime-90d='[{{range $idx, $h := .UptimeHistory90}}{{if $idx}},{{end}}{"label":"{{$h.Date}}","uptime":{{$h.Uptime}}}{{end}}]'
          data-uptime-1y='[{{range $idx, $h := .UptimeHistory365}}{{if $idx}},{{end}}{"label":"{{$h.Date}}","uptime":{{$h.Uptime}}}{{end}}]'></div>
        <div class="uptime-legend">
          <div class="legend-item"><div class="legend-dot" style="background: var(--success);"></div> >99%</div>
          <div class="legend-item"><div class="legend-dot" style="background: var(--warning);"></div> 95-99%</div>
          <div class="legend-item"><div class="legend-dot" style="background: var(--danger);"></div> <95%</div>
        </div>
      </section>
      {{end}}

      {{if .ShowFooter}}
      <footer class="footer">
        <div>
          {{if .CustomFooterText}}{{.CustomFooterText}}{{else}}&copy; <span id="footerYear"></span> {{.Title}}{{end}}
        </div>
        <div>Live updates enabled</div>
      </footer>
      {{end}}
    </div>
  </div>

  <!-- Status Page Customizer (only shown in edit mode) -->
  <div class="customize-overlay" id="spCustomizeOverlay" aria-hidden="true">
    <div class="customize-panel" role="dialog" aria-modal="true" aria-label="Customize status page">
      <div class="customize-header">
        <div class="customize-title">Customize</div>
        <button class="customize-close" id="spCustomizeClose">Close</button>
      </div>
      <div class="customize-body">
        <div class="customize-field">
          <label for="spCustomizeTitle">Page title</label>
          <input class="customize-input" id="spCustomizeTitle" type="text" placeholder="Status page title" />
        </div>

        <div class="customize-field">
          <label for="spCustomizeDescription">Description (optional)</label>
          <textarea class="customize-textarea" id="spCustomizeDescription" placeholder="Short description shown in the header"></textarea>
        </div>

        <div class="customize-row">
          <div class="customize-field">
            <label for="spCustomizePrimary">Primary color</label>
            <input class="customize-input" id="spCustomizePrimary" type="text" placeholder="#6366f1" />
          </div>
          <div class="customize-field">
            <label for="spCustomizeSecondary">Secondary color</label>
            <input class="customize-input" id="spCustomizeSecondary" type="text" placeholder="#06b6d4" />
          </div>
        </div>

        <div>
          <div class="customize-section-title">Visibility</div>
          <div class="customize-toggles">
            <label class="customize-toggle"><input type="checkbox" id="spSetShowMonitorTags" /> Show tags</label>
            <label class="customize-toggle"><input type="checkbox" id="spSetShowMonitorURL" /> Show URL/host</label>
            <label class="customize-toggle"><input type="checkbox" id="spSetShowMonitorUptime" /> Show uptime history</label>
            <label class="customize-toggle"><input type="checkbox" id="spSetShowMonitorTLS" /> Show TLS expiry</label>
            <label class="customize-toggle"><input type="checkbox" id="spSetShowLatencyCharts" /> Show latency charts</label>
            <label class="customize-toggle"><input type="checkbox" id="spSetShowAgentMetrics" /> Show agent/push metrics</label>
            <label class="customize-toggle"><input type="checkbox" id="spSetShowGlobalUptime" /> Show global uptime</label>
            <label class="customize-toggle"><input type="checkbox" id="spSetShowFooter" /> Show footer</label>
          </div>
        </div>

        <div class="customize-field">
          <label for="spCustomizeFooterText">Footer text (optional)</label>
          <input class="customize-input" id="spCustomizeFooterText" type="text" placeholder="Leave empty to use default copyright" />
        </div>

        <div>
          <div class="customize-section-title">Displayed components (drag to reorder)</div>
          <div class="customize-selected" id="spCustomizeSelected"></div>
        </div>

        <div>
          <div class="customize-section-title">Add / remove monitors</div>
          <div class="customize-monitors">
            <div class="customize-monitors-header">
              <span style="font-size:0.75rem; color: var(--text-muted);">Requires being logged in</span>
              <button class="header-btn" id="spCustomizeLoadMore" style="padding:6px 10px; font-size:0.75rem;">Load more</button>
            </div>
            <div class="customize-monitors-list" id="spCustomizeMonitorList"></div>
          </div>
        </div>
      </div>
      <div class="customize-footer">
        <div class="customize-status" id="spCustomizeStatus"></div>
        <button class="customize-save" id="spCustomizeSave">Save & Reload</button>
      </div>
    </div>
  </div>

  <!-- Fullscreen Chart Modal -->
  <div id="chartFullscreenModal" class="chart-fullscreen-modal hidden">
    <div class="chart-fullscreen-header">
      <div class="chart-fullscreen-title" id="fullscreenChartTitle">Response Time</div>
      <div class="chart-fullscreen-range-btns" id="fullscreenRangeBtns">
        <button class="fs-range-btn" data-range="1h" onclick="changeFullscreenRange('1h')">1h</button>
        <button class="fs-range-btn" data-range="24h" onclick="changeFullscreenRange('24h')">24h</button>
        <button class="fs-range-btn" data-range="30d" onclick="changeFullscreenRange('30d')">30d</button>
        <button class="fs-range-btn" data-range="90d" onclick="changeFullscreenRange('90d')">90d</button>
        <button class="fs-range-btn" data-range="1y" onclick="changeFullscreenRange('1y')">1y</button>
      </div>
      <div class="chart-fullscreen-stats">
        <div class="chart-fullscreen-stat">
          <div class="chart-fullscreen-stat-value min" id="fullscreenStatMin">—</div>
          <div class="chart-fullscreen-stat-label">Min</div>
        </div>
        <div class="chart-fullscreen-stat">
          <div class="chart-fullscreen-stat-value avg" id="fullscreenStatAvg">—</div>
          <div class="chart-fullscreen-stat-label">Avg</div>
        </div>
        <div class="chart-fullscreen-stat">
          <div class="chart-fullscreen-stat-value max" id="fullscreenStatMax">—</div>
          <div class="chart-fullscreen-stat-label">Max</div>
        </div>
      </div>
      <button class="chart-fullscreen-close" onclick="closeChartFullscreen()">
        Close (Esc)
      </button>
    </div>
    <div class="chart-fullscreen-body">
      <div class="chart-fullscreen-wrapper" id="fullscreenChartWrapper">
        <svg id="fullscreenChartSvg" preserveAspectRatio="none"></svg>
      </div>
      <div class="chart-fullscreen-legend" id="fullscreenChartLegend"></div>
    </div>
  </div>

  <script>
    const rangeLabels = {
      "1h": { title: "Last Hour", hint: "Each bar = 5 min" },
      "24h": { title: "Last 24 Hours", hint: "Each bar = 1 hour" },
      "30d": { title: "Last 30 Days", hint: "Each bar = 1 day" },
      "90d": { title: "Last 90 Days", hint: "Each bar = 1 day" },
      "1y": { title: "Last Year", hint: "Each bar = 1 day" }
    };

    // Render uptime bars for a container
    function renderUptimeBars(container, data) {
      container.innerHTML = data.map(item => {
        const uptime = item.uptime;
        const label = item.time || item.label;
        // -1 indicates no data for this time bucket
        if (uptime < 0) {
          return ` + "`" + `<div class="uptime-bar" title="${label}: No data">
            <div class="uptime-bar-fill nodata" style="height: 30%"></div>
          </div>` + "`" + `;
        }
        const colorClass = uptime >= 99.5 ? "good" : uptime >= 95 ? "warning" : "bad";
        const height = Math.max(5, uptime);
        return ` + "`" + `<div class="uptime-bar" title="${label}: ${uptime.toFixed(1)}%">
          <div class="uptime-bar-fill ${colorClass}" style="height: ${height}%"></div>
        </div>` + "`" + `;
      }).join("");
    }

    function getGlobalUptimeData(range) {
      const container = document.getElementById("globalUptimeBars");
      if (!container) return [];
      const dataStr = container.getAttribute("data-uptime-" + range);
      let data = [];
      try {
        data = JSON.parse(dataStr || "[]");
      } catch (e) {
        data = [];
      }
      return data;
    }

    // Render global uptime bars
    function renderGlobalUptime(range = "24h") {
      const container = document.getElementById("globalUptimeBars");
      if (!container) return;
      
      const data = getGlobalUptimeData(range);
      renderUptimeBars(container, data);
    }

    // Render per-monitor uptime bars for a specific range
    function renderMonitorUptime(uptimeSection, range) {
      const barsContainer = uptimeSection.querySelector(".uptime-bars");
      if (!barsContainer) return;

      const dataAttr = "data-history-" + range;
      const dataStr = barsContainer.getAttribute(dataAttr);
      
      let data = [];
      try {
        data = JSON.parse(dataStr || "[]");
      } catch (e) {
        console.error("Failed to parse uptime data:", e);
      }

      renderUptimeBars(barsContainer, data);

      // Update labels
      const rangeLabel = uptimeSection.querySelector(".uptime-range-label");
      const hintLabel = uptimeSection.querySelector(".uptime-bar-hint");
      if (rangeLabel && rangeLabels[range]) {
        rangeLabel.textContent = "Uptime History (" + rangeLabels[range].title.replace("Last ", "") + ")";
      }
      if (hintLabel && rangeLabels[range]) {
        hintLabel.textContent = rangeLabels[range].hint;
      }
    }

    // Render latency chart for a specific range
    function renderLatencyChartForRange(card, range) {
      const wrapper = card.querySelector(".latency-chart-wrapper");
      const container = card.querySelector(".latency-chart-container");
      if (!wrapper || !container) return;

      const dataAttr = "data-latency-" + range;
      const dataStr = wrapper.getAttribute(dataAttr);
      
      const downtimeAttr = "data-downtime-" + range;
      const downtimeStr = wrapper.getAttribute(downtimeAttr);
      
      wrapper.dataset.currentRange = range;
      
      // Update title
      const titleEl = container.querySelector(".latency-chart-title");
      if (titleEl && rangeLabels[range]) {
        titleEl.textContent = "Response Time (" + rangeLabels[range].title.replace("Last ", "") + ")";
      }

      // Re-render the chart with downtime data
      renderLatencyChartWithData(wrapper, dataStr, downtimeStr);
    }

    // Setup per-monitor uptime range buttons
    function setupMonitorUptimeRanges() {
      document.querySelectorAll(".component-card").forEach(card => {
        const uptimeSection = card.querySelector(".component-uptime");
        if (!uptimeSection) return;

        const btns = uptimeSection.querySelectorAll(".range-btn");
        btns.forEach(btn => {
          btn.addEventListener("click", () => {
            btns.forEach(b => b.classList.remove("active"));
            btn.classList.add("active");
            const range = btn.dataset.range;
            
            // Update uptime bars
            renderMonitorUptime(uptimeSection, range);
            
            // Also update latency chart
            renderLatencyChartForRange(card, range);
          });
        });

        // Initial render with default range (1h)
        renderMonitorUptime(uptimeSection, "1h");
      });
    }

    // Calculate nice round Y-axis values for charts
    function getNiceAxisValues(minVal, maxVal, numTicks) {
      // Nice intervals to use: 1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, etc.
      const niceIntervals = [1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000, 10000];
      
      const range = maxVal - minVal;
      const roughInterval = range / (numTicks - 1);
      
      // Find the best nice interval
      let interval = niceIntervals[niceIntervals.length - 1];
      for (let i = 0; i < niceIntervals.length; i++) {
        if (niceIntervals[i] >= roughInterval) {
          interval = niceIntervals[i];
          break;
        }
      }
      
      // Calculate nice min and max (round down min, round up max)
      const niceMin = Math.floor(minVal / interval) * interval;
      const niceMax = Math.ceil(maxVal / interval) * interval;
      
      // Generate tick values
      const values = [];
      for (let v = niceMax; v >= niceMin; v -= interval) {
        values.push(v);
        if (values.length >= numTicks + 1) break;
      }
      
      return { values, niceMin, niceMax };
    }

    // Render latency chart with specific data string
    function renderLatencyChartWithData(wrapper, dataStr, downtimeStr) {
      const svg = wrapper.querySelector(".latency-chart");
      const tooltip = wrapper.querySelector(".latency-tooltip");
      const container = wrapper.closest(".latency-chart-container");
      
      if (!svg) return;
      
      let data;
      try {
        data = JSON.parse(dataStr || "[]");
      } catch (e) {
        return;
      }
      
      // Parse downtime periods
      let downtimePeriods = [];
      try {
        downtimePeriods = JSON.parse(downtimeStr || "[]");
      } catch (e) {
        downtimePeriods = [];
      }
      
      if (!data || data.length === 0) {
        // Clear SVG but keep defs
        const defsEl = svg.querySelector("defs");
        svg.innerHTML = (defsEl ? defsEl.outerHTML : "") + '<text x="50%" y="50%" text-anchor="middle" fill="var(--text-muted)" font-size="12">No data available</text>';
        if (container) {
          const minEl = container.querySelector('[data-stat="min"]');
          const maxEl = container.querySelector('[data-stat="max"]');
          const avgEl = container.querySelector('[data-stat="avg"]');
          if (minEl) minEl.textContent = "—";
          if (maxEl) maxEl.textContent = "—";
          if (avgEl) avgEl.textContent = "—";
        }
        return;
      }

      const latencies = data.map(d => d.latency);
      const dataMaxVal = Math.max(...latencies);
      const dataMinVal = Math.min(...latencies);
      const avgVal = Math.round(latencies.reduce((a, b) => a + b, 0) / latencies.length);
      
      // Calculate nice Y-axis values
      const niceAxis = getNiceAxisValues(0, dataMaxVal, 3);
      const minVal = niceAxis.niceMin;
      const maxVal = niceAxis.niceMax;
      const range = maxVal - minVal || 1;
      
      // Update stats (use actual data values, not nice axis values)
      if (container) {
        const minEl = container.querySelector('[data-stat="min"]');
        const maxEl = container.querySelector('[data-stat="max"]');
        const avgEl = container.querySelector('[data-stat="avg"]');
        if (minEl) minEl.textContent = dataMinVal + "ms";
        if (maxEl) maxEl.textContent = dataMaxVal + "ms";
        if (avgEl) avgEl.textContent = avgVal + "ms";
      }
      
      // Get actual dimensions
      const rect = wrapper.getBoundingClientRect();
      const width = rect.width || 400;
      const height = rect.height || 80;
      // Increased padding for axis labels
      const padding = { top: 5, bottom: 18, left: 35, right: 10 };
      const chartHeight = height - padding.top - padding.bottom;
      const chartWidth = width - padding.left - padding.right;
      
      // Update viewBox to match actual dimensions
      svg.setAttribute("viewBox", "0 0 " + width + " " + height);
      
      // Get time range - use FULL selected range, not just data range
      const currentRange = wrapper.dataset.currentRange || "1h";
      const now = Math.floor(Date.now() / 1000);
      
      // Calculate expected time range based on selected period
      let expectedDuration;
      switch(currentRange) {
        case "1h": expectedDuration = 3600; break;
        case "24h": expectedDuration = 86400; break;
        case "30d": expectedDuration = 30 * 86400; break;
        case "90d": expectedDuration = 90 * 86400; break;
        case "1y": expectedDuration = 365 * 86400; break;
        default: expectedDuration = 3600;
      }
      
      // Use full time range from (now - duration) to now
      const rangeEndUnix = now;
      const rangeStartUnix = now - expectedDuration;
      const timeRange = expectedDuration;
      
      // Get actual data time range
      const unixTimes = data.map(d => d.unix).filter(u => u);
      const dataMinUnix = unixTimes.length > 0 ? Math.min(...unixTimes) : rangeStartUnix;
      const dataMaxUnix = unixTimes.length > 0 ? Math.max(...unixTimes) : rangeEndUnix;
      
      // Calculate gap threshold based on detecting sparse regions in data
      // The goal is to show gaps when monitoring wasn't actively collecting data
      
      // Calculate intervals between consecutive data points
      const intervals = [];
      for (let i = 1; i < data.length; i++) {
        if (data[i].unix && data[i-1].unix) {
          intervals.push(data[i].unix - data[i-1].unix);
        }
      }
      
      // Use cluster-based gap detection
      // Find the "dense" interval (where most data points are close together)
      // Then flag anything much larger as a gap
      let gapThreshold = 300; // default 5 minutes
      if (intervals.length > 2) {
        const sortedIntervals = [...intervals].sort((a, b) => a - b);
        
        // Use 10th percentile as the "dense cluster" interval
        // This represents how close points are in the densest parts of data
        const p10Idx = Math.max(0, Math.floor(sortedIntervals.length * 0.10));
        const denseInterval = sortedIntervals[p10Idx] || sortedIntervals[0];
        
        // A gap is when interval is significantly larger than dense regions
        // Use 6x multiplier - aggressive enough to catch sparse regions
        const dynamicThreshold = Math.max(denseInterval * 6, 180); // At least 3 minutes
        
        // Cap maximum gap threshold based on time range
        // This ensures we don't miss gaps even if data is uniformly sparse
        let maxThreshold;
        switch(currentRange) {
          case "1h": maxThreshold = 300; break;      // Max 5 min gap threshold for 1h
          case "24h": maxThreshold = 900; break;     // Max 15 min gap threshold for 24h
          case "30d": maxThreshold = 7200; break;    // Max 2 hour gap threshold for 30d
          case "90d": maxThreshold = 14400; break;   // Max 4 hour gap threshold for 90d
          case "1y": maxThreshold = 43200; break;    // Max 12 hour gap threshold for 1y
          default: maxThreshold = 600;
        }
        
        gapThreshold = Math.min(dynamicThreshold, maxThreshold);
      }
      
      // Calculate points with percentage-based x positions (using FULL range)
      const points = data.map((d, i) => {
        // Use Unix timestamp for positioning based on full selected range
        let x;
        if (d.unix && timeRange > 0) {
          x = padding.left + ((d.unix - rangeStartUnix) / timeRange) * chartWidth;
        } else {
          x = padding.left + (i / (data.length - 1 || 1)) * chartWidth;
        }
        const y = padding.top + chartHeight - ((d.latency - minVal) / range) * chartHeight;
        return { x, y, latency: d.latency, time: d.time, unix: d.unix, pctX: (x / width) * 100 };
      });
      
      // Split points into segments based on time gaps
      function splitIntoSegments(pts, threshold) {
        if (pts.length === 0) return [];
        const segments = [];
        let currentSegment = [pts[0]];
        
        for (let i = 1; i < pts.length; i++) {
          const timeDiff = pts[i].unix - pts[i-1].unix;
          if (timeDiff > threshold) {
            // Gap detected - save current segment and start new one
            if (currentSegment.length > 0) {
              segments.push({
                points: currentSegment,
                gapAfter: { startX: pts[i-1].x, endX: pts[i].x, startUnix: pts[i-1].unix, endUnix: pts[i].unix }
              });
            }
            currentSegment = [pts[i]];
          } else {
            currentSegment.push(pts[i]);
          }
        }
        // Add the last segment
        if (currentSegment.length > 0) {
          segments.push({ points: currentSegment, gapAfter: null });
        }
        return segments;
      }
      
      // Create smooth curve using cubic bezier for a segment
      function createSmoothPath(pts) {
        if (pts.length < 2) {
          if (pts.length === 1) {
            // Single point - just return a move command (will be shown as dot)
            return "M " + pts[0].x.toFixed(2) + "," + pts[0].y.toFixed(2);
          }
          return "";
        }
        let d = "M " + pts[0].x.toFixed(2) + "," + pts[0].y.toFixed(2);
        for (let i = 0; i < pts.length - 1; i++) {
          const p0 = pts[Math.max(0, i - 1)];
          const p1 = pts[i];
          const p2 = pts[i + 1];
          const p3 = pts[Math.min(pts.length - 1, i + 2)];
          const tension = 0.3;
          const cp1x = p1.x + (p2.x - p0.x) * tension;
          const cp1y = p1.y + (p2.y - p0.y) * tension;
          const cp2x = p2.x - (p3.x - p1.x) * tension;
          const cp2y = p2.y - (p3.y - p1.y) * tension;
          d += " C " + cp1x.toFixed(2) + "," + cp1y.toFixed(2) + " " + cp2x.toFixed(2) + "," + cp2y.toFixed(2) + " " + p2.x.toFixed(2) + "," + p2.y.toFixed(2);
        }
        return d;
      }
      
      // Create area path for a segment
      function createAreaPath(pts, linePath) {
        if (pts.length < 2) return "";
        const lastPt = pts[pts.length - 1];
        const firstPt = pts[0];
        return linePath + " L " + lastPt.x.toFixed(2) + "," + (height - padding.bottom) + " L " + firstPt.x.toFixed(2) + "," + (height - padding.bottom) + " Z";
      }
      
      // Helper function to find x position for a Unix timestamp (using full range)
      function findXForUnix(unixTime) {
        if (!unixTime || timeRange <= 0) {
          return null;
        }
        // Calculate position based on where this timestamp falls in the FULL time range
        const position = (unixTime - rangeStartUnix) / timeRange;
        return padding.left + position * chartWidth;
      }
      
      const monitorId = container?.dataset.monitorId || "default";
      
      // Split data into segments
      const segments = splitIntoSegments(points, gapThreshold);
      
      // Add leading gap (from range start to first data point)
      let leadingGap = null;
      if (points.length > 0 && dataMinUnix > rangeStartUnix + gapThreshold) {
        const leadingGapEndX = points[0].x;
        leadingGap = {
          startX: padding.left,
          endX: leadingGapEndX,
          startUnix: rangeStartUnix,
          endUnix: dataMinUnix
        };
      }
      
      // Add trailing gap (from last data point to range end)
      let trailingGap = null;
      if (points.length > 0 && rangeEndUnix > dataMaxUnix + gapThreshold) {
        const trailingGapStartX = points[points.length - 1].x;
        trailingGap = {
          startX: trailingGapStartX,
          endX: padding.left + chartWidth,
          startUnix: dataMaxUnix,
          endUnix: rangeEndUnix
        };
      }
      
      // Build SVG content
      const defsEl = svg.querySelector("defs");
      let svgContent = defsEl ? defsEl.outerHTML : "";
      
      // Add gradient definitions
      svgContent += '<defs>';
      svgContent += '<linearGradient id="downtimeGradient-' + monitorId + '" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stop-color="rgba(239, 68, 68, 0.25)"/><stop offset="100%" stop-color="rgba(239, 68, 68, 0.05)"/></linearGradient>';
      svgContent += '<linearGradient id="noDataGradient-' + monitorId + '" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stop-color="rgba(113, 113, 122, 0.15)"/><stop offset="100%" stop-color="rgba(113, 113, 122, 0.02)"/></linearGradient>';
      svgContent += '</defs>';
      
      // Add subtle horizontal grid lines and Y-axis labels using nice values
      const yAxisValues = niceAxis.values.slice(0, 3); // Use top 3 nice values
      for (let i = 0; i < yAxisValues.length; i++) {
        const labelValue = yAxisValues[i];
        const y = padding.top + ((maxVal - labelValue) / range) * chartHeight;
        // Grid line (skip top and bottom lines for cleaner look)
        if (i === 1) {
          svgContent += '<line class="latency-grid-line" x1="' + padding.left + '" y1="' + y.toFixed(2) + '" x2="' + (width - padding.right) + '" y2="' + y.toFixed(2) + '"/>';
        }
        // Y-axis label
        svgContent += '<text class="latency-axis-label latency-axis-label-y" x="' + (padding.left - 4) + '" y="' + (y + 3).toFixed(2) + '">' + labelValue + 'ms</text>';
      }
      
      // Add X-axis time labels
      // Calculate time span to determine appropriate label format
      const timeSpanHours = timeRange / 3600;
      const timeSpanDays = timeRange / 86400;
      
      function formatTimeLabel(unix, rangeType) {
        const d = new Date(unix * 1000);
        const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
        
        // Format based on ACTUAL time span for clarity
        if (timeSpanDays >= 30) {
          // Very long spans: show month abbreviation
          return months[d.getUTCMonth()] + ' ' + d.getUTCDate();
        } else if (timeSpanDays >= 2) {
          // Multi-day spans: show month/day
          return (d.getUTCMonth() + 1) + '/' + d.getUTCDate();
        } else if (timeSpanHours >= 4) {
          // Several hours: show hour only
          return d.getUTCHours().toString().padStart(2, '0') + ':00';
        } else {
          // Short spans: show hour:minute
          return d.getUTCHours().toString().padStart(2, '0') + ':' + d.getUTCMinutes().toString().padStart(2, '0');
        }
      }
      
      // Add X-axis labels at intervals (using FULL selected range)
      const numXLabels = 5;
      for (let i = 0; i < numXLabels; i++) {
        const pct = i / (numXLabels - 1);
        const x = padding.left + pct * chartWidth;
        const unix = rangeStartUnix + pct * timeRange;
        const label = formatTimeLabel(unix, currentRange);
        svgContent += '<text class="latency-axis-label latency-axis-label-x" x="' + x.toFixed(2) + '" y="' + (height - 2) + '">' + label + '</text>';
      }
      
      // Add downtime shaded areas BEFORE the latency line so they appear behind
      if (downtimePeriods && downtimePeriods.length > 0 && timeRange > 0) {
        downtimePeriods.forEach((period) => {
          // Use Unix timestamps for accurate positioning
          let startX = findXForUnix(period.startUnix);
          let endX = findXForUnix(period.endUnix);
          
          // Skip if we couldn't calculate positions
          if (startX === null || endX === null) {
            return;
          }
          
          // Ensure minimum width for visibility
          const minWidth = 4;
          if (endX - startX < minWidth) {
            const center = (startX + endX) / 2;
            startX = center - minWidth / 2;
            endX = center + minWidth / 2;
          }
          
          // Clamp to chart bounds
          startX = Math.max(padding.left, Math.min(startX, width - padding.right));
          endX = Math.max(padding.left, Math.min(endX, width - padding.right));
          
          // Draw shaded rectangle for downtime period
          svgContent += '<rect class="downtime-area" x="' + startX.toFixed(2) + '" y="' + padding.top + '" width="' + (endX - startX).toFixed(2) + '" height="' + chartHeight + '" fill="url(#downtimeGradient-' + monitorId + ')" rx="2" data-start="' + period.start + '" data-end="' + period.end + '"/>';
          
          // Draw subtle border lines on left and right edges
          svgContent += '<line class="downtime-border-left" x1="' + startX.toFixed(2) + '" y1="' + padding.top + '" x2="' + startX.toFixed(2) + '" y2="' + (height - padding.bottom) + '"/>';
          svgContent += '<line class="downtime-border-right" x1="' + endX.toFixed(2) + '" y1="' + padding.top + '" x2="' + endX.toFixed(2) + '" y2="' + (height - padding.bottom) + '"/>';
        });
      }
      
      // Draw "no data" gaps (gray shaded areas)
      const gapAreas = [];
      const midY = padding.top + chartHeight / 2;
      
      // Helper function to draw a gap
      function drawGap(gap) {
        const gapWidth = gap.endX - gap.startX;
        if (gapWidth > 8) { // Only show if gap is visible enough
          // Draw subtle shaded area for gap
          svgContent += '<rect class="no-data-area" x="' + gap.startX.toFixed(2) + '" y="' + padding.top + '" width="' + gapWidth.toFixed(2) + '" height="' + chartHeight + '" fill="url(#noDataGradient-' + monitorId + ')" rx="2" data-gap-start="' + gap.startUnix + '" data-gap-end="' + gap.endUnix + '"/>';
          // Add dashed line in the middle of the gap
          svgContent += '<line class="no-data-line" x1="' + (gap.startX + 4).toFixed(2) + '" y1="' + midY.toFixed(2) + '" x2="' + (gap.endX - 4).toFixed(2) + '" y2="' + midY.toFixed(2) + '" stroke="rgba(113, 113, 122, 0.4)" stroke-width="1" stroke-dasharray="4 4"/>';
          // Store gap info for hover interactions
          gapAreas.push({ startX: gap.startX, endX: gap.endX, startUnix: gap.startUnix, endUnix: gap.endUnix });
        }
      }
      
      // Draw leading gap (before first data point)
      if (leadingGap) {
        drawGap(leadingGap);
      }
      
      // Draw gaps between segments
      segments.forEach((segment, idx) => {
        if (segment.gapAfter) {
          drawGap(segment.gapAfter);
        }
      });
      
      // Draw trailing gap (after last data point)
      if (trailingGap) {
        drawGap(trailingGap);
      }
      
      // Draw each segment separately (area fills first, then lines)
      segments.forEach((segment) => {
        const segPts = segment.points;
        if (segPts.length === 0) return;
        
        const linePath = createSmoothPath(segPts);
        if (segPts.length >= 2) {
          const areaPath = createAreaPath(segPts, linePath);
          svgContent += '<path class="latency-area" d="' + areaPath + '" fill="url(#latencyAreaGradient-' + monitorId + ')"/>';
        }
      });
      
      // Draw lines on top of areas
      segments.forEach((segment) => {
        const segPts = segment.points;
        if (segPts.length === 0) return;
        
        const linePath = createSmoothPath(segPts);
        if (segPts.length >= 2) {
          svgContent += '<path class="latency-line" d="' + linePath + '"/>';
        } else if (segPts.length === 1) {
          // Single point - draw a dot
          svgContent += '<circle class="latency-single-point" cx="' + segPts[0].x.toFixed(2) + '" cy="' + segPts[0].y.toFixed(2) + '" r="3" fill="var(--cyan)"/>';
        }
      });
      
      // Add dots for hover - sample every Nth point to avoid clutter
      const maxDots = 30;
      const step = Math.max(1, Math.floor(points.length / maxDots));
      points.forEach((p, i) => {
        if (i % step === 0 || i === points.length - 1) {
          svgContent += '<circle class="latency-dot visible" cx="' + p.x.toFixed(2) + '" cy="' + p.y.toFixed(2) + '" r="4" data-idx="' + i + '" data-latency="' + p.latency + '" data-time="' + p.time + '"/>';
        }
      });
      
      svg.innerHTML = svgContent;
      
      // Add hover interactions for latency dots
      const dots = svg.querySelectorAll(".latency-dot");
      dots.forEach((dot) => {
        const latency = dot.dataset.latency;
        const time = dot.dataset.time;
        const cx = parseFloat(dot.getAttribute("cx"));
        
        dot.addEventListener("mouseenter", () => {
          tooltip.innerHTML = '<strong>' + latency + 'ms</strong><br><span style="opacity:0.7;font-size:0.65rem">' + time + '</span>';
          tooltip.style.left = (cx / width * 100) + "%";
          tooltip.style.top = (parseFloat(dot.getAttribute("cy")) - 12) + "px";
          tooltip.style.opacity = "1";
        });
        dot.addEventListener("mouseleave", () => {
          tooltip.style.opacity = "0";
        });
      });
      
      // Add hover interactions for downtime areas
      const downtimeAreas = svg.querySelectorAll(".downtime-area");
      downtimeAreas.forEach((area) => {
        const start = area.dataset.start;
        const end = area.dataset.end;
        const areaX = parseFloat(area.getAttribute("x"));
        const areaWidth = parseFloat(area.getAttribute("width"));
        const centerX = areaX + areaWidth / 2;
        
        area.style.cursor = "pointer";
        area.addEventListener("mouseenter", () => {
          tooltip.innerHTML = '<strong style="color: var(--danger);">Downtime</strong><br><span style="opacity:0.7;font-size:0.65rem">' + start + ' – ' + end + '</span>';
          tooltip.style.left = (centerX / width * 100) + "%";
          tooltip.style.top = (padding.top + chartHeight / 2) + "px";
          tooltip.style.opacity = "1";
        });
        area.addEventListener("mouseleave", () => {
          tooltip.style.opacity = "0";
        });
      });
      
      // Add hover interactions for no-data gap areas
      const noDataAreas = svg.querySelectorAll(".no-data-area");
      noDataAreas.forEach((area) => {
        const gapStart = parseInt(area.dataset.gapStart);
        const gapEnd = parseInt(area.dataset.gapEnd);
        const areaX = parseFloat(area.getAttribute("x"));
        const areaWidth = parseFloat(area.getAttribute("width"));
        const centerX = areaX + areaWidth / 2;
        
        // Format duration
        const durationSec = gapEnd - gapStart;
        let durationText;
        if (durationSec >= 86400) {
          durationText = Math.round(durationSec / 86400) + " day(s)";
        } else if (durationSec >= 3600) {
          durationText = Math.round(durationSec / 3600) + " hour(s)";
        } else {
          durationText = Math.round(durationSec / 60) + " min";
        }
        
        area.style.cursor = "help";
        area.style.pointerEvents = "all";
        area.addEventListener("mouseenter", () => {
          tooltip.innerHTML = '<strong style="color: var(--text-muted);">No Data</strong><br><span style="opacity:0.7;font-size:0.65rem">Gap: ~' + durationText + '</span>';
          tooltip.style.left = (centerX / width * 100) + "%";
          tooltip.style.top = (padding.top + chartHeight / 2) + "px";
          tooltip.style.opacity = "1";
        });
        area.addEventListener("mouseleave", () => {
          tooltip.style.opacity = "0";
        });
      });
      
      // Check if there are data gaps (more than 1 segment means there are gaps)
      const hasGaps = segments.length > 1 || leadingGap !== null || trailingGap !== null;
      
      // Update legend if downtimes or gaps exist
      updateChartLegend(container, downtimePeriods.length > 0, hasGaps);
    }
    
    // Update chart legend to show downtime and no-data indicators
    function updateChartLegend(container, hasDowntime, hasGaps) {
      if (!container) return;
      
      // Remove existing legend
      const existingLegend = container.querySelector(".latency-chart-legend");
      if (existingLegend) {
        existingLegend.remove();
      }
      
      // Add legend if there are downtime periods or data gaps
      if (hasDowntime || hasGaps) {
        const legend = document.createElement("div");
        legend.className = "latency-chart-legend";
        let legendHTML = '<div class="chart-legend-item"><div class="legend-line-response"></div><span>Response Time</span></div>';
        
        if (hasGaps) {
          legendHTML += '<div class="chart-legend-item"><div class="legend-area-nodata"></div><span>No Data</span></div>';
        }
        
        if (hasDowntime) {
          legendHTML += '<div class="chart-legend-item"><div class="legend-area-downtime"></div><span>Downtime</span></div>';
        }
        
        legend.innerHTML = legendHTML;
        container.appendChild(legend);
      }
    }

    // Render latency charts with improved design (initial load)
    function renderLatencyCharts() {
      document.querySelectorAll(".latency-chart-wrapper").forEach(wrapper => {
        const currentRange = wrapper.dataset.currentRange || "1h";
        const dataStr = wrapper.getAttribute("data-latency-" + currentRange);
        const downtimeStr = wrapper.getAttribute("data-downtime-" + currentRange);
        renderLatencyChartWithData(wrapper, dataStr, downtimeStr);
      });
    }

    function applyComponentFilter(filter) {
      const activeFilter = filter || "all";
      const filterBtns = document.querySelectorAll(".components-section .filter-pill");
      const cards = document.querySelectorAll(".component-card");

      filterBtns.forEach(btn => {
        btn.classList.toggle("active", btn.dataset.filter === activeFilter);
      });

      cards.forEach(card => {
        const status = card.dataset.status;
        card.style.display = (activeFilter === "all" || status === activeFilter) ? "" : "none";
      });
    }

    // Filter components
    function setupFilters() {
      const filterBtns = document.querySelectorAll(".components-section .filter-pill");
      
      filterBtns.forEach(btn => {
        btn.addEventListener("click", () => {
          applyComponentFilter(btn.dataset.filter);
        });
      });
    }

    // View toggle (detailed/compact)
    let escapeHandlerBound = false;

    function ensureEscapeHandler() {
      if (escapeHandlerBound) return;
      escapeHandlerBound = true;
      document.addEventListener("keydown", (e) => {
        if (e.key !== "Escape") return;
        const section = document.querySelector(".components-section");
        if (!section) return;
        if (section.classList.contains("compact-fullscreen")) {
          section.classList.remove("compact-fullscreen");
        }
      });
    }

    function setupViewToggle() {
      const viewBtns = document.querySelectorAll(".view-toggle .view-btn");
      const cardsContainer = document.getElementById("componentCards");
      const componentsSection = document.querySelector(".components-section");
      const fullscreenBtn = document.getElementById("fullscreenBtn");
      const cards = document.querySelectorAll(".component-card");
      
      // Restore saved preference
      const savedView = localStorage.getItem("statusPageViewMode") || "detailed";
      applyViewMode(savedView);
      
      viewBtns.forEach(btn => {
        btn.addEventListener("click", () => {
          const view = btn.dataset.view;
          applyViewMode(view);
          localStorage.setItem("statusPageViewMode", view);
        });
      });
      
      // Fullscreen toggle
      fullscreenBtn.addEventListener("click", () => {
        componentsSection.classList.toggle("compact-fullscreen");
      });
      
      // Exit fullscreen on Escape key (global handler)
      ensureEscapeHandler();
      
      function applyViewMode(view) {
        viewBtns.forEach(b => b.classList.remove("active"));
        document.querySelector('.view-btn[data-view="' + view + '"]').classList.add("active");
        
        if (view === "compact") {
          cardsContainer.classList.add("compact");
          componentsSection.classList.add("compact-active");
          cards.forEach(card => card.classList.add("compact-mode"));
        } else {
          cardsContainer.classList.remove("compact");
          componentsSection.classList.remove("compact-active");
          componentsSection.classList.remove("compact-fullscreen");
          cards.forEach(card => card.classList.remove("compact-mode"));
        }
      }
    }

    // Global uptime range selector
    function applyGlobalUptimeRange(range) {
      const activeRange = range || "24h";
      const rangeBtns = document.querySelectorAll(".global-uptime-section .filter-pill");
      rangeBtns.forEach(btn => {
        btn.classList.toggle("active", btn.dataset.range === activeRange);
      });
      renderGlobalUptime(activeRange);
    }

    function setupGlobalUptimeRange() {
      const rangeBtns = document.querySelectorAll(".global-uptime-section .filter-pill");
      rangeBtns.forEach(btn => {
        btn.addEventListener("click", () => {
          applyGlobalUptimeRange(btn.dataset.range);
        });
      });
    }

    function setupCopyStatusLink() {
      const btn = document.getElementById("copyStatusLink");
      if (!btn) return;
      btn.addEventListener("click", () => {
        navigator.clipboard?.writeText(window.location.href).then(
          () => alert("Status URL copied!"),
          () => alert(window.location.href)
        );
      });
    }

    function updateFooterYear() {
      const footerYear = document.getElementById("footerYear");
      if (footerYear) footerYear.textContent = new Date().getFullYear();
    }

    function updateLastUpdated() {
      const lastUpdated = document.getElementById("lastUpdated");
      if (!lastUpdated) return;
      const now = new Date();
      lastUpdated.textContent = ` + "`" + `Updated ${now.toISOString().slice(11, 19)} UTC` + "`" + `;
    }

    // Fullscreen chart functionality
    let currentFullscreenData = null;
    let currentFullscreenWrapper = null;
    let currentFullscreenMonitorName = null;
    
    function openChartFullscreen(btn) {
      const container = btn.closest('.latency-chart-container');
      if (!container) return;
      
      const wrapper = container.querySelector('.latency-chart-wrapper');
      if (!wrapper) return;
      
      const currentRange = wrapper.dataset.currentRange || "1h";
      const dataStr = wrapper.getAttribute("data-latency-" + currentRange);
      const downtimeStr = wrapper.getAttribute("data-downtime-" + currentRange);
      const monitorName = container.dataset.monitorName || "Monitor";
      const monitorId = container.dataset.monitorId || "default";
      
      // Store wrapper reference for range changes
      currentFullscreenWrapper = wrapper;
      currentFullscreenMonitorName = monitorName;
      
      // Update stats from data
      updateFullscreenStats(dataStr, currentRange, monitorName);
      
      // Store data for rendering
      currentFullscreenData = { dataStr, downtimeStr, currentRange, monitorId };
      
      // Update range button active state
      updateFullscreenRangeButtons(currentRange);
      
      // Show modal
      const modal = document.getElementById('chartFullscreenModal');
      modal.classList.remove('hidden');
      
      // Render fullscreen chart
      setTimeout(() => renderFullscreenChart(), 50);
      
      // Add escape key listener
      document.addEventListener('keydown', handleFullscreenEscape);
    }
    
    function changeFullscreenRange(range) {
      if (!currentFullscreenWrapper) return;
      
      const dataStr = currentFullscreenWrapper.getAttribute("data-latency-" + range);
      const downtimeStr = currentFullscreenWrapper.getAttribute("data-downtime-" + range);
      const monitorId = currentFullscreenWrapper.closest('.latency-chart-container')?.dataset.monitorId || "default";
      
      // Update stats
      updateFullscreenStats(dataStr, range, currentFullscreenMonitorName);
      
      // Update data
      currentFullscreenData = { dataStr, downtimeStr, currentRange: range, monitorId };
      
      // Update button states
      updateFullscreenRangeButtons(range);
      
      // Re-render chart
      renderFullscreenChart();
    }
    
    function updateFullscreenRangeButtons(activeRange) {
      document.querySelectorAll('.fs-range-btn').forEach(btn => {
        btn.classList.toggle('active', btn.dataset.range === activeRange);
      });
    }
    
    function updateFullscreenStats(dataStr, range, monitorName) {
      let data = [];
      try {
        data = JSON.parse(dataStr || '[]');
      } catch(e) {}
      
      const latencies = data.map(d => d.latency).filter(l => l > 0);
      const minVal = latencies.length > 0 ? Math.min(...latencies) : 0;
      const maxVal = latencies.length > 0 ? Math.max(...latencies) : 0;
      const avgVal = latencies.length > 0 ? Math.round(latencies.reduce((a, b) => a + b, 0) / latencies.length) : 0;
      
      document.getElementById('fullscreenChartTitle').textContent = monitorName + ' - Response Time (' + rangeLabels[range].title.replace("Last ", "") + ')';
      document.getElementById('fullscreenStatMin').textContent = minVal > 0 ? minVal + 'ms' : '—';
      document.getElementById('fullscreenStatAvg').textContent = avgVal > 0 ? avgVal + 'ms' : '—';
      document.getElementById('fullscreenStatMax').textContent = maxVal > 0 ? maxVal + 'ms' : '—';
    }
    
    function closeChartFullscreen() {
      const modal = document.getElementById('chartFullscreenModal');
      modal.classList.add('hidden');
      currentFullscreenData = null;
      currentFullscreenWrapper = null;
      currentFullscreenMonitorName = null;
      document.removeEventListener('keydown', handleFullscreenEscape);
    }
    
    function handleFullscreenEscape(e) {
      if (e.key === 'Escape') {
        closeChartFullscreen();
      }
    }
    
    function renderFullscreenChart() {
      if (!currentFullscreenData) return;
      
      const { dataStr, downtimeStr, currentRange, monitorId } = currentFullscreenData;
      const wrapper = document.getElementById('fullscreenChartWrapper');
      const svg = document.getElementById('fullscreenChartSvg');
      const legendEl = document.getElementById('fullscreenChartLegend');
      
      let data;
      try {
        data = JSON.parse(dataStr || "[]");
      } catch (e) {
        return;
      }
      
      let downtimePeriods = [];
      try {
        downtimePeriods = JSON.parse(downtimeStr || "[]");
      } catch (e) {
        downtimePeriods = [];
      }
      
      if (!data || data.length === 0) {
        svg.innerHTML = '<text x="50%" y="50%" text-anchor="middle" fill="var(--text-muted)" font-size="16">No data available</text>';
        return;
      }
      
      const latencies = data.map(d => d.latency);
      const dataMaxVal = Math.max(...latencies);
      const dataMinVal = Math.min(...latencies);
      const avgVal = Math.round(latencies.reduce((a, b) => a + b, 0) / latencies.length);
      
      // Calculate nice Y-axis values (start from 0 for better readability)
      const niceAxis = getNiceAxisValues(0, dataMaxVal, 6);
      const minVal = niceAxis.niceMin;
      const maxVal = niceAxis.niceMax;
      const range = maxVal - minVal || 1;
      
      // Get dimensions - larger padding for clearer labels
      const rect = wrapper.getBoundingClientRect();
      const width = rect.width || 800;
      const height = rect.height || 400;
      const padding = { top: 30, bottom: 60, left: 80, right: 40 };
      const chartHeight = height - padding.top - padding.bottom;
      const chartWidth = width - padding.left - padding.right;
      
      svg.setAttribute("viewBox", "0 0 " + width + " " + height);
      
      // Get time range - use FULL selected range, not just data range
      const now = Math.floor(Date.now() / 1000);
      
      // Calculate expected time range based on selected period
      let expectedDuration;
      switch(currentRange) {
        case "1h": expectedDuration = 3600; break;
        case "24h": expectedDuration = 86400; break;
        case "30d": expectedDuration = 30 * 86400; break;
        case "90d": expectedDuration = 90 * 86400; break;
        case "1y": expectedDuration = 365 * 86400; break;
        default: expectedDuration = 3600;
      }
      
      // Use full time range from (now - duration) to now
      const rangeEndUnix = now;
      const rangeStartUnix = now - expectedDuration;
      const timeRange = expectedDuration;
      
      // Get actual data time range
      const unixTimes = data.map(d => d.unix).filter(u => u);
      const dataMinUnix = unixTimes.length > 0 ? Math.min(...unixTimes) : rangeStartUnix;
      const dataMaxUnix = unixTimes.length > 0 ? Math.max(...unixTimes) : rangeEndUnix;
      
      // Gap detection (same as normal chart)
      const intervals = [];
      for (let i = 1; i < data.length; i++) {
        if (data[i].unix && data[i-1].unix) {
          intervals.push(data[i].unix - data[i-1].unix);
        }
      }
      
      let gapThreshold = 300;
      if (intervals.length > 2) {
        const sortedIntervals = [...intervals].sort((a, b) => a - b);
        const p10Idx = Math.max(0, Math.floor(sortedIntervals.length * 0.10));
        const denseInterval = sortedIntervals[p10Idx] || sortedIntervals[0];
        const dynamicThreshold = Math.max(denseInterval * 6, 180);
        let maxThreshold;
        switch(currentRange) {
          case "1h": maxThreshold = 300; break;
          case "24h": maxThreshold = 900; break;
          case "30d": maxThreshold = 7200; break;
          case "90d": maxThreshold = 14400; break;
          case "1y": maxThreshold = 43200; break;
          default: maxThreshold = 600;
        }
        gapThreshold = Math.min(dynamicThreshold, maxThreshold);
      }
      
      const points = data.map((d, i) => {
        let x;
        if (d.unix && timeRange > 0) {
          x = padding.left + ((d.unix - rangeStartUnix) / timeRange) * chartWidth;
        } else {
          x = padding.left + (i / (data.length - 1 || 1)) * chartWidth;
        }
        const y = padding.top + chartHeight - ((d.latency - minVal) / range) * chartHeight;
        return { x, y, latency: d.latency, time: d.time, unix: d.unix };
      });
      
      // Add leading gap (from range start to first data point)
      let leadingGap = null;
      if (points.length > 0 && dataMinUnix > rangeStartUnix + gapThreshold) {
        leadingGap = {
          startX: padding.left,
          endX: points[0].x,
          startUnix: rangeStartUnix,
          endUnix: dataMinUnix
        };
      }
      
      // Add trailing gap (from last data point to range end)
      let trailingGap = null;
      if (points.length > 0 && rangeEndUnix > dataMaxUnix + gapThreshold) {
        trailingGap = {
          startX: points[points.length - 1].x,
          endX: padding.left + chartWidth,
          startUnix: dataMaxUnix,
          endUnix: rangeEndUnix
        };
      }
      
      function splitIntoSegments(pts, threshold) {
        if (pts.length === 0) return [];
        const segments = [];
        let currentSegment = [pts[0]];
        for (let i = 1; i < pts.length; i++) {
          const timeDiff = pts[i].unix - pts[i-1].unix;
          if (timeDiff > threshold) {
            if (currentSegment.length > 0) {
              segments.push({ points: currentSegment, gapAfter: { startX: pts[i-1].x, endX: pts[i].x, startUnix: pts[i-1].unix, endUnix: pts[i].unix } });
            }
            currentSegment = [pts[i]];
          } else {
            currentSegment.push(pts[i]);
          }
        }
        if (currentSegment.length > 0) {
          segments.push({ points: currentSegment, gapAfter: null });
        }
        return segments;
      }
      
      function createSmoothPath(pts) {
        if (pts.length < 2) {
          if (pts.length === 1) return "M " + pts[0].x.toFixed(2) + "," + pts[0].y.toFixed(2);
          return "";
        }
        let d = "M " + pts[0].x.toFixed(2) + "," + pts[0].y.toFixed(2);
        for (let i = 0; i < pts.length - 1; i++) {
          const p0 = pts[Math.max(0, i - 1)];
          const p1 = pts[i];
          const p2 = pts[i + 1];
          const p3 = pts[Math.min(pts.length - 1, i + 2)];
          const tension = 0.3;
          const cp1x = p1.x + (p2.x - p0.x) * tension;
          const cp1y = p1.y + (p2.y - p0.y) * tension;
          const cp2x = p2.x - (p3.x - p1.x) * tension;
          const cp2y = p2.y - (p3.y - p1.y) * tension;
          d += " C " + cp1x.toFixed(2) + "," + cp1y.toFixed(2) + " " + cp2x.toFixed(2) + "," + cp2y.toFixed(2) + " " + p2.x.toFixed(2) + "," + p2.y.toFixed(2);
        }
        return d;
      }
      
      function createAreaPath(pts, linePath) {
        if (pts.length < 2) return "";
        const lastPt = pts[pts.length - 1];
        const firstPt = pts[0];
        return linePath + " L " + lastPt.x.toFixed(2) + "," + (height - padding.bottom) + " L " + firstPt.x.toFixed(2) + "," + (height - padding.bottom) + " Z";
      }
      
      const segments = splitIntoSegments(points, gapThreshold);
      
      let svgContent = '';
      svgContent += '<defs>';
      svgContent += '<linearGradient id="fsGradient" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stop-color="rgba(6, 182, 212, 0.5)"/><stop offset="100%" stop-color="rgba(6, 182, 212, 0.1)"/></linearGradient>';
      svgContent += '<linearGradient id="fsNoDataGradient" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stop-color="rgba(113, 113, 122, 0.3)"/><stop offset="100%" stop-color="rgba(113, 113, 122, 0.1)"/></linearGradient>';
      svgContent += '</defs>';
      
      // Draw chart background - solid dark background for contrast
      svgContent += '<rect x="' + padding.left + '" y="' + padding.top + '" width="' + chartWidth + '" height="' + chartHeight + '" fill="#111118" rx="4"/>';
      
      // Use nice Y-axis values for cleaner labels
      const yAxisValues = niceAxis.values;
      
      // Draw horizontal grid lines and Y-axis labels
      for (let i = 0; i < yAxisValues.length; i++) {
        const labelValue = yAxisValues[i];
        const y = padding.top + ((maxVal - labelValue) / range) * chartHeight;
        // Grid line - more visible
        svgContent += '<line stroke="rgba(255,255,255,0.15)" stroke-width="1" x1="' + padding.left + '" y1="' + y.toFixed(2) + '" x2="' + (width - padding.right) + '" y2="' + y.toFixed(2) + '"/>';
        // Y-axis label - brighter
        svgContent += '<text x="' + (padding.left - 12) + '" y="' + (y + 5).toFixed(2) + '" fill="rgba(255,255,255,0.85)" font-size="13" font-family="var(--font-mono)" text-anchor="end">' + labelValue + 'ms</text>';
      }
      
      // X-axis labels with vertical grid lines
      // Use the FULL selected range for time span calculations
      const timeSpanHours = expectedDuration / 3600;
      const timeSpanDays = expectedDuration / 86400;
      
      function formatTimeLabel(unix, rangeType) {
        const d = new Date(unix * 1000);
        const months = ['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec'];
        
        // Format based on selected time range
        if (timeSpanDays >= 30) {
          // For very long spans (30+ days), show month + day
          return months[d.getUTCMonth()] + ' ' + d.getUTCDate();
        } else if (timeSpanDays >= 2) {
          // For multi-day spans, show month/day + hour
          return (d.getUTCMonth() + 1) + '/' + d.getUTCDate() + ' ' + d.getUTCHours().toString().padStart(2, '0') + ':00';
        } else if (timeSpanHours >= 6) {
          // For 6+ hour spans, show day + hour
          return d.getUTCDate() + ' ' + months[d.getUTCMonth()] + ' ' + d.getUTCHours().toString().padStart(2, '0') + ':00';
        } else {
          // For short spans, show hour:minute
          return d.getUTCHours().toString().padStart(2, '0') + ':' + d.getUTCMinutes().toString().padStart(2, '0');
        }
      }
      
      // Adjust number of labels based on time span
      const numXLabels = timeSpanDays >= 7 ? 8 : 10;
      for (let i = 0; i < numXLabels; i++) {
        const pct = i / (numXLabels - 1);
        const x = padding.left + pct * chartWidth;
        const unix = rangeStartUnix + pct * timeRange;
        const label = formatTimeLabel(unix, currentRange);
        // Vertical grid line - more visible
        svgContent += '<line stroke="rgba(255,255,255,0.08)" stroke-width="1" x1="' + x.toFixed(2) + '" y1="' + padding.top + '" x2="' + x.toFixed(2) + '" y2="' + (height - padding.bottom) + '"/>';
        // X-axis label - brighter
        svgContent += '<text x="' + x.toFixed(2) + '" y="' + (height - padding.bottom + 20) + '" fill="rgba(255,255,255,0.85)" font-size="12" font-family="var(--font-mono)" text-anchor="middle">' + label + '</text>';
      }
      
      // Draw axis lines - more visible
      svgContent += '<line stroke="rgba(255,255,255,0.4)" stroke-width="2" x1="' + padding.left + '" y1="' + padding.top + '" x2="' + padding.left + '" y2="' + (height - padding.bottom) + '"/>';
      svgContent += '<line stroke="rgba(255,255,255,0.4)" stroke-width="2" x1="' + padding.left + '" y1="' + (height - padding.bottom) + '" x2="' + (width - padding.right) + '" y2="' + (height - padding.bottom) + '"/>';
      
      // Helper function to draw a gap with label
      function drawFullscreenGap(gap) {
        const { startX, endX, startUnix, endUnix } = gap;
        const gapWidth = endX - startX;
        if (gapWidth > 10) {
          svgContent += '<rect x="' + startX.toFixed(2) + '" y="' + padding.top + '" width="' + gapWidth.toFixed(2) + '" height="' + chartHeight + '" fill="url(#fsNoDataGradient)" rx="4"/>';
          const midX = (startX + endX) / 2;
          const midY = padding.top + chartHeight / 2;
          // Dashed line through gap
          svgContent += '<line x1="' + startX.toFixed(2) + '" y1="' + midY.toFixed(2) + '" x2="' + endX.toFixed(2) + '" y2="' + midY.toFixed(2) + '" stroke="rgba(255,255,255,0.3)" stroke-dasharray="10,6" stroke-width="2"/>';
          // Gap duration label
          const gapDuration = endUnix - startUnix;
          let gapLabel = '';
          if (gapDuration >= 86400) {
            gapLabel = Math.round(gapDuration / 86400) + 'd';
          } else if (gapDuration >= 3600) {
            gapLabel = Math.round(gapDuration / 3600) + 'h';
          } else {
            gapLabel = Math.round(gapDuration / 60) + 'm';
          }
          svgContent += '<text x="' + midX.toFixed(2) + '" y="' + (midY - 15).toFixed(2) + '" fill="rgba(255,255,255,0.7)" font-size="14" font-family="var(--font-mono)" text-anchor="middle">No Data (' + gapLabel + ')</text>';
        }
      }
      
      // Draw leading gap (before first data point)
      if (leadingGap) {
        drawFullscreenGap(leadingGap);
      }
      
      // Draw segments with thicker lines
      segments.forEach((segment, idx) => {
        const linePath = createSmoothPath(segment.points);
        if (segment.points.length >= 2) {
          const areaPath = createAreaPath(segment.points, linePath);
          // Area fill
          svgContent += '<path d="' + areaPath + '" fill="url(#fsGradient)"/>';
          // Main line - thicker for better visibility
          svgContent += '<path d="' + linePath + '" fill="none" stroke="var(--primary)" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"/>';
          // Add glow effect for the line
          svgContent += '<path d="' + linePath + '" fill="none" stroke="var(--primary)" stroke-width="8" stroke-linecap="round" stroke-linejoin="round" opacity="0.15"/>';
        } else if (segment.points.length === 1) {
          svgContent += '<circle cx="' + segment.points[0].x.toFixed(2) + '" cy="' + segment.points[0].y.toFixed(2) + '" r="6" fill="var(--primary)"/>';
        }
        
        // Draw data points as small circles for clarity (only when not too many points)
        // Skip circles for dense data to avoid visual clutter
        if (data.length <= 50) {
          segment.points.forEach(pt => {
            svgContent += '<circle cx="' + pt.x.toFixed(2) + '" cy="' + pt.y.toFixed(2) + '" r="3" fill="var(--primary)" stroke="rgba(0,0,0,0.5)" stroke-width="1"/>';
          });
        }
        
        // Draw gap area between segments
        if (segment.gapAfter) {
          drawFullscreenGap(segment.gapAfter);
        }
      });
      
      // Draw trailing gap (after last data point)
      if (trailingGap) {
        drawFullscreenGap(trailingGap);
      }
      
      // Add axis labels - more visible
      svgContent += '<text x="' + (padding.left / 2) + '" y="' + (height / 2) + '" fill="rgba(255,255,255,0.7)" font-size="14" text-anchor="middle" transform="rotate(-90 ' + (padding.left / 2 - 15) + ' ' + (height / 2) + ')">Latency (ms)</text>';
      svgContent += '<text x="' + (padding.left + chartWidth / 2) + '" y="' + (height - 10) + '" fill="rgba(255,255,255,0.7)" font-size="14" text-anchor="middle">Time (UTC)</text>';
      
      svg.innerHTML = svgContent;
      
      // Update legend
      const hasGaps = segments.length > 1 || leadingGap !== null || trailingGap !== null;
      let legendHtml = '<div class="chart-legend-item"><div class="legend-line-response" style="width:24px;height:3px;"></div><span style="font-size:14px;">Response Time</span></div>';
      if (hasGaps) {
        legendHtml += '<div class="chart-legend-item"><div class="legend-area-nodata" style="width:24px;height:14px;"></div><span style="font-size:14px;">No Data</span></div>';
      }
      legendEl.innerHTML = legendHtml;
    }
    
    // Handle window resize for fullscreen
    window.addEventListener('resize', () => {
      if (currentFullscreenData) {
        renderFullscreenChart();
      }
    });

    let refreshTimer = null;
    let refreshInFlight = false;
    let refreshPending = false;
    let isProgrammaticScroll = false;
    let lastUserScrollY = window.scrollY;

    window.addEventListener("scroll", () => {
      if (isProgrammaticScroll) return;
      lastUserScrollY = window.scrollY;
    }, { passive: true });

    function scheduleRefresh() {
      if (refreshTimer) return;
      refreshTimer = setTimeout(() => {
        refreshTimer = null;
        refreshStatusPage();
      }, 1000);
    }

    function restoreScrollPosition(scrollY) {
      const rawTarget = Math.max(0, Number(scrollY) || 0);
      const maxScrollableY = Math.max(0, document.documentElement.scrollHeight - window.innerHeight);
      const targetY = Math.min(rawTarget, maxScrollableY);
      if (Math.abs(window.scrollY - targetY) < 2) return;

      isProgrammaticScroll = true;
      window.scrollTo(0, targetY);
      requestAnimationFrame(() => {
        window.scrollTo(0, targetY);
        isProgrammaticScroll = false;
        lastUserScrollY = window.scrollY;
      });
    }

    async function refreshStatusPage() {
      if (refreshInFlight) {
        refreshPending = true;
        return;
      }
      refreshInFlight = true;

      const activeFilter = document.querySelector('.components-section .filter-pill.active')?.dataset.filter || "all";
      const activeGlobalRange = document.querySelector('.global-uptime-section .filter-pill.active')?.dataset.range || "24h";
      // Close fullscreen if open to avoid stale references.
      const fullscreenModal = document.getElementById('chartFullscreenModal');
      if (fullscreenModal && !fullscreenModal.classList.contains('hidden')) {
        closeChartFullscreen();
      }

      try {
        const pageUrl = window.location.pathname.replace(/\/$/, "");
        // no-cache (not no-store) so the browser sends If-None-Match and a
        // 304 from the server skips re-downloading an unchanged page.
        const response = await fetch(pageUrl, { cache: "no-cache" });
        if (!response.ok) {
          return;
        }

        const html = await response.text();
        const doc = new DOMParser().parseFromString(html, "text/html");
        const newPageInner = doc.querySelector(".page-inner");
        const currentPageInner = document.querySelector(".page-inner");
        const desiredScrollY = lastUserScrollY;

        if (newPageInner && currentPageInner) {
          currentPageInner.innerHTML = newPageInner.innerHTML;
        }

        if (doc.title) {
          document.title = doc.title;
        }

        setupFilters();
        setupViewToggle();
        setupGlobalUptimeRange();
        setupMonitorUptimeRanges();
        setupCopyStatusLink();
        updateFooterYear();
        updateLastUpdated();

        // Restore UI state without synthetic clicks, which can shift focus/scroll.
        applyComponentFilter(activeFilter);
        applyGlobalUptimeRange(activeGlobalRange);
        renderLatencyCharts();

        // Restore scroll using the most recent user position captured before DOM swap.
        restoreScrollPosition(desiredScrollY);
      } catch (err) {
        console.error("Failed to refresh status page:", err);
      } finally {
        refreshInFlight = false;
        if (refreshPending) {
          refreshPending = false;
          scheduleRefresh();
        }
      }
    }

    function startStatusStream() {
      if (!("EventSource" in window)) return;
      const streamUrl = window.location.pathname.replace(/\/$/, "") + "/stream";
      const source = new EventSource(streamUrl);
      source.addEventListener("update", () => scheduleRefresh());
      source.addEventListener("connected", () => {});
      source.addEventListener("heartbeat", () => {});
      source.onerror = () => {
        // EventSource handles reconnects automatically.
      };
    }

    // Initialize
    const initialRange = document.querySelector('.global-uptime-section .filter-pill.active')?.dataset.range || "24h";
    setupFilters();
    setupViewToggle();
    setupGlobalUptimeRange();
    setupMonitorUptimeRanges();
    applyComponentFilter(document.querySelector('.components-section .filter-pill.active')?.dataset.filter || "all");
    applyGlobalUptimeRange(initialRange);
    renderLatencyCharts();
    setupCopyStatusLink();
    updateFooterYear();
    updateLastUpdated();
    startStatusStream();

    // In-page customizer (opt-in via ?edit=1)
    (function setupStatusPageCustomizer() {
      const body = document.body;
      if (!body) return;

      const slug = body.dataset.statusPageSlug || "";
      const pageId = body.dataset.statusPageId || "";
      if (!slug || !pageId) return;

      const params = new URLSearchParams(window.location.search || "");
      const editParamEnabled = params.get("edit") === "1";
      const editKey = "statuspage:" + slug + ":edit";
      if (editParamEnabled) localStorage.setItem(editKey, "1");

      const editModeEnabled = editParamEnabled || localStorage.getItem(editKey) === "1";
      if (!editModeEnabled) return;

      // Disable auto-refresh while in edit mode (it would wipe unsaved changes)
      window.__SP_DISABLE_REFRESH = true;

      const customizeBtn = document.getElementById("customizeBtn");
      const overlay = document.getElementById("spCustomizeOverlay");
      const closeBtn = document.getElementById("spCustomizeClose");
      const saveBtn = document.getElementById("spCustomizeSave");
      const statusEl = document.getElementById("spCustomizeStatus");
      const titleInput = document.getElementById("spCustomizeTitle");
      const descInput = document.getElementById("spCustomizeDescription");
      const primaryInput = document.getElementById("spCustomizePrimary");
      const secondaryInput = document.getElementById("spCustomizeSecondary");
      const footerTextInput = document.getElementById("spCustomizeFooterText");

      const showMonitorTagsInput = document.getElementById("spSetShowMonitorTags");
      const showMonitorURLInput = document.getElementById("spSetShowMonitorURL");
      const showMonitorUptimeInput = document.getElementById("spSetShowMonitorUptime");
      const showMonitorTLSInput = document.getElementById("spSetShowMonitorTLS");
      const showLatencyChartsInput = document.getElementById("spSetShowLatencyCharts");
      const showAgentMetricsInput = document.getElementById("spSetShowAgentMetrics");
      const showGlobalUptimeInput = document.getElementById("spSetShowGlobalUptime");
      const showFooterInput = document.getElementById("spSetShowFooter");
      const selectedEl = document.getElementById("spCustomizeSelected");
      const monitorListEl = document.getElementById("spCustomizeMonitorList");
      const loadMoreBtn = document.getElementById("spCustomizeLoadMore");

      if (!customizeBtn || !overlay || !closeBtn || !saveBtn || !statusEl || !titleInput || !descInput || !primaryInput || !secondaryInput || !footerTextInput || !showMonitorTagsInput || !showMonitorURLInput || !showMonitorUptimeInput || !showMonitorTLSInput || !showLatencyChartsInput || !showAgentMetricsInput || !showGlobalUptimeInput || !showFooterInput || !selectedEl || !monitorListEl || !loadMoreBtn) {
        return;
      }

      customizeBtn.style.display = "";

      const spTitle = document.getElementById("spTitle");
      const spDescription = document.getElementById("spDescription");
      const cardsContainer = document.getElementById("componentCards");

      const hexColorRe = /^#[0-9A-Fa-f]{6}$/;

      const initial = {
        title: spTitle ? spTitle.textContent.trim() : "",
        description: (body.dataset.statusPageHasDescription === "1" && spDescription) ? spDescription.textContent : "",
        primaryColor: (body.dataset.statusPagePrimaryColor || "").trim(),
        secondaryColor: (body.dataset.statusPageSecondaryColor || "").trim(),
        settings: {
          showMonitorTags: body.dataset.spShowMonitorTags === "1",
          showMonitorURL: body.dataset.spShowMonitorUrl === "1",
          showMonitorUptime: body.dataset.spShowMonitorUptime === "1",
          showMonitorTLS: body.dataset.spShowMonitorTls === "1",
          showLatencyCharts: body.dataset.spShowLatencyCharts === "1",
          showAgentMetrics: body.dataset.spShowAgentMetrics === "1",
          showGlobalUptime: body.dataset.spShowGlobalUptime === "1",
          showFooter: body.dataset.spShowFooter === "1",
          footerText: (body.dataset.spFooterText || "")
        }
      };

      let selectedMonitorIDs = [];
      const displayedNameById = {};
      const monitorNameFromAPI = {};
      const monitorDisplayNames = {};

      function getCurrentCardOrder() {
        const ids = [];
        if (!cardsContainer) return ids;
        const cards = cardsContainer.querySelectorAll(".component-card");
        cards.forEach(card => {
          const id = card.dataset.monitorId;
          if (id) ids.push(id);
          const nameEl = card.querySelector(".component-name-text") || card.querySelector(".component-name");
          if (id && nameEl) {
            displayedNameById[id] = nameEl.textContent.replace(/\s+/g, " ").trim();
          }
        });
        return ids;
      }

      // Initialize selected monitors from the rendered page
      selectedMonitorIDs = getCurrentCardOrder();

      function setStatus(msg) {
        statusEl.textContent = msg || "";
      }

      function openOverlay() {
        overlay.classList.add("open");
        overlay.setAttribute("aria-hidden", "false");
        setStatus("");
        titleInput.value = initial.title;
        descInput.value = initial.description || "";
        primaryInput.value = initial.primaryColor || "";
        secondaryInput.value = initial.secondaryColor || "";
        footerTextInput.value = initial.settings.footerText || "";

        showMonitorTagsInput.checked = !!initial.settings.showMonitorTags;
        showMonitorURLInput.checked = !!initial.settings.showMonitorURL;
        showMonitorUptimeInput.checked = !!initial.settings.showMonitorUptime;
        showMonitorTLSInput.checked = !!initial.settings.showMonitorTLS;
        showLatencyChartsInput.checked = !!initial.settings.showLatencyCharts;
        showAgentMetricsInput.checked = !!initial.settings.showAgentMetrics;
        showGlobalUptimeInput.checked = !!initial.settings.showGlobalUptime;
        showFooterInput.checked = !!initial.settings.showFooter;

        applyVisibilityPreview();
        renderSelectedList();
        ensureMonitorListLoaded();
      }

      function closeOverlay() {
        overlay.classList.remove("open");
        overlay.setAttribute("aria-hidden", "true");
        setStatus("");
      }

      customizeBtn.addEventListener("click", openOverlay);
      closeBtn.addEventListener("click", closeOverlay);

      overlay.addEventListener("click", (e) => {
        if (e.target === overlay) closeOverlay();
      });

      window.addEventListener("keydown", (e) => {
        if (e.key === "Escape" && overlay.classList.contains("open")) {
          closeOverlay();
        }
      });

      function applyTitlePreview() {
        const title = titleInput.value.trim();
        if (spTitle) spTitle.textContent = title || initial.title;
        document.title = (spTitle ? spTitle.textContent : initial.title) + " – Status";
      }

      function applyDescriptionPreview() {
        if (!spDescription) return;
        const val = descInput.value;
        if (val.trim() === "") {
          spDescription.textContent = body.dataset.statusPageHasDescription === "1" ? (initial.description || "") : "System Status";
        } else {
          spDescription.textContent = val;
        }
      }

      function applyColorPreview() {
        const primary = primaryInput.value.trim();
        const secondary = secondaryInput.value.trim();
        if (hexColorRe.test(primary)) document.documentElement.style.setProperty("--brand-primary", primary);
        if (hexColorRe.test(secondary)) document.documentElement.style.setProperty("--brand-secondary", secondary);
      }

      titleInput.addEventListener("input", applyTitlePreview);
      descInput.addEventListener("input", applyDescriptionPreview);
      primaryInput.addEventListener("input", applyColorPreview);
      secondaryInput.addEventListener("input", applyColorPreview);

        function applyVisibilityPreview() {
          const showTags = !!showMonitorTagsInput.checked;
          const showURL = !!showMonitorURLInput.checked;
          const showUptime = !!showMonitorUptimeInput.checked;
          const showTLS = !!showMonitorTLSInput.checked;
          const showLatency = !!showLatencyChartsInput.checked;
          const showAgent = !!showAgentMetricsInput.checked;
          const showGlobal = !!showGlobalUptimeInput.checked;
          const showFooter = !!showFooterInput.checked;

          document.querySelectorAll(".component-tags").forEach(el => { el.style.display = showTags ? "" : "none"; });
          document.querySelectorAll(".component-url").forEach(el => { el.style.display = showURL ? "" : "none"; });
          document.querySelectorAll(".component-uptime").forEach(el => { el.style.display = showUptime ? "" : "none"; });
          document.querySelectorAll(".component-tls").forEach(el => { el.style.display = showTLS ? "" : "none"; });
          document.querySelectorAll(".latency-chart-container, .group-latency-summary").forEach(el => { el.style.display = showLatency ? "" : "none"; });

        document.querySelectorAll('.component-card[data-type="agent"] .agent-metrics, .component-card[data-type="agent"] .component-stats, .component-card[data-type="push"] .agent-metrics, .component-card[data-type="push"] .component-stats').forEach(el => {
          el.style.display = showAgent ? "" : "none";
        });

        document.querySelectorAll(".uptime-section").forEach(el => { el.style.display = showGlobal ? "" : "none"; });
        document.querySelectorAll("footer.footer").forEach(el => { el.style.display = showFooter ? "" : "none"; });
      }

        [showMonitorTagsInput, showMonitorURLInput, showMonitorUptimeInput, showMonitorTLSInput, showLatencyChartsInput, showAgentMetricsInput, showGlobalUptimeInput, showFooterInput].forEach(el => {
          el.addEventListener("change", applyVisibilityPreview);
        });

      function applyCardOrder() {
        if (!cardsContainer) return;
        const cardsById = {};
        cardsContainer.querySelectorAll(".component-card").forEach(card => {
          const id = card.dataset.monitorId;
          if (id) cardsById[id] = card;
        });
        selectedMonitorIDs.forEach(id => {
          const card = cardsById[id];
          if (card) cardsContainer.appendChild(card);
        });
      }

      function setCardVisibility(id, visible) {
        if (!cardsContainer) return;
        const card = cardsContainer.querySelector('.component-card[data-monitor-id="' + id + '"]');
        if (!card) return;
        card.style.display = visible ? "" : "none";
      }

      function effectiveDisplayName(id) {
        const override = (monitorDisplayNames[id] || "").trim();
        if (override) return override;
        const apiName = (monitorNameFromAPI[id] || "").trim();
        if (apiName) return apiName;
        const displayed = (displayedNameById[id] || "").trim();
        if (displayed) return displayed;
        return id;
      }

      function updateCardTitle(id) {
        if (!cardsContainer) return;
        const card = cardsContainer.querySelector('.component-card[data-monitor-id="' + id + '"]');
        if (!card) return;

        const newName = effectiveDisplayName(id);
        const nameTextEl = card.querySelector(".component-name-text");
        if (nameTextEl) {
          nameTextEl.textContent = newName;
        }

        const chartContainer = card.querySelector(".latency-chart-container");
        if (chartContainer) {
          chartContainer.dataset.monitorName = newName;
        }
      }

      function renderSelectedList() {
        selectedEl.innerHTML = "";

        selectedMonitorIDs.forEach(id => {
          const row = document.createElement("div");
          row.className = "customize-item";
          row.draggable = true;
          row.dataset.monitorId = id;

          const handle = document.createElement("span");
          handle.className = "customize-handle";
          row.appendChild(handle);

          const main = document.createElement("div");
          main.className = "customize-item-main";

          const title = document.createElement("div");
          title.className = "customize-item-name";
          title.textContent = effectiveDisplayName(id).replace(/\s+/g, " ").trim();
          main.appendChild(title);

          const input = document.createElement("input");
          input.className = "customize-item-input";
          input.type = "text";
          input.placeholder = "Custom display title (optional)";
          input.value = (monitorDisplayNames[id] || "");
          input.addEventListener("input", () => {
            const v = input.value.trim();
            if (v === "") {
              delete monitorDisplayNames[id];
            } else {
              monitorDisplayNames[id] = v;
            }
            updateCardTitle(id);
          });
          main.appendChild(input);

          row.appendChild(main);

          const card = cardsContainer ? cardsContainer.querySelector('.component-card[data-monitor-id="' + id + '"]') : null;
          if (!card) {
            const badge = document.createElement("span");
            badge.className = "customize-item-badge";
            badge.textContent = "new";
            row.appendChild(badge);
          }

          selectedEl.appendChild(row);
        });

        let dragId = null;
        selectedEl.querySelectorAll(".customize-item").forEach(item => {
          item.addEventListener("dragstart", (e) => {
            dragId = item.dataset.monitorId || null;
            item.classList.add("dragging");
            try { e.dataTransfer.effectAllowed = "move"; } catch (_) {}
          });
          item.addEventListener("dragend", () => {
            item.classList.remove("dragging");
          });
          item.addEventListener("dragover", (e) => {
            e.preventDefault();
            if (!dragId) return;
            const overId = item.dataset.monitorId;
            if (!overId || overId === dragId) return;
            const from = selectedMonitorIDs.indexOf(dragId);
            const to = selectedMonitorIDs.indexOf(overId);
            if (from === -1 || to === -1) return;
            selectedMonitorIDs.splice(from, 1);
            selectedMonitorIDs.splice(to, 0, dragId);
            renderSelectedList();
            applyCardOrder();
            syncCheckboxes();
          });
        });
      }

      function syncDisplayNameInputs() {
        selectedEl.querySelectorAll(".customize-item").forEach(item => {
          const id = item.dataset.monitorId;
          const input = item.querySelector(".customize-item-input");
          if (!id || !input) return;
          if (String(input.value || "").trim() === "" && String(monitorDisplayNames[id] || "").trim() !== "") {
            input.value = monitorDisplayNames[id];
          }
        });
      }

      // Monitor list (from API)
      let monitorPage = 1;
      let monitorTotal = null;
      let monitorLoaded = 0;
      const checkboxByMonitorId = {};

      function recomputeDisplayNameOverrides() {
        selectedMonitorIDs.forEach(id => {
          if (monitorDisplayNames[id] !== undefined) return;

          const displayed = (displayedNameById[id] || "").trim();
          const apiName = (monitorNameFromAPI[id] || "").trim();

          if (!apiName) {
            if (displayed) monitorDisplayNames[id] = displayed;
            return;
          }

          if (displayed && displayed !== apiName) {
            monitorDisplayNames[id] = displayed;
          }
        });
      }

      function upsertMonitorCheckbox(monitor) {
        const id = monitor.id;
        if (!id || checkboxByMonitorId[id]) return;

        if (monitor && monitor.name) {
          monitorNameFromAPI[id] = String(monitor.name);
        }

        const row = document.createElement("div");
        row.className = "customize-monitor-row";

        const cb = document.createElement("input");
        cb.type = "checkbox";
        cb.checked = selectedMonitorIDs.includes(id);
        cb.addEventListener("change", () => {
          if (cb.checked) {
            if (!selectedMonitorIDs.includes(id)) selectedMonitorIDs.push(id);
            setCardVisibility(id, true);
          } else {
            selectedMonitorIDs = selectedMonitorIDs.filter(x => x !== id);
            setCardVisibility(id, false);
            delete monitorDisplayNames[id];
          }

          recomputeDisplayNameOverrides();
          renderSelectedList();
          applyCardOrder();
        });

        const label = document.createElement("div");
        label.style.flex = "1";
        label.style.minWidth = "0";
        label.textContent = (monitor.name || id);

        const badge = document.createElement("span");
        badge.className = "customize-item-badge";
        badge.textContent = monitor.type || "monitor";

        row.appendChild(cb);
        row.appendChild(label);
        row.appendChild(badge);
        monitorListEl.appendChild(row);

        checkboxByMonitorId[id] = cb;
      }

      function apiProxyURL(path) {
        return "/_sp_api" + path;
      }

      async function apiFetch(path, options) {
        const opt = Object.assign({}, options || {});
        opt.headers = Object.assign({}, (options && options.headers) ? options.headers : {});
        opt.headers["X-Status-Page-ID"] = pageId;

        let res = await fetch(apiProxyURL(path), opt);
        // If the proxy isn't configured (or we're deployed with the API on the same origin),
        // fall back to calling the API directly.
        if (res.status === 404) {
          res = await fetch(path, opt);
        }
        return res;
      }

      async function fetchMonitorsPage(pageNum) {
        const path = "/api/v1/monitors?page=" + pageNum + "&page_size=100";
        const res = await apiFetch(path, { credentials: "include" });
        if (res.status === 401) {
          setStatus("Not logged in. Open the app, log in, then refresh this page.");
          return { items: [], total: 0 };
        }
        if (!res.ok) {
          setStatus("Failed to load monitors (" + res.status + ").");
          return { items: [], total: 0 };
        }
        return await res.json();
      }

      async function loadMoreMonitors() {
        setStatus("Loading monitors…");
        const data = await fetchMonitorsPage(monitorPage);
        if (!data || !Array.isArray(data.items)) {
          setStatus("");
          return;
        }
        monitorTotal = typeof data.total === "number" ? data.total : monitorTotal;
        data.items.forEach(m => upsertMonitorCheckbox(m));
        recomputeDisplayNameOverrides();
        syncDisplayNameInputs();
        monitorLoaded += data.items.length;
        monitorPage += 1;

        if (monitorTotal !== null && monitorLoaded >= monitorTotal) {
          loadMoreBtn.disabled = true;
          loadMoreBtn.textContent = "All loaded";
        }

        setStatus("");
      }

      async function ensureMonitorListLoaded() {
        if (monitorLoaded > 0) return;
        await loadMoreMonitors();
      }

      loadMoreBtn.addEventListener("click", (e) => {
        e.preventDefault();
        loadMoreMonitors();
      });

      function syncCheckboxes() {
        Object.keys(checkboxByMonitorId).forEach(id => {
          checkboxByMonitorId[id].checked = selectedMonitorIDs.includes(id);
        });
      }

      async function save() {
        setStatus("Saving…");

        const payload = {};
        const newTitle = titleInput.value.trim();
        if (newTitle && newTitle !== initial.title) payload.title = newTitle;

        const newDesc = descInput.value.trim();
        if (newDesc && newDesc !== (initial.description || "")) payload.description = newDesc;

        const newPrimary = primaryInput.value.trim();
        if (newPrimary && hexColorRe.test(newPrimary) && newPrimary !== (initial.primaryColor || "")) payload.primary_color = newPrimary;

        const newSecondary = secondaryInput.value.trim();
        if (newSecondary && hexColorRe.test(newSecondary) && newSecondary !== (initial.secondaryColor || "")) payload.secondary_color = newSecondary;

        payload.monitor_ids = selectedMonitorIDs.slice();

        const names = {};
        Object.keys(monitorDisplayNames).forEach(id => {
          const v = String(monitorDisplayNames[id] || "").trim();
          if (v) names[id] = v;
        });
        payload.monitor_display_names = names;

          payload.settings = {
            show_monitor_tags: !!showMonitorTagsInput.checked,
            show_monitor_url: !!showMonitorURLInput.checked,
            show_monitor_uptime: !!showMonitorUptimeInput.checked,
            show_monitor_tls: !!showMonitorTLSInput.checked,
            show_latency_charts: !!showLatencyChartsInput.checked,
            show_agent_metrics: !!showAgentMetricsInput.checked,
            show_global_uptime: !!showGlobalUptimeInput.checked,
            show_footer: !!showFooterInput.checked,
            footer_text: String(footerTextInput.value || "")
          };

        try {
          const res = await apiFetch("/api/v1/status-pages/" + pageId, {
            method: "PATCH",
            credentials: "include",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(payload)
          });

          if (res.status === 401) {
            setStatus("Not logged in. Open the app, log in, then retry.");
            return;
          }

          if (!res.ok) {
            const text = await res.text();
            setStatus("Save failed (" + res.status + "): " + (text || "error"));
            return;
          }

          setStatus("Saved. Reloading…");
          window.location.reload();
        } catch (e) {
          setStatus("Save failed: " + (e && e.message ? e.message : "network error"));
        }
      }

      saveBtn.addEventListener("click", (e) => {
        e.preventDefault();
        save();
      });

      // Optional: allow dragging cards directly to reorder
      if (cardsContainer) {
        let dragCardId = null;
        cardsContainer.querySelectorAll(".component-card").forEach(card => {
          card.draggable = true;
          card.addEventListener("dragstart", (e) => {
            dragCardId = card.dataset.monitorId || null;
            try { e.dataTransfer.effectAllowed = "move"; } catch (_) {}
          });
          card.addEventListener("dragover", (e) => {
            e.preventDefault();
          });
          card.addEventListener("drop", (e) => {
            e.preventDefault();
            const overId = card.dataset.monitorId || null;
            if (!dragCardId || !overId || dragCardId === overId) return;
            const from = selectedMonitorIDs.indexOf(dragCardId);
            const to = selectedMonitorIDs.indexOf(overId);
            if (from === -1 || to === -1) return;
            selectedMonitorIDs.splice(from, 1);
            selectedMonitorIDs.splice(to, 0, dragCardId);
            applyCardOrder();
            renderSelectedList();
            syncCheckboxes();
          });
        });
      }
    })();

    // Auto-refresh without hard reload so scroll position is preserved.
    if (!window.__SP_DISABLE_REFRESH) {
      setInterval(() => {
        if (document.visibilityState !== "visible") return;
        scheduleRefresh();
      }, 60000);
    }
  </script>
</body>
</html>`
