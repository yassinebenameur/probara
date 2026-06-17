package models

import "time"

// AgentMetrics represents system metrics collected by an agent
type AgentMetrics struct {
	// CPU usage percentage (0-100). -1 means unavailable on this platform.
	CPUPercent float64 `json:"cpu_percent"`

	// Number of logical CPU cores
	CPUCores int `json:"cpu_cores"`

	// Memory metrics in bytes
	MemoryUsed  uint64 `json:"memory_used"`
	MemoryTotal uint64 `json:"memory_total"`

	// Swap metrics in bytes
	SwapUsed  uint64 `json:"swap_used"`
	SwapTotal uint64 `json:"swap_total"`

	// Disk metrics in bytes (for the configured disk path; kept for back-compat)
	DiskUsed  uint64 `json:"disk_used"`
	DiskTotal uint64 `json:"disk_total"`

	// Per-mount disk usage across all real filesystems
	DiskMounts []DiskMount `json:"disk_mounts,omitempty"`

	// Cumulative disk I/O counters in bytes (summed across devices)
	DiskReadBytes  uint64 `json:"disk_read_bytes"`
	DiskWriteBytes uint64 `json:"disk_write_bytes"`

	// Network I/O counters in bytes
	NetworkBytesIn  uint64 `json:"network_bytes_in"`
	NetworkBytesOut uint64 `json:"network_bytes_out"`

	// System load averages
	LoadAvg1  float64 `json:"load_avg_1"`
	LoadAvg5  float64 `json:"load_avg_5"`
	LoadAvg15 float64 `json:"load_avg_15"`

	// Process count
	ProcessCount int `json:"process_count"`

	// Host uptime in seconds
	UptimeSeconds uint64 `json:"uptime_seconds"`

	// Timestamp when metrics were collected
	Timestamp time.Time `json:"timestamp"`
}

// DiskMount represents disk usage for a single mounted filesystem
type DiskMount struct {
	Path   string `json:"path"`
	Used   uint64 `json:"used"`
	Total  uint64 `json:"total"`
	Fstype string `json:"fstype"`
}

// AgentMetricsPayload represents the payload sent by an agent to the backend
type AgentMetricsPayload struct {
	AgentID  string       `json:"agent_id"`
	TenantID string       `json:"tenant_id,omitempty"` // Optional, can be derived from API key
	Metrics  AgentMetrics `json:"metrics"`
}

// AgentInstallCommand represents the installation instructions for an agent
type AgentInstallCommand struct {
	AgentID                string `json:"agent_id"`
	BackendURL             string `json:"backend_url"`
	InstallScript          string `json:"install_script"`
	WindowsInstallScript   string `json:"windows_install_script"`
	UninstallScript        string `json:"uninstall_script"`
	WindowsUninstallScript string `json:"windows_uninstall_script"`
	ConfigTemplate         string `json:"config_template"`
	DownloadURL            string `json:"download_url"`
	IntervalSeconds        int    `json:"interval_seconds"`
}
