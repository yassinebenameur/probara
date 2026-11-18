package middleware

import (
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/shared/clientip"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/securityheaders"
)

// Cross-site request forgery defence for the session cookie.
//
// SameSite=Lax is not enough on its own: the operator UI and the status pages
// are often the same *site* (one host on two ports, app.x and status.x), and a
// workspace editor can publish a status-page template whose script runs
// there. To the browser that script's fetch is same-site, so it carries the
// session cookie of an operator who opens the page, and a fetch with a
// text/plain body is a "simple" request that needs no CORS preflight. Every
// unsafe request (anything but GET, HEAD, OPTIONS) that a session cookie
// authenticates -- and the anonymous routes that set or clear that cookie --
// must therefore come from an origin the API trusts. A bearer credential (API
// key, MCP token) is never ambient, so it is not checked.

// CrossOriginRefusedCode is the error code of an unsafe cookie-authenticated
// (or sign-in, refresh, sign-out) request from an origin the API does not
// trust.
const CrossOriginRefusedCode = "cross_origin_refused"

// UnsupportedMediaTypeCode is the error code of an unsafe cookie-authenticated
// request whose body is not JSON (or, on an upload route, multipart).
const UnsupportedMediaTypeCode = "unsupported_media_type"

// MultipartUploadPaths are the cookie-authenticated routes that take a
// multipart/form-data body (the monitor import preview's file upload). Every
// other cookie-authenticated unsafe request with a body must send
// application/json. A new upload route must be listed here, or browsers get
// 415 on it (TestMultipartUploadPathsMatchCatalog checks the MCP catalog's
// multipart tools against this list).
var MultipartUploadPaths = []string{"/api/v1/monitors/import/preview"}

// OriginPolicy decides whether a browser request may act on the session
// cookie. Trusted are the operator UI's origin (APP_BASE_URL), the API's
// configured origin (PUBLIC_BASE_URL), the CORS allowlist
// (API_ALLOWED_ORIGINS), and the API's own origin as the request saw it: its
// Host, or the X-Forwarded-Host of a TRUSTED_PROXY_CIDRS peer. A nil
// *OriginPolicy trusts only that last one.
//
// Why this needs no configuration for the operator UI:
//
//   - The chart's bundled ingress (and any same-origin deployment) sends /api
//     to the API with the browser's Host: the page's Origin is the API's own.
//   - Everything else reaches the API through the UI's own /api proxy
//     (web/app/api, web/lib/api-proxy.ts), where the API sees an internal
//     Host. That proxy makes the same-origin comparison itself, against one
//     value: the Host header it was reached with -- any port-forward, LAN
//     address, Caddy or nginx-ingress keeps the browser's -- or, only when the
//     operator sets API_PROXY_TRUST_FORWARDED_HOST=true for a fronting proxy
//     that rewrites Host, that proxy's X-Forwarded-Host. When the page is its
//     own origin it forwards the write without Origin, Referer and
//     Sec-Fetch-Site, which this policy lets through as it does any client
//     that sends none of them (below). A page on another origin cannot make
//     that comparison succeed: the browser sets Host from the URL it fetched,
//     so a page on another host or port never matches. A client-chosen
//     X-Forwarded-Host is neither compared nor passed on (the proxy sends the
//     browser's host instead), since anything that forwards client headers to
//     the frontend would let a same-origin script on another origin choose
//     it. A write the proxy cannot vouch for keeps its headers and is judged
//     here.
//
// The API itself never believes X-Forwarded-Host from an untrusted peer: a
// script can set any header on a same-origin request, and a proxy to the API
// on another origin that passes client headers through (the status page's
// /_sp_api) turns that into an attacker-chosen forwarded host next to an
// attacker Origin. A trusted peer's (the UI proxy's, listed in
// TRUSTED_PROXY_CIDRS) is the browser's host, never a client value. For the
// same reason Sec-Fetch-Site: same-origin is not taken as proof here when an
// Origin is present: it compares the page with the URL the browser fetched,
// which can be such a proxy.
type OriginPolicy struct {
	allowed map[string]bool // scheme://host[:port], lowercase, default port dropped
	proxies *clientip.Resolver
	log     *logger.Logger
}

// NewOriginPolicy builds the policy from the already-parsed configuration.
// Values that are not an http(s) origin are skipped. log (optional) records
// each refusal with the origin refused.
func NewOriginPolicy(appBaseURL, publicBaseURL string, allowedOrigins []string, trustedProxies []netip.Prefix, log *logger.Logger) *OriginPolicy {
	p := &OriginPolicy{allowed: map[string]bool{}, proxies: clientip.NewResolver(trustedProxies), log: log}
	for _, raw := range append([]string{appBaseURL, publicBaseURL}, allowedOrigins...) {
		if o, ok := parseOrigin(raw); ok {
			p.allowed[o.String()] = true
		}
	}
	return p
}

// Allows reports whether r comes from a trusted origin, judged on the first
// of these the request carries:
//
//   - Origin: a configured origin or the API's own. "null" (a sandboxed or
//     redirected context) is not trusted.
//   - Sec-Fetch-Site (no Origin): only "same-origin".
//   - Referer (neither): its origin, by the Origin rule.
//
// A request with none of the three is allowed: every browser sends Origin on
// a cross-origin unsafe fetch or form post (and modern ones Sec-Fetch-Site on
// every request), so its absence means a non-browser client -- curl with a
// cookie jar, a script -- or a write the UI's /api proxy checked and vouched
// for (see OriginPolicy). Neither had the cookie attached by a browser on
// someone else's page.
func (p *OriginPolicy) Allows(r *http.Request) bool {
	ok, _ := p.judge(r)
	return ok
}

// judge is Allows plus what the refusal was based on, for its message.
func (p *OriginPolicy) judge(r *http.Request) (bool, string) {
	if origin := r.Header.Get("Origin"); origin != "" {
		return p.trusts(origin, r), "origin " + displayOrigin(origin)
	}
	if site := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))); site != "" {
		return site == "same-origin", "a " + displaySite(site) + " request without an Origin"
	}
	if referer := r.Header.Get("Referer"); referer != "" {
		u, err := url.Parse(referer)
		if err != nil || u.Host == "" {
			return false, "an unreadable Referer"
		}
		page := u.Scheme + "://" + u.Host
		return p.trusts(page, r), "origin " + displayOrigin(page) + " (from the Referer)"
	}
	return true, ""
}

// RequireTrustedOrigin refuses an unsafe request from an untrusted origin
// (Allows) with 403 cross_origin_refused. It guards the anonymous routes that
// set or clear the session cookie -- sign-in (login CSRF), refresh and
// sign-out (forced logout) -- which run outside AuthMiddleware.
func (p *OriginPolicy) RequireTrustedOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unsafeMethod(r.Method) && !p.admitOrigin(w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// admitCookieRequest is AuthMiddleware's check for a request the session
// cookie authenticates: an unsafe method needs a trusted origin and, when it
// has a body, a JSON one (multipart on MultipartUploadPaths). A text/plain or
// form-encoded body is what a cross-site "simple" request can send without a
// preflight, so refusing it keeps a JSON payload out of one even if an origin
// check were ever bypassed. It writes the refusal and reports false.
func (p *OriginPolicy) admitCookieRequest(w http.ResponseWriter, r *http.Request) bool {
	if !unsafeMethod(r.Method) {
		return true
	}
	if !p.admitOrigin(w, r) {
		return false
	}
	if !acceptableBody(r) {
		apierrors.WriteError(w, http.StatusUnsupportedMediaType, UnsupportedMediaTypeCode,
			"a signed-in request with a body must send Content-Type: application/json")
		return false
	}
	return true
}

// admitOrigin writes the 403 for a request from an untrusted origin, naming
// that origin and the setting that would trust it, and logs it.
func (p *OriginPolicy) admitOrigin(w http.ResponseWriter, r *http.Request) bool {
	ok, seen := p.judge(r)
	if ok {
		return true
	}
	if p != nil && p.log != nil {
		p.log.WithFields(map[string]interface{}{
			"path":   r.URL.Path,
			"method": r.Method,
			"origin": seen,
			"host":   r.Host,
		}).Warn("Refused a session-cookie write from an untrusted origin; if this is the operator UI, set APP_BASE_URL to its origin")
	}
	apierrors.WriteError(w, http.StatusForbidden, CrossOriginRefusedCode,
		"cross-origin request refused: "+seen+" is not the operator UI. If it is, set APP_BASE_URL on the API to that origin "+
			"(or add it to API_ALLOWED_ORIGINS); browsers may write with the session cookie only from the operator UI.")
	return false
}

// displayOrigin is origin as it may be shown back: normalized
// scheme://host[:port] when it parses, a fixed word otherwise, never the raw
// header.
func displayOrigin(raw string) string {
	o, ok := parseOrigin(raw)
	if !ok {
		if strings.TrimSpace(raw) == "null" {
			return "null (an opaque origin)"
		}
		return "(unreadable)"
	}
	if (o.scheme == "https" && o.port == "443") || (o.scheme == "http" && o.port == "80") {
		return o.scheme + "://" + o.host
	}
	return o.scheme + "://" + net.JoinHostPort(o.host, o.port)
}

func displaySite(site string) string {
	switch site {
	case "same-site", "cross-site", "none":
		return site
	}
	return "foreign"
}

func unsafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// acceptableBody: no body at all (whatever Content-Type says, it carries
// nothing), or a JSON body, or multipart on an upload route. A body of
// unknown length (chunked) counts as a body.
func acceptableBody(r *http.Request) bool {
	if r.ContentLength == 0 {
		return true
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return false
	}
	switch media {
	case "application/json":
		return true
	case "multipart/form-data":
		for _, path := range MultipartUploadPaths {
			if r.URL.Path == path {
				return true
			}
		}
	}
	return false
}

// trusts reports whether origin (scheme://host[:port]) is a configured origin
// or the API's own as this request saw it.
func (p *OriginPolicy) trusts(origin string, r *http.Request) bool {
	o, ok := parseOrigin(origin)
	if !ok {
		return false
	}
	if p != nil && p.allowed[o.String()] {
		return true
	}
	return p.isOwnOrigin(o, r)
}

// isOwnOrigin compares o with the origin the request was sent to: the Host
// header, or a trusted proxy's X-Forwarded-Host (a forwarding header from any
// other peer is ignored, as for client addresses). The scheme is compared
// only when the request is known to be HTTPS (TLS here, or a trusted proxy's
// X-Forwarded-Proto): behind an untrusted TLS-terminating ingress the API
// cannot tell, and a host match is what separates the UI from a status page
// on another host or port.
func (p *OriginPolicy) isOwnOrigin(o origin, r *http.Request) bool {
	var proxies *clientip.Resolver
	if p != nil {
		proxies = p.proxies
	}
	host := r.Host
	if proxies.TrustsPeer(r) {
		if fh := firstForwardedValue(r.Header.Get("X-Forwarded-Host")); fh != "" {
			host = fh
		}
	}
	if host == "" {
		return false
	}
	https := securityheaders.IsHTTPS(r, proxies)
	if https && o.scheme != "https" {
		return false
	}
	scheme := o.scheme
	if https {
		scheme = "https"
	}
	own, ok := parseOrigin(scheme + "://" + host)
	return ok && own.host == o.host && own.port == o.port
}

// origin is a parsed scheme://host[:port], lowercase, with the scheme's
// default port made explicit so "https://x" and "https://x:443" are equal.
type origin struct {
	scheme, host, port string
}

func (o origin) String() string {
	return o.scheme + "://" + net.JoinHostPort(o.host, o.port)
}

func parseOrigin(raw string) (origin, bool) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return origin{}, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return origin{}, false
	}
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	switch {
	case scheme == "https" && port == "":
		port = "443"
	case scheme == "http" && port == "":
		port = "80"
	case scheme != "https" && scheme != "http":
		return origin{}, false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return origin{}, false
	}
	return origin{scheme: scheme, host: host, port: port}, true
}

func firstForwardedValue(v string) string {
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}
