// Package mcp serves Probara over the Model Context Protocol (Streamable
// HTTP, stateless) at /api/v1/mcp.
//
// It is a protocol adapter, never a second implementation: every catalog
// tool stands for one REST route and runs by dispatching that route's
// request through the root router in process, carrying the caller's bearer
// credential. Auth, the audit trail, the write gate, tenant-admin and
// superadmin gates, validation and secret masking all apply exactly as for a
// direct API call. Do not call services from this package.
package mcp

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"runtime/debug"
	"sort"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	apimiddleware "github.com/yassinebenameur/probara/api/internal/middleware"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

const apiPrefix = "/api/v1"

// Path is where the MCP endpoint is mounted.
const Path = apiPrefix + "/mcp"

// gate is the role middleware a route sits behind, read off the router.
type gate int

const (
	gateNone gate = iota
	gateTenantAdmin
	gateSuperadmin
)

// spec is one catalog entry: a REST route described for an agent.
type spec struct {
	Name        string
	Title       string
	Description string
	Method      string
	// Route is the chi pattern below /api/v1, e.g. "/monitors/{id}".
	Route    string
	PathDocs map[string]string
	Query    []param
	// Body is the request type the handler decodes; its fields become
	// top-level tool arguments.
	Body reflect.Type
	// BodyArg passes a non-object or free-form body through one argument.
	BodyArg *param
	// Multipart uploads the FileArg argument's text as the form file `file`.
	Multipart *multipart
	// Required names body fields the agent must send (reflection cannot
	// tell; the API's validation remains the authority).
	Required []string
	// FieldDocs overrides reflected body field descriptions.
	FieldDocs map[string]string
	// Enums lists the allowed values of named-string body fields, which
	// reflection only sees as "string".
	Enums map[string][]string
	// Destructive marks deletes and other irreversible changes.
	Destructive bool
	// NeedsWrite marks GETs whose handler demands write access because they
	// reveal a secret (push token, worker credential). Hidden from read-only
	// sessions like a mutation; still read-only in effect.
	NeedsWrite bool
	// Platform marks routes that do not act in a tenant (users, OIDC
	// mappings, the tenant list): no tenant_id argument.
	Platform bool
}

type multipart struct {
	ContentArg  string
	FilenameArg string
}

type param struct {
	Name        string
	Type        string // string (default), integer, number, boolean, array, object
	Description string
	Enum        []string
	Required    bool
	Default     any
}

// tool is a spec resolved against the live router.
type tool struct {
	spec       spec
	pathParams []string
	argSet     map[string]struct{}
	bodyFields map[string]struct{}
	readOnly   bool
	gate       gate
	def        *mcp.Tool
}

// extraTool is a tool without a 1:1 route (schema discovery, waiting for
// alert events). Its handler must still reach data only through the router
// or through compile-time registries, never through services.
type extraTool struct {
	def      *mcp.Tool
	readOnly bool
	handler  mcp.ToolHandler
}

// capability is what a caller may see. Filtering tools/list by it is UX
// only: the inner dispatch enforces access whatever is listed.
type capability struct {
	apiKey     bool
	write      bool
	superadmin bool
}

// Handler serves the MCP endpoint.
type Handler struct {
	log     *logger.Logger
	version string
	router  http.Handler
	tools   []*tool
	extras  []extraTool
	stream  http.Handler

	// resourceMetadata returns the RFC 9728 metadata URL advertised on a
	// 401, which is how MCP clients discover OAuth sign-in.
	resourceMetadata func(*http.Request) string

	mu      sync.Mutex
	servers map[capability]*mcp.Server
}

// NewHandler creates the MCP handler. Bind must be called with the finished
// router before it serves.
func NewHandler(log *logger.Logger, resourceMetadata func(*http.Request) string) *Handler {
	return &Handler{
		log:              log,
		version:          buildVersion(),
		resourceMetadata: resourceMetadata,
		servers:          map[capability]*mcp.Server{},
	}
}

// Bind resolves the catalog against the router: each tool's route must exist
// (an unknown route is a startup error, not a tool that always 404s), its
// read-only status comes from the write gate's own predicate, and its role
// gate from the middleware chain chi reports for the route.
func (h *Handler) Bind(router chi.Router, extras []extraTool) error {
	routes, err := routeGates(router)
	if err != nil {
		return err
	}
	tools, err := resolveCatalog(catalog(), routes)
	if err != nil {
		return err
	}
	if err := checkCoverage(routes, tools); err != nil {
		return err
	}
	h.router = router
	h.tools = tools
	h.extras = extras
	h.stream = mcp.NewStreamableHTTPHandler(h.serverFor, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
		// The endpoint authenticates every request with a bearer credential
		// and never reads cookies, so a DNS-rebound or cross-site page gains
		// nothing; the SDK's localhost check would only 403 legitimate
		// traffic arriving through a sidecar or a local reverse proxy.
		DisableLocalhostProtection: true,
	})
	return nil
}

func resolveCatalog(specs []spec, routes map[string]gate) ([]*tool, error) {
	seen := map[string]struct{}{}
	tools := make([]*tool, 0, len(specs))
	for _, s := range specs {
		if _, dup := seen[s.Name]; dup {
			return nil, fmt.Errorf("mcp: duplicate tool name %q", s.Name)
		}
		seen[s.Name] = struct{}{}
		g, ok := routes[routeKey(s.Method, apiPrefix+s.Route)]
		if !ok {
			return nil, fmt.Errorf("mcp: tool %s: no route %s %s", s.Name, s.Method, apiPrefix+s.Route)
		}
		t := &tool{
			spec:       s,
			pathParams: pathParams(s.Route),
			gate:       g,
			readOnly:   apimiddleware.IsReadOnlyRequest(s.Method, apiPrefix+samplePath(s.Route)),
		}
		schema, err := t.inputSchema(!s.Platform)
		if err != nil {
			return nil, fmt.Errorf("mcp: %w", err)
		}
		destructive := s.Destructive
		t.def = &mcp.Tool{
			Name:        s.Name,
			Title:       s.Title,
			Description: s.Description,
			InputSchema: schema,
			Annotations: &mcp.ToolAnnotations{
				Title:           s.Title,
				ReadOnlyHint:    t.readOnly,
				DestructiveHint: &destructive,
				IdempotentHint:  t.readOnly || s.Method == http.MethodPut || s.Method == http.MethodDelete,
			},
		}
		tools = append(tools, t)
	}
	return tools, nil
}

// routeGates walks the router and records, per method+pattern, the role
// gate in that route's middleware chain.
func routeGates(router chi.Routes) (map[string]gate, error) {
	tenantAdmin := reflect.ValueOf(apimiddleware.RequireTenantAdmin).Pointer()
	superadmin := reflect.ValueOf(apimiddleware.RequireSuperadmin).Pointer()
	out := map[string]gate{}
	err := chi.Walk(router, func(method, route string, _ http.Handler, mws ...func(http.Handler) http.Handler) error {
		g := gateNone
		for _, mw := range mws {
			switch reflect.ValueOf(mw).Pointer() {
			case superadmin:
				g = gateSuperadmin
			case tenantAdmin:
				if g == gateNone {
					g = gateTenantAdmin
				}
			}
		}
		out[routeKey(method, route)] = g
		return nil
	})
	return out, err
}

// routeKey normalizes a chi pattern: a nested Route("/") group shows up as
// "/*/" or "//", and subrouter roots carry a trailing slash.
func routeKey(method, pattern string) string {
	for strings.Contains(pattern, "/*/") {
		pattern = strings.ReplaceAll(pattern, "/*/", "/")
	}
	for strings.Contains(pattern, "//") {
		pattern = strings.ReplaceAll(pattern, "//", "/")
	}
	if len(pattern) > 1 {
		pattern = strings.TrimSuffix(pattern, "/")
	}
	return method + " " + pattern
}

func pathParams(route string) []string {
	var out []string
	for _, seg := range strings.Split(route, "/") {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			out = append(out, strings.TrimSuffix(strings.TrimPrefix(seg, "{"), "}"))
		}
	}
	return out
}

// samplePath fills path parameters so the write-gate predicate (which
// matches concrete paths) can classify the route.
func samplePath(route string) string {
	parts := strings.Split(route, "/")
	for i, seg := range parts {
		if strings.HasPrefix(seg, "{") {
			parts[i] = "00000000-0000-0000-0000-000000000000"
		}
	}
	return strings.Join(parts, "/")
}

// ServeHTTP serves an authenticated MCP request. Use Protect to put it
// behind authentication.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.stream == nil {
		http.Error(w, "MCP endpoint not ready", http.StatusServiceUnavailable)
		return
	}
	h.stream.ServeHTTP(w, r)
}

// Protect returns the MCP endpoint behind auth. The session cookie is
// stripped first, so only a bearer credential (tenant API key or MCP OAuth
// token) can authenticate: a browser page can never drive MCP with an
// operator's cookie. A 401 advertises the OAuth protected-resource metadata
// so MCP clients can start sign-in.
func (h *Handler) Protect(auth func(http.Handler) http.Handler) http.Handler {
	authed := auth(h)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.Clone(r.Context())
		r.Header.Del("Cookie")
		if r.Header.Get("X-Forwarded-For") == "" {
			r.Header.Set("X-Forwarded-For", clientIP(r.RemoteAddr))
		}
		var challenge string
		if h.resourceMetadata != nil {
			challenge = fmt.Sprintf(`Bearer resource_metadata=%q`, h.resourceMetadata(r))
		}
		authed.ServeHTTP(&challengeWriter{ResponseWriter: w, challenge: challenge}, r)
	})
}

type challengeWriter struct {
	http.ResponseWriter
	challenge string
}

func (c *challengeWriter) WriteHeader(code int) {
	if code == http.StatusUnauthorized && c.challenge != "" {
		c.Header().Set("WWW-Authenticate", c.challenge)
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *challengeWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (c *challengeWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

func capabilityOf(ctx context.Context) capability {
	actor, _ := ctxpkg.GetActorType(ctx)
	return capability{
		apiKey:     actor == ctxpkg.ActorTypeAPIKey,
		write:      ctxpkg.CanWrite(ctx),
		superadmin: ctxpkg.IsSuperadmin(ctx),
	}
}

// serverFor returns the (cached) server whose tool list fits the caller.
func (h *Handler) serverFor(r *http.Request) *mcp.Server {
	c := capabilityOf(r.Context())
	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok := h.servers[c]; ok {
		return s
	}
	s := h.buildServer(c)
	h.servers[c] = s
	return s
}

func (h *Handler) buildServer(c capability) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "probara",
		Title:   "Probara",
		Version: h.version,
	}, &mcp.ServerOptions{Instructions: instructions(c)})

	for _, t := range h.visibleTools(c) {
		t := t
		s.AddTool(t.def, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return h.call(ctx, t, req)
		})
	}
	for _, e := range h.extras {
		if !e.readOnly && !c.write {
			continue
		}
		s.AddTool(e.def, e.handler)
	}
	registerResources(s)
	registerPrompts(s, c)
	return s
}

func (h *Handler) visibleTools(c capability) []*tool {
	var out []*tool
	for _, t := range h.tools {
		switch {
		case t.gate == gateSuperadmin && !c.superadmin:
			continue
		case t.gate == gateTenantAdmin && c.apiKey:
			continue
		case (!t.readOnly || t.spec.NeedsWrite) && !c.write:
			continue
		}
		out = append(out, t)
	}
	return out
}

// ToolNames lists every catalog tool, for docs and tests.
func (h *Handler) ToolNames() []string {
	names := make([]string, 0, len(h.tools)+len(h.extras))
	for _, t := range h.tools {
		names = append(names, t.spec.Name)
	}
	for _, e := range h.extras {
		names = append(names, e.def.Name)
	}
	sort.Strings(names)
	return names
}

// buildVersion reports the module version the binary was built at, for the
// MCP server's implementation info.
func buildVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
