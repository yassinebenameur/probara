package models

import "encoding/json"

type HTTPStatusRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

type HTTPHeaderAssertion struct {
	Name            string  `json:"name"`
	Op              string  `json:"op"` // exists, equals, contains, regex, not_equals, not_contains, not_regex
	Value           *string `json:"value,omitempty"`
	CaseInsensitive *bool   `json:"case_insensitive,omitempty"`
}

type HTTPBodyAssertion struct {
	Op              string `json:"op"` // contains, not_contains, regex, not_regex
	Value           string `json:"value"`
	CaseInsensitive *bool  `json:"case_insensitive,omitempty"`
}

type HTTPJSONAssertion struct {
	Path            string  `json:"path"` // gjson path
	Op              string  `json:"op"`   // exists, equals, not_equals, contains, not_contains, regex, number_gt, number_gte, number_lt, number_lte, bool_is
	Value           *string `json:"value,omitempty"`
	CaseInsensitive *bool   `json:"case_insensitive,omitempty"`
}

// HTTPMonitorConfig represents configuration for HTTP monitors
type HTTPMonitorConfig struct {
	URL                      string                `json:"url"`
	Method                   string                `json:"method"`
	Headers                  map[string]string     `json:"headers,omitempty"`
	Body                     *string               `json:"body,omitempty"`
	ExpectedStatus           *int                  `json:"expected_status,omitempty"`
	ExpectedStatuses         []int                 `json:"expected_statuses,omitempty"`
	ExpectedStatusRanges     []HTTPStatusRange     `json:"expected_status_ranges,omitempty"`
	ExpectedStatusClasses    []string              `json:"expected_status_classes,omitempty"` // e.g. ["2xx", "3xx"]
	ExpectedBodySubstring    *string               `json:"expected_body_substring,omitempty"`
	ExpectedBodyRegex        *string               `json:"expected_body_regex,omitempty"`
	BodyAssertions           []HTTPBodyAssertion   `json:"body_assertions,omitempty"`
	ResponseHeaderAssertions []HTTPHeaderAssertion `json:"response_header_assertions,omitempty"`
	JSONAssertions           []HTTPJSONAssertion   `json:"json_assertions,omitempty"`
	MaxLatencyMs             *int64                `json:"max_latency_ms,omitempty"`
	FollowRedirects          *bool                 `json:"follow_redirects,omitempty"`
	MaxRedirects             *int                  `json:"max_redirects,omitempty"`
	TLSSkipVerify            *bool                 `json:"tls_skip_verify,omitempty"`
	TLSMinDaysValid          *int                  `json:"tls_min_days_valid,omitempty"`
	TLSServerName            *string               `json:"tls_server_name,omitempty"`
	TLSCAPem                 *string               `json:"tls_ca_pem,omitempty"`
	CollectTiming            *bool                 `json:"collect_timing,omitempty"`
}

// PingMonitorConfig represents configuration for ping monitors
type PingMonitorConfig struct {
	Host string `json:"host"`
}

// DNSMonitorConfig represents configuration for DNS monitors
type DNSMonitorConfig struct {
	Host            string   `json:"host"`
	RecordType      string   `json:"record_type,omitempty"`      // A, AAAA, CNAME, TXT, MX, NS
	ExpectedAnswers []string `json:"expected_answers,omitempty"` // Optional expected answers
}

// GroupMonitorConfig represents configuration for group monitors
type GroupMonitorConfig struct {
	MonitorIDs []string `json:"monitor_ids"`
}

// SIPMonitorConfig represents configuration for SIP monitors
type SIPMonitorConfig struct {
	Host           string `json:"host"`                      // SIP server hostname/IP
	Port           int    `json:"port"`                      // Default 5060
	Transport      string `json:"transport"`                 // "udp" or "tcp" (default: udp)
	ExpectedStatus *int   `json:"expected_status,omitempty"` // Expected SIP response (default: 200)
}

// SyntheticAPIMonitorConfig represents configuration for synthetic API workflow monitors
type SyntheticAPIMonitorConfig struct {
	BaseURL     string                   `json:"base_url,omitempty"`
	FailureMode string                   `json:"failure_mode,omitempty"` // fail_fast (default) | continue
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

// SyntheticBrowserMonitorConfig represents configuration for synthetic browser journey monitors
type SyntheticBrowserMonitorConfig struct {
	StartURL    string                       `json:"start_url"`
	Device      string                       `json:"device,omitempty"`
	FailureMode string                       `json:"failure_mode,omitempty"` // fail_fast (default) | continue
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

// CheckJobPayload represents the payload for a check job
type CheckJobPayload struct {
	MonitorID      string          `json:"monitor_id"`
	Type           string          `json:"type"`
	Config         json.RawMessage `json:"config"`
	TimeoutSeconds int             `json:"timeout_seconds"`
}
