package securityheaders

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/shared/clientip"
)

var trustedProxy = clientip.NewResolver([]netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})

func serve(mw func(http.Handler) http.Handler, req *http.Request) http.Header {
	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)
	return rec.Header()
}

func TestAPIHeaders(t *testing.T) {
	h := serve(API(nil, 0), httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil))
	want := map[string]string{
		HeaderContentTypeOptions: "nosniff",
		HeaderReferrerPolicy:     "strict-origin-when-cross-origin",
		HeaderPermissionsPolicy:  "camera=(), microphone=(), geolocation=(), payment=(), usb=()",
		HeaderFrameOptions:       "DENY",
		HeaderCSP:                "default-src 'none'; frame-ancestors 'none'",
		HeaderHSTS:               "",
	}
	for name, value := range want {
		if got := h.Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
}

func TestStatusPageHeaders(t *testing.T) {
	h := serve(StatusPage(nil, 0), httptest.NewRequest(http.MethodGet, "/public/status/acme", nil))
	want := map[string]string{
		HeaderContentTypeOptions: "nosniff",
		HeaderReferrerPolicy:     "strict-origin-when-cross-origin",
		HeaderPermissionsPolicy:  PermissionsPolicy,
		// Framing and CSP are per page; the handler sets them.
		HeaderFrameOptions: "",
		HeaderCSP:          "",
	}
	for name, value := range want {
		if got := h.Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
}

func TestHSTSOnlyOverHTTPS(t *testing.T) {
	const sts = "max-age=31536000"
	tests := []struct {
		name    string
		remote  string
		proto   string
		tls     bool
		maxAge  int
		wantSTS string
	}{
		{"disabled", "10.1.2.3:4000", "https", false, 0, ""},
		{"plain http", "203.0.113.9:4000", "", false, 31536000, ""},
		{"direct TLS", "203.0.113.9:4000", "", true, 31536000, sts},
		{"trusted proxy https", "10.1.2.3:4000", "https", false, 31536000, sts},
		{"trusted proxy https chained", "10.1.2.3:4000", "https, http", false, 31536000, sts},
		{"trusted proxy http", "10.1.2.3:4000", "http", false, 31536000, ""},
		{"untrusted peer claims https", "203.0.113.9:4000", "https", false, 31536000, ""},
		{"IPv4-mapped trusted peer", "[::ffff:10.1.2.3]:4000", "https", false, 31536000, sts},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remote
			if tt.proto != "" {
				req.Header.Set("X-Forwarded-Proto", tt.proto)
			}
			if tt.tls {
				req.TLS = &tls.ConnectionState{}
			}
			for _, mw := range []func(http.Handler) http.Handler{API(trustedProxy, tt.maxAge), StatusPage(trustedProxy, tt.maxAge)} {
				if got := serve(mw, req).Get(HeaderHSTS); got != tt.wantSTS {
					t.Fatalf("HSTS = %q, want %q", got, tt.wantSTS)
				}
			}
		})
	}
}

func TestResolveCSPMode(t *testing.T) {
	tests := []struct {
		stored     string
		customized bool
		want       string
	}{
		{"", false, CSPModeStrict},
		{"", true, CSPModeCompatible},
		{"strict", true, CSPModeStrict},
		{"OFF", false, CSPModeOff},
		{"compatible", false, CSPModeCompatible},
		{"bogus", false, CSPModeStrict},
		{"bogus", true, CSPModeCompatible},
	}
	for _, tt := range tests {
		if got := ResolveCSPMode(tt.stored, tt.customized); got != tt.want {
			t.Errorf("ResolveCSPMode(%q, %v) = %q, want %q", tt.stored, tt.customized, got, tt.want)
		}
	}
}

func TestStatusPagePolicy(t *testing.T) {
	ancestors := []string{"'self'", "https://ops.example.com"}

	strict := StatusPagePolicy(CSPModeStrict, "abc123", ancestors)
	for _, want := range []string{
		"script-src 'nonce-abc123'", "worker-src 'self'", "base-uri 'none'", "object-src 'none'",
		"frame-ancestors 'self' https://ops.example.com",
		// Custom CSS @import / @font-face and plain-http logos keep
		// rendering under strict.
		"style-src 'self' 'unsafe-inline' https:", "font-src 'self' data: https:", "img-src 'self' data: https: http:",
	} {
		if !strings.Contains(strict, want) {
			t.Errorf("strict policy %q lacks %q", strict, want)
		}
	}
	for _, directive := range strings.Split(strict, "; ") {
		if strings.HasPrefix(directive, "script-src ") && directive != "script-src 'nonce-abc123'" {
			t.Errorf("strict script-src must be the nonce alone, got %q", directive)
		}
		// A nonce in style-src would switch off 'unsafe-inline' for the
		// template's style="" attributes.
		if strings.HasPrefix(directive, "style-src ") && strings.Contains(directive, "nonce-") {
			t.Errorf("strict style-src carries a nonce: %q", directive)
		}
	}

	compat := StatusPagePolicy(CSPModeCompatible, "abc123", ancestors)
	if strings.Contains(compat, "nonce-") {
		t.Errorf("compatible policy carries a nonce, which disables 'unsafe-inline': %q", compat)
	}
	if !strings.Contains(compat, "script-src 'self' 'unsafe-inline' https: http:") {
		t.Errorf("compatible policy %q does not allow inline/https scripts", compat)
	}
	if !strings.HasSuffix(compat, "frame-ancestors 'self' https://ops.example.com") {
		t.Errorf("compatible policy %q lacks frame-ancestors", compat)
	}

	if got := StatusPagePolicy(CSPModeOff, "abc123", ancestors); got != "frame-ancestors 'self' https://ops.example.com" {
		t.Errorf("off policy = %q, want frame-ancestors only", got)
	}
	if got := StatusPagePolicy(CSPModeOff, "", nil); got != "frame-ancestors 'none'" {
		t.Errorf("off policy without ancestors = %q", got)
	}
}

func TestOrigin(t *testing.T) {
	ok := map[string]string{
		"https://Example.com":             "https://example.com",
		"https://example.com/app/":        "https://example.com",
		"http://localhost:3000":           "http://localhost:3000",
		"  https://intranet.corp.local ":  "https://intranet.corp.local",
		"https://[::1]:8443":              "https://[::1]:8443",
		"https://ops.example.com/?x=1#hi": "https://ops.example.com",
	}
	for in, want := range ok {
		got, valid := Origin(in)
		if !valid || got != want {
			t.Errorf("Origin(%q) = %q, %v; want %q", in, got, valid, want)
		}
	}
	bad := []string{
		"",
		"example.com",
		"ftp://example.com",
		"https://user@example.com",
		"https://exa mple.com",
		"https://example.com; script-src *",
		"https://example.com\r\nX-Evil: 1",
		"*",
		"'self'",
		"https://example.com:99999",
		"/relative",
	}
	for _, in := range bad {
		if got, valid := Origin(in); valid {
			t.Errorf("Origin(%q) = %q, want invalid", in, got)
		}
	}
}

func TestFrameAncestors(t *testing.T) {
	if got := strings.Join(FrameAncestors("https://OPS.example.com/app/"), " "); got != "'self' https://ops.example.com" {
		t.Fatalf("FrameAncestors = %q", got)
	}
	for _, unset := range []string{"", "not a url; script-src *"} {
		if got := FrameAncestors(unset); len(got) != 1 || got[0] != "'self'" {
			t.Fatalf("FrameAncestors(%q) = %v, want ['self']", unset, got)
		}
	}
}

func TestNewNonceIsFreshBase64(t *testing.T) {
	a, b := NewNonce(), NewNonce()
	if a == b || len(a) != 24 {
		t.Fatalf("nonces %q, %q: want two distinct 24-char values", a, b)
	}
}
