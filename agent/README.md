# Probara Agent

A lightweight system metrics collector that reports CPU, memory, disk, network, and process metrics to the Probara backend.

## Features

- **Cross-platform**: Supports Linux, macOS, and Windows
- **Lightweight**: < 20MB binary, minimal resource usage
- **Reliable**: Automatic retry with exponential backoff
- **Configurable**: Customizable reporting interval and monitored disk path

## Installation

### Quick Install (Linux/macOS)

```bash
# Download and run the service installer from your Probara dashboard.
# On Linux the agent installs as a system service, so run it as root.
curl -fsSL -H "Authorization: Bearer YOUR_API_KEY" \
  "https://your-backend-url/api/v1/monitors/{id}/agent/install/script.sh" | sudo bash
```

The installer configures the agent as a supervised service: a system `systemd`
unit on Linux (installed under `/etc/systemd/system`, which requires root) and
`launchd` on macOS. The service restarts automatically if the agent exits and
reconnects when the backend is available again.

Enable "Allow remote disable" in the install UI only when this machine should
remove the local service automatically after the monitor is deleted in Probara.

### Uninstall

The dashboard provides a matching uninstall command for Linux, macOS, and
Windows. On Linux/macOS it stops the local service and removes the installed
binary, config, runner, state, and logs:

```bash
curl -fsSL -H "Authorization: Bearer YOUR_API_KEY" \
  "https://your-backend-url/api/v1/monitors/{id}/agent/uninstall/script.sh" | sudo bash
```

### Manual Installation

1. Download the agent binary for your platform:
   - Linux: `probara-agent-linux-amd64`
   - macOS: `probara-agent-darwin-amd64`
   - Windows: `probara-agent-windows-amd64.exe`

2. Make it executable (Linux/macOS):
   ```bash
   chmod +x probara-agent-linux-amd64
   ```

3. Run the agent:
   ```bash
   ./probara-agent-linux-amd64 \
     -backend-url https://your-backend-url \
     -agent-id YOUR_AGENT_ID \
     -api-key YOUR_API_KEY \
     -interval 60
   ```

## Configuration

### Command-line Flags

- `-backend-url` (required): Backend API URL
- `-agent-id` (required): Unique agent identifier
- `-api-key` (required): API key for authentication
- `-interval` (optional): Reporting interval in seconds (default: 60)
- `-disk-path` (optional): Disk path to monitor (default: `/`)
- `-allow-remote-disable` (optional): Allow the agent to run its configured
  disable command when the backend returns `410 Gone`
- `-remote-disable-command` (optional): Local uninstall script path to run when
  remote disable is allowed

### Environment Variables

You can also configure the agent using environment variables:

```bash
export BACKEND_URL="https://your-backend-url"
export AGENT_ID="YOUR_AGENT_ID"
export API_KEY="YOUR_API_KEY"
export INTERVAL=60
export DISK_PATH="/"
```

## Running as a Service

### Linux (systemd)

Create a systemd service file at `/etc/systemd/system/probara-agent.service`:

```ini
[Unit]
Description=Probara Agent
After=network.target

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/probara-agent \
  -backend-url https://your-backend-url \
  -agent-id YOUR_AGENT_ID \
  -api-key YOUR_API_KEY \
  -interval 60
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

Enable and start the service:

```bash
sudo systemctl daemon-reload
sudo systemctl enable probara-agent
sudo systemctl start probara-agent
sudo systemctl status probara-agent
```

### macOS (launchd)

Create a plist file at `~/Library/LaunchAgents/com.probara.agent.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.probara.agent</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/probara-agent</string>
        <string>-backend-url</string>
        <string>https://your-backend-url</string>
        <string>-agent-id</string>
        <string>YOUR_AGENT_ID</string>
        <string>-api-key</string>
        <string>YOUR_API_KEY</string>
        <string>-interval</string>
        <string>60</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
</dict>
</plist>
```

Load and start the agent:

```bash
launchctl load ~/Library/LaunchAgents/com.probara.agent.plist
launchctl start com.probara.agent
```

### Windows (NSSM)

Use [NSSM](https://nssm.cc/) to install the agent as a Windows service:

```cmd
nssm install ProbaraAgent "C:\Program Files\ProbaraAgent\probara-agent.exe"
nssm set ProbaraAgent AppParameters "-backend-url https://your-backend-url -agent-id YOUR_AGENT_ID -api-key YOUR_API_KEY -interval 60"
nssm start ProbaraAgent
```

## Metrics Collected

- **CPU Usage**: Percentage of CPU utilization
- **Memory**: Used and total memory in bytes
- **Disk**: Used and total disk space in bytes
- **Network I/O**: Bytes received and sent
- **Load Average**: 1, 5, and 15-minute load averages
- **Process Count**: Number of running processes

## Troubleshooting

### Agent can't connect to backend

- Verify the backend URL is correct and accessible
- Check firewall rules allow outbound HTTPS connections
- Ensure the API key is valid

### High CPU usage

- Increase the reporting interval (e.g., `-interval 120` for 2 minutes)
- Check for other resource-intensive processes

### Permission errors

- Run the agent with appropriate permissions (may need root/admin for some metrics)
- On Linux, some metrics require elevated privileges

### Logs

The agent logs to stdout. To save logs:

```bash
./probara-agent-linux-amd64 ... > /var/log/probara-agent.log 2>&1
```

## Building from Source

```bash
cd agent
go build -o probara-agent ./cmd/agent
```

Cross-compile for different platforms:

```bash
# Linux
GOOS=linux GOARCH=amd64 go build -o probara-agent-linux-amd64 ./cmd/agent

# macOS
GOOS=darwin GOARCH=amd64 go build -o probara-agent-darwin-amd64 ./cmd/agent

# Windows
GOOS=windows GOARCH=amd64 go build -o probara-agent-windows-amd64.exe ./cmd/agent
```

## License

See main project LICENSE file.
