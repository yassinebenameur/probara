package api

import (
	"bytes"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
)

// TestRealRouterRedactsPathTokens drives the real router's token routes with
// methods they do not serve (405 — chi records no URL params, and nothing
// touches the unreachable database) and checks the request log never holds
// the token. The generic cases, metrics included, are in the middleware
// package (route_redaction_test.go).
func TestRealRouterRedactsPathTokens(t *testing.T) {
	sqlDB, err := sql.Open("postgres", "postgres://unused@127.0.0.1:1/unused?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	cfg := &config.APIConfig{}
	cfg.ServiceName = "api-test"
	cfg.NATSURL = "nats://127.0.0.1:1"
	var buf bytes.Buffer
	log := logger.New("api-test", "info")
	log.SetOutput(&buf)
	h := NewServer(cfg, log, metrics.NewRegistry("api-test"), &db.Client{DB: sqlDB}).http.Handler

	const secret = "AQEsecretTOKENvalue_x-9"
	for _, path := range []string{"/api/v1/alert-actions/", "/api/v1/push/"} {
		buf.Reset()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, path+secret, nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("PUT %s… = %d, want 405", path, rec.Code)
		}
		if strings.Contains(buf.String(), secret) {
			t.Fatalf("token reached the request log:\n%s", buf.String())
		}
		if !strings.Contains(buf.String(), `"path":"`+path+`{token}"`) {
			t.Fatalf("no redacted request line for %s:\n%s", path, buf.String())
		}
	}
}
