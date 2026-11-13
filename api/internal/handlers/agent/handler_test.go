package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/models"
)

func TestHandleLegacyMetricsGoneIsATombstone(t *testing.T) {
	handler := NewHandler(panicService{}, logger.New("agent-handler-test", "fatal"), "")
	body := &trackingReader{r: strings.NewReader(`{"agent_id":"old-agent","metrics":{"cpu_percent":12}}`)}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/metrics", body)
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), uuid.NewString()))
	rec := httptest.NewRecorder()

	handler.HandleLegacyMetricsGone(rec, req)

	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusGone, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	if link := rec.Header().Get("Link"); !strings.Contains(link, `</api/v1/otlp/v1/metrics>; rel="successor-version"`) {
		t.Fatalf("Link = %q, want the OTLP successor-version", link)
	}
	for _, h := range []string{"Deprecation", "Sunset"} {
		if v := rec.Header().Get(h); v != "" {
			t.Fatalf("%s header = %q, want none after the sunset", h, v)
		}
	}
	var resp struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body is not JSON: %v (%s)", err, rec.Body.String())
	}
	if !strings.Contains(resp.Error, "/api/v1/otlp/v1/metrics") {
		t.Fatalf("error %q does not name the OTLP endpoint", resp.Error)
	}
	if body.read {
		t.Fatal("tombstone read the request body")
	}
}

type trackingReader struct {
	r    io.Reader
	read bool
}

func (t *trackingReader) Read(p []byte) (int, error) {
	t.read = true
	return t.r.Read(p)
}

// panicService fails the test run if the tombstone reaches the service (and
// through it the database).
type panicService struct{}

func (panicService) GetMonitorByAgentID(context.Context, string, uuid.UUID) (uuid.UUID, error) {
	panic("tombstone must not reach the agent service")
}

func (panicService) GenerateInstallCommand(context.Context, uuid.UUID, uuid.UUID, string, string) (*models.AgentInstallCommand, error) {
	panic("tombstone must not reach the agent service")
}

func (panicService) GenerateCollectorConfig(context.Context, uuid.UUID, uuid.UUID, string, string) (string, error) {
	panic("tombstone must not reach the agent service")
}

func TestEmbeddableAPIKeyRequiresWriteScopeAPIKey(t *testing.T) {
	cases := []struct {
		name  string
		actor string
		scope string
		want  string
	}{
		{"write-scope api key", ctxpkg.ActorTypeAPIKey, "write", "pk_secret"},
		{"read-scope api key", ctxpkg.ActorTypeAPIKey, "read", ""},
		{"api key without scope", ctxpkg.ActorTypeAPIKey, "", ""},
		{"user session", ctxpkg.ActorTypeAdminUser, "write", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/monitors/x/agent/install/script.sh", nil)
			req.Header.Set("Authorization", "Bearer pk_secret")
			ctx := ctxpkg.WithActorType(req.Context(), tc.actor)
			if tc.scope != "" {
				ctx = ctxpkg.WithAuthScope(ctx, tc.scope)
			}
			if got := embeddableAPIKey(req.WithContext(ctx)); got != tc.want {
				t.Fatalf("embeddableAPIKey = %q, want %q", got, tc.want)
			}
		})
	}
}
