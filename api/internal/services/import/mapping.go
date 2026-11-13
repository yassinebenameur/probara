// Field-mapping heuristics: suggesting which source column feeds which monitor field, and which monitor type a row is.

package importservice

import (
	"fmt"
	"strings"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/validation"
)

// suggestMapping generates suggested field mappings based on field names
func (s *Service) suggestMapping(fields []string) models.FieldMapping {
	mapping := models.FieldMapping{}

	fieldMap := make(map[string]string)
	for _, f := range fields {
		fieldMap[strings.ToLower(f)] = f
	}

	// Name mappings
	for _, candidate := range []string{"name", "monitor_name", "title", "label"} {
		if original, ok := fieldMap[candidate]; ok {
			mapping.Name = original
			break
		}
	}

	// Type mappings
	for _, candidate := range []string{"type", "monitor_type", "kind"} {
		if original, ok := fieldMap[candidate]; ok {
			mapping.Type = original
			break
		}
	}

	for _, candidate := range []string{"config", "monitor_config"} {
		if original, ok := fieldMap[candidate]; ok {
			mapping.Config = original
			break
		}
	}

	// URL mappings (for HTTP monitors)
	for _, candidate := range []string{"url", "endpoint", "address", "target", "config.url"} {
		if original, ok := fieldMap[candidate]; ok {
			mapping.URL = original
			break
		}
	}

	// Method mappings
	for _, candidate := range []string{"method", "http_method", "config.method"} {
		if original, ok := fieldMap[candidate]; ok {
			mapping.Method = original
			break
		}
	}

	// Expected status mappings
	for _, candidate := range []string{"expected_status", "status_code", "config.expected_status"} {
		if original, ok := fieldMap[candidate]; ok {
			mapping.ExpectedStatus = original
			break
		}
	}

	// Expected body mappings
	for _, candidate := range []string{"expected_body", "expected_body_substring", "body_contains", "config.expected_body_substring"} {
		if original, ok := fieldMap[candidate]; ok {
			mapping.ExpectedBody = original
			break
		}
	}

	// Host mappings (for ping monitors)
	for _, candidate := range []string{"host", "hostname", "server", "ip", "config.host"} {
		if original, ok := fieldMap[candidate]; ok {
			mapping.Host = original
			break
		}
	}

	// Port mappings (for gRPC monitors)
	for _, candidate := range []string{"port", "grpc_port", "config.port"} {
		if original, ok := fieldMap[candidate]; ok {
			mapping.Port = original
			break
		}
	}

	// Service mappings (for gRPC health checks)
	for _, candidate := range []string{"service", "grpc_service", "service_name", "config.service"} {
		if original, ok := fieldMap[candidate]; ok {
			mapping.Service = original
			break
		}
	}

	// TLS mappings (for gRPC monitors)
	for _, candidate := range []string{"use_tls", "tls", "ssl", "secure", "config.use_tls"} {
		if original, ok := fieldMap[candidate]; ok {
			mapping.UseTLS = original
			break
		}
	}

	// Interval mappings
	for _, candidate := range []string{"interval", "interval_seconds", "check_interval", "frequency"} {
		if original, ok := fieldMap[candidate]; ok {
			mapping.IntervalSeconds = original
			break
		}
	}

	// Timeout mappings
	for _, candidate := range []string{"timeout", "timeout_seconds"} {
		if original, ok := fieldMap[candidate]; ok {
			mapping.TimeoutSeconds = original
			break
		}
	}

	// Tags mappings
	for _, candidate := range []string{"tags", "labels", "categories"} {
		if original, ok := fieldMap[candidate]; ok {
			mapping.Tags = original
			break
		}
	}

	// Enabled mappings
	for _, candidate := range []string{"enabled", "active", "status"} {
		if original, ok := fieldMap[candidate]; ok {
			mapping.Enabled = original
			break
		}
	}

	// Group members mappings
	for _, candidate := range []string{"members", "group_members", "monitors", "monitor_ids"} {
		if original, ok := fieldMap[candidate]; ok {
			mapping.GroupMembers = original
			break
		}
	}

	// Retry tolerance. "maxretries" is Uptime Kuma's spelling.
	for _, candidate := range []string{"consecutive_failures_threshold", "retries", "max_retries", "maxretries"} {
		if original, ok := fieldMap[candidate]; ok {
			mapping.ConsecutiveFailuresThreshold = original
			break
		}
	}

	return mapping
}

// consecutiveFailuresThresholdBounds mirror the monitors table CHECK
// constraint (migration 000044) and ValidateMonitorNotificationFields.
const (
	minConsecutiveFailuresThreshold = 1
	maxConsecutiveFailuresThreshold = 10
)

// extractFailureThreshold reads the retry tolerance and clamps it into the
// range the column accepts. Sources routinely carry 0 ("no retries", which
// Probara spells as 1) or a value well above 10.
func (s *Service) extractFailureThreshold(fields map[string]interface{}, fieldName string) *int {
	if fieldName == "" {
		return nil
	}
	if _, ok := fields[fieldName]; !ok {
		return nil
	}

	threshold := s.extractInt(fields, fieldName)
	if threshold < minConsecutiveFailuresThreshold {
		threshold = minConsecutiveFailuresThreshold
	}
	if threshold > maxConsecutiveFailuresThreshold {
		threshold = maxConsecutiveFailuresThreshold
	}
	return &threshold
}

// generateWarnings generates warnings about the import data
func (s *Service) generateWarnings(rows []models.ImportRow, mapping models.FieldMapping) []string {
	var warnings []string

	// Check for missing required fields
	if mapping.Name == "" {
		warnings = append(warnings, "No 'name' field detected. Please map a field to 'name'.")
	}

	// Check for agent type monitors
	agentCount := 0
	for _, row := range rows {
		typeValue := s.getFieldValue(row.Fields, mapping.Type)
		if strings.EqualFold(fmt.Sprint(typeValue), "agent") {
			agentCount++
		}
	}
	if agentCount > 0 && mapping.Config == "" {
		warnings = append(warnings, fmt.Sprintf("%d monitor(s) have type 'agent' which cannot be batch imported (requires agent installation).", agentCount))
	}

	return warnings
}

// detectAndSuggestTypes detects all types in the data and suggests mappings for unknown types
func (s *Service) detectAndSuggestTypes(rows []models.ImportRow, mapping models.FieldMapping) ([]string, map[string]string) {
	typeSet := make(map[string]bool)
	suggestedMapping := make(map[string]string)

	// Supported types come from the validator registry so preview suggestions
	// stay in lockstep with what ExecuteImport actually accepts.
	supportedTypes := make(map[string]bool)
	for _, t := range validation.DefaultRegistry.Types() {
		supportedTypes[string(t)] = true
	}

	// Common type aliases that map to supported types
	typeAliases := map[string]string{
		// HTTP aliases
		"api":         "http",
		"web":         "http",
		"website":     "http",
		"endpoint":    "http",
		"url":         "http",
		"rest":        "http",
		"https":       "http",
		"http_check":  "http",
		"api_check":   "http",
		"web_check":   "http",
		"healthcheck": "http",
		"health":      "http",
		// gRPC aliases
		"grpcs":       "grpc",
		"grpc_health": "grpc",
		"g-rpc":       "grpc",
		// Ping aliases
		"icmp":       "ping",
		"ping_check": "ping",
		"host":       "ping",
		"server":     "ping",
		// Group aliases
		"folder":     "group",
		"category":   "group",
		"collection": "group",
		// Database aliases
		"postgresql": "postgres",
		"pg":         "postgres",
		"pgsql":      "postgres",
		"mongo":      "mongodb",
		"amqp":       "rabbitmq",
		"rabbit":     "rabbitmq",
		"mariadb":    "mysql",
		// WebSocket aliases
		"ws":  "websocket",
		"wss": "websocket",
	}

	for _, row := range rows {
		typeValue := s.getFieldValue(row.Fields, mapping.Type)
		if typeValue != nil {
			typeStr := strings.ToLower(strings.TrimSpace(fmt.Sprint(typeValue)))
			if typeStr != "" {
				typeSet[typeStr] = true

				// If not a supported type, suggest a mapping
				if !supportedTypes[typeStr] {
					if alias, ok := typeAliases[typeStr]; ok {
						suggestedMapping[typeStr] = alias
					} else {
						// Default suggestion based on common patterns
						suggestedMapping[typeStr] = "http"
					}
				}
			}
		}
	}

	// Convert to slice
	detectedTypes := make([]string, 0, len(typeSet))
	for t := range typeSet {
		detectedTypes = append(detectedTypes, t)
	}

	return detectedTypes, suggestedMapping
}

// getFieldValue gets a field value from a row using the mapping
func (s *Service) getFieldValue(fields map[string]interface{}, fieldName string) interface{} {
	if fieldName == "" {
		return nil
	}
	return fields[fieldName]
}

func (s *Service) resolveMonitorType(row models.ImportRow, mapping models.FieldMapping, typeMapping map[string]string) string {
	rawType := strings.ToLower(strings.TrimSpace(s.extractString(row.Fields, mapping.Type)))
	monitorType := rawType

	if mappedType, ok := typeMapping[rawType]; ok && mappedType != "" {
		monitorType = strings.ToLower(mappedType)
	}

	if monitorType != "" {
		return monitorType
	}

	hasHost := s.extractString(row.Fields, mapping.Host) != ""
	hasGRPCHint := s.extractString(row.Fields, mapping.Service) != "" ||
		s.extractString(row.Fields, mapping.UseTLS) != "" ||
		s.extractString(row.Fields, mapping.Port) != ""

	if hasHost && hasGRPCHint {
		return "grpc"
	}
	if hasHost {
		return "ping"
	}
	if s.extractString(row.Fields, mapping.URL) != "" {
		return "http"
	}
	if len(s.extractStrings(row.Fields, mapping.GroupMembers)) > 0 || s.extractString(row.Fields, mapping.GroupMembers) != "" {
		return "group"
	}
	return "http"
}

// isSupportedMonitorType delegates to the validator registry so the import
// path automatically accepts every monitor type the platform supports and
// never drifts when new types are added.
func isSupportedMonitorType(monitorType string) bool {
	return validation.DefaultRegistry.Has(models.MonitorType(monitorType))
}
