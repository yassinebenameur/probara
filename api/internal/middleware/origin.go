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
// Host, or the X-Forwarded-Host of a TRUSTED_PROXY_CIDRS peer (the web UI's
// /api proxy, an ingress). A nil *OriginPolicy trusts only that last one.
type OriginPolicy struct {
	allowed map[string]bool // scheme://host[:port], lowercase, default port dropped
	proxies *clientip.Resolver
}

// NewOriginPolicy builds the policy from the already-parsed configuration.
// Values that are not an http(s) origin are skipped.
func NewOriginPolicy(appBaseURL, publicBaseURL string, allowedOrigins []string, trustedProxies []netip.Prefix) *OriginPolicy {
	p := &OriginPolicy{allowed: map[string]bool{}, proxies: clientip.NewResolver(trustedProxies)}
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
// Sec-Fetch-Site: same-origin is not taken as proof when an Origin is
// present: it compares the page with the URL the browser fetched, and that
// URL can be a proxy to the API on another origin -- a status page's own
// /_sp_api, say -- whose script fetches it same-origin. Only the Origin names
// the page.
//
// A request with none of the three is allowed: every browser sends Origin on
// a cross-origin unsafe fetch or form post (and modern ones Sec-Fetch-Site on
// every request), so its absence means a non-browser client -- curl with a
// cookie jar, a script -- and such a client holds the cookie itself rather
// than having it attached by a browser on someone else's page.
func (p *OriginPolicy) Allows(r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		return p.trusts(origin, r)
	}
	if site := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))); site != "" {
		return site == "same-origin"
	}
	if referer := r.Header.Get("Referer"); referer != "" {
		u, err := url.Parse(referer)
		if err != nil || u.Host == "" {
			return false
		}
		return p.trusts(u.Scheme+"://"+u.Host, r)
	}
	return true
}

// RequireTrustedOrigin refuses an unsafe request from an untrusted origin
// (Allows) with 403 cross_origin_refused. It guards the anonymous routes that
// set or clear the session cookie -- sign-in (login CSRF), refresh and
// sign-out (forced logout) -- which run outside AuthMiddleware.
func (p *OriginPolicy) RequireTrustedOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unsafeMethod(r.Method) && !p.Allows(r) {
			writeCrossOriginRefused(w)
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
	if !p.Allows(r) {
		writeCrossOriginRefused(w)
		return false
	}
	if !acceptableBody(r) {
		apierrors.WriteError(w, http.StatusUnsupportedMediaType, UnsupportedMediaTypeCode,
			"a signed-in request with a body must send Content-Type: application/json")
		return false
	}
	return true
}

func writeCrossOriginRefused(w http.ResponseWriter) {
	apierrors.WriteError(w, http.StatusForbidden, CrossOriginRefusedCode,
		"cross-origin request refused: the session cookie is accepted on writes only from the operator UI, the API's own origin or API_ALLOWED_ORIGINS")
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
