package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// MonitorType represents the type of monitor
type MonitorType string

const (
	MonitorTypeHTTP             MonitorType = "http"
	MonitorTypePing             MonitorType = "ping"
	MonitorTypeDNS              MonitorType = "dns"
	MonitorTypeGRPC             MonitorType = "grpc"
	MonitorTypeGroup            MonitorType = "group"
	MonitorTypeAgent            MonitorType = "agent"
	MonitorTypePush             MonitorType = "push"
	MonitorTypeSIP              MonitorType = "sip"
	MonitorTypeSyntheticAPI     MonitorType = "synthetic_api"
	MonitorTypeSyntheticBrowser MonitorType = "synthetic_browser"
)

// Monitor represents a monitor in the system
type Monitor struct {
	ID              uuid.UUID       `json:"id"`
	TenantID        uuid.UUID       `json:"tenant_id"`
	Name            string          `json:"name"`
	Type            MonitorType     `json:"type"`
	Config          json.RawMessage `json:"config"`
	IntervalSeconds int             `json:"interval_seconds"`
	TimeoutSeconds  int             `json:"timeout_seconds"`
	AlertPolicyID   *uuid.UUID      `json:"alert_policy_id,omitempty"`
	AlertPolicyIDs  []uuid.UUID     `json:"alert_policy_ids,omitempty"`
	Enabled         bool            `json:"enabled"`
	Tags            []string        `json:"tags,omitempty"`
	NextRunAt       *time.Time      `json:"next_run_at,omitempty"`
	AgentID         *string         `json:"agent_id,omitempty"`   // Unique identifier for agent monitors
	PushToken       *string         `json:"push_token,omitempty"` // Unique token for push monitors
	MemberIDs       []uuid.UUID     `json:"member_ids,omitempty"` // Populated for group monitors
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	DeletedAt       *time.Time      `json:"deleted_at,omitempty"`
}

// CreateMonitorRequest represents a request to create a monitor
type CreateMonitorRequest struct {
	Name            string          `json:"name"`
	Type            MonitorType     `json:"type"`
	Config          json.RawMessage `json:"config"`
	IntervalSeconds int             `json:"interval_seconds"`
	TimeoutSeconds  int             `json:"timeout_seconds"`
	AlertPolicyID   *string         `json:"alert_policy_id,omitempty"`
	AlertPolicyIDs  []string        `json:"alert_policy_ids,omitempty"`
	Enabled         *bool           `json:"enabled,omitempty"`
	Tags            []string        `json:"tags,omitempty"`
}

// UpdateMonitorRequest represents a request to update a monitor
type UpdateMonitorRequest struct {
	Name            *string         `json:"name,omitempty"`
	Type            *MonitorType    `json:"type,omitempty"`
	Config          json.RawMessage `json:"config,omitempty"`
	IntervalSeconds *int            `json:"interval_seconds,omitempty"`
	TimeoutSeconds  *int            `json:"timeout_seconds,omitempty"`
	AlertPolicyID   *string         `json:"alert_policy_id,omitempty"`
	AlertPolicyIDs  *[]string       `json:"alert_policy_ids,omitempty"`
	Enabled         *bool           `json:"enabled,omitempty"`
	Tags            *[]string       `json:"tags,omitempty"`
}

// MonitorListResponse represents a paginated list of monitors
type MonitorListResponse struct {
	Items    []Monitor `json:"items"`
	Page     int       `json:"page"`
	PageSize int       `json:"page_size"`
	Total    int       `json:"total"`
}

// CheckResult represents a check result
type CheckResult struct {
	ID           uuid.UUID       `json:"id"`
	Status       string          `json:"status"` // "success", "failure", "error"
	ResultSource string          `json:"result_source"`
	HTTPStatus   *int            `json:"http_status,omitempty"`
	LatencyMS    *int            `json:"latency_ms,omitempty"`
	ErrorMessage *string         `json:"error_message,omitempty"`
	MetricsData  json.RawMessage `json:"metrics_data"` // Raw JSON for agent metrics
	CreatedAt    time.Time       `json:"created_at"`
}

// MonitorResultsResponse represents a response containing monitor check results
type MonitorResultsResponse struct {
	MonitorID uuid.UUID     `json:"monitor_id"`
	Results   []CheckResult `json:"results"`
}

type MonitorAnalyticsRange string

const (
	MonitorAnalyticsRange1h   MonitorAnalyticsRange = "1h"
	MonitorAnalyticsRange6h   MonitorAnalyticsRange = "6h"
	MonitorAnalyticsRange24h  MonitorAnalyticsRange = "24h"
	MonitorAnalyticsRange7d   MonitorAnalyticsRange = "7d"
	MonitorAnalyticsRange30d  MonitorAnalyticsRange = "30d"
	MonitorAnalyticsRange90d  MonitorAnalyticsRange = "90d"
	MonitorAnalyticsRange365d MonitorAnalyticsRange = "365d"
)

type AnalyticsSource string

const (
	AnalyticsSourceRaw    AnalyticsSource = "raw"
	AnalyticsSourceRollup AnalyticsSource = "rollup"
)

type MonitorAnalyticsSummary struct {
	UptimePct       float64    `json:"uptime_pct"`
	SLAPct          float64    `json:"sla_pct"`
	DowntimePct     float64    `json:"downtime_pct"`
	AvgLatencyMS    *float64   `json:"avg_latency_ms,omitempty"`
	MedianLatencyMS *float64   `json:"median_latency_ms,omitempty"`
	P95LatencyMS    *float64   `json:"p95_latency_ms,omitempty"`
	LatestStatus    *string    `json:"latest_status,omitempty"`
	LatestCheckAt   *time.Time `json:"latest_check_at,omitempty"`
}

type MonitorAnalyticsSeriesPoint struct {
	BucketStart  time.Time `json:"bucket_start"`
	UptimePct    float64   `json:"uptime_pct"`
	AvgLatencyMS *float64  `json:"avg_latency_ms,omitempty"`
	TotalChecks  int       `json:"total_checks"`
	HasData      bool      `json:"has_data"`
}

type MonitorAnalyticsDowntimePeriod struct {
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	IsOpen    bool      `json:"is_open"`
}

type MonitorAnalyticsResponse struct {
	MonitorID       uuid.UUID                        `json:"monitor_id"`
	Range           MonitorAnalyticsRange            `json:"range"`
	GeneratedAt     time.Time                        `json:"generated_at"`
	Source          AnalyticsSource                  `json:"source"`
	CoverageStart   *time.Time                       `json:"coverage_start,omitempty"`
	IsPartial       bool                             `json:"is_partial"`
	Summary         MonitorAnalyticsSummary          `json:"summary"`
	UptimeSeries    []MonitorAnalyticsSeriesPoint    `json:"uptime_series"`
	LatencySeries   []MonitorAnalyticsSeriesPoint    `json:"latency_series"`
	DowntimePeriods []MonitorAnalyticsDowntimePeriod `json:"downtime_periods"`
}

// RunMonitorNowResponse represents a response for an on-demand monitor run
type RunMonitorNowResponse struct {
	JobID     string    `json:"job_id"`
	MonitorID uuid.UUID `json:"monitor_id"`
	QueuedAt  time.Time `json:"queued_at"`
	Deadline  time.Time `json:"deadline"`
}

// GroupConfig represents the configuration for a group monitor
type GroupConfig struct {
	MonitorIDs []string `json:"monitor_ids"`
}

// AgentConfig represents the configuration for an agent monitor
type AgentConfig struct {
	AgentID                 string `json:"agent_id"`
	ExpectedIntervalSeconds int    `json:"expected_interval_seconds"`
}

// PushConfig represents the configuration for a push monitor
type PushConfig struct {
	PushToken               string `json:"push_token"`
	ExpectedIntervalSeconds int    `json:"expected_interval_seconds"`
	GracePeriodSeconds      int    `json:"grace_period_seconds"` // Debounce buffer before marking as down
}

// SIPConfig represents the configuration for a SIP monitor
type SIPConfig struct {
	Host           string `json:"host"`                      // SIP server hostname/IP
	Port           int    `json:"port"`                      // Default 5060
	Transport      string `json:"transport"`                 // "udp" or "tcp" (default: udp)
	ExpectedStatus *int   `json:"expected_status,omitempty"` // Expected SIP response (default: 200)
}

// DNSConfig represents the configuration for a DNS monitor
type DNSConfig struct {
	Host            string   `json:"host"`
	RecordType      string   `json:"record_type,omitempty"`      // A, AAAA, CNAME, TXT, MX, NS
	ExpectedAnswers []string `json:"expected_answers,omitempty"` // Optional expected answers
}

// GRPCConfig represents the configuration for a gRPC health monitor
type GRPCConfig struct {
	Host    string `json:"host"`
	Port    int    `json:"port,omitempty"`
	Service string `json:"service,omitempty"`
	UseTLS  *bool  `json:"use_tls,omitempty"`
}

// SyntheticAPIConfig represents the configuration for a synthetic API monitor
type SyntheticAPIConfig struct {
	BaseURL     string                   `json:"base_url,omitempty"`
	FailureMode string                   `json:"failure_mode,omitempty"` // fail_fast (default) or continue
	Variables   map[string]string        `json:"variables,omitempty"`
	Steps       []SyntheticAPIStepConfig `json:"steps"`
}

type SyntheticAPIStepConfig struct {
	ID      string                        `json:"id"`
	Name    string                        `json:"name,omitempty"`
	Request SyntheticAPIRequestConfig     `json:"request"`
	Assert  []SyntheticAPIAssertionConfig `json:"assert,omitempty"`
	Extract []SyntheticAPIExtractConfig   `json:"extract,omitempty"`
}

type SyntheticAPIRequestConfig struct {
	Method          string            `json:"method"`
	URL             string            `json:"url"`
	Headers         map[string]string `json:"headers,omitempty"`
	Body            *string           `json:"body,omitempty"`
	TimeoutSeconds  *int              `json:"timeout_seconds,omitempty"`
	FollowRedirects *bool             `json:"follow_redirects,omitempty"`
	MaxRedirects    *int              `json:"max_redirects,omitempty"`
}

type SyntheticAPIAssertionConfig struct {
	Target string          `json:"target"` // status | header | body | json
	Op     string          `json:"op"`     // target-dependent
	Path   string          `json:"path,omitempty"`
	Value  json.RawMessage `json:"value,omitempty"`
}

type SyntheticAPIExtractConfig struct {
	Name      string `json:"name"`
	From      string `json:"from"` // json | header
	Path      string `json:"path"`
	Sensitive *bool  `json:"sensitive,omitempty"`
}

// SyntheticBrowserConfig represents the configuration for a synthetic browser monitor
type SyntheticBrowserConfig struct {
	StartURL    string                       `json:"start_url"`
	Device      string                       `json:"device,omitempty"`
	FailureMode string                       `json:"failure_mode,omitempty"` // fail_fast (default) or continue
	Variables   map[string]string            `json:"variables,omitempty"`
	Artifacts   SyntheticBrowserArtifacts    `json:"artifacts,omitempty"`
	Steps       []SyntheticBrowserStepConfig `json:"steps"`
}

type SyntheticBrowserArtifacts struct {
	ScreenshotOnFailure bool `json:"screenshot_on_failure,omitempty"`
	TraceOnFailure      bool `json:"trace_on_failure,omitempty"`
	HarOnFailure        bool `json:"har_on_failure,omitempty"`
}

type SyntheticBrowserStepConfig struct {
	ID             string  `json:"id"`
	Action         string  `json:"action"` // goto | click | fill | wait_for | assert_visible | assert_text | assert_url
	URL            string  `json:"url,omitempty"`
	Selector       string  `json:"selector,omitempty"`
	Value          *string `json:"value,omitempty"`
	TimeoutSeconds *int    `json:"timeout_seconds,omitempty"`
}

// PushInfo represents webhook info returned to the user
type PushInfo struct {
	PushToken       string `json:"push_token"`
	WebhookURL      string `json:"webhook_url"`
	IntervalSeconds int    `json:"interval_seconds"`
	GracePeriodSecs int    `json:"grace_period_seconds"`
	ExampleCurl     string `json:"example_curl"`
}

// AddMonitorsToGroupRequest represents a request to add monitors to a group
type AddMonitorsToGroupRequest struct {
	MonitorIDs []string `json:"monitor_ids"`
}

// RemoveMonitorsFromGroupRequest represents a request to remove monitors from a group
type RemoveMonitorsFromGroupRequest struct {
	MonitorIDs []string `json:"monitor_ids"`
}

// BulkDeleteMonitorsRequest is the body of POST /api/v1/monitors/bulk/delete.
type BulkDeleteMonitorsRequest struct {
	MonitorIDs []string `json:"monitor_ids"`
}

// BulkDeleteMonitorsResponse is the response to a bulk delete.
type BulkDeleteMonitorsResponse struct {
	Deleted int64 `json:"deleted"`
}

// BulkAlertPolicyOp is the operation type for BulkUpdateAlertPolicyRequest.
type BulkAlertPolicyOp string

const (
	BulkAlertPolicyOpAttach BulkAlertPolicyOp = "attach"
	BulkAlertPolicyOpDetach BulkAlertPolicyOp = "detach"
)

// BulkUpdateAlertPolicyRequest is the body for POST /v1/monitors/bulk/alert-policy.
type BulkUpdateAlertPolicyRequest struct {
	MonitorIDs []string          `json:"monitor_ids"`
	PolicyID   string            `json:"policy_id"`
	Op         BulkAlertPolicyOp `json:"op"`
}

// BulkUpdateAlertPolicyResponse is the response for POST /v1/monitors/bulk/alert-policy.
type BulkUpdateAlertPolicyResponse struct {
	Updated           int         `json:"updated"`
	Unchanged         int         `json:"unchanged"`
	MonitorIDsUpdated []uuid.UUID `json:"monitor_ids_updated"`
}
