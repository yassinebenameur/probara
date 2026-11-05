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
		HeaderReferrerPolicy:     "no-referrer",
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
		HeaderReferrerPolicy:     "no-referrer",
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
	const sts = "max-age=31536000; includeSubDomains"
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
	for _, want := range []string{"script-src 'nonce-abc123'", "worker-src 'self'", "base-uri 'none'", "frame-ancestors 'self' https://ops.example.com"} {
		if !strings.Contains(strict, want) {
			t.Errorf("strict policy %q lacks %q", strict, want)
		}
	}
	if strings.Contains(strict, "unsafe-inline' https:") || strings.Contains(strict, "script-src 'self' 'unsafe-inline'") {
		t.Errorf("strict policy allows inline scripts: %q", strict)
	}

	compat := StatusPagePolicy(CSPModeCompatible, "abc123", ancestors)
	if strings.Contains(compat, "nonce-") {
		t.Errorf("compatible policy carries a nonce, which disables 'unsafe-inline': %q", compat)
	}
	if !strings.Contains(compat, "script-src 'self' 'unsafe-inline' https:") {
		t.Errorf("compatible policy %q does not allow inline/https scripts", compat)
	}

	if got := StatusPagePolicy(CSPModeOff, "abc123", ancestors); got != "frame-ancestors 'self' https://ops.example.com" {
		t.Errorf("off policy = %q, want frame-ancestors only", got)
	}
	if got := StatusPagePolicy(CSPModeOff, "", nil); got != "frame-ancestors 'none'" {
		t.Errorf("off policy without ancestors = %q", got)
	}
}

func TestNormalizeOrigin(t *testing.T) {
	ok := map[string]string{
		"https://Example.com":            "https://example.com",
		"https://example.com/":           "https://example.com",
		"http://localhost:3000":          "http://localhost:3000",
		"https://*.example.com":          "https://*.example.com",
		"https://*.example.com:8443":     "https://*.example.com:8443",
		"  https://intranet.corp.local ": "https://intranet.corp.local",
		"https://[::1]:8443":             "https://[::1]:8443",
	}
	for in, want := range ok {
		got, err := NormalizeOrigin(in)
		if err != nil || got != want {
			t.Errorf("NormalizeOrigin(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"",
		"example.com",
		"ftp://example.com",
		"https://example.com/path",
		"https://example.com?x=1",
		"https://user@example.com",
		"https://exa mple.com",
		"https://example.com; script-src *",
		"https://example.com\r\nX-Evil: 1",
		"*",
		"'self'",
		"https://example.com:99999",
	}
	for _, in := range bad {
		if got, err := NormalizeOrigin(in); err == nil {
			t.Errorf("NormalizeOrigin(%q) = %q, want error", in, got)
		}
	}
}

func TestFrameAncestors(t *testing.T) {
	got := FrameAncestors("https://ops.example.com/app/", []string{"https://intranet.example.com", "https://OPS.example.com", "not an origin; script-src *"})
	want := "'self' https://ops.example.com https://intranet.example.com"
	if strings.Join(got, " ") != want {
		t.Fatalf("FrameAncestors = %q, want %q", strings.Join(got, " "), want)
	}
	if got := FrameAncestors("", nil); len(got) != 1 || got[0] != "'self'" {
		t.Fatalf("FrameAncestors with nothing configured = %v, want ['self']", got)
	}
}

func TestNewNonceIsFreshBase64(t *testing.T) {
	a, b := NewNonce(), NewNonce()
	if a == b || len(a) != 24 {
		t.Fatalf("nonces %q, %q: want two distinct 24-char values", a, b)
	}
}
