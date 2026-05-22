package monitors

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// Service handles monitor business logic
type Service struct {
	repo           Repository
	groupResolver  GroupResolver
	statusNotifier StatusNotifier
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
		repo: database,
	}
}

// ConfigureHistoryDependencies wires optional collaborators used by history reset flows.
func (s *Service) ConfigureHistoryDependencies(groupResolver GroupResolver, statusNotifier StatusNotifier) {
	s.groupResolver = groupResolver
	s.statusNotifier = statusNotifier
}

// CreateMonitor creates a new monitor
func (s *Service) CreateMonitor(ctx context.Context, tenantID uuid.UUID, req *models.CreateMonitorRequest) (*models.Monitor, error) {
	now := time.Now()
	monitorID := uuid.New()

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

	monitor := &models.Monitor{
		ID:              monitorID,
		TenantID:        tenantID,
		Name:            req.Name,
		Type:            req.Type,
		Config:          req.Config,
		IntervalSeconds: req.IntervalSeconds,
		TimeoutSeconds:  req.TimeoutSeconds,
		AlertPolicyID:   alertPolicyID,
		AlertPolicyIDs:  alertPolicyIDs,
		Enabled:         enabled,
		Tags:            req.Tags,
		AgentID:         agentID,
		PushToken:       pushToken,
		NextRunAt:       &nextRunAt,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if err := s.repo.Create(ctx, monitor); err != nil {
		return nil, err
	}

	if err := s.repo.SetAlertPolicies(ctx, monitorID, alertPolicyIDs); err != nil {
		return nil, err
	}

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
	}

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

	return &models.MonitorListResponse{
		Items:    monitors,
		Page:     page,
		PageSize: pageSize,
		Total:    total,
	}, nil
}

// UpdateMonitor updates a monitor (partial update)
func (s *Service) UpdateMonitor(ctx context.Context, tenantID, monitorID uuid.UUID, req *models.UpdateMonitorRequest) (*models.Monitor, error) {
	// First, get the existing monitor to validate
	existing, err := s.GetMonitor(ctx, tenantID, monitorID)
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
		setParts = append(setParts, fmt.Sprintf("config = $%d", argIndex))
		args = append(args, req.Config)
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

	// Update next_run_at if interval changed
	if req.IntervalSeconds != nil {
		setParts = append(setParts, fmt.Sprintf("next_run_at = $%d", argIndex))
		args = append(args, time.Now())
		argIndex++
	}

	if len(setParts) == 0 {
		// No fields to update, return existing
		return existing, nil
	}

	// Create a monitor with ID and TenantID for the update
	monitor := &models.Monitor{
		ID:       monitorID,
		TenantID: tenantID,
	}

	if err := s.repo.Update(ctx, monitor, setParts, args); err != nil {
		return nil, err
	}

	if updatePolicies {
		if err := s.repo.SetAlertPolicies(ctx, monitorID, alertPolicyIDs); err != nil {
			return nil, err
		}
		monitor.AlertPolicyIDs = alertPolicyIDs
	} else if policies, err := s.repo.GetAlertPolicyIDs(ctx, monitorID); err == nil {
		monitor.AlertPolicyIDs = mergeAlertPolicyIDs(policies, derefUUID(monitor.AlertPolicyID))
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

// BulkUpdateAlertPolicy attaches or detaches a single alert policy across many monitors.
func (s *Service) BulkUpdateAlertPolicy(
	ctx context.Context,
	tenantID uuid.UUID,
	monitorIDs []uuid.UUID,
	policyID uuid.UUID,
	op models.BulkAlertPolicyOp,
) (*models.BulkUpdateAlertPolicyResponse, error) {
	if len(monitorIDs) == 0 {
		return nil, fmt.Errorf("monitor_ids cannot be empty")
	}
	if op != models.BulkAlertPolicyOpAttach && op != models.BulkAlertPolicyOpDetach {
		return nil, fmt.Errorf("invalid op: %q", op)
	}

	if err := s.repo.VerifyAlertPolicy(ctx, tenantID, policyID); err != nil {
		return nil, err
	}
	if err := s.repo.VerifyMonitorsBelongToTenant(ctx, tenantID, monitorIDs); err != nil {
		return nil, err
	}

	var changed []uuid.UUID
	var err error
	switch op {
	case models.BulkAlertPolicyOpAttach:
		changed, err = s.repo.BulkAttachAlertPolicy(ctx, tenantID, monitorIDs, policyID)
	case models.BulkAlertPolicyOpDetach:
		changed, err = s.repo.BulkDetachAlertPolicy(ctx, tenantID, monitorIDs, policyID)
	}
	if err != nil {
		return nil, err
	}

	return &models.BulkUpdateAlertPolicyResponse{
		Updated:           len(changed),
		Unchanged:         len(monitorIDs) - len(changed),
		MonitorIDsUpdated: changed,
	}, nil
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
