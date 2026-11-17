package statuspage

import (
	"context"
	"fmt"
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
//
// Access: every add-on reads the page through the slug-keyed render cache
// (summary and badges via cachedPage, the feed via feedCacheKey), so any
// access check must run before this call. A page that is not public is
// served by passing r through gatedPageRequest first: writeAddon then sends
// Cache-Control "private, no-store", no Access-Control-Allow-Origin and
// Cross-Origin-Resource-Policy "same-origin", so neither a shared cache nor
// another site can read the response. Short of that, Cache-Control,
// Access-Control-Allow-Origin and Cross-Origin-Resource-Policy values set
// on w before the call are kept as they are.
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
	writeAddon(w, r, addonResponse{
		contentType: "application/json; charset=utf-8",
		body:        page.summaryJSON,
		etag:        page.summaryETag,
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
			label = uptimeBadgeLabel(component.Name, rangeName)
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

// handleFeed serves the Atom feed from the render cache: one build per page
// per TTL however many readers poll, invalidated with the page. The feed
// depends on the slug alone, so query strings are ignored and cannot force
// a rebuild.
func (h *Handlers) handleFeed(w http.ResponseWriter, r *http.Request, slug string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), statusPageBuildTimeout)
	defer cancel()
	page, etag, err := h.cache.GetPage(feedCacheKey(slug), func() (renderedPage, error) {
		feed, err := h.service.GetStatusPageFeed(ctx, slug, h.canonicalURL(slug))
		if err != nil {
			return renderedPage{}, err
		}
		body, err := renderAtomFeed(feed)
		if err != nil {
			return renderedPage{}, fmt.Errorf("encode feed: %w", err)
		}
		return renderedPage{html: string(body)}, nil
	})
	if err != nil {
		h.writeStatusPageError(w, slug, err, "Failed to build status page feed")
		return
	}
	writeAddon(w, r, addonResponse{
		contentType: "application/atom+xml; charset=utf-8",
		body:        []byte(page.html),
		etag:        etag,
		maxAge:      addonFeedMaxAge,
		csp:         addonDocumentCSP,
	})
}

type addonResponse struct {
	contentType string
	body        []byte
	// etag is the validator; computed from body when empty.
	etag   string
	maxAge int
	csp    string
}

type gatedPageKey struct{}

// gatedPageRequest marks r as serving a page that is not public (password,
// private or token-gated): writeAddon then makes the response private and
// unreadable cross-origin. It is the one call an access check makes before
// servePageAddon for such a page.
func gatedPageRequest(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), gatedPageKey{}, true))
}

func isGatedPage(r *http.Request) bool {
	gated, _ := r.Context().Value(gatedPageKey{}).(bool)
	return gated
}

// setIfAbsent sets key unless a value was set before dispatch.
func setIfAbsent(header http.Header, key, value string) {
	if len(header.Values(key)) == 0 {
		header.Set(key, value)
	}
}

// writeAddon writes an add-on response with an ETag (a 304 for a matching
// If-None-Match), a short public Cache-Control, and cross-origin access.
//
// A public page's add-ons are readable from any origin without credentials:
// they are the page's public data, and a browser never attaches cookies to a
// request answered with Access-Control-Allow-Origin "*". A gated page
// (gatedPageRequest) gets none of that; see servePageAddon. X-Robots-Tag
// keeps these machine documents out of search results whatever the page's
// own setting.
func writeAddon(w http.ResponseWriter, r *http.Request, resp addonResponse) {
	header := w.Header()
	etag := resp.etag
	if etag == "" {
		etag = computeETag(string(resp.body))
	}
	header.Set("ETag", etag)
	if isGatedPage(r) {
		header.Set("Cache-Control", "private, no-store")
		header.Del("Access-Control-Allow-Origin")
		header.Set("Cross-Origin-Resource-Policy", "same-origin")
	} else {
		setIfAbsent(header, "Cache-Control", "public, max-age="+strconv.Itoa(resp.maxAge))
		setIfAbsent(header, "Access-Control-Allow-Origin", "*")
		setIfAbsent(header, "Cross-Origin-Resource-Policy", "cross-origin")
	}
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
