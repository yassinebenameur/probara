package worker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	probing "github.com/prometheus-community/pro-bing"
	"github.com/tidwall/gjson"
	"github.com/yassinebenameur/probara/shared/models"
)

// Checker is an interface for different monitor types
type Checker interface {
	Check(ctx context.Context, config json.RawMessage, timeoutSeconds int) CheckResult
}

// CheckResult represents the final evaluated result
type CheckResult struct {
	Status               string
	HTTPStatus           *int
	LatencyMs            *int64
	ErrorMessage         *string
	MatchedBodySubstring bool
	MetricsData          json.RawMessage
}

// HTTPChecker implements Checker for HTTP monitors
type HTTPChecker struct {
	maxBodySizeBytes int
	blockPrivateIPs  bool
	allowedCIDRs     []*net.IPNet
}

// NewHTTPChecker creates a new HTTP checker
func NewHTTPChecker(maxBodySizeBytes int, blockPrivateIPs bool, allowedCIDRs []*net.IPNet) *HTTPChecker {
	return &HTTPChecker{
		maxBodySizeBytes: maxBodySizeBytes,
		blockPrivateIPs:  blockPrivateIPs,
		allowedCIDRs:     allowedCIDRs,
	}
}

type httpTimingInfo struct {
	DNSMs          *int64 `json:"dns_ms,omitempty"`
	ConnectMs      *int64 `json:"connect_ms,omitempty"`
	TLSHandshakeMs *int64 `json:"tls_handshake_ms,omitempty"`
	TTFBMs         *int64 `json:"ttfb_ms,omitempty"`
	TotalMs        *int64 `json:"total_ms,omitempty"`
}

type httpTLSInfo struct {
	Version          string   `json:"version,omitempty"`
	CipherSuite      string   `json:"cipher_suite,omitempty"`
	ServerName       string   `json:"server_name,omitempty"`
	NotBefore        string   `json:"not_before,omitempty"`
	NotAfter         string   `json:"not_after,omitempty"`
	DaysUntilExpiry  *int     `json:"days_until_expiry,omitempty"`
	Subject          string   `json:"subject,omitempty"`
	Issuer           string   `json:"issuer,omitempty"`
	SerialNumber     string   `json:"serial_number,omitempty"`
	DNSNames         []string `json:"dns_names,omitempty"`
	IPAddresses      []string `json:"ip_addresses,omitempty"`
	VerifiedChains   int      `json:"verified_chains,omitempty"`
	PeerCertificates int      `json:"peer_certificates,omitempty"`
}

type httpMetrics struct {
	FinalURL   string          `json:"final_url,omitempty"`
	Redirects  int             `json:"redirects,omitempty"`
	Timing     *httpTimingInfo `json:"timing,omitempty"`
	TLS        *httpTLSInfo    `json:"tls,omitempty"`
	Assertions []string        `json:"assertions_failed,omitempty"`
}

type httpMetricsEnvelope struct {
	HTTP *httpMetrics `json:"http,omitempty"`
}

// Check performs an HTTP check
func (c *HTTPChecker) Check(ctx context.Context, configRaw json.RawMessage, timeoutSeconds int) CheckResult {
	var config models.HTTPMonitorConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		errMsg := fmt.Sprintf("failed to unmarshal HTTP config: %v", err)
		return CheckResult{
			Status:       "error",
			ErrorMessage: &errMsg,
		}
	}

	parsedURL, err := url.Parse(config.URL)
	if err != nil {
		errMsg := fmt.Sprintf("invalid_url: %v", err)
		return CheckResult{
			Status:       "error",
			ErrorMessage: &errMsg,
		}
	}

	startTime := time.Now()

	// Create HTTP client with timeout
	timeout := time.Duration(timeoutSeconds) * time.Second
	followRedirects := true
	if config.FollowRedirects != nil {
		followRedirects = *config.FollowRedirects
	}
	maxRedirects := 10
	if config.MaxRedirects != nil {
		maxRedirects = *config.MaxRedirects
	}

	collectTiming := false
	if config.CollectTiming != nil {
		collectTiming = *config.CollectTiming
	}

	var redirectsFollowed int
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !followRedirects {
				return http.ErrUseLastResponse
			}
			if maxRedirects >= 0 && len(via) > maxRedirects {
				return fmt.Errorf("redirects: stopped after %d redirects", maxRedirects)
			}
			redirectsFollowed = len(via)
			return nil
		},
	}

	tlsCfg := &tls.Config{}
	tlsCfg.InsecureSkipVerify = config.TLSSkipVerify != nil && *config.TLSSkipVerify
	if config.TLSServerName != nil && strings.TrimSpace(*config.TLSServerName) != "" {
		tlsCfg.ServerName = strings.TrimSpace(*config.TLSServerName)
	} else if parsedURL.Hostname() != "" {
		tlsCfg.ServerName = parsedURL.Hostname()
	}

	if config.TLSCAPem != nil && strings.TrimSpace(*config.TLSCAPem) != "" {
		systemPool, err := x509.SystemCertPool()
		if err != nil || systemPool == nil {
			systemPool = x509.NewCertPool()
		}
		if !systemPool.AppendCertsFromPEM([]byte(*config.TLSCAPem)) {
			errMsg := "tls_ca_pem: failed to parse PEM"
			return CheckResult{
				Status:       "error",
				ErrorMessage: &errMsg,
			}
		}
		tlsCfg.RootCAs = systemPool
	}

	transport := &http.Transport{
		TLSClientConfig:     tlsCfg,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        10,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: timeout,
	}

	// SSRF protection: block private/link-local/etc IPs if enabled (can be overridden by HTTP_ALLOWED_CIDRS).
	if c.blockPrivateIPs {
		dialer := &net.Dialer{
			Timeout:   timeout,
			KeepAlive: 30 * time.Second,
		}
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}

			// If host is already an IP literal, validate directly.
			if ip := net.ParseIP(host); ip != nil {
				if !c.isIPAllowed(ip) {
					return nil, fmt.Errorf("ssrf_blocked: ip %s not allowed", host)
				}
				return dialer.DialContext(ctx, network, address)
			}

			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}

			var lastErr error
			for _, ipAddr := range ips {
				ipStr := ipAddr.IP.String()
				if !c.isIPAllowed(ipAddr.IP) {
					lastErr = fmt.Errorf("ssrf_blocked: resolved %s -> %s", host, ipStr)
					continue
				}

				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ipStr, port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}

			if lastErr == nil {
				lastErr = errors.New("no resolved IPs")
			}
			return nil, lastErr
		}
	}

	client.Transport = transport

	// Create request
	var bodyReader io.Reader
	if config.Body != nil && *config.Body != "" {
		bodyReader = strings.NewReader(*config.Body)
	}

	req, err := http.NewRequestWithContext(ctx, config.Method, config.URL, bodyReader)
	if err != nil {
		errMsg := fmt.Sprintf("request_creation: %v", err)
		return CheckResult{
			Status:       "error",
			ErrorMessage: &errMsg,
		}
	}

	// Set headers
	for k, v := range config.Headers {
		req.Header.Set(k, v)
	}

	var traceTimings httpTimingInfo
	var dnsStart, connectStart, tlsStart time.Time
	var firstByteAt time.Time
	if collectTiming {
		trace := &httptrace.ClientTrace{
			DNSStart: func(info httptrace.DNSStartInfo) { dnsStart = time.Now() },
			DNSDone: func(info httptrace.DNSDoneInfo) {
				if !dnsStart.IsZero() {
					ms := time.Since(dnsStart).Milliseconds()
					traceTimings.DNSMs = &ms
				}
			},
			ConnectStart: func(network, addr string) { connectStart = time.Now() },
			ConnectDone: func(network, addr string, err error) {
				if !connectStart.IsZero() {
					ms := time.Since(connectStart).Milliseconds()
					traceTimings.ConnectMs = &ms
				}
			},
			TLSHandshakeStart: func() { tlsStart = time.Now() },
			TLSHandshakeDone: func(cs tls.ConnectionState, err error) {
				if !tlsStart.IsZero() {
					ms := time.Since(tlsStart).Milliseconds()
					traceTimings.TLSHandshakeMs = &ms
				}
			},
			GotFirstResponseByte: func() {
				firstByteAt = time.Now()
				ms := firstByteAt.Sub(startTime).Milliseconds()
				traceTimings.TTFBMs = &ms
			},
		}
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	}

	// Execute request
	resp, err := client.Do(req)
	latencyMs := time.Since(startTime).Milliseconds()
	traceTimings.TotalMs = &latencyMs

	if err != nil {
		errorReason := "unknown"
		errStr := err.Error()
		if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline") {
			errorReason = "timeout"
		} else if strings.Contains(errStr, "no such host") || strings.Contains(errStr, "DNS") {
			errorReason = "dns"
		} else if strings.Contains(errStr, "connection refused") || strings.Contains(errStr, "connect") {
			errorReason = "connect"
		}

		errMsg := fmt.Sprintf("%s: %v", errorReason, err)
		return CheckResult{
			Status:       "error",
			ErrorMessage: &errMsg,
			LatencyMs:    &latencyMs,
		}
	}
	defer resp.Body.Close()

	// Read response body with size limit
	limitedReader := io.LimitReader(resp.Body, int64(c.maxBodySizeBytes))
	bodyBytes, err := io.ReadAll(limitedReader)
	if err != nil {
		errMsg := fmt.Sprintf("body_read: %v", err)
		httpStatus := resp.StatusCode
		return CheckResult{
			Status:       "error",
			HTTPStatus:   &httpStatus,
			ErrorMessage: &errMsg,
			LatencyMs:    &latencyMs,
		}
	}

	body := string(bodyBytes)

	httpStatus := resp.StatusCode

	metrics := &httpMetrics{
		FinalURL:  resp.Request.URL.String(),
		Redirects: redirectsFollowed,
	}

	if collectTiming {
		metrics.Timing = &traceTimings
	}

	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		leaf := resp.TLS.PeerCertificates[0]
		days := int(time.Until(leaf.NotAfter).Hours() / 24)
		serverName := tlsCfg.ServerName
		metrics.TLS = &httpTLSInfo{
			Version:          tls.VersionName(resp.TLS.Version),
			CipherSuite:      tls.CipherSuiteName(resp.TLS.CipherSuite),
			ServerName:       serverName,
			NotBefore:        leaf.NotBefore.UTC().Format(time.RFC3339),
			NotAfter:         leaf.NotAfter.UTC().Format(time.RFC3339),
			DaysUntilExpiry:  &days,
			Subject:          leaf.Subject.String(),
			Issuer:           leaf.Issuer.String(),
			SerialNumber:     leaf.SerialNumber.String(),
			DNSNames:         leaf.DNSNames,
			VerifiedChains:   len(resp.TLS.VerifiedChains),
			PeerCertificates: len(resp.TLS.PeerCertificates),
		}
		for _, ip := range leaf.IPAddresses {
			metrics.TLS.IPAddresses = append(metrics.TLS.IPAddresses, ip.String())
		}
	}

	var assertionsFailed []string

	// Check HTTP status code(s)
	statusOK := c.evaluateStatusCode(resp.StatusCode, &config)
	if !statusOK {
		assertionsFailed = append(assertionsFailed, fmt.Sprintf("status_code: got %d", resp.StatusCode))
	}

	// Check response headers
	if len(config.ResponseHeaderAssertions) > 0 {
		failed := evaluateHeaderAssertions(resp.Header, config.ResponseHeaderAssertions)
		assertionsFailed = append(assertionsFailed, failed...)
	}

	// Body assertions (backwards compatible fields are treated as additional assertions)
	matchedBodySubstring := true
	bodyAssertions := append([]models.HTTPBodyAssertion{}, config.BodyAssertions...)
	if config.ExpectedBodySubstring != nil && strings.TrimSpace(*config.ExpectedBodySubstring) != "" {
		bodyAssertions = append(bodyAssertions, models.HTTPBodyAssertion{
			Op:    "contains",
			Value: *config.ExpectedBodySubstring,
		})
		matchedBodySubstring = strings.Contains(body, *config.ExpectedBodySubstring)
	}
	if config.ExpectedBodyRegex != nil && strings.TrimSpace(*config.ExpectedBodyRegex) != "" {
		bodyAssertions = append(bodyAssertions, models.HTTPBodyAssertion{
			Op:    "regex",
			Value: *config.ExpectedBodyRegex,
		})
	}

	if len(bodyAssertions) > 0 {
		failed, err := evaluateBodyAssertions(body, bodyAssertions)
		if err != nil {
			errMsg := fmt.Sprintf("body_assertions: %v", err)
			return CheckResult{
				Status:               "error",
				HTTPStatus:           &httpStatus,
				LatencyMs:            &latencyMs,
				ErrorMessage:         &errMsg,
				MatchedBodySubstring: matchedBodySubstring,
			}
		}
		assertionsFailed = append(assertionsFailed, failed...)
	}

	// JSON assertions (gjson paths)
	if len(config.JSONAssertions) > 0 {
		if !gjson.Valid(body) {
			assertionsFailed = append(assertionsFailed, "json: invalid JSON response")
		} else {
			failed, err := evaluateJSONAssertions(body, config.JSONAssertions)
			if err != nil {
				errMsg := fmt.Sprintf("json_assertions: %v", err)
				return CheckResult{
					Status:               "error",
					HTTPStatus:           &httpStatus,
					LatencyMs:            &latencyMs,
					ErrorMessage:         &errMsg,
					MatchedBodySubstring: matchedBodySubstring,
				}
			}
			assertionsFailed = append(assertionsFailed, failed...)
		}
	}

	// Latency threshold
	if config.MaxLatencyMs != nil && *config.MaxLatencyMs > 0 && latencyMs > *config.MaxLatencyMs {
		assertionsFailed = append(assertionsFailed, fmt.Sprintf("latency: %dms > %dms", latencyMs, *config.MaxLatencyMs))
	}

	// TLS certificate expiry check (https only)
	if config.TLSMinDaysValid != nil {
		if parsedURL.Scheme != "https" {
			assertionsFailed = append(assertionsFailed, "tls: tls_min_days_valid requires https")
		} else if metrics.TLS == nil || metrics.TLS.DaysUntilExpiry == nil {
			assertionsFailed = append(assertionsFailed, "tls: missing certificate info")
		} else if *metrics.TLS.DaysUntilExpiry < *config.TLSMinDaysValid {
			assertionsFailed = append(assertionsFailed, fmt.Sprintf("tls: expires in %dd (< %dd)", *metrics.TLS.DaysUntilExpiry, *config.TLSMinDaysValid))
		}
	}

	metrics.Assertions = assertionsFailed

	if len(assertionsFailed) > 0 {
		metricsJSON, _ := json.Marshal(httpMetricsEnvelope{HTTP: metrics})
		errMsg := strings.Join(assertionsFailed, "; ")
		return CheckResult{
			Status:               "failure",
			HTTPStatus:           &httpStatus,
			LatencyMs:            &latencyMs,
			ErrorMessage:         &errMsg,
			MatchedBodySubstring: matchedBodySubstring,
			MetricsData:          metricsJSON,
		}
	}

	metricsJSON, _ := json.Marshal(httpMetricsEnvelope{HTTP: metrics})
	return CheckResult{
		Status:               "success",
		HTTPStatus:           &httpStatus,
		LatencyMs:            &latencyMs,
		MatchedBodySubstring: matchedBodySubstring,
		MetricsData:          metricsJSON,
	}
}

func (c *HTTPChecker) evaluateStatusCode(statusCode int, config *models.HTTPMonitorConfig) bool {
	// Collect matchers from config (OR semantics).
	matchers := make([]func(int) bool, 0)

	if config.ExpectedStatus != nil {
		expected := *config.ExpectedStatus
		matchers = append(matchers, func(code int) bool { return code == expected })
	}
	for _, expected := range config.ExpectedStatuses {
		expected := expected
		matchers = append(matchers, func(code int) bool { return code == expected })
	}
	for _, r := range config.ExpectedStatusRanges {
		r := r
		matchers = append(matchers, func(code int) bool { return code >= r.Min && code <= r.Max })
	}
	for _, class := range config.ExpectedStatusClasses {
		class := strings.TrimSpace(strings.ToLower(class))
		if len(class) == 3 && strings.HasSuffix(class, "xx") {
			digit := class[0]
			if digit >= '1' && digit <= '5' {
				min := int(digit-'0') * 100
				max := min + 99
				matchers = append(matchers, func(code int) bool { return code >= min && code <= max })
			}
		}
	}

	// Default: 2xx
	if len(matchers) == 0 {
		return statusCode >= 200 && statusCode < 300
	}

	for _, m := range matchers {
		if m(statusCode) {
			return true
		}
	}
	return false
}

func (c *HTTPChecker) isIPAllowed(ip net.IP) bool {
	if ip == nil {
		return false
	}

	// Always allow explicitly configured CIDRs (operator override).
	for _, cidr := range c.allowedCIDRs {
		if cidr != nil && cidr.Contains(ip) {
			return true
		}
	}

	// Block common private / loopback / link-local / CGNAT ranges.
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	if isPrivateOrReservedIP(ip) {
		return false
	}
	return true
}

func isPrivateOrReservedIP(ip net.IP) bool {
	privateCIDRs := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16",  // link-local + cloud metadata
		"100.64.0.0/10",   // CGNAT
		"192.0.0.0/24",    // IETF protocol assignments
		"192.0.2.0/24",    // TEST-NET-1
		"198.51.100.0/24", // TEST-NET-2
		"203.0.113.0/24",  // TEST-NET-3
		"224.0.0.0/4",     // multicast
		"240.0.0.0/4",     // reserved
		"::1/128",
		"fc00::/7",  // unique local
		"fe80::/10", // link-local
	}

	for _, cidr := range privateCIDRs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func evaluateHeaderAssertions(headers http.Header, assertions []models.HTTPHeaderAssertion) []string {
	var failed []string
	for _, a := range assertions {
		name := strings.TrimSpace(a.Name)
		if name == "" {
			failed = append(failed, "header: empty name")
			continue
		}

		values := headers.Values(name)
		op := strings.TrimSpace(strings.ToLower(a.Op))
		value := ""
		if a.Value != nil {
			value = *a.Value
		}
		ci := a.CaseInsensitive != nil && *a.CaseInsensitive

		matchAny := func(pred func(string) bool) bool {
			for _, v := range values {
				if pred(v) {
					return true
				}
			}
			return false
		}
		matchNone := func(pred func(string) bool) bool {
			for _, v := range values {
				if pred(v) {
					return false
				}
			}
			return true
		}

		normalize := func(s string) string {
			if ci {
				return strings.ToLower(s)
			}
			return s
		}

		switch op {
		case "exists":
			if len(values) == 0 {
				failed = append(failed, fmt.Sprintf("header %s: missing", name))
			}
		case "equals":
			if len(values) == 0 || !matchAny(func(v string) bool { return normalize(v) == normalize(value) }) {
				failed = append(failed, fmt.Sprintf("header %s: != %q", name, value))
			}
		case "contains":
			if len(values) == 0 || !matchAny(func(v string) bool { return strings.Contains(normalize(v), normalize(value)) }) {
				failed = append(failed, fmt.Sprintf("header %s: does not contain %q", name, value))
			}
		case "regex":
			pattern := value
			if ci {
				pattern = "(?i)" + pattern
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				failed = append(failed, fmt.Sprintf("header %s: invalid regex", name))
				continue
			}
			if len(values) == 0 || !matchAny(func(v string) bool { return re.MatchString(v) }) {
				failed = append(failed, fmt.Sprintf("header %s: regex no match", name))
			}
		case "not_equals":
			if len(values) > 0 && !matchNone(func(v string) bool { return normalize(v) == normalize(value) }) {
				failed = append(failed, fmt.Sprintf("header %s: equals %q", name, value))
			}
		case "not_contains":
			if len(values) > 0 && !matchNone(func(v string) bool { return strings.Contains(normalize(v), normalize(value)) }) {
				failed = append(failed, fmt.Sprintf("header %s: contains %q", name, value))
			}
		case "not_regex":
			pattern := value
			if ci {
				pattern = "(?i)" + pattern
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				failed = append(failed, fmt.Sprintf("header %s: invalid regex", name))
				continue
			}
			if len(values) > 0 && !matchNone(func(v string) bool { return re.MatchString(v) }) {
				failed = append(failed, fmt.Sprintf("header %s: regex matched", name))
			}
		default:
			failed = append(failed, fmt.Sprintf("header %s: unknown op %q", name, a.Op))
		}
	}
	return failed
}

func evaluateBodyAssertions(body string, assertions []models.HTTPBodyAssertion) ([]string, error) {
	var failed []string
	for _, a := range assertions {
		op := strings.TrimSpace(strings.ToLower(a.Op))
		value := a.Value
		ci := a.CaseInsensitive != nil && *a.CaseInsensitive

		bodyToCheck := body
		valueToCheck := value
		if ci && (op == "contains" || op == "not_contains") {
			bodyToCheck = strings.ToLower(bodyToCheck)
			valueToCheck = strings.ToLower(valueToCheck)
		}

		switch op {
		case "contains":
			if !strings.Contains(bodyToCheck, valueToCheck) {
				failed = append(failed, fmt.Sprintf("body: missing %q", value))
			}
		case "not_contains":
			if strings.Contains(bodyToCheck, valueToCheck) {
				failed = append(failed, fmt.Sprintf("body: contains %q", value))
			}
		case "regex":
			pattern := value
			if ci {
				pattern = "(?i)" + pattern
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				return nil, fmt.Errorf("invalid regex %q", value)
			}
			if !re.MatchString(body) {
				failed = append(failed, fmt.Sprintf("body: regex no match %q", value))
			}
		case "not_regex":
			pattern := value
			if ci {
				pattern = "(?i)" + pattern
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				return nil, fmt.Errorf("invalid regex %q", value)
			}
			if re.MatchString(body) {
				failed = append(failed, fmt.Sprintf("body: regex matched %q", value))
			}
		default:
			return nil, fmt.Errorf("unknown op %q", a.Op)
		}
	}
	return failed, nil
}

func evaluateJSONAssertions(body string, assertions []models.HTTPJSONAssertion) ([]string, error) {
	var failed []string
	for _, a := range assertions {
		path := strings.TrimSpace(a.Path)
		if path == "" {
			return nil, errors.New("empty json path")
		}
		op := strings.TrimSpace(strings.ToLower(a.Op))
		value := ""
		if a.Value != nil {
			value = *a.Value
		}
		ci := a.CaseInsensitive != nil && *a.CaseInsensitive

		result := gjson.Get(body, path)

		normalize := func(s string) string {
			if ci {
				return strings.ToLower(s)
			}
			return s
		}

		switch op {
		case "exists":
			if !result.Exists() {
				failed = append(failed, fmt.Sprintf("json %s: missing", path))
			}
		case "equals":
			if !result.Exists() || normalize(result.String()) != normalize(value) {
				failed = append(failed, fmt.Sprintf("json %s: != %q", path, value))
			}
		case "not_equals":
			if result.Exists() && normalize(result.String()) == normalize(value) {
				failed = append(failed, fmt.Sprintf("json %s: equals %q", path, value))
			}
		case "contains":
			if !result.Exists() || !strings.Contains(normalize(result.String()), normalize(value)) {
				failed = append(failed, fmt.Sprintf("json %s: does not contain %q", path, value))
			}
		case "not_contains":
			if result.Exists() && strings.Contains(normalize(result.String()), normalize(value)) {
				failed = append(failed, fmt.Sprintf("json %s: contains %q", path, value))
			}
		case "regex":
			pattern := value
			if ci {
				pattern = "(?i)" + pattern
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				return nil, fmt.Errorf("invalid regex %q", value)
			}
			if !result.Exists() || !re.MatchString(result.String()) {
				failed = append(failed, fmt.Sprintf("json %s: regex no match %q", path, value))
			}
		case "number_gt", "number_gte", "number_lt", "number_lte":
			if !result.Exists() {
				failed = append(failed, fmt.Sprintf("json %s: missing", path))
				continue
			}
			expected, err := parseFloat(value)
			if err != nil {
				return nil, fmt.Errorf("invalid number %q", value)
			}
			actual := result.Float()
			switch op {
			case "number_gt":
				if !(actual > expected) {
					failed = append(failed, fmt.Sprintf("json %s: %v !> %v", path, actual, expected))
				}
			case "number_gte":
				if !(actual >= expected) {
					failed = append(failed, fmt.Sprintf("json %s: %v !>= %v", path, actual, expected))
				}
			case "number_lt":
				if !(actual < expected) {
					failed = append(failed, fmt.Sprintf("json %s: %v !< %v", path, actual, expected))
				}
			case "number_lte":
				if !(actual <= expected) {
					failed = append(failed, fmt.Sprintf("json %s: %v !<= %v", path, actual, expected))
				}
			}
		case "bool_is":
			if !result.Exists() {
				failed = append(failed, fmt.Sprintf("json %s: missing", path))
				continue
			}
			expected, err := parseBool(value)
			if err != nil {
				return nil, fmt.Errorf("invalid bool %q", value)
			}
			if result.Bool() != expected {
				failed = append(failed, fmt.Sprintf("json %s: %v != %v", path, result.Bool(), expected))
			}
		default:
			return nil, fmt.Errorf("unknown op %q", a.Op)
		}
	}
	return failed, nil
}

func parseFloat(s string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSpace(s), 64)
}

func parseBool(s string) (bool, error) {
	return strconv.ParseBool(strings.TrimSpace(strings.ToLower(s)))
}

// PingChecker implements Checker for ping monitors
type PingChecker struct{}

// NewPingChecker creates a new ping checker
func NewPingChecker() *PingChecker {
	return &PingChecker{}
}

// Check performs a ping check
func (c *PingChecker) Check(ctx context.Context, configRaw json.RawMessage, timeoutSeconds int) CheckResult {
	var config models.PingMonitorConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		errMsg := fmt.Sprintf("failed to unmarshal ping config: %v", err)
		return CheckResult{
			Status:       "error",
			ErrorMessage: &errMsg,
		}
	}

	// Create pinger
	pinger, err := probing.NewPinger(config.Host)
	if err != nil {
		errMsg := fmt.Sprintf("failed to create pinger: %v", err)
		return CheckResult{
			Status:       "error",
			ErrorMessage: &errMsg,
		}
	}

	// Set unprivileged mode (uses UDP instead of raw ICMP)
	pinger.SetPrivileged(false)

	// Set timeout
	pinger.Timeout = time.Duration(timeoutSeconds) * time.Second

	// Set count to 1 ping
	pinger.Count = 1

	// Run ping
	err = pinger.Run()
	if err != nil {
		errMsg := fmt.Sprintf("ping failed: %v", err)
		return CheckResult{
			Status:       "error",
			ErrorMessage: &errMsg,
		}
	}

	stats := pinger.Statistics()

	// Check if we received any packets
	if stats.PacketsRecv == 0 {
		errMsg := "no response received"
		return CheckResult{
			Status:       "failure",
			ErrorMessage: &errMsg,
		}
	}

	// Success - convert RTT to milliseconds
	latencyMs := stats.AvgRtt.Milliseconds()

	return CheckResult{
		Status:    "success",
		LatencyMs: &latencyMs,
	}
}

// AgentChecker implements Checker for agent monitors
// It doesn't actively check anything, but verifies the agent is still reporting
type AgentChecker struct {
	db interface {
		QueryRowContext(ctx context.Context, query string, args ...interface{}) interface {
			Scan(dest ...interface{}) error
		}
	}
}

// NewAgentChecker creates a new agent checker
func NewAgentChecker(db interface {
	QueryRowContext(ctx context.Context, query string, args ...interface{}) interface {
		Scan(dest ...interface{}) error
	}
}) *AgentChecker {
	return &AgentChecker{db: db}
}

// Check verifies that the agent has reported recently
func (c *AgentChecker) Check(ctx context.Context, configRaw json.RawMessage, timeoutSeconds int) CheckResult {
	// Note: Agent monitors are primarily passive - they receive metrics pushed from agents
	// This check is a placeholder that always returns success
	// The actual staleness detection happens in the scheduler/alerter based on
	// the last check_result timestamp

	return CheckResult{
		Status: "success",
	}
}
