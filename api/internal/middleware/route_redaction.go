package middleware

import (
	"net/http"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
)

// Some public routes carry their credential in the path: push heartbeats
// (/push/{token}), signed alert actions (/alert-actions/{token}), on-call
// calendar feeds (/oncall-feeds/{token}.ics). Request logs and metric labels
// must never hold that value — anyone reading them could replay it — so they
// use the two helpers below instead of r.URL.Path. Redaction is keyed on the
// route parameter's NAME (credentialParam), so a new token route is covered
// as soon as it names its parameter {token}/{…token}/{…secret}.

// unmatchedRoute is the metric label for a request no route pattern matched.
const unmatchedRoute = "unmatched"

// credentialParam reports whether a chi route parameter carries a credential.
func credentialParam(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "token") || strings.Contains(n, "secret")
}

// routeLabel is the bounded, credential-free route label for metrics: the
// matched chi pattern ("/api/v1/alert-actions/{token}"), or the deepest
// mount that matched for a 404/405 ("/api/v1/*"). Call it after the handler
// ran — chi fills the pattern while routing.
func routeLabel(r *http.Request) string {
	if p := chi.RouteContext(r.Context()).RoutePattern(); p != "" {
		return p
	}
	return unmatchedRoute
}

// pathRedactor rewrites request paths so that every segment a credential
// parameter would capture reads as its placeholder ("{token}").
//
// It matches against the router's registered patterns, not the request's
// own route context, because chi records URL params only for a request that
// reaches a handler: a HEAD from a mail link scanner (405) or a mangled link
// (404) on /alert-actions/<token> would otherwise log the token verbatim.
// The templates come from chi.Walk over the root router, on first use, when
// every route is registered.
type pathRedactor struct {
	once      sync.Once
	templates [][]string // segment templates, each ending at its last credential segment
}

// redact returns r.URL.Path with credential segments replaced. Call it from
// a middleware on the root router (chi.RouteContext(...).Routes is the
// router to walk).
func (p *pathRedactor) redact(r *http.Request) string {
	path := r.URL.Path
	rctx := chi.RouteContext(r.Context())
	if rctx == nil || rctx.Routes == nil {
		return path
	}
	p.once.Do(func() { p.templates = credentialTemplates(rctx.Routes) })
	return redactPath(path, p.templates)
}

// credentialTemplates walks routes and keeps, for every pattern with a
// credential parameter, its segments up to the last such parameter.
func credentialTemplates(routes chi.Routes) [][]string {
	seen := map[string]bool{}
	var out [][]string
	_ = chi.Walk(routes, func(_ string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		segs := strings.Split(walkPattern(route), "/")
		last := -1
		for i, s := range segs {
			if segmentHasCredential(s) {
				last = i
			}
		}
		if last < 0 {
			return nil
		}
		tmpl := segs[:last+1]
		key := strings.Join(tmpl, "/")
		if !seen[key] {
			seen[key] = true
			out = append(out, tmpl)
		}
		return nil
	})
	return out
}

// walkPattern drops the "/*" mount segments chi.Walk keeps for sub-routers.
func walkPattern(p string) string {
	for strings.Contains(p, "/*/") {
		p = strings.ReplaceAll(p, "/*/", "/")
	}
	return p
}

// segmentParams returns the parameter names in one pattern segment, e.g.
// "{token}.ics" → [token], "{id:[0-9]+}" → [id].
func segmentParams(seg string) []string {
	var names []string
	for {
		start := strings.IndexByte(seg, '{')
		if start < 0 {
			return names
		}
		end := strings.IndexByte(seg[start:], '}')
		if end < 0 {
			return names
		}
		name := seg[start+1 : start+end]
		if i := strings.IndexByte(name, ':'); i >= 0 {
			name = name[:i]
		}
		names = append(names, name)
		seg = seg[start+end+1:]
	}
}

func segmentHasCredential(seg string) bool {
	for _, name := range segmentParams(seg) {
		if credentialParam(name) {
			return true
		}
	}
	return false
}

// redactPath replaces, for every template whose literal segments match the
// path's leading segments, each credential segment with the template's
// spelling of it. Parameter segments match any non-empty segment, so a
// malformed value (wrong suffix, extra segments after it) is still redacted:
// over-redacting a path that would 404 anyway costs nothing.
func redactPath(path string, templates [][]string) string {
	if len(templates) == 0 {
		return path
	}
	segs := strings.Split(path, "/")
	changed := false
	for _, tmpl := range templates {
		if len(segs) < len(tmpl) {
			continue
		}
		match := true
		for i, t := range tmpl {
			if len(segmentParams(t)) > 0 {
				if segs[i] == "" {
					match = false
					break
				}
				continue
			}
			if segs[i] != t {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		for i, t := range tmpl {
			if segmentHasCredential(t) && segs[i] != t {
				segs[i] = t
				changed = true
			}
		}
	}
	if !changed {
		return path
	}
	return strings.Join(segs, "/")
}
