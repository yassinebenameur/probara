package agent

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/models"
)

// Service handles agent-related business logic
type Service struct {
	db *sql.DB
}

// NewService creates a new agent service
func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

// GetMonitorByAgentID retrieves a monitor by its agent ID
func (s *Service) GetMonitorByAgentID(ctx context.Context, agentID string, tenantID uuid.UUID) (uuid.UUID, error) {
	var monitorID uuid.UUID
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM monitors
		 WHERE agent_id = $1 AND tenant_id = $2 AND type = 'agent' AND deleted_at IS NULL`,
		agentID, tenantID,
	).Scan(&monitorID)
	if err != nil {
		return uuid.Nil, err
	}
	return monitorID, nil
}

// GenerateInstallCommand generates OpenTelemetry Collector installation
// instructions for an agent monitor: per-OS install/uninstall scripts (which
// also remove any legacy probara-agent install) and the generated collector
// config (linux variant; per-platform variants via GenerateCollectorConfig).
func (s *Service) GenerateInstallCommand(ctx context.Context, monitorID, tenantID uuid.UUID, backendURL, apiKey string) (*models.AgentInstallCommand, error) {
	agentID, intervalSeconds, err := s.lookupAgentMonitor(ctx, monitorID, tenantID)
	if err != nil {
		return nil, err
	}

	return &models.AgentInstallCommand{
		AgentID:                agentID,
		BackendURL:             backendURL,
		InstallScript:          buildCollectorUnixInstallScript(backendURL, agentID, apiKey, intervalSeconds),
		WindowsInstallScript:   buildCollectorWindowsInstallScript(backendURL, agentID, apiKey, intervalSeconds),
		UninstallScript:        buildCollectorUnixUninstallScript(),
		WindowsUninstallScript: buildCollectorWindowsUninstallScript(),
		CollectorConfig:        buildCollectorConfig(backendURL, intervalSeconds, "linux"),
		CollectorVersion:       CollectorVersion,
		DownloadURL:            fmt.Sprintf("%s/static/collector/", backendURL),
		IntervalSeconds:        intervalSeconds,
	}, nil
}

// GenerateCollectorConfig renders the collector YAML for one platform
// (linux|darwin|windows) — the config-management path (Ansible/Chef, stock
// otelcol-contrib) behind GET /monitors/{id}/agent/config.yaml.
func (s *Service) GenerateCollectorConfig(ctx context.Context, monitorID, tenantID uuid.UUID, backendURL, platform string) (string, error) {
	switch platform {
	case "linux", "darwin", "windows":
	case "":
		platform = "linux"
	default:
		return "", fmt.Errorf("unsupported platform %q", platform)
	}
	_, intervalSeconds, err := s.lookupAgentMonitor(ctx, monitorID, tenantID)
	if err != nil {
		return "", err
	}
	return buildCollectorConfig(backendURL, intervalSeconds, platform), nil
}

func (s *Service) lookupAgentMonitor(ctx context.Context, monitorID, tenantID uuid.UUID) (string, int, error) {
	var agentID sql.NullString
	var intervalSeconds int
	err := s.db.QueryRowContext(ctx,
		`SELECT agent_id, interval_seconds FROM monitors
		 WHERE id = $1 AND tenant_id = $2 AND type = 'agent' AND deleted_at IS NULL`,
		monitorID, tenantID,
	).Scan(&agentID, &intervalSeconds)
	if err != nil {
		return "", 0, fmt.Errorf("failed to get monitor: %w", err)
	}
	if !agentID.Valid {
		return "", 0, fmt.Errorf("monitor does not have an agent_id")
	}
	return agentID.String, intervalSeconds, nil
}
