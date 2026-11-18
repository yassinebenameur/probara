package monitors

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/dbtx"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/pagination"
	"github.com/yassinebenameur/probara/api/internal/validation"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/db/pgerr"
	"github.com/yassinebenameur/probara/shared/logger"
	sharedmodels "github.com/yassinebenameur/probara/shared/models"
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
	// versions records configuration history (versions.go); nil on
	// repositories that are not Postgres-backed.
	versions   *VersionStore
	versionLog *logger.Logger
}

type GroupResolver interface {
	GetGroupLeafMembers(ctx context.Context, tenantID, groupID uuid.UUID) ([]models.Monitor, error)
}

type StatusNotifier interface {
	PublishStatusUpdate(ctx context.Context, monitorID, tenantID uuid.UUID)
}

// NewService creates a new monitor service
func NewService(database Repository) *Service {
	svc := &Service{
		repo:      database,
		encryptor: secrets.NoOpEncryptor{},
	}
	// History lives in the same database as the monitors, so every
	// Postgres-backed service records it without extra wiring and no write
	// path can be built without it.
	if pg, ok := database.(*PostgresRepository); ok {
		svc.versions = NewVersionStore(pg.db)
	}
	return svc
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
// keep the stored value; pass nil existing on create), moves credential-named
// headers into the secret maps (secrets.NormalizeSensitiveHeaders, after the
// merge so an absent secret map is restored before a moved header edits it),
// refuses a merged config whose kept secrets conflict with the request, and
// encrypts secret fields. Every monitor write (handlers, import, MCP, bulk)
// goes through here. Configs for types without secret fields pass through
// unchanged.
func (s *Service) prepareConfigForWrite(monitorType models.MonitorType, incoming, existing json.RawMessage) (json.RawMessage, error) {
	if !secrets.HasMonitorSecrets(string(monitorType)) {
		return incoming, nil
	}
	merged, err := mergeConfigForWrite(monitorType, incoming, existing)
	if err != nil {
		return nil, err
	}
	encrypted, err := secrets.EncryptMonitorConfig(s.encryptor, string(monitorType), merged)
	if err != nil {
		return nil, fmt.Errorf("encrypt config secrets: %w", err)
	}
	return encrypted, nil
}

// mergeConfigForWrite is the part of prepareConfigForWrite before
// encryption: the write-only merge, the move of credential-named entries and
// the conflict check. Planning an apply (apply_config.go) runs the same steps,
// so what it reports is what a save would store.
func mergeConfigForWrite(monitorType models.MonitorType, incoming, existing json.RawMessage) (json.RawMessage, error) {
	merged, err := secrets.MergeMonitorConfigSecrets(string(monitorType), incoming, existing)
	if err != nil {
		return nil, fmt.Errorf("merge config secrets: %w", err)
	}
	merged, _, err = secrets.NormalizeSensitiveHeaders(string(monitorType), merged)
	if err != nil {
		return nil, fmt.Errorf("normalize sensitive headers: %w", err)
	}
	if err := validation.ValidateMergedSecretPairs(monitorType, merged); err != nil {
		if len(existing) == 0 {
			// Nothing was kept on a create, so the conflict comes from
			// moving credential-named entries into their secret map.
			return nil, fmt.Errorf("%w: %v (after moving credential-named headers, metadata or variables into their secret map; rename or remove one of them)", ErrSecretConflict, err)
		}
		return nil, fmt.Errorf("%w: %v (a stored secret was kept because the request omitted it; send \"\" to clear it, or leave it out of a submitted secret map)", ErrSecretConflict, err)
	}
	return merged, nil
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

var ErrMonitorTypeMismatch = errors.New("test type must match the saved monitor type")

// ErrSecretConflict reports a config that is only invalid once the write-only
// merge restored a stored secret the request left out (basic auth beside a
// kept Authorization secret header, a plain header beside a kept secret one).
// Handlers map it to 400.
var ErrSecretConflict = errors.New("config conflicts with stored secrets")

// ErrMonitorKeyTaken reports a key another live monitor of the workspace
// already has (idx_monitors_tenant_key). Handlers map it to 409.
var ErrMonitorKeyTaken = errors.New("another monitor in this workspace already has this key")

// monitorKeyIndex is the unique index that keeps keys unique per workspace.
const monitorKeyIndex = "idx_monitors_tenant_key"

// keyTakenOr turns a violation of the key's unique index into
// ErrMonitorKeyTaken and returns any other error as is.
func keyTakenOr(err error) error {
	if pgerr.IsUniqueViolationOf(err, monitorKeyIndex) {
		return ErrMonitorKeyTaken
	}
	return err
}

// normalizedKey is the key column value of a request's key: nil (no key)
// for nil or "", which is how a PATCH removes a key.
func normalizedKey(key *string) *string {
	if key == nil || *key == "" {
		return nil
	}
	value := *key
	return &value
}

// ErrCallMonitorForbidden refuses a SIP test-call (INVITE) monitor to anyone
// but a tenant admin's session. Each check of such a monitor places a real,
// possibly billed, call with stored trunk credentials, which makes it a
// toll-fraud vector: editors and API keys (whatever their scope) can neither
// create nor change one. Handlers map it to 403.
var ErrCallMonitorForbidden = errors.New("SIP test-call monitors can only be created, changed or run by a tenant admin signed in to Probara (API keys cannot)")

// RequireCallPermission returns ErrCallMonitorForbidden when a monitor of
// monitorType with config places calls and the caller is not a tenant admin
// (ctxpkg.IsTenantAdmin, the RequireTenantAdmin predicate). Create and update
// apply it here, in the service, so every path that writes monitors (the
// importer included) inherits it; the run-now and test-check handlers call it
// before dispatching a call.
func RequireCallPermission(ctx context.Context, monitorType models.MonitorType, config json.RawMessage) error {
	if sharedmodels.IsSIPCallConfig(string(monitorType), config) && !ctxpkg.IsTenantAdmin(ctx) {
		return ErrCallMonitorForbidden
	}
	return nil
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
		if monitor.Type != monitorType {
			return nil, ErrMonitorTypeMismatch
		}
		existing = monitor.Config
	}
	merged, err := secrets.MergeMonitorConfigSecrets(string(monitorType), config, existing)
	if err != nil {
		return nil, fmt.Errorf("merge config secrets: %w", err)
	}
	return merged, nil
}

// InTx runs fn as one save: every monitor, group member, dependency and
// configuration version write made with the ctx it passes commits together
// or not at all (api/internal/dbtx). Inside an outer save it joins it.
// Without a Postgres-backed repository (unit tests) fn runs as is.
//
// When it opens the transaction, a deadlock or serialization failure runs fn
// once more, then comes back wrapped in dbtx.ErrConflict (handlers answer
// 503: nothing was saved). fn must therefore only write through ctx.
func (s *Service) InTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if s.versions == nil {
		return fn(ctx)
	}
	return dbtx.RunRetry(ctx, s.versions.db, func(ctx context.Context, _ *sql.Tx) error {
		return fn(withSaveScope(ctx))
	})
}

// CreateMonitor creates a new monitor and records its first version, in one
// transaction.
func (s *Service) CreateMonitor(ctx context.Context, tenantID uuid.UUID, req *models.CreateMonitorRequest) (*models.Monitor, error) {
	var monitor *models.Monitor
	err := s.InTx(ctx, func(ctx context.Context) error {
		var err error
		monitor, err = s.createMonitor(ctx, tenantID, req)
		return err
	})
	if err != nil {
		return nil, err
	}
	return monitor, nil
}

func (s *Service) createMonitor(ctx context.Context, tenantID uuid.UUID, req *models.CreateMonitorRequest) (*models.Monitor, error) {
	if err := RequireCallPermission(ctx, req.Type, req.Config); err != nil {
		return nil, err
	}
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
	recoveryThreshold := 1 // DB default: single-sample recovery (S-T7)
	if req.RecoveryThreshold != nil {
		recoveryThreshold = *req.RecoveryThreshold
	}
	notificationMode := "default" // DB default
	if req.NotificationMode != nil {
		notificationMode = *req.NotificationMode
	}
	memberAlertRollup := "per_monitor" // DB default
	if req.MemberAlertRollup != nil {
		memberAlertRollup = *req.MemberAlertRollup
	}
	dependencySuppression := "inherit" // DB default
	if req.DependencySuppression != nil {
		dependencySuppression = *req.DependencySuppression
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
		Key:                          normalizedKey(req.Key),
		Type:                         req.Type,
		Config:                       config,
		IntervalSeconds:              req.IntervalSeconds,
		TimeoutSeconds:               req.TimeoutSeconds,
		Enabled:                      enabled,
		Tags:                         req.Tags,
		AgentID:                      agentID,
		PushToken:                    pushToken,
		NextRunAt:                    &nextRunAt,
		CreatedAt:                    now,
		UpdatedAt:                    now,
		ConsecutiveFailuresThreshold: consecutiveFailuresThreshold,
		RecoveryThreshold:            recoveryThreshold,
		NotificationMode:             notificationMode,
		MemberAlertRollup:            memberAlertRollup,
		DependencySuppression:        dependencySuppression,
		LocationQuorum:               locationQuorum,
	}

	if err := s.repo.Create(ctx, monitor); err != nil {
		return nil, keyTakenOr(err)
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

	s.recordVersion(ctx, tenantID, monitorID, SecretFieldChanges("", nil, req.Type, config))
	s.maskSecrets(monitor)
	return monitor, nil
}

// GetMonitor retrieves a monitor by ID (tenant-scoped)
func (s *Service) GetMonitor(ctx context.Context, tenantID, monitorID uuid.UUID) (*models.Monitor, error) {
	monitor, err := s.repo.GetByID(ctx, tenantID, monitorID)
	if err != nil {
		return nil, err
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

	// Attach alert reachability (see ListMonitors)
	if routingMap, err := s.repo.GetAlertRoutingForMonitors(ctx, []uuid.UUID{monitorID}); err == nil {
		if status, ok := routingMap[monitorID]; ok {
			monitor.AlertRouting = &status
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
	page, pageSize = pagination.Normalize(page, pageSize, pagination.DefaultSize, pagination.MaxSize)

	monitors, total, err := s.repo.List(ctx, tenantID, tag, enabled, page, pageSize)
	if err != nil {
		return nil, err
	}

	monitorIDs := make([]uuid.UUID, 0, len(monitors))
	for i := range monitors {
		monitorIDs = append(monitorIDs, monitors[i].ID)
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

	// Attach alert reachability so the UI can flag monitors whose alerts
	// would notify nobody.
	if routingMap, err := s.repo.GetAlertRoutingForMonitors(ctx, monitorIDs); err == nil {
		for i := range monitors {
			if status, ok := routingMap[monitors[i].ID]; ok {
				monitors[i].AlertRouting = &status
			}
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

// UpdateMonitor updates a monitor (partial update) and records the version,
// in one transaction: a write that fails half way leaves nothing behind.
func (s *Service) UpdateMonitor(ctx context.Context, tenantID, monitorID uuid.UUID, req *models.UpdateMonitorRequest) (*models.Monitor, error) {
	var monitor *models.Monitor
	err := s.InTx(ctx, func(ctx context.Context) error {
		var err error
		monitor, err = s.updateMonitor(ctx, tenantID, monitorID, req)
		return err
	})
	if err != nil {
		return nil, err
	}
	return monitor, nil
}

// OnlyPause reports whether an update changes nothing but the pause state.
// Pausing is not configuration: it records no version (it shows in the
// state timeline and the audit log instead).
func OnlyPause(req *models.UpdateMonitorRequest) bool {
	if req == nil || req.Enabled == nil {
		return false
	}
	rest := *req
	rest.Enabled = nil
	return reflect.ValueOf(rest).IsZero()
}

func (s *Service) updateMonitor(ctx context.Context, tenantID, monitorID uuid.UUID, req *models.UpdateMonitorRequest) (*models.Monitor, error) {
	// Lock the row first (lock order, api/internal/dbtx; a no-op when the
	// caller already did, together with the save's other monitors), then
	// keep the configuration before any write as the baseline of a monitor
	// without history.
	if err := s.LockMonitors(ctx, tenantID, []uuid.UUID{monitorID}, nil); err != nil {
		return nil, err
	}
	if !OnlyPause(req) {
		s.CaptureBaseline(ctx, tenantID, monitorID)
	}

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

	if req.Key != nil {
		setParts = append(setParts, fmt.Sprintf("key = $%d", argIndex))
		args = append(args, normalizedKey(req.Key))
		argIndex++
	}

	if req.Type != nil {
		setParts = append(setParts, fmt.Sprintf("type = $%d", argIndex))
		args = append(args, *req.Type)
		argIndex++
	}

	effectiveType := existing.Type
	if req.Type != nil {
		effectiveType = *req.Type
	}

	var secretChanges []string
	if len(req.Config) > 0 {
		config, err := s.prepareConfigForWrite(effectiveType, req.Config, existing.Config)
		if err != nil {
			return nil, err
		}
		secretChanges = SecretFieldChanges(existing.Type, existing.Config, effectiveType, config)
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

	// Validate timeout < interval — active check types only. Passive types
	// (agent/push/group) never execute a check, so timeout is meaningless for
	// them; the DB constraint exempts them likewise (migration 000012), and
	// the agent form has always written timeout == interval, which made every
	// UI edit of an agent monitor fail here.
	if validation.IsActiveCheckType(effectiveType) && timeoutSeconds >= intervalSeconds {
		return nil, ErrTimeoutNotBelowInterval
	}

	// Enabled is handled by repo.SetEnabled below, not the dynamic UPDATE: a
	// real toggle must reset state and record a pause/resume interval in one
	// locked transaction (S-P2, docs/state-semantics.md).

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

	// A new recovery threshold applies from the next passing check; passes
	// already counted toward a recovery in progress are kept (S-T7).
	if req.RecoveryThreshold != nil {
		setParts = append(setParts, fmt.Sprintf("recovery_threshold = $%d", argIndex))
		args = append(args, *req.RecoveryThreshold)
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

	if req.DependencySuppression != nil {
		setParts = append(setParts, fmt.Sprintf("dependency_suppression = $%d", argIndex))
		args = append(args, *req.DependencySuppression)
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

	if len(setParts) == 0 && req.NotificationChannels == nil && req.Enabled == nil && req.LocationIDs == nil {
		// No fields to update, return existing. A PATCH carrying only
		// depends_on_ids ends here (the handler syncs the edges after):
		// dependency edges are deliberately outside the test-call gate
		// below, as on the edge routes (mutateDependencyEdge), since they
		// place no call and change nothing a call does.
		return existing, nil
	}

	// Any change to a test-call monitor's row, or one that would make a
	// monitor one, is a tenant admin's (pausing included: resuming starts
	// calls again). Validation separately refuses switching between the two.
	// Nothing above wrote anything: building setParts only reads.
	updatedType, updatedConfig := existing.Type, existing.Config
	if req.Type != nil {
		updatedType = *req.Type
	}
	if len(req.Config) > 0 {
		updatedConfig = req.Config
	}
	if err := RequireCallPermission(ctx, existing.Type, existing.Config); err != nil {
		return nil, err
	}
	if err := RequireCallPermission(ctx, updatedType, updatedConfig); err != nil {
		return nil, err
	}

	// Create a monitor with ID and TenantID for the update
	monitor := &models.Monitor{
		ID:       monitorID,
		TenantID: tenantID,
	}

	if len(setParts) > 0 {
		if err := s.repo.Update(ctx, monitor, setParts, args); err != nil {
			return nil, keyTakenOr(err)
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

	if req.Enabled != nil {
		if err := s.repo.SetEnabled(ctx, tenantID, monitorID, *req.Enabled); err != nil {
			return nil, err
		}
		monitor.Enabled = *req.Enabled
		if existing.Enabled != *req.Enabled {
			monitor.CurrentState = "unknown"
			monitor.ConsecutiveSuccesses = 0
		}
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

	if !OnlyPause(req) {
		s.recordVersion(ctx, tenantID, monitorID, secretChanges)
	}
	return monitor, nil
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

// DeleteMonitor deletes a monitor. Groups holding it and monitors
// depending on it lose it from their settings, so each gets a system
// version in the same transaction.
func (s *Service) DeleteMonitor(ctx context.Context, tenantID, monitorID uuid.UUID) error {
	return s.deleteMonitors(ctx, tenantID, []uuid.UUID{monitorID}, func(ctx context.Context) error {
		return s.repo.Delete(ctx, tenantID, monitorID)
	})
}

// deleteMonitors runs a delete in one transaction: the deleted monitors and
// the groups and dependents that name them are locked together, FOR UPDATE
// in one ascending pass before the first write (lock order,
// api/internal/dbtx; the affected rows' lock is what their system versions
// rely on), the affected monitors are prepared for their system versions
// (set-based, see SystemVersions), the delete runs, and the versions are
// recorded; a recording failure never fails the delete.
func (s *Service) deleteMonitors(ctx context.Context, tenantID uuid.UUID, deleted []uuid.UUID, del func(ctx context.Context) error) error {
	return s.InTx(ctx, func(ctx context.Context) error {
		var affected []uuid.UUID
		if s.versions != nil {
			var err error
			if affected, err = s.versions.MonitorsAffectedByDelete(ctx, tenantID, deleted); err != nil {
				return err
			}
		}
		if err := s.LockMonitors(ctx, tenantID, append(append([]uuid.UUID{}, deleted...), affected...), nil); err != nil {
			return err
		}
		tx, inTx := dbtx.From(ctx)
		var cascade *SystemVersions
		if s.versions != nil && inTx {
			cascade = PrepareSystemVersionsTx(ctx, tx, tenantID, affected)
		}
		if err := del(ctx); err != nil {
			return err
		}
		if cascade != nil {
			cascade.RecordTx(ctx, tx)
		}
		return nil
	})
}

// ValidateMergedConfig validates config the way a save of it would store it
// for the monitor: merged with the secrets it holds now and normalized. A
// restore checks this before writing, because a version's masked config can
// rely on a secret the monitor no longer holds (a connection string, say),
// and the saved monitor would then lack a required field.
func (s *Service) ValidateMergedConfig(ctx context.Context, tenantID, monitorID uuid.UUID, monitorType models.MonitorType, config json.RawMessage) error {
	existing, err := s.repo.GetByID(ctx, tenantID, monitorID)
	if err != nil {
		return err
	}
	merged, err := secrets.MergeMonitorConfigSecrets(string(monitorType), config, existing.Config)
	if err != nil {
		return err
	}
	if merged, _, err = secrets.NormalizeSensitiveHeaders(string(monitorType), merged); err != nil {
		return err
	}
	// A conflict with a kept secret (basic auth beside a stored
	// Authorization secret header) is reported as such, the way a save
	// reports it.
	if err := validation.ValidateMergedSecretPairs(monitorType, merged); err != nil {
		return fmt.Errorf("%w: %v", ErrSecretConflict, err)
	}
	return validation.DefaultRegistry.Validate(monitorType, merged)
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
	var deleted int64
	err := s.deleteMonitors(ctx, tenantID, monitorIDs, func(ctx context.Context) error {
		var err error
		deleted, err = s.repo.BulkSoftDelete(ctx, tenantID, monitorIDs)
		return err
	})
	return deleted, err
}

// BulkAlertingBatchSize bounds the monitors one transaction of a bulk
// alerting edit changes, so the row locks it holds (which result ingest of
// those monitors waits on) stay short.
const BulkAlertingBatchSize = 100

// ErrChannelsNotInTenant reports a bulk edit naming a channel that is not
// one of the tenant's.
var ErrChannelsNotInTenant = errors.New("one or more channels not found")

// ErrBulkAlertingRefused reports a bulk alerting edit that a selected
// monitor's type cannot take; the wrapped text names the monitor and rule.
var ErrBulkAlertingRefused = errors.New("bulk alerting edit refused")

// verifyChannels refuses channel ids that are not the tenant's (the
// repository would only refuse them on write, after earlier monitors of the
// edit were changed). Without a Postgres-backed repository it checks nothing.
func (s *Service) verifyChannels(ctx context.Context, tenantID uuid.UUID, channelIDs []string) error {
	if s.versions == nil {
		return nil
	}
	parsed := make([]uuid.UUID, 0, len(channelIDs))
	for _, id := range channelIDs {
		channelID, err := uuid.Parse(id)
		if err != nil {
			return fmt.Errorf("%w: invalid channel_id %q", ErrChannelsNotInTenant, id)
		}
		parsed = append(parsed, channelID)
	}
	parsed = sortedIDs(parsed)
	var count int
	if err := dbtx.Of(ctx, s.versions.db).QueryRowContext(ctx, `
		SELECT count(*) FROM alert_channels WHERE id = ANY($1) AND tenant_id = $2
	`, pq.Array(parsed), tenantID).Scan(&count); err != nil {
		return fmt.Errorf("verify channels: %w", err)
	}
	if count != len(parsed) {
		return ErrChannelsNotInTenant
	}
	return nil
}

// BulkUpdateAlerting applies alerting fields to many monitors in a tenant-scoped way.
// Nil fields mean "leave unchanged". Returns the count of monitors updated.
func (s *Service) BulkUpdateAlerting(
	ctx context.Context,
	tenantID uuid.UUID,
	monitorIDs []uuid.UUID,
	threshold *int,
	recoveryThreshold *int,
	mode *string,
	channels []models.MonitorChannelAssignment,
) (int, error) {
	if len(monitorIDs) == 0 {
		return 0, fmt.Errorf("monitor_ids cannot be empty")
	}
	if err := s.repo.VerifyMonitorsBelongToTenant(ctx, tenantID, monitorIDs); err != nil {
		return 0, err
	}

	// Everything a request can get wrong is checked up front, so a 403 or a
	// 400 means nothing changed: the test-call gate (UpdateMonitor would only
	// stop at the first call monitor), the per-type threshold rules the
	// handler's range check cannot see, and the channels.
	for _, monitorID := range monitorIDs {
		existing, err := s.repo.GetByID(ctx, tenantID, monitorID)
		if err != nil {
			return 0, err
		}
		if err := RequireCallPermission(ctx, existing.Type, existing.Config); err != nil {
			return 0, err
		}
		// A save of a domain monitor refuses a failure threshold below the
		// domain floor (validation.ValidateMonitorUpdate); the bulk edit
		// changes no location, so only the threshold half of the rule is
		// asked (location count 0).
		if threshold != nil && existing.Type == models.MonitorTypeDomain {
			if err := sharedmodels.ValidateDomainMonitorRules(*threshold, 0); err != nil {
				return 0, fmt.Errorf("%w: monitor %q: %v; no monitor was changed", ErrBulkAlertingRefused, existing.Name, err)
			}
		}
	}

	if len(channels) > 0 {
		ids := make([]string, 0, len(channels))
		for _, channel := range channels {
			ids = append(ids, channel.ChannelID)
		}
		if err := s.verifyChannels(ctx, tenantID, ids); err != nil {
			return 0, err
		}
	}

	// Batches of BulkAlertingBatchSize monitors in ascending id order, one
	// transaction each with its rows locked first (lock order,
	// api/internal/dbtx): every monitor of a batch and its version, or none
	// of them. Batches commit one after the other, so a failure part-way
	// (only an infrastructure or concurrency error once the checks above
	// passed) leaves the earlier batches applied; the count says how many.
	ordered := sortedIDs(monitorIDs)
	updated := 0
	for start := 0; start < len(ordered); start += BulkAlertingBatchSize {
		batch := ordered[start:min(start+BulkAlertingBatchSize, len(ordered))]
		err := s.InTx(ctx, func(ctx context.Context) error {
			// Rows first, then the batch's baselines and versions set-based
			// (one savepoint each, not one per monitor).
			if err := s.LockMonitors(ctx, tenantID, batch, nil); err != nil {
				return err
			}
			s.CaptureBaselines(ctx, tenantID, batch...)
			ctx, versions := DeferVersions(ctx)
			for _, monitorID := range batch {
				req := &models.UpdateMonitorRequest{
					ConsecutiveFailuresThreshold: threshold,
					RecoveryThreshold:            recoveryThreshold,
					NotificationMode:             mode,
					NotificationChannels:         channels,
				}
				if _, err := s.updateMonitor(ctx, tenantID, monitorID, req); err != nil {
					return fmt.Errorf("failed to update monitor %s: %w", monitorID, err)
				}
			}
			versions.Flush(ctx)
			return nil
		})
		if err != nil {
			return updated, err
		}
		updated += len(batch)
	}
	return updated, nil
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
