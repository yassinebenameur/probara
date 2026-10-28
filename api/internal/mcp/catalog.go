package mcp

import (
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"

	"github.com/yassinebenameur/probara/api/internal/validation"
)

// catalog is every REST route an agent can call, one tool per route. A new
// route needs an entry here or a reason in notExposed; the router coverage
// test fails otherwise.
func catalog() []spec {
	var all []spec
	for _, group := range [][]spec{
		monitorTools(),
		observabilityTools(),
		locationTools(),
		alertTools(),
		incidentTools(),
		notificationTools(),
		slaTools(),
		statusPageTools(),
		workspaceTools(),
		platformTools(),
	} {
		all = append(all, group...)
	}
	return all
}

// notExposed lists /api/v1 routes deliberately left out, with the reason.
// Keys are routeKey(method, pattern); method "*" covers every method.
var notExposed = map[string]string{
	"POST /api/v1/auth/login":            "browser session flow; MCP authenticates with its own bearer credential",
	"POST /api/v1/auth/refresh":          "browser session flow",
	"POST /api/v1/auth/logout":           "browser session flow",
	"GET /api/v1/auth/me":                "cookie-session identity; whoami covers it",
	"GET /api/v1/auth/oidc/status":       "browser SSO flow",
	"GET /api/v1/auth/oidc/start":        "browser SSO flow",
	"GET /api/v1/auth/oidc/callback":     "browser SSO flow",
	"GET /api/v1/users/bootstrap/status": "first-run setup, unauthenticated",
	"POST /api/v1/users/bootstrap/first": "first-run setup, unauthenticated",
	"GET /api/v1/push/{token}":           "heartbeat ingest from the monitored job, authenticated by the token in the URL",
	"POST /api/v1/push/{token}":          "heartbeat ingest from the monitored job",
	"POST /api/v1/agent/metrics":         "retired (410 Gone); collectors push to /otlp/v1/metrics",
	"POST /api/v1/otlp/v1/metrics":       "OTLP ingest from collectors, not an operator action",
	"GET /api/v1/alerts/stream":          "server-sent events; wait_for_alert_events reads it for a bounded window",
	"POST /api/v1/alert-policies":        "retired (410 Gone); notification settings replaced alert policies",
	"GET /api/v1/alert-policies":         "retired (410 Gone)",
	"GET /api/v1/alert-policies/{id}":    "retired (410 Gone)",
	"PATCH /api/v1/alert-policies/{id}":  "retired (410 Gone)",
	"DELETE /api/v1/alert-policies/{id}": "retired (410 Gone)",
	"* /api/v1/mcp":                      "the MCP endpoint itself",

	"GET /api/v1/oauth/.well-known/oauth-authorization-server": "OAuth protocol endpoint for MCP clients",
	"GET /api/v1/oauth/.well-known/openid-configuration":       "OAuth protocol endpoint for MCP clients",
	"POST /api/v1/oauth/register":                              "OAuth protocol endpoint for MCP clients",
	"GET /api/v1/oauth/authorize":                              "OAuth protocol endpoint for MCP clients",
	"POST /api/v1/oauth/token":                                 "OAuth protocol endpoint for MCP clients",
	"POST /api/v1/oauth/revoke":                                "OAuth protocol endpoint for MCP clients",
	"GET /api/v1/oauth/authorize/request":                      "consent screen data, signed-in user sessions only",
	"POST /api/v1/oauth/authorize/decision":                    "consent decision, signed-in user sessions only",
	"GET /api/v1/oauth/grants":                                 "MCP connection management, signed-in user sessions only",
	"DELETE /api/v1/oauth/grants/{id}":                         "MCP connection management, signed-in user sessions only",
}

// checkCoverage fails when an /api/v1 route is neither a tool nor listed in
// notExposed, or when notExposed names a route that no longer exists.
func checkCoverage(routes map[string]gate, tools []*tool) error {
	covered := map[string]bool{}
	for _, t := range tools {
		covered[routeKey(t.spec.Method, apiPrefix+t.spec.Route)] = true
	}
	seenExclusion := map[string]bool{}
	var missing []string
	for key := range routes {
		method, pattern, _ := strings.Cut(key, " ")
		if !strings.HasPrefix(pattern, apiPrefix+"/") || method == http.MethodOptions || method == http.MethodHead {
			continue
		}
		if covered[key] {
			continue
		}
		if _, ok := notExposed[key]; ok {
			seenExclusion[key] = true
			continue
		}
		if _, ok := notExposed["* "+pattern]; ok {
			seenExclusion["* "+pattern] = true
			continue
		}
		missing = append(missing, key)
	}
	var stale []string
	for key := range notExposed {
		if !seenExclusion[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	var problems []string
	if len(missing) > 0 {
		problems = append(problems, "routes with no MCP tool (add one to the catalog, or a reason to notExposed): "+strings.Join(missing, ", "))
	}
	if len(stale) > 0 {
		problems = append(problems, "notExposed entries for routes that do not exist: "+strings.Join(stale, ", "))
	}
	if len(problems) > 0 {
		return fmt.Errorf("mcp catalog coverage: %s", strings.Join(problems, "; "))
	}
	return nil
}

func typeOf[T any]() reflect.Type { return reflect.TypeFor[T]() }

// monitorTypes is the registry's type list, the single source of monitor
// types, sorted for a stable schema.
func monitorTypes() []string {
	var out []string
	for _, t := range validation.DefaultRegistry.Types() {
		out = append(out, string(t))
	}
	sort.Strings(out)
	return out
}

var pageParams = []param{
	{Name: "page", Type: "integer", Description: "1-based page number (default 1)."},
	{Name: "page_size", Type: "integer", Description: "Items per page, 1-100 (default 20)."},
}

func withPaging(params ...param) []param {
	return append(params, pageParams...)
}

var analyticsRange = param{
	Name:        "range",
	Description: "Time window (default 24h).",
	Enum:        []string{"1h", "6h", "24h", "7d", "30d", "90d", "365d"},
}

var dashboardRange = param{
	Name:        "range",
	Description: "Time window (default 24h).",
	Enum:        []string{"1h", "24h", "7d", "30d", "90d", "365d"},
}

var backendURLParam = param{
	Name:        "backend_url",
	Description: "Override the API base URL baked into the output (defaults to the configured public URL).",
}
