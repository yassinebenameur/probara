// Package securityheaders owns the browser security headers the Go services
// send: the API's strict policy for non-document responses, the status
// page's per-page Content-Security-Policy, and HSTS. The operator UI
// (Next.js) builds its own nonce CSP in web/proxy.ts and its static headers
// in web/next.config.js; keep the static values there in step with Base.
//
// HSTS is sent only on requests that reached us over HTTPS -- TLS terminated
// by this process, or X-Forwarded-Proto: https from a TRUSTED_PROXY_CIDRS
// peer (shared/clientip) -- and only when SECURITY_HSTS_MAX_AGE > 0. A
// forwarding header from any other peer is client-settable and never counts.
package securityheaders

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/yassinebenameur/probara/shared/clientip"
)

// Header names, spelled once.
const (
	HeaderCSP                = "Content-Security-Policy"
	HeaderContentTypeOptions = "X-Content-Type-Options"
	HeaderFrameOptions       = "X-Frame-Options"
	HeaderReferrerPolicy     = "Referrer-Policy"
	HeaderPermissionsPolicy  = "Permissions-Policy"
	HeaderHSTS               = "Strict-Transport-Security"
)

// ReferrerPolicy is sent by every surface. Not no-referrer: under
// no-referrer a browser serializes the Origin of a document's own
// non-GET requests as "null", which the status page's push subscribe check
// and the OAuth consent check refuse. This policy never sends a path to
// another origin and never sends anything on an HTTPS-to-HTTP hop.
const ReferrerPolicy = "strict-origin-when-cross-origin"

// PermissionsPolicy turns off device and payment features no Probara page
// uses, so an injected script or a framed page cannot prompt for them.
// Notifications (status-page push) are not a policy-controlled feature.
const PermissionsPolicy = "camera=(), microphone=(), geolocation=(), payment=(), usb=()"

// APIPolicy is the CSP on every API response. The API serves JSON, files
// and redirects, never a document, so nothing may load and nothing may frame
// it. A handler that ever renders HTML must set its own policy.
const APIPolicy = "default-src 'none'; frame-ancestors 'none'"

// IsHTTPS reports whether the client reached this service over HTTPS: either
// this process terminated TLS, or a trusted proxy said so in
// X-Forwarded-Proto. The leftmost X-Forwarded-Proto value is the scheme the
// client-facing proxy saw.
func IsHTTPS(r *http.Request, proxies *clientip.Resolver) bool {
	if r.TLS != nil {
		return true
	}
	peer, ok := peerAddr(r.RemoteAddr)
	if !ok || !proxies.Trusts(peer) {
		return false
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if i := strings.IndexByte(proto, ','); i >= 0 {
		proto = proto[:i]
	}
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

// HSTSValue is the Strict-Transport-Security value for maxAge seconds. No
// includeSubDomains: the header must not commit sibling hosts the operator
// may still serve over plain HTTP.
func HSTSValue(maxAge int) string {
	return "max-age=" + strconv.Itoa(maxAge)
}

// SetHSTS adds Strict-Transport-Security when maxAge > 0 and the request is
// HTTPS (IsHTTPS).
func SetHSTS(h http.Header, r *http.Request, proxies *clientip.Resolver, maxAge int) {
	if maxAge <= 0 || !IsHTTPS(r, proxies) {
		return
	}
	h.Set(HeaderHSTS, HSTSValue(maxAge))
}

// Base sets the headers every response from every surface carries.
func Base(h http.Header) {
	h.Set(HeaderContentTypeOptions, "nosniff")
	h.Set(HeaderReferrerPolicy, ReferrerPolicy)
	h.Set(HeaderPermissionsPolicy, PermissionsPolicy)
}

// API is the API server's middleware: Base, no framing, the APIPolicy CSP,
// and HSTS when applicable. Headers are set before the handler runs, so a
// handler may still override one for a response that needs it.
func API(proxies *clientip.Resolver, hstsMaxAge int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			Base(h)
			h.Set(HeaderFrameOptions, "DENY")
			h.Set(HeaderCSP, APIPolicy)
			SetHSTS(h, r, proxies, hstsMaxAge)
			next.ServeHTTP(w, r)
		})
	}
}

// StatusPage is the status-page server's middleware: Base and HSTS on every
// response. It sets no CSP and no X-Frame-Options: X-Frame-Options cannot
// name the operator UI's origin, so framing is controlled by the
// frame-ancestors directive the page handler sends with each rendered page.
func StatusPage(proxies *clientip.Resolver, hstsMaxAge int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			Base(h)
			SetHSTS(h, r, proxies, hstsMaxAge)
			next.ServeHTTP(w, r)
		})
	}
}

// Status page CSP modes (the csp_mode page setting).
const (
	// CSPModeStrict runs only the page's own nonce-carrying scripts. It still
	// admits remote stylesheets, fonts and images (see StatusPagePolicy),
	// because those cannot run code.
	CSPModeStrict = "strict"
	// CSPModeCompatible allows inline scripts and third-party resources over
	// http: or https:, for pages whose custom HTML or CSS embeds widgets,
	// analytics or web fonts.
	CSPModeCompatible = "compatible"
	// CSPModeOff sends no resource policy; only frame-ancestors remains.
	CSPModeOff = "off"
)

// ParseCSPMode normalizes a stored or submitted csp_mode. "" is valid and
// means automatic (see ResolveCSPMode).
func ParseCSPMode(raw string) (string, bool) {
	switch v := strings.ToLower(strings.TrimSpace(raw)); v {
	case "", CSPModeStrict, CSPModeCompatible, CSPModeOff:
		return v, true
	default:
		return "", false
	}
}

// ResolveCSPMode returns the effective mode for a page. An explicit mode
// wins; automatic is compatible when the page carries operator-authored
// customization (custom CSS, custom head/footer HTML or a custom template),
// which may load inline or third-party resources written before the policy
// existed, and strict otherwise.
func ResolveCSPMode(stored string, customized bool) string {
	if mode, ok := ParseCSPMode(stored); ok && mode != "" {
		return mode
	}
	if customized {
		return CSPModeCompatible
	}
	return CSPModeStrict
}

// NewNonce returns a fresh CSP nonce: 128 random bits, base64.
func NewNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail on supported platforms; a fixed nonce
		// would silently defeat the policy, so refuse loudly instead.
		panic(fmt.Sprintf("securityheaders: crypto/rand failed: %v", err))
	}
	return base64.StdEncoding.EncodeToString(b[:])
}

// StatusPagePolicy builds a status page's CSP for mode. nonce is used only in
// strict mode (a nonce next to 'unsafe-inline' would disable the latter).
// frameAncestors is the full source list, normally from FrameAncestors.
func StatusPagePolicy(mode, nonce string, frameAncestors []string) string {
	ancestors := "frame-ancestors " + strings.Join(frameAncestors, " ")
	if len(frameAncestors) == 0 {
		ancestors = "frame-ancestors 'none'"
	}
	switch mode {
	case CSPModeOff:
		return ancestors
	case CSPModeCompatible:
		// http: as well as https:: the mode exists so pages customized before
		// the policy keep working, and on an HTTPS page the browser's own
		// mixed-content rules still refuse active http: content.
		return strings.Join([]string{
			"default-src 'self' https: http: data:",
			"script-src 'self' 'unsafe-inline' https: http:",
			"style-src 'self' 'unsafe-inline' https: http:",
			"img-src 'self' data: https: http:",
			"font-src 'self' data: https: http:",
			"connect-src 'self' https: http: wss: ws:",
			"frame-src https: http:",
			"worker-src 'self'",
			"object-src 'none'",
			"base-uri 'self'",
			ancestors,
		}, "; ")
	default:
		return strings.Join([]string{
			"default-src 'self'",
			"script-src 'nonce-" + nonce + "'",
			// Strict guards script execution. Remote stylesheets and fonts
			// (custom CSS @import, @font-face) and remote logos cannot run
			// code, so they stay allowed: https: for styles and fonts, and
			// http: too for images, so a plain-http logo_url still renders
			// (an HTTPS page's browser upgrades or blocks it on its own).
			// style-src keeps 'unsafe-inline' for the template's style=""
			// attributes, which cannot carry a nonce.
			"style-src 'self' 'unsafe-inline' https:",
			"img-src 'self' data: https: http:",
			"font-src 'self' data: https:",
			"connect-src 'self'",
			// The push service worker (/public/status/sw.js) is a same-origin
			// script URL, which a nonce-only script-src would refuse.
			"worker-src 'self'",
			"manifest-src 'self'",
			"object-src 'none'",
			"base-uri 'none'",
			"form-action 'self'",
			ancestors,
		}, "; ")
	}
}

// FrameAncestors returns a status page's frame-ancestors source list: 'self'
// and the operator UI origin (APP_BASE_URL), whose template editor previews
// pages in an iframe. An APP_BASE_URL that is not an http(s) URL is ignored,
// so the value can never inject a directive.
func FrameAncestors(appBaseURL string) []string {
	out := []string{"'self'"}
	if origin, ok := Origin(appBaseURL); ok {
		out = append(out, origin)
	}
	return out
}

// Origin returns the scheme://host[:port] of an absolute http(s) URL,
// lowercased, or false when raw is not one (or its host holds characters
// that do not belong in a host).
func Origin(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.User != nil {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && scheme != "http" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || !validHost(host) {
		return "", false
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	out := scheme + "://" + host
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", false
		}
		out += ":" + port
	}
	return out, true
}

// validHost accepts DNS names (letters, digits, '-' and '.') and IP literals.
func validHost(host string) bool {
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	for _, c := range host {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '.':
		default:
			return false
		}
	}
	return !strings.HasPrefix(host, ".") && !strings.HasSuffix(host, ".") && !strings.Contains(host, "..")
}

func peerAddr(remoteAddr string) (netip.Addr, bool) {
	if ap, err := netip.ParseAddrPort(remoteAddr); err == nil {
		return ap.Addr().WithZone("").Unmap(), true
	}
	if a, err := netip.ParseAddr(remoteAddr); err == nil {
		return a.WithZone("").Unmap(), true
	}
	return netip.Addr{}, false
}
