package mcp

// The OpenAPI 3.1 document of the REST API is a second view of the tool
// catalog: the same specs, resolved against the same router, so a route
// cannot be described in one and missing from the other. Every /api/v1 route
// is a catalog tool, a documented non-tool route (openAPIRoutes), or listed in
// notExposed — which the document repeats under x-probara-not-described.
//
// The document is committed as api/openapi.json and regenerated with
// `make openapi`; TestOpenAPIGolden fails when it is stale.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	monitorhandlers "github.com/yassinebenameur/probara/api/internal/handlers/monitors"
	apimiddleware "github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// OpenAPIPath is where the document is served, to authenticated callers.
const OpenAPIPath = apiPrefix + "/openapi.json"

// OpenAPI returns the document Bind generated (nil before Bind).
func (h *Handler) OpenAPI() []byte { return h.openapi }

// ServeOpenAPI serves the generated document. Mount it behind the API's auth
// middleware: it lists every operation, including the admin ones.
func (h *Handler) ServeOpenAPI(w http.ResponseWriter, _ *http.Request) {
	if h.openapi == nil {
		http.Error(w, "OpenAPI document not ready", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(h.openapi)
}

// operation is one described route: a catalog tool or a documented route.
type operation struct {
	spec     spec
	gate     gate
	readOnly bool
	doc      *documentedRoute // nil for catalog tools
}

func resolveDocumented(routes map[string]gate) ([]operation, error) {
	var out []operation
	for _, d := range openAPIRoutes() {
		key := routeKey(d.Method, apiPrefix+d.Route)
		g, ok := routes[key]
		if !ok {
			return nil, fmt.Errorf("openapi: route %s: no such route", key)
		}
		// A documented route is one an agent cannot call; anything else
		// belongs in the catalog, where both views get it.
		if _, ok := notExposed[key]; !ok {
			return nil, fmt.Errorf("openapi: route %s is documented outside the catalog but not listed in notExposed", key)
		}
		d := d
		out = append(out, operation{
			spec:     d.spec,
			gate:     g,
			readOnly: apimiddleware.IsReadOnlyRequest(d.Method, apiPrefix+samplePath(d.Route)),
			doc:      &d,
		})
	}
	return out, nil
}

// buildOpenAPI renders the document. Output is deterministic: every object
// is a map, so encoding/json writes keys sorted, and nothing depends on
// iteration order.
func buildOpenAPI(tools []*tool, documented []operation, version string) ([]byte, error) {
	ops := make([]operation, 0, len(tools)+len(documented))
	for _, t := range tools {
		ops = append(ops, operation{spec: t.spec, gate: t.gate, readOnly: t.readOnly})
	}
	ops = append(ops, documented...)

	b := &docBuilder{schemas: map[string]any{}, names: map[reflect.Type]string{}}
	if err := b.nameTypes(ops); err != nil {
		return nil, err
	}
	errSchema, err := b.ref(reflect.TypeFor[apierrors.ErrorResponse]())
	if err != nil {
		return nil, err
	}

	paths := map[string]map[string]any{}
	described := map[string]bool{}
	tags := map[string]bool{}
	for _, op := range ops {
		key := strings.ToLower(op.spec.Method)
		if paths[op.spec.Route] == nil {
			paths[op.spec.Route] = map[string]any{}
		}
		if _, dup := paths[op.spec.Route][key]; dup {
			return nil, fmt.Errorf("openapi: %s %s described twice", op.spec.Method, op.spec.Route)
		}
		o, err := b.operation(op)
		if err != nil {
			return nil, fmt.Errorf("openapi: %s: %w", op.spec.Name, err)
		}
		paths[op.spec.Route][key] = o
		tags[tagOf(op.spec.Route)] = true
		described[routeKey(op.spec.Method, apiPrefix+op.spec.Route)] = true
	}
	for name := range successResponses {
		found := false
		for _, op := range ops {
			found = found || op.spec.Name == name
		}
		if !found {
			return nil, fmt.Errorf("openapi: successResponses names %q, which is not a described operation", name)
		}
	}

	notDescribed := map[string]string{}
	for key, reason := range notExposed {
		if !described[key] {
			notDescribed[key] = reason
		}
	}

	tagList := make([]any, 0, len(tags))
	tagNames := make([]string, 0, len(tags))
	for t := range tags {
		tagNames = append(tagNames, t)
	}
	sort.Strings(tagNames)
	for _, t := range tagNames {
		tagList = append(tagList, map[string]any{"name": t})
	}

	doc := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       "Probara API",
			"version":     version,
			"description": docDescription,
		},
		"servers":  []any{map[string]any{"url": apiPrefix}},
		"security": []any{map[string]any{"session": []any{}}, map[string]any{"apiKey": []any{}}},
		"tags":     tagList,
		"paths":    paths,
		"components": map[string]any{
			"schemas":         b.schemas,
			"securitySchemes": securitySchemes,
			"parameters":      map[string]any{tenantArg: tenantParameter},
			"responses":       errorResponses(errSchema),
		},
		"x-probara-not-described": notDescribed,
	}
	return encodeDocument(doc)
}

// encodeDocument round-trips through a generic value so every object,
// including the reflected schemas, is written with sorted keys and without
// HTML escaping, two-space indented with a trailing newline.
func encodeDocument(doc any) ([]byte, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(generic); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

const docDescription = "Probara's REST API, generated from the same catalog as the MCP tools and checked " +
	"against the router, so every operation listed here is routed. Routes deliberately left out are listed with " +
	"the reason under x-probara-not-described; health and metrics endpoints (/healthz, /readyz, /metrics) live " +
	"outside /api/v1 and are not described.\n\n" +
	"Authenticate with a workspace API key (Authorization: Bearer pk_…) or an operator session cookie. A session " +
	"picks the workspace of a workspace-scoped operation with the tenant_id query parameter (or the X-Tenant-ID " +
	"header); an API key is bound to its workspace and ignores both.\n\n" +
	"Response bodies are described only where the handler's Go response type is known; other operations say so. " +
	"Errors usually use the JSON envelope {\"error\": code, \"message\": text}, but a few low-level paths answer " +
	"plain text, so branch on the status code first. List endpoints take 1-based page and page_size; a page_size " +
	"above the maximum is lowered to it, and a missing or invalid value takes the default."

var securitySchemes = map[string]any{
	"session": map[string]any{
		"type": "apiKey",
		"in":   "cookie",
		"name": "admin_access",
		"description": "Operator session cookie, set by the operator UI's sign-in. Select the workspace with the " +
			"tenant_id query parameter or the X-Tenant-ID header. Roles come from the user's workspace membership.",
	},
	"apiKey": map[string]any{
		"type":         "http",
		"scheme":       "bearer",
		"bearerFormat": "pk_<secret>",
		"description": "Workspace API key with read or write scope, bound to the workspace that created it. API keys " +
			"cannot call operations that need a tenant admin or superadmin session. MCP OAuth access tokens are " +
			"refused here: they are valid only on the MCP endpoint.",
	},
}

var tenantParameter = map[string]any{
	"name":     tenantArg,
	"in":       "query",
	"required": false,
	"schema":   map[string]any{"type": "string", "format": "uuid"},
	"description": "Workspace to act in, for a session (the X-Tenant-ID header does the same and wins when both " +
		"are sent). Ignored for API keys, which stay in their own workspace. A malformed value is answered 400; a " +
		"workspace the user is not a member of, 403; no workspace at all on a workspace-scoped operation, 401.",
}

func errorResponses(errSchema any) map[string]any {
	jsonError := func(description string) map[string]any {
		return map[string]any{
			"description": description,
			"content":     map[string]any{"application/json": map[string]any{"schema": errSchema}},
		}
	}
	tooMany := jsonError("Rate limited (API_RATE_LIMIT_PER_MINUTE, per credential, or per address for " +
		"anonymous calls and failed authentication). error is rate_limited; wait Retry-After seconds.")
	tooMany["headers"] = map[string]any{
		"Retry-After": map[string]any{
			"description": "Seconds until a request is accepted again.",
			"schema":      map[string]any{"type": "integer"},
		},
	}
	return map[string]any{
		"BadRequest": jsonError("Malformed tenant_id or X-Tenant-ID (error bad_request), or a request the handler " +
			"rejected (validation_error)."),
		"Unauthorized": jsonError("No valid credential (missing, expired or revoked session or API key), or a " +
			"workspace-scoped operation called without a workspace."),
		"Forbidden": jsonError("The credential may not do this: a viewer or read-scope API key on a write, a " +
			"workspace the user is not a member of, or an operation that needs a tenant admin or superadmin session."),
		"TooManyRequests": tooMany,
		"Error": map[string]any{
			"description": "Any other error: 404 not_found, 409 on a conflicting change, 500 internal_error, 503 " +
				"when a dependency (NATS, local AI) is unavailable, and others. Most answers use the JSON envelope; " +
				"a few low-level paths answer plain text.",
			"content": map[string]any{
				"application/json": map[string]any{"schema": errSchema},
				"text/plain":       map[string]any{"schema": map[string]any{"type": "string"}},
			},
		},
	}
}

// tagOf groups operations by resource: the route's first segment.
func tagOf(route string) string {
	seg := strings.SplitN(strings.TrimPrefix(route, "/"), "/", 2)[0]
	return strings.TrimSuffix(seg, ".json")
}

type docBuilder struct {
	schemas map[string]any
	names   map[reflect.Type]string
}

// componentType is the named struct a response type is shown as a
// component of, or nil when it is shown inline.
func componentType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		return componentType(t.Elem())
	}
	if t.Kind() == reflect.Struct && t.Name() != "" {
		return t
	}
	return nil
}

// shortName is a component's name: the bare type name for the models
// packages and this package's response shapes, package.Type otherwise
// (audit.ListResponse reads better than ListResponse).
func shortName(t reflect.Type) string {
	pkg := t.PkgPath()
	base := pkg[strings.LastIndex(pkg, "/")+1:]
	if base == "models" || pkg == reflect.TypeFor[successResponse]().PkgPath() {
		return t.Name()
	}
	return base + "." + t.Name()
}

// nameTypes assigns component names: shortName, or the module-relative
// package path when two types share it, so names never depend on which
// types happen to be reflected first.
func (b *docBuilder) nameTypes(ops []operation) error {
	types := []reflect.Type{reflect.TypeFor[apierrors.ErrorResponse]()}
	for _, op := range ops {
		if r, ok := successResponses[op.spec.Name]; ok && r.Body != nil {
			if c := componentType(r.Body); c != nil {
				types = append(types, c)
			}
		}
	}
	byName := map[string][]reflect.Type{}
	for _, t := range types {
		if _, seen := b.names[t]; seen {
			continue
		}
		b.names[t] = ""
		byName[shortName(t)] = append(byName[shortName(t)], t)
	}
	for name, ts := range byName {
		for _, t := range ts {
			if len(ts) == 1 {
				b.names[t] = name
				continue
			}
			path := t.PkgPath()
			if i := strings.Index(path, "/probara/"); i >= 0 {
				path = path[i+len("/probara/"):]
			}
			b.names[t] = strings.ReplaceAll(path, "/", ".") + "." + t.Name()
		}
	}
	return nil
}

// ref returns the schema of a response body type, registering named structs
// as components.
func (b *docBuilder) ref(t reflect.Type) (any, error) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() == reflect.Slice {
		items, err := b.ref(t.Elem())
		if err != nil {
			return nil, err
		}
		// A nil slice encodes as null.
		return map[string]any{"type": []any{"array", "null"}, "items": items}, nil
	}
	name, ok := b.names[t]
	if !ok {
		s, err := responseSchema(t)
		if err != nil {
			return nil, err
		}
		return s, nil
	}
	if _, done := b.schemas[name]; !done {
		s, err := responseSchema(t)
		if err != nil {
			return nil, err
		}
		b.schemas[name] = s
	}
	return map[string]any{"$ref": "#/components/schemas/" + name}, nil
}

// responseSchema reflects a response type. Unlike request bodies, required
// lists stay: encoding/json always writes a field without omitempty. Closed
// objects (additionalProperties false) are opened, so adding a response
// field is never a breaking change for a client that validates.
func responseSchema(t reflect.Type) (*jsonschema.Schema, error) {
	if err := checkReflectable(t, map[reflect.Type]bool{}); err != nil {
		return nil, err
	}
	s, err := jsonschema.ForType(t, responseOptions)
	if err != nil {
		return nil, err
	}
	openObjects(s)
	if t == reflect.TypeFor[models.Monitor]() {
		addSecretFields(s)
	}
	return s, nil
}

var (
	jsonMarshaler = reflect.TypeFor[json.Marshaler]()
	textMarshaler = reflect.TypeFor[interface{ MarshalText() ([]byte, error) }]()
)

// responseOptions are reflectOptions without IgnoreInvalidTypes: a response
// field reflection cannot describe fails generation instead of silently
// vanishing from the schema.
var responseOptions = func() *jsonschema.ForOptions {
	o := *reflectOptions
	o.IgnoreInvalidTypes = false
	return &o
}()

// checkReflectable refuses a response type that marshals itself (unless
// reflectOptions maps it): reflection would describe its Go fields, not
// what it writes. Types reflection cannot handle at all fail in ForType.
func checkReflectable(t reflect.Type, seen map[reflect.Type]bool) error {
	if seen[t] {
		return nil
	}
	seen[t] = true
	if t.Kind() == reflect.Pointer {
		return checkReflectable(t.Elem(), seen)
	}
	if _, mapped := reflectOptions.TypeSchemas[t]; mapped {
		return nil
	}
	if t.Implements(jsonMarshaler) || reflect.PointerTo(t).Implements(jsonMarshaler) ||
		(t.Kind() != reflect.String && (t.Implements(textMarshaler) || reflect.PointerTo(t).Implements(textMarshaler))) {
		return fmt.Errorf("type %s marshals itself; add its wire schema to reflectOptions", t)
	}
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return checkReflectable(t.Elem(), seen)
	case reflect.Map:
		return checkReflectable(t.Elem(), seen)
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() && !f.Anonymous {
				continue
			}
			if tag := f.Tag.Get("json"); tag == "-" {
				continue
			}
			if err := checkReflectable(f.Type, seen); err != nil {
				return fmt.Errorf("%s.%s: %w", t, f.Name, err)
			}
		}
	}
	return nil
}

func isFalseSchema(s *jsonschema.Schema) bool {
	if s == nil {
		return false
	}
	raw, err := json.Marshal(s)
	return err == nil && string(raw) == "false"
}

func openObjects(s *jsonschema.Schema) {
	if s == nil {
		return
	}
	if isFalseSchema(s.AdditionalProperties) {
		s.AdditionalProperties = nil
	}
	for _, p := range s.Properties {
		openObjects(p)
	}
	openObjects(s.Items)
	openObjects(s.AdditionalProperties)
	for _, sub := range s.AnyOf {
		openObjects(sub)
	}
	for _, sub := range s.OneOf {
		openObjects(sub)
	}
	for _, sub := range s.Defs {
		openObjects(sub)
	}
}

// monitorConfigBodies are the request bodies whose config is a monitor
// config, annotated with the secret registry like the Monitor response.
var monitorConfigBodies = map[reflect.Type]bool{
	reflect.TypeFor[models.CreateMonitorRequest]():              true,
	reflect.TypeFor[models.UpdateMonitorRequest]():              true,
	reflect.TypeFor[monitorhandlers.TestMonitorConfigRequest](): true,
}

// addSecretFields lists, on a monitor config property, the secret fields of
// each monitor type (shared/secrets/monitor_config.go). The config schema
// itself depends on the type, so the fields cannot be marked writeOnly in
// place; describe_monitor_type gives the full per-type schema.
func addSecretFields(s *jsonschema.Schema) {
	prop := s.Properties["config"]
	if prop == nil {
		return
	}
	byType := map[string][]string{}
	for typ, names := range secrets.MonitorSecretFields {
		byType[typ] = append(byType[typ], names...)
	}
	for typ, names := range secrets.MonitorSecretMapFields {
		byType[typ] = append(byType[typ], names...)
	}
	fields := map[string]any{}
	for typ, names := range byType {
		list := append([]string(nil), names...)
		sort.Strings(list)
		fields[typ] = list
	}
	if prop.Extra == nil {
		prop.Extra = map[string]any{}
	}
	prop.Extra["x-probara-secret-fields"] = fields
	note := "Secret fields, by monitor type, are listed in x-probara-secret-fields; they are encrypted at rest " +
		"and read back as \"***\"."
	if prop.Description == "" {
		prop.Description = note
	} else if !strings.Contains(prop.Description, "x-probara-secret-fields") {
		prop.Description += " " + note
	}
}

func (b *docBuilder) operation(op operation) (map[string]any, error) {
	s := op.spec
	public := op.doc != nil && op.doc.Public
	o := map[string]any{
		"operationId": s.Name,
		"summary":     s.Title,
		"description": s.Description + accessNote(op),
		"tags":        []any{tagOf(s.Route)},
	}
	if public {
		o["security"] = []any{}
	} else if op.gate != gateNone || s.SessionOnly {
		o["security"] = []any{map[string]any{"session": []any{}}}
	}

	var params []any
	for _, name := range pathParams(s.Route) {
		desc := s.PathDocs[name]
		if desc == "" {
			desc = defaultPathDoc(name)
		}
		params = append(params, map[string]any{
			"name": name, "in": "path", "required": true, "description": desc,
			"schema": map[string]any{"type": "string"},
		})
	}
	for _, q := range s.Query {
		params = append(params, queryParameter("query", q))
	}
	if op.doc != nil {
		for _, h := range op.doc.Headers {
			params = append(params, queryParameter("header", h))
		}
	}
	if !s.Platform && !public {
		params = append(params, map[string]any{"$ref": "#/components/parameters/" + tenantArg})
	}
	if len(params) > 0 {
		o["parameters"] = params
	}

	body, err := requestBody(op)
	if err != nil {
		return nil, err
	}
	if body != nil {
		o["requestBody"] = body
	}

	responses, err := b.responses(op, public)
	if err != nil {
		return nil, err
	}
	o["responses"] = responses
	return o, nil
}

func accessNote(op operation) string {
	var notes []string
	switch op.gate {
	case gateSuperadmin:
		notes = append(notes, "Needs a superadmin session; API keys are refused.")
	case gateTenantAdmin:
		notes = append(notes, "Needs a tenant admin (or superadmin) session; API keys are refused.")
	default:
		if op.spec.SessionOnly {
			notes = append(notes, "Needs a signed-in session; API keys are refused (403).")
		}
	}
	switch {
	case !op.readOnly:
		notes = append(notes, "Needs write access: viewers and read-scope API keys get 403.")
	case op.spec.NeedsWrite:
		notes = append(notes, "Needs write access because the answer reveals a credential: viewers and read-scope API keys get 403.")
	}
	if op.doc != nil && op.doc.Public {
		notes = nil
	}
	if len(notes) == 0 {
		return ""
	}
	return "\n\nAccess: " + strings.Join(notes, " ")
}

func queryParameter(in string, p param) map[string]any {
	schema := p.schema()
	schema.Description = "" // carried once, on the parameter
	out := map[string]any{
		"name":   p.Name,
		"in":     in,
		"schema": schema,
	}
	if p.Description != "" {
		out["description"] = p.Description
	}
	if p.Required {
		out["required"] = true
	}
	return out
}

func requestBody(op operation) (map[string]any, error) {
	s := op.spec
	content := map[string]any{}
	required := true
	switch {
	case s.Multipart != nil:
		content["multipart/form-data"] = map[string]any{"schema": map[string]any{
			"type":     "object",
			"required": []any{"file"},
			"properties": map[string]any{"file": map[string]any{
				"type": "string",
				"description": "The file to read. Its extension (.json, .yaml, .yml, .csv) selects the parser; " +
					"without one the format is detected from the content.",
			}},
		}}
	case s.BodyArg != nil:
		schema := s.BodyArg.schema()
		content["application/json"] = map[string]any{"schema": schema}
		required = s.BodyArg.Required
	case s.Body != nil:
		schema, err := bodySchema(s.Body)
		if err != nil {
			return nil, err
		}
		for name, prop := range schema.Properties {
			if doc, ok := s.FieldDocs[name]; ok {
				prop.Description = doc
			}
			if values, ok := s.Enums[name]; ok {
				setEnum(prop, values)
			}
		}
		schema.Required = append([]string(nil), s.Required...)
		if monitorConfigBodies[s.Body] {
			addSecretFields(schema)
		}
		content["application/json"] = map[string]any{"schema": schema}
	}
	if op.doc != nil {
		for _, media := range op.doc.RequestMedia {
			content[media] = map[string]any{}
		}
		if op.doc.RequestOptional {
			required = false
		}
	}
	if len(content) == 0 {
		return nil, nil
	}
	return map[string]any{"required": required, "content": content}, nil
}

func (b *docBuilder) responses(op operation, public bool) (map[string]any, error) {
	out := map[string]any{}
	success, typed := successResponses[op.spec.Name]
	if typed {
		r := map[string]any{"description": success.description()}
		content := map[string]any{}
		if success.Body != nil {
			schema, err := b.ref(success.Body)
			if err != nil {
				return nil, err
			}
			content["application/json"] = map[string]any{"schema": schema}
		}
		for _, media := range success.Media {
			content[media] = map[string]any{}
		}
		if len(content) > 0 {
			r["content"] = content
		}
		out[strconv.Itoa(success.Status)] = r
	} else {
		out["2XX"] = map[string]any{"description": "Success. The response body is not described in this document."}
	}
	refTo := func(name string) map[string]any {
		return map[string]any{"$ref": "#/components/responses/" + name}
	}
	if op.doc != nil {
		for status, description := range op.doc.Errors {
			out[strconv.Itoa(status)] = map[string]any{
				"description": description,
				"content":     map[string]any{op.doc.ErrorMedia: map[string]any{}},
			}
		}
	}
	// A documented route's own error answers win over the shared ones.
	setDefault := func(status, name string) {
		if _, ok := out[status]; !ok {
			out[status] = refTo(name)
		}
	}
	if !public {
		setDefault("400", "BadRequest")
		setDefault("401", "Unauthorized")
		setDefault("403", "Forbidden")
	}
	setDefault("429", "TooManyRequests")
	out["default"] = refTo("Error")
	return out, nil
}
