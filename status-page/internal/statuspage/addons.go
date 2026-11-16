package statuspage

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// Page add-ons are read-only sub-routes beside a page: its summary contract,
// SVG badges and Atom feed. They are dispatched from HandleStatusPage, never
// registered on the mux, so an access check there covers them like every
// other sub-route. The slug is always the first path segment.
//
//	GET /public/status/{slug}/summary.json
//	GET /public/status/{slug}/feed.atom
//	GET /public/status/{slug}/badge.svg[?style=flat|square]
//	GET /public/status/{slug}/monitors/{id}/badge.svg[?style=…]
//	GET /public/status/{slug}/monitors/{id}/uptime.svg[?range=…&style=…]
const (
	routeSummary     = "summary.json"
	routeFeed        = "feed.atom"
	routeBadge       = "badge.svg"
	routeUptimeBadge = "uptime.svg"
)

// pageAddonRoutes lists every add-on route shape, for route tables and the
// test that walks them.
var pageAddonRoutes = []string{
	"{slug}/" + routeSummary,
	"{slug}/" + routeFeed,
	"{slug}/" + routeBadge,
	"{slug}/monitors/{id}/" + routeBadge,
	"{slug}/monitors/{id}/" + routeUptimeBadge,
}

// Shared-cache lifetimes. The page itself is no-cache (it revalidates by
// ETag); these are polled by READMEs, dashboards and feed readers, so a
// short public max-age lets a proxy absorb them.
const (
	addonDataMaxAge = 60
	addonFeedMaxAge = 300
)

// Response policies. None of these documents runs script or loads anything;
// the sandbox keeps it that way if one is opened as a document.
const (
	addonDocumentCSP = "default-src 'none'; frame-ancestors 'none'; sandbox"
	addonImageCSP    = "default-src 'none'; sandbox"
)

// pageAddon is a parsed add-on path.
type pageAddon struct {
	slug      string
	route     string
	monitorID string // monitor badges only, canonical UUID form
}

// parsePageAddon recognizes an add-on path (the part after /public/status/).
// A monitor ID that is not a UUID is not a route; it falls through to the
// page lookup and 404s like any unknown path.
func parsePageAddon(path string) (pageAddon, bool) {
	parts := strings.Split(path, "/")
	switch len(parts) {
	case 2:
		switch parts[1] {
		case routeSummary, routeFeed, routeBadge:
			if parts[0] != "" {
				return pageAddon{slug: parts[0], route: parts[1]}, true
			}
		}
	case 4:
		if parts[0] == "" || parts[1] != "monitors" || (parts[3] != routeBadge && parts[3] != routeUptimeBadge) {
			return pageAddon{}, false
		}
		id, err := uuid.Parse(parts[2])
		if err != nil {
			return pageAddon{}, false
		}
		return pageAddon{slug: parts[0], route: parts[3], monitorID: id.String()}, true
	}
	return pageAddon{}, false
}

// servePageAddon serves path when it names a page add-on and reports whether
// it did.
func (h *Handlers) servePageAddon(w http.ResponseWriter, r *http.Request, path string) bool {
	addon, ok := parsePageAddon(path)
	if !ok {
		return false
	}
	switch {
	case addon.route == routeSummary:
		h.handleSummary(w, r, addon.slug)
	case addon.route == routeFeed:
		h.handleFeed(w, r, addon.slug)
	default:
		h.handleBadge(w, r, addon)
	}
	return true
}

func (h *Handlers) handleSummary(w http.ResponseWriter, r *http.Request, slug string) {
	page, _, err := h.cachedPage(r.Context(), slug)
	if err != nil {
		h.writeStatusPageError(w, slug, err, "Failed to build status page summary")
		return
	}
	body, err := json.Marshal(page.summary)
	if err != nil {
		h.logger.WithError(err).Error("Failed to encode status page summary")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	writeAddon(w, r, addonResponse{
		contentType: "application/json; charset=utf-8",
		body:        append(body, '\n'),
		maxAge:      addonDataMaxAge,
		csp:         addonDocumentCSP,
	})
}

// handleBadge serves the page status badge, or a status or uptime badge of
// one component. Only components the page renders have one: any other ID
// is a 404, never a lookup of an arbitrary monitor.
func (h *Handlers) handleBadge(w http.ResponseWriter, r *http.Request, addon pageAddon) {
	query := r.URL.Query()
	style := query.Get("style")
	if style == "" {
		style = badgeStyleFlat
	}
	if style != badgeStyleFlat && style != badgeStyleSquare {
		http.Error(w, "style must be flat or square", http.StatusBadRequest)
		return
	}
	rangeName := defaultBadgeRange
	if addon.route == routeUptimeBadge {
		if v := query.Get("range"); v != "" {
			rangeName = v
		}
		if !badgeRanges[rangeName] {
			http.Error(w, "range must be one of 1h, 24h, 7d, 30d, 90d", http.StatusBadRequest)
			return
		}
	}

	page, _, err := h.cachedPage(r.Context(), addon.slug)
	if err != nil {
		h.writeStatusPageError(w, addon.slug, err, "Failed to build status page badge")
		return
	}
	summary := page.summary

	var label, value, color string
	switch {
	case addon.monitorID == "":
		label = "status"
		value, color = pageStatusBadge(summary.Status)
	default:
		component, ok := summary.component(addon.monitorID)
		if !ok {
			http.Error(w, "Component not found on this status page", http.StatusNotFound)
			return
		}
		label = component.Name
		if addon.route == routeUptimeBadge {
			// The summary nulls a hidden figure; a badge must not read that
			// as "no data" and publish a badge the page would not show.
			if summary.componentUptimeHidden {
				http.Error(w, "Uptime is not shown on this status page", http.StatusNotFound)
				return
			}
			label = component.Name + " " + rangeName
			value, color = uptimeBadge(component.Uptime.forRange(rangeName))
		} else {
			value, color = componentStatusBadge(component.Status)
		}
	}

	writeAddon(w, r, addonResponse{
		contentType: "image/svg+xml; charset=utf-8",
		body:        renderBadge(label, value, color, style),
		maxAge:      addonDataMaxAge,
		csp:         addonImageCSP,
	})
}

func (h *Handlers) handleFeed(w http.ResponseWriter, r *http.Request, slug string) {
	ctx, cancel := context.WithTimeout(r.Context(), statusPageBuildTimeout)
	defer cancel()
	feed, err := h.service.GetStatusPageFeed(ctx, slug, h.canonicalURL(slug))
	if err != nil {
		h.writeStatusPageError(w, slug, err, "Failed to build status page feed")
		return
	}
	body, err := renderAtomFeed(feed)
	if err != nil {
		h.logger.WithError(err).Error("Failed to encode status page feed")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	writeAddon(w, r, addonResponse{
		contentType: "application/atom+xml; charset=utf-8",
		body:        body,
		maxAge:      addonFeedMaxAge,
		csp:         addonDocumentCSP,
	})
}

type addonResponse struct {
	contentType string
	body        []byte
	maxAge      int
	csp         string
}

// writeAddon writes an add-on response with an ETag (a 304 for a matching
// If-None-Match) and a short public Cache-Control. A Cache-Control set
// before dispatch wins, so an access check that serves a non-public page can
// mark the response private and no shared cache keeps it.
//
// Every add-on is readable from any origin without credentials: it is the
// page's public data, and a browser never attaches cookies to a request
// answered with Access-Control-Allow-Origin "*". X-Robots-Tag keeps these
// machine documents out of search results whatever the page's own setting.
func writeAddon(w http.ResponseWriter, r *http.Request, resp addonResponse) {
	header := w.Header()
	etag := computeETag(string(resp.body))
	header.Set("ETag", etag)
	if header.Get("Cache-Control") == "" {
		header.Set("Cache-Control", "public, max-age="+strconv.Itoa(resp.maxAge))
	}
	header.Set("Access-Control-Allow-Origin", "*")
	header.Set("Cross-Origin-Resource-Policy", "cross-origin")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Robots-Tag", "noindex")
	header.Set("Content-Security-Policy", resp.csp)
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	header.Set("Content-Type", resp.contentType)
	header.Set("Content-Length", strconv.Itoa(len(resp.body)))
	_, _ = w.Write(resp.body)
}
