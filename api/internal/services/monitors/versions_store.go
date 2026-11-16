package monitors

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/dbtx"
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
	columns := []string{"type", "config", "tags", "notification_mode", "location_quorum"}
	dest := []any{&snapshot.Type, &config, pq.Array(&tags), &snapshot.NotificationMode, &snapshot.LocationQuorum}
	for _, c := range snapshotColumns {
		columns = append(columns, c.name)
		dest = append(dest, c.dest(snapshot))
	}
	err := q.QueryRowContext(ctx, `
		SELECT `+strings.Join(columns, ", ")+`
		FROM monitors
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, monitorID, tenantID).Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("load monitor for version: %w", err)
	}
	// Masked through the secret registry and the credential name rules,
	// after folding miscased keys and moving plain credentials as a save
	// would: a row stored before those rules ran can hold a credential in a
	// plain header, and a version must never hold one.
	masked, err := secrets.MaskStoredMonitorConfig(string(snapshot.Type), config)
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

// RecordVersionTx records a version inside the caller's transaction, for
// paths that create or change monitors through the repository instead of the
// service (discovery approval, beside CreateInTx). The source is set
// explicitly; the actor comes from ctx. Unlike the service path it returns
// its error, so the caller's transaction rolls back with it.
func RecordVersionTx(ctx context.Context, tx *sql.Tx, tenantID, monitorID uuid.UUID, source string) error {
	_, err := recordVersionTx(ctx, tx, nil, tenantID, monitorID, versionMetaFromContext(WithVersionSource(ctx, source), nil))
	return err
}

// RecordSystemVersionsTx records, inside the caller's transaction, a
// version with source and actor "system" for each monitor whose settings a
// change outside a save just rewrote: a deleted location, channel or member
// monitor. Without it the next save, whoever makes it, would be credited
// with that change. pool reads the monitors' committed state for the
// baseline of a monitor without history (see recordVersionTx). Each monitor
// is recorded under a savepoint; the first failure is returned.
func RecordSystemVersionsTx(ctx context.Context, tx *sql.Tx, pool db.Querier, tenantID uuid.UUID, monitorIDs []uuid.UUID) error {
	meta := versionMeta{source: VersionSourceSystem, actor: versionActor{typ: VersionActorSystem}}
	for _, monitorID := range monitorIDs {
		if err := dbtx.Savepoint(ctx, tx, "monitor_config_version", func() error {
			_, err := recordVersionTx(ctx, tx, pool, tenantID, monitorID, meta)
			return err
		}); err != nil {
			return fmt.Errorf("record system version of monitor %s: %w", monitorID, err)
		}
	}
	return nil
}

// recordVersionTx snapshots the monitor and inserts the next version unless
// it would repeat the latest one, then prunes beyond MaxVersionsPerMonitor.
// It returns the recorded version, or 0 when nothing was recorded.
//
// A monitor without any version yet (one that predates history) first gets
// a baseline: its configuration before this save, read through pool — a
// connection outside the save's transaction, which still sees the committed
// state — recorded as version 1 with source "baseline", so the first change
// after the upgrade can be compared and undone. A monitor created by this
// save has no committed row there, and its creation is version 1. pool is
// nil where no baseline can exist (discovery creates the monitor).
func recordVersionTx(ctx context.Context, tx versionDB, pool db.Querier, tenantID, monitorID uuid.UUID, meta versionMeta) (int, error) {
	// Serialize version numbering per monitor for the rest of the
	// transaction. A transaction-scoped advisory lock rather than a lock on
	// the monitors row, which the scheduler and result ingest update
	// constantly. Taken before the snapshot is read, so concurrent saves
	// record in commit order, each crediting its own change.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('monitor_config_versions:' || $1::text, 0))`, monitorID); err != nil {
		return 0, fmt.Errorf("lock monitor versions: %w", err)
	}
	snapshot, found, err := loadSnapshot(ctx, tx, tenantID, monitorID)
	if err != nil || !found {
		return 0, err
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return 0, err
	}

	latest := 0
	var latestRaw []byte
	err = tx.QueryRowContext(ctx, `
		SELECT version, snapshot FROM monitor_config_versions
		WHERE tenant_id = $1 AND monitor_id = $2 ORDER BY version DESC LIMIT 1
	`, tenantID, monitorID).Scan(&latest, &latestRaw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("load latest monitor version: %w", err)
	}

	var previous *models.MonitorConfigSnapshot
	if latest > 0 {
		if previous, err = decodeStoredSnapshot(latestRaw); err != nil {
			return 0, err
		}
	} else if pool != nil {
		baseline, existed, err := loadSnapshot(ctx, pool, tenantID, monitorID)
		if err != nil {
			return 0, fmt.Errorf("load baseline: %w", err)
		}
		if existed {
			previous = baseline
		}
	}
	if previous != nil {
		previousEncoded, err := json.Marshal(previous)
		if err != nil {
			return 0, err
		}
		if bytes.Equal(previousEncoded, encoded) && len(meta.secretChanges) == 0 {
			return 0, nil
		}
		if latest == 0 {
			baselineMeta := versionMeta{source: VersionSourceBaseline, actor: versionActor{typ: VersionActorSystem}}
			if err := insertVersion(ctx, tx, tenantID, monitorID, 1, previousEncoded, []string{}, baselineMeta); err != nil {
				return 0, err
			}
			latest = 1
		}
	}

	changedFields := []string{}
	if previous != nil {
		changes, err := diffSnapshots(previous, snapshot)
		if err != nil {
			return 0, err
		}
		for _, change := range changes {
			changedFields = append(changedFields, change.Path)
		}
	}
	next := latest + 1
	if err := insertVersion(ctx, tx, tenantID, monitorID, next, encoded, changedFields, meta); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM monitor_config_versions
		WHERE tenant_id = $1 AND monitor_id = $2 AND version <= $3
	`, tenantID, monitorID, next-MaxVersionsPerMonitor); err != nil {
		return 0, fmt.Errorf("prune monitor versions: %w", err)
	}
	return next, nil
}

func insertVersion(ctx context.Context, tx versionDB, tenantID, monitorID uuid.UUID, version int, snapshot []byte, changedFields []string, meta versionMeta) error {
	secretChanges := meta.secretChanges
	if secretChanges == nil {
		secretChanges = []string{}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO monitor_config_versions
			(tenant_id, monitor_id, version, snapshot, changed_fields, changed_secret_fields,
			 actor_type, actor_id, actor_label, source, restored_from)
		VALUES ($1, $2, $3, $4, $5, $6, $7::text, $8::uuid,
			COALESCE(CASE $7::text
				WHEN 'user' THEN (SELECT username FROM admin_users WHERE id = $8::uuid)
				WHEN 'api_key' THEN (SELECT name FROM api_keys WHERE id = $8::uuid)
			END, ''),
			$9, $10)
	`, tenantID, monitorID, version, snapshot, pq.Array(changedFields), pq.Array(secretChanges),
		meta.actor.typ, meta.actor.id, meta.source, meta.restoredFrom); err != nil {
		return fmt.Errorf("insert monitor version: %w", err)
	}
	return nil
}

// VersionStore reads and writes monitor configuration history. Every query
// is scoped to a tenant, and runs in the transaction ctx carries, if any
// (api/internal/dbtx).
type VersionStore struct {
	db db.DB
}

// NewVersionStore creates a version store on the database.
func NewVersionStore(database db.DB) *VersionStore {
	return &VersionStore{db: database}
}

func (v *VersionStore) conn(ctx context.Context) dbtx.Conn {
	return dbtx.Of(ctx, v.db)
}

// record writes a version in the save's transaction (or its own), under a
// savepoint: a failure to record rolls back the version only, never the save
// it describes.
func (v *VersionStore) record(ctx context.Context, tenantID, monitorID uuid.UUID, meta versionMeta) (int, error) {
	// A baseline is only told apart from the save inside the save's own
	// transaction: after a commit, the pool sees the saved state. Recorded
	// outside one (a deferred batch flushed after the fact), a monitor
	// without history starts at its current state instead.
	var pool db.Querier
	if _, ok := dbtx.From(ctx); ok {
		pool = v.db
	}
	version := 0
	err := dbtx.Run(ctx, v.db, func(ctx context.Context, tx *sql.Tx) error {
		return dbtx.Savepoint(ctx, tx, "monitor_config_version", func() error {
			var err error
			version, err = recordVersionTx(ctx, tx, pool, tenantID, monitorID, meta)
			return err
		})
	})
	if err != nil {
		return 0, err
	}
	return version, nil
}

// MonitorsAffectedByDelete lists the live monitors of the tenant whose
// recorded settings name one of the monitors being deleted: groups holding
// them as members and monitors depending on them.
func (v *VersionStore) MonitorsAffectedByDelete(ctx context.Context, tenantID uuid.UUID, deleted []uuid.UUID) ([]uuid.UUID, error) {
	ids, err := queryStrings(ctx, v.conn(ctx), `
		SELECT DISTINCT m.id::text FROM monitors m
		WHERE m.tenant_id = $1 AND m.deleted_at IS NULL AND m.id <> ALL($2::uuid[]) AND (
			EXISTS (SELECT 1 FROM monitor_groups mg WHERE mg.group_id = m.id AND mg.monitor_id = ANY($2::uuid[]))
			OR EXISTS (SELECT 1 FROM monitor_dependencies md WHERE md.monitor_id = m.id AND md.depends_on_id = ANY($2::uuid[])))
		ORDER BY 1
	`, tenantID, pq.Array(deleted))
	if err != nil {
		return nil, fmt.Errorf("find monitors affected by delete: %w", err)
	}
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		out = append(out, uuid.MustParse(id))
	}
	return out, nil
}

// recordSystem records system versions for monitors in the transaction ctx
// carries (or its own).
func (v *VersionStore) recordSystem(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID) error {
	if len(monitorIDs) == 0 {
		return nil
	}
	return dbtx.Run(ctx, v.db, func(ctx context.Context, tx *sql.Tx) error {
		return RecordSystemVersionsTx(ctx, tx, v.db, tenantID, monitorIDs)
	})
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
	if err := v.conn(ctx).QueryRowContext(ctx, `
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

	rows, err := v.conn(ctx).QueryContext(ctx, `
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
	summary, err := scanVersionSummary(v.conn(ctx).QueryRowContext(ctx, `
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
	if err := v.conn(ctx).QueryRowContext(ctx, `
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
	return queryStrings(ctx, v.conn(ctx), `
		SELECT DISTINCT field FROM monitor_config_versions, unnest(changed_secret_fields) AS field
		WHERE tenant_id = $1 AND monitor_id = $2 AND version > $3 AND ($4 = 0 OR version <= $4)
		ORDER BY field
	`, tenantID, monitorID, from, to)
}

// CurrentSnapshot reads the live monitor as a snapshot (ErrVersionNotFound
// when it does not exist in the tenant).
func (v *VersionStore) CurrentSnapshot(ctx context.Context, tenantID, monitorID uuid.UUID) (*models.MonitorConfigSnapshot, error) {
	snapshot, found, err := loadSnapshot(ctx, v.conn(ctx), tenantID, monitorID)
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
		found, err := queryStrings(ctx, v.conn(ctx), query, tenantID, pq.Array(ids))
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
