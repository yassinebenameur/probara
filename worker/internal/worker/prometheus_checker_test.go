package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPrometheusChecker(t *testing.T) {
	cases := []struct{ name, body, status string }{
		{"scalar", `{"status":"success","data":{"resultType":"scalar","result":[1,"4"]}}`, "success"},
		{"threshold", `{"status":"success","data":{"resultType":"scalar","result":[1,"5"]}}`, "failure"},
		{"vector all", `{"status":"success","data":{"resultType":"vector","result":[{"value":[1,"1"]},{"value":[1,"4"]}]}}`, "success"},
		{"vector one fails", `{"status":"success","data":{"resultType":"vector","result":[{"value":[1,"1"]},{"value":[1,"6"]}]}}`, "failure"},
		{"empty", `{"status":"success","data":{"resultType":"vector","result":[]}}`, "failure"},
		{"nan", `{"status":"success","data":{"resultType":"scalar","result":[1,"NaN"]}}`, "error"},
		{"infinity", `{"status":"success","data":{"resultType":"scalar","result":[1,"+Inf"]}}`, "error"},
		{"matrix", `{"status":"success","data":{"resultType":"matrix","result":[]}}`, "error"},
		{"histogram", `{"status":"success","data":{"resultType":"vector","result":[{"histogram":[1,{}]}]}}`, "error"},
		{"null vector", `{"status":"success","data":{"resultType":"vector","result":null}}`, "error"},
		{"bad sample", `{"status":"success","data":{"resultType":"scalar","result":[null,"1"]}}`, "error"},
		{"malformed", `not json secret`, "error"},
		{"api error", `{"status":"error","error":"secret"}`, "error"},
		{"partial", `{"status":"success","warnings":["secret"],"data":{"resultType":"scalar","result":[1,"1"]}}`, "error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/prefix/api/v1/query" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				if r.Form.Get("query") != "sum(rate(errors[5m]))" || r.Form.Get("timeout") != "2s" {
					t.Errorf("bad query form %v", r.Form)
				}
				if r.Header.Get("Authorization") != "Bearer secret" {
					t.Error("missing authentication")
				}
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			cfg, _ := json.Marshal(map[string]any{"url": server.URL + "/prefix/", "query": "sum(rate(errors[5m]))", "operator": "lt", "threshold": 5, "auth_type": "bearer", "bearer_token": "secret"})
			got := NewPrometheusChecker(false, nil).Check(context.Background(), cfg, 2)
			if got.Status != tc.status {
				t.Fatalf("got %s: %v, want %s", got.Status, got.ErrorMessage, tc.status)
			}
			if got.ErrorMessage != nil && strings.Contains(*got.ErrorMessage, "secret") {
				t.Fatal("response leaked secret")
			}
		})
	}
}

func TestPrometheusNetworkAndNoData(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	for _, mode := range []string{"redirect", "large", "http error", "basic", "masked basic", "no data success", "no data error", "ssrf", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "redirect":
					http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
				case "large":
					fmt.Fprint(w, strings.Repeat("x", prometheusMaxResponseBytes+1))
				case "http error":
					http.Error(w, "secret", http.StatusUnauthorized)
				case "masked basic":
					t.Error("unresolved masked password reached the server")
				case "basic":
					u, p, ok := r.BasicAuth()
					if !ok || u != "user" || p != "secret" {
						t.Error("bad basic auth")
					}
					fmt.Fprint(w, `{"status":"success","data":{"resultType":"scalar","result":[1,"0"]}}`)
				default:
					fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
				}
			}))
			defer server.Close()
			config := map[string]any{"url": server.URL, "query": "up", "operator": "eq", "threshold": 0}
			want := "error"
			if mode == "basic" {
				config["auth_type"] = "basic"
				config["username"] = "user"
				config["password"] = "secret"
				want = "success"
			}
			if mode == "masked basic" {
				config["auth_type"] = "basic"
				config["username"] = "user"
				config["password"] = "***"
			}
			if mode == "no data success" {
				config["no_data_status"] = "success"
				want = "success"
			}
			if mode == "no data error" {
				config["no_data_status"] = "error"
			}
			raw, _ := json.Marshal(config)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			got := NewPrometheusChecker(mode == "ssrf", nil).Check(ctx, raw, 2)
			if got.Status != want {
				t.Fatalf("got %+v want %s", got, want)
			}
		})
	}
	if targetCalls.Load() != 0 {
		t.Fatal("followed credential-bearing redirect")
	}
}

func TestPrometheusEvaluatesBeyondPreviewLimit(t *testing.T) {
	rows := make([]string, 21)
	for i := range rows {
		rows[i] = `{"value":[1,"1"]}`
	}
	rows[20] = `{"value":[1,"10"]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[%s]}}`, strings.Join(rows, ","))
	}))
	defer server.Close()
	raw, _ := json.Marshal(map[string]any{"url": server.URL, "query": "up", "operator": "lt", "threshold": 5})
	got := NewPrometheusChecker(false, nil).Check(context.Background(), raw, 2)
	if got.Status != "failure" {
		t.Fatalf("got %+v", got)
	}
	var env struct {
		Prometheus prometheusMetrics `json:"prometheus"`
	}
	if err := json.Unmarshal(got.MetricsData, &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Prometheus.Values) != 20 || env.Prometheus.SampleCount != 21 || env.Prometheus.FailedCount != 1 {
		t.Fatalf("wrong metrics %+v", env)
	}
}

func TestPrometheusTimeoutAndTLSVerification(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(io.Discard, r.Body); <-r.Context().Done() }))
		defer server.Close()
		raw, _ := json.Marshal(map[string]any{"url": server.URL, "query": "up", "operator": "eq", "threshold": 1})
		result := NewPrometheusChecker(false, nil).Check(context.Background(), raw, 1)
		if result.Status != "error" || result.ErrorMessage == nil || !strings.Contains(*result.ErrorMessage, "timed out") {
			t.Fatalf("unexpected timeout result %+v", result)
		}
	})
	t.Run("untrusted TLS", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("request reached untrusted TLS server") }))
		defer server.Close()
		raw, _ := json.Marshal(map[string]any{"url": server.URL, "query": "up", "operator": "eq", "threshold": 1})
		if result := NewPrometheusChecker(false, nil).Check(context.Background(), raw, 2); result.Status != "error" {
			t.Fatalf("untrusted TLS accepted: %+v", result)
		}
	})
}
