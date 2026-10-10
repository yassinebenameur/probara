package agent

// Server-generated OpenTelemetry Collector config and installers for agent
// monitors. The distributed binary is probara-collector (OCB manifest in
// collector/manifest.yaml, served from /static/collector/); the config below
// is standard OTLP, so stock otelcol-contrib from a customer's own mirror
// runs it unchanged.
//
// Secret posture matches the old agent installers: the YAML holds only
// ${env:PROBARA_API_KEY}/${env:PROBARA_AGENT_ID} references (0644), the env
// file carrying the values is 0600 (Unix) / the service registry Environment
// value (Windows). Install scripts remove any legacy probara-agent install
// first — that is the guaranteed decommission path for the old fleet — and
// uninstall scripts clean both generations.

import (
	"fmt"
	"strings"
)

// CollectorVersion is the probara-collector build served from
// /static/collector/. Bump together with collector/manifest.yaml and
// scripts/build-collector.sh (pinned OCB version).
const CollectorVersion = "0.159.0"

// buildFilesystemScraper renders the filesystem scraper block. Pseudo/virtual
// filesystems are excluded on every platform: devfs and friends always read
// 100% (or carry no meaningful capacity), which would dominate "fullest
// mount" displays and false-trigger filesystem alert rules. On macOS the
// APFS boot/firmware system volumes are excluded too — all volumes in an
// APFS container share its free space, so /System/Volumes/Data alone already
// represents the whole disk; the rest (Preboot, Hardware, Update, VM, …) is
// plumbing noise. RE2 has no negative lookahead, so the exclusion enumerates
// the known system volumes instead of excluding /System/Volumes/ minus Data.
func buildFilesystemScraper(platform string) string {
	block := `      filesystem:
        metrics:
          system.filesystem.utilization:
            enabled: true
        exclude_fs_types:
          match_type: strict
          fs_types: [devfs, devtmpfs, tmpfs, squashfs, overlay, autofs, procfs, sysfs, ramfs, iso9660, nullfs]`
	if platform == "darwin" {
		block += `
        exclude_mount_points:
          match_type: regexp
          mount_points:
            - ^/System/Volumes/(Preboot|Hardware|iSCPreboot|Update|VM|xarts|Recovery)($|/)
            - ^/private/var/vm($|/)`
	}
	return block
}

// buildCollectorConfig renders the collector YAML for one platform
// (linux|darwin|windows). Scraper sets differ per OS: `processes` is
// linux-only, `load` is unsupported on Windows. The metricstransform block
// produces the curated `system.cpu.utilization{state=used}` series (mean
// across CPUs, non-idle states summed) that the default dashboards and rule
// presets target.
func buildCollectorConfig(backendURL string, intervalSeconds int, platform string) string {
	scrapers := []string{
		`      cpu:
        metrics:
          system.cpu.utilization:
            enabled: true
          system.cpu.logical.count:
            enabled: true`,
		`      memory:
        metrics:
          system.memory.utilization:
            enabled: true`,
		`      disk:`,
		buildFilesystemScraper(platform),
		// Per-interface packet/error/drop counters are disabled by default:
		// they are the single biggest series-cardinality driver (4 metrics ×
		// interfaces × directions) and are rarely charted or alerted on.
		// Re-enable them in the config if you need them — the store accepts
		// any metric the collector sends.
		`      network:
        metrics:
          system.network.packets:
            enabled: false
          system.network.errors:
            enabled: false
          system.network.dropped:
            enabled: false`,
		`      paging:
        metrics:
          system.paging.utilization:
            enabled: true`,
	}
	if platform != "windows" {
		scrapers = append(scrapers, `      load:`)
	}
	if platform == "linux" {
		scrapers = append(scrapers, `      processes:`)
	}
	scrapers = append(scrapers, `      system:`)

	return fmt.Sprintf(`# Probara host metrics collector configuration (generated).
# Values in ${env:...} come from the collector.env file (Unix) or the
# service's registry Environment (Windows); the file itself carries no
# secrets and stock otelcol-contrib runs it unchanged.
receivers:
  hostmetrics:
    collection_interval: %ds
    scrapers:
%s

processors:
  memory_limiter:
    check_interval: 5s
    limit_mib: 256
  resourcedetection:
    detectors: [system]
    system:
      hostname_sources: [os]
  resource:
    attributes:
      - key: probara.agent.id
        value: ${env:PROBARA_AGENT_ID}
        action: upsert
  # Collapse per-CPU, per-state utilization into one {state=used} series:
  # mean across CPUs, then non-idle states summed. Rule presets and the host
  # overview target system.cpu.utilization{state=used}.
  metricstransform:
    transforms:
      - include: system.cpu.utilization
        match_type: strict
        action: update
        operations:
          - action: aggregate_labels
            label_set: [state]
            aggregation_type: mean
          - action: aggregate_label_values
            label: state
            aggregated_values: [user, system, wait, interrupt, nice, softirq, steal]
            new_value: used
            aggregation_type: sum
  batch:

exporters:
  otlphttp:
    # The exporter appends /v1/metrics — this lands on POST /api/v1/otlp/v1/metrics.
    endpoint: %s/api/v1/otlp
    compression: gzip
    headers:
      Authorization: "Bearer ${env:PROBARA_API_KEY}"
      X-Probara-Agent-Id: "${env:PROBARA_AGENT_ID}"

service:
  telemetry:
    metrics:
      level: none
  pipelines:
    metrics:
      receivers: [hostmetrics]
      processors: [memory_limiter, resourcedetection, resource, metricstransform, batch]
      exporters: [otlphttp]
`, intervalSeconds, strings.Join(scrapers, "\n"), backendURL)
}

// legacyAgentCleanupUnix removes a previous-generation probara-agent install
// (service, binary, config). Embedded at the top of the collector install
// script: reinstalling a host is the guaranteed decommission path for the
// old fleet.
const legacyAgentCleanupUnix = `# Remove any legacy probara-agent install (previous generation).
if command -v systemctl >/dev/null 2>&1 && [ -f "/etc/systemd/system/probara-agent.service" ]; then
  systemctl stop probara-agent.service >/dev/null 2>&1 || true
  systemctl disable probara-agent.service >/dev/null 2>&1 || true
  rm -f /etc/systemd/system/probara-agent.service
  systemctl daemon-reload >/dev/null 2>&1 || true
fi
LEGACY_PLIST="$HOME/Library/LaunchAgents/com.probara.agent.plist"
if command -v launchctl >/dev/null 2>&1 && [ -f "$LEGACY_PLIST" ]; then
  launchctl bootout "gui/$(id -u)/com.probara.agent" >/dev/null 2>&1 || launchctl remove "com.probara.agent" >/dev/null 2>&1 || true
  rm -f "$LEGACY_PLIST"
fi
rm -f /usr/local/bin/probara-agent "$HOME/.local/bin/probara-agent"
rm -rf /usr/local/lib/probara-agent /etc/probara-agent /var/lib/probara-agent \
       "$HOME/.local/lib/probara-agent" "$HOME/.config/probara-agent" \
       "$HOME/.local/state/probara-agent" "$HOME/Library/Logs/ProbaraAgent"`

func buildCollectorUnixInstallScript(backendURL, agentID, apiKey string, intervalSeconds int) string {
	template := `#!/bin/bash
set -euo pipefail

SERVICE_NAME="probara-collector"
LABEL="com.probara.collector"
BACKEND_URL="__BACKEND_URL__"
AGENT_ID="__AGENT_ID__"
API_KEY="__API_KEY__"

echo "Installing Probara Collector..."

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

# The collector installs as a system service on Linux and must run as root
# (same posture as the previous agent installer).
if [ "$OS" = "linux" ]; then
  if [ "$(id -u)" != "0" ]; then
    echo "Probara Collector must be installed as root."
    echo "Re-run with sudo:        curl ... | sudo bash"
    echo "or switch to root first: su -"
    exit 1
  fi
  INSTALL_DIR="/usr/local/bin"
  CONFIG_DIR="/etc/probara-collector"
  RUNNER_DIR="/usr/local/lib/probara-collector"
else
  INSTALL_DIR="$HOME/.local/bin"
  CONFIG_DIR="$HOME/.config/probara-collector"
  RUNNER_DIR="$HOME/.local/lib/probara-collector"
fi

__LEGACY_AGENT_CLEANUP__

COLLECTOR_BIN="$INSTALL_DIR/probara-collector"
CONFIG_FILE="$CONFIG_DIR/config.yaml"
ENV_FILE="$CONFIG_DIR/collector.env"
RUNNER="$RUNNER_DIR/run-collector.sh"
UNINSTALL_SCRIPT="$RUNNER_DIR/uninstall-collector.sh"
DOWNLOAD_URL="${BACKEND_URL}/static/collector/probara-collector-${OS}-${ARCH}"
CHECKSUMS_URL="${BACKEND_URL}/static/collector/checksums.txt"

mkdir -p "$INSTALL_DIR" "$CONFIG_DIR" "$RUNNER_DIR"

echo "Downloading collector for ${OS}-${ARCH}..."
curl -fsSL -o "$COLLECTOR_BIN.tmp" "$DOWNLOAD_URL"

echo "Verifying checksum..."
EXPECTED=$(curl -fsSL "$CHECKSUMS_URL" | grep "probara-collector-${OS}-${ARCH}$" | awk '{print $1}')
if [ -n "$EXPECTED" ]; then
  if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL=$(sha256sum "$COLLECTOR_BIN.tmp" | awk '{print $1}')
  else
    ACTUAL=$(shasum -a 256 "$COLLECTOR_BIN.tmp" | awk '{print $1}')
  fi
  if [ "$EXPECTED" != "$ACTUAL" ]; then
    echo "Checksum mismatch for downloaded collector binary."; rm -f "$COLLECTOR_BIN.tmp"; exit 1
  fi
else
  echo "Warning: no checksum published for probara-collector-${OS}-${ARCH}; skipping verification."
fi
mv "$COLLECTOR_BIN.tmp" "$COLLECTOR_BIN"
chmod +x "$COLLECTOR_BIN"

cat > "$ENV_FILE" <<PROBARA_ENV
PROBARA_API_KEY=$API_KEY
PROBARA_AGENT_ID=$AGENT_ID
PROBARA_ENV
chmod 600 "$ENV_FILE"

if [ "$OS" = "linux" ]; then
cat > "$CONFIG_FILE" <<'PROBARA_CONFIG'
__COLLECTOR_CONFIG_LINUX__
PROBARA_CONFIG
else
cat > "$CONFIG_FILE" <<'PROBARA_CONFIG'
__COLLECTOR_CONFIG_DARWIN__
PROBARA_CONFIG
fi
chmod 644 "$CONFIG_FILE"

cat > "$UNINSTALL_SCRIPT" <<'PROBARA_UNINSTALL'
__UNIX_UNINSTALL_SCRIPT__
PROBARA_UNINSTALL
chmod +x "$UNINSTALL_SCRIPT"

install_systemd_system() {
  if ! command -v systemctl >/dev/null 2>&1; then
    echo "systemctl is required to install Probara Collector as a Linux system service."
    exit 1
  fi

  UNIT_FILE="/etc/systemd/system/probara-collector.service"

  cat > "$UNIT_FILE" <<PROBARA_SYSTEMD
[Unit]
Description=Probara Collector
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=$ENV_FILE
ExecStart=$COLLECTOR_BIN --config $CONFIG_FILE
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
PROBARA_SYSTEMD

  systemctl daemon-reload
  systemctl enable --now probara-collector.service
  echo "Probara Collector installed as a system systemd service."
  echo "Status: systemctl status probara-collector.service"
  echo "Logs: journalctl -u probara-collector.service -f"
}

install_launchd() {
  PLIST_DIR="$HOME/Library/LaunchAgents"
  LOG_DIR="$HOME/Library/Logs/ProbaraCollector"
  PLIST_FILE="$PLIST_DIR/${LABEL}.plist"
  mkdir -p "$PLIST_DIR" "$LOG_DIR"

  # launchd has no EnvironmentFile: the runner sources the env file, then
  # execs the collector.
  {
    printf '#!/bin/sh\nset -eu\nset -a\n. "%s"\nset +a\nexec "%s" --config "%s"\n' "$ENV_FILE" "$COLLECTOR_BIN" "$CONFIG_FILE"
  } > "$RUNNER"
  chmod +x "$RUNNER"

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
  <string>${LOG_DIR}/collector.log</string>
  <key>StandardErrorPath</key>
  <string>${LOG_DIR}/collector.err.log</string>
  <key>WorkingDirectory</key>
  <string>${HOME}</string>
</dict>
</plist>
PROBARA_PLIST

  launchctl bootstrap "gui/$(id -u)" "$PLIST_FILE" || launchctl load "$PLIST_FILE"
  launchctl enable "gui/$(id -u)/${LABEL}" >/dev/null 2>&1 || true
  launchctl kickstart -k "gui/$(id -u)/${LABEL}" || launchctl start "$LABEL"
  echo "Probara Collector installed as a launchd service."
  echo "Status: launchctl print gui/$(id -u)/${LABEL}"
  echo "Logs: tail -f ${LOG_DIR}/collector.log"
}

case "$OS" in
  linux) install_systemd_system ;;
  darwin) install_launchd ;;
esac

echo "Installation complete. Probara Collector will restart automatically if it exits."
`

	return strings.NewReplacer(
		"__BACKEND_URL__", backendURL,
		"__AGENT_ID__", agentID,
		"__API_KEY__", apiKey,
		"__LEGACY_AGENT_CLEANUP__", legacyAgentCleanupUnix,
		"__COLLECTOR_CONFIG_LINUX__", buildCollectorConfig(backendURL, intervalSeconds, "linux"),
		"__COLLECTOR_CONFIG_DARWIN__", buildCollectorConfig(backendURL, intervalSeconds, "darwin"),
		"__UNIX_UNINSTALL_SCRIPT__", buildCollectorUnixUninstallScript(),
	).Replace(template)
}

func buildCollectorWindowsInstallScript(backendURL, agentID, apiKey string, intervalSeconds int) string {
	template := `#Requires -RunAsAdministrator
$ErrorActionPreference = "Stop"

$CurrentIdentity = [Security.Principal.WindowsIdentity]::GetCurrent()
$CurrentPrincipal = New-Object Security.Principal.WindowsPrincipal($CurrentIdentity)
if (-not $CurrentPrincipal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
  throw "Run this installer from an elevated PowerShell window."
}

$ServiceName = "ProbaraCollector"
$BackendURL = "__BACKEND_URL__"
$AgentID = "__AGENT_ID__"
$ApiKey = "__API_KEY__"
$InstallDir = Join-Path $env:ProgramFiles "ProbaraCollector"
$CollectorPath = Join-Path $InstallDir "probara-collector.exe"
$ConfigPath = Join-Path $InstallDir "config.yaml"
$UninstallScript = Join-Path $InstallDir "uninstall-probara-collector.ps1"
$DownloadURL = "$BackendURL/static/collector/probara-collector-windows-amd64.exe"
$ChecksumsURL = "$BackendURL/static/collector/checksums.txt"

Write-Host "Installing Probara Collector..."

# Remove any legacy probara-agent install (previous generation, NSSM-based).
$LegacyDir = Join-Path $env:ProgramFiles "ProbaraAgent"
$LegacyNssm = Join-Path $LegacyDir "nssm.exe"
if (Get-Service -Name "ProbaraAgent" -ErrorAction SilentlyContinue) {
  if (Test-Path $LegacyNssm) {
    & $LegacyNssm stop ProbaraAgent 2>$null | Out-Null
    & $LegacyNssm remove ProbaraAgent confirm 2>$null | Out-Null
  } else {
    Stop-Service -Name "ProbaraAgent" -Force -ErrorAction SilentlyContinue
    sc.exe delete ProbaraAgent | Out-Null
  }
}
if (Test-Path $LegacyDir) { Remove-Item -Recurse -Force $LegacyDir -ErrorAction SilentlyContinue }
$LegacyLogs = Join-Path $env:ProgramData "ProbaraAgent"
if (Test-Path $LegacyLogs) { Remove-Item -Recurse -Force $LegacyLogs -ErrorAction SilentlyContinue }

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null

Write-Host "Downloading collector..."
Invoke-WebRequest -UseBasicParsing -Uri $DownloadURL -OutFile "$CollectorPath.tmp"

Write-Host "Verifying checksum..."
$ChecksumLine = (Invoke-WebRequest -UseBasicParsing -Uri $ChecksumsURL).Content -split "` + "`" + `n" | Where-Object { $_ -match "probara-collector-windows-amd64.exe" }
if ($ChecksumLine) {
  $Expected = ($ChecksumLine -split "\s+")[0].ToLower()
  $Actual = (Get-FileHash -Algorithm SHA256 "$CollectorPath.tmp").Hash.ToLower()
  if ($Expected -ne $Actual) {
    Remove-Item "$CollectorPath.tmp" -Force
    throw "Checksum mismatch for downloaded collector binary."
  }
} else {
  Write-Warning "No checksum published for the Windows binary; skipping verification."
}
Move-Item -Force "$CollectorPath.tmp" $CollectorPath

@'
__COLLECTOR_CONFIG__
'@ | Set-Content -Path $ConfigPath -Encoding UTF8

@'
__WINDOWS_UNINSTALL_SCRIPT__
'@ | Set-Content -Path $UninstallScript -Encoding UTF8

if (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) {
  Stop-Service -Name $ServiceName -Force -ErrorAction SilentlyContinue
  sc.exe delete $ServiceName | Out-Null
  Start-Sleep -Seconds 1
}

# The collector has native Windows service support (no NSSM). Credentials are
# delivered through the service's registry Environment value, never the
# world-readable config file.
$BinPath = '"' + $CollectorPath + '" --config "' + $ConfigPath + '"'
sc.exe create $ServiceName binPath= $BinPath start= auto DisplayName= "Probara Collector" | Out-Null
$RegPath = "HKLM:\SYSTEM\CurrentControlSet\Services\$ServiceName"
Set-ItemProperty -Path $RegPath -Name Environment -Type MultiString -Value @(
  "PROBARA_API_KEY=$ApiKey",
  "PROBARA_AGENT_ID=$AgentID"
)
sc.exe failure $ServiceName reset= 86400 actions= restart/10000/restart/10000/restart/10000 | Out-Null

Start-Service -Name $ServiceName
Write-Host "Installation complete. Probara Collector is running as Windows service '$ServiceName'."
`

	return strings.NewReplacer(
		"__BACKEND_URL__", backendURL,
		"__AGENT_ID__", agentID,
		"__API_KEY__", apiKey,
		"__COLLECTOR_CONFIG__", buildCollectorConfig(backendURL, intervalSeconds, "windows"),
		"__WINDOWS_UNINSTALL_SCRIPT__", buildCollectorWindowsUninstallScript(),
	).Replace(template)
}

// buildCollectorUnixUninstallScript removes both the collector install and
// any legacy probara-agent leftovers.
func buildCollectorUnixUninstallScript() string {
	return `#!/bin/sh
set -eu

SERVICE_NAME="probara-collector"
LABEL="com.probara.collector"
BOOTOUT_TARGET=""
SYSTEMD_STOP=0

# Linux: system service installed as root.
if command -v systemctl >/dev/null 2>&1 && [ -f "/etc/systemd/system/${SERVICE_NAME}.service" ]; then
  systemctl disable "${SERVICE_NAME}.service" >/dev/null 2>&1 || true
  rm -f "/etc/systemd/system/${SERVICE_NAME}.service"
  systemctl daemon-reload >/dev/null 2>&1 || true
  SYSTEMD_STOP=1
fi

# macOS: per-user launchd agent.
PLIST_FILE="$HOME/Library/LaunchAgents/${LABEL}.plist"
if command -v launchctl >/dev/null 2>&1 && [ -f "$PLIST_FILE" ]; then
  rm -f "$PLIST_FILE"
  BOOTOUT_TARGET="gui/$(id -u)/${LABEL}"
fi

rm -f "/usr/local/bin/probara-collector" "$HOME/.local/bin/probara-collector"
rm -rf "/usr/local/lib/probara-collector" \
       "/etc/probara-collector" \
       "$HOME/.local/lib/probara-collector" \
       "$HOME/.config/probara-collector" \
       "$HOME/Library/Logs/ProbaraCollector"

# Legacy probara-agent leftovers (previous generation).
if command -v systemctl >/dev/null 2>&1 && [ -f "/etc/systemd/system/probara-agent.service" ]; then
  systemctl disable probara-agent.service >/dev/null 2>&1 || true
  rm -f /etc/systemd/system/probara-agent.service
  systemctl daemon-reload >/dev/null 2>&1 || true
  systemctl stop probara-agent.service >/dev/null 2>&1 || true
fi
LEGACY_PLIST="$HOME/Library/LaunchAgents/com.probara.agent.plist"
if command -v launchctl >/dev/null 2>&1 && [ -f "$LEGACY_PLIST" ]; then
  rm -f "$LEGACY_PLIST"
  launchctl bootout "gui/$(id -u)/com.probara.agent" >/dev/null 2>&1 || launchctl remove "com.probara.agent" >/dev/null 2>&1 || true
fi
rm -f /usr/local/bin/probara-agent "$HOME/.local/bin/probara-agent"
rm -rf /usr/local/lib/probara-agent /etc/probara-agent /var/lib/probara-agent \
       "$HOME/.local/lib/probara-agent" "$HOME/.config/probara-agent" \
       "$HOME/.local/state/probara-agent" "$HOME/Library/Logs/ProbaraAgent"

# Stop last so a service invoking this script can finish.
if [ "$SYSTEMD_STOP" -eq 1 ]; then
  systemctl stop "${SERVICE_NAME}.service" >/dev/null 2>&1 || true
fi

if [ -n "$BOOTOUT_TARGET" ]; then
  launchctl bootout "$BOOTOUT_TARGET" >/dev/null 2>&1 || launchctl remove "$LABEL" >/dev/null 2>&1 || true
fi

echo "Probara Collector uninstalled."
`
}

func buildCollectorWindowsUninstallScript() string {
	return `$ErrorActionPreference = "SilentlyContinue"

$ServiceName = "ProbaraCollector"
$InstallDir = Join-Path $env:ProgramFiles "ProbaraCollector"

if (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) {
  Stop-Service -Name $ServiceName -Force
  sc.exe delete $ServiceName | Out-Null
}
Remove-Item -Recurse -Force $InstallDir

# Legacy probara-agent leftovers (previous generation, NSSM-based).
$LegacyDir = Join-Path $env:ProgramFiles "ProbaraAgent"
$LegacyNssm = Join-Path $LegacyDir "nssm.exe"
if (Get-Service -Name "ProbaraAgent" -ErrorAction SilentlyContinue) {
  if (Test-Path $LegacyNssm) {
    & $LegacyNssm stop ProbaraAgent | Out-Null
    & $LegacyNssm remove ProbaraAgent confirm | Out-Null
  } else {
    Stop-Service -Name "ProbaraAgent" -Force
    sc.exe delete ProbaraAgent | Out-Null
  }
}
Remove-Item -Recurse -Force $LegacyDir
Remove-Item -Recurse -Force (Join-Path $env:ProgramData "ProbaraAgent")

Write-Host "Probara Collector uninstalled."
`
}
