package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	health "google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
)

func TestHTTPChecker_SendsSecretHeadersAndBasicAuth(t *testing.T) {
	var got http.Header
	var user, pass string
	var hasBasic bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		user, pass, hasBasic = r.BasicAuth()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	config, _ := json.Marshal(map[string]any{
		"url":                 server.URL,
		"method":              "GET",
		"headers":             map[string]string{"Accept": "application/json"},
		"secret_headers":      map[string]string{"X-Api-Key": "key-1"},
		"basic_auth_username": "ops",
		"basic_auth_password": "pw",
	})
	result := NewHTTPChecker(1<<20, false, nil).Check(context.Background(), config, 5)
	if result.Status != "success" {
		t.Fatalf("status = %s (%v)", result.Status, result.ErrorMessage)
	}
	if got.Get("Accept") != "application/json" || got.Get("X-Api-Key") != "key-1" {
		t.Fatalf("headers received = %v", got)
	}
	if !hasBasic || user != "ops" || pass != "pw" {
		t.Fatalf("basic auth = %v %q %q", hasBasic, user, pass)
	}
}

func TestGRPCChecker_SendsMetadata(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var mu sync.Mutex
	var received metadata.MD
	server := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		mu.Lock()
		received = md
		mu.Unlock()
		return handler(ctx, req)
	}))
	healthSrv := health.NewServer()
	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(server, healthSrv)
	go func() { _ = server.Serve(lis) }()
	defer server.Stop()

	host, portStr, _ := net.SplitHostPort(lis.Addr().String())
	port, _ := strconv.Atoi(portStr)
	config, _ := json.Marshal(map[string]any{
		"host": host, "port": port, "use_tls": false,
		"metadata":        map[string]string{"X-Tenant": "t1"},
		"secret_metadata": map[string]string{"authorization": "Bearer g"},
	})
	result := NewGRPCChecker(false, nil).Check(context.Background(), config, 5)
	if result.Status != "success" {
		t.Fatalf("status = %s (%v)", result.Status, result.ErrorMessage)
	}
	mu.Lock()
	defer mu.Unlock()
	if v := received.Get("x-tenant"); len(v) != 1 || v[0] != "t1" {
		t.Fatalf("x-tenant = %v (md %v)", v, received)
	}
	if v := received.Get("authorization"); len(v) != 1 || v[0] != "Bearer g" {
		t.Fatalf("authorization = %v", v)
	}
	if strings.Contains(string(result.MetricsData), "Bearer g") {
		t.Fatalf("metrics leak secret metadata: %s", result.MetricsData)
	}
}

// TestSyntheticAPIChecker_RedactsSecretVariables puts a secret everywhere a
// check reports text — the resolved step URL, a header the server echoes into
// an assertion failure, an extracted value — and requires none of it to
// survive while the request itself carried the real value.
func TestSyntheticAPIChecker_RedactsSecretVariables(t *testing.T) {
	const secret = "s3cr3t-T0ken_9"
	var gotAuth, gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.Query().Get("key")
		w.Header().Set("X-Echo", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"echo":%q}`, gotQuery)
	}))
	defer server.Close()

	config, _ := json.Marshal(map[string]any{
		"failure_mode":     "continue",
		"variables":        map[string]string{"base": server.URL},
		"secret_variables": map[string]string{"token": secret},
		"steps": []map[string]any{{
			"id": "s1",
			"request": map[string]any{
				"method":  "GET",
				"url":     "{{base}}/items?key={{token}}",
				"headers": map[string]string{"Authorization": "Bearer {{token}}"},
			},
			"assert":  []map[string]any{{"target": "header", "path": "X-Echo", "op": "equals", "value": "nope"}},
			"extract": []map[string]any{{"name": "echoed", "from": "json", "path": "echo"}},
		}},
	})
	result := NewSyntheticAPIChecker().Check(context.Background(), config, 5)

	if gotAuth != "Bearer "+secret || gotQuery != secret {
		t.Fatalf("request did not carry the secret: auth=%q query=%q", gotAuth, gotQuery)
	}
	if result.Status != "failure" {
		t.Fatalf("status = %s (%v)", result.Status, result.ErrorMessage)
	}
	reported := string(result.MetricsData)
	if result.ErrorMessage != nil {
		reported += *result.ErrorMessage
	}
	for _, form := range []string{secret, "s3cr3t"} {
		if strings.Contains(reported, form) {
			t.Fatalf("result leaks %q: %s", form, reported)
		}
	}
	if !strings.Contains(reported, syntheticRedactedValue) {
		t.Fatalf("expected the redaction token in the result: %s", reported)
	}
}

func TestSecretRedactor(t *testing.T) {
	if newSecretRedactor(nil) != nil || newSecretRedactor(map[string]string{"a": ""}) != nil {
		t.Fatal("no secrets must mean no redactor")
	}
	var nilRedactor *secretRedactor
	if nilRedactor.redact("keep") != "keep" {
		t.Fatal("nil redactor must pass text through")
	}

	r := newSecretRedactor(map[string]string{"short": "ab", "long": "ab\"cd"})
	if got := r.redact(`value ab"cd and "ab\"cd" and ab`); strings.Contains(got, "ab") {
		t.Fatalf("redact = %q", got)
	}

	msg := "failed at https://x.test/?p=ab"
	metrics := json.RawMessage(`{"synthetic_browser":{"start_url":"https://x.test/?p=ab","total_latency_ms":12,"steps":[{"selector":"#ab","error":"ab"}],"artifacts":{"screenshot_path":"/tmp/ab.png","warnings":["ab"]}}}`)
	out := r.redactResult(CheckResult{Status: "failure", ErrorMessage: &msg, MetricsData: metrics})
	if strings.Contains(*out.ErrorMessage, "ab") || strings.Contains(string(out.MetricsData), "ab") {
		t.Fatalf("redactResult leaks: %s / %s", *out.ErrorMessage, out.MetricsData)
	}
	if !strings.Contains(string(out.MetricsData), `"total_latency_ms":12`) {
		t.Fatalf("numbers must survive redaction exactly: %s", out.MetricsData)
	}

	// URL-escaped forms of a value are redacted too.
	escaped := newSecretRedactor(map[string]string{"t": "a b/+c"})
	for _, form := range []string{"a b/+c", "a+b%2F%2Bc", "a%20b%2F+c"} {
		if got := escaped.redact("x=" + form); got != "x="+syntheticRedactedValue {
			t.Fatalf("redact(%q) = %q", form, got)
		}
	}

	bad := r.redactResult(CheckResult{MetricsData: json.RawMessage(`{not json ab`)})
	if bad.MetricsData != nil {
		t.Fatalf("undecodable metrics must be dropped, got %s", bad.MetricsData)
	}
}
