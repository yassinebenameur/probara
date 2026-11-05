// Package securityheaders owns the browser security headers the Go services
// send: the API's strict JSON policy, the status page's per-page CSP, and
// HSTS. The operator UI (Next.js) builds its own nonce CSP in web/proxy.ts;
// its directive list mirrors the reasoning here.
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
	HeaderHSTS               = "Strict-Transport-Security"
)

// APIPolicy is the CSP on every API response. The API serves JSON, files and
// redirects, never a document, so nothing may load and nothing may frame it.
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

// SetHSTS adds Strict-Transport-Security when maxAge > 0 and the request is
// HTTPS (IsHTTPS). Browsers ignore the header over plain HTTP anyway; the
// check keeps a plain-HTTP deployment from advertising a promise it cannot
// keep through a proxy that does terminate TLS for some hosts only.
func SetHSTS(h http.Header, r *http.Request, proxies *clientip.Resolver, maxAge int) {
	if maxAge <= 0 || !IsHTTPS(r, proxies) {
		return
	}
	h.Set(HeaderHSTS, "max-age="+strconv.Itoa(maxAge)+"; includeSubDomains")
}

func setBase(h http.Header) {
	h.Set(HeaderContentTypeOptions, "nosniff")
	h.Set(HeaderReferrerPolicy, "no-referrer")
}

// API is the API server's middleware: nosniff, no referrer, no framing, the
// APIPolicy CSP, and HSTS when applicable. Headers are set before the handler
// runs, so a handler may still override one for a response that needs it.
func API(proxies *clientip.Resolver, hstsMaxAge int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			setBase(h)
			h.Set(HeaderFrameOptions, "DENY")
			h.Set(HeaderCSP, APIPolicy)
			SetHSTS(h, r, proxies, hstsMaxAge)
			next.ServeHTTP(w, r)
		})
	}
}

// StatusPage is the status-page server's middleware: nosniff, no referrer
// and HSTS on every response. It sets no CSP and no X-Frame-Options: framing
// is a per-page setting (allowed_embed_origins), so the page handler sends
// the CSP, frame-ancestors included, with the rendered HTML.
func StatusPage(proxies *clientip.Resolver, hstsMaxAge int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			setBase(h)
			SetHSTS(h, r, proxies, hstsMaxAge)
			next.ServeHTTP(w, r)
		})
	}
}

// Status page CSP modes (the csp_mode page setting).
const (
	// CSPModeStrict allows only the page's own nonce-carrying inline scripts
	// and same-origin resources (plus https: images for remote logos).
	CSPModeStrict = "strict"
	// CSPModeCompatible allows inline scripts and https: third-party
	// resources, for pages whose custom HTML embeds widgets or analytics.
	CSPModeCompatible = "compatible"
	// CSPModeOff sends no resource policy; only frame-ancestors remains,
	// because embedding is controlled separately by allowed_embed_origins.
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
// wins; automatic is compatible when the page carries tenant-authored markup
// (custom head/footer HTML or a published custom template), which may hold
// inline or third-party scripts written before the policy existed, and
// strict otherwise.
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
		return strings.Join([]string{
			"default-src 'self' https: data:",
			"script-src 'self' 'unsafe-inline' https:",
			"style-src 'self' 'unsafe-inline' https:",
			"img-src 'self' data: https:",
			"font-src 'self' data: https:",
			"connect-src 'self' https: wss:",
			"frame-src https:",
			"worker-src 'self'",
			"object-src 'none'",
			"base-uri 'self'",
			ancestors,
		}, "; ")
	default:
		return strings.Join([]string{
			"default-src 'self'",
			"script-src 'nonce-" + nonce + "'",
			"style-src 'self' 'unsafe-inline'",
			// https: so a logo_url on another host still renders.
			"img-src 'self' data: https:",
			"font-src 'self' data:",
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

// MaxEmbedOrigins bounds allowed_embed_origins; the list lands in a response
// header on every page view.
const MaxEmbedOrigins = 20

// NormalizeOrigin validates an embedding origin and returns its canonical
// form: scheme://host[:port], lowercase, http or https, no path, query,
// fragment or credentials. A leading "*." host wildcard is allowed (CSP
// matches any subdomain). Everything reaching a CSP header goes through this,
// so a value can never inject a directive.
func NormalizeOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("origin is empty")
	}
	wildcard := false
	candidate := raw
	if scheme, rest, ok := strings.Cut(raw, "://*."); ok {
		wildcard = true
		candidate = scheme + "://" + rest
	}
	u, err := url.Parse(candidate)
	if err != nil {
		return "", fmt.Errorf("origin %q is not a URL", raw)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && scheme != "http" {
		return "", fmt.Errorf("origin %q must start with https:// or http://", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || (u.Path != "" && u.Path != "/") || strings.HasSuffix(raw, "?") || strings.HasSuffix(raw, "#") {
		return "", fmt.Errorf("origin %q must be scheme://host[:port] with no path", raw)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || !validHost(host) {
		return "", fmt.Errorf("origin %q has an invalid host", raw)
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if wildcard {
		host = "*." + host
	}
	out := scheme + "://" + host
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("origin %q has an invalid port", raw)
		}
		out += ":" + port
	}
	return out, nil
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

// FrameAncestors returns a status page's frame-ancestors source list: 'self',
// the operator UI origin (APP_BASE_URL, which frames the template editor's
// preview) and the page's allowed embed origins. Entries that do not
// normalize are dropped rather than trusted: the API validates on write, so
// one here means a row written by something else.
func FrameAncestors(appBaseURL string, embedOrigins []string) []string {
	out := []string{"'self'"}
	seen := map[string]bool{}
	add := func(raw string) {
		origin, err := NormalizeOrigin(raw)
		if err != nil || seen[origin] {
			return
		}
		seen[origin] = true
		out = append(out, origin)
	}
	if strings.TrimSpace(appBaseURL) != "" {
		if u, err := url.Parse(strings.TrimSpace(appBaseURL)); err == nil && u.Host != "" {
			add(u.Scheme + "://" + u.Host)
		}
	}
	for i, raw := range embedOrigins {
		if i >= MaxEmbedOrigins {
			break
		}
		add(raw)
	}
	return out
}

func peerAddr(remoteAddr string) (netip.Addr, bool) {
	if ap, err := netip.ParseAddrPort(remoteAddr); err == nil {
		return ap.Addr().Unmap(), true
	}
	if a, err := netip.ParseAddr(remoteAddr); err == nil {
		return a.Unmap(), true
	}
	return netip.Addr{}, false
}
