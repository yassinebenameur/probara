package models

// ImportFormat represents the detected file format
type ImportFormat string

const (
	ImportFormatJSON ImportFormat = "json"
	ImportFormatYAML ImportFormat = "yaml"
	ImportFormatCSV  ImportFormat = "csv"

	ImportSchemaPortableMonitorExport = "portable_monitor_export"
)

// ImportRow represents a single row from the imported file
type ImportRow struct {
	Index  int                    `json:"index"`
	Fields map[string]interface{} `json:"fields"`
}

// FieldMapping represents the mapping from source field to target field
type FieldMapping struct {
	Name             string `json:"name,omitempty"`
	Type             string `json:"type,omitempty"`
	Config           string `json:"config,omitempty"`
	URL              string `json:"url,omitempty"`
	Method           string `json:"method,omitempty"`
	ExpectedStatus   string `json:"expected_status,omitempty"`
	ExpectedBody     string `json:"expected_body,omitempty"`
	Host             string `json:"host,omitempty"`
	Port             string `json:"port,omitempty"`
	Service          string `json:"service,omitempty"`
	UseTLS           string `json:"use_tls,omitempty"`
	IntervalSeconds  string `json:"interval_seconds,omitempty"`
	TimeoutSeconds   string `json:"timeout_seconds,omitempty"`
	Tags             string `json:"tags,omitempty"`
	Enabled          string `json:"enabled,omitempty"`
	GroupMembers     string `json:"group_members,omitempty"` // For group type - comma-separated monitor names
	AlertPolicyNames string `json:"alert_policy_names,omitempty"`
}

// ImportPreviewResponse is returned after parsing the uploaded file
type ImportPreviewResponse struct {
	Format               ImportFormat      `json:"format"`
	Schema               string            `json:"schema,omitempty"`
	Rows                 []ImportRow       `json:"rows"`
	DetectedFields       []string          `json:"detected_fields"`
	SuggestedMapping     FieldMapping      `json:"suggested_mapping"`
	Warnings             []string          `json:"warnings"`
	TotalRows            int               `json:"total_rows"`
	DetectedTypes        []string          `json:"detected_types"`
	SuggestedTypeMapping map[string]string `json:"suggested_type_mapping"`
}

// ImportExecuteRequest is sent to execute the import with confirmed mappings
type ImportExecuteRequest struct {
	Rows        []ImportRow       `json:"rows"`
	Mapping     FieldMapping      `json:"mapping"`
	TypeMapping map[string]string `json:"type_mapping,omitempty"`
}

// ImportRowResult represents the result of importing a single row
type ImportRowResult struct {
	Index      int     `json:"index"`
	Status     string  `json:"status"` // "success", "failed", "skipped"
	MonitorID  *string `json:"monitor_id,omitempty"`
	Name       string  `json:"name"`
	Type       string  `json:"type"`
	Error      string  `json:"error,omitempty"`
	SkipReason string  `json:"skip_reason,omitempty"`
}

// ImportExecuteResponse is returned after executing the import
type ImportExecuteResponse struct {
	Results      []ImportRowResult `json:"results"`
	TotalRows    int               `json:"total_rows"`
	SuccessCount int               `json:"success_count"`
	FailedCount  int               `json:"failed_count"`
	SkippedCount int               `json:"skipped_count"`
}

type PortableMonitorExport struct {
	Kind     string                  `json:"kind" yaml:"kind"`
	Version  int                     `json:"version" yaml:"version"`
	Monitors []PortableExportMonitor `json:"monitors" yaml:"monitors"`
}

type PortableExportMonitor struct {
	Name             string      `json:"name" yaml:"name"`
	Type             MonitorType `json:"type" yaml:"type"`
	IntervalSeconds  int         `json:"interval_seconds" yaml:"interval_seconds"`
	TimeoutSeconds   int         `json:"timeout_seconds" yaml:"timeout_seconds"`
	Enabled          bool        `json:"enabled" yaml:"enabled"`
	Tags             []string    `json:"tags,omitempty" yaml:"tags,omitempty"`
	Config           interface{} `json:"config" yaml:"config"`
	AlertPolicyNames []string    `json:"alert_policy_names,omitempty" yaml:"alert_policy_names,omitempty"`
	GroupMembers     []string    `json:"group_members,omitempty" yaml:"group_members,omitempty"`
}
