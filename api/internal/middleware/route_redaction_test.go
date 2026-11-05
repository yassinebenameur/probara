package middleware

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/yassinebenameur/probara/shared/logger"
)

// tokenRouter mirrors the API's shape: logging, metrics and recovery on the
// root router, token-bearing routes inside nested sub-routers.
func tokenRouter(buf *bytes.Buffer) http.Handler {
	log := logger.New("route-redaction-test", "info")
	log.SetOutput(buf)
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

	r := chi.NewRouter()
	r.Use(RecoveryMiddleware(log))
	r.Use(LoggingMiddleware(log))
	r.Use(MetricsMiddleware("route-redaction-test"))
	r.Route("/api/v1", func(r chi.Router) {
		r.Route("/push", func(r chi.Router) {
			r.Get("/{token}", ok)
		})
		r.Route("/alert-actions", func(r chi.Router) {
			r.Get("/{token}", ok)
			r.Post("/{token}", ok)
		})
		r.Get("/oncall-feeds/{token}.ics", ok)
		r.Get("/vaults/{vaultID}/keys/{signingSecret}", ok)
		r.Get("/monitors/{id}", ok)
		r.Get("/boom/{token}", func(http.ResponseWriter, *http.Request) { panic("boom") })
	})
	return r
}

func TestTokenRoutesNeverReachLogsOrMetricLabels(t *testing.T) {
	const secret = "AQEsecretTOKENvalue_x-9"
	cases := []struct {
		name, method, path string
		wantPath           string
	}{
		{"push", http.MethodGet, "/api/v1/push/" + secret, "/api/v1/push/{token}"},
		{"action GET", http.MethodGet, "/api/v1/alert-actions/" + secret, "/api/v1/alert-actions/{token}"},
		{"action POST", http.MethodPost, "/api/v1/alert-actions/" + secret, "/api/v1/alert-actions/{token}"},
		// A link scanner's HEAD is a 405: chi records no URL params for it.
		{"action HEAD 405", http.MethodHead, "/api/v1/alert-actions/" + secret, "/api/v1/alert-actions/{token}"},
		// A mangled link 404s but still carries the real token.
		{"action 404 tail", http.MethodGet, "/api/v1/alert-actions/" + secret + "/extra", "/api/v1/alert-actions/{token}/extra"},
		{"feed", http.MethodGet, "/api/v1/oncall-feeds/" + secret + ".ics", "/api/v1/oncall-feeds/{token}.ics"},
		{"feed wrong suffix", http.MethodGet, "/api/v1/oncall-feeds/" + secret, "/api/v1/oncall-feeds/{token}.ics"},
		{"named secret", http.MethodGet, "/api/v1/vaults/v1/keys/" + secret, "/api/v1/vaults/v1/keys/{signingSecret}"},
		{"panic", http.MethodGet, "/api/v1/boom/" + secret, "/api/v1/boom/{token}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			h := tokenRouter(&buf)
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tc.method, tc.path, nil))

			if strings.Contains(buf.String(), secret) {
				t.Fatalf("token reached the log:\n%s", buf.String())
			}
			var sawPath bool
			for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
				var entry map[string]any
				if err := json.Unmarshal([]byte(line), &entry); err != nil {
					t.Fatalf("log line is not JSON: %q", line)
				}
				if entry["path"] == tc.wantPath {
					sawPath = true
				}
			}
			if !sawPath {
				t.Fatalf("no log line with path %q:\n%s", tc.wantPath, buf.String())
			}
		})
	}

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	routes := map[string]bool{}
	for _, f := range families {
		if !strings.HasPrefix(f.GetName(), "probara_api_http_") {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if strings.Contains(l.GetValue(), secret) {
					t.Fatalf("token reached metric %s label %s=%q", f.GetName(), l.GetName(), l.GetValue())
				}
				if l.GetName() == "route" {
					routes[l.GetValue()] = true
				}
			}
		}
	}
	for _, want := range []string{"/api/v1/alert-actions/{token}", "/api/v1/oncall-feeds/{token}.ics", "/api/v1/push/{token}"} {
		if !routes[want] {
			t.Errorf("no series with route=%q; have %v", want, routes)
		}
	}
}

func TestOrdinaryPathsKeepTheirValues(t *testing.T) {
	var buf bytes.Buffer
	h := tokenRouter(&buf)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/monitors/abc-123", nil))
	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["path"] != "/api/v1/monitors/abc-123" || entry["route"] != "/api/v1/monitors/{id}" {
		t.Fatalf("path=%v route=%v, want the raw path and the pattern", entry["path"], entry["route"])
	}
}

func TestRedactPathTemplates(t *testing.T) {
	tmpls := [][]string{
		strings.Split("/api/v1/push/{token}", "/"),
		strings.Split("/api/v1/oncall-feeds/{token}.ics", "/"),
	}
	for in, want := range map[string]string{
		"/api/v1/push/abc":            "/api/v1/push/{token}",
		"/api/v1/push/":               "/api/v1/push/",
		"/api/v1/push":                "/api/v1/push",
		"/api/v1/pushx/abc":           "/api/v1/pushx/abc",
		"/api/v1/oncall-feeds/a.ics":  "/api/v1/oncall-feeds/{token}.ics",
		"/api/v2/oncall-feeds/a.ics":  "/api/v2/oncall-feeds/a.ics",
		"/api/v1/push/abc/def?x=tail": "/api/v1/push/{token}/def?x=tail",
	} {
		if got := redactPath(in, tmpls); got != want {
			t.Errorf("redactPath(%q) = %q, want %q", in, got, want)
		}
	}
}
