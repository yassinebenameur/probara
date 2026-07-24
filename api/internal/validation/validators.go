package validation

import (
	"crypto/x509"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/yassinebenameur/probara/api/internal/models"
	sharedmodels "github.com/yassinebenameur/probara/shared/models"
)

// HTTPConfigValidator validates HTTP monitor configuration
type HTTPConfigValidator struct{}

// ValidateConfig validates HTTP monitor config
func (v *HTTPConfigValidator) ValidateConfig(configRaw json.RawMessage) error {
	var config sharedmodels.HTTPMonitorConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return fmt.Errorf("invalid HTTP config: %w", err)
	}

	if config.URL == "" {
		return fmt.Errorf("url is required")
	}

	if err := validateURL(config.URL); err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}

	parsedURL, _ := url.Parse(config.URL)

	if config.Method == "" {
		return fmt.Errorf("method is required")
	}

	method := strings.ToUpper(config.Method)
	validMethods := map[string]bool{
		"GET":     true,
		"POST":    true,
		"PUT":     true,
		"PATCH":   true,
		"DELETE":  true,
		"HEAD":    true,
		"OPTIONS": true,
	}
	if !validMethods[method] {
		return fmt.Errorf("method must be one of: GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS")
	}

	if config.ExpectedStatus != nil && (*config.ExpectedStatus < 100 || *config.ExpectedStatus > 599) {
		return fmt.Errorf("expected_status must be between 100 and 599")
	}

	for _, code := range config.ExpectedStatuses {
		if code < 100 || code > 599 {
			return fmt.Errorf("expected_statuses must contain only values between 100 and 599")
		}
	}

	for _, r := range config.ExpectedStatusRanges {
		if r.Min < 100 || r.Min > 599 || r.Max < 100 || r.Max > 599 {
			return fmt.Errorf("expected_status_ranges must contain only values between 100 and 599")
		}
		if r.Min > r.Max {
			return fmt.Errorf("expected_status_ranges min must be <= max")
		}
	}

	for _, cls := range config.ExpectedStatusClasses {
		cls = strings.TrimSpace(strings.ToLower(cls))
		if cls == "" {
			return fmt.Errorf("expected_status_classes cannot contain empty values")
		}
		if len(cls) != 3 || !strings.HasSuffix(cls, "xx") || cls[0] < '1' || cls[0] > '5' {
			return fmt.Errorf("expected_status_classes must be one of: 1xx, 2xx, 3xx, 4xx, 5xx")
		}
	}

	if config.ExpectedBodyRegex != nil && strings.TrimSpace(*config.ExpectedBodyRegex) != "" {
		if _, err := regexp.Compile(*config.ExpectedBodyRegex); err != nil {
			return fmt.Errorf("expected_body_regex is invalid: %w", err)
		}
	}

	if err := validateHTTPBodyAssertions(config.BodyAssertions); err != nil {
		return err
	}

	if err := validateHTTPHeaderAssertions(config.ResponseHeaderAssertions); err != nil {
		return err
	}

	if err := validateHTTPJSONAssertions(config.JSONAssertions); err != nil {
		return err
	}

	if config.MaxLatencyMs != nil && *config.MaxLatencyMs <= 0 {
		return fmt.Errorf("max_latency_ms must be greater than 0")
	}

	if config.MaxRedirects != nil {
		if *config.MaxRedirects < 0 {
			return fmt.Errorf("max_redirects must be 0 or greater")
		}
		if *config.MaxRedirects > 50 {
			return fmt.Errorf("max_redirects must be 50 or less")
		}
	}

	if config.TLSSkipVerify != nil && parsedURL.Scheme != "https" {
		return fmt.Errorf("tls_skip_verify requires an https url")
	}

	if config.TLSMinDaysValid != nil {
		if *config.TLSMinDaysValid < 0 {
			return fmt.Errorf("tls_min_days_valid must be 0 or greater")
		}
		if parsedURL.Scheme != "https" {
			return fmt.Errorf("tls_min_days_valid requires an https url")
		}
	}

	if config.TLSServerName != nil && strings.TrimSpace(*config.TLSServerName) == "" {
		return fmt.Errorf("tls_server_name cannot be empty")
	}

	if config.TLSCAPem != nil && strings.TrimSpace(*config.TLSCAPem) != "" {
		if parsedURL.Scheme != "https" {
			return fmt.Errorf("tls_ca_pem requires an https url")
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(*config.TLSCAPem)) {
			return fmt.Errorf("tls_ca_pem must be valid PEM encoded certificates")
		}
	}

	return nil
}

func validateHTTPBodyAssertions(assertions []sharedmodels.HTTPBodyAssertion) error {
	for i, a := range assertions {
		op := strings.TrimSpace(strings.ToLower(a.Op))
		if op == "" {
			return fmt.Errorf("body_assertions[%d].op is required", i)
		}
		if strings.TrimSpace(a.Value) == "" {
			return fmt.Errorf("body_assertions[%d].value is required", i)
		}

		switch op {
		case "contains", "not_contains":
			// ok
		case "regex", "not_regex":
			pattern := a.Value
			if a.CaseInsensitive != nil && *a.CaseInsensitive {
				pattern = "(?i)" + pattern
			}
			if _, err := regexp.Compile(pattern); err != nil {
				return fmt.Errorf("body_assertions[%d].value invalid regex: %w", i, err)
			}
		default:
			return fmt.Errorf("body_assertions[%d].op must be one of: contains, not_contains, regex, not_regex", i)
		}
	}
	return nil
}

func validateHTTPHeaderAssertions(assertions []sharedmodels.HTTPHeaderAssertion) error {
	for i, a := range assertions {
		name := strings.TrimSpace(a.Name)
		if name == "" {
			return fmt.Errorf("response_header_assertions[%d].name is required", i)
		}
		op := strings.TrimSpace(strings.ToLower(a.Op))
		if op == "" {
			return fmt.Errorf("response_header_assertions[%d].op is required", i)
		}

		requiresValue := op != "exists"
		if requiresValue && (a.Value == nil || strings.TrimSpace(*a.Value) == "") {
			return fmt.Errorf("response_header_assertions[%d].value is required for op %s", i, op)
		}

		switch op {
		case "exists", "equals", "contains", "not_equals", "not_contains":
			// ok
		case "regex", "not_regex":
			pattern := ""
			if a.Value != nil {
				pattern = *a.Value
			}
			if a.CaseInsensitive != nil && *a.CaseInsensitive {
				pattern = "(?i)" + pattern
			}
			if _, err := regexp.Compile(pattern); err != nil {
				return fmt.Errorf("response_header_assertions[%d].value invalid regex: %w", i, err)
			}
		default:
			return fmt.Errorf("response_header_assertions[%d].op must be one of: exists, equals, contains, regex, not_equals, not_contains, not_regex", i)
		}
	}
	return nil
}

func validateHTTPJSONAssertions(assertions []sharedmodels.HTTPJSONAssertion) error {
	for i, a := range assertions {
		path := strings.TrimSpace(a.Path)
		if path == "" {
			return fmt.Errorf("json_assertions[%d].path is required", i)
		}
		op := strings.TrimSpace(strings.ToLower(a.Op))
		if op == "" {
			return fmt.Errorf("json_assertions[%d].op is required", i)
		}

		requiresValue := op != "exists"
		if requiresValue && (a.Value == nil || strings.TrimSpace(*a.Value) == "") {
			return fmt.Errorf("json_assertions[%d].value is required for op %s", i, op)
		}

		switch op {
		case "exists", "equals", "not_equals", "contains", "not_contains":
			// ok
		case "regex":
			pattern := ""
			if a.Value != nil {
				pattern = *a.Value
			}
			if a.CaseInsensitive != nil && *a.CaseInsensitive {
				pattern = "(?i)" + pattern
			}
			if _, err := regexp.Compile(pattern); err != nil {
				return fmt.Errorf("json_assertions[%d].value invalid regex: %w", i, err)
			}
		case "number_gt", "number_gte", "number_lt", "number_lte":
			if a.Value == nil {
				return fmt.Errorf("json_assertions[%d].value is required", i)
			}
			if _, err := strconv.ParseFloat(strings.TrimSpace(*a.Value), 64); err != nil {
				return fmt.Errorf("json_assertions[%d].value must be a number: %w", i, err)
			}
		case "bool_is":
			if a.Value == nil {
				return fmt.Errorf("json_assertions[%d].value is required", i)
			}
			if _, err := strconv.ParseBool(strings.TrimSpace(strings.ToLower(*a.Value))); err != nil {
				return fmt.Errorf("json_assertions[%d].value must be a bool: %w", i, err)
			}
		default:
			return fmt.Errorf("json_assertions[%d].op must be one of: exists, equals, not_equals, contains, not_contains, regex, number_gt, number_gte, number_lt, number_lte, bool_is", i)
		}
	}
	return nil
}

// PingConfigValidator validates ping monitor configuration
type PingConfigValidator struct{}

// ValidateConfig validates ping monitor config
func (v *PingConfigValidator) ValidateConfig(configRaw json.RawMessage) error {
	var config sharedmodels.PingMonitorConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return fmt.Errorf("invalid ping config: %w", err)
	}

	if config.Host == "" {
		return fmt.Errorf("host is required")
	}

	// Validate host is either a valid hostname or IP address
	if ip := net.ParseIP(config.Host); ip == nil {
		// Not an IP, check if it's a valid hostname
		if !isValidHostname(config.Host) {
			return fmt.Errorf("host must be a valid IP address or hostname")
		}
	}

	return nil
}

// DNSConfigValidator validates DNS monitor configuration
type DNSConfigValidator struct{}

// ValidateConfig validates DNS monitor config
func (v *DNSConfigValidator) ValidateConfig(configRaw json.RawMessage) error {
	var config sharedmodels.DNSMonitorConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return fmt.Errorf("invalid dns config: %w", err)
	}

	host := strings.TrimSpace(config.Host)
	if host == "" {
		return fmt.Errorf("host is required")
	}

	// Validate host is either a valid hostname or IP address
	if ip := net.ParseIP(host); ip == nil {
		if !isValidHostname(host) {
			return fmt.Errorf("host must be a valid IP address or hostname")
		}
	}

	if config.RecordType != "" {
		recordType := strings.ToUpper(strings.TrimSpace(config.RecordType))
		switch recordType {
		case "A", "AAAA", "CNAME", "TXT", "MX", "NS":
			// ok
		default:
			return fmt.Errorf("record_type must be one of: A, AAAA, CNAME, TXT, MX, NS")
		}
	}

	for i, ans := range config.ExpectedAnswers {
		if strings.TrimSpace(ans) == "" {
			return fmt.Errorf("expected_answers[%d] cannot be empty", i)
		}
	}

	if ns := strings.TrimSpace(config.Nameserver); ns != "" {
		nsHost := ns
		if h, p, err := net.SplitHostPort(ns); err == nil {
			nsHost = h
			if port, err := strconv.Atoi(p); err != nil || port < 1 || port > 65535 {
				return fmt.Errorf("nameserver port must be between 1 and 65535")
			}
		}
		if ip := net.ParseIP(nsHost); ip == nil {
			if !isValidHostname(nsHost) {
				return fmt.Errorf("nameserver must be a valid IP address or hostname (host or host:port)")
			}
		}
	}

	return nil
}

// GRPCConfigValidator validates gRPC health monitor configuration
type GRPCConfigValidator struct{}

// ValidateConfig validates gRPC monitor config
func (v *GRPCConfigValidator) ValidateConfig(configRaw json.RawMessage) error {
	var config sharedmodels.GRPCMonitorConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return fmt.Errorf("invalid grpc config: %w", err)
	}

	host := strings.TrimSpace(config.Host)
	if host == "" {
		return fmt.Errorf("host is required")
	}

	if ip := net.ParseIP(host); ip == nil {
		if !isValidHostname(host) {
			return fmt.Errorf("host must be a valid IP address or hostname")
		}
	}

	// Port is optional; checker will default to 443 for TLS or 80 for plaintext.
	if config.Port != 0 && (config.Port < 1 || config.Port > 65535) {
		return fmt.Errorf("port must be between 1 and 65535")
	}

	if config.Service != "" && strings.TrimSpace(config.Service) == "" {
		return fmt.Errorf("service cannot be empty")
	}

	return nil
}

// TCPConfigValidator validates raw TCP connect monitor configuration
type TCPConfigValidator struct{}

// ValidateConfig validates TCP monitor config
func (v *TCPConfigValidator) ValidateConfig(configRaw json.RawMessage) error {
	var config sharedmodels.TCPMonitorConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return fmt.Errorf("invalid tcp config: %w", err)
	}

	host := strings.TrimSpace(config.Host)
	if host == "" {
		return fmt.Errorf("host is required")
	}

	if ip := net.ParseIP(host); ip == nil {
		if !isValidHostname(host) {
			return fmt.Errorf("host must be a valid IP address or hostname")
		}
	}

	if config.Port < 1 || config.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}

	return nil
}

// WebSocketConfigValidator validates WebSocket monitor configuration
type WebSocketConfigValidator struct{}

// ValidateConfig validates WebSocket monitor config
func (v *WebSocketConfigValidator) ValidateConfig(configRaw json.RawMessage) error {
	var config sharedmodels.WebSocketMonitorConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return fmt.Errorf("invalid websocket config: %w", err)
	}

	rawURL := strings.TrimSpace(config.URL)
	if rawURL == "" {
		return fmt.Errorf("url is required")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("url must be a valid URL")
	}
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return fmt.Errorf("url must use the ws:// or wss:// scheme")
	}
	if u.Host == "" {
		return fmt.Errorf("url must include a host")
	}

	for name := range config.Headers {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("header names cannot be empty")
		}
	}

	if config.ExpectedSubstring != nil && *config.ExpectedSubstring == "" {
		return fmt.Errorf("expected_substring cannot be empty")
	}

	if config.MaxLatencyMs != nil && *config.MaxLatencyMs <= 0 {
		return fmt.Errorf("max_latency_ms must be greater than 0")
	}
	if config.WarnLatencyMs != nil && *config.WarnLatencyMs <= 0 {
		return fmt.Errorf("warn_latency_ms must be greater than 0")
	}
	if config.MaxLatencyMs != nil && config.WarnLatencyMs != nil && *config.WarnLatencyMs >= *config.MaxLatencyMs {
		return fmt.Errorf("warn_latency_ms must be lower than max_latency_ms")
	}

	return nil
}

// GroupConfigValidator validates group monitor configuration
type GroupConfigValidator struct{}

// ValidateConfig validates group monitor config
func (v *GroupConfigValidator) ValidateConfig(configRaw json.RawMessage) error {
	var config models.GroupConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return fmt.Errorf("invalid group config: %w", err)
	}

	if len(config.MonitorIDs) == 0 {
		return fmt.Errorf("monitor_ids is required and cannot be empty")
	}

	// Validate that all monitor IDs are non-empty
	for _, idStr := range config.MonitorIDs {
		if idStr == "" {
			return fmt.Errorf("monitor_ids cannot contain empty strings")
		}
	}

	return nil
}

// AgentConfigValidator validates agent monitor configuration
type AgentConfigValidator struct{}

// ValidateConfig validates agent monitor config
func (v *AgentConfigValidator) ValidateConfig(configRaw json.RawMessage) error {
	var config models.AgentConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return fmt.Errorf("invalid agent config: %w", err)
	}

	// Agent ID can be empty on creation (will be generated by backend)
	if config.ExpectedIntervalSeconds < minIntervalSeconds {
		return fmt.Errorf("expected_interval_seconds must be at least %d", minIntervalSeconds)
	}

	if config.ExpectedIntervalSeconds > maxIntervalSeconds {
		return fmt.Errorf("expected_interval_seconds must be at most %d", maxIntervalSeconds)
	}

	return nil
}

// PushConfigValidator validates push monitor configuration
type PushConfigValidator struct{}

// ValidateConfig validates push monitor config
func (v *PushConfigValidator) ValidateConfig(configRaw json.RawMessage) error {
	var config models.PushConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return fmt.Errorf("invalid push config: %w", err)
	}

	// Push token can be empty on creation (will be generated by backend)
	if config.ExpectedIntervalSeconds < minIntervalSeconds {
		return fmt.Errorf("expected_interval_seconds must be at least %d", minIntervalSeconds)
	}

	if config.ExpectedIntervalSeconds > maxIntervalSeconds {
		return fmt.Errorf("expected_interval_seconds must be at most %d", maxIntervalSeconds)
	}

	// Grace period cannot be negative
	if config.GracePeriodSeconds < 0 {
		return fmt.Errorf("grace_period_seconds cannot be negative")
	}

	return nil
}

// SIPConfigValidator validates SIP monitor configuration
type SIPConfigValidator struct{}

// ValidateConfig validates SIP monitor config
func (v *SIPConfigValidator) ValidateConfig(configRaw json.RawMessage) error {
	var config models.SIPConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return fmt.Errorf("invalid SIP config: %w", err)
	}

	if config.Host == "" {
		return fmt.Errorf("host is required")
	}

	// Validate host is either a valid hostname or IP address
	if ip := net.ParseIP(config.Host); ip == nil {
		// Not an IP, check if it's a valid hostname
		if !isValidHostname(config.Host) {
			return fmt.Errorf("host must be a valid IP address or hostname")
		}
	}

	// Validate port (default is 5060 if not specified)
	if config.Port != 0 && (config.Port < 1 || config.Port > 65535) {
		return fmt.Errorf("port must be between 1 and 65535")
	}

	// Validate transport
	if config.Transport != "" {
		transport := strings.ToLower(config.Transport)
		if transport != "udp" && transport != "tcp" && transport != "tls" {
			return fmt.Errorf("transport must be 'udp', 'tcp', or 'tls'")
		}
	}

	// Validate method
	if config.Method != "" {
		method := strings.ToLower(config.Method)
		if method != "options" && method != "register" {
			return fmt.Errorf("method must be 'options' or 'register'")
		}
	}

	// Digest credentials go together: a password without a username (or vice
	// versa) is a config mistake, not a server-side condition.
	if (strings.TrimSpace(config.Username) == "") != (config.Password == "") {
		return fmt.Errorf("username and password must be provided together")
	}

	// Validate expected status (SIP response codes are 100-699)
	if config.ExpectedStatus != nil && (*config.ExpectedStatus < 100 || *config.ExpectedStatus > 699) {
		return fmt.Errorf("expected_status must be between 100 and 699")
	}

	return nil
}

// SyntheticAPIConfigValidator validates synthetic API monitor configuration.
type SyntheticAPIConfigValidator struct{}

// ValidateConfig validates synthetic API monitor config.
func (v *SyntheticAPIConfigValidator) ValidateConfig(configRaw json.RawMessage) error {
	var config sharedmodels.SyntheticAPIMonitorConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return fmt.Errorf("invalid synthetic_api config: %w", err)
	}

	if config.BaseURL != "" {
		if err := validateURL(config.BaseURL); err != nil {
			return fmt.Errorf("invalid base_url: %w", err)
		}
	}

	if err := validateSyntheticFailureMode(config.FailureMode); err != nil {
		return err
	}

	if len(config.Steps) == 0 {
		return fmt.Errorf("steps is required and cannot be empty")
	}
	if len(config.Steps) > 20 {
		return fmt.Errorf("steps cannot exceed 20")
	}

	for key := range config.Variables {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("variables cannot contain empty keys")
		}
	}

	validMethods := map[string]bool{
		"GET":     true,
		"POST":    true,
		"PUT":     true,
		"PATCH":   true,
		"DELETE":  true,
		"HEAD":    true,
		"OPTIONS": true,
	}

	stepIDs := make(map[string]bool, len(config.Steps))
	extractNames := make(map[string]bool)
	for i, step := range config.Steps {
		stepID := strings.TrimSpace(step.ID)
		if stepID == "" {
			return fmt.Errorf("steps[%d].id is required", i)
		}
		if stepIDs[stepID] {
			return fmt.Errorf("steps[%d].id must be unique", i)
		}
		stepIDs[stepID] = true

		method := strings.ToUpper(strings.TrimSpace(step.Request.Method))
		if method == "" {
			method = "GET"
		}
		if !validMethods[method] {
			return fmt.Errorf("steps[%d].request.method must be one of: GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS", i)
		}

		reqURL := strings.TrimSpace(step.Request.URL)
		if reqURL == "" {
			return fmt.Errorf("steps[%d].request.url is required", i)
		}

		parsedURL, err := url.Parse(reqURL)
		if err != nil {
			return fmt.Errorf("steps[%d].request.url is invalid: %w", i, err)
		}
		if parsedURL.IsAbs() {
			if !containsTemplate(reqURL) {
				if err := validateURL(reqURL); err != nil {
					return fmt.Errorf("steps[%d].request.url is invalid: %w", i, err)
				}
			}
		} else if strings.TrimSpace(config.BaseURL) == "" {
			return fmt.Errorf("steps[%d].request.url must be absolute when base_url is not provided", i)
		}

		if step.Request.TimeoutSeconds != nil {
			if *step.Request.TimeoutSeconds <= 0 {
				return fmt.Errorf("steps[%d].request.timeout_seconds must be greater than 0", i)
			}
			if *step.Request.TimeoutSeconds > 300 {
				return fmt.Errorf("steps[%d].request.timeout_seconds must be 300 or less", i)
			}
		}

		if step.Request.MaxRedirects != nil {
			if *step.Request.MaxRedirects < 0 {
				return fmt.Errorf("steps[%d].request.max_redirects must be 0 or greater", i)
			}
			if *step.Request.MaxRedirects > 50 {
				return fmt.Errorf("steps[%d].request.max_redirects must be 50 or less", i)
			}
		}

		for j, a := range step.Assert {
			if err := validateSyntheticAPIAssertion(i, j, a); err != nil {
				return err
			}
		}

		for j, e := range step.Extract {
			name := strings.TrimSpace(e.Name)
			if name == "" {
				return fmt.Errorf("steps[%d].extract[%d].name is required", i, j)
			}
			if extractNames[name] {
				return fmt.Errorf("steps[%d].extract[%d].name must be globally unique", i, j)
			}
			extractNames[name] = true

			from := strings.ToLower(strings.TrimSpace(e.From))
			if from != "json" && from != "header" {
				return fmt.Errorf("steps[%d].extract[%d].from must be 'json' or 'header'", i, j)
			}

			if strings.TrimSpace(e.Path) == "" {
				return fmt.Errorf("steps[%d].extract[%d].path is required", i, j)
			}
		}
	}

	return nil
}

// SyntheticBrowserConfigValidator validates synthetic browser monitor configuration.
type SyntheticBrowserConfigValidator struct{}

// ValidateConfig validates synthetic browser monitor config.
func (v *SyntheticBrowserConfigValidator) ValidateConfig(configRaw json.RawMessage) error {
	var config sharedmodels.SyntheticBrowserMonitorConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return fmt.Errorf("invalid synthetic_browser config: %w", err)
	}

	if strings.TrimSpace(config.StartURL) == "" {
		return fmt.Errorf("start_url is required")
	}
	if !containsTemplate(config.StartURL) {
		if err := validateURL(config.StartURL); err != nil {
			return fmt.Errorf("invalid start_url: %w", err)
		}
	}

	if err := validateSyntheticFailureMode(config.FailureMode); err != nil {
		return err
	}

	if len(config.Steps) == 0 {
		return fmt.Errorf("steps is required and cannot be empty")
	}
	if len(config.Steps) > 12 {
		return fmt.Errorf("steps cannot exceed 12")
	}

	for key := range config.Variables {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("variables cannot contain empty keys")
		}
	}

	stepIDs := make(map[string]bool, len(config.Steps))
	for i, step := range config.Steps {
		stepID := strings.TrimSpace(step.ID)
		if stepID == "" {
			return fmt.Errorf("steps[%d].id is required", i)
		}
		if stepIDs[stepID] {
			return fmt.Errorf("steps[%d].id must be unique", i)
		}
		stepIDs[stepID] = true

		if step.TimeoutSeconds != nil {
			if *step.TimeoutSeconds <= 0 {
				return fmt.Errorf("steps[%d].timeout_seconds must be greater than 0", i)
			}
			if *step.TimeoutSeconds > 180 {
				return fmt.Errorf("steps[%d].timeout_seconds must be 180 or less", i)
			}
		}

		action := strings.ToLower(strings.TrimSpace(step.Action))
		if action == "" {
			return fmt.Errorf("steps[%d].action is required", i)
		}

		switch action {
		case "goto":
			if strings.TrimSpace(step.URL) == "" {
				return fmt.Errorf("steps[%d].url is required for action goto", i)
			}
			if !containsTemplate(step.URL) {
				if err := validateURL(step.URL); err != nil {
					return fmt.Errorf("steps[%d].url is invalid: %w", i, err)
				}
			}
		case "click", "wait_for", "assert_visible":
			if strings.TrimSpace(step.Selector) == "" {
				return fmt.Errorf("steps[%d].selector is required for action %s", i, action)
			}
		case "fill", "assert_text":
			if strings.TrimSpace(step.Selector) == "" {
				return fmt.Errorf("steps[%d].selector is required for action %s", i, action)
			}
			if step.Value == nil || strings.TrimSpace(*step.Value) == "" {
				return fmt.Errorf("steps[%d].value is required for action %s", i, action)
			}
		case "assert_url":
			if step.Value == nil || strings.TrimSpace(*step.Value) == "" {
				return fmt.Errorf("steps[%d].value is required for action assert_url", i)
			}
		default:
			return fmt.Errorf("steps[%d].action must be one of: goto, click, fill, wait_for, assert_visible, assert_text, assert_url", i)
		}
	}

	return nil
}

func validateSyntheticFailureMode(mode string) error {
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "" || mode == "fail_fast" || mode == "continue" {
		return nil
	}
	return fmt.Errorf("failure_mode must be one of: fail_fast, continue")
}

func validateSyntheticAPIAssertion(stepIdx, assertionIdx int, a sharedmodels.SyntheticAPIAssertionConfig) error {
	target := strings.ToLower(strings.TrimSpace(a.Target))
	op := strings.ToLower(strings.TrimSpace(a.Op))
	if target == "" {
		return fmt.Errorf("steps[%d].assert[%d].target is required", stepIdx, assertionIdx)
	}
	if op == "" {
		return fmt.Errorf("steps[%d].assert[%d].op is required", stepIdx, assertionIdx)
	}

	value, err := decodeAssertionValue(a.Value)
	if err != nil {
		return fmt.Errorf("steps[%d].assert[%d].value is invalid JSON: %w", stepIdx, assertionIdx, err)
	}

	switch target {
	case "status":
		switch op {
		case "equals", "not_equals":
			if value == nil {
				return fmt.Errorf("steps[%d].assert[%d].value is required for %s %s assertion", stepIdx, assertionIdx, target, op)
			}
			if _, err := coerceToInt(value); err != nil {
				return fmt.Errorf("steps[%d].assert[%d].value must be an integer status code", stepIdx, assertionIdx)
			}
		case "in":
			values, ok := value.([]interface{})
			if !ok || len(values) == 0 {
				return fmt.Errorf("steps[%d].assert[%d].value must be a non-empty array for status in assertion", stepIdx, assertionIdx)
			}
			for _, item := range values {
				if _, err := coerceToInt(item); err != nil {
					return fmt.Errorf("steps[%d].assert[%d].value must contain only integer status codes", stepIdx, assertionIdx)
				}
			}
		default:
			return fmt.Errorf("steps[%d].assert[%d].op must be one of: equals, not_equals, in for status assertions", stepIdx, assertionIdx)
		}
	case "header":
		if strings.TrimSpace(a.Path) == "" {
			return fmt.Errorf("steps[%d].assert[%d].path is required for header assertions", stepIdx, assertionIdx)
		}
		if err := validateSyntheticTextAssertion(stepIdx, assertionIdx, op, value); err != nil {
			return err
		}
	case "body":
		if err := validateSyntheticTextAssertion(stepIdx, assertionIdx, op, value); err != nil {
			return err
		}
	case "json":
		if strings.TrimSpace(a.Path) == "" {
			return fmt.Errorf("steps[%d].assert[%d].path is required for json assertions", stepIdx, assertionIdx)
		}
		if err := validateSyntheticJSONAssertion(stepIdx, assertionIdx, op, value); err != nil {
			return err
		}
	default:
		return fmt.Errorf("steps[%d].assert[%d].target must be one of: status, header, body, json", stepIdx, assertionIdx)
	}

	return nil
}

func validateSyntheticTextAssertion(stepIdx, assertionIdx int, op string, value interface{}) error {
	switch op {
	case "exists":
		return nil
	case "equals", "not_equals", "contains", "not_contains":
		if value == nil {
			return fmt.Errorf("steps[%d].assert[%d].value is required for op %s", stepIdx, assertionIdx, op)
		}
	case "regex", "not_regex":
		if value == nil {
			return fmt.Errorf("steps[%d].assert[%d].value is required for op %s", stepIdx, assertionIdx, op)
		}
		pattern := fmt.Sprint(value)
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("steps[%d].assert[%d].value is invalid regex: %w", stepIdx, assertionIdx, err)
		}
	default:
		return fmt.Errorf("steps[%d].assert[%d].op must be one of: exists, equals, not_equals, contains, not_contains, regex, not_regex", stepIdx, assertionIdx)
	}
	return nil
}

func validateSyntheticJSONAssertion(stepIdx, assertionIdx int, op string, value interface{}) error {
	switch op {
	case "exists":
		return nil
	case "equals", "not_equals", "contains", "not_contains":
		if value == nil {
			return fmt.Errorf("steps[%d].assert[%d].value is required for op %s", stepIdx, assertionIdx, op)
		}
	case "regex", "not_regex":
		if value == nil {
			return fmt.Errorf("steps[%d].assert[%d].value is required for op %s", stepIdx, assertionIdx, op)
		}
		pattern := fmt.Sprint(value)
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("steps[%d].assert[%d].value is invalid regex: %w", stepIdx, assertionIdx, err)
		}
	case "number_gt", "number_gte", "number_lt", "number_lte":
		if value == nil {
			return fmt.Errorf("steps[%d].assert[%d].value is required for op %s", stepIdx, assertionIdx, op)
		}
		if _, err := coerceToFloat64(value); err != nil {
			return fmt.Errorf("steps[%d].assert[%d].value must be numeric for op %s", stepIdx, assertionIdx, op)
		}
	case "bool_is":
		if value == nil {
			return fmt.Errorf("steps[%d].assert[%d].value is required for op %s", stepIdx, assertionIdx, op)
		}
		if _, err := coerceToBool(value); err != nil {
			return fmt.Errorf("steps[%d].assert[%d].value must be boolean for op %s", stepIdx, assertionIdx, op)
		}
	default:
		return fmt.Errorf("steps[%d].assert[%d].op must be one of: exists, equals, not_equals, contains, not_contains, regex, not_regex, number_gt, number_gte, number_lt, number_lte, bool_is", stepIdx, assertionIdx)
	}
	return nil
}

func decodeAssertionValue(raw json.RawMessage) (interface{}, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	var value interface{}
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func containsTemplate(s string) bool {
	return strings.Contains(s, "{{") && strings.Contains(s, "}}")
}

func coerceToInt(v interface{}) (int, error) {
	switch n := v.(type) {
	case float64:
		if n != math.Trunc(n) {
			return 0, fmt.Errorf("not an int")
		}
		return int(n), nil
	case int:
		return n, nil
	case string:
		return strconv.Atoi(strings.TrimSpace(n))
	default:
		return 0, fmt.Errorf("not an int")
	}
}

func coerceToFloat64(v interface{}) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case int:
		return float64(n), nil
	case string:
		return strconv.ParseFloat(strings.TrimSpace(n), 64)
	default:
		return 0, fmt.Errorf("not a number")
	}
}

func coerceToBool(v interface{}) (bool, error) {
	switch b := v.(type) {
	case bool:
		return b, nil
	case string:
		return strconv.ParseBool(strings.TrimSpace(strings.ToLower(b)))
	default:
		return false, fmt.Errorf("not a bool")
	}
}

// Helper functions

func validateURL(urlStr string) error {
	u, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("failed to parse URL: %w", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL scheme must be http or https")
	}

	if u.Host == "" {
		return fmt.Errorf("URL must have a host")
	}

	return nil
}

func isValidHostname(hostname string) bool {
	if len(hostname) == 0 || len(hostname) > 253 {
		return false
	}

	hostnameRegex := regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?\.)*[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?$`)
	return hostnameRegex.MatchString(hostname)
}

// Ensure validators implement ConfigValidator interface
var (
	_ ConfigValidator = (*HTTPConfigValidator)(nil)
	_ ConfigValidator = (*PingConfigValidator)(nil)
	_ ConfigValidator = (*DNSConfigValidator)(nil)
	_ ConfigValidator = (*GroupConfigValidator)(nil)
	_ ConfigValidator = (*AgentConfigValidator)(nil)
	_ ConfigValidator = (*PushConfigValidator)(nil)
	_ ConfigValidator = (*SIPConfigValidator)(nil)
	_ ConfigValidator = (*SyntheticAPIConfigValidator)(nil)
	_ ConfigValidator = (*SyntheticBrowserConfigValidator)(nil)
)
