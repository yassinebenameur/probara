package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ptr returns a pointer to the given value
func ptr[T any](v T) *T {
	return &v
}

func TestHTTPChecker_StatusCodeEvaluation(t *testing.T) {
	tests := []struct {
		name           string
		serverStatus   int
		expectedStatus *int // nil = default 2xx
		wantStatus     string
	}{
		{"default 200 is success", 200, nil, "success"},
		{"default 201 is success", 201, nil, "success"},
		{"default 204 is success", 204, nil, "success"},
		{"default 299 is success", 299, nil, "success"},
		{"default 300 is failure", 300, nil, "failure"},
		{"default 400 is failure", 400, nil, "failure"},
		{"default 404 is failure", 404, nil, "failure"},
		{"default 500 is failure", 500, nil, "failure"},
		{"custom 404 expected is success", 404, ptr(404), "success"},
		{"custom 500 expected is success", 500, ptr(500), "success"},
		{"custom 200 expected but got 201 is failure", 201, ptr(200), "failure"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.serverStatus)
			}))
			defer server.Close()

			checker := NewHTTPChecker(1024*1024, false, nil)

			config := map[string]interface{}{
				"url":    server.URL,
				"method": "GET",
			}
			if tt.expectedStatus != nil {
				config["expected_status"] = *tt.expectedStatus
			}

			configJSON, err := json.Marshal(config)
			if err != nil {
				t.Fatalf("failed to marshal config: %v", err)
			}

			result := checker.Check(context.Background(), configJSON, 10)

			if result.Status != tt.wantStatus {
				t.Errorf("Check() status = %v, want %v", result.Status, tt.wantStatus)
			}

			if result.HTTPStatus == nil {
				t.Error("HTTPStatus should not be nil")
			} else if *result.HTTPStatus != tt.serverStatus {
				t.Errorf("HTTPStatus = %v, want %v", *result.HTTPStatus, tt.serverStatus)
			}
		})
	}
}

func TestHTTPChecker_BodySubstringMatching(t *testing.T) {
	tests := []struct {
		name           string
		responseBody   string
		expectedSubstr *string
		wantStatus     string
		wantMatched    bool
	}{
		{"no substring check configured", "hello world", nil, "success", true},
		{"empty substring check", "hello world", ptr(""), "success", true},
		{"substring found", "hello world", ptr("world"), "success", true},
		{"substring not found", "hello world", ptr("goodbye"), "failure", false},
		{"exact match", "OK", ptr("OK"), "success", true},
		{"case sensitive - not found", "Hello World", ptr("hello"), "failure", false},
		{"partial match in middle", "the quick brown fox", ptr("quick"), "success", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(tt.responseBody))
			}))
			defer server.Close()

			checker := NewHTTPChecker(1024*1024, false, nil)

			config := map[string]interface{}{
				"url":    server.URL,
				"method": "GET",
			}
			if tt.expectedSubstr != nil {
				config["expected_body_substring"] = *tt.expectedSubstr
			}

			configJSON, err := json.Marshal(config)
			if err != nil {
				t.Fatalf("failed to marshal config: %v", err)
			}

			result := checker.Check(context.Background(), configJSON, 10)

			if result.Status != tt.wantStatus {
				t.Errorf("Check() status = %v, want %v", result.Status, tt.wantStatus)
			}

			if result.MatchedBodySubstring != tt.wantMatched {
				t.Errorf("MatchedBodySubstring = %v, want %v", result.MatchedBodySubstring, tt.wantMatched)
			}
		})
	}
}

func TestHTTPChecker_LatencyMeasurement(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	checker := NewHTTPChecker(1024*1024, false, nil)

	config := map[string]interface{}{
		"url":    server.URL,
		"method": "GET",
	}
	configJSON, _ := json.Marshal(config)

	result := checker.Check(context.Background(), configJSON, 10)

	if result.Status != "success" {
		t.Errorf("Check() status = %v, want success", result.Status)
	}

	if result.LatencyMs == nil {
		t.Fatal("LatencyMs should not be nil")
	}

	// Latency should be at least 50ms due to sleep
	if *result.LatencyMs < 50 {
		t.Errorf("LatencyMs = %v, want >= 50", *result.LatencyMs)
	}
}

func TestHTTPChecker_InvalidConfig(t *testing.T) {
	checker := NewHTTPChecker(1024*1024, false, nil)

	// Invalid JSON
	result := checker.Check(context.Background(), []byte("not valid json"), 10)

	if result.Status != "error" {
		t.Errorf("Check() status = %v, want error", result.Status)
	}

	if result.ErrorMessage == nil {
		t.Error("ErrorMessage should not be nil for invalid config")
	}
}

func TestHTTPChecker_InvalidURL(t *testing.T) {
	checker := NewHTTPChecker(1024*1024, false, nil)

	config := map[string]interface{}{
		"url":    "://invalid-url",
		"method": "GET",
	}
	configJSON, _ := json.Marshal(config)

	result := checker.Check(context.Background(), configJSON, 10)

	if result.Status != "error" {
		t.Errorf("Check() status = %v, want error", result.Status)
	}

	if result.ErrorMessage == nil {
		t.Error("ErrorMessage should not be nil for invalid URL")
	}
}

func TestHTTPChecker_ConnectionRefused(t *testing.T) {
	checker := NewHTTPChecker(1024*1024, false, nil)

	// Use a port that's almost certainly not listening
	config := map[string]interface{}{
		"url":    "http://127.0.0.1:59999",
		"method": "GET",
	}
	configJSON, _ := json.Marshal(config)

	result := checker.Check(context.Background(), configJSON, 2)

	if result.Status != "error" {
		t.Errorf("Check() status = %v, want error", result.Status)
	}

	if result.ErrorMessage == nil {
		t.Error("ErrorMessage should not be nil for connection refused")
	}

	// Should still have latency measurement
	if result.LatencyMs == nil {
		t.Error("LatencyMs should be set even on error")
	}
}

func TestHTTPChecker_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep longer than the timeout
		time.Sleep(3 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	checker := NewHTTPChecker(1024*1024, false, nil)

	config := map[string]interface{}{
		"url":    server.URL,
		"method": "GET",
	}
	configJSON, _ := json.Marshal(config)

	// Use a short timeout
	result := checker.Check(context.Background(), configJSON, 1)

	if result.Status != "error" {
		t.Errorf("Check() status = %v, want error", result.Status)
	}

	if result.ErrorMessage == nil {
		t.Error("ErrorMessage should not be nil for timeout")
	}
}

func TestHTTPChecker_CustomHeaders(t *testing.T) {
	var receivedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	checker := NewHTTPChecker(1024*1024, false, nil)

	config := map[string]interface{}{
		"url":    server.URL,
		"method": "GET",
		"headers": map[string]string{
			"X-Custom-Header": "test-value",
			"Authorization":   "Bearer token123",
		},
	}
	configJSON, _ := json.Marshal(config)

	result := checker.Check(context.Background(), configJSON, 10)

	if result.Status != "success" {
		t.Errorf("Check() status = %v, want success", result.Status)
	}

	if receivedHeaders.Get("X-Custom-Header") != "test-value" {
		t.Errorf("X-Custom-Header = %v, want test-value", receivedHeaders.Get("X-Custom-Header"))
	}

	if receivedHeaders.Get("Authorization") != "Bearer token123" {
		t.Errorf("Authorization = %v, want Bearer token123", receivedHeaders.Get("Authorization"))
	}
}

func TestHTTPChecker_RequestBody(t *testing.T) {
	var receivedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 1024)
		n, _ := r.Body.Read(body)
		receivedBody = string(body[:n])
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	checker := NewHTTPChecker(1024*1024, false, nil)

	requestBody := `{"key": "value"}`
	config := map[string]interface{}{
		"url":    server.URL,
		"method": "POST",
		"body":   requestBody,
	}
	configJSON, _ := json.Marshal(config)

	result := checker.Check(context.Background(), configJSON, 10)

	if result.Status != "success" {
		t.Errorf("Check() status = %v, want success", result.Status)
	}

	if receivedBody != requestBody {
		t.Errorf("received body = %v, want %v", receivedBody, requestBody)
	}
}

func TestHTTPChecker_HTTPMethods(t *testing.T) {
	methods := []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			var receivedMethod string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedMethod = r.Method
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			checker := NewHTTPChecker(1024*1024, false, nil)

			config := map[string]interface{}{
				"url":    server.URL,
				"method": method,
			}
			configJSON, _ := json.Marshal(config)

			result := checker.Check(context.Background(), configJSON, 10)

			if result.Status != "success" {
				t.Errorf("Check() status = %v, want success", result.Status)
			}

			if receivedMethod != method {
				t.Errorf("received method = %v, want %v", receivedMethod, method)
			}
		})
	}
}

func TestHTTPChecker_CombinedStatusAndBodyCheck(t *testing.T) {
	tests := []struct {
		name           string
		serverStatus   int
		responseBody   string
		expectedStatus *int
		expectedSubstr *string
		wantStatus     string
	}{
		{
			name:           "both pass",
			serverStatus:   200,
			responseBody:   "OK",
			expectedStatus: ptr(200),
			expectedSubstr: ptr("OK"),
			wantStatus:     "success",
		},
		{
			name:           "status passes, body fails",
			serverStatus:   200,
			responseBody:   "ERROR",
			expectedStatus: ptr(200),
			expectedSubstr: ptr("OK"),
			wantStatus:     "failure",
		},
		{
			name:           "status fails, body passes",
			serverStatus:   500,
			responseBody:   "OK",
			expectedStatus: ptr(200),
			expectedSubstr: ptr("OK"),
			wantStatus:     "failure",
		},
		{
			name:           "both fail",
			serverStatus:   500,
			responseBody:   "ERROR",
			expectedStatus: ptr(200),
			expectedSubstr: ptr("OK"),
			wantStatus:     "failure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.serverStatus)
				w.Write([]byte(tt.responseBody))
			}))
			defer server.Close()

			checker := NewHTTPChecker(1024*1024, false, nil)

			config := map[string]interface{}{
				"url":    server.URL,
				"method": "GET",
			}
			if tt.expectedStatus != nil {
				config["expected_status"] = *tt.expectedStatus
			}
			if tt.expectedSubstr != nil {
				config["expected_body_substring"] = *tt.expectedSubstr
			}

			configJSON, _ := json.Marshal(config)

			result := checker.Check(context.Background(), configJSON, 10)

			if result.Status != tt.wantStatus {
				t.Errorf("Check() status = %v, want %v", result.Status, tt.wantStatus)
			}
		})
	}
}

func TestHTTPChecker_StatusRules_ListRangeClass(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusFound) // 302
		w.Write([]byte("redirect"))
	}))
	defer server.Close()

	checker := NewHTTPChecker(1024*1024, false, nil)

	tests := []struct {
		name   string
		config map[string]interface{}
		want   string
	}{
		{
			name: "class 3xx",
			config: map[string]interface{}{
				"url":                     server.URL,
				"method":                  "GET",
				"expected_status_classes": []string{"3xx"},
			},
			want: "success",
		},
		{
			name: "range 300-399",
			config: map[string]interface{}{
				"url":                    server.URL,
				"method":                 "GET",
				"expected_status_ranges": []map[string]int{{"min": 300, "max": 399}},
			},
			want: "success",
		},
		{
			name: "list [302]",
			config: map[string]interface{}{
				"url":               server.URL,
				"method":            "GET",
				"expected_statuses": []int{302},
			},
			want: "success",
		},
		{
			name: "default expects 2xx -> failure",
			config: map[string]interface{}{
				"url":    server.URL,
				"method": "GET",
			},
			want: "failure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configJSON, _ := json.Marshal(tt.config)
			result := checker.Check(context.Background(), configJSON, 10)
			if result.Status != tt.want {
				t.Errorf("Check() status = %v, want %v", result.Status, tt.want)
			}
		})
	}
}

func TestHTTPChecker_ResponseHeaderAssertions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	checker := NewHTTPChecker(1024*1024, false, nil)

	config := map[string]interface{}{
		"url":    server.URL,
		"method": "GET",
		"response_header_assertions": []map[string]interface{}{
			{"name": "Content-Type", "op": "contains", "value": "application/json"},
		},
	}
	configJSON, _ := json.Marshal(config)

	result := checker.Check(context.Background(), configJSON, 10)
	if result.Status != "success" {
		t.Errorf("Check() status = %v, want success", result.Status)
	}

	badConfig := map[string]interface{}{
		"url":    server.URL,
		"method": "GET",
		"response_header_assertions": []map[string]interface{}{
			{"name": "Content-Type", "op": "equals", "value": "text/plain"},
		},
	}
	badJSON, _ := json.Marshal(badConfig)
	badResult := checker.Check(context.Background(), badJSON, 10)
	if badResult.Status != "failure" {
		t.Errorf("Check() status = %v, want failure", badResult.Status)
	}
	if badResult.ErrorMessage == nil || !strings.Contains(*badResult.ErrorMessage, "header") {
		t.Errorf("Expected failure error message to mention header assertion, got %v", badResult.ErrorMessage)
	}
}

func TestHTTPChecker_JSONAssertions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok","latency":123,"active":true}`))
	}))
	defer server.Close()

	checker := NewHTTPChecker(1024*1024, false, nil)

	config := map[string]interface{}{
		"url":    server.URL,
		"method": "GET",
		"json_assertions": []map[string]interface{}{
			{"path": "status", "op": "equals", "value": "ok"},
			{"path": "latency", "op": "number_gt", "value": "100"},
			{"path": "active", "op": "bool_is", "value": "true"},
		},
	}
	configJSON, _ := json.Marshal(config)

	result := checker.Check(context.Background(), configJSON, 10)
	if result.Status != "success" {
		t.Errorf("Check() status = %v, want success", result.Status)
	}

	invalidJSONServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not json"))
	}))
	defer invalidJSONServer.Close()

	invalidConfig := map[string]interface{}{
		"url":    invalidJSONServer.URL,
		"method": "GET",
		"json_assertions": []map[string]interface{}{
			{"path": "status", "op": "exists"},
		},
	}
	invalidCfgJSON, _ := json.Marshal(invalidConfig)
	invalidResult := checker.Check(context.Background(), invalidCfgJSON, 10)
	if invalidResult.Status != "failure" {
		t.Errorf("Check() status = %v, want failure", invalidResult.Status)
	}
	if invalidResult.ErrorMessage == nil || !strings.Contains(*invalidResult.ErrorMessage, "json") {
		t.Errorf("Expected failure error message to mention JSON, got %v", invalidResult.ErrorMessage)
	}
}

func TestHTTPChecker_MaxLatencyThreshold(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))
	defer server.Close()

	checker := NewHTTPChecker(1024*1024, false, nil)

	config := map[string]interface{}{
		"url":            server.URL,
		"method":         "GET",
		"max_latency_ms": 10,
	}
	configJSON, _ := json.Marshal(config)

	result := checker.Check(context.Background(), configJSON, 10)
	if result.Status != "failure" {
		t.Errorf("Check() status = %v, want failure", result.Status)
	}
	if result.ErrorMessage == nil || !strings.Contains(*result.ErrorMessage, "latency") {
		t.Errorf("Expected failure error message to mention latency, got %v", result.ErrorMessage)
	}
}

func TestHTTPChecker_TLSInfoAndExpiryCheck(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))
	defer server.Close()

	checker := NewHTTPChecker(1024*1024, false, nil)

	config := map[string]interface{}{
		"url":             server.URL,
		"method":          "GET",
		"tls_skip_verify": true,
	}
	configJSON, _ := json.Marshal(config)

	result := checker.Check(context.Background(), configJSON, 10)
	if result.Status != "success" {
		t.Errorf("Check() status = %v, want success", result.Status)
	}
	if len(result.MetricsData) == 0 {
		t.Fatalf("Expected MetricsData to be set")
	}

	var env struct {
		HTTP struct {
			TLS *struct {
				NotAfter        string `json:"not_after"`
				DaysUntilExpiry *int   `json:"days_until_expiry"`
			} `json:"tls"`
		} `json:"http"`
	}
	if err := json.Unmarshal(result.MetricsData, &env); err != nil {
		t.Fatalf("failed to unmarshal metrics_data: %v", err)
	}
	if env.HTTP.TLS == nil || env.HTTP.TLS.NotAfter == "" {
		t.Fatalf("Expected TLS info with not_after, got %#v", env)
	}
	if env.HTTP.TLS.DaysUntilExpiry == nil {
		t.Fatalf("Expected TLS info with days_until_expiry, got %#v", env)
	}

	expiryFailConfig := map[string]interface{}{
		"url":                server.URL,
		"method":             "GET",
		"tls_skip_verify":    true,
		"tls_min_days_valid": *env.HTTP.TLS.DaysUntilExpiry + 1,
	}
	expiryFailJSON, _ := json.Marshal(expiryFailConfig)
	expiryFailResult := checker.Check(context.Background(), expiryFailJSON, 10)
	if expiryFailResult.Status != "failure" {
		t.Errorf("Check() status = %v, want failure", expiryFailResult.Status)
	}
	if expiryFailResult.ErrorMessage == nil || !strings.Contains(*expiryFailResult.ErrorMessage, "tls") {
		t.Errorf("Expected failure error message to mention tls, got %v", expiryFailResult.ErrorMessage)
	}
}

func TestAgentChecker_AlwaysSuccess(t *testing.T) {
	checker := NewAgentChecker(nil)

	config := map[string]interface{}{}
	configJSON, _ := json.Marshal(config)

	result := checker.Check(context.Background(), configJSON, 10)

	if result.Status != "success" {
		t.Errorf("AgentChecker.Check() status = %v, want success", result.Status)
	}
}
