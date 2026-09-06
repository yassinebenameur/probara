package models

import (
	"fmt"
	"math"
	"net/url"
	"strings"
)

// PrometheusMonitorConfig evaluates an instant query. Every float sample must
// satisfy the comparison; use PromQL aggregation to reduce series explicitly.
type PrometheusMonitorConfig struct {
	URL          string   `json:"url"`
	Query        string   `json:"query"`
	Operator     string   `json:"operator"`
	Threshold    *float64 `json:"threshold"`
	NoDataStatus string   `json:"no_data_status,omitempty"`
	AuthType     string   `json:"auth_type,omitempty"`
	Username     string   `json:"username,omitempty"`
	Password     string   `json:"password,omitempty"`
	BearerToken  string   `json:"bearer_token,omitempty"`
}

func (c PrometheusMonitorConfig) Validate() error {
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.TrimSpace(c.URL) != c.URL {
		return fmt.Errorf("url must be an HTTP(S) base URL without credentials, query parameters or fragment")
	}
	if len(c.URL) > 2048 {
		return fmt.Errorf("url must not exceed 2048 bytes")
	}
	if strings.TrimSpace(c.Query) == "" || len(c.Query) > 16384 {
		return fmt.Errorf("query must contain 1 to 16384 bytes")
	}
	switch c.Operator {
	case "gt", "gte", "lt", "lte", "eq", "ne":
	default:
		return fmt.Errorf("operator must be gt, gte, lt, lte, eq or ne")
	}
	if c.Threshold == nil || math.IsNaN(*c.Threshold) || math.IsInf(*c.Threshold, 0) {
		return fmt.Errorf("threshold must be a finite number")
	}
	switch c.NoDataStatus {
	case "", "success", "failure", "error":
	default:
		return fmt.Errorf("no_data_status must be success, failure or error")
	}
	switch c.AuthType {
	case "", "none":
	case "basic":
		if strings.TrimSpace(c.Username) == "" || strings.Contains(c.Username, ":") {
			return fmt.Errorf("basic authentication requires a username without a colon")
		}
	case "bearer": // Missing secrets are permitted for write-only update merging.
	default:
		return fmt.Errorf("auth_type must be none, basic or bearer")
	}
	if strings.ContainsAny(c.BearerToken, "\r\n") {
		return fmt.Errorf("bearer_token must not contain newlines")
	}
	return nil
}

func (c PrometheusMonitorConfig) Matches(value float64) bool {
	switch c.Operator {
	case "gt":
		return value > *c.Threshold
	case "gte":
		return value >= *c.Threshold
	case "lt":
		return value < *c.Threshold
	case "lte":
		return value <= *c.Threshold
	case "eq":
		return value == *c.Threshold
	case "ne":
		return value != *c.Threshold
	}
	return false
}
