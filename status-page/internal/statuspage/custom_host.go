package statuspage

import (
	"context"
	"net/http"
	"net/netip"
	"path"
	"strings"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/pagedomains"
)

// Serving a page on its custom domain, and nothing else on any other host.
//
// CustomHosts runs in front of every route and sorts each request by its
// host (requestHost):
//
//   - internal: an IP address, a single label or a name no custom domain can
//     have (pagedomains.CanBeCustomDomain): kubelet probes, metrics scrapes
//     and in-cluster calls. The platform routes, without a table lookup, so
//     a database stall can never fail a liveness probe.
//   - a platform host: STATUS_PAGE_BASE_URL's host and
//     STATUS_PAGE_PLATFORM_HOSTS. The platform routes: every page under
//     /public/status/, health and metrics.
//   - a verified custom domain (hostTable): rewritten onto its page's slug
//     paths, so the visibility gate, the route table, the caches and every
//     handler apply unchanged:
//
//     /         → /public/status/{slug}/
//     /{rest}   → /public/status/{slug}/{rest}   (data, stream, push, sw.js, unlock, add-ons, …)
//
//   - anything else (a pending, failed, superseded or removed domain still
//     pointed at the service, an unknown host, or any host before the table
//     has loaded once): the not-found answer, never the platform routes. A
//     custom-domain Ingress sends all of a host to this service under that
//     host's certificate, so falling through would publish every page.
//
// With no public platform host configured (no STATUS_PAGE_BASE_URL host that
// could be a public name, and no STATUS_PAGE_PLATFORM_HOSTS), the service
// cannot tell its own hosts from others: every host but a verified domain
// gets the platform routes, as before custom domains. That is the
// development setup; Helm always configures platform hosts.
//
// Everything on a custom domain is a sub-route of that one page and passes
// its gate: other slugs, /metrics and the health routes are unknown
// sub-routes there. The rewritten request carries
// the custom host (customHostFrom), which makes the session cookie host-only
// at path / and always Secure, the unlock form post to /unlock, redirects go
// to the visitor's own URL, the service worker scope the root, and push
// subscriptions remember their origin. A custom domain is its own origin and
// holds a single page, so the cross-page refusal that protects gated pages
// sharing the platform origin (crossPageRequest) has nothing to guard there.

// customHost is the custom domain a request arrived on.
type customHost struct {
	Host   string
	PageID uuid.UUID
	Slug   string
	// Path is the path the visitor asked for, before the rewrite.
	Path string
}

type customHostKey struct{}

func customHostFrom(r *http.Request) (customHost, bool) {
	ch, ok := r.Context().Value(customHostKey{}).(customHost)
	return ch, ok
}

// CustomHosts wraps next (the platform routes) with the host sorting above.
func (h *Handlers) CustomHosts(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := h.requestHost(r)
		if !pagedomains.CanBeCustomDomain(host) || h.isPlatformHost(host) {
			next.ServeHTTP(w, r)
			return
		}
		route, _, _ := h.hosts.Lookup(r.Context(), host)
		if !route.Verified {
			if !h.platformHostsConfigured() {
				next.ServeHTTP(w, r)
				return
			}
			writeNotFound(w)
			return
		}
		visitorPath := r.URL.Path
		if visitorPath == "" {
			visitorPath = "/"
		}
		if !cleanRequestPath(visitorPath) {
			writeNotFound(w)
			return
		}
		ctx := context.WithValue(r.Context(), customHostKey{}, customHost{Host: host, PageID: route.PageID, Slug: route.Slug, Path: visitorPath})
		rewritten := r.WithContext(ctx)
		u := *r.URL
		u.Path = "/public/status/" + route.Slug + visitorPath
		u.RawPath = ""
		rewritten.URL = &u
		next.ServeHTTP(w, rewritten)
	})
}

// cleanRequestPath refuses dot segments and empty segments: rewritten under
// the slug, the mux would redirect them to a cleaned path outside it.
func cleanRequestPath(p string) bool {
	if !strings.HasPrefix(p, "/") {
		return false
	}
	cleaned := path.Clean(p)
	return cleaned == p || cleaned+"/" == p
}

// requestHost is the host the visitor asked for: Host, or X-Forwarded-Host
// when STATUS_PAGE_TRUST_FORWARDED_HOST is on and the peer is a trusted
// proxy (TRUSTED_PROXY_CIDRS). Normalized; "" when unusable.
func (h *Handlers) requestHost(r *http.Request) string {
	if h.config != nil && h.config.TrustForwardedHost && h.peerTrusted(r) {
		if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
			// The leftmost value is the host the client-facing proxy saw.
			if i := strings.IndexByte(fwd, ','); i >= 0 {
				fwd = fwd[:i]
			}
			return pagedomains.HostFromHeader(fwd)
		}
	}
	return pagedomains.HostFromHeader(r.Host)
}

func (h *Handlers) peerTrusted(r *http.Request) bool {
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	return h.clientIPs.Trusts(peer.Addr().Unmap())
}

// platformHosts are the hosts that serve the platform routes:
// STATUS_PAGE_BASE_URL's host and STATUS_PAGE_PLATFORM_HOSTS. Never a custom
// domain, even if a row named one (the API refuses that).
func (h *Handlers) platformHosts() []string {
	if h.config == nil {
		return nil
	}
	hosts := append([]string(nil), h.config.PlatformHosts...)
	if base := pagedomains.HostOfURL(h.config.StatusPageBaseURL); base != "" {
		hosts = append(hosts, base)
	}
	return hosts
}

func (h *Handlers) isPlatformHost(host string) bool {
	for _, p := range h.platformHosts() {
		if p == host {
			return true
		}
	}
	return false
}

// platformHostsConfigured reports whether at least one platform host is a
// public name, so other public names can be refused. A localhost base URL
// (development) does not count.
func (h *Handlers) platformHostsConfigured() bool {
	for _, p := range h.platformHosts() {
		if pagedomains.CanBeCustomDomain(p) {
			return true
		}
	}
	return false
}

// pagePath is the page's own URL path as the visitor sees it: / on its
// custom domain, /public/status/{slug} elsewhere.
func pagePath(r *http.Request, slug string) string {
	if _, ok := customHostFrom(r); ok {
		return "/"
	}
	return "/public/status/" + slug
}

// pageSubPath is a sub-route's path as the visitor sees it (suffix starts
// with "/").
func pageSubPath(r *http.Request, slug, suffix string) string {
	if _, ok := customHostFrom(r); ok {
		return suffix
	}
	return "/public/status/" + slug + suffix
}

// visitorPath is the path the visitor asked for, before any rewrite.
func visitorPath(r *http.Request) string {
	if ch, ok := customHostFrom(r); ok {
		return ch.Path
	}
	return r.URL.Path
}

// sessionCookieScope is where a visitor session cookie applies: host-only at
// / on a custom domain (the origin holds this one page), the page's path on
// the platform host. A session opened on one host never reaches the other:
// they are different origins.
func sessionCookieScope(r *http.Request, slug string) string {
	if _, ok := customHostFrom(r); ok {
		return "/"
	}
	return sessionCookiePath(slug)
}

// customHostPageMismatch reports a request on a custom domain whose rewritten
// slug now names a different page than the table did (the slug was renamed
// and reused before the table reloaded): it must answer not found rather
// than serve the other page.
func customHostPageMismatch(r *http.Request, pageID uuid.UUID) bool {
	ch, ok := customHostFrom(r)
	return ok && ch.PageID != pageID
}

// customHostCookieSecure is the session cookie's Secure flag on a custom
// domain: always on. Custom domains are served over https only (the chart
// requires a TLS Secret for each, and every URL the platform builds for one
// is https), so a kiosk display following a stray http:// link must not send
// its long-lived cookie in clear. Neither STATUS_PAGE_COOKIE_SECURE nor
// request detection, which describe the platform host, decide here.
func (h *Handlers) customHostCookieSecure(*http.Request) bool {
	return true
}
