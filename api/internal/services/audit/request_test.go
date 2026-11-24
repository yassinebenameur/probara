package audit

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yassinebenameur/probara/shared/clientip"
)

// The audit IP comes from shared/clientip: a client-set X-Forwarded-For is
// believed only when the peer is a trusted proxy.
func TestFromRequestUsesTrustedProxyResolver(t *testing.T) {
	trusted, err := clientip.ParseTrustedProxies("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	resolver := clientip.NewResolver(trusted)

	cases := []struct {
		name, remote, xff, want string
	}{
		{"spoofed header from untrusted peer", "203.0.113.7:5555", "1.2.3.4", "203.0.113.7"},
		{"forwarded by trusted proxy", "10.0.0.2:80", "6.6.6.6, 198.51.100.9", "198.51.100.9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got Event
			h := resolver.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = FromRequest(r)
			}))
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.RemoteAddr = tc.remote
			req.Header.Set("X-Forwarded-For", tc.xff)
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got.IP != tc.want {
				t.Fatalf("IP = %q, want %q", got.IP, tc.want)
			}
		})
	}
}
