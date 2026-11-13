// Execution: creating monitors from mapped rows and syncing group members.

package importservice

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/validation"
)

// ExecuteImport executes the import with the given rows and mapping
func (s *Service) ExecuteImport(ctx context.Context, tenantID uuid.UUID, req *models.ImportExecuteRequest) (*models.ImportExecuteResponse, error) {
	results := make([]models.ImportRowResult, len(req.Rows))
	successCount := 0
	failedCount := 0
	skippedCount := 0
	useRawConfig := strings.TrimSpace(req.Mapping.Config) != ""

	// Build type mapping (ensure non-nil)
	typeMapping := req.TypeMapping
	if typeMapping == nil {
		typeMapping = make(map[string]string)
	}

	// First pass: create non-group monitors and collect references for group resolution.
	monitorNameToID := make(map[string]uuid.UUID)
	monitorNameToIDs := make(map[string][]uuid.UUID)
	importedKeys := make(map[string]struct{})
	groupRows := make([]int, 0)

	existingMonitors, err := s.loadAllMonitors(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	for _, monitor := range existingMonitors {
		key := importDuplicateKey(monitor.Name, string(monitor.Type))
		if key != "" {
			importedKeys[key] = struct{}{}
		}
		monitorNameToID[monitor.Name] = monitor.ID
		monitorNameToIDs[monitor.Name] = append(monitorNameToIDs[monitor.Name], monitor.ID)
	}

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

		monitorType := s.resolveMonitorType(row, req.Mapping, typeMapping)
		result.Type = monitorType

		if !isSupportedMonitorType(monitorType) {
			result.Status = "skipped"
			result.SkipReason = fmt.Sprintf("Unknown monitor type '%s' - please configure type mapping", monitorType)
			skippedCount++
			results[i] = result
			continue
		}

		if !useRawConfig && monitorType == "agent" {
			result.Status = "skipped"
			result.SkipReason = "Agent monitors cannot be batch imported (requires agent installation)"
			skippedCount++
			results[i] = result
			continue
		}

		if !useRawConfig && monitorType != "http" && monitorType != "ping" && monitorType != "dns" && monitorType != "grpc" && monitorType != "group" {
			result.Status = "skipped"
			result.SkipReason = fmt.Sprintf("Monitor type '%s' requires a raw config field for import", monitorType)
			skippedCount++
			results[i] = result
			continue
		}

		duplicateKey := importDuplicateKey(name, monitorType)
		if _, exists := importedKeys[duplicateKey]; exists {
			result.Status = "skipped"
			result.SkipReason = fmt.Sprintf("Monitor '%s' (%s) already exists", name, monitorType)
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

		var (
			monitor *models.Monitor
			err     error
		)
		if useRawConfig {
			monitor, err = s.createMonitorFromConfig(ctx, tenantID, row, req.Mapping, monitorType)
		} else {
			monitor, err = s.createMonitor(ctx, tenantID, row, req.Mapping, monitorType)
		}
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
		importedKeys[duplicateKey] = struct{}{}
		monitorNameToID[name] = monitor.ID
		monitorNameToIDs[name] = append(monitorNameToIDs[name], monitor.ID)
	}

	// Second pass: create group monitors
	for _, i := range groupRows {
		row := req.Rows[i]
		result := results[i]

		duplicateKey := importDuplicateKey(result.Name, result.Type)
		if _, exists := importedKeys[duplicateKey]; exists {
			result.Status = "skipped"
			result.SkipReason = fmt.Sprintf("Monitor '%s' (%s) already exists", result.Name, result.Type)
			skippedCount++
			results[i] = result
			continue
		}

		var (
			monitor *models.Monitor
			err     error
		)
		if useRawConfig {
			monitor, err = s.createGroupMonitorFromConfig(ctx, tenantID, row, req.Mapping, monitorNameToIDs)
		} else {
			monitor, err = s.createGroupMonitor(ctx, tenantID, row, req.Mapping, monitorNameToID)
		}
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
		importedKeys[duplicateKey] = struct{}{}
		monitorNameToID[result.Name] = monitor.ID
		monitorNameToIDs[result.Name] = append(monitorNameToIDs[result.Name], monitor.ID)
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
		Name:                         name,
		Type:                         models.MonitorType(monitorType),
		Config:                       config,
		IntervalSeconds:              intervalSeconds,
		TimeoutSeconds:               timeoutSeconds,
		Enabled:                      &enabled,
		Tags:                         tags,
		ConsecutiveFailuresThreshold: s.extractFailureThreshold(row.Fields, mapping.ConsecutiveFailuresThreshold),
	}

	return s.monitorService.CreateMonitor(ctx, tenantID, req)
}

func (s *Service) createMonitorFromConfig(ctx context.Context, tenantID uuid.UUID, row models.ImportRow, mapping models.FieldMapping, monitorType string) (*models.Monitor, error) {
	name := s.extractString(row.Fields, mapping.Name)
	if name == "" {
		name = fmt.Sprintf("Monitor %d", row.Index+1)
	}

	intervalSeconds := s.extractInt(row.Fields, mapping.IntervalSeconds)
	if intervalSeconds <= 0 {
		intervalSeconds = 60
	}

	timeoutSeconds := s.extractInt(row.Fields, mapping.TimeoutSeconds)
	if activeCheckType(models.MonitorType(monitorType)) {
		if timeoutSeconds <= 0 {
			timeoutSeconds = 30
		}
		if timeoutSeconds >= intervalSeconds {
			timeoutSeconds = intervalSeconds - 1
		}
	} else {
		timeoutSeconds = 0
	}

	enabled := s.extractBool(row.Fields, mapping.Enabled)
	if mapping.Enabled == "" {
		enabled = true
	}

	tags := s.extractTags(row.Fields, mapping.Tags)

	config, err := s.extractRawConfig(row.Fields, mapping.Config, monitorType)
	if err != nil {
		return nil, err
	}

	// The import path bypasses the HTTP handler, so validate here: otherwise a
	// bad translation surfaces as an opaque database error instead of naming
	// the offending field.
	if err := validation.DefaultRegistry.Validate(models.MonitorType(monitorType), config); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}
	// Schedule rules a config imposes (a SIP test call's interval floor and
	// timeout cap) hold on import too.
	if err := validation.DefaultRegistry.ValidateSchedule(models.MonitorType(monitorType), config, intervalSeconds, timeoutSeconds); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	req := &models.CreateMonitorRequest{
		Name:                         name,
		Type:                         models.MonitorType(monitorType),
		Config:                       config,
		IntervalSeconds:              intervalSeconds,
		TimeoutSeconds:               timeoutSeconds,
		Enabled:                      &enabled,
		Tags:                         tags,
		ConsecutiveFailuresThreshold: s.extractFailureThreshold(row.Fields, mapping.ConsecutiveFailuresThreshold),
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
		Name:                         name,
		Type:                         models.MonitorTypeGroup,
		Config:                       configBytes,
		IntervalSeconds:              intervalSeconds,
		TimeoutSeconds:               timeoutSeconds,
		Enabled:                      &enabled,
		Tags:                         tags,
		ConsecutiveFailuresThreshold: s.extractFailureThreshold(row.Fields, mapping.ConsecutiveFailuresThreshold),
	}

	monitor, err := s.monitorService.CreateMonitor(ctx, tenantID, req)
	if err != nil {
		return nil, err
	}

	if err := s.syncGroupMembers(ctx, tenantID, monitor.ID, memberIDs); err != nil {
		return nil, err
	}

	return monitor, nil
}

// syncGroupMembers writes the monitor_groups rows that back group membership.
//
// config.monitor_ids alone is not membership: Monitor.MemberIDs, the group
// alert roll-up, and the dashboards all read the junction table. The HTTP
// handler populates it after create, but the import path calls the monitor
// service directly and would otherwise leave every imported group empty.
// Unlike the handler this reports failures instead of logging them, so a
// partially-built group shows up as a failed row rather than a silent one.
func (s *Service) syncGroupMembers(ctx context.Context, tenantID, groupID uuid.UUID, memberIDs []string) error {
	if len(memberIDs) == 0 {
		return nil
	}

	parsed := make([]uuid.UUID, 0, len(memberIDs))
	for _, memberID := range memberIDs {
		id, err := uuid.Parse(memberID)
		if err != nil {
			return fmt.Errorf("invalid group member id '%s': %w", memberID, err)
		}
		parsed = append(parsed, id)
	}

	if err := s.groupService.AddMonitorsToGroup(ctx, tenantID, groupID, parsed); err != nil {
		return fmt.Errorf("failed to attach group members: %w", err)
	}
	return nil
}

func (s *Service) createGroupMonitorFromConfig(ctx context.Context, tenantID uuid.UUID, row models.ImportRow, mapping models.FieldMapping, nameToIDs map[string][]uuid.UUID) (*models.Monitor, error) {
	name := s.extractString(row.Fields, mapping.Name)
	if name == "" {
		name = fmt.Sprintf("Group %d", row.Index+1)
	}

	intervalSeconds := s.extractInt(row.Fields, mapping.IntervalSeconds)
	if intervalSeconds <= 0 {
		intervalSeconds = 60
	}

	enabled := s.extractBool(row.Fields, mapping.Enabled)
	if mapping.Enabled == "" {
		enabled = true
	}

	tags := s.extractTags(row.Fields, mapping.Tags)
	memberNames := s.extractStrings(row.Fields, mapping.GroupMembers)
	if len(memberNames) == 0 {
		return nil, fmt.Errorf("group_members is required for portable group import")
	}

	configValue, ok := row.Fields[mapping.Config]
	if mapping.Config == "" || !ok {
		return nil, fmt.Errorf("config is required for portable group import")
	}
	configMap, ok := configValue.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("group config must be an object")
	}

	memberIDs := make([]string, 0, len(memberNames))
	for _, memberName := range memberNames {
		ids := nameToIDs[memberName]
		if len(ids) == 0 {
			return nil, fmt.Errorf("group member name '%s' could not be resolved", memberName)
		}
		if len(ids) > 1 {
			return nil, fmt.Errorf("group member name '%s' is ambiguous", memberName)
		}
		memberIDs = append(memberIDs, ids[0].String())
	}

	configCopy := cloneMap(configMap)
	configCopy["monitor_ids"] = memberIDs
	config, err := s.marshalSanitizedConfig(configCopy, models.MonitorTypeGroup)
	if err != nil {
		return nil, err
	}

	if err := validation.DefaultRegistry.Validate(models.MonitorTypeGroup, config); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	req := &models.CreateMonitorRequest{
		Name:                         name,
		Type:                         models.MonitorTypeGroup,
		Config:                       config,
		IntervalSeconds:              intervalSeconds,
		TimeoutSeconds:               0,
		Enabled:                      &enabled,
		Tags:                         tags,
		ConsecutiveFailuresThreshold: s.extractFailureThreshold(row.Fields, mapping.ConsecutiveFailuresThreshold),
	}

	monitor, err := s.monitorService.CreateMonitor(ctx, tenantID, req)
	if err != nil {
		return nil, err
	}

	if err := s.syncGroupMembers(ctx, tenantID, monitor.ID, memberIDs); err != nil {
		return nil, err
	}

	return monitor, nil
}

func importDuplicateKey(name, monitorType string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	monitorType = strings.ToLower(strings.TrimSpace(monitorType))
	if name == "" || monitorType == "" {
		return ""
	}
	return monitorType + "\x00" + name
}

func activeCheckType(monitorType models.MonitorType) bool {
	return validation.IsActiveCheckType(monitorType)
}

func (s *Service) extractRawConfig(fields map[string]interface{}, fieldName, monitorType string) (json.RawMessage, error) {
	if fieldName == "" {
		return nil, fmt.Errorf("config mapping is required for portable import")
	}
	value, ok := fields[fieldName]
	if !ok || value == nil {
		return nil, fmt.Errorf("config is required for monitor type '%s'", monitorType)
	}
	return s.marshalSanitizedConfig(value, models.MonitorType(monitorType))
}

func (s *Service) marshalSanitizedConfig(value interface{}, monitorType models.MonitorType) (json.RawMessage, error) {
	switch monitorType {
	case models.MonitorTypePush:
		if configMap, ok := value.(map[string]interface{}); ok {
			value = cloneMap(configMap)
			delete(value.(map[string]interface{}), "push_token")
		}
	case models.MonitorTypeAgent:
		if configMap, ok := value.(map[string]interface{}); ok {
			value = cloneMap(configMap)
			delete(value.(map[string]interface{}), "agent_id")
		}
	}

	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config: %w", err)
	}
	return raw, nil
}
