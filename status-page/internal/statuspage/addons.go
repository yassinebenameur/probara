package statuspage

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Page add-ons are the read-only sub-routes beside a page: its summary
// contract, SVG badges, the embeddable widget script and the Atom feed. They
// are dispatched from HandleStatusPage (never registered on the mux), so a
// gate there covers them like every other sub-route.
//
//	GET /public/status/{slug}/summary.json
//	GET /public/status/{slug}/badge.svg[?metric=status|uptime&range=30d]
//	GET /public/status/{slug}/monitors/{id}/badge.svg[?metric=…&range=…]
//	GET /public/status/{slug}/embed.js
//	GET /public/status/{slug}/feed.atom   (feed.rss redirects here)
//
// pageAddonSuffixes lists the dispatched suffixes, for route tables and
// tests; the monitor badge is the /badge.svg suffix with a /monitors/{id}
// segment before it.
var pageAddonSuffixes = []string{"/summary.json", "/badge.svg", "/embed.js", "/feed.atom", "/feed.rss"}

// Shared-cache lifetimes. The page itself is no-cache (it revalidates by
// ETag); these are polled by READMEs, dashboards and feed readers, so a
// short public max-age lets a CDN absorb them.
const (
	addonDataMaxAge   = 60
	addonFeedMaxAge   = 300
	addonScriptMaxAge = 3600
)

//go:embed embed.js
var embedScript []byte

// servePageAddon serves path when it names a page add-on and reports whether
// it did.
func (h *Handlers) servePageAddon(w http.ResponseWriter, r *http.Request, path string) bool {
	switch {
	case strings.HasSuffix(path, "/summary.json"):
		h.handleSummary(w, r, strings.TrimSuffix(path, "/summary.json"))
	case strings.HasSuffix(path, "/badge.svg"):
		rest := strings.TrimSuffix(path, "/badge.svg")
		if slug, monitorID, ok := strings.Cut(rest, "/monitors/"); ok {
			h.handleBadge(w, r, slug, monitorID)
		} else {
			h.handleBadge(w, r, rest, "")
		}
	case strings.HasSuffix(path, "/embed.js"):
		h.handleEmbedScript(w, r, strings.TrimSuffix(path, "/embed.js"))
	case strings.HasSuffix(path, "/feed.atom"):
		h.handleFeed(w, r, strings.TrimSuffix(path, "/feed.atom"))
	case strings.HasSuffix(path, "/feed.rss"):
		// One feed format; the RSS spelling some readers guess is a
		// permanent, relative redirect so it also works under a path prefix
		// or another host.
		if !validAddonSlug(strings.TrimSuffix(path, "/feed.rss")) {
			http.Error(w, "Status page not found", http.StatusNotFound)
			return true
		}
		http.Redirect(w, r, "feed.atom", http.StatusMovedPermanently)
	default:
		return false
	}
	return true
}

// validAddonSlug rejects an empty slug or one with a path separator, which
// no page has; it saves a lookup for a mistyped URL.
func validAddonSlug(slug string) bool {
	return slug != "" && !strings.Contains(slug, "/")
}

// summaryFor returns the cached summary of slug, building the page on a miss.
func (h *Handlers) summaryFor(ctx context.Context, slug string) (*pageSummary, error) {
	page, _, err := h.cachedPage(ctx, slug)
	if err != nil {
		return nil, err
	}
	if page.summary == nil {
		return nil, ErrStatusPageNotFound
	}
	return page.summary, nil
}

func (h *Handlers) handleSummary(w http.ResponseWriter, r *http.Request, slug string) {
	if !validAddonSlug(slug) {
		http.Error(w, "Status page not found", http.StatusNotFound)
		return
	}
	summary, err := h.summaryFor(r.Context(), slug)
	if err != nil {
		h.writeStatusPageError(w, slug, err, "Failed to build status page summary")
		return
	}
	body, err := json.Marshal(summary)
	if err != nil {
		h.logger.WithError(err).Error("Failed to encode status page summary")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	writeAddon(w, r, addonResponse{
		contentType: "application/json; charset=utf-8",
		body:        body,
		maxAge:      addonDataMaxAge,
		crossOrigin: true,
		csp:         "default-src 'none'; frame-ancestors 'none'",
	})
}

// handleBadge serves the page badge (monitorID "") or a component badge.
// Only components the page renders have one: an ID the page does not show
// is a 404, never a lookup of an arbitrary monitor.
func (h *Handlers) handleBadge(w http.ResponseWriter, r *http.Request, slug, monitorID string) {
	if !validAddonSlug(slug) || strings.Contains(monitorID, "/") {
		http.Error(w, "Status page not found", http.StatusNotFound)
		return
	}
	query := r.URL.Query()
	metric := query.Get("metric")
	if metric == "" {
		metric = "status"
	}
	if metric != "status" && metric != "uptime" {
		http.Error(w, "metric must be status or uptime", http.StatusBadRequest)
		return
	}
	rangeName := query.Get("range")
	if rangeName == "" {
		rangeName = defaultBadgeRange
	}
	if !badgeRanges[rangeName] {
		http.Error(w, "range must be one of 1h, 24h, 7d, 30d, 90d", http.StatusBadRequest)
		return
	}

	summary, err := h.summaryFor(r.Context(), slug)
	if err != nil {
		h.writeStatusPageError(w, slug, err, "Failed to build status page badge")
		return
	}

	// An uptime badge exists only where the page shows uptime: the summary
	// nulls a hidden figure, and a badge must not read that as "no data".
	if metric == "uptime" && ((monitorID == "" && summary.pageUptimeHidden) || (monitorID != "" && summary.componentUptimeHidden)) {
		http.Error(w, "Uptime is not shown on this status page", http.StatusNotFound)
		return
	}

	var label, value, color string
	if monitorID == "" {
		label = "status"
		if metric == "uptime" {
			label = "uptime " + rangeName
			uptime, _ := summary.Uptime.forRange(rangeName)
			value, color = uptimeBadge(uptime)
		} else {
			value, color = pageStatusBadge(summary.Status.Counts)
		}
	} else {
		component, ok := summary.component(monitorID)
		if !ok {
			http.Error(w, "Component not found", http.StatusNotFound)
			return
		}
		label = component.Name
		if metric == "uptime" {
			label = component.Name + " " + rangeName
			uptime, _ := component.Uptime.forRange(rangeName)
			value, color = uptimeBadge(uptime)
		} else {
			value, color = componentStatusBadge(component.Status)
		}
	}

	writeAddon(w, r, addonResponse{
		contentType: "image/svg+xml; charset=utf-8",
		body:        renderBadge(label, value, color),
		maxAge:      addonDataMaxAge,
		crossOrigin: true,
		// A badge opened directly renders as a document; it has no script
		// and must not gain any.
		csp: "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors *",
	})
}

// handleEmbedScript serves the widget script. It is the same for every page
// (it derives the summary URL from its own src), so it needs no lookup; an
// unknown slug surfaces when the widget's summary request 404s.
func (h *Handlers) handleEmbedScript(w http.ResponseWriter, r *http.Request, slug string) {
	if !validAddonSlug(slug) {
		http.Error(w, "Status page not found", http.StatusNotFound)
		return
	}
	writeAddon(w, r, addonResponse{
		contentType: "text/javascript; charset=utf-8",
		body:        embedScript,
		maxAge:      addonScriptMaxAge,
		crossOrigin: true,
	})
}

func (h *Handlers) handleFeed(w http.ResponseWriter, r *http.Request, slug string) {
	if !validAddonSlug(slug) {
		http.Error(w, "Status page not found", http.StatusNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), statusPageBuildTimeout)
	defer cancel()
	feed, err := h.service.GetStatusPageFeed(ctx, slug)
	if err != nil {
		h.writeStatusPageError(w, slug, err, "Failed to build status page feed")
		return
	}
	body, err := renderAtomFeed(feed, h.canonicalURL(slug), time.Now())
	if err != nil {
		h.logger.WithError(err).Error("Failed to encode status page feed")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	writeAddon(w, r, addonResponse{
		contentType: "application/atom+xml; charset=utf-8",
		body:        body,
		maxAge:      addonFeedMaxAge,
		crossOrigin: true,
		csp:         "default-src 'none'; frame-ancestors 'none'",
	})
}

type addonResponse struct {
	contentType string
	body        []byte
	maxAge      int
	// crossOrigin lets other sites read the response (embed.js on a
	// customer's site fetching summary.json, a dashboard pulling a badge).
	// The data is the page's public data; no credentials are allowed, so a
	// cookie-gated page is never readable cross-origin.
	crossOrigin bool
	csp         string
}

// writeAddon writes an add-on response with an ETag (and a 304 for a
// matching If-None-Match) and a public Cache-Control. A Cache-Control set
// before dispatch wins: a gate that serves a non-public page marks it
// private, no-store, and a shared cache must never keep that response.
func writeAddon(w http.ResponseWriter, r *http.Request, resp addonResponse) {
	header := w.Header()
	etag := computeETag(string(resp.body))
	header.Set("ETag", etag)
	if header.Get("Cache-Control") == "" {
		header.Set("Cache-Control", "public, max-age="+strconv.Itoa(resp.maxAge))
	}
	if resp.crossOrigin {
		header.Set("Access-Control-Allow-Origin", "*")
		header.Set("Cross-Origin-Resource-Policy", "cross-origin")
	}
	if resp.csp != "" {
		header.Set("Content-Security-Policy", resp.csp)
	}
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	header.Set("Content-Type", resp.contentType)
	_, _ = w.Write(resp.body)
}
