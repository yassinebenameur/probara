package statuspage

import (
	"context"
	"net/http"
	"net/netip"
	"path"
	"strings"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/pagedomains"
	"github.com/yassinebenameur/probara/shared/securityheaders"
)

// Serving a page on its custom domain.
//
// CustomHosts runs in front of every route. A request whose host is a
// verified custom domain (hostTable) is rewritten onto the page's slug
// paths, so the visibility gate, the route table, the caches and every
// handler apply unchanged:
//
//	/                     → /public/status/{slug}/
//	/{rest}               → /public/status/{slug}/{rest}   (data, stream, push, unlock, add-ons, …)
//	/sw.js                → /public/status/sw.js
//
// Everything on a custom host is therefore a sub-route of that one page and
// passes its gate: other slugs, the /_sp_api operator proxy, /metrics and
// the health routes answer not found there. On any other host nothing
// changes. The rewritten request carries the custom host (customHostFrom),
// which is what makes the session cookie host-only at path /, the unlock
// form post to /unlock, redirects go to the visitor's own URL, the service
// worker scope the root, and push subscriptions remember their origin.
//
// A custom host is its own origin and holds a single page, so the
// cross-page refusal that protects gated pages sharing the platform origin
// (crossPageRequest) has nothing to guard against there.

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

// CustomHosts wraps next with the custom-domain rewrite.
func (h *Handlers) CustomHosts(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := h.requestHost(r)
		if !mayBeCustomHost(host) || host == h.platformHost() {
			next.ServeHTTP(w, r)
			return
		}
		route, ok := h.hosts.Lookup(r.Context(), host)
		if !ok {
			next.ServeHTTP(w, r)
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
		target := "/public/status/" + route.Slug + visitorPath
		if visitorPath == "/sw.js" {
			target = "/public/status/sw.js"
		}
		ctx := context.WithValue(r.Context(), customHostKey{}, customHost{Host: host, PageID: route.PageID, Slug: route.Slug, Path: visitorPath})
		rewritten := r.WithContext(ctx)
		u := *r.URL
		u.Path = target
		u.RawPath = ""
		rewritten.URL = &u
		next.ServeHTTP(w, rewritten)
	})
}

// mayBeCustomHost reports whether host could be a custom domain at all. An
// IP address or a single label (localhost, a Service name) never is
// (pagedomains.NormalizeHostname refuses them), so such requests skip the
// table: kubelet probes and metrics scrapes, which arrive by pod IP, never
// wait on a table reload, whatever the database is doing.
func mayBeCustomHost(host string) bool {
	if host == "" || !strings.Contains(host, ".") {
		return false
	}
	_, err := netip.ParseAddr(host)
	return err != nil
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

// platformHost is STATUS_PAGE_BASE_URL's host: never a custom domain, even
// if a row named it (the API refuses that).
func (h *Handlers) platformHost() string {
	if h.config == nil {
		return ""
	}
	return pagedomains.HostOfURL(h.config.StatusPageBaseURL)
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
// domain: STATUS_PAGE_COOKIE_SECURE when set, else the request's own scheme
// (X-Forwarded-Proto counts only from a trusted proxy). An https
// STATUS_PAGE_BASE_URL describes the platform host only.
func (h *Handlers) customHostCookieSecure(r *http.Request) bool {
	if secure, detect := h.config.CustomHostCookieSecure(); !detect {
		return secure
	}
	return securityheaders.IsHTTPS(r, h.clientIPs)
}
