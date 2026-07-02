package monitors

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// Service handles monitor business logic. It is the encryption boundary for
// secret monitor-config fields (DB passwords, …): secrets are encrypted before
// INSERT/UPDATE and masked on every read so plaintext never leaves the process.
type Service struct {
	repo           Repository
	groupResolver  GroupResolver
	statusNotifier StatusNotifier
	encryptor      secrets.Encryptor
}

type GroupResolver interface {
	GetGroupLeafMembers(ctx context.Context, tenantID, groupID uuid.UUID) ([]models.Monitor, error)
}

type StatusNotifier interface {
	PublishStatusUpdate(ctx context.Context, monitorID, tenantID uuid.UUID)
}

// NewService creates a new monitor service
func NewService(database Repository) *Service {
	return &Service{
		repo:      database,
		encryptor: secrets.NoOpEncryptor{},
	}
}

// ConfigureEncryption wires the encryptor used for secret config fields.
// Without it the service falls back to a NoOpEncryptor (dev/test).
func (s *Service) ConfigureEncryption(encryptor secrets.Encryptor) {
	if encryptor != nil {
		s.encryptor = encryptor
	}
}

// ConfigureHistoryDependencies wires optional collaborators used by history reset flows.
func (s *Service) ConfigureHistoryDependencies(groupResolver GroupResolver, statusNotifier StatusNotifier) {
	s.groupResolver = groupResolver
	s.statusNotifier = statusNotifier
}

// prepareConfigForWrite merges write-only secret placeholders ("***"/empty =
// keep the stored value; pass nil existing on create) and encrypts secret
// fields. Configs for types without secret fields pass through unchanged.
func (s *Service) prepareConfigForWrite(monitorType models.MonitorType, incoming, existing json.RawMessage) (json.RawMessage, error) {
	if !secrets.HasMonitorSecrets(string(monitorType)) {
		return incoming, nil
	}
	merged, err := secrets.MergeMonitorConfigSecrets(string(monitorType), incoming, existing)
	if err != nil {
		return nil, fmt.Errorf("merge config secrets: %w", err)
	}
	encrypted, err := secrets.EncryptMonitorConfig(s.encryptor, string(monitorType), merged)
	if err != nil {
		return nil, fmt.Errorf("encrypt config secrets: %w", err)
	}
	return encrypted, nil
}

// maskSecrets replaces secret config fields with the "***" placeholder before
// a monitor leaves the service. On marshal errors the config is left as-is —
// stored values are ciphertext envelopes, so no plaintext can leak.
func (s *Service) maskSecrets(monitor *models.Monitor) {
	if monitor == nil || !secrets.HasMonitorSecrets(string(monitor.Type)) {
		return
	}
	if masked, err := secrets.MaskMonitorConfig(string(monitor.Type), monitor.Config); err == nil {
		monitor.Config = masked
	}
}

// ResolveTestConfig resolves write-only secret placeholders ("***") in an
// incoming config against the stored monitor's config so a test-connection
// request can run with the real (still encrypted) secrets. With no monitorID
// the config passes through after placeholder cleanup, dropping orphaned
// placeholders.
func (s *Service) ResolveTestConfig(ctx context.Context, tenantID uuid.UUID, monitorID *uuid.UUID, monitorType models.MonitorType, config json.RawMessage) (json.RawMessage, error) {
	if !secrets.HasMonitorSecrets(string(monitorType)) {
		return config, nil
	}
	var existing json.RawMessage
	if monitorID != nil {
		monitor, err := s.repo.GetByID(ctx, tenantID, *monitorID)
		if err != nil {
			return nil, err
		}
		existing = monitor.Config
	}
	merged, err := secrets.MergeMonitorConfigSecrets(string(monitorType), config, existing)
	if err != nil {
		return nil, fmt.Errorf("merge config secrets: %w", err)
	}
	return merged, nil
}

// CreateMonitor creates a new monitor
func (s *Service) CreateMonitor(ctx context.Context, tenantID uuid.UUID, req *models.CreateMonitorRequest) (*models.Monitor, error) {
	now := time.Now()
	monitorID := uuid.New()

	config, err := s.prepareConfigForWrite(req.Type, req.Config, nil)
	if err != nil {
		return nil, err
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	// Set next_run_at to now
	nextRunAt := now

	var alertPolicyID *uuid.UUID
	alertPolicyIDs, err := parseAlertPolicyIDs(req.AlertPolicyIDs)
	if err != nil {
		return nil, err
	}

	if req.AlertPolicyID != nil && *req.AlertPolicyID != "" {
		parsedID, err := uuid.Parse(*req.AlertPolicyID)
		if err != nil {
			return nil, fmt.Errorf("invalid alert_policy_id: %w", err)
		}
		alertPolicyID = &parsedID
		alertPolicyIDs = mergeAlertPolicyIDs(alertPolicyIDs, parsedID)
	}

	for _, policyID := range alertPolicyIDs {
		if err := s.repo.VerifyAlertPolicy(ctx, tenantID, policyID); err != nil {
			return nil, err
		}
	}

	if alertPolicyID == nil && len(alertPolicyIDs) > 0 {
		first := alertPolicyIDs[0]
		alertPolicyID = &first
	}

	// Generate agent_id for agent monitors
	var agentID *string
	if req.Type == models.MonitorTypeAgent {
		aid := uuid.New().String()
		agentID = &aid
	}

	// Generate push_token for push monitors
	var pushToken *string
	if req.Type == models.MonitorTypePush {
		token := generatePushToken()
		pushToken = &token
	}

	// Apply defaults for notification fields
	consecutiveFailuresThreshold := 2 // DB default
	if req.ConsecutiveFailuresThreshold != nil {
		consecutiveFailuresThreshold = *req.ConsecutiveFailuresThreshold
	}
	notificationMode := "default" // DB default
	if req.NotificationMode != nil {
		notificationMode = *req.NotificationMode
	}
	memberAlertRollup := "per_monitor" // DB default
	if req.MemberAlertRollup != nil {
		memberAlertRollup = *req.MemberAlertRollup
	}

	locationIDs, err := parseLocationIDs(req.LocationIDs)
	if err != nil {
		return nil, err
	}
	locationQuorum := effectiveLocationQuorum(req.LocationQuorum, len(locationIDs))

	monitor := &models.Monitor{
		ID:                           monitorID,
		TenantID:                     tenantID,
		Name:                         req.Name,
		Type:                         req.Type,
		Config:                       config,
		IntervalSeconds:              req.IntervalSeconds,
		TimeoutSeconds:               req.TimeoutSeconds,
		AlertPolicyID:                alertPolicyID,
		AlertPolicyIDs:               alertPolicyIDs,
		Enabled:                      enabled,
		Tags:                         req.Tags,
		AgentID:                      agentID,
		PushToken:                    pushToken,
		NextRunAt:                    &nextRunAt,
		CreatedAt:                    now,
		UpdatedAt:                    now,
		ConsecutiveFailuresThreshold: consecutiveFailuresThreshold,
		NotificationMode:             notificationMode,
		MemberAlertRollup:            memberAlertRollup,
		LocationQuorum:               locationQuorum,
	}

	if err := s.repo.Create(ctx, monitor); err != nil {
		return nil, err
	}

	if err := s.repo.SetAlertPolicies(ctx, monitorID, alertPolicyIDs); err != nil {
		return nil, err
	}

	if len(locationIDs) > 0 {
		if err := s.repo.SetLocations(ctx, tenantID, monitorID, locationIDs); err != nil {
			return nil, err
		}
		monitor.LocationIDs = locationIDs
	}

	// Persist custom channel assignments if mode is 'custom'
	if notificationMode == "custom" && len(req.NotificationChannels) > 0 {
		if err := s.repo.ReplaceMonitorChannels(ctx, tenantID, monitorID, req.NotificationChannels); err != nil {
			return nil, err
		}
		// Reload from DB to pick up channel name/type joined from alert_channels.
		if channelMap, err := s.repo.GetChannelsForMonitors(ctx, []uuid.UUID{monitorID}); err == nil {
			if channels, ok := channelMap[monitorID]; ok {
				monitor.NotificationChannels = channels
			} else {
				monitor.NotificationChannels = []models.MonitorChannelAssignment{}
			}
		} else {
			monitor.NotificationChannels = req.NotificationChannels
		}
	} else {
		monitor.NotificationChannels = []models.MonitorChannelAssignment{}
	}

	s.maskSecrets(monitor)
	return monitor, nil
}

// GetMonitor retrieves a monitor by ID (tenant-scoped)
func (s *Service) GetMonitor(ctx context.Context, tenantID, monitorID uuid.UUID) (*models.Monitor, error) {
	monitor, err := s.repo.GetByID(ctx, tenantID, monitorID)
	if err != nil {
		return nil, err
	}

	if policyIDs, err := s.repo.GetAlertPolicyIDs(ctx, monitorID); err == nil {
		monitor.AlertPolicyIDs = mergeAlertPolicyIDs(policyIDs, derefUUID(monitor.AlertPolicyID))
	}

	// If this is a group monitor, populate member IDs
	if monitor.Type == models.MonitorTypeGroup {
		memberIDs, err := s.repo.GetMemberIDs(ctx, monitorID)
		if err != nil {
			return nil, err
		}
		monitor.MemberIDs = memberIDs
	} else {
		dependsOnIDs, err := s.repo.GetDependsOnIDs(ctx, monitorID)
		if err != nil {
			return nil, err
		}
		monitor.DependsOnIDs = dependsOnIDs
	}

	// Attach notification channels
	if channelMap, err := s.repo.GetChannelsForMonitors(ctx, []uuid.UUID{monitorID}); err == nil {
		if channels, ok := channelMap[monitorID]; ok {
			monitor.NotificationChannels = channels
		} else {
			monitor.NotificationChannels = []models.MonitorChannelAssignment{}
		}
	}

	// Attach the private-location selection + per-location breakdown
	if locationMap, err := s.repo.GetLocationIDsForMonitors(ctx, []uuid.UUID{monitorID}); err == nil {
		monitor.LocationIDs = locationMap[monitorID]
	}
	if len(monitor.LocationIDs) > 0 {
		if statuses, err := s.repo.GetLocationStatuses(ctx, monitorID); err == nil {
			monitor.Locations = statuses
		}
	}

	s.maskSecrets(monitor)
	return monitor, nil
}

// ListMonitors lists monitors with optional filters and pagination
func (s *Service) ListMonitors(ctx context.Context, tenantID uuid.UUID, tag *string, enabled *bool, page, pageSize int) (*models.MonitorListResponse, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	monitors, total, err := s.repo.List(ctx, tenantID, tag, enabled, page, pageSize)
	if err != nil {
		return nil, err
	}

	monitorIDs := make([]uuid.UUID, 0, len(monitors))
	for i := range monitors {
		monitorIDs = append(monitorIDs, monitors[i].ID)
	}
	if policyMap, err := s.repo.GetAlertPolicyIDsForMonitors(ctx, monitorIDs); err == nil {
		for i := range monitors {
			policies := policyMap[monitors[i].ID]
			monitors[i].AlertPolicyIDs = mergeAlertPolicyIDs(policies, derefUUID(monitors[i].AlertPolicyID))
		}
	}

	// Attach notification channels for all monitors
	if channelMap, err := s.repo.GetChannelsForMonitors(ctx, monitorIDs); err == nil {
		for i := range monitors {
			if channels, ok := channelMap[monitors[i].ID]; ok {
				monitors[i].NotificationChannels = channels
			} else {
				monitors[i].NotificationChannels = []models.MonitorChannelAssignment{}
			}
		}
	}

	// Attach location selections
	if locationMap, err := s.repo.GetLocationIDsForMonitors(ctx, monitorIDs); err == nil {
		for i := range monitors {
			monitors[i].LocationIDs = locationMap[monitors[i].ID]
		}
	}

	for i := range monitors {
		s.maskSecrets(&monitors[i])
	}

	return &models.MonitorListResponse{
		Items:    monitors,
		Page:     page,
		PageSize: pageSize,
		Total:    total,
	}, nil
}

// UpdateMonitor updates a monitor (partial update)
func (s *Service) UpdateMonitor(ctx context.Context, tenantID, monitorID uuid.UUID, req *models.UpdateMonitorRequest) (*models.Monitor, error) {
	// Load straight from the repo: the stored (unmasked) config is needed to
	// resolve write-only secret placeholders in the incoming config.
	existing, err := s.repo.GetByID(ctx, tenantID, monitorID)
	if err != nil {
		return nil, err
	}

	// Build update query dynamically
	setParts := []string{}
	args := []interface{}{}
	argIndex := 1

	if req.Name != nil {
		setParts = append(setParts, fmt.Sprintf("name = $%d", argIndex))
		args = append(args, *req.Name)
		argIndex++
	}

	if req.Type != nil {
		setParts = append(setParts, fmt.Sprintf("type = $%d", argIndex))
		args = append(args, *req.Type)
		argIndex++
	}

	if len(req.Config) > 0 {
		effectiveType := existing.Type
		if req.Type != nil {
			effectiveType = *req.Type
		}
		config, err := s.prepareConfigForWrite(effectiveType, req.Config, existing.Config)
		if err != nil {
			return nil, err
		}
		setParts = append(setParts, fmt.Sprintf("config = $%d", argIndex))
		args = append(args, config)
		argIndex++
	}

	intervalSeconds := existing.IntervalSeconds
	if req.IntervalSeconds != nil {
		intervalSeconds = *req.IntervalSeconds
		setParts = append(setParts, fmt.Sprintf("interval_seconds = $%d", argIndex))
		args = append(args, *req.IntervalSeconds)
		argIndex++
	}

	timeoutSeconds := existing.TimeoutSeconds
	if req.TimeoutSeconds != nil {
		timeoutSeconds = *req.TimeoutSeconds
		setParts = append(setParts, fmt.Sprintf("timeout_seconds = $%d", argIndex))
		args = append(args, *req.TimeoutSeconds)
		argIndex++
	}

	// Validate timeout < interval
	if timeoutSeconds >= intervalSeconds {
		return nil, fmt.Errorf("timeout_seconds must be less than interval_seconds")
	}

	alertPolicyIDs := []uuid.UUID{}
	updatePolicies := false
	if req.AlertPolicyIDs != nil {
		parsed, err := parseAlertPolicyIDs(*req.AlertPolicyIDs)
		if err != nil {
			return nil, err
		}
		alertPolicyIDs = parsed
		updatePolicies = true
	}

	if req.AlertPolicyID != nil {
		var alertPolicyID *uuid.UUID
		if *req.AlertPolicyID != "" {
			parsedID, err := uuid.Parse(*req.AlertPolicyID)
			if err != nil {
				return nil, fmt.Errorf("invalid alert_policy_id: %w", err)
			}
			if err := s.repo.VerifyAlertPolicy(ctx, tenantID, parsedID); err != nil {
				return nil, err
			}
			alertPolicyID = &parsedID
			alertPolicyIDs = mergeAlertPolicyIDs(alertPolicyIDs, parsedID)
		}
		setParts = append(setParts, fmt.Sprintf("alert_policy_id = $%d", argIndex))
		args = append(args, alertPolicyID)
		argIndex++
		updatePolicies = true
	}

	if req.AlertPolicyIDs != nil {
		for _, policyID := range alertPolicyIDs {
			if err := s.repo.VerifyAlertPolicy(ctx, tenantID, policyID); err != nil {
				return nil, err
			}
		}
		if req.AlertPolicyID == nil {
			if len(alertPolicyIDs) > 0 {
				first := alertPolicyIDs[0]
				setParts = append(setParts, fmt.Sprintf("alert_policy_id = $%d", argIndex))
				args = append(args, &first)
				argIndex++
			} else {
				setParts = append(setParts, "alert_policy_id = NULL")
			}
		}
	}

	if req.Enabled != nil {
		setParts = append(setParts, fmt.Sprintf("enabled = $%d", argIndex))
		args = append(args, *req.Enabled)
		argIndex++
	}

	if req.Tags != nil {
		setParts = append(setParts, fmt.Sprintf("tags = $%d", argIndex))
		args = append(args, pq.Array(*req.Tags))
		argIndex++
	}

	if req.ConsecutiveFailuresThreshold != nil {
		setParts = append(setParts, fmt.Sprintf("consecutive_failures_threshold = $%d", argIndex))
		args = append(args, *req.ConsecutiveFailuresThreshold)
		argIndex++
	}

	if req.NotificationMode != nil {
		setParts = append(setParts, fmt.Sprintf("notification_mode = $%d", argIndex))
		args = append(args, *req.NotificationMode)
		argIndex++
	}

	if req.MemberAlertRollup != nil {
		setParts = append(setParts, fmt.Sprintf("member_alert_rollup = $%d", argIndex))
		args = append(args, *req.MemberAlertRollup)
		argIndex++
	}

	// Location threading: when the set changes, the quorum is re-clamped to
	// the new set size; an explicit quorum alone is clamped to the current set.
	var newLocationIDs []uuid.UUID
	if req.LocationIDs != nil {
		parsed, err := parseLocationIDs(*req.LocationIDs)
		if err != nil {
			return nil, err
		}
		newLocationIDs = parsed

		quorumReq := req.LocationQuorum
		if quorumReq == nil {
			existingQuorum := existing.LocationQuorum
			quorumReq = &existingQuorum
		}
		setParts = append(setParts, fmt.Sprintf("location_quorum = $%d", argIndex))
		args = append(args, effectiveLocationQuorum(quorumReq, len(parsed)))
		argIndex++
	} else if req.LocationQuorum != nil {
		currentLocations, err := s.repo.GetLocationIDsForMonitors(ctx, []uuid.UUID{monitorID})
		if err != nil {
			return nil, err
		}
		setParts = append(setParts, fmt.Sprintf("location_quorum = $%d", argIndex))
		args = append(args, effectiveLocationQuorum(req.LocationQuorum, len(currentLocations[monitorID])))
		argIndex++
	}

	// Update next_run_at if interval changed
	if req.IntervalSeconds != nil {
		setParts = append(setParts, fmt.Sprintf("next_run_at = $%d", argIndex))
		args = append(args, time.Now())
		argIndex++
	}

	if len(setParts) == 0 && req.NotificationChannels == nil {
		// No fields to update, return existing
		return existing, nil
	}

	// Create a monitor with ID and TenantID for the update
	monitor := &models.Monitor{
		ID:       monitorID,
		TenantID: tenantID,
	}

	if len(setParts) > 0 {
		if err := s.repo.Update(ctx, monitor, setParts, args); err != nil {
			return nil, err
		}
	} else {
		// No DB column updates but we still need to handle monitor_channels below.
		// Re-load so monitor has all fields populated.
		loaded, err := s.repo.GetByID(ctx, tenantID, monitorID)
		if err != nil {
			return nil, err
		}
		*monitor = *loaded
	}

	if updatePolicies {
		if err := s.repo.SetAlertPolicies(ctx, monitorID, alertPolicyIDs); err != nil {
			return nil, err
		}
		monitor.AlertPolicyIDs = alertPolicyIDs
	} else if policies, err := s.repo.GetAlertPolicyIDs(ctx, monitorID); err == nil {
		monitor.AlertPolicyIDs = mergeAlertPolicyIDs(policies, derefUUID(monitor.AlertPolicyID))
	}

	if req.LocationIDs != nil {
		if err := s.repo.SetLocations(ctx, tenantID, monitorID, newLocationIDs); err != nil {
			return nil, err
		}
		monitor.LocationIDs = newLocationIDs
	} else if locationMap, err := s.repo.GetLocationIDsForMonitors(ctx, []uuid.UUID{monitorID}); err == nil {
		monitor.LocationIDs = locationMap[monitorID]
	}

	// Handle notification channel updates
	effectiveMode := monitor.NotificationMode
	if req.NotificationMode != nil {
		effectiveMode = *req.NotificationMode
	}

	if req.NotificationMode != nil && effectiveMode == "default" {
		// Switching to default: remove all custom channels
		if err := s.repo.DeleteMonitorChannels(ctx, monitorID); err != nil {
			return nil, err
		}
		monitor.NotificationChannels = []models.MonitorChannelAssignment{}
	} else if req.NotificationChannels != nil {
		// Replace channel set
		if err := s.repo.ReplaceMonitorChannels(ctx, tenantID, monitorID, req.NotificationChannels); err != nil {
			return nil, err
		}
		// Reload from DB to pick up channel name/type joined from alert_channels.
		if channelMap, err := s.repo.GetChannelsForMonitors(ctx, []uuid.UUID{monitorID}); err == nil {
			if channels, ok := channelMap[monitorID]; ok {
				monitor.NotificationChannels = channels
			} else {
				monitor.NotificationChannels = []models.MonitorChannelAssignment{}
			}
		} else {
			monitor.NotificationChannels = req.NotificationChannels
		}
	} else {
		// Load existing channels
		if channelMap, err := s.repo.GetChannelsForMonitors(ctx, []uuid.UUID{monitorID}); err == nil {
			if channels, ok := channelMap[monitorID]; ok {
				monitor.NotificationChannels = channels
			} else {
				monitor.NotificationChannels = []models.MonitorChannelAssignment{}
			}
		}
	}

	return monitor, nil
}

func parseAlertPolicyIDs(ids []string) ([]uuid.UUID, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	unique := make(map[uuid.UUID]struct{}, len(ids))
	result := make([]uuid.UUID, 0, len(ids))
	for _, raw := range ids {
		if raw == "" {
			continue
		}
		parsed, err := uuid.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid alert_policy_id: %w", err)
		}
		if _, ok := unique[parsed]; ok {
			continue
		}
		unique[parsed] = struct{}{}
		result = append(result, parsed)
	}
	return result, nil
}

func mergeAlertPolicyIDs(ids []uuid.UUID, extra ...uuid.UUID) []uuid.UUID {
	unique := make(map[uuid.UUID]struct{}, len(ids)+len(extra))
	result := make([]uuid.UUID, 0, len(ids)+len(extra))
	for _, id := range ids {
		if id == uuid.Nil {
			continue
		}
		if _, ok := unique[id]; ok {
			continue
		}
		unique[id] = struct{}{}
		result = append(result, id)
	}
	for _, id := range extra {
		if id == uuid.Nil {
			continue
		}
		if _, ok := unique[id]; ok {
			continue
		}
		unique[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func derefUUID(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}

func parseLocationIDs(ids []string) ([]uuid.UUID, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	result := make([]uuid.UUID, 0, len(ids))
	for _, raw := range ids {
		if raw == "" {
			continue
		}
		parsed, err := uuid.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid location_id: %w", err)
		}
		if _, ok := seen[parsed]; ok {
			continue
		}
		seen[parsed] = struct{}{}
		result = append(result, parsed)
	}
	return result, nil
}

// effectiveLocationQuorum clamps a requested quorum to [1, locationCount].
// A monitor with 0 or 1 locations always has quorum 1 (the legacy behavior).
func effectiveLocationQuorum(requested *int, locationCount int) int {
	quorum := 1
	if requested != nil && *requested > 1 {
		quorum = *requested
	}
	if locationCount > 0 && quorum > locationCount {
		quorum = locationCount
	}
	if locationCount <= 1 {
		quorum = 1
	}
	return quorum
}

// DeleteMonitor deletes a monitor
func (s *Service) DeleteMonitor(ctx context.Context, tenantID, monitorID uuid.UUID) error {
	return s.repo.Delete(ctx, tenantID, monitorID)
}

// DeleteMonitorHistory clears raw checks, alert events, and persisted analytics state.
func (s *Service) DeleteMonitorHistory(ctx context.Context, tenantID, monitorID uuid.UUID) error {
	monitor, err := s.GetMonitor(ctx, tenantID, monitorID)
	if err != nil {
		return err
	}

	monitorIDs := []uuid.UUID{monitorID}
	if monitor.Type == models.MonitorTypeGroup {
		if s.groupResolver == nil {
			return fmt.Errorf("group history deletion is not configured")
		}
		members, err := s.groupResolver.GetGroupLeafMembers(ctx, tenantID, monitorID)
		if err != nil {
			return err
		}
		for _, member := range members {
			monitorIDs = append(monitorIDs, member.ID)
		}
	}

	monitorIDs = dedupeMonitorIDs(monitorIDs)
	if err := s.repo.DeleteHistory(ctx, tenantID, monitorIDs); err != nil {
		return err
	}

	if s.statusNotifier != nil {
		for _, id := range monitorIDs {
			s.statusNotifier.PublishStatusUpdate(ctx, id, tenantID)
		}
	}

	return nil
}

// BulkDeleteMonitors soft-deletes the supplied monitors in one statement.
// All monitors must belong to the tenant — if any don't, no rows are tombstoned.
func (s *Service) BulkDeleteMonitors(
	ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID,
) (int64, error) {
	if len(monitorIDs) == 0 {
		return 0, fmt.Errorf("monitor_ids cannot be empty")
	}
	if err := s.repo.VerifyMonitorsBelongToTenant(ctx, tenantID, monitorIDs); err != nil {
		return 0, err
	}
	return s.repo.BulkSoftDelete(ctx, tenantID, monitorIDs)
}

// BulkUpdateAlerting applies alerting fields to many monitors in a tenant-scoped way.
// Nil fields mean "leave unchanged". Returns the count of monitors updated.
func (s *Service) BulkUpdateAlerting(
	ctx context.Context,
	tenantID uuid.UUID,
	monitorIDs []uuid.UUID,
	threshold *int,
	mode *string,
	channels []models.MonitorChannelAssignment,
) (int, error) {
	if len(monitorIDs) == 0 {
		return 0, fmt.Errorf("monitor_ids cannot be empty")
	}
	if err := s.repo.VerifyMonitorsBelongToTenant(ctx, tenantID, monitorIDs); err != nil {
		return 0, err
	}

	for _, monitorID := range monitorIDs {
		req := &models.UpdateMonitorRequest{
			ConsecutiveFailuresThreshold: threshold,
			NotificationMode:             mode,
			NotificationChannels:         channels,
		}
		if _, err := s.UpdateMonitor(ctx, tenantID, monitorID, req); err != nil {
			return 0, fmt.Errorf("failed to update monitor %s: %w", monitorID, err)
		}
	}
	return len(monitorIDs), nil
}

// generatePushToken generates a unique token for push monitors
func generatePushToken() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

func dedupeMonitorIDs(ids []uuid.UUID) []uuid.UUID {
	if len(ids) < 2 {
		return ids
	}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	result := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}
