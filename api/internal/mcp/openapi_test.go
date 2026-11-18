package mcp

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/yassinebenameur/probara/shared/logger"
)

// TestSuccessResponsesNameOperations keeps the response table keyed by real
// operation IDs (Bind refuses a stale key too, but this needs no router).
func TestSuccessResponsesNameOperations(t *testing.T) {
	names := map[string]bool{}
	for _, s := range catalog() {
		names[s.Name] = true
	}
	for _, d := range openAPIRoutes() {
		if names[d.Name] {
			t.Errorf("%s: documented route reuses a catalog tool name", d.Name)
		}
		names[d.Name] = true
	}
	for name, r := range successResponses {
		if !names[name] {
			t.Errorf("successResponses[%q]: no such operation", name)
		}
		if r.Body != nil {
			if _, err := responseSchema(r.Body); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		}
	}
	t.Logf("%d of %d operations have a described success response", len(successResponses), len(names))
}

type selfMarshaling struct{ v int }

func (s selfMarshaling) MarshalJSON() ([]byte, error) { return json.Marshal(s.v) }

// TestResponseSchemaRefusesWhatReflectionCannotShow: a type that writes its
// own JSON, or a field reflection cannot describe, fails instead of being
// documented wrongly or dropped.
func TestResponseSchemaRefusesWhatReflectionCannotShow(t *testing.T) {
	type wrapsMarshaler struct {
		Value *selfMarshaling `json:"value"`
	}
	if _, err := responseSchema(reflect.TypeFor[wrapsMarshaler]()); err == nil || !strings.Contains(err.Error(), "marshals itself") {
		t.Errorf("self-marshaling field: err = %v", err)
	}
	type hasFunc struct {
		F func() `json:"f"`
	}
	if _, err := responseSchema(reflect.TypeFor[hasFunc]()); err == nil {
		t.Error("a func field must fail, not vanish from the schema")
	}
	type fine struct {
		At   *time.Time `json:"at"`
		Note string     `json:"note,omitempty"`
	}
	s, err := responseSchema(reflect.TypeFor[fine]())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(s)
	if strings.Contains(string(raw), `"additionalProperties":false`) {
		t.Errorf("response objects must stay open: %s", raw)
	}
	if len(s.Required) != 1 || s.Required[0] != "at" {
		t.Errorf("required = %v, want [at] (fields without omitempty are always written)", s.Required)
	}
}

// stubRouter routes every catalog tool, documented route and notExposed
// entry to a no-op handler: enough for Bind to resolve the catalog.
func stubRouter(t *testing.T) chi.Router {
	t.Helper()
	r := chi.NewRouter()
	noop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	seen := map[string]bool{}
	add := func(method, pattern string) {
		if seen[method+" "+pattern] {
			return
		}
		seen[method+" "+pattern] = true
		if method == "*" {
			r.Handle(pattern, noop)
			return
		}
		r.Method(method, pattern, noop)
	}
	for _, s := range catalog() {
		add(s.Method, apiPrefix+s.Route)
	}
	for _, d := range openAPIRoutes() {
		add(d.Method, apiPrefix+d.Route)
	}
	for key := range notExposed {
		method, pattern, _ := strings.Cut(key, " ")
		add(method, pattern)
	}
	return r
}

func boundHandler(t *testing.T) *Handler {
	t.Helper()
	h := NewHandler(logger.New("mcp-test", "error"), nil)
	if err := h.Bind(stubRouter(t), nil); err != nil {
		t.Fatal(err)
	}
	return h
}

// TestOpenAPIGenerationFailureDegradesOnlyTheEndpoint: a response type
// reflection cannot describe must not stop the API or MCP from starting.
func TestOpenAPIGenerationFailureDegradesOnlyTheEndpoint(t *testing.T) {
	type unreflectable struct {
		F func() `json:"f"`
	}
	saved, had := successResponses["list_monitors"]
	successResponses["list_monitors"] = ok[unreflectable]()
	t.Cleanup(func() {
		if had {
			successResponses["list_monitors"] = saved
		} else {
			delete(successResponses, "list_monitors")
		}
	})

	h := NewHandler(logger.New("mcp-test", "error"), nil)
	if err := h.Bind(stubRouter(t), nil); err != nil {
		t.Fatalf("Bind must succeed when only the OpenAPI document fails: %v", err)
	}
	if h.OpenAPIError() == nil || h.OpenAPI() != nil {
		t.Fatal("the generation error must be recorded and no document kept")
	}

	rec := httptest.NewRecorder()
	h.ServeOpenAPI(rec, httptest.NewRequest(http.MethodGet, OpenAPIPath, nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"openapi_unavailable"`) {
		t.Errorf("degraded endpoint: %d %s, want 503 openapi_unavailable", rec.Code, rec.Body.String())
	}

	// MCP keeps serving: an initialize call succeeds.
	req := httptest.NewRequest(http.MethodPost, Path, strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"serverInfo"`) {
		t.Errorf("MCP initialize while OpenAPI is degraded: %d %s", rec.Code, rec.Body.String())
	}
}

func TestServeOpenAPICachingAndCompression(t *testing.T) {
	h := boundHandler(t)
	if err := h.OpenAPIError(); err != nil {
		t.Fatal(err)
	}
	get := func(header map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, OpenAPIPath, nil)
		for k, v := range header {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeOpenAPI(rec, req)
		return rec
	}

	plain := get(nil)
	etag := plain.Header().Get("ETag")
	if plain.Code != http.StatusOK || !bytes.Equal(plain.Body.Bytes(), h.OpenAPI()) ||
		plain.Header().Get("Content-Encoding") != "" || !strings.HasPrefix(etag, `"`) ||
		plain.Header().Get("Cache-Control") != "private, max-age=300" || plain.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("identity response: %d headers %v", plain.Code, plain.Header())
	}

	zipped := get(map[string]string{"Accept-Encoding": "br, gzip;q=0.8"})
	if zipped.Header().Get("Content-Encoding") != "gzip" || zipped.Header().Get("ETag") == etag {
		t.Fatalf("gzip response headers: %v", zipped.Header())
	}
	zr, err := gzip.NewReader(zipped.Body)
	if err != nil {
		t.Fatal(err)
	}
	unzipped, err := io.ReadAll(zr)
	if err != nil || !bytes.Equal(unzipped, h.OpenAPI()) {
		t.Fatalf("gzip body does not decode to the document: %v", err)
	}
	if zipped.Body.Len() >= len(h.OpenAPI())/4 {
		t.Errorf("gzip body is %d bytes for a %d-byte document", zipped.Body.Len(), len(h.OpenAPI()))
	}

	if got := get(map[string]string{"Accept-Encoding": "gzip;q=0"}); got.Header().Get("Content-Encoding") != "" {
		t.Error("gzip;q=0 must get the identity encoding")
	}

	for _, inm := range []string{etag, zipped.Header().Get("ETag"), "W/" + etag, `"other", ` + etag, "*"} {
		rec := get(map[string]string{"If-None-Match": inm})
		if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 || rec.Header().Get("ETag") == "" {
			t.Errorf("If-None-Match %s: %d, %d bytes", inm, rec.Code, rec.Body.Len())
		}
	}
	if rec := get(map[string]string{"If-None-Match": `"stale"`}); rec.Code != http.StatusOK {
		t.Errorf("a stale ETag must get the document, got %d", rec.Code)
	}
}
