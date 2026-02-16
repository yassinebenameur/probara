package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSyntheticAPIChecker_SuccessWithExtraction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/login":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"token":"abc123"}`))
		case "/v1/resource":
			if r.Header.Get("Authorization") != "Bearer abc123" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"ok":false}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	checker := NewSyntheticAPIChecker()
	sensitive := true
	cfg := map[string]interface{}{
		"base_url": server.URL,
		"steps": []map[string]interface{}{
			{
				"id": "login",
				"request": map[string]interface{}{
					"method": "POST",
					"url":    "/auth/login",
				},
				"assert": []map[string]interface{}{
					{"target": "status", "op": "equals", "value": 200},
				},
				"extract": []map[string]interface{}{
					{"name": "token", "from": "json", "path": "token", "sensitive": sensitive},
				},
			},
			{
				"id": "check",
				"request": map[string]interface{}{
					"method": "GET",
					"url":    "/v1/resource",
					"headers": map[string]string{
						"Authorization": "Bearer {{token}}",
					},
				},
				"assert": []map[string]interface{}{
					{"target": "status", "op": "in", "value": []int{200}},
					{"target": "json", "op": "bool_is", "path": "ok", "value": true},
				},
			},
		},
	}
	configJSON, _ := json.Marshal(cfg)

	result := checker.Check(context.Background(), configJSON, 10)
	if result.Status != "success" {
		t.Fatalf("expected success, got %s (err=%v)", result.Status, result.ErrorMessage)
	}
	if result.HTTPStatus == nil || *result.HTTPStatus != http.StatusOK {
		t.Fatalf("expected final HTTP status 200, got %#v", result.HTTPStatus)
	}
	if len(result.MetricsData) == 0 {
		t.Fatal("expected metrics_data")
	}

	var envelope struct {
		SyntheticAPI struct {
			Steps []struct {
				ID        string            `json:"id"`
				Status    string            `json:"status"`
				Extracted map[string]string `json:"extracted"`
			} `json:"steps"`
		} `json:"synthetic_api"`
	}
	if err := json.Unmarshal(result.MetricsData, &envelope); err != nil {
		t.Fatalf("failed to decode metrics_data: %v", err)
	}
	if len(envelope.SyntheticAPI.Steps) != 2 {
		t.Fatalf("expected 2 step metrics, got %d", len(envelope.SyntheticAPI.Steps))
	}
	if envelope.SyntheticAPI.Steps[0].Extracted["token"] != "[REDACTED]" {
		t.Fatalf("expected redacted extracted token, got %#v", envelope.SyntheticAPI.Steps[0].Extracted)
	}
}

func TestSyntheticAPIChecker_FailureModeContinue(t *testing.T) {
	var firstCalled int32
	var secondCalled int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/first":
			atomic.AddInt32(&firstCalled, 1)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
		case "/second":
			atomic.AddInt32(&secondCalled, 1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	checker := NewSyntheticAPIChecker()
	cfg := map[string]interface{}{
		"base_url":     server.URL,
		"failure_mode": "continue",
		"steps": []map[string]interface{}{
			{
				"id": "first",
				"request": map[string]interface{}{
					"method": "GET",
					"url":    "/first",
				},
				"assert": []map[string]interface{}{
					{"target": "status", "op": "equals", "value": 200},
				},
			},
			{
				"id": "second",
				"request": map[string]interface{}{
					"method": "GET",
					"url":    "/second",
				},
				"assert": []map[string]interface{}{
					{"target": "status", "op": "equals", "value": 200},
				},
			},
		},
	}
	configJSON, _ := json.Marshal(cfg)

	result := checker.Check(context.Background(), configJSON, 10)
	if result.Status != "failure" {
		t.Fatalf("expected failure, got %s", result.Status)
	}
	if atomic.LoadInt32(&firstCalled) != 1 || atomic.LoadInt32(&secondCalled) != 1 {
		t.Fatalf("expected both steps to run once, got first=%d second=%d", firstCalled, secondCalled)
	}
}

func TestSyntheticAPIChecker_MissingVariable(t *testing.T) {
	checker := NewSyntheticAPIChecker()
	cfg := map[string]interface{}{
		"base_url": "https://example.com",
		"steps": []map[string]interface{}{
			{
				"id": "needs-var",
				"request": map[string]interface{}{
					"method": "GET",
					"url":    "/ping",
					"headers": map[string]string{
						"Authorization": "Bearer {{missing_token}}",
					},
				},
			},
		},
	}
	configJSON, _ := json.Marshal(cfg)

	result := checker.Check(context.Background(), configJSON, 5)
	if result.Status != "error" {
		t.Fatalf("expected error, got %s", result.Status)
	}
	if result.ErrorMessage == nil || !strings.Contains(*result.ErrorMessage, "missing variable") {
		t.Fatalf("expected missing variable error, got %v", result.ErrorMessage)
	}
}
