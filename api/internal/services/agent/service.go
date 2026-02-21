package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/statusupdates"
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
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name FROM monitors 
		 WHERE agent_id = $1 AND tenant_id = $2 AND type = 'agent' AND enabled = true`,
		payload.AgentID, tenantID,
	).Scan(&monitorID, &monitorName)
	if err == sql.ErrNoRows {
		return fmt.Errorf("agent not found or disabled: %s", payload.AgentID)
	}
	if err != nil {
		return fmt.Errorf("failed to lookup agent: %w", err)
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

	// Insert check result
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO check_results 
		 (id, monitor_id, tenant_id, job_id, status, result_source, latency_ms, metrics_data, created_at, started_at, completed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		uuid.New(),
		monitorID,
		tenantID,
		jobID,
		status,
		string(models.ResultSourceMonitor),
		latencyMs,
		metricsJSON,
		payload.Metrics.Timestamp,
		payload.Metrics.Timestamp,
		time.Now(),
	)
	if err != nil {
		return fmt.Errorf("failed to insert check result: %w", err)
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
		 WHERE agent_id = $1 AND tenant_id = $2 AND type = 'agent'`,
		agentID, tenantID,
	).Scan(&monitorID)
	if err != nil {
		return uuid.Nil, err
	}
	return monitorID, nil
}

// GenerateInstallCommand generates installation instructions for an agent
func (s *Service) GenerateInstallCommand(ctx context.Context, monitorID, tenantID uuid.UUID, backendURL, apiKey string) (*models.AgentInstallCommand, error) {
	// Get monitor details
	var agentID sql.NullString
	var intervalSeconds int
	err := s.db.QueryRowContext(ctx,
		`SELECT agent_id, interval_seconds FROM monitors 
		 WHERE id = $1 AND tenant_id = $2 AND type = 'agent'`,
		monitorID, tenantID,
	).Scan(&agentID, &intervalSeconds)
	if err != nil {
		return nil, fmt.Errorf("failed to get monitor: %w", err)
	}

	if !agentID.Valid {
		return nil, fmt.Errorf("monitor does not have an agent_id")
	}

	// Generate installation script
	installScript := fmt.Sprintf(`#!/bin/bash
set -e

# Probara Agent Installation Script
echo "Installing Probara Agent..."

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
    x86_64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

# Download URL
DOWNLOAD_URL="%s/static/agent/probara-agent-${OS}-${ARCH}"

# Create install directory
INSTALL_DIR="$HOME/.local/bin"
mkdir -p "$INSTALL_DIR"

# Download agent binary
echo "Downloading agent for ${OS}-${ARCH}..."
curl -sSL -o "$INSTALL_DIR/probara-agent" "${DOWNLOAD_URL}"
chmod +x "$INSTALL_DIR/probara-agent"

echo "Agent installed successfully to $INSTALL_DIR/probara-agent"
echo ""
echo "Starting agent..."
"$INSTALL_DIR/probara-agent" \
  -backend-url "%s" \
  -agent-id "%s" \
  -api-key "%s" \
  -interval %d &

echo ""
echo "Agent is running in the background!"
echo "To run as a service, see the documentation."
`, backendURL, backendURL, agentID.String, apiKey, intervalSeconds)

	// Generate config template
	configTemplate := fmt.Sprintf(`# Probara Agent Configuration
BACKEND_URL=%s
AGENT_ID=%s
API_KEY=%s
INTERVAL=%d
DISK_PATH=/
`, backendURL, agentID.String, apiKey, intervalSeconds)

	return &models.AgentInstallCommand{
		AgentID:         agentID.String,
		BackendURL:      backendURL,
		InstallScript:   installScript,
		ConfigTemplate:  configTemplate,
		DownloadURL:     fmt.Sprintf("%s/static/agent/", backendURL),
		IntervalSeconds: intervalSeconds,
	}, nil
}
