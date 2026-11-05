package monitors

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// Config version history (monitor_config_versions, migration 000119).
//
// Every write of a monitor's configuration records a version: CreateMonitor
// and UpdateMonitor call recordVersion, so the HTTP handlers, the importer,
// MCP (which dispatches through the router) and bulk edits inherit it.
// Discovery approval creates monitors through the repository inside its own
// transaction and calls RecordVersionTx beside CreateInTx. Any new path that
// creates or changes a monitor without going through the service must do the
// same.
//
// A version is a masked snapshot (models.MonitorConfigSnapshot) of the
// monitor after the write, including the junction tables (custom channels,
// locations, dependencies, group members). A write whose snapshot hashes like
// the latest version and that changed no secret is not recorded.
// changed_secret_fields compares the stored secret envelopes before and after
// the write, so a rotated password is a version even though its masked
// snapshot is unchanged.

// Version sources: the surface a change came through.
const (
	VersionSourceAPI       = "api"
	VersionSourceUI        = "ui"
	VersionSourceMCP       = "mcp"
	VersionSourceImport    = "import"
	VersionSourceApply     = "apply"
	VersionSourceRestore   = "restore"
	VersionSourceDiscovery = "discovery"
)

// Version actor types.
const (
	VersionActorUser   = "user"
	VersionActorAPIKey = "api_key"
	VersionActorSystem = "system"
)

// MaxVersionsPerMonitor is how many versions each monitor keeps; older ones
// are pruned when a new version is recorded.
const MaxVersionsPerMonitor = 100

// ErrVersionNotFound reports an unknown version (or one pruned by retention).
var ErrVersionNotFound = errors.New("monitor version not found")

type versionSourceKey struct{}
type versionRestoreKey struct{}
type versionBatchKey struct{}

// WithVersionSource names the source recorded for monitor writes made with
// ctx (import, apply, discovery). Without it the source follows the
// credential: MCP dispatch → mcp, an operator session → ui, else api.
func WithVersionSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, versionSourceKey{}, source)
}

// WithVersionRestore marks writes made with ctx as a restore of version v.
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

// versionMeta is everything about a version except the snapshot.
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
// part of a write. The HTTP handlers sync group members and dependencies
// after CreateMonitor/UpdateMonitor returns, and the importer adds group
// members in a second pass; recording at the service call would split one
// change into two versions. Flush records each touched monitor once.
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

// DeferVersions returns a context whose monitor writes queue their versions
// in the returned batch instead of recording them; call Flush once the write
// is complete (also on error paths: whatever was written is recorded). Inside
// an already deferred context it returns ctx and a nil batch, whose Flush is
// a no-op, so the outer batch records everything.
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

// Flush records a version for every monitor written since DeferVersions (or
// the last Flush). Safe on a nil batch and when called more than once.
func (b *VersionBatch) Flush(ctx context.Context) {
	if b == nil {
		return
	}
	b.mu.Lock()
	order, pending := b.order, b.pending
	b.order, b.pending = nil, map[uuid.UUID]*pendingVersion{}
	b.mu.Unlock()
	for _, monitorID := range order {
		entry := pending[monitorID]
		fields := make([]string, 0, len(entry.secrets))
		for field := range entry.secrets {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		entry.svc.recordVersionNow(ctx, entry.tenantID, monitorID, fields)
	}
}

// RecordVersion records the monitor's current configuration as a version
// (deferred when ctx carries a VersionBatch). Handlers call it after writes
// that bypass CreateMonitor/UpdateMonitor: group member and dependency edge
// routes, dependency syncs.
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

// recordVersionNow writes the version. A failure is logged, never returned:
// the monitor write it describes has already committed, and failing the
// request would invite a retry of a write that succeeded.
func (s *Service) recordVersionNow(ctx context.Context, tenantID, monitorID uuid.UUID, secretChanges []string) {
	if s.versions == nil {
		return
	}
	if _, err := s.versions.record(ctx, tenantID, monitorID, versionMetaFromContext(ctx, secretChanges)); err != nil {
		s.versionLogger().WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"monitor_id": monitorID,
		}).Error("Failed to record monitor config version")
	}
}

// Versions returns the version history store (nil when the service runs on
// a repository that is not Postgres-backed, as in unit tests).
func (s *Service) Versions() *VersionStore {
	return s.versions
}

// SecretFieldChanges names the secret config fields whose stored value
// differs between two stored (encrypted) configs: scalar fields by name, map
// fields as "field.key". Stored values are ciphertext envelopes that the
// write-only merge carries forward unchanged, so an unchanged secret compares
// equal and any set, replace or clear shows up. beforeType/before are empty
// on create.
func SecretFieldChanges(beforeType models.MonitorType, before json.RawMessage, afterType models.MonitorType, after json.RawMessage) []string {
	if !secrets.HasMonitorSecrets(string(beforeType)) && !secrets.HasMonitorSecrets(string(afterType)) {
		return nil
	}
	prev := decodeConfigMap(before)
	next := decodeConfigMap(after)
	changed := map[string]struct{}{}
	for _, field := range unionFields(secrets.MonitorSecretFields[string(beforeType)], secrets.MonitorSecretFields[string(afterType)]) {
		a, _ := prev[field].(string)
		b, _ := next[field].(string)
		if a != b {
			changed[field] = struct{}{}
		}
	}
	for _, field := range unionFields(secrets.MonitorSecretMapFields[string(beforeType)], secrets.MonitorSecretMapFields[string(afterType)]) {
		a, _ := prev[field].(map[string]any)
		b, _ := next[field].(map[string]any)
		for key, value := range a {
			// DeepEqual: a malformed stored map could hold a non-string
			// value, and comparing those with != would panic.
			if other, ok := b[key]; !ok || !reflect.DeepEqual(other, value) {
				changed[field+"."+key] = struct{}{}
			}
		}
		for key := range b {
			if _, ok := a[key]; !ok {
				changed[field+"."+key] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(changed))
	for field := range changed {
		out = append(out, field)
	}
	sort.Strings(out)
	return out
}

func unionFields(a, b []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(a)+len(b))
	for _, list := range [][]string{a, b} {
		for _, field := range list {
			if _, ok := seen[field]; !ok {
				seen[field] = struct{}{}
				out = append(out, field)
			}
		}
	}
	return out
}

func decodeConfigMap(raw json.RawMessage) map[string]any {
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out
}

// canonicalJSON re-encodes a JSON document with sorted object keys and the
// original number literals, so equal configs hash equal whatever their
// stored formatting.
func canonicalJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return json.RawMessage("{}"), nil
	}
	value, err := decodeJSONValue(raw)
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func decodeJSONValue(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

// snapshotHash is the SHA-256 (hex) of the snapshot's canonical encoding.
func snapshotHash(snapshot *models.MonitorConfigSnapshot) (string, error) {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// versionDB is what recording needs: a *sql.Tx satisfies it.
type versionDB interface {
	db.Querier
	db.Executer
}

// loadSnapshot reads a live monitor's configuration as a masked, canonical
// snapshot. found is false for a missing or deleted monitor.
func loadSnapshot(ctx context.Context, q db.Querier, tenantID, monitorID uuid.UUID) (*models.MonitorConfigSnapshot, bool, error) {
	snapshot := &models.MonitorConfigSnapshot{}
	var config []byte
	var tags []string
	err := q.QueryRowContext(ctx, `
		SELECT name, type, config, interval_seconds, timeout_seconds, tags,
			consecutive_failures_threshold, recovery_threshold, notification_mode,
			member_alert_rollup, dependency_suppression, location_quorum,
			COALESCE(escalation_policy_id::text, '')
		FROM monitors
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, monitorID, tenantID).Scan(
		&snapshot.Name, &snapshot.Type, &config, &snapshot.IntervalSeconds, &snapshot.TimeoutSeconds, pq.Array(&tags),
		&snapshot.ConsecutiveFailuresThreshold, &snapshot.RecoveryThreshold, &snapshot.NotificationMode,
		&snapshot.MemberAlertRollup, &snapshot.DependencySuppression, &snapshot.LocationQuorum,
		&snapshot.EscalationPolicyID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("load monitor for version: %w", err)
	}
	masked, err := secrets.MaskMonitorConfig(string(snapshot.Type), config)
	if err != nil {
		return nil, false, fmt.Errorf("mask monitor config for version: %w", err)
	}
	if snapshot.Config, err = canonicalJSON(masked); err != nil {
		return nil, false, fmt.Errorf("canonicalize monitor config for version: %w", err)
	}
	snapshot.Tags = tags

	rows, err := q.QueryContext(ctx, `
		SELECT mc.channel_id, mc.delay_seconds, mc.notify_after_ack
		FROM monitor_channels mc
		JOIN alert_channels ac ON ac.id = mc.channel_id
		WHERE mc.monitor_id = $1
	`, monitorID)
	if err != nil {
		return nil, false, fmt.Errorf("load monitor channels for version: %w", err)
	}
	for rows.Next() {
		var channel models.MonitorSnapshotChannel
		if err := rows.Scan(&channel.ChannelID, &channel.DelaySeconds, &channel.NotifyAfterAck); err != nil {
			rows.Close()
			return nil, false, fmt.Errorf("scan monitor channel for version: %w", err)
		}
		snapshot.NotificationChannels = append(snapshot.NotificationChannels, channel)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	idQueries := []struct {
		target *[]string
		query  string
	}{
		{&snapshot.LocationIDs, `
			SELECT ml.location_id::text FROM monitor_locations ml
			JOIN locations l ON l.id = ml.location_id AND l.deleted_at IS NULL
			WHERE ml.monitor_id = $1`},
		{&snapshot.DependsOnIDs, `
			SELECT md.depends_on_id::text FROM monitor_dependencies md
			JOIN monitors m ON m.id = md.depends_on_id AND m.deleted_at IS NULL
			WHERE md.monitor_id = $1`},
		{&snapshot.MemberIDs, `
			SELECT mg.monitor_id::text FROM monitor_groups mg
			JOIN monitors m ON m.id = mg.monitor_id AND m.deleted_at IS NULL
			WHERE mg.group_id = $1`},
	}
	for _, iq := range idQueries {
		ids, err := queryStrings(ctx, q, iq.query, monitorID)
		if err != nil {
			return nil, false, fmt.Errorf("load monitor relations for version: %w", err)
		}
		*iq.target = ids
	}
	normalizeSnapshot(snapshot)
	return snapshot, true, nil
}

func queryStrings(ctx context.Context, q db.Querier, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

// normalizeSnapshot sorts the set-valued fields and turns nil slices into
// empty ones, so the encoding (and hash) depends only on content.
func normalizeSnapshot(snapshot *models.MonitorConfigSnapshot) {
	if snapshot.Tags == nil {
		snapshot.Tags = []string{}
	}
	if snapshot.NotificationChannels == nil {
		snapshot.NotificationChannels = []models.MonitorSnapshotChannel{}
	}
	sort.Slice(snapshot.NotificationChannels, func(i, j int) bool {
		return snapshot.NotificationChannels[i].ChannelID < snapshot.NotificationChannels[j].ChannelID
	})
	for _, ids := range []*[]string{&snapshot.LocationIDs, &snapshot.DependsOnIDs, &snapshot.MemberIDs} {
		if *ids == nil {
			*ids = []string{}
		}
		sort.Strings(*ids)
	}
	if len(snapshot.Config) == 0 {
		snapshot.Config = json.RawMessage("{}")
	}
}

// RecordVersionTx records a version inside the caller's transaction, for
// paths that create or change monitors through the repository or plain SQL
// instead of the service (discovery approval, beside CreateInTx; converting
// routes to an escalation policy with assign). The source is set explicitly,
// or "" to follow ctx like the service path (WithVersionSource, else the
// credential); the actor comes from ctx. Unlike the service path it returns
// its error, so the caller's transaction rolls back with it.
func RecordVersionTx(ctx context.Context, tx *sql.Tx, tenantID, monitorID uuid.UUID, source string) error {
	_, err := recordVersionTx(ctx, tx, tenantID, monitorID, versionMetaFromContext(WithVersionSource(ctx, source), nil))
	return err
}

// recordVersionTx snapshots the monitor and inserts the next version unless
// it would duplicate the latest one, then prunes beyond the retention limit.
// It returns the recorded version, or 0 when nothing was recorded.
func recordVersionTx(ctx context.Context, tx versionDB, tenantID, monitorID uuid.UUID, meta versionMeta) (int, error) {
	// Serialize version numbering per monitor for the rest of the
	// transaction. A transaction-scoped advisory lock rather than a lock on
	// the monitors row, which the scheduler and ingest update constantly.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('monitor_config_versions:' || $1::text, 0))`, monitorID); err != nil {
		return 0, fmt.Errorf("lock monitor versions: %w", err)
	}
	snapshot, found, err := loadSnapshot(ctx, tx, tenantID, monitorID)
	if err != nil || !found {
		return 0, err
	}
	hash, err := snapshotHash(snapshot)
	if err != nil {
		return 0, err
	}

	latest, latestHash := 0, ""
	err = tx.QueryRowContext(ctx, `
		SELECT version, snapshot_hash FROM monitor_config_versions
		WHERE monitor_id = $1 ORDER BY version DESC LIMIT 1
	`, monitorID).Scan(&latest, &latestHash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("load latest monitor version: %w", err)
	}
	if latest > 0 && latestHash == hash && len(meta.secretChanges) == 0 {
		return 0, nil
	}

	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return 0, err
	}
	secretChanges := meta.secretChanges
	if secretChanges == nil {
		secretChanges = []string{}
	}
	next := latest + 1
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO monitor_config_versions
			(tenant_id, monitor_id, version, snapshot, snapshot_hash, changed_secret_fields,
			 actor_type, actor_id, actor_label, source, restored_from)
		VALUES ($1, $2, $3, $4, $5, $6, $7::text, $8::uuid,
			COALESCE(CASE $7::text
				WHEN 'user' THEN (SELECT username FROM admin_users WHERE id = $8::uuid)
				WHEN 'api_key' THEN (SELECT name FROM api_keys WHERE id = $8::uuid)
			END, ''),
			$9, $10)
	`, tenantID, monitorID, next, encoded, hash, pq.Array(secretChanges),
		meta.actor.typ, meta.actor.id, meta.source, meta.restoredFrom); err != nil {
		return 0, fmt.Errorf("insert monitor version: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM monitor_config_versions WHERE monitor_id = $1 AND version <= $2
	`, monitorID, next-MaxVersionsPerMonitor); err != nil {
		return 0, fmt.Errorf("prune monitor versions: %w", err)
	}
	return next, nil
}

// VersionStore reads and writes monitor config version history.
type VersionStore struct {
	db db.DB
}

// NewVersionStore creates a version store on the database.
func NewVersionStore(database db.DB) *VersionStore {
	return &VersionStore{db: database}
}

func (v *VersionStore) record(ctx context.Context, tenantID, monitorID uuid.UUID, meta versionMeta) (int, error) {
	tx, err := v.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	version, err := recordVersionTx(ctx, tx, tenantID, monitorID, meta)
	if err != nil {
		return 0, err
	}
	return version, tx.Commit()
}

type storedVersion struct {
	summary  models.MonitorVersionSummary
	snapshot models.MonitorConfigSnapshot
}

const versionColumns = `version, source, actor_type, actor_id, actor_label, restored_from,
	changed_secret_fields, snapshot_hash, created_at, snapshot`

func scanVersion(scan func(dest ...any) error) (*storedVersion, error) {
	var out storedVersion
	var raw []byte
	var restored sql.NullInt64
	var secretFields []string
	if err := scan(&out.summary.Version, &out.summary.Source, &out.summary.ActorType, &out.summary.ActorID,
		&out.summary.ActorLabel, &restored, pq.Array(&secretFields), &out.summary.SnapshotHash,
		&out.summary.CreatedAt, &raw); err != nil {
		return nil, err
	}
	if restored.Valid {
		from := int(restored.Int64)
		out.summary.RestoredFrom = &from
	}
	if secretFields == nil {
		secretFields = []string{}
	}
	out.summary.ChangedSecretFields = secretFields
	if err := json.Unmarshal(raw, &out.snapshot); err != nil {
		return nil, fmt.Errorf("decode monitor version snapshot: %w", err)
	}
	normalizeSnapshot(&out.snapshot)
	return &out, nil
}

func (v *VersionStore) loadAll(ctx context.Context, tenantID, monitorID uuid.UUID) ([]*storedVersion, error) {
	rows, err := v.db.QueryContext(ctx, `
		SELECT `+versionColumns+` FROM monitor_config_versions
		WHERE tenant_id = $1 AND monitor_id = $2
		ORDER BY version
	`, tenantID, monitorID)
	if err != nil {
		return nil, fmt.Errorf("list monitor versions: %w", err)
	}
	defer rows.Close()
	var out []*storedVersion
	for rows.Next() {
		version, err := scanVersion(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, version)
	}
	return out, rows.Err()
}

func (v *VersionStore) load(ctx context.Context, tenantID, monitorID uuid.UUID, version int) (*storedVersion, error) {
	row := v.db.QueryRowContext(ctx, `
		SELECT `+versionColumns+` FROM monitor_config_versions
		WHERE tenant_id = $1 AND monitor_id = $2 AND version = $3
	`, tenantID, monitorID, version)
	out, err := scanVersion(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrVersionNotFound
	}
	return out, err
}

// List returns a monitor's retained versions, newest first, each with the
// snapshot paths it changed relative to the previous retained version.
func (v *VersionStore) List(ctx context.Context, tenantID, monitorID uuid.UUID) (*models.MonitorVersionListResponse, error) {
	all, err := v.loadAll(ctx, tenantID, monitorID)
	if err != nil {
		return nil, err
	}
	resp := &models.MonitorVersionListResponse{
		Items:          make([]models.MonitorVersionSummary, 0, len(all)),
		RetentionLimit: MaxVersionsPerMonitor,
	}
	if len(all) > 0 {
		started := all[0].summary.CreatedAt
		resp.HistoryStartsAt = &started
		resp.Truncated = all[0].summary.Version > 1
	}
	for i := len(all) - 1; i >= 0; i-- {
		summary := all[i].summary
		summary.ChangedFields = []string{}
		if i > 0 {
			changes, err := DiffSnapshots(&all[i-1].snapshot, &all[i].snapshot)
			if err != nil {
				return nil, err
			}
			for _, change := range changes {
				summary.ChangedFields = append(summary.ChangedFields, change.Path)
			}
		}
		resp.Items = append(resp.Items, summary)
	}
	return resp, nil
}

// Get returns one version with its snapshot.
func (v *VersionStore) Get(ctx context.Context, tenantID, monitorID uuid.UUID, version int) (*models.MonitorVersion, error) {
	stored, err := v.load(ctx, tenantID, monitorID, version)
	if err != nil {
		return nil, err
	}
	summary := stored.summary
	summary.ChangedFields = []string{}
	if previous, err := v.previous(ctx, tenantID, monitorID, version); err != nil {
		return nil, err
	} else if previous != nil {
		changes, err := DiffSnapshots(&previous.snapshot, &stored.snapshot)
		if err != nil {
			return nil, err
		}
		for _, change := range changes {
			summary.ChangedFields = append(summary.ChangedFields, change.Path)
		}
	}
	return &models.MonitorVersion{MonitorVersionSummary: summary, Snapshot: stored.snapshot}, nil
}

func (v *VersionStore) previous(ctx context.Context, tenantID, monitorID uuid.UUID, version int) (*storedVersion, error) {
	row := v.db.QueryRowContext(ctx, `
		SELECT `+versionColumns+` FROM monitor_config_versions
		WHERE tenant_id = $1 AND monitor_id = $2 AND version < $3
		ORDER BY version DESC LIMIT 1
	`, tenantID, monitorID, version)
	out, err := scanVersion(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return out, err
}

// AgainstCurrent is the diff `against` value naming the live monitor.
const AgainstCurrent = "current"

// ErrInvalidAgainst reports a diff `against` that is neither a version
// number nor "current".
var ErrInvalidAgainst = errors.New(`against must be a version number or "current"`)

// Diff returns the changes that lead from `against` to version. An empty
// against means the previous retained version; "current" means the live
// monitor (what a restore of version would change, read the other way).
func (v *VersionStore) Diff(ctx context.Context, tenantID, monitorID uuid.UUID, version int, against string) (*models.MonitorVersionDiffResponse, error) {
	target, err := v.load(ctx, tenantID, monitorID, version)
	if err != nil {
		return nil, err
	}
	resp := &models.MonitorVersionDiffResponse{Version: version}
	var base *models.MonitorConfigSnapshot
	switch against {
	case "":
		previous, err := v.previous(ctx, tenantID, monitorID, version)
		if err != nil {
			return nil, err
		}
		if previous != nil {
			base = &previous.snapshot
			resp.Against = strconv.Itoa(previous.summary.Version)
		}
	case AgainstCurrent:
		current, found, err := loadSnapshot(ctx, v.db, tenantID, monitorID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, ErrVersionNotFound
		}
		base = current
		resp.Against = AgainstCurrent
	default:
		n, err := strconv.Atoi(against)
		if err != nil || n < 1 {
			return nil, ErrInvalidAgainst
		}
		other, err := v.load(ctx, tenantID, monitorID, n)
		if err != nil {
			return nil, err
		}
		base = &other.snapshot
		resp.Against = strconv.Itoa(n)
	}
	// Without a base (the oldest retained version) every field reads as added.
	if resp.Changes, err = diffAll(base, &target.snapshot); err != nil {
		return nil, err
	}
	return resp, nil
}

// LiveReferences drops the channels, locations, dependencies, members and
// escalation policy a snapshot names that no longer exist in the tenant, so
// a restore can apply the rest. It returns the trimmed copy and the dropped
// references ("channel:<id>", "location:<id>", "dependency:<id>",
// "member:<id>", "escalation_policy:<id>").
//
// A deleted policy falls back to notification_mode 'default' rather than
// refusing the restore: like a dropped channel it is reported, and the
// workspace's default routing still reaches someone, where refusing would
// make every version recorded under that policy unrestorable.
func (v *VersionStore) LiveReferences(ctx context.Context, tenantID uuid.UUID, snapshot models.MonitorConfigSnapshot) (models.MonitorConfigSnapshot, []string, error) {
	dropped := []string{}
	if snapshot.NotificationMode == "policy" && snapshot.EscalationPolicyID != "" {
		policies, err := queryStrings(ctx, v.db, `SELECT id::text FROM escalation_policies WHERE tenant_id = $1 AND id::text = $2`,
			tenantID, snapshot.EscalationPolicyID)
		if err != nil {
			return snapshot, nil, err
		}
		if len(policies) == 0 {
			dropped = append(dropped, "escalation_policy:"+snapshot.EscalationPolicyID)
			snapshot.NotificationMode = "default"
			snapshot.EscalationPolicyID = ""
		}
	}
	live := func(query string, ids []string) (map[string]bool, error) {
		out := map[string]bool{}
		if len(ids) == 0 {
			return out, nil
		}
		found, err := queryStrings(ctx, v.db, query, tenantID, pq.Array(ids))
		if err != nil {
			return nil, err
		}
		for _, id := range found {
			out[id] = true
		}
		return out, nil
	}
	channelIDs := make([]string, 0, len(snapshot.NotificationChannels))
	for _, channel := range snapshot.NotificationChannels {
		channelIDs = append(channelIDs, channel.ChannelID)
	}
	liveChannels, err := live(`SELECT id::text FROM alert_channels WHERE tenant_id = $1 AND id::text = ANY($2)`, channelIDs)
	if err != nil {
		return snapshot, nil, err
	}
	channels := []models.MonitorSnapshotChannel{}
	for _, channel := range snapshot.NotificationChannels {
		if liveChannels[channel.ChannelID] {
			channels = append(channels, channel)
		} else {
			dropped = append(dropped, "channel:"+channel.ChannelID)
		}
	}
	snapshot.NotificationChannels = channels

	filter := func(kind, query string, ids []string) ([]string, error) {
		alive, err := live(query, ids)
		if err != nil {
			return nil, err
		}
		kept := []string{}
		for _, id := range ids {
			if alive[id] {
				kept = append(kept, id)
			} else {
				dropped = append(dropped, kind+":"+id)
			}
		}
		return kept, nil
	}
	const liveMonitors = `SELECT id::text FROM monitors WHERE tenant_id = $1 AND id::text = ANY($2) AND deleted_at IS NULL`
	if snapshot.LocationIDs, err = filter("location", `SELECT id::text FROM locations WHERE tenant_id = $1 AND id::text = ANY($2) AND deleted_at IS NULL`, snapshot.LocationIDs); err != nil {
		return snapshot, nil, err
	}
	if snapshot.DependsOnIDs, err = filter("dependency", liveMonitors, snapshot.DependsOnIDs); err != nil {
		return snapshot, nil, err
	}
	if snapshot.MemberIDs, err = filter("member", liveMonitors, snapshot.MemberIDs); err != nil {
		return snapshot, nil, err
	}
	return snapshot, dropped, nil
}

// RestoreRequest turns a snapshot into the update that re-applies it. The
// config goes in masked, so the write-only merge keeps every secret the
// monitor holds now ("***" keeps, an absent field keeps): a restore never
// brings back a secret value, it cannot. currentConfig is the live
// monitor's config (masked or not, only its keys are read): secret map keys
// added since the version are submitted as "***" too, because a present
// secret map is edited per key and would otherwise delete them. Group
// members travel as config.monitor_ids, which the handler syncs into
// monitor_groups like any group update. The type is sent only when it
// differs.
func RestoreRequest(snapshot models.MonitorConfigSnapshot, currentType models.MonitorType, currentConfig json.RawMessage) (*models.UpdateMonitorRequest, error) {
	config := snapshot.Config
	if snapshot.Type == currentType {
		var err error
		if config, err = keepCurrentSecretKeys(string(currentType), config, currentConfig); err != nil {
			return nil, err
		}
	}
	if snapshot.Type == models.MonitorTypeGroup {
		value, err := decodeJSONValue(config)
		if err != nil {
			return nil, fmt.Errorf("decode group config: %w", err)
		}
		fields, _ := value.(map[string]any)
		if fields == nil {
			fields = map[string]any{}
		}
		fields["monitor_ids"] = snapshot.MemberIDs
		if config, err = json.Marshal(fields); err != nil {
			return nil, err
		}
	}
	name := snapshot.Name
	interval := snapshot.IntervalSeconds
	timeout := snapshot.TimeoutSeconds
	tags := append([]string{}, snapshot.Tags...)
	threshold := snapshot.ConsecutiveFailuresThreshold
	recovery := snapshot.RecoveryThreshold
	mode := snapshot.NotificationMode
	rollup := snapshot.MemberAlertRollup
	suppression := snapshot.DependencySuppression
	locations := append([]string{}, snapshot.LocationIDs...)
	quorum := snapshot.LocationQuorum
	req := &models.UpdateMonitorRequest{
		Name:                         &name,
		Config:                       config,
		IntervalSeconds:              &interval,
		TimeoutSeconds:               &timeout,
		Tags:                         &tags,
		ConsecutiveFailuresThreshold: &threshold,
		NotificationMode:             &mode,
		MemberAlertRollup:            &rollup,
		DependencySuppression:        &suppression,
		LocationIDs:                  &locations,
		LocationQuorum:               &quorum,
	}
	if recovery > 0 {
		req.RecoveryThreshold = &recovery
	}
	if snapshot.Type != currentType {
		typ := snapshot.Type
		req.Type = &typ
	}
	if mode == "policy" {
		policy := snapshot.EscalationPolicyID
		req.EscalationPolicyID = &policy
	}
	if mode == "custom" {
		// Non-nil even when empty: the update replaces the route set.
		req.NotificationChannels = make([]models.MonitorChannelAssignment, 0, len(snapshot.NotificationChannels))
		for _, channel := range snapshot.NotificationChannels {
			req.NotificationChannels = append(req.NotificationChannels, models.MonitorChannelAssignment{
				ChannelID:      channel.ChannelID,
				DelaySeconds:   channel.DelaySeconds,
				NotifyAfterAck: channel.NotifyAfterAck,
			})
		}
	}
	if snapshot.Type != models.MonitorTypeGroup {
		deps := append([]string{}, snapshot.DependsOnIDs...)
		req.DependsOnIDs = &deps
	}
	return req, nil
}

// keepCurrentSecretKeys adds to each secret map field the snapshot carries
// (secrets.MonitorSecretMapFields) the keys the live config holds and the
// snapshot lacks, valued "***", so the merge keeps them. A map field absent
// from the snapshot needs nothing: an absent map is kept whole.
func keepCurrentSecretKeys(monitorType string, config, currentConfig json.RawMessage) (json.RawMessage, error) {
	mapFields := secrets.MonitorSecretMapFields[monitorType]
	if len(mapFields) == 0 || len(currentConfig) == 0 {
		return config, nil
	}
	current := decodeConfigMap(currentConfig)
	value, err := decodeJSONValue(config)
	if err != nil {
		return nil, fmt.Errorf("decode version config: %w", err)
	}
	fields, _ := value.(map[string]any)
	if fields == nil {
		return config, nil
	}
	changed := false
	for _, field := range mapFields {
		restored, ok := fields[field].(map[string]any)
		if !ok {
			continue
		}
		live, _ := current[field].(map[string]any)
	liveKeys:
		for key := range live {
			for have := range restored {
				// The merge matches keys case-insensitively (header names).
				if strings.EqualFold(have, key) {
					continue liveKeys
				}
			}
			restored[key] = secrets.MaskedSecret
			changed = true
		}
	}
	if !changed {
		return config, nil
	}
	return json.Marshal(fields)
}

// DiffSnapshots lists the paths that differ between two snapshots: object
// fields recurse (config.url), arrays and scalars compare whole.
func DiffSnapshots(before, after *models.MonitorConfigSnapshot) ([]models.MonitorVersionChange, error) {
	return diffAll(before, after)
}

func diffAll(before, after *models.MonitorConfigSnapshot) ([]models.MonitorVersionChange, error) {
	a := map[string]any{}
	if before != nil {
		var err error
		if a, err = snapshotMap(before); err != nil {
			return nil, err
		}
	}
	b, err := snapshotMap(after)
	if err != nil {
		return nil, err
	}
	changes := []models.MonitorVersionChange{}
	diffObjects("", a, b, &changes)
	return changes, nil
}

func snapshotMap(snapshot *models.MonitorConfigSnapshot) (map[string]any, error) {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	value, err := decodeJSONValue(encoded)
	if err != nil {
		return nil, err
	}
	out, _ := value.(map[string]any)
	return out, nil
}

func diffObjects(prefix string, a, b map[string]any, out *[]models.MonitorVersionChange) {
	keys := make([]string, 0, len(a)+len(b))
	for key := range a {
		keys = append(keys, key)
	}
	for key := range b {
		if _, ok := a[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		va, inA := a[key]
		vb, inB := b[key]
		switch {
		case !inA:
			*out = append(*out, models.MonitorVersionChange{Path: path, After: vb})
		case !inB:
			*out = append(*out, models.MonitorVersionChange{Path: path, Before: va})
		default:
			ma, okA := va.(map[string]any)
			mb, okB := vb.(map[string]any)
			if okA && okB {
				diffObjects(path, ma, mb, out)
			} else if !reflect.DeepEqual(va, vb) {
				*out = append(*out, models.MonitorVersionChange{Path: path, Before: va, After: vb})
			}
		}
	}
}
