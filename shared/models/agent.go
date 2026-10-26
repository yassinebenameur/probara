package models

// AgentInstallCommand represents the OTel Collector installation
// instructions for an agent monitor. CollectorConfig is the generated
// collector YAML (linux variant; per-platform variants via
// GET /monitors/{id}/agent/config.yaml).
type AgentInstallCommand struct {
	AgentID                string `json:"agent_id"`
	BackendURL             string `json:"backend_url"`
	InstallScript          string `json:"install_script"`
	WindowsInstallScript   string `json:"windows_install_script"`
	UninstallScript        string `json:"uninstall_script"`
	WindowsUninstallScript string `json:"windows_uninstall_script"`
	CollectorConfig        string `json:"collector_config"`
	CollectorVersion       string `json:"collector_version"`
	DownloadURL            string `json:"download_url"`
	IntervalSeconds        int    `json:"interval_seconds"`
}
