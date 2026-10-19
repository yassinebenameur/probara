package mcp

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
)

// tenantArg is the optional argument OAuth (user) sessions use to pick the
// tenant of a tenant-scoped call. API keys are bound to one tenant and the
// API ignores the header for them.
const tenantArg = "tenant_id"

// reflectOptions teaches schema inference the wire shape of the types the
// request structs use that reflection would otherwise get wrong (uuid.UUID
// is a byte array; time.Time and json.RawMessage marshal themselves).
var reflectOptions = &jsonschema.ForOptions{
	IgnoreInvalidTypes: true,
	TypeSchemas: map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[uuid.UUID]():       {Type: "string", Format: "uuid"},
		reflect.TypeFor[time.Time]():       {Type: "string", Format: "date-time"},
		reflect.TypeFor[time.Duration]():   {Type: "integer", Description: "nanoseconds"},
		reflect.TypeFor[json.RawMessage](): {},
		reflect.TypeFor[any]():             {},
	},
}

// bodySchema infers the JSON schema of a request body type. Inference marks
// every field without omitempty as required, which says nothing about what
// the handler actually demands (PATCH bodies, defaults), so required lists
// are dropped: the API's own validation answers with a precise message.
func bodySchema(t reflect.Type) (*jsonschema.Schema, error) {
	s, err := jsonschema.ForType(t, reflectOptions)
	if err != nil {
		return nil, err
	}
	stripRequired(s)
	return s, nil
}

func stripRequired(s *jsonschema.Schema) {
	if s == nil {
		return
	}
	s.Required = nil
	for _, p := range s.Properties {
		stripRequired(p)
	}
	stripRequired(s.Items)
	stripRequired(s.AdditionalProperties)
	for _, sub := range s.AnyOf {
		stripRequired(sub)
	}
	for _, sub := range s.OneOf {
		stripRequired(sub)
	}
	for _, sub := range s.Defs {
		stripRequired(sub)
	}
}

// inputSchema builds a tool's flat argument schema: path params, query
// params, tenant_id (when tenant-scoped) and the body's fields side by side.
// A body field that collides with a path or query name is a catalog bug and
// fails at startup (TestCatalogBuilds pins it).
func (t *tool) inputSchema(tenantScoped bool) (*jsonschema.Schema, error) {
	s := &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{}}
	t.argSet = map[string]struct{}{}
	add := func(name string, prop *jsonschema.Schema, required bool) error {
		if _, dup := s.Properties[name]; dup {
			return fmt.Errorf("tool %s: argument %q defined twice", t.spec.Name, name)
		}
		s.Properties[name] = prop
		t.argSet[name] = struct{}{}
		if required {
			s.Required = append(s.Required, name)
		}
		return nil
	}

	for _, name := range t.pathParams {
		desc := t.spec.PathDocs[name]
		if desc == "" {
			desc = defaultPathDoc(name)
		}
		if err := add(name, &jsonschema.Schema{Type: "string", Description: desc}, true); err != nil {
			return nil, err
		}
	}
	for _, q := range t.spec.Query {
		if err := add(q.Name, q.schema(), q.Required); err != nil {
			return nil, err
		}
	}
	if tenantScoped {
		if err := add(tenantArg, &jsonschema.Schema{
			Type:   "string",
			Format: "uuid",
			Description: "Tenant (workspace) to act in. Only for sign-in (OAuth) connections; defaults to the tenant " +
				"chosen when the connection was authorized. API-key connections are bound to their key's tenant.",
		}, false); err != nil {
			return nil, err
		}
	}

	switch {
	case t.spec.Multipart != nil:
		if err := add(t.spec.Multipart.ContentArg, &jsonschema.Schema{
			Type:        "string",
			Description: "The file's full text content (JSON, YAML or CSV).",
		}, true); err != nil {
			return nil, err
		}
		if err := add(t.spec.Multipart.FilenameArg, &jsonschema.Schema{
			Type: "string",
			Description: "File name; its extension (.json, .yaml, .yml, .csv) selects the parser. " +
				"Omit to auto-detect from the content.",
		}, false); err != nil {
			return nil, err
		}
	case t.spec.BodyArg != nil:
		if err := add(t.spec.BodyArg.Name, t.spec.BodyArg.schema(), t.spec.BodyArg.Required); err != nil {
			return nil, err
		}
	case t.spec.Body != nil:
		body, err := bodySchema(t.spec.Body)
		if err != nil {
			return nil, fmt.Errorf("tool %s: body schema: %w", t.spec.Name, err)
		}
		if body.Type != "object" {
			return nil, fmt.Errorf("tool %s: body %s is not an object; use BodyArg", t.spec.Name, t.spec.Body)
		}
		t.bodyFields = map[string]struct{}{}
		names := make([]string, 0, len(body.Properties))
		for name := range body.Properties {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			prop := body.Properties[name]
			if doc, ok := t.spec.FieldDocs[name]; ok {
				prop.Description = doc
			}
			if values, ok := t.spec.Enums[name]; ok {
				setEnum(prop, values)
			}
			required := false
			for _, r := range t.spec.Required {
				if r == name {
					required = true
				}
			}
			if err := add(name, prop, required); err != nil {
				return nil, err
			}
			t.bodyFields[name] = struct{}{}
		}
		for _, r := range t.spec.Required {
			if _, ok := t.bodyFields[r]; !ok {
				return nil, fmt.Errorf("tool %s: required field %q is not in body %s", t.spec.Name, r, t.spec.Body)
			}
		}
		for name := range t.spec.Enums {
			if _, ok := t.bodyFields[name]; !ok {
				return nil, fmt.Errorf("tool %s: enum field %q is not in body %s", t.spec.Name, name, t.spec.Body)
			}
		}
		for name := range t.spec.FieldDocs {
			if _, ok := t.bodyFields[name]; !ok {
				return nil, fmt.Errorf("tool %s: documented field %q is not in body %s", t.spec.Name, name, t.spec.Body)
			}
		}
	}
	return s, nil
}

func (p param) schema() *jsonschema.Schema {
	s := &jsonschema.Schema{Type: p.Type, Description: p.Description}
	if p.Type == "" {
		s.Type = "string"
	}
	if p.Type == "array" {
		s.Items = &jsonschema.Schema{Type: "string"}
	}
	if p.Type == "object" {
		s.AdditionalProperties = nil
	}
	for _, e := range p.Enum {
		s.Enum = append(s.Enum, e)
	}
	if p.Default != nil {
		if raw, err := json.Marshal(p.Default); err == nil {
			s.Default = raw
		}
	}
	return s
}

func defaultPathDoc(name string) string {
	switch name {
	case "id":
		return "ID (UUID) of the resource."
	default:
		return name + " (path parameter)."
	}
}

// setEnum constrains a string field, or the items of a string array.
func setEnum(s *jsonschema.Schema, values []string) {
	target := s
	if s.Type == "array" && s.Items != nil {
		target = s.Items
	}
	target.Enum = nil
	for _, v := range values {
		target.Enum = append(target.Enum, v)
	}
}
