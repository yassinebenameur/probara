package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
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

	installScript := buildUnixInstallScript(backendURL, agentID.String, apiKey, intervalSeconds)
	windowsInstallScript := buildWindowsInstallScript(backendURL, agentID.String, apiKey, intervalSeconds)

	// Generate config template
	configTemplate := fmt.Sprintf(`# Probara Agent Configuration
BACKEND_URL=%s
AGENT_ID=%s
API_KEY=%s
INTERVAL=%d
DISK_PATH=/
`, backendURL, agentID.String, apiKey, intervalSeconds)

	return &models.AgentInstallCommand{
		AgentID:              agentID.String,
		BackendURL:           backendURL,
		InstallScript:        installScript,
		WindowsInstallScript: windowsInstallScript,
		ConfigTemplate:       configTemplate,
		DownloadURL:          fmt.Sprintf("%s/static/agent/", backendURL),
		IntervalSeconds:      intervalSeconds,
	}, nil
}

func buildUnixInstallScript(backendURL, agentID, apiKey string, intervalSeconds int) string {
	template := `#!/bin/bash
set -euo pipefail

SERVICE_NAME="probara-agent"
LABEL="com.probara.agent"
BACKEND_URL="__BACKEND_URL__"
AGENT_ID="__AGENT_ID__"
API_KEY="__API_KEY__"
INTERVAL="__INTERVAL__"
DISK_PATH="/"

echo "Installing Probara Agent..."

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

case "$OS" in
  linux|darwin) ;;
  *) echo "Unsupported operating system: $OS"; exit 1 ;;
esac

DOWNLOAD_URL="${BACKEND_URL}/static/agent/probara-agent-${OS}-${ARCH}"
INSTALL_DIR="$HOME/.local/bin"
CONFIG_DIR="$HOME/.config/probara-agent"
STATE_DIR="$HOME/.local/state/probara-agent"
RUNNER_DIR="$HOME/.local/lib/probara-agent"
RUNNER="$RUNNER_DIR/run-agent.sh"
CONFIG_FILE="$CONFIG_DIR/agent.env"

mkdir -p "$INSTALL_DIR" "$CONFIG_DIR" "$STATE_DIR" "$RUNNER_DIR"

echo "Downloading agent for ${OS}-${ARCH}..."
curl -fsSL -o "$INSTALL_DIR/probara-agent" "$DOWNLOAD_URL"
chmod +x "$INSTALL_DIR/probara-agent"

cat > "$CONFIG_FILE" <<PROBARA_ENV
BACKEND_URL=$BACKEND_URL
AGENT_ID=$AGENT_ID
API_KEY=$API_KEY
INTERVAL=$INTERVAL
DISK_PATH=$DISK_PATH
PROBARA_ENV
chmod 600 "$CONFIG_FILE"

cat > "$RUNNER" <<'PROBARA_RUNNER'
#!/bin/sh
set -eu
. "$HOME/.config/probara-agent/agent.env"
exec "$HOME/.local/bin/probara-agent" \
  -backend-url "$BACKEND_URL" \
  -agent-id "$AGENT_ID" \
  -api-key "$API_KEY" \
  -interval "$INTERVAL" \
  -disk-path "$DISK_PATH"
PROBARA_RUNNER
chmod +x "$RUNNER"

install_systemd_user() {
  if ! command -v systemctl >/dev/null 2>&1; then
    echo "systemctl is required to install Probara Agent as a Linux user service."
    exit 1
  fi

  SYSTEMD_DIR="$HOME/.config/systemd/user"
  UNIT_FILE="$SYSTEMD_DIR/probara-agent.service"
  mkdir -p "$SYSTEMD_DIR"

  cat > "$UNIT_FILE" <<'PROBARA_SYSTEMD'
[Unit]
Description=Probara Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%h/.local/lib/probara-agent/run-agent.sh
Restart=always
RestartSec=10

[Install]
WantedBy=default.target
PROBARA_SYSTEMD

  systemctl --user daemon-reload
  systemctl --user enable --now probara-agent.service
  echo "Probara Agent installed as a user systemd service."
  echo "Status: systemctl --user status probara-agent.service"
  echo "Logs: journalctl --user -u probara-agent.service -f"
}

install_launchd() {
  PLIST_DIR="$HOME/Library/LaunchAgents"
  LOG_DIR="$HOME/Library/Logs/ProbaraAgent"
  PLIST_FILE="$PLIST_DIR/${LABEL}.plist"
  mkdir -p "$PLIST_DIR" "$LOG_DIR"

  launchctl bootout "gui/$(id -u)" "$PLIST_FILE" >/dev/null 2>&1 || launchctl unload "$PLIST_FILE" >/dev/null 2>&1 || true

  cat > "$PLIST_FILE" <<PROBARA_PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>${LABEL}</string>
  <key>ProgramArguments</key>
  <array>
    <string>${RUNNER}</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>${LOG_DIR}/agent.log</string>
  <key>StandardErrorPath</key>
  <string>${LOG_DIR}/agent.err.log</string>
  <key>WorkingDirectory</key>
  <string>${HOME}</string>
</dict>
</plist>
PROBARA_PLIST

  launchctl bootstrap "gui/$(id -u)" "$PLIST_FILE" || launchctl load "$PLIST_FILE"
  launchctl enable "gui/$(id -u)/${LABEL}" >/dev/null 2>&1 || true
  launchctl kickstart -k "gui/$(id -u)/${LABEL}" || launchctl start "$LABEL"
  echo "Probara Agent installed as a launchd service."
  echo "Status: launchctl print gui/$(id -u)/${LABEL}"
  echo "Logs: tail -f ${LOG_DIR}/agent.log"
}

case "$OS" in
  linux) install_systemd_user ;;
  darwin) install_launchd ;;
esac

echo "Installation complete. Probara Agent will restart automatically if it exits."
`

	return strings.NewReplacer(
		"__BACKEND_URL__", backendURL,
		"__AGENT_ID__", agentID,
		"__API_KEY__", apiKey,
		"__INTERVAL__", fmt.Sprintf("%d", intervalSeconds),
	).Replace(template)
}

func buildWindowsInstallScript(backendURL, agentID, apiKey string, intervalSeconds int) string {
	template := `#Requires -RunAsAdministrator
$ErrorActionPreference = "Stop"

$CurrentIdentity = [Security.Principal.WindowsIdentity]::GetCurrent()
$CurrentPrincipal = New-Object Security.Principal.WindowsPrincipal($CurrentIdentity)
if (-not $CurrentPrincipal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
  throw "Run this installer from an elevated PowerShell window."
}

$ServiceName = "ProbaraAgent"
$BackendURL = "__BACKEND_URL__"
$AgentID = "__AGENT_ID__"
$ApiKey = "__API_KEY__"
$Interval = "__INTERVAL__"
$InstallDir = Join-Path $env:ProgramFiles "ProbaraAgent"
$LogDir = Join-Path $env:ProgramData "ProbaraAgent\logs"
$AgentPath = Join-Path $InstallDir "probara-agent.exe"
$NssmPath = Join-Path $InstallDir "nssm.exe"
$AgentDownloadURL = "$BackendURL/static/agent/probara-agent-windows-amd64.exe"
$NssmURL = "https://nssm.cc/release/nssm-2.24.zip"

Write-Host "Installing Probara Agent..."
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
New-Item -ItemType Directory -Force -Path $LogDir | Out-Null

Write-Host "Downloading agent..."
Invoke-WebRequest -UseBasicParsing -Uri $AgentDownloadURL -OutFile $AgentPath

if (-not (Test-Path $NssmPath)) {
  $NssmZip = Join-Path $env:TEMP "nssm-2.24.zip"
  $NssmExtract = Join-Path $env:TEMP "nssm-2.24"
  Write-Host "Downloading NSSM service wrapper..."
  Invoke-WebRequest -UseBasicParsing -Uri $NssmURL -OutFile $NssmZip
  if (Test-Path $NssmExtract) {
    Remove-Item -Recurse -Force $NssmExtract
  }
  Expand-Archive -Path $NssmZip -DestinationPath $env:TEMP -Force
  Copy-Item (Join-Path $NssmExtract "win64\nssm.exe") $NssmPath -Force
}

if (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) {
  & $NssmPath stop $ServiceName 2>$null | Out-Null
  & $NssmPath remove $ServiceName confirm 2>$null | Out-Null
}

$AppParameters = '-backend-url "' + $BackendURL + '" -agent-id "' + $AgentID + '" -api-key "' + $ApiKey + '" -interval ' + $Interval

& $NssmPath install $ServiceName $AgentPath
& $NssmPath set $ServiceName AppDirectory $InstallDir
& $NssmPath set $ServiceName AppParameters $AppParameters
& $NssmPath set $ServiceName AppStdout (Join-Path $LogDir "agent.log")
& $NssmPath set $ServiceName AppStderr (Join-Path $LogDir "agent.err.log")
& $NssmPath set $ServiceName AppRotateFiles 1
& $NssmPath set $ServiceName AppRotateOnline 1
& $NssmPath set $ServiceName AppRestartDelay 10000
& $NssmPath set $ServiceName Start SERVICE_AUTO_START

Start-Service -Name $ServiceName
Write-Host "Installation complete. Probara Agent is running as Windows service '$ServiceName'."
Write-Host "Logs: $LogDir"
`

	return strings.NewReplacer(
		"__BACKEND_URL__", backendURL,
		"__AGENT_ID__", agentID,
		"__API_KEY__", apiKey,
		"__INTERVAL__", fmt.Sprintf("%d", intervalSeconds),
	).Replace(template)
}
