package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/jsonschema-go/jsonschema"

	mcpserver "github.com/yassinebenameur/probara/api/internal/mcp"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
)

var updateOpenAPI = flag.Bool("update", false, "rewrite api/openapi.json")

// openAPIGolden is the committed document, relative to this package.
const openAPIGolden = "../../openapi.json"

// routerOnlyServer builds the real router against a database that is never
// reached (sql.Open does not connect).
func routerOnlyServer(t *testing.T) *Server {
	t.Helper()
	sqlDB, err := sql.Open("postgres", "postgres://unused@127.0.0.1:1/unused?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	cfg := &config.APIConfig{}
	cfg.ServiceName = "api-test"
	cfg.NATSURL = "nats://127.0.0.1:1"
	s := NewServer(cfg, logger.New("api-test", "error"), metrics.NewRegistry("api-test"), &db.Client{DB: sqlDB})
	if s.mcpErr != nil {
		t.Fatal(s.mcpErr)
	}
	return s
}

// generateOpenAPI binds a fresh MCP handler to the server's router, exactly
// as NewServer does, and returns the document it generates.
func generateOpenAPI(t *testing.T, s *Server) []byte {
	t.Helper()
	h := mcpserver.NewHandler(logger.New("api-test", "error"), nil)
	if err := h.Bind(s.http.Handler.(chi.Router), nil); err != nil {
		t.Fatal(err)
	}
	return h.OpenAPI()
}

// TestOpenAPIGolden keeps api/openapi.json equal to what the router and the
// MCP catalog generate. Refresh it with `make openapi`.
func TestOpenAPIGolden(t *testing.T) {
	s := routerOnlyServer(t)
	doc := generateOpenAPI(t, s)
	if again := generateOpenAPI(t, routerOnlyServer(t)); !bytes.Equal(doc, again) {
		t.Fatal("OpenAPI generation is not deterministic: two runs differ")
	}
	if *updateOpenAPI {
		if err := os.WriteFile(openAPIGolden, doc, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	golden, err := os.ReadFile(openAPIGolden)
	if err != nil {
		t.Fatalf("%v; generate it with `make openapi`", err)
	}
	if !bytes.Equal(doc, golden) {
		t.Fatal("api/openapi.json is stale: routes or the MCP catalog changed. Regenerate it with `make openapi` " +
			"and commit the result.")
	}
}

type openAPIDoc struct {
	Paths        map[string]map[string]json.RawMessage `json:"paths"`
	NotDescribed map[string]string                     `json:"x-probara-not-described"`
}

// TestOpenAPICoversRouter reads the document the way a client would: every
// routed /api/v1 operation is either described under paths or listed with a
// reason under x-probara-not-described, and nothing else is.
func TestOpenAPICoversRouter(t *testing.T) {
	s := routerOnlyServer(t)
	var doc openAPIDoc
	if err := json.Unmarshal(generateOpenAPI(t, s), &doc); err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	typed := 0
	for path, ops := range doc.Paths {
		for method, raw := range ops {
			listed[strings.ToUpper(method)+" /api/v1"+path] = true
			var op struct {
				Responses map[string]json.RawMessage `json:"responses"`
			}
			if err := json.Unmarshal(raw, &op); err != nil {
				t.Fatal(err)
			}
			if _, untyped := op.Responses["2XX"]; !untyped {
				typed++
			}
		}
	}
	described := len(listed)
	for key, reason := range doc.NotDescribed {
		if reason == "" {
			t.Errorf("%s: excluded without a reason", key)
		}
		listed[key] = true
	}
	var missing []string
	err := chi.Walk(s.http.Handler.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = normalizeRoutePattern(strings.ReplaceAll(route, "//", "/"))
		if !strings.HasPrefix(route, "/api/v1/") || method == http.MethodOptions || method == http.MethodHead {
			return nil
		}
		if !listed[method+" "+route] && !listed["* "+route] {
			missing = append(missing, method+" "+route)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("routed operations neither described nor excluded: %s", strings.Join(missing, ", "))
	}
	t.Logf("%d operations described (%d with a typed success response), %d routes excluded", described, typed, len(doc.NotDescribed))
}

// TestOpenAPIDocumentValidates checks the committed document against the
// OpenAPI 3.1 schema with the OpenAPI schema dialect (schema-base), so every
// embedded schema is validated as a JSON Schema 2020-12 schema too. The
// meta-schemas are vendored under testdata/openapi-3.1, copied unchanged from
// https://spec.openapis.org/oas/3.1/ (2022-10-07) and
// https://json-schema.org/draft/2020-12/.
func TestOpenAPIDocumentValidates(t *testing.T) {
	validator := openAPIValidator(t)
	golden, err := os.ReadFile(openAPIGolden)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(golden, &doc); err != nil {
		t.Fatal(err)
	}
	if err := validator.Validate(doc); err != nil {
		t.Fatalf("api/openapi.json is not a valid OpenAPI 3.1 document: %v", err)
	}

	// The validator must be able to fail: a document without info, and one
	// with a malformed embedded schema, are both refused.
	broken := cloneJSON(t, doc)
	delete(broken, "info")
	if validator.Validate(broken) == nil {
		t.Error("validator accepted a document without info")
	}
	broken = cloneJSON(t, doc)
	schemas := broken["components"].(map[string]any)["schemas"].(map[string]any)
	for name := range schemas {
		schemas[name] = map[string]any{"type": 5}
		break
	}
	if validator.Validate(broken) == nil {
		t.Error("validator accepted a schema with a non-string type")
	}
}

func cloneJSON(t *testing.T, v map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func openAPIValidator(t *testing.T) *jsonschema.Resolved {
	t.Helper()
	files, err := filepath.Glob("testdata/openapi-3.1/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("meta-schemas missing: %v", err)
	}
	byID := map[string]*jsonschema.Schema{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var s jsonschema.Schema
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		byID[s.ID] = &s
	}
	root := byID["https://spec.openapis.org/oas/3.1/schema-base/2022-10-07"]
	if root == nil {
		t.Fatal("schema-base meta-schema missing")
	}
	resolved, err := root.Resolve(&jsonschema.ResolveOptions{
		Loader: func(uri *url.URL) (*jsonschema.Schema, error) {
			u := *uri
			u.Fragment = ""
			if s, ok := byID[u.String()]; ok {
				return s, nil
			}
			return nil, fmt.Errorf("no vendored meta-schema for %s", uri)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// TestOpenAPIRefusesAnonymous: the document is for authenticated callers
// only, and an MCP OAuth token is not a REST credential.
func TestOpenAPIRefusesAnonymous(t *testing.T) {
	s := routerOnlyServer(t)
	for name, header := range map[string]http.Header{
		"no credential":   {},
		"garbage cookie":  {"Cookie": {"admin_access=not-a-token"}},
		"MCP OAuth token": {"Authorization": {"Bearer pbm_at_not-valid-here"}},
	} {
		req := httptest.NewRequest(http.MethodGet, mcpserver.OpenAPIPath, nil)
		req.Header = header
		rec := httptest.NewRecorder()
		s.http.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), `"openapi"`) {
			t.Errorf("%s: status %d, body %.80q; want 401 and no document", name, rec.Code, rec.Body.String())
		}
	}
}
