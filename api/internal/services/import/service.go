package importservice

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/yassinebenameur/probara/api/internal/models"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
	"github.com/yassinebenameur/probara/shared/db"
)

// Service handles monitor import operations
type Service struct {
	db             *db.Client
	monitorService *monitorservice.Service
}

// NewService creates a new import service
func NewService(dbClient *db.Client, monitorSvc *monitorservice.Service) *Service {
	return &Service{
		db:             dbClient,
		monitorService: monitorSvc,
	}
}

// ParseFile auto-detects the file format and parses its contents
func (s *Service) ParseFile(data []byte, filename string) (*models.ImportPreviewResponse, error) {
	ext := strings.ToLower(filepath.Ext(filename))

	var format models.ImportFormat
	var rows []models.ImportRow
	var err error

	// Try to detect format from extension first
	switch ext {
	case ".json":
		format = models.ImportFormatJSON
		rows, err = s.parseJSON(data)
	case ".yaml", ".yml":
		format = models.ImportFormatYAML
		rows, err = s.parseYAML(data)
	case ".csv":
		format = models.ImportFormatCSV
		rows, err = s.parseCSV(data)
	default:
		// Try to auto-detect from content
		format, rows, err = s.autoDetectAndParse(data)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to parse file: %w", err)
	}

	if len(rows) == 0 {
		return nil, fmt.Errorf("no data found in file")
	}

	// Extract all unique field names
	fieldSet := make(map[string]bool)
	for _, row := range rows {
		for key := range row.Fields {
			fieldSet[key] = true
		}
	}

	detectedFields := make([]string, 0, len(fieldSet))
	for field := range fieldSet {
		detectedFields = append(detectedFields, field)
	}

	// Generate suggested mapping
	suggestedMapping := s.suggestMapping(detectedFields)

	// Generate warnings (ensure non-nil)
	warnings := s.generateWarnings(rows, suggestedMapping)
	if warnings == nil {
		warnings = []string{}
	}

	// Detect types and suggest mappings
	detectedTypes, suggestedTypeMapping := s.detectAndSuggestTypes(rows, suggestedMapping)

	return &models.ImportPreviewResponse{
		Format:               format,
		Rows:                 rows,
		DetectedFields:       detectedFields,
		SuggestedMapping:     suggestedMapping,
		Warnings:             warnings,
		TotalRows:            len(rows),
		DetectedTypes:        detectedTypes,
		SuggestedTypeMapping: suggestedTypeMapping,
	}, nil
}

// parseJSON parses JSON data
func (s *Service) parseJSON(data []byte) ([]models.ImportRow, error) {
	// Try to parse as array first
	var arrayData []map[string]interface{}
	if err := json.Unmarshal(data, &arrayData); err == nil {
		return s.mapsToRows(arrayData), nil
	}

	// Try to parse as object with items array
	var objectData struct {
		Items    []map[string]interface{} `json:"items"`
		Monitors []map[string]interface{} `json:"monitors"`
		Data     []map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(data, &objectData); err == nil {
		if len(objectData.Items) > 0 {
			return s.mapsToRows(objectData.Items), nil
		}
		if len(objectData.Monitors) > 0 {
			return s.mapsToRows(objectData.Monitors), nil
		}
		if len(objectData.Data) > 0 {
			return s.mapsToRows(objectData.Data), nil
		}
	}

	// Try to parse as single object
	var singleData map[string]interface{}
	if err := json.Unmarshal(data, &singleData); err == nil {
		return s.mapsToRows([]map[string]interface{}{singleData}), nil
	}

	return nil, fmt.Errorf("invalid JSON format")
}

// parseYAML parses YAML data
func (s *Service) parseYAML(data []byte) ([]models.ImportRow, error) {
	// First, try to parse as a generic interface to see what we have
	var generic interface{}
	if err := yaml.Unmarshal(data, &generic); err != nil {
		return nil, fmt.Errorf("invalid YAML: %v", err)
	}

	// Handle based on type
	switch v := generic.(type) {
	case []interface{}:
		// It's an array - convert each item to map
		maps := make([]map[string]interface{}, 0, len(v))
		for _, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				maps = append(maps, m)
			}
		}
		if len(maps) > 0 {
			return s.mapsToRows(maps), nil
		}
		return nil, fmt.Errorf("YAML array contains no valid objects")

	case map[string]interface{}:
		// It's an object - check for common wrapper keys
		for _, key := range []string{"items", "monitors", "data", "services", "endpoints", "checks"} {
			if items, ok := v[key]; ok {
				if itemArray, ok := items.([]interface{}); ok {
					maps := make([]map[string]interface{}, 0, len(itemArray))
					for _, item := range itemArray {
						if m, ok := item.(map[string]interface{}); ok {
							maps = append(maps, m)
						}
					}
					if len(maps) > 0 {
						return s.mapsToRows(maps), nil
					}
				}
			}
		}
		// Treat as single monitor
		if len(v) > 0 {
			return s.mapsToRows([]map[string]interface{}{v}), nil
		}
		return nil, fmt.Errorf("YAML object is empty")

	default:
		return nil, fmt.Errorf("YAML must be an array or object, got %T", generic)
	}
}

// parseCSV parses CSV data
func (s *Service) parseCSV(data []byte) ([]models.ImportRow, error) {
	reader := csv.NewReader(bytes.NewReader(data))

	// Read header row
	headers, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV header: %w", err)
	}

	// Read all records
	var rows []models.ImportRow
	index := 0
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read CSV row %d: %w", index+1, err)
		}

		fields := make(map[string]interface{})
		for i, header := range headers {
			if i < len(record) {
				// Try to parse as number or bool
				fields[header] = s.parseValue(record[i])
			}
		}

		rows = append(rows, models.ImportRow{
			Index:  index,
			Fields: fields,
		})
		index++
	}

	return rows, nil
}

// autoDetectAndParse tries to auto-detect format from content
func (s *Service) autoDetectAndParse(data []byte) (models.ImportFormat, []models.ImportRow, error) {
	// Try JSON first
	rows, err := s.parseJSON(data)
	if err == nil {
		return models.ImportFormatJSON, rows, nil
	}

	// Try YAML
	rows, err = s.parseYAML(data)
	if err == nil {
		return models.ImportFormatYAML, rows, nil
	}

	// Try CSV
	rows, err = s.parseCSV(data)
	if err == nil {
		return models.ImportFormatCSV, rows, nil
	}

	return "", nil, fmt.Errorf("could not detect file format")
}

// mapsToRows converts a slice of maps to ImportRows
func (s *Service) mapsToRows(maps []map[string]interface{}) []models.ImportRow {
	rows := make([]models.ImportRow, len(maps))
	for i, m := range maps {
		// Flatten nested objects
		flattened := s.flattenMap(m, "")
		rows[i] = models.ImportRow{
			Index:  i,
			Fields: flattened,
		}
	}
	return rows
}

// flattenMap flattens nested maps with dot notation
func (s *Service) flattenMap(m map[string]interface{}, prefix string) map[string]interface{} {
	result := make(map[string]interface{})
	for key, value := range m {
		fullKey := key
		if prefix != "" {
			fullKey = prefix + "." + key
		}

		switch v := value.(type) {
		case map[string]interface{}:
			// Recursively flatten
			for k, val := range s.flattenMap(v, fullKey) {
				result[k] = val
			}
		default:
			result[fullKey] = value
		}
	}
	return result
}

// parseValue tries to parse a string value to appropriate type
func (s *Service) parseValue(val string) interface{} {
	val = strings.TrimSpace(val)

	// Try bool
	if strings.EqualFold(val, "true") {
		return true
	}
	if strings.EqualFold(val, "false") {
		return false
	}

	// Try int
	if i, err := strconv.Atoi(val); err == nil {
		return i
	}

	// Try float
	if f, err := strconv.ParseFloat(val, 64); err == nil {
		return f
	}

	return val
}

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

	return mapping
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
	if agentCount > 0 {
		warnings = append(warnings, fmt.Sprintf("%d monitor(s) have type 'agent' which cannot be batch imported (requires agent installation).", agentCount))
	}

	return warnings
}

// detectAndSuggestTypes detects all types in the data and suggests mappings for unknown types
func (s *Service) detectAndSuggestTypes(rows []models.ImportRow, mapping models.FieldMapping) ([]string, map[string]string) {
	typeSet := make(map[string]bool)
	suggestedMapping := make(map[string]string)

	// Supported types
	supportedTypes := map[string]bool{
		"http":  true,
		"ping":  true,
		"dns":   true,
		"grpc":  true,
		"group": true,
		"agent": true,
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

// ExecuteImport executes the import with the given rows and mapping
func (s *Service) ExecuteImport(ctx context.Context, tenantID uuid.UUID, req *models.ImportExecuteRequest) (*models.ImportExecuteResponse, error) {
	results := make([]models.ImportRowResult, len(req.Rows))
	successCount := 0
	failedCount := 0
	skippedCount := 0

	// Build type mapping (ensure non-nil)
	typeMapping := req.TypeMapping
	if typeMapping == nil {
		typeMapping = make(map[string]string)
	}

	// First pass: create non-group monitors and collect name->ID mapping
	monitorNameToID := make(map[string]uuid.UUID)
	groupRows := make([]int, 0)

	for i, row := range req.Rows {
		result := models.ImportRowResult{
			Index: row.Index,
		}

		// Extract name
		name := s.extractString(row.Fields, req.Mapping.Name)
		if name == "" {
			name = fmt.Sprintf("Monitor %d", row.Index+1)
		}
		result.Name = name

		// Extract type (default to http)
		rawType := strings.ToLower(strings.TrimSpace(s.extractString(row.Fields, req.Mapping.Type)))
		monitorType := rawType

		// Apply type mapping if available
		if mappedType, ok := typeMapping[rawType]; ok && mappedType != "" {
			monitorType = strings.ToLower(mappedType)
		}

		// If still empty, try to infer type from fields
		if monitorType == "" {
			hasHost := s.extractString(row.Fields, req.Mapping.Host) != ""
			hasGRPCHint := s.extractString(row.Fields, req.Mapping.Service) != "" ||
				s.extractString(row.Fields, req.Mapping.UseTLS) != "" ||
				s.extractString(row.Fields, req.Mapping.Port) != ""

			if hasHost && hasGRPCHint {
				monitorType = "grpc"
			} else if hasHost {
				monitorType = "ping"
			} else if s.extractString(row.Fields, req.Mapping.URL) != "" {
				monitorType = "http"
			} else if s.extractString(row.Fields, req.Mapping.GroupMembers) != "" {
				monitorType = "group"
			} else {
				monitorType = "http"
			}
		}
		result.Type = monitorType

		// Check for unsupported types
		if monitorType == "agent" {
			result.Status = "skipped"
			result.SkipReason = "Agent monitors cannot be batch imported (requires agent installation)"
			skippedCount++
			results[i] = result
			continue
		}

		if monitorType != "http" && monitorType != "ping" && monitorType != "dns" && monitorType != "grpc" && monitorType != "group" {
			result.Status = "skipped"
			result.SkipReason = fmt.Sprintf("Unknown monitor type '%s' - please configure type mapping", monitorType)
			skippedCount++
			results[i] = result
			continue
		}

		// Defer group monitors to second pass
		if monitorType == "group" {
			groupRows = append(groupRows, i)
			results[i] = result
			continue
		}

		// Create the monitor
		monitor, err := s.createMonitor(ctx, tenantID, row, req.Mapping, monitorType)
		if err != nil {
			result.Status = "failed"
			result.Error = err.Error()
			failedCount++
			results[i] = result
			continue
		}

		result.Status = "success"
		monitorID := monitor.ID.String()
		result.MonitorID = &monitorID
		successCount++
		results[i] = result

		// Store name->ID mapping for group resolution
		monitorNameToID[name] = monitor.ID
	}

	// Second pass: create group monitors
	for _, i := range groupRows {
		row := req.Rows[i]
		result := results[i]

		monitor, err := s.createGroupMonitor(ctx, tenantID, row, req.Mapping, monitorNameToID)
		if err != nil {
			result.Status = "failed"
			result.Error = err.Error()
			failedCount++
			results[i] = result
			continue
		}

		result.Status = "success"
		monitorID := monitor.ID.String()
		result.MonitorID = &monitorID
		successCount++
		results[i] = result
	}

	return &models.ImportExecuteResponse{
		Results:      results,
		TotalRows:    len(req.Rows),
		SuccessCount: successCount,
		FailedCount:  failedCount,
		SkippedCount: skippedCount,
	}, nil
}

// createMonitor creates a single non-group monitor
func (s *Service) createMonitor(ctx context.Context, tenantID uuid.UUID, row models.ImportRow, mapping models.FieldMapping, monitorType string) (*models.Monitor, error) {
	name := s.extractString(row.Fields, mapping.Name)
	if name == "" {
		name = fmt.Sprintf("Monitor %d", row.Index+1)
	}

	intervalSeconds := s.extractInt(row.Fields, mapping.IntervalSeconds)
	if intervalSeconds <= 0 {
		intervalSeconds = 60 // Default to 60 seconds
	}

	timeoutSeconds := s.extractInt(row.Fields, mapping.TimeoutSeconds)
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30 // Default to 30 seconds
	}

	// Ensure timeout < interval
	if timeoutSeconds >= intervalSeconds {
		timeoutSeconds = intervalSeconds - 1
	}

	enabled := s.extractBool(row.Fields, mapping.Enabled)
	if mapping.Enabled == "" {
		enabled = true // Default enabled
	}

	tags := s.extractTags(row.Fields, mapping.Tags)

	// Build config based on type
	var config json.RawMessage
	var err error

	switch monitorType {
	case "http":
		config, err = s.buildHTTPConfig(row, mapping)
	case "ping":
		config, err = s.buildPingConfig(row, mapping)
	case "dns":
		config, err = s.buildDNSConfig(row, mapping)
	case "grpc":
		config, err = s.buildGRPCConfig(row, mapping)
	default:
		return nil, fmt.Errorf("unsupported monitor type: %s", monitorType)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to build config: %w", err)
	}

	req := &models.CreateMonitorRequest{
		Name:            name,
		Type:            models.MonitorType(monitorType),
		Config:          config,
		IntervalSeconds: intervalSeconds,
		TimeoutSeconds:  timeoutSeconds,
		Enabled:         &enabled,
		Tags:            tags,
	}

	return s.monitorService.CreateMonitor(ctx, tenantID, req)
}

// createGroupMonitor creates a group monitor with members
func (s *Service) createGroupMonitor(ctx context.Context, tenantID uuid.UUID, row models.ImportRow, mapping models.FieldMapping, nameToID map[string]uuid.UUID) (*models.Monitor, error) {
	name := s.extractString(row.Fields, mapping.Name)
	if name == "" {
		name = fmt.Sprintf("Group %d", row.Index+1)
	}

	intervalSeconds := s.extractInt(row.Fields, mapping.IntervalSeconds)
	if intervalSeconds <= 0 {
		intervalSeconds = 60
	}

	timeoutSeconds := s.extractInt(row.Fields, mapping.TimeoutSeconds)
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30
	}

	enabled := s.extractBool(row.Fields, mapping.Enabled)
	if mapping.Enabled == "" {
		enabled = true
	}

	tags := s.extractTags(row.Fields, mapping.Tags)

	// Extract member names and resolve to IDs
	membersStr := s.extractString(row.Fields, mapping.GroupMembers)
	memberNames := strings.Split(membersStr, ",")
	memberIDs := make([]string, 0)

	for _, memberName := range memberNames {
		memberName = strings.TrimSpace(memberName)
		if memberName == "" {
			continue
		}
		if id, ok := nameToID[memberName]; ok {
			memberIDs = append(memberIDs, id.String())
		}
	}

	// Build group config
	groupConfig := models.GroupConfig{
		MonitorIDs: memberIDs,
	}
	configBytes, err := json.Marshal(groupConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal group config: %w", err)
	}

	req := &models.CreateMonitorRequest{
		Name:            name,
		Type:            models.MonitorTypeGroup,
		Config:          configBytes,
		IntervalSeconds: intervalSeconds,
		TimeoutSeconds:  timeoutSeconds,
		Enabled:         &enabled,
		Tags:            tags,
	}

	return s.monitorService.CreateMonitor(ctx, tenantID, req)
}

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

// extractString extracts a string value from fields
func (s *Service) extractString(fields map[string]interface{}, fieldName string) string {
	if fieldName == "" {
		return ""
	}
	if val, ok := fields[fieldName]; ok {
		return strings.TrimSpace(fmt.Sprint(val))
	}
	return ""
}

// extractInt extracts an integer value from fields
func (s *Service) extractInt(fields map[string]interface{}, fieldName string) int {
	if fieldName == "" {
		return 0
	}
	if val, ok := fields[fieldName]; ok {
		switch v := val.(type) {
		case int:
			return v
		case int64:
			return int(v)
		case float64:
			return int(v)
		case string:
			if i, err := strconv.Atoi(v); err == nil {
				return i
			}
		}
	}
	return 0
}

// extractBool extracts a boolean value from fields
func (s *Service) extractBool(fields map[string]interface{}, fieldName string) bool {
	if fieldName == "" {
		return false
	}
	if val, ok := fields[fieldName]; ok {
		switch v := val.(type) {
		case bool:
			return v
		case string:
			return strings.EqualFold(v, "true") || strings.EqualFold(v, "yes") || strings.EqualFold(v, "1")
		case int:
			return v != 0
		case float64:
			return v != 0
		}
	}
	return false
}

// extractTags extracts tags from fields
func (s *Service) extractTags(fields map[string]interface{}, fieldName string) []string {
	if fieldName == "" {
		return nil
	}
	if val, ok := fields[fieldName]; ok {
		switch v := val.(type) {
		case []interface{}:
			tags := make([]string, 0, len(v))
			for _, item := range v {
				tag := strings.TrimSpace(fmt.Sprint(item))
				if tag != "" {
					tags = append(tags, tag)
				}
			}
			return tags
		case []string:
			return v
		case string:
			// Parse comma-separated tags
			parts := strings.Split(v, ",")
			tags := make([]string, 0, len(parts))
			for _, part := range parts {
				tag := strings.TrimSpace(part)
				if tag != "" {
					tags = append(tags, tag)
				}
			}
			return tags
		}
	}
	return nil
}
