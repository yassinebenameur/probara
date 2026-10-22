package agent

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	agentservice "github.com/yassinebenameur/probara/api/internal/services/agent"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/models"
)

func TestHandleReceiveMetricsReturnsGoneWhenAgentUnavailable(t *testing.T) {
	tenantID := uuid.New()
	handler := NewHandler(agentUnavailableService{}, logger.New("agent-handler-test", "fatal"), "")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/metrics", bytes.NewBufferString(`{
		"agent_id": "deleted-agent",
		"metrics": {
			"timestamp": "2026-05-22T12:00:00Z"
		}
	}`))
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), tenantID.String()))
	rec := httptest.NewRecorder()

	handler.HandleReceiveMetrics(rec, req)

	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusGone, rec.Body.String())
	}
}

type agentUnavailableService struct{}

func (agentUnavailableService) ProcessMetrics(context.Context, models.AgentMetricsPayload, uuid.UUID) error {
	return agentservice.ErrAgentUnavailable
}

func (agentUnavailableService) GetMonitorByAgentID(context.Context, string, uuid.UUID) (uuid.UUID, error) {
	return uuid.Nil, nil
}

func (agentUnavailableService) GenerateInstallCommand(context.Context, uuid.UUID, uuid.UUID, string, string) (*models.AgentInstallCommand, error) {
	return nil, nil
}

func (agentUnavailableService) GenerateCollectorConfig(context.Context, uuid.UUID, uuid.UUID, string, string) (string, error) {
	return "", nil
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
