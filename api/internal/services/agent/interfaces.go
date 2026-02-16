package agent

import (
	"context"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/models"
)

// AgentService defines the interface for agent operations
type AgentService interface {
	// ProcessMetrics processes incoming agent metrics and stores them as check results
	ProcessMetrics(ctx context.Context, payload models.AgentMetricsPayload, tenantID uuid.UUID) error

	// GetMonitorByAgentID retrieves a monitor by its agent ID
	GetMonitorByAgentID(ctx context.Context, agentID string, tenantID uuid.UUID) (uuid.UUID, error)

	// GenerateInstallCommand generates installation instructions for an agent
	GenerateInstallCommand(ctx context.Context, monitorID, tenantID uuid.UUID, backendURL, apiKey string) (*models.AgentInstallCommand, error)
}

// Ensure Service implements AgentService
var _ AgentService = (*Service)(nil)
