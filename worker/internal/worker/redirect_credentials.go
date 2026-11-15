package worker

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/yassinebenameur/probara/shared/secrets"
)

// errRedirectRefused marks a redirect a checker would not follow because the
// hop would carry a credential, or what it protects, off the origin.
var errRedirectRefused = errors.New("redirect refused")

// redirectGuard keeps request credentials on the origin of the request a
// monitor was configured for. Go's client drops only Authorization/Cookie
// style headers, only when the host stops matching (a subdomain still gets
// them), and never on a scheme downgrade or a port change, so a configured
// secret header, or basic auth on https://h/ -> http://h/, would otherwise go
// out again, possibly in cleartext. It also sets Referer to the previous URL
// and, on 307/308, re-sends the body.
//
// On a hop that leaves the origin (credentialsMayFollow), guard removes
// Referer, every credential-named header (secrets.IsSensitiveHeaderName,
// which covers Authorization and so basic auth, Proxy-Authorization and
// Cookie) and every name in secretNames (the monitor's secret_headers, or a
// synthetic step's headers that reference a secret). When the request
// carried credentials, it then refuses the hop outright if the hop would
// re-send the body, or if the request URL or body itself carries a secret
// (carriesSecret): stripping headers cannot take those back. Go copies the
// first request's headers into every hop afresh, so this runs per hop, and
// each hop is compared with that first request: credentials only ever reach
// the configured origin.
type redirectGuard struct {
	secretNames   []string
	carriesSecret bool
}

func (g redirectGuard) check(req *http.Request, via []*http.Request) error {
	if len(via) == 0 || credentialsMayFollow(via[0].URL, req.URL) {
		return nil
	}
	req.Header.Del("Referer")
	for name := range req.Header {
		if secrets.IsSensitiveHeaderName(name) {
			req.Header.Del(name)
		}
	}
	for _, name := range g.secretNames {
		req.Header.Del(name)
	}

	if !g.hadCredentials(via[0]) {
		return nil
	}
	if g.carriesSecret {
		return fmt.Errorf("%w: not following the redirect to another origin (%s): the request URL or body carries a secret", errRedirectRefused, originOf(req.URL))
	}
	if req.Body != nil && req.Body != http.NoBody {
		return fmt.Errorf("%w: not following the %s redirect to another origin (%s): it would re-send the request body without the credentials it was sent with", errRedirectRefused, req.Method, originOf(req.URL))
	}
	return nil
}

// hadCredentials reports whether the configured request carried any: a
// credential-named header, a secret header, or a secret in its URL or body.
// It reads the first request, since Go may already have dropped some
// headers from the hop.
func (g redirectGuard) hadCredentials(first *http.Request) bool {
	if g.carriesSecret {
		return true
	}
	for name := range first.Header {
		if secrets.IsSensitiveHeaderName(name) {
			return true
		}
	}
	for _, name := range g.secretNames {
		if first.Header.Get(name) != "" {
			return true
		}
	}
	return false
}

// credentialsMayFollow reports whether a hop to `to` stays on the origin of
// `from`, the configured request: same host, and either the same scheme and
// port, or an http -> https upgrade on the same host (default ports 80 ->
// 443, or the same explicit port on both). A host or other port change, and
// an https -> http downgrade, leave it.
func credentialsMayFollow(from, to *url.URL) bool {
	if !strings.EqualFold(from.Hostname(), to.Hostname()) {
		return false
	}
	fromScheme, toScheme := strings.ToLower(from.Scheme), strings.ToLower(to.Scheme)
	fromPort, toPort := effectivePort(from), effectivePort(to)
	if fromScheme == toScheme {
		return fromPort == toPort
	}
	if fromScheme == "http" && toScheme == "https" {
		return (fromPort == "80" && toPort == "443") || (from.Port() != "" && from.Port() == to.Port())
	}
	return false
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

// originOf is scheme://host[:port], the part of a URL a refusal names.
func originOf(u *url.URL) string {
	return (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
}
