// Per-type config builders for the simple (field-mapped) import path.

package importservice

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// buildHTTPConfig builds HTTP monitor config from row
func (s *Service) buildHTTPConfig(row models.ImportRow, mapping models.FieldMapping) (json.RawMessage, error) {
	url := s.extractString(row.Fields, mapping.URL)
	if url == "" {
		return nil, fmt.Errorf("URL is required for HTTP monitor")
	}

	method := strings.ToUpper(s.extractString(row.Fields, mapping.Method))
	if method == "" {
		method = "GET"
	}

	config := map[string]interface{}{
		"url":    url,
		"method": method,
	}

	expectedStatus := s.extractInt(row.Fields, mapping.ExpectedStatus)
	if expectedStatus > 0 {
		config["expected_status"] = expectedStatus
	}

	expectedBody := s.extractString(row.Fields, mapping.ExpectedBody)
	if expectedBody != "" {
		config["expected_body_substring"] = expectedBody
	}

	return json.Marshal(config)
}

// buildPingConfig builds ping monitor config from row
func (s *Service) buildPingConfig(row models.ImportRow, mapping models.FieldMapping) (json.RawMessage, error) {
	host := s.extractString(row.Fields, mapping.Host)
	if host == "" {
		// Try URL field as fallback
		host = s.extractString(row.Fields, mapping.URL)
	}
	if host == "" {
		return nil, fmt.Errorf("host is required for ping monitor")
	}

	// Remove protocol prefix if present
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimPrefix(host, "https://")
	// Remove path if present
	if idx := strings.Index(host, "/"); idx > 0 {
		host = host[:idx]
	}
	// Remove port if present
	if idx := strings.Index(host, ":"); idx > 0 {
		host = host[:idx]
	}

	config := map[string]interface{}{
		"host": host,
	}

	return json.Marshal(config)
}

// buildDNSConfig builds DNS monitor config from row
func (s *Service) buildDNSConfig(row models.ImportRow, mapping models.FieldMapping) (json.RawMessage, error) {
	host := s.extractString(row.Fields, mapping.Host)
	if host == "" {
		// Try URL field as fallback
		host = s.extractString(row.Fields, mapping.URL)
	}
	if host == "" {
		return nil, fmt.Errorf("host is required for dns monitor")
	}

	// Remove protocol prefix if present
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimPrefix(host, "https://")
	// Remove path if present
	if idx := strings.Index(host, "/"); idx > 0 {
		host = host[:idx]
	}
	// Remove port if present
	if idx := strings.Index(host, ":"); idx > 0 {
		host = host[:idx]
	}

	config := map[string]interface{}{
		"host": host,
	}

	return json.Marshal(config)
}

// buildGRPCConfig builds gRPC monitor config from row
func (s *Service) buildGRPCConfig(row models.ImportRow, mapping models.FieldMapping) (json.RawMessage, error) {
	host := s.extractString(row.Fields, mapping.Host)
	port := s.extractInt(row.Fields, mapping.Port)
	service := strings.TrimSpace(s.extractString(row.Fields, mapping.Service))

	useTLS := true
	if mapping.UseTLS != "" {
		if raw, ok := row.Fields[mapping.UseTLS]; ok {
			parsed, ok := parseBoolValue(raw)
			if !ok {
				return nil, fmt.Errorf("use_tls must be a boolean value")
			}
			useTLS = parsed
		}
	}

	if host == "" {
		urlTarget := s.extractString(row.Fields, mapping.URL)
		parsedHost, parsedPort, inferredTLS := parseHostPortAndTLS(urlTarget)
		host = parsedHost
		if port == 0 {
			port = parsedPort
		}
		if mapping.UseTLS == "" && inferredTLS != nil {
			useTLS = *inferredTLS
		}
	}

	if host == "" {
		return nil, fmt.Errorf("host is required for grpc monitor")
	}

	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" {
		return nil, fmt.Errorf("host is required for grpc monitor")
	}

	if port <= 0 {
		if useTLS {
			port = 443
		} else {
			port = 80
		}
	}

	config := map[string]interface{}{
		"host":    host,
		"port":    port,
		"use_tls": useTLS,
	}
	if service != "" {
		config["service"] = service
	}

	return json.Marshal(config)
}

func parseHostPortAndTLS(target string) (string, int, *bool) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", 0, nil
	}

	var inferredTLS *bool
	parsePort := func(raw string) int {
		if raw == "" {
			return 0
		}
		p, err := strconv.Atoi(raw)
		if err != nil || p < 1 || p > 65535 {
			return 0
		}
		return p
	}

	parsedURL, err := url.Parse(target)
	if err == nil && parsedURL.Host != "" {
		switch strings.ToLower(strings.TrimSpace(parsedURL.Scheme)) {
		case "grpcs", "https":
			v := true
			inferredTLS = &v
		case "grpc", "http":
			v := false
			inferredTLS = &v
		}
		return strings.Trim(strings.TrimSpace(parsedURL.Hostname()), "[]"), parsePort(parsedURL.Port()), inferredTLS
	}

	rawHost := target
	if idx := strings.Index(rawHost, "/"); idx > 0 {
		rawHost = rawHost[:idx]
	}
	rawHost = strings.TrimSpace(rawHost)

	if strings.Contains(rawHost, ":") {
		if h, p, err := net.SplitHostPort(rawHost); err == nil {
			return strings.Trim(strings.TrimSpace(h), "[]"), parsePort(p), inferredTLS
		}
	}

	return strings.Trim(rawHost, "[]"), 0, inferredTLS
}

func parseBoolValue(val interface{}) (bool, bool) {
	switch v := val.(type) {
	case bool:
		return v, true
	case string:
		s := strings.TrimSpace(strings.ToLower(v))
		switch s {
		case "true", "yes", "1", "on":
			return true, true
		case "false", "no", "0", "off":
			return false, true
		default:
			return false, false
		}
	case int:
		return v != 0, true
	case int64:
		return v != 0, true
	case float64:
		return v != 0, true
	default:
		return false, false
	}
}
