package worker

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/yassinebenameur/probara/shared/secrets"
)

// stripCredentialsOnRedirect removes request credentials from a redirect hop
// that leaves the origin (scheme, host and port) of the request the monitor
// was configured for, which includes any https -> http downgrade. Go's client
// drops only Authorization/Cookie-style headers, only when the host stops
// matching (a subdomain still gets them), and never on a scheme or port
// change, so a configured secret header, or basic auth on https://h/ ->
// http://h/, would otherwise go out again, possibly in cleartext.
//
// Removed: every credential-named header (secrets.IsSensitiveHeaderName,
// which covers Authorization and so basic auth, Proxy-Authorization and
// Cookie) and every name in secretNames (the monitor's secret_headers, or a
// synthetic step's headers that reference a secret). The redirect is still
// followed, without them. Go copies the first request's headers into every
// hop afresh, so this runs per hop, and each hop is compared with that first
// request: credentials only ever reach the configured origin.
func stripCredentialsOnRedirect(req *http.Request, via []*http.Request, secretNames []string) {
	if len(via) == 0 || sameOrigin(via[0].URL, req.URL) {
		return
	}
	for name := range req.Header {
		if secrets.IsSensitiveHeaderName(name) {
			req.Header.Del(name)
		}
	}
	for _, name := range secretNames {
		req.Header.Del(name)
	}
}

// sameOrigin compares scheme, host and effective port (a default port written
// out or left implicit is the same origin).
func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		effectivePort(a) == effectivePort(b)
}

func effectivePort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	}
	return ""
}
