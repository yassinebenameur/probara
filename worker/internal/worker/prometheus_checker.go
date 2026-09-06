package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/models"
)

const prometheusMaxResponseBytes = 2 * 1024 * 1024

type PrometheusChecker struct {
	blockPrivateIPs bool
	allowedCIDRs    []*net.IPNet
}

func NewPrometheusChecker(blockPrivateIPs bool, allowedCIDRs []*net.IPNet) *PrometheusChecker {
	return &PrometheusChecker{blockPrivateIPs: blockPrivateIPs, allowedCIDRs: allowedCIDRs}
}

type prometheusMetrics struct {
	Values      []float64 `json:"values"`
	SampleCount int       `json:"sample_count"`
	FailedCount int       `json:"failed_count"`
}

func (c *PrometheusChecker) Check(ctx context.Context, raw json.RawMessage, timeoutSeconds int) CheckResult {
	start := time.Now()
	result := func(status, message string, metrics *prometheusMetrics) CheckResult {
		latency := time.Since(start).Milliseconds()
		r := CheckResult{Status: status, LatencyMs: &latency}
		if message != "" {
			r.ErrorMessage = &message
		}
		if metrics != nil {
			r.MetricsData, _ = json.Marshal(map[string]any{"prometheus": metrics})
		}
		return r
	}
	var cfg models.PrometheusMonitorConfig
	if json.Unmarshal(raw, &cfg) != nil {
		return result("error", "invalid prometheus config", nil)
	}
	if err := cfg.Validate(); err != nil {
		return result("error", err.Error(), nil)
	}
	if timeoutSeconds <= 0 {
		return result("error", "timeout must be positive", nil)
	}
	// A masked placeholder means the secret was never resolved for this job;
	// sending it would authenticate with the literal string.
	if cfg.AuthType == "bearer" && (cfg.BearerToken == "" || cfg.BearerToken == "***") {
		return result("error", "bearer token is required", nil)
	}
	if cfg.AuthType == "basic" && cfg.Password == "***" {
		return result("error", "basic authentication password was not resolved", nil)
	}
	timeout := time.Duration(timeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	endpoint := strings.TrimRight(cfg.URL, "/") + "/api/v1/query"
	body := url.Values{"query": {cfg.Query}, "timeout": {strconv.Itoa(timeoutSeconds) + "s"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body.Encode()))
	if err != nil {
		return result("error", "invalid query endpoint", nil)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	switch cfg.AuthType {
	case "basic":
		req.SetBasicAuth(cfg.Username, cfg.Password)
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+cfg.BearerToken)
	}
	// Direct guarded dialing prevents proxy bypass and DNS rebinding. Never
	// follow redirects, which could forward the query or credentials elsewhere.
	transport := &http.Transport{DialContext: newDialGuard(c.blockPrivateIPs, c.allowedCIDRs, timeout).DialContext, TLSHandshakeTimeout: timeout}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return result("error", "Prometheus query timed out or was canceled", nil)
		}
		return result("error", "Prometheus connection failed (check endpoint, TLS and worker network policy)", nil)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return result("error", fmt.Sprintf("Prometheus returned HTTP %d", resp.StatusCode), nil)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, prometheusMaxResponseBytes+1))
	if err != nil {
		return result("error", "failed to read Prometheus response", nil)
	}
	if len(data) > prometheusMaxResponseBytes {
		return result("error", "Prometheus response exceeds 2 MiB; aggregate the query", nil)
	}
	var envelope struct {
		Status   string   `json:"status"`
		Warnings []string `json:"warnings"`
		Data     struct {
			ResultType string          `json:"resultType"`
			Result     json.RawMessage `json:"result"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &envelope) != nil || envelope.Status != "success" {
		return result("error", "Prometheus query failed or returned an invalid response", nil)
	}
	// Warnings can indicate partial evaluation; do not turn incomplete data green.
	if len(envelope.Warnings) > 0 {
		return result("error", "Prometheus returned query warnings; inspect the query in Prometheus", nil)
	}
	samples, err := prometheusSamples(envelope.Data.ResultType, envelope.Data.Result)
	if err != nil {
		return result("error", err.Error(), nil)
	}
	metrics := &prometheusMetrics{Values: []float64{}, SampleCount: len(samples)}
	if len(samples) == 0 {
		status := cfg.NoDataStatus
		if status == "" {
			status = "failure"
		}
		message := "query returned no samples"
		if status == "success" {
			message = ""
		}
		return result(status, message, metrics)
	}
	for _, v := range samples {
		if len(metrics.Values) < 20 {
			metrics.Values = append(metrics.Values, v)
		}
		if !cfg.Matches(v) {
			metrics.FailedCount++
		}
	}
	if metrics.FailedCount > 0 {
		return result("failure", fmt.Sprintf("%d of %d samples do not satisfy %s %g", metrics.FailedCount, len(samples), cfg.Operator, *cfg.Threshold), metrics)
	}
	return result("success", "", metrics)
}

func prometheusSamples(kind string, raw json.RawMessage) ([]float64, error) {
	parse := func(raw json.RawMessage) (float64, error) {
		var pair []json.RawMessage
		var timestamp float64
		var value string
		if json.Unmarshal(raw, &pair) != nil || len(pair) != 2 || string(pair[0]) == "null" || json.Unmarshal(pair[0], &timestamp) != nil || json.Unmarshal(pair[1], &value) != nil {
			return 0, fmt.Errorf("Prometheus returned an invalid sample")
		}
		v, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, fmt.Errorf("Prometheus returned a non-finite or invalid sample")
		}
		return v, nil
	}
	switch kind {
	case "scalar":
		v, err := parse(raw)
		if err != nil {
			return nil, err
		}
		return []float64{v}, nil
	case "vector":
		var rows []struct {
			Value json.RawMessage `json:"value"`
		}
		if string(raw) == "null" || json.Unmarshal(raw, &rows) != nil {
			return nil, fmt.Errorf("Prometheus returned an invalid vector")
		}
		values := make([]float64, 0, len(rows))
		for _, row := range rows {
			v, err := parse(row.Value)
			if err != nil {
				return nil, err
			}
			values = append(values, v)
		}
		return values, nil
	default:
		return nil, fmt.Errorf("query must return a float scalar or instant vector; matrices, strings and histograms are unsupported")
	}
}
