package notifications

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseTeamsWebhookConfig(t *testing.T) {
	_, err := ParseTeamsWebhookConfig(nil)
	if err == nil {
		t.Fatalf("expected error for empty config")
	}

	_, err = ParseTeamsWebhookConfig(json.RawMessage(`{}`))
	if err == nil {
		t.Fatalf("expected error for missing webhook_url")
	}

	cfg, err := ParseTeamsWebhookConfig(json.RawMessage(`{"webhook_url":"https://example.com/hook"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.WebhookURL != "https://example.com/hook" {
		t.Fatalf("unexpected webhook url: %s", cfg.WebhookURL)
	}
}

func TestSendTeamsWebhook_Success(t *testing.T) {
	var gotMethod string
	var gotContentType string
	var gotPayload TeamsMessage

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("failed to decode payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	message := TeamsMessage{
		Type:    "MessageCard",
		Context: "https://schema.org/extensions",
		Title:   "Test",
	}

	if err := SendTeamsWebhook(context.Background(), server.URL, message); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("expected POST, got %s", gotMethod)
	}
	if gotContentType != "application/json" {
		t.Fatalf("expected application/json content type, got %s", gotContentType)
	}
	if gotPayload.Title != "Test" {
		t.Fatalf("unexpected payload title: %s", gotPayload.Title)
	}
}

func TestSendTeamsWebhook_ErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	if err := SendTeamsWebhook(context.Background(), server.URL, TeamsMessage{}); err == nil {
		t.Fatalf("expected error for non-2xx response")
	}
}
