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
	"sort"
	"strconv"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/pagination"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// versionDB is what recording needs; a *sql.Tx satisfies it.
type versionDB interface {
	db.Querier
	db.Executer
}

// loadSnapshot reads a live monitor's configuration as a masked, canonical
// snapshot. found is false for a missing, deleted or foreign monitor. Every
// query is tenant-scoped, the junction tables through the row they point at.
func loadSnapshot(ctx context.Context, q db.Querier, tenantID, monitorID uuid.UUID) (*models.MonitorConfigSnapshot, bool, error) {
	snapshot := &models.MonitorConfigSnapshot{}
	var config []byte
	var tags []string
	err := q.QueryRowContext(ctx, `
		SELECT name, type, config, interval_seconds, timeout_seconds, tags,
			consecutive_failures_threshold, notification_mode,
			member_alert_rollup, dependency_suppression, location_quorum
		FROM monitors
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, monitorID, tenantID).Scan(
		&snapshot.Name, &snapshot.Type, &config, &snapshot.IntervalSeconds, &snapshot.TimeoutSeconds, pq.Array(&tags),
		&snapshot.ConsecutiveFailuresThreshold, &snapshot.NotificationMode,
		&snapshot.MemberAlertRollup, &snapshot.DependencySuppression, &snapshot.LocationQuorum,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("load monitor for version: %w", err)
	}
	// Masking goes through the secret registry, so a snapshot holds no
	// secret (plaintext or ciphertext) for any type, present or future.
	masked, err := secrets.MaskMonitorConfig(string(snapshot.Type), config)
	if err != nil {
		return nil, false, fmt.Errorf("mask monitor config for version: %w", err)
	}
	if snapshot.Config, err = canonicalJSON(masked); err != nil {
		return nil, false, fmt.Errorf("canonicalize monitor config for version: %w", err)
	}
	snapshot.Tags = tags

	rows, err := q.QueryContext(ctx, `
		SELECT mc.channel_id::text, mc.delay_seconds
		FROM monitor_channels mc
		JOIN alert_channels ac ON ac.id = mc.channel_id AND ac.tenant_id = $2
		WHERE mc.monitor_id = $1
	`, monitorID, tenantID)
	if err != nil {
		return nil, false, fmt.Errorf("load monitor channels for version: %w", err)
	}
	for rows.Next() {
		var channel models.MonitorSnapshotChannel
		if err := rows.Scan(&channel.ChannelID, &channel.DelaySeconds); err != nil {
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
			JOIN locations l ON l.id = ml.location_id AND l.tenant_id = $2 AND l.deleted_at IS NULL
			WHERE ml.monitor_id = $1`},
		{&snapshot.DependsOnIDs, `
			SELECT md.depends_on_id::text FROM monitor_dependencies md
			JOIN monitors m ON m.id = md.depends_on_id AND m.tenant_id = $2 AND m.deleted_at IS NULL
			WHERE md.monitor_id = $1`},
		{&snapshot.MemberIDs, `
			SELECT mg.monitor_id::text FROM monitor_groups mg
			JOIN monitors m ON m.id = mg.monitor_id AND m.tenant_id = $2 AND m.deleted_at IS NULL
			WHERE mg.group_id = $1`},
	}
	for _, iq := range idQueries {
		ids, err := queryStrings(ctx, q, iq.query, monitorID, tenantID)
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
// empty ones, so the encoding (and hash) depends only on content. Tags keep
// their order: it is what the operator saved.
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

// canonicalJSON re-encodes a JSON document with sorted object keys and the
// original number literals, so equal configs hash equal whatever their
// stored formatting.
func canonicalJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
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

// RecordVersionTx records a version inside the caller's transaction, for
// paths that create or change monitors through the repository instead of the
// service (discovery approval, beside CreateInTx). The source is set
// explicitly; the actor comes from ctx. Unlike the service path it returns
// its error, so the caller's transaction rolls back with it.
func RecordVersionTx(ctx context.Context, tx *sql.Tx, tenantID, monitorID uuid.UUID, source string) error {
	_, err := recordVersionTx(ctx, tx, tenantID, monitorID, versionMetaFromContext(WithVersionSource(ctx, source), nil))
	return err
}

// recordVersionTx snapshots the monitor and inserts the next version unless
// it would repeat the latest one, then prunes beyond MaxVersionsPerMonitor.
// It returns the recorded version, or 0 when nothing was recorded.
func recordVersionTx(ctx context.Context, tx versionDB, tenantID, monitorID uuid.UUID, meta versionMeta) (int, error) {
	// Serialize version numbering per monitor for the rest of the
	// transaction. A transaction-scoped advisory lock rather than a lock on
	// the monitors row, which the scheduler and result ingest update
	// constantly.
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
	var latestRaw []byte
	err = tx.QueryRowContext(ctx, `
		SELECT version, snapshot_hash, snapshot FROM monitor_config_versions
		WHERE tenant_id = $1 AND monitor_id = $2 ORDER BY version DESC LIMIT 1
	`, tenantID, monitorID).Scan(&latest, &latestHash, &latestRaw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("load latest monitor version: %w", err)
	}
	if latest > 0 && latestHash == hash && len(meta.secretChanges) == 0 {
		return 0, nil
	}

	changedFields := []string{}
	if latest > 0 {
		previous, err := decodeStoredSnapshot(latestRaw)
		if err != nil {
			return 0, err
		}
		changes, err := diffSnapshots(previous, snapshot)
		if err != nil {
			return 0, err
		}
		for _, change := range changes {
			changedFields = append(changedFields, change.Path)
		}
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
			(tenant_id, monitor_id, version, snapshot, snapshot_hash, changed_fields, changed_secret_fields,
			 actor_type, actor_id, actor_label, source, restored_from)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::text, $9::uuid,
			COALESCE(CASE $8::text
				WHEN 'user' THEN (SELECT username FROM admin_users WHERE id = $9::uuid)
				WHEN 'api_key' THEN (SELECT name FROM api_keys WHERE id = $9::uuid)
			END, ''),
			$10, $11)
	`, tenantID, monitorID, next, encoded, hash, pq.Array(changedFields), pq.Array(secretChanges),
		meta.actor.typ, meta.actor.id, meta.source, meta.restoredFrom); err != nil {
		return 0, fmt.Errorf("insert monitor version: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM monitor_config_versions
		WHERE tenant_id = $1 AND monitor_id = $2 AND version <= $3
	`, tenantID, monitorID, next-MaxVersionsPerMonitor); err != nil {
		return 0, fmt.Errorf("prune monitor versions: %w", err)
	}
	return next, nil
}

// VersionStore reads and writes monitor configuration history. Every query
// is scoped to a tenant.
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

const versionSummaryColumns = `version, source, actor_type, actor_id, actor_label, restored_from,
	changed_fields, changed_secret_fields, created_at`

func scanVersionSummary(scan func(dest ...any) error, extra ...any) (models.MonitorVersionSummary, error) {
	var out models.MonitorVersionSummary
	var restored sql.NullInt64
	var actorID uuid.NullUUID
	var changed, secretFields []string
	dest := append([]any{&out.Version, &out.Source, &out.ActorType, &actorID, &out.ActorLabel, &restored,
		pq.Array(&changed), pq.Array(&secretFields), &out.CreatedAt}, extra...)
	if err := scan(dest...); err != nil {
		return out, err
	}
	if actorID.Valid {
		id := actorID.UUID
		out.ActorID = &id
	}
	if restored.Valid {
		from := int(restored.Int64)
		out.RestoredFrom = &from
	}
	if changed == nil {
		changed = []string{}
	}
	if secretFields == nil {
		secretFields = []string{}
	}
	out.ChangedFields = changed
	out.ChangedSecretFields = secretFields
	return out, nil
}

// List returns a page of a monitor's kept versions, newest first. A monitor
// of another tenant reads as one without history.
func (v *VersionStore) List(ctx context.Context, tenantID, monitorID uuid.UUID, page, pageSize int) (*models.MonitorVersionListResponse, error) {
	page, pageSize = pagination.Normalize(page, pageSize, pagination.DefaultSize, pagination.MaxSize)
	resp := &models.MonitorVersionListResponse{
		Items:          []models.MonitorVersionSummary{},
		Page:           page,
		PageSize:       pageSize,
		RetentionLimit: MaxVersionsPerMonitor,
	}
	var oldest sql.NullInt64
	var startedAt sql.NullTime
	if err := v.db.QueryRowContext(ctx, `
		SELECT count(*), min(version), min(created_at) FROM monitor_config_versions
		WHERE tenant_id = $1 AND monitor_id = $2
	`, tenantID, monitorID).Scan(&resp.Total, &oldest, &startedAt); err != nil {
		return nil, fmt.Errorf("count monitor versions: %w", err)
	}
	if startedAt.Valid {
		started := startedAt.Time
		resp.HistoryStartsAt = &started
	}
	resp.Truncated = oldest.Valid && oldest.Int64 > 1

	rows, err := v.db.QueryContext(ctx, `
		SELECT `+versionSummaryColumns+` FROM monitor_config_versions
		WHERE tenant_id = $1 AND monitor_id = $2
		ORDER BY version DESC
		LIMIT $3 OFFSET $4
	`, tenantID, monitorID, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, fmt.Errorf("list monitor versions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		summary, err := scanVersionSummary(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan monitor version: %w", err)
		}
		resp.Items = append(resp.Items, summary)
	}
	return resp, rows.Err()
}

// Get returns one version with its snapshot.
func (v *VersionStore) Get(ctx context.Context, tenantID, monitorID uuid.UUID, version int) (*models.MonitorVersion, error) {
	var raw []byte
	summary, err := scanVersionSummary(v.db.QueryRowContext(ctx, `
		SELECT `+versionSummaryColumns+`, snapshot FROM monitor_config_versions
		WHERE tenant_id = $1 AND monitor_id = $2 AND version = $3
	`, tenantID, monitorID, version).Scan, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrVersionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load monitor version: %w", err)
	}
	snapshot, err := decodeStoredSnapshot(raw)
	if err != nil {
		return nil, err
	}
	return &models.MonitorVersion{MonitorVersionSummary: summary, Snapshot: *snapshot}, nil
}

// decodeStoredSnapshot reads a snapshot back from its JSONB column. JSONB
// reorders object keys, so the config is re-canonicalized to compare
// byte for byte with a freshly loaded one.
func decodeStoredSnapshot(raw []byte) (*models.MonitorConfigSnapshot, error) {
	var snapshot models.MonitorConfigSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil, fmt.Errorf("decode monitor version snapshot: %w", err)
	}
	config, err := canonicalJSON(snapshot.Config)
	if err != nil {
		return nil, fmt.Errorf("canonicalize monitor version config: %w", err)
	}
	snapshot.Config = config
	normalizeSnapshot(&snapshot)
	return &snapshot, nil
}

// previousVersion is the newest kept version below version, 0 without one.
func (v *VersionStore) previousVersion(ctx context.Context, tenantID, monitorID uuid.UUID, version int) (int, error) {
	var previous sql.NullInt64
	if err := v.db.QueryRowContext(ctx, `
		SELECT max(version) FROM monitor_config_versions
		WHERE tenant_id = $1 AND monitor_id = $2 AND version < $3
	`, tenantID, monitorID, version).Scan(&previous); err != nil {
		return 0, fmt.Errorf("load previous monitor version: %w", err)
	}
	return int(previous.Int64), nil
}

// secretChangesBetween names the secret fields any version in (from, to]
// changed: the secrets that differ between version from and version to, or
// the live monitor when to is 0, which the masked snapshots cannot show.
func (v *VersionStore) secretChangesBetween(ctx context.Context, tenantID, monitorID uuid.UUID, from, to int) ([]string, error) {
	return queryStrings(ctx, v.db, `
		SELECT DISTINCT field FROM monitor_config_versions, unnest(changed_secret_fields) AS field
		WHERE tenant_id = $1 AND monitor_id = $2 AND version > $3 AND ($4 = 0 OR version <= $4)
		ORDER BY field
	`, tenantID, monitorID, from, to)
}

// CurrentSnapshot reads the live monitor as a snapshot (ErrVersionNotFound
// when it does not exist in the tenant).
func (v *VersionStore) CurrentSnapshot(ctx context.Context, tenantID, monitorID uuid.UUID) (*models.MonitorConfigSnapshot, error) {
	snapshot, found, err := loadSnapshot(ctx, v.db, tenantID, monitorID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrVersionNotFound
	}
	return snapshot, nil
}

// AgainstCurrent is the diff `against` value naming the live monitor.
const AgainstCurrent = "current"

// ErrInvalidAgainst reports a diff `against` that is neither a version
// number nor "current".
var ErrInvalidAgainst = errors.New(`against must be a version number or "current"`)

// Diff returns the changes that lead from `against` to version. An empty
// against means the previous kept version; "current" means the live monitor,
// so the result is what a restore of version would change. Secret changes
// appear as Secret entries without values.
func (v *VersionStore) Diff(ctx context.Context, tenantID, monitorID uuid.UUID, version int, against string) (*models.MonitorVersionDiffResponse, error) {
	target, err := v.Get(ctx, tenantID, monitorID, version)
	if err != nil {
		return nil, err
	}
	resp := &models.MonitorVersionDiffResponse{Version: version}
	var base *models.MonitorConfigSnapshot
	var secretChanges []string
	switch against {
	case "":
		previous, err := v.previousVersion(ctx, tenantID, monitorID, version)
		if err != nil {
			return nil, err
		}
		if previous > 0 {
			other, err := v.Get(ctx, tenantID, monitorID, previous)
			if err != nil {
				return nil, err
			}
			base = &other.Snapshot
			resp.Against = strconv.Itoa(previous)
			secretChanges = target.ChangedSecretFields
		}
	case AgainstCurrent:
		if base, err = v.CurrentSnapshot(ctx, tenantID, monitorID); err != nil {
			return nil, err
		}
		resp.Against = AgainstCurrent
		if secretChanges, err = v.secretChangesBetween(ctx, tenantID, monitorID, version, 0); err != nil {
			return nil, err
		}
	default:
		n, err := strconv.Atoi(against)
		if err != nil || n < 1 {
			return nil, ErrInvalidAgainst
		}
		other, err := v.Get(ctx, tenantID, monitorID, n)
		if err != nil {
			return nil, err
		}
		base = &other.Snapshot
		resp.Against = strconv.Itoa(n)
		low, high := min(n, version), max(n, version)
		if secretChanges, err = v.secretChangesBetween(ctx, tenantID, monitorID, low, high); err != nil {
			return nil, err
		}
	}
	// Without a base (the oldest kept version) every field reads as added.
	if resp.Changes, err = diffSnapshots(base, &target.Snapshot); err != nil {
		return nil, err
	}
	resp.Changes = withSecretChanges(resp.Changes, base, &target.Snapshot, secretChanges)
	return resp, nil
}

// LiveReferences drops the channels, locations, dependencies and members a
// snapshot names that no longer exist in the tenant, so a restore can apply
// the rest. It returns the trimmed copy and the dropped references
// ("channel:<id>", "location:<id>", "dependency:<id>", "member:<id>").
func (v *VersionStore) LiveReferences(ctx context.Context, tenantID uuid.UUID, snapshot models.MonitorConfigSnapshot) (models.MonitorConfigSnapshot, []string, error) {
	dropped := []string{}
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
