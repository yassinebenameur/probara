package monitors

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/google/uuid"

	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

// Monitor configuration history (monitor_config_versions, migration 000111).
//
// Every save of a monitor records a version: CreateMonitor and UpdateMonitor
// call recordVersion, so the HTTP handlers, bulk edits, the importer and MCP
// (which dispatches through the router) inherit it. Writes that bypass those
// two calls record explicitly: the handlers' group member and dependency
// routes call RecordVersion, and discovery approval, which creates monitors
// through the repository inside its own transaction, calls RecordVersionTx
// beside CreateInTx. Any new path that creates or changes a monitor must do
// one of these.
//
// A version is a masked snapshot of the monitor after the save
// (models.MonitorConfigSnapshot), taken from the database so the junction
// tables (custom channels, locations, dependencies, group members) are
// included. A save whose snapshot hashes like the latest version and that
// changed no secret records nothing. changed_secret_fields compares the
// stored secret envelopes before and after the save, so a rotated password
// is a version even though its masked snapshot is unchanged.

// Version sources: the surface a save came through.
const (
	VersionSourceUI        = "ui"
	VersionSourceAPI       = "api"
	VersionSourceMCP       = "mcp"
	VersionSourceImport    = "import"
	VersionSourceDiscovery = "discovery"
	VersionSourceRestore   = "restore"
	// VersionSourceBaseline is the configuration a monitor had before its
	// first recorded save (recordVersionTx).
	VersionSourceBaseline = "baseline"
	// VersionSourceSystem is a change made outside a save: a deleted
	// location, channel or member monitor (RecordSystemVersionsTx).
	VersionSourceSystem = "system"
)

// Version actor types.
const (
	VersionActorUser   = "user"
	VersionActorAPIKey = "api_key"
	VersionActorSystem = "system"
)

// MaxVersionsPerMonitor is how many versions each monitor keeps; the oldest
// are pruned when a new version is recorded. It bounds the table at this many
// snapshots per live monitor whatever an automation does.
const MaxVersionsPerMonitor = 100

// ErrVersionNotFound reports an unknown version, one pruned by retention, or
// a monitor of another tenant.
var ErrVersionNotFound = errors.New("monitor version not found")

type versionSourceKey struct{}
type versionRestoreKey struct{}
type versionBatchKey struct{}

// WithVersionSource names the source recorded for monitor saves made with
// ctx (import, discovery). Without it the source follows the credential: MCP
// dispatch is mcp, an operator session ui, anything else api.
func WithVersionSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, versionSourceKey{}, source)
}

// WithVersionRestore marks saves made with ctx as a restore of version v.
func WithVersionRestore(ctx context.Context, v int) context.Context {
	return context.WithValue(WithVersionSource(ctx, VersionSourceRestore), versionRestoreKey{}, v)
}

func versionSourceFromContext(ctx context.Context) string {
	if source, ok := ctx.Value(versionSourceKey{}).(string); ok && source != "" {
		return source
	}
	if via, ok := ctxpkg.GetVia(ctx); ok && via == ctxpkg.ViaMCP {
		return VersionSourceMCP
	}
	if _, ok := ctxpkg.GetAdminID(ctx); ok {
		return VersionSourceUI
	}
	return VersionSourceAPI
}

type versionActor struct {
	typ string
	id  *uuid.UUID
}

func versionActorFromContext(ctx context.Context) versionActor {
	if adminID, ok := ctxpkg.GetAdminID(ctx); ok {
		if parsed, err := uuid.Parse(adminID); err == nil {
			return versionActor{typ: VersionActorUser, id: &parsed}
		}
	}
	if keyID, ok := ctxpkg.GetAPIKeyID(ctx); ok {
		if parsed, err := uuid.Parse(keyID); err == nil {
			return versionActor{typ: VersionActorAPIKey, id: &parsed}
		}
	}
	if actorType, ok := ctxpkg.GetActorType(ctx); ok && actorType == ctxpkg.ActorTypeAPIKey {
		return versionActor{typ: VersionActorAPIKey}
	}
	return versionActor{typ: VersionActorSystem}
}

// versionMeta is everything about a version except its snapshot.
type versionMeta struct {
	source        string
	actor         versionActor
	restoredFrom  *int
	secretChanges []string
}

func versionMetaFromContext(ctx context.Context, secretChanges []string) versionMeta {
	meta := versionMeta{
		source:        versionSourceFromContext(ctx),
		actor:         versionActorFromContext(ctx),
		secretChanges: secretChanges,
	}
	if v, ok := ctx.Value(versionRestoreKey{}).(int); ok {
		meta.restoredFrom = &v
	}
	return meta
}

// VersionBatch defers version recording until the caller has finished every
// part of one save. The HTTP handlers sync group members and dependencies
// after CreateMonitor/UpdateMonitor returns, and the importer adds group
// members in a second pass: recording at the service call would split one
// save into two versions. Flush records each touched monitor once.
type VersionBatch struct {
	mu      sync.Mutex
	order   []uuid.UUID
	pending map[uuid.UUID]*pendingVersion
}

type pendingVersion struct {
	svc      *Service
	tenantID uuid.UUID
	secrets  map[string]struct{}
}

// DeferVersions returns a context whose monitor saves queue their versions in
// the returned batch instead of recording them; call Flush once the save is
// complete (on error paths too: whatever was written is recorded). Inside an
// already deferred context it returns ctx and a nil batch, whose Flush is a
// no-op, so the outer batch records everything.
func DeferVersions(ctx context.Context) (context.Context, *VersionBatch) {
	if _, ok := ctx.Value(versionBatchKey{}).(*VersionBatch); ok {
		return ctx, nil
	}
	batch := &VersionBatch{pending: map[uuid.UUID]*pendingVersion{}}
	return context.WithValue(ctx, versionBatchKey{}, batch), batch
}

func (b *VersionBatch) add(svc *Service, tenantID, monitorID uuid.UUID, secretChanges []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry, ok := b.pending[monitorID]
	if !ok {
		entry = &pendingVersion{svc: svc, tenantID: tenantID, secrets: map[string]struct{}{}}
		b.pending[monitorID] = entry
		b.order = append(b.order, monitorID)
	}
	for _, field := range secretChanges {
		entry.secrets[field] = struct{}{}
	}
}

// Flush records a version for every monitor saved since DeferVersions (or
// the previous Flush) and returns the version recorded per monitor (absent
// when the save changed nothing). Safe on a nil batch and when called twice.
// Callers flush inside the save's transaction, so the versions commit with
// it; recording ignores ctx cancellation (recordVersionNow), so a client
// that disconnects after a save outside a transaction committed still gets
// its version written.
func (b *VersionBatch) Flush(ctx context.Context) map[uuid.UUID]int {
	recorded := map[uuid.UUID]int{}
	if b == nil {
		return recorded
	}
	b.mu.Lock()
	order, pending := b.order, b.pending
	b.order, b.pending = nil, map[uuid.UUID]*pendingVersion{}
	b.mu.Unlock()
	// One set-based recording (one savepoint) per service and tenant,
	// however many monitors the save touched.
	type key struct {
		svc      *Service
		tenantID uuid.UUID
	}
	groups := map[key][]recordEntry{}
	var keys []key
	for _, monitorID := range order {
		entry := pending[monitorID]
		fields := make([]string, 0, len(entry.secrets))
		for field := range entry.secrets {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		k := key{entry.svc, entry.tenantID}
		if _, seen := groups[k]; !seen {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], recordEntry{monitorID: monitorID, secretChanges: fields})
	}
	for _, k := range keys {
		for id, version := range k.svc.recordNow(ctx, k.tenantID, groups[k]) {
			recorded[id] = version
		}
	}
	return recorded
}

// RecordVersion records the monitor's current configuration as a version
// (queued when ctx carries a VersionBatch). Handlers call it after writes that
// bypass CreateMonitor/UpdateMonitor: group member and dependency routes and
// the dependency sync of a create or update.
func (s *Service) RecordVersion(ctx context.Context, tenantID, monitorID uuid.UUID) {
	s.recordVersion(ctx, tenantID, monitorID, nil)
}

func (s *Service) recordVersion(ctx context.Context, tenantID, monitorID uuid.UUID, secretChanges []string) {
	if s.versions == nil {
		return
	}
	if batch, ok := ctx.Value(versionBatchKey{}).(*VersionBatch); ok {
		batch.add(s, tenantID, monitorID, secretChanges)
		return
	}
	s.recordVersionNow(ctx, tenantID, monitorID, secretChanges)
}

// recordVersionNow writes the version, in the save's transaction when ctx
// carries one, and returns its number (0 when nothing was recorded). A
// failure is logged, never returned: it rolls back to a savepoint and leaves
// the save itself alone, and failing the request would invite a retry of a
// save that succeeded. The write ignores ctx cancellation: a save that
// committed must not lose its version to a client that went away.
func (s *Service) recordVersionNow(ctx context.Context, tenantID, monitorID uuid.UUID, secretChanges []string) int {
	return s.recordNow(ctx, tenantID, []recordEntry{{monitorID: monitorID, secretChanges: secretChanges}})[monitorID]
}

// recordNow records the versions of a save's monitors (see recordVersionNow)
// and returns the version recorded per monitor.
func (s *Service) recordNow(ctx context.Context, tenantID uuid.UUID, entries []recordEntry) map[uuid.UUID]int {
	if s.versions == nil {
		return map[uuid.UUID]int{}
	}
	ctx = context.WithoutCancel(ctx)
	recorded, err := s.versions.recordSet(ctx, tenantID, entries, versionMetaFromContext(ctx, nil))
	if err != nil {
		s.versionLogger().WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"monitors":  len(entries),
		}).Error("Failed to record monitor config versions")
		return map[uuid.UUID]int{}
	}
	return recorded
}

// Versions returns the version history store (nil when the service runs on a
// repository that is not Postgres-backed, as in unit tests).
func (s *Service) Versions() *VersionStore {
	return s.versions
}

// ConfigureVersionLog sets the logger that reports failed version writes.
func (s *Service) ConfigureVersionLog(log *logger.Logger) {
	s.versionLog = log
}

func (s *Service) versionLogger() *logger.Logger {
	if s.versionLog != nil {
		return s.versionLog
	}
	// Not stored: recordings run concurrently, and this path is rare (no
	// logger configured and a failed write).
	return logger.New("api", "info")
}
