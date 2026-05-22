package models

import "time"

// AgentMetrics represents system metrics collected by an agent
type AgentMetrics struct {
	// CPU usage percentage (0-100). -1 means unavailable on this platform.
	CPUPercent float64 `json:"cpu_percent"`

	// Memory metrics in bytes
	MemoryUsed  uint64 `json:"memory_used"`
	MemoryTotal uint64 `json:"memory_total"`

	// Disk metrics in bytes
	DiskUsed  uint64 `json:"disk_used"`
	DiskTotal uint64 `json:"disk_total"`

	// Network I/O counters in bytes
	NetworkBytesIn  uint64 `json:"network_bytes_in"`
	NetworkBytesOut uint64 `json:"network_bytes_out"`

	// System load averages
	LoadAvg1  float64 `json:"load_avg_1"`
	LoadAvg5  float64 `json:"load_avg_5"`
	LoadAvg15 float64 `json:"load_avg_15"`

	// Process count
	ProcessCount int `json:"process_count"`

	// Timestamp when metrics were collected
	Timestamp time.Time `json:"timestamp"`
}

// AgentMetricsPayload represents the payload sent by an agent to the backend
type AgentMetricsPayload struct {
	AgentID  string       `json:"agent_id"`
	TenantID string       `json:"tenant_id,omitempty"` // Optional, can be derived from API key
	Metrics  AgentMetrics `json:"metrics"`
}

// AgentInstallCommand represents the installation instructions for an agent
type AgentInstallCommand struct {
	AgentID              string `json:"agent_id"`
	BackendURL           string `json:"backend_url"`
	InstallScript        string `json:"install_script"`
	WindowsInstallScript string `json:"windows_install_script"`
	ConfigTemplate       string `json:"config_template"`
	DownloadURL          string `json:"download_url"`
	IntervalSeconds      int    `json:"interval_seconds"`
}
