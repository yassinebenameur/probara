//go:build !unit

package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"

	mcpserver "github.com/yassinebenameur/probara/api/internal/mcp"
)

// TestOpenAPIServedToAPIKeys: any valid API key, even read scope, gets the
// document, byte for byte the committed one (tests run unstamped, so
// info.version is "dev" on both sides).
func TestOpenAPIServedToAPIKeys(t *testing.T) {
	f := newMCPFixture(t)
	key := f.apiKey(f.tenantA, "read")
	status, body, contentType := f.do(t, key, http.MethodGet, mcpserver.OpenAPIPath, nil)
	if status != http.StatusOK || !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("status %d, content type %q", status, contentType)
	}
	golden, err := os.ReadFile(openAPIGolden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, golden) {
		t.Fatal("served document differs from api/openapi.json")
	}
}

// TestOpenAPIResponsesMatchHandlers calls real routes and validates each
// answer against the response schema the document gives for it: the
// response table and the reflected models must describe what handlers send.
func TestOpenAPIResponsesMatchHandlers(t *testing.T) {
	f := newMCPFixture(t)
	key := f.apiKey(f.tenantA, "write")
	doc := loadOpenAPIDoc(t)

	check := func(method, route, target string, body any) []byte {
		t.Helper()
		status, raw, _ := f.do(t, key, method, target, body)
		schema, ok := doc.responseSchema(method, route, status)
		if !ok {
			t.Fatalf("%s %s answered %d, which the document does not describe as a success: %s", method, target, status, raw)
		}
		var instance any
		if err := json.Unmarshal(raw, &instance); err != nil {
			t.Fatalf("%s %s: body is not JSON: %v", method, target, err)
		}
		if err := schema.Validate(instance); err != nil {
			t.Errorf("%s %s: answer does not match the documented schema: %v\n%s", method, target, err, raw)
		}
		return raw
	}

	raw := check(http.MethodPost, "/monitors", "/api/v1/monitors", map[string]any{
		"name": "OpenAPI check", "type": "http", "interval_seconds": 60, "timeout_seconds": 10,
		"config": map[string]any{"url": "https://example.com", "method": "GET"},
	})
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.ID == "" {
		t.Fatalf("created monitor: %v %s", err, raw)
	}
	id := created.ID
	check(http.MethodGet, "/monitors/{id}", "/api/v1/monitors/"+id, nil)
	check(http.MethodPatch, "/monitors/{id}", "/api/v1/monitors/"+id, map[string]any{"name": "OpenAPI check 2"})
	check(http.MethodGet, "/monitors", "/api/v1/monitors", nil)
	check(http.MethodGet, "/monitors/{id}/results", "/api/v1/monitors/"+id+"/results", nil)
	check(http.MethodGet, "/monitors/{id}/analytics", "/api/v1/monitors/"+id+"/analytics", nil)
	check(http.MethodGet, "/monitors/{id}/dependencies", "/api/v1/monitors/"+id+"/dependencies", nil)
	check(http.MethodGet, "/monitors/{id}/versions", "/api/v1/monitors/"+id+"/versions", nil)
	check(http.MethodGet, "/monitors/dependency-graph", "/api/v1/monitors/dependency-graph", nil)
	check(http.MethodGet, "/auth-context", "/api/v1/auth-context", nil)
	check(http.MethodGet, "/alerts", "/api/v1/alerts", nil)
	check(http.MethodGet, "/incidents", "/api/v1/incidents", nil)
	check(http.MethodGet, "/alert-channels", "/api/v1/alert-channels", nil)
	check(http.MethodGet, "/alert-channel-plugins", "/api/v1/alert-channel-plugins", nil)
	check(http.MethodGet, "/notification-settings", "/api/v1/notification-settings", nil)
	check(http.MethodGet, "/status-pages", "/api/v1/status-pages", nil)
	check(http.MethodGet, "/maintenance-windows", "/api/v1/maintenance-windows", nil)
	check(http.MethodGet, "/locations", "/api/v1/locations", nil)
	check(http.MethodGet, "/slas", "/api/v1/slas", nil)
	check(http.MethodGet, "/mesh", "/api/v1/mesh", nil)
	check(http.MethodGet, "/dashboard/summary", "/api/v1/dashboard/summary", nil)
	check(http.MethodGet, "/dashboard/overview", "/api/v1/dashboard/overview", nil)
	// The tenant list is session-only, and the document says so.
	if status, raw, _ := f.do(t, key, http.MethodGet, "/api/v1/tenants", nil); status != http.StatusForbidden {
		t.Errorf("GET /tenants with an API key: %d %s, want 403", status, raw)
	}
	if security, _ := doc.paths["/tenants"]["get"].(map[string]any)["security"].([]any); len(security) != 1 {
		t.Errorf("GET /tenants must list the session scheme only, got %v", security)
	}
	check(http.MethodPost, "/monitors/bulk/delete", "/api/v1/monitors/bulk/delete", map[string]any{"monitor_ids": []string{id}})
}

func (f *mcpFixture) do(t *testing.T, key, method, target string, body any) (int, []byte, string) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, f.http.URL+target, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, raw, res.Header.Get("Content-Type")
}

type loadedOpenAPI struct {
	paths   map[string]map[string]any
	schemas map[string]any
}

func loadOpenAPIDoc(t *testing.T) loadedOpenAPI {
	t.Helper()
	raw, err := os.ReadFile(openAPIGolden)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths      map[string]map[string]any `json:"paths"`
		Components struct {
			Schemas map[string]any `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return loadedOpenAPI{paths: doc.Paths, schemas: doc.Components.Schemas}
}

// responseSchema returns the JSON schema the document gives for one success
// status of an operation, resolved against the document's components.
func (d loadedOpenAPI) responseSchema(method, route string, status int) (*jsonschema.Resolved, bool) {
	op, _ := d.paths[route][strings.ToLower(method)].(map[string]any)
	responses, _ := op["responses"].(map[string]any)
	resp, _ := responses[strconv.Itoa(status)].(map[string]any)
	content, _ := resp["content"].(map[string]any)
	media, _ := content["application/json"].(map[string]any)
	schema, ok := media["schema"]
	if !ok || status >= 300 {
		return nil, false
	}
	// Components become $defs of one root so the document's references resolve.
	root := map[string]any{"$defs": d.schemas, "allOf": []any{schema}}
	raw, err := json.Marshal(root)
	if err != nil {
		return nil, false
	}
	raw = bytes.ReplaceAll(raw, []byte(`"#/components/schemas/`), []byte(`"#/$defs/`))
	var s jsonschema.Schema
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	resolved, err := s.Resolve(nil)
	if err != nil {
		return nil, false
	}
	return resolved, true
}
