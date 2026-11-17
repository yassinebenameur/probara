//go:build !unit

package api

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	mcpserver "github.com/yassinebenameur/probara/api/internal/mcp"
	"github.com/yassinebenameur/probara/shared/config"
	sharedmodels "github.com/yassinebenameur/probara/shared/models"
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

// TestOpenAPIResponsesMatchHandlers seeds a workspace with one of each main
// resource and validates real answers against the document: the status is
// a documented success, JSON bodies match its schema (list items included,
// since the lists are not empty), a 204 has no body, and a non-JSON answer
// carries a documented media type.
func TestOpenAPIResponsesMatchHandlers(t *testing.T) {
	natsURL := startJetStream(t)
	f := newMCPFixtureWith(t, func(cfg *config.APIConfig) { cfg.NATSURL = natsURL })
	key := f.apiKey(f.tenantA, "write")
	doc := loadOpenAPIDoc(t)

	check := func(method, route, target string, body any, want int) []byte {
		t.Helper()
		status, raw, contentType := f.do(t, key, method, target, body)
		if status != want {
			t.Fatalf("%s %s: %d, want %d: %s", method, target, status, want, raw)
		}
		resp, ok := doc.response(method, route, status)
		if !ok {
			t.Fatalf("%s %s: the document has no %d success response", method, route, status)
		}
		content, _ := resp["content"].(map[string]any)
		media, _, _ := mime.ParseMediaType(contentType)
		switch {
		case len(content) == 0:
			if len(raw) != 0 {
				t.Errorf("%s %s: documented without a body, answered %d bytes", method, target, len(raw))
			}
		case media == "application/json" && content["application/json"] != nil:
			schema, ok := doc.schemaFor(content["application/json"])
			if !ok {
				t.Fatalf("%s %s: documented JSON answer has no usable schema", method, route)
			}
			var instance any
			if err := json.Unmarshal(raw, &instance); err != nil {
				t.Fatalf("%s %s: body is not JSON: %v", method, target, err)
			}
			if err := schema.Validate(instance); err != nil {
				t.Errorf("%s %s: answer does not match the documented schema: %v\n%s", method, target, err, raw)
			}
		default:
			if _, documented := content[media]; !documented {
				t.Errorf("%s %s: answered %q, documented %v", method, target, media, keysOf(content))
			}
		}
		return raw
	}
	idOf := func(raw []byte) string {
		t.Helper()
		var v struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &v); err != nil || v.ID == "" {
			t.Fatalf("no id in %s", raw)
		}
		return v.ID
	}
	nonEmptyList := func(raw []byte) {
		t.Helper()
		var v struct {
			Items []json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal(raw, &v); err != nil || len(v.Items) == 0 {
			t.Fatalf("expected a non-empty items list: %s", raw)
		}
	}
	const (
		get, post, patch, del = http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete
		ok, created, accepted = http.StatusOK, http.StatusCreated, http.StatusAccepted
		noContent             = http.StatusNoContent
	)

	// Monitor: 201, read, update, list, an optional-body 202, history.
	monitorID := idOf(check(post, "/monitors", "/api/v1/monitors", map[string]any{
		"name": "OpenAPI check", "type": "http", "interval_seconds": 60, "timeout_seconds": 10,
		"config": map[string]any{"url": "https://example.com", "method": "GET"},
	}, created))
	m := "/api/v1/monitors/" + monitorID
	check(get, "/monitors/{id}", m, nil, ok)
	check(patch, "/monitors/{id}", m, map[string]any{"name": "OpenAPI check 2"}, ok)
	nonEmptyList(check(get, "/monitors", "/api/v1/monitors", nil, ok))
	check(post, "/monitors/{id}/run", m+"/run", nil, accepted)
	check(get, "/monitors/{id}/results", m+"/results", nil, ok)
	check(get, "/monitors/{id}/analytics", m+"/analytics", nil, ok)
	check(get, "/monitors/{id}/dependencies", m+"/dependencies", nil, ok)
	check(get, "/monitors/{id}/versions", m+"/versions", nil, ok)
	check(get, "/monitors/dependency-graph", "/api/v1/monitors/dependency-graph", nil, ok)
	check(get, "/monitors/export", "/api/v1/monitors/export", nil, ok) // YAML

	// Alert (alerts are raised by the alerter; seed one).
	alertID := uuid.New()
	if _, err := f.db.ExecContext(f.ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, status, triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, 'active', NOW(), 3, NOW(), NOW())`, alertID, f.tenantA, monitorID); err != nil {
		t.Fatal(err)
	}
	nonEmptyList(check(get, "/alerts", "/api/v1/alerts", nil, ok))
	check(get, "/alerts/{id}", "/api/v1/alerts/"+alertID.String(), nil, ok)
	check(get, "/alerts/recent", "/api/v1/alerts/recent", nil, ok)
	check(post, "/alerts/{id}/acknowledge", "/api/v1/alerts/"+alertID.String()+"/acknowledge", nil, ok)

	// Incident, including the AI analysis that is JSON null until one runs.
	incidentID := idOf(check(post, "/incidents", "/api/v1/incidents",
		map[string]any{"title": "OpenAPI incident", "summary": "seeded"}, created))
	check(patch, "/incidents/{id}", "/api/v1/incidents/"+incidentID, map[string]any{"title": "OpenAPI incident 2"}, ok)
	check(get, "/incidents/{id}", "/api/v1/incidents/"+incidentID, nil, ok)
	if raw := check(get, "/incidents/{id}/ai-analysis", "/api/v1/incidents/"+incidentID+"/ai-analysis", nil, ok); string(bytes.TrimSpace(raw)) != "null" {
		t.Errorf("expected the no-analysis answer to be null, got %s", raw)
	}
	nonEmptyList(check(get, "/incidents", "/api/v1/incidents", nil, ok))

	// Alert channel: 201, list, then a 204 delete.
	channelID := idOf(check(post, "/alert-channels", "/api/v1/alert-channels", map[string]any{
		"name": "OpenAPI hook", "type": "generic_webhook", "config": map[string]any{"url": "https://example.com/hook"},
	}, created))
	nonEmptyList(check(get, "/alert-channels", "/api/v1/alert-channels", nil, ok))
	check(get, "/alert-channels/{id}", "/api/v1/alert-channels/"+channelID, nil, ok)
	check(get, "/alert-channel-plugins", "/api/v1/alert-channel-plugins", nil, ok)
	check(del, "/alert-channels/{id}", "/api/v1/alert-channels/"+channelID, nil, noContent)

	// Status page, and a plain-text answer.
	pageID := idOf(check(post, "/status-pages", "/api/v1/status-pages",
		map[string]any{"slug": "openapi-check", "title": "OpenAPI"}, created))
	nonEmptyList(check(get, "/status-pages", "/api/v1/status-pages", nil, ok))
	check(get, "/status-pages/{id}/template/default", "/api/v1/status-pages/"+pageID+"/template/default", nil, ok)

	// Maintenance window.
	start := time.Now().Add(time.Hour).UTC()
	check(post, "/maintenance-windows", "/api/v1/maintenance-windows", map[string]any{
		"title": "OpenAPI window", "monitor_ids": []string{monitorID},
		"starts_at": start.Format(time.RFC3339), "ends_at": start.Add(time.Hour).Format(time.RFC3339),
	}, created)
	nonEmptyList(check(get, "/maintenance-windows", "/api/v1/maintenance-windows", nil, ok))

	// Location: 201, list, and the delete that reports detached monitors.
	locationID := idOf(check(post, "/locations", "/api/v1/locations", map[string]any{"name": "OpenAPI site"}, created))
	nonEmptyList(check(get, "/locations", "/api/v1/locations", nil, ok))
	check(del, "/locations/{id}", "/api/v1/locations/"+locationID, nil, ok)

	// SLA.
	slaID := idOf(check(post, "/slas", "/api/v1/slas",
		map[string]any{"name": "OpenAPI SLA", "target_pct": 99.9, "monitor_ids": []string{monitorID}}, created))
	nonEmptyList(check(get, "/slas", "/api/v1/slas", nil, ok))
	check(get, "/slas/{id}", "/api/v1/slas/"+slaID, nil, ok)
	check(get, "/slas/{id}/reports", "/api/v1/slas/"+slaID+"/reports", nil, ok)

	// Workspace-wide reads.
	check(get, "/auth-context", "/api/v1/auth-context", nil, ok)
	check(get, "/notification-settings", "/api/v1/notification-settings", nil, ok)
	check(get, "/mesh", "/api/v1/mesh", nil, ok)
	check(get, "/dashboard/summary", "/api/v1/dashboard/summary", nil, ok)
	check(get, "/dashboard/overview", "/api/v1/dashboard/overview", nil, ok)

	// The tenant list is session-only, and the document says so.
	if status, raw, _ := f.do(t, key, get, "/api/v1/tenants", nil); status != http.StatusForbidden {
		t.Errorf("GET /tenants with an API key: %d %s, want 403", status, raw)
	}
	if security, _ := doc.paths["/tenants"]["get"].(map[string]any)["security"].([]any); len(security) != 1 {
		t.Errorf("GET /tenants must list the session scheme only, got %v", security)
	}

	check(post, "/monitors/bulk/delete", "/api/v1/monitors/bulk/delete", map[string]any{"monitor_ids": []string{monitorID}}, ok)
}

// startJetStream runs an in-process NATS server with the check-job stream,
// so run-now can queue its job and answer 202.
func startJetStream(t *testing.T) string {
	t.Helper()
	srv, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoLog: true, NoSigs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	t.Cleanup(srv.Shutdown)
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("NATS server did not start")
	}
	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := nc.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	subject := sharedmodels.CheckJobSubject
	if _, err := js.AddStream(&nats.StreamConfig{Name: "OPENAPI_CHECK_JOBS", Subjects: []string{subject, subject + ".>"}}); err != nil {
		t.Fatal(err)
	}
	return srv.ClientURL()
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
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

// response returns the document's response object for one success status
// of an operation.
func (d loadedOpenAPI) response(method, route string, status int) (map[string]any, bool) {
	if status >= 300 {
		return nil, false
	}
	op, _ := d.paths[route][strings.ToLower(method)].(map[string]any)
	responses, _ := op["responses"].(map[string]any)
	resp, ok := responses[strconv.Itoa(status)].(map[string]any)
	return resp, ok
}

// schemaFor resolves a media type object's schema against the document's
// components.
func (d loadedOpenAPI) schemaFor(mediaType any) (*jsonschema.Resolved, bool) {
	media, _ := mediaType.(map[string]any)
	schema, ok := media["schema"]
	if !ok {
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
