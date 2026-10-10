package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/monitorstate"
	"github.com/yassinebenameur/probara/shared/statusupdates"
)

var (
	ErrAgentUnavailable = errors.New("agent monitor not found")
	ErrAgentDisabled    = errors.New("agent monitor disabled")
)

// Service handles agent-related business logic
type Service struct {
	db        *sql.DB
	publisher *statusupdates.Publisher
}

// NewService creates a new agent service
func NewService(db *sql.DB, publisher *statusupdates.Publisher) *Service {
	return &Service{db: db, publisher: publisher}
}

// ProcessMetrics processes incoming agent metrics and stores them as check results
func (s *Service) ProcessMetrics(ctx context.Context, payload models.AgentMetricsPayload, tenantID uuid.UUID) error {
	// Verify that the agent exists and belongs to the tenant
	var monitorID uuid.UUID
	var monitorName string
	var enabled bool
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, enabled FROM monitors
		 WHERE agent_id = $1 AND tenant_id = $2 AND type = 'agent' AND deleted_at IS NULL`,
		payload.AgentID, tenantID,
	).Scan(&monitorID, &monitorName, &enabled)
	if err == sql.ErrNoRows {
		return fmt.Errorf("%w: %s", ErrAgentUnavailable, payload.AgentID)
	}
	if err != nil {
		return fmt.Errorf("failed to lookup agent: %w", err)
	}
	if !enabled {
		return fmt.Errorf("%w: %s", ErrAgentDisabled, payload.AgentID)
	}

	// Determine status based on metrics
	// For now, we always mark it as success if we received metrics
	// In the future, we can add threshold-based status determination
	status := "success"

	// Calculate latency (time since metrics were collected)
	latencyMs := int(time.Since(payload.Metrics.Timestamp).Milliseconds())
	if latencyMs < 0 {
		latencyMs = 0
	}

	// Marshal metrics to JSON
	metricsJSON, err := json.Marshal(payload.Metrics)
	if err != nil {
		return fmt.Errorf("failed to marshal metrics: %w", err)
	}

	// Generate a job ID for this metrics report
	jobID := uuid.New()

	// Insert the check result and advance the monitor state machine in one
	// transaction so dashboard health reflects agent reports.
	latency := int64(latencyMs)
	if _, err := monitorstate.Record(ctx, s.db, monitorstate.Result{
		MonitorID:    monitorID,
		TenantID:     tenantID,
		JobID:        jobID,
		Status:       status,
		ResultSource: string(models.ResultSourceMonitor),
		LatencyMs:    &latency,
		MetricsData:  metricsJSON,
		StartedAt:    payload.Metrics.Timestamp,
		CompletedAt:  time.Now(),
	}); err != nil {
		return fmt.Errorf("failed to record check result: %w", err)
	}

	s.publishStatusUpdate(monitorID, tenantID)
	return nil
}

func (s *Service) publishStatusUpdate(monitorID, tenantID uuid.UUID) {
	if s.publisher == nil {
		return
	}
	event := statusupdates.Event{
		Type:      "check_result",
		MonitorID: monitorID.String(),
		TenantID:  tenantID.String(),
		Timestamp: time.Now().UTC(),
	}
	if err := s.publisher.Publish(event); err != nil {
		// Best-effort; ignore publish errors.
	}
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
