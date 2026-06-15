package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOpenAICompatProvider_Complete(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"served-model","choices":[{"message":{"role":"assistant","content":"{\"ok\":true}"}}]}`))
	}))
	defer srv.Close()

	p := newOpenAICompatProvider(srv.URL+"/", "secret-key", 5*time.Second, "json_object")
	resp, err := p.Complete(context.Background(), CompletionRequest{
		Model:      "gpt-x",
		System:     "sys",
		User:       "usr",
		MaxTokens:  256,
		JSONSchema: json.RawMessage(`{"type":"object"}`),
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if gotPath != "/chat/completions" {
		t.Errorf("path = %q, want /chat/completions (trailing slash trimmed)", gotPath)
	}
	if gotAuth != "Bearer secret-key" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotBody["model"] != "gpt-x" {
		t.Errorf("model = %v", gotBody["model"])
	}
	if _, ok := gotBody["response_format"]; !ok {
		t.Error("expected response_format when JSONMode is json_object")
	}
	if resp.Content != `{"ok":true}` {
		t.Errorf("content = %q", resp.Content)
	}
	if resp.Model != "served-model" {
		t.Errorf("model = %q", resp.Model)
	}
}

// With the default (off) JSON mode the provider must NOT send response_format —
// this is what keeps it compatible with servers that reject the field.
func TestOpenAICompatProvider_OmitsResponseFormatByDefault(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"m","choices":[{"message":{"content":"{}"}}]}`))
	}))
	defer srv.Close()

	p := newOpenAICompatProvider(srv.URL, "", 5*time.Second, "")
	if _, err := p.Complete(context.Background(), CompletionRequest{
		Model:      "m",
		User:       "u",
		JSONSchema: json.RawMessage(`{"type":"object"}`),
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, ok := gotBody["response_format"]; ok {
		t.Error("response_format must be omitted when JSONMode is off (default)")
	}
}

func TestOpenAICompatProvider_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer srv.Close()

	p := newOpenAICompatProvider(srv.URL, "", 5*time.Second, "")
	_, err := p.Complete(context.Background(), CompletionRequest{Model: "m", User: "u"})
	if err == nil {
		t.Fatal("expected error on non-2xx status")
	}
}

func TestOpenAICompatProvider_NoChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"m","choices":[]}`))
	}))
	defer srv.Close()

	p := newOpenAICompatProvider(srv.URL, "", 5*time.Second, "")
	if _, err := p.Complete(context.Background(), CompletionRequest{Model: "m", User: "u"}); err == nil {
		t.Fatal("expected error when no choices returned")
	}
}
