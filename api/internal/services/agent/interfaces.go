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

	// GenerateInstallCommand generates OTel Collector installation
	// instructions for an agent monitor
	GenerateInstallCommand(ctx context.Context, monitorID, tenantID uuid.UUID, backendURL, apiKey string) (*models.AgentInstallCommand, error)

	// GenerateCollectorConfig renders the collector YAML for one platform
	// (the config-management/stock-otelcol path)
	GenerateCollectorConfig(ctx context.Context, monitorID, tenantID uuid.UUID, backendURL, platform string) (string, error)
}

// Ensure Service implements AgentService
var _ AgentService = (*Service)(nil)
