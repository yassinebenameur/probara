package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestCatalogEntriesAreDescribed keeps every tool usable by a model: a
// title, a description, a snake_case verb_noun name, and an argument schema
// that builds (no body field colliding with a path or query parameter).
func TestCatalogEntriesAreDescribed(t *testing.T) {
	specs := catalog()
	t.Logf("%d catalog tools", len(specs))
	for _, s := range specs {
		if s.Title == "" || len(s.Description) < 20 {
			t.Errorf("%s: missing title or description", s.Name)
		}
		if s.Name != strings.ToLower(s.Name) || (!strings.Contains(s.Name, "_") && s.Name != "whoami") {
			t.Errorf("%s: tool names are snake_case verb_noun", s.Name)
		}
		tl := &tool{spec: s, pathParams: pathParams(s.Route)}
		schema, err := tl.inputSchema(!s.Platform)
		if err != nil {
			t.Errorf("%s: %v", s.Name, err)
			continue
		}
		if _, err := json.Marshal(schema); err != nil {
			t.Errorf("%s: schema does not marshal: %v", s.Name, err)
		}
		if s.Destructive && s.Method == http.MethodGet {
			t.Errorf("%s: a GET cannot be destructive", s.Name)
		}
	}
}

func TestBuildRequestMapsArguments(t *testing.T) {
	s := spec{
		Name: "t", Method: http.MethodPatch, Route: "/monitors/{id}",
		Query: []param{{Name: "tag", Type: "array"}},
		Body: typeOf[struct {
			Name string `json:"name"`
		}](),
	}
	tl := &tool{spec: s, pathParams: pathParams(s.Route)}
	if _, err := tl.inputSchema(true); err != nil {
		t.Fatal(err)
	}
	header := http.Header{"Authorization": {"Bearer pk_x"}, "Cookie": {"admin_access=steal"}}
	req, err := tl.buildRequest(t.Context(), json.RawMessage(`{"id":"a/b","tag":["x","y"],"name":"n","tenant_id":"T"}`), header)
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.Path != "/api/v1/monitors/a/b" || req.URL.RawPath != "/api/v1/monitors/a%2Fb" {
		t.Errorf("path not escaped: %q %q", req.URL.Path, req.URL.RawPath)
	}
	if got := req.URL.Query()["tag"]; len(got) != 2 {
		t.Errorf("repeated query param lost: %v", got)
	}
	if req.Header.Get("Cookie") != "" {
		t.Error("cookies must never reach the inner request")
	}
	if req.Header.Get("Authorization") != "Bearer pk_x" || req.Header.Get("X-Tenant-ID") != "T" {
		t.Errorf("headers: %v", req.Header)
	}
	if req.Context().Value(struct{}{}) != nil {
		t.Error("inner context must not inherit values")
	}

	if _, err := tl.buildRequest(t.Context(), json.RawMessage(`{"name":"n"}`), header); err == nil {
		t.Error("missing path parameter must fail")
	}
}

func TestParseAlertEventsSkipsHeartbeats(t *testing.T) {
	stream := "event: connected\ndata: {\"status\":\"connected\"}\n\n" +
		"event: heartbeat\ndata: {\"time\":\"x\"}\n\n" +
		"event: alert_created\ndata: {\"id\":\"a1\"}\n\n" +
		"event: alert_resolved\ndata: {\"id\":\"a1\""
	got := parseAlertEvents([]byte(stream))
	if len(got) != 1 || got[0].Event != "alert_created" {
		t.Fatalf("want one complete alert_created event, got %+v", got)
	}
}
