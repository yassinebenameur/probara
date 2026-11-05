package worker

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/yassinebenameur/probara/shared/secrets"
)

// stripCredentialsOnRedirect removes credentials from a redirected request
// that leaves the trust of the request the monitor was configured for: a
// different host (or port) than the first request, or plain http after an
// https start. Go's client only drops Authorization/Cookie-style headers, only
// on a host change, and never on a downgrade, so a configured secret header —
// or basic auth on https://h/ -> http://h/ — would otherwise go out again,
// possibly in cleartext.
//
// Removed: every credential-named header (secrets.IsSensitiveHeaderName, which
// covers Authorization and so basic auth) and every name in secretNames (the
// monitor's secret_headers, or a synthetic step's headers that reference a
// secret variable). The redirect is still followed, without them. Go copies
// the first request's headers into each hop afresh, so this runs per hop.
func stripCredentialsOnRedirect(req *http.Request, via []*http.Request, secretNames []string) {
	if len(via) == 0 || !redirectLeavesOrigin(via[0].URL, req.URL) {
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

func redirectLeavesOrigin(from, to *url.URL) bool {
	if !strings.EqualFold(from.Host, to.Host) {
		return true
	}
	return strings.EqualFold(from.Scheme, "https") && !strings.EqualFold(to.Scheme, "https")
}
