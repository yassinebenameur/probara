package monitors

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/dbtx"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/logger"
)

// systemVersionLog reports failed system versions (RecordTx), which never
// fail the change they describe.
var systemVersionLog = logger.New("api", "info")

// lockVersionsSQL takes the configuration-version advisory locks of the
// monitors in $1 in array order; callers pass ids sorted ascending (lock
// order, api/internal/dbtx).
const lockVersionsSQL = `SELECT count(pg_advisory_xact_lock(hashtextextended('monitor_config_versions:' || id::text, 0))) FROM unnest($1::uuid[]) AS id`

func sortedIDs(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok || id == uuid.Nil {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i][:], out[j][:]) < 0 })
	return out
}

// LockMonitorsTx takes, on tx and before the first write, the row locks of
// every monitor a transaction touches, in the documented lock order
// (api/internal/dbtx): ascending id, FOR UPDATE for the monitors it changes
// (write), FOR KEY SHARE for those it only points at (read: the targets of
// new dependency or group member edges, whose foreign-key check would
// otherwise take that lock later, out of order). Ids of other tenants lock
// nothing.
func LockMonitorsTx(ctx context.Context, tx versionDB, tenantID uuid.UUID, write, read []uuid.UUID) error {
	write = sortedIDs(write)
	writeSet := make(map[uuid.UUID]bool, len(write))
	for _, id := range write {
		writeSet[id] = true
	}
	all := sortedIDs(append(append([]uuid.UUID{}, write...), read...))
	if len(all) == 0 {
		return nil
	}
	if len(all) == len(write) {
		// One statement: LockRows sits above the sort, so rows lock in id order.
		if _, err := tx.ExecContext(ctx, `
			SELECT id FROM monitors WHERE id = ANY($1) AND tenant_id = $2 ORDER BY id FOR UPDATE
		`, pq.Array(all), tenantID); err != nil {
			return fmt.Errorf("lock monitors: %w", err)
		}
		return nil
	}
	for _, id := range all {
		mode := "FOR KEY SHARE"
		if writeSet[id] {
			mode = "FOR UPDATE"
		}
		if _, err := tx.ExecContext(ctx, `SELECT id FROM monitors WHERE id = $1 AND tenant_id = $2 `+mode, id, tenantID); err != nil {
			return fmt.Errorf("lock monitors: %w", err)
		}
	}
	return nil
}

// LockMonitors is LockMonitorsTx on the save transaction ctx carries; a
// no-op outside one (unit tests).
func (s *Service) LockMonitors(ctx context.Context, tenantID uuid.UUID, write, read []uuid.UUID) error {
	tx, ok := dbtx.From(ctx)
	if !ok {
		return nil
	}
	return LockMonitorsTx(ctx, tx, tenantID, write, read)
}

// saveScope is shared by everything one save transaction does (Service.InTx
// opens it): the pre-change snapshots of monitors without history.
type saveScope struct {
	mu        sync.Mutex
	baselines map[uuid.UUID]*models.MonitorConfigSnapshot
}

type saveScopeKey struct{}

func withSaveScope(ctx context.Context) context.Context {
	if scopeFrom(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, saveScopeKey{}, &saveScope{baselines: map[uuid.UUID]*models.MonitorConfigSnapshot{}})
}

func scopeFrom(ctx context.Context) *saveScope {
	scope, _ := ctx.Value(saveScopeKey{}).(*saveScope)
	return scope
}

func (scope *saveScope) baseline(monitorID uuid.UUID) *models.MonitorConfigSnapshot {
	if scope == nil {
		return nil
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	return scope.baselines[monitorID]
}

// CaptureBaseline reads, on the save's transaction and before its first
// write, the configuration of a monitor that has no history yet, so the
// version the save records can be preceded by a baseline: version 1 is the
// configuration before the monitor's first recorded change. Every save path
// that changes an existing monitor calls it first (updateMonitor, the member
// and dependency routes); a create has nothing to capture. It reads through
// the transaction itself, never a second connection, and a failure only
// loses the baseline.
func (s *Service) CaptureBaseline(ctx context.Context, tenantID, monitorID uuid.UUID) {
	scope := scopeFrom(ctx)
	tx, ok := dbtx.From(ctx)
	if s.versions == nil || scope == nil || !ok {
		return
	}
	scope.mu.Lock()
	_, done := scope.baselines[monitorID]
	if !done {
		scope.baselines[monitorID] = nil
	}
	scope.mu.Unlock()
	if done {
		return
	}
	var snapshot *models.MonitorConfigSnapshot
	err := dbtx.Savepoint(ctx, tx, "monitor_config_baseline", func() error {
		var exists bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS (SELECT 1 FROM monitor_config_versions WHERE tenant_id = $1 AND monitor_id = $2)
		`, tenantID, monitorID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return nil
		}
		found := false
		var err error
		snapshot, found, err = loadSnapshot(ctx, tx, tenantID, monitorID)
		if !found {
			snapshot = nil
		}
		return err
	})
	if err != nil {
		s.versionLogger().WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"monitor_id": monitorID,
		}).Warn("Failed to capture monitor config baseline")
		return
	}
	scope.mu.Lock()
	scope.baselines[monitorID] = snapshot
	scope.mu.Unlock()
}

// RecordVersionTx records a version inside the caller's transaction, for
// paths that create monitors through the repository instead of the service
// (discovery approval, beside CreateInTx). The source is set explicitly;
// the actor comes from ctx. Unlike the service path it returns its error,
// so the caller's transaction rolls back with it.
func RecordVersionTx(ctx context.Context, tx *sql.Tx, tenantID, monitorID uuid.UUID, source string) error {
	_, err := recordVersionTx(ctx, tx, nil, tenantID, monitorID, versionMetaFromContext(WithVersionSource(ctx, source), nil))
	return err
}

// recordVersionTx snapshots the monitor and inserts the next version unless
// it would repeat the latest one, then prunes beyond MaxVersionsPerMonitor.
// It returns the recorded version, or 0 when nothing was recorded. A monitor
// without any version yet gets baseline — its configuration before this
// save, captured by CaptureBaseline — as version 1 (source "baseline") and
// the change as version 2; without a baseline (a create) the save is
// version 1.
func recordVersionTx(ctx context.Context, tx versionDB, baseline *models.MonitorConfigSnapshot, tenantID, monitorID uuid.UUID, meta versionMeta) (int, error) {
	// Serialize version numbering per monitor until commit. Taken after the
	// save's row locks (lock order, api/internal/dbtx) and before the
	// snapshot is read, so concurrent saves record in commit order, each
	// crediting its own change.
	if _, err := tx.ExecContext(ctx, lockVersionsSQL, pq.Array([]uuid.UUID{monitorID})); err != nil {
		return 0, fmt.Errorf("lock monitor versions: %w", err)
	}
	snapshot, found, err := loadSnapshot(ctx, tx, tenantID, monitorID)
	if err != nil || !found {
		return 0, err
	}
	latest, err := latestVersions(ctx, tx, tenantID, []uuid.UUID{monitorID})
	if err != nil {
		return 0, err
	}
	rows, err := versionRows(snapshot, latest[monitorID], baseline, meta.secretChanges)
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	for _, row := range rows {
		rowMeta := meta
		if row.source == VersionSourceBaseline {
			rowMeta = versionMeta{source: VersionSourceBaseline, actor: versionActor{typ: VersionActorSystem}}
		}
		if err := insertVersion(ctx, tx, tenantID, monitorID, row.version, row.snapshot, row.changedFields, rowMeta); err != nil {
			return 0, err
		}
	}
	if err := pruneVersions(ctx, tx, tenantID, []uuid.UUID{monitorID}); err != nil {
		return 0, err
	}
	return rows[len(rows)-1].version, nil
}

// latestVersion is a monitor's newest kept version.
type latestVersion struct {
	version  int
	snapshot *models.MonitorConfigSnapshot
}

// latestVersions reads the newest kept version of each monitor, in one query.
func latestVersions(ctx context.Context, q versionDB, tenantID uuid.UUID, monitorIDs []uuid.UUID) (map[uuid.UUID]*latestVersion, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT DISTINCT ON (monitor_id) monitor_id, version, snapshot FROM monitor_config_versions
		WHERE tenant_id = $1 AND monitor_id = ANY($2)
		ORDER BY monitor_id, version DESC
	`, tenantID, pq.Array(monitorIDs))
	if err != nil {
		return nil, fmt.Errorf("load latest monitor versions: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]*latestVersion{}
	for rows.Next() {
		var id uuid.UUID
		var raw []byte
		latest := &latestVersion{}
		if err := rows.Scan(&id, &latest.version, &raw); err != nil {
			return nil, err
		}
		if latest.snapshot, err = decodeStoredSnapshot(raw); err != nil {
			return nil, err
		}
		out[id] = latest
	}
	return out, rows.Err()
}

// pendingVersion is one row to insert.
type pendingVersionRow struct {
	version       int
	snapshot      []byte
	changedFields []string
	source        string
}

// versionRows decides what a monitor's new snapshot records against its
// latest version (or, without one, its baseline): nothing when it repeats
// it and no secret changed, else the next version, preceded by the
// baseline as version 1 for a monitor without history.
func versionRows(snapshot *models.MonitorConfigSnapshot, latest *latestVersion, baseline *models.MonitorConfigSnapshot, secretChanges []string) ([]pendingVersionRow, error) {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	var rows []pendingVersionRow
	next := 1
	previous := baseline
	if latest != nil {
		previous, next = latest.snapshot, latest.version+1
	}
	changedFields := []string{}
	if previous != nil {
		previousEncoded, err := json.Marshal(previous)
		if err != nil {
			return nil, err
		}
		if bytes.Equal(previousEncoded, encoded) && len(secretChanges) == 0 {
			return nil, nil
		}
		if latest == nil {
			rows = append(rows, pendingVersionRow{version: 1, snapshot: previousEncoded, changedFields: []string{}, source: VersionSourceBaseline})
			next = 2
		}
		changes, err := diffSnapshots(previous, snapshot)
		if err != nil {
			return nil, err
		}
		for _, change := range changes {
			changedFields = append(changedFields, change.Path)
		}
	}
	return append(rows, pendingVersionRow{version: next, snapshot: encoded, changedFields: changedFields}), nil
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

// pruneVersions keeps the newest MaxVersionsPerMonitor versions of each
// monitor, in one statement.
func pruneVersions(ctx context.Context, tx versionDB, tenantID uuid.UUID, monitorIDs []uuid.UUID) error {
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM monitor_config_versions v
		USING (
			SELECT monitor_id, max(version) AS top FROM monitor_config_versions
			WHERE tenant_id = $1 AND monitor_id = ANY($2) GROUP BY monitor_id
		) t
		WHERE v.tenant_id = $1 AND v.monitor_id = t.monitor_id AND v.version <= t.top - $3
	`, tenantID, pq.Array(monitorIDs), MaxVersionsPerMonitor); err != nil {
		return fmt.Errorf("prune monitor versions: %w", err)
	}
	return nil
}

// record writes a version in the save's transaction (or its own), under a
// savepoint: a failure to record rolls back the version only, never the save
// it describes. The baseline comes from the save's scope (CaptureBaseline).
func (v *VersionStore) record(ctx context.Context, tenantID, monitorID uuid.UUID, meta versionMeta) (int, error) {
	baseline := scopeFrom(ctx).baseline(monitorID)
	version := 0
	err := dbtx.Run(ctx, v.db, func(ctx context.Context, tx *sql.Tx) error {
		return dbtx.Savepoint(ctx, tx, "monitor_config_version", func() error {
			var err error
			version, err = recordVersionTx(ctx, tx, baseline, tenantID, monitorID, meta)
			return err
		})
	})
	if err != nil {
		return 0, err
	}
	return version, nil
}

// systemVersionChunk bounds the rows one INSERT of system versions carries.
const systemVersionChunk = 500

// SystemVersions records a version with source and actor "system" for every
// monitor whose settings a change outside a save rewrites: a deleted
// location, channel or member monitor. Without it, the next save of each
// monitor, whoever makes it, would be credited with that change.
//
// It is set-based, so a change touching thousands of monitors stays a short
// transaction: PrepareSystemVersionsTx (before the change's writes) reads
// the baselines of monitors without history in a handful of statements, and
// RecordTx (after them) records every version in a handful more — whatever
// the number of monitors. Both run under savepoints and only log a failure:
// recording never fails or rolls back the change it describes.
type SystemVersions struct {
	tenantID  uuid.UUID
	ids       []uuid.UUID
	baselines map[uuid.UUID]*models.MonitorConfigSnapshot
	failed    bool
}

// PrepareSystemVersionsTx captures, on tx and before the change's writes,
// the configuration of the affected monitors that have no history yet.
func PrepareSystemVersionsTx(ctx context.Context, tx versionDB, tenantID uuid.UUID, monitorIDs []uuid.UUID) *SystemVersions {
	p := &SystemVersions{tenantID: tenantID, ids: sortedIDs(monitorIDs), baselines: map[uuid.UUID]*models.MonitorConfigSnapshot{}}
	if len(p.ids) == 0 {
		return p
	}
	err := dbtx.Savepoint(ctx, tx, "monitor_system_versions", func() error {
		versioned, err := queryStrings(ctx, tx, `
			SELECT DISTINCT monitor_id::text FROM monitor_config_versions WHERE tenant_id = $1 AND monitor_id = ANY($2)
		`, tenantID, pq.Array(p.ids))
		if err != nil {
			return err
		}
		has := make(map[string]bool, len(versioned))
		for _, id := range versioned {
			has[id] = true
		}
		var missing []uuid.UUID
		for _, id := range p.ids {
			if !has[id.String()] {
				missing = append(missing, id)
			}
		}
		p.baselines, err = loadSnapshots(ctx, tx, tenantID, missing)
		return err
	})
	if err != nil {
		p.fail(err)
	}
	return p
}

func (p *SystemVersions) fail(err error) {
	p.failed = true
	systemVersionLog.WithFields(map[string]interface{}{
		"error":     err.Error(),
		"tenant_id": p.tenantID,
		"monitors":  len(p.ids),
	}).Error("Failed to record system monitor config versions")
}

// RecordTx records the system versions on tx, after the change's writes.
func (p *SystemVersions) RecordTx(ctx context.Context, tx versionDB) {
	if p == nil || p.failed || len(p.ids) == 0 {
		return
	}
	ctx = context.WithoutCancel(ctx)
	err := dbtx.Savepoint(ctx, tx, "monitor_system_versions", func() error {
		if _, err := tx.ExecContext(ctx, lockVersionsSQL, pq.Array(p.ids)); err != nil {
			return fmt.Errorf("lock monitor versions: %w", err)
		}
		latest, err := latestVersions(ctx, tx, p.tenantID, p.ids)
		if err != nil {
			return err
		}
		current, err := loadSnapshots(ctx, tx, p.tenantID, p.ids)
		if err != nil {
			return err
		}
		type recordRow struct {
			MonitorID     uuid.UUID       `json:"monitor_id"`
			Version       int             `json:"version"`
			Snapshot      json.RawMessage `json:"snapshot"`
			ChangedFields []string        `json:"changed_fields"`
			Source        string          `json:"source"`
		}
		var pending []recordRow
		for _, id := range p.ids {
			snapshot := current[id]
			if snapshot == nil {
				continue // deleted by the change itself
			}
			rows, err := versionRows(snapshot, latest[id], p.baselines[id], nil)
			if err != nil {
				return err
			}
			for _, row := range rows {
				source := row.source
				if source == "" {
					source = VersionSourceSystem
				}
				pending = append(pending, recordRow{MonitorID: id, Version: row.version, Snapshot: row.snapshot, ChangedFields: row.changedFields, Source: source})
			}
		}
		for start := 0; start < len(pending); start += systemVersionChunk {
			end := min(start+systemVersionChunk, len(pending))
			payload, err := json.Marshal(pending[start:end])
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO monitor_config_versions
					(tenant_id, monitor_id, version, snapshot, changed_fields, changed_secret_fields,
					 actor_type, actor_label, source)
				SELECT $1, r.monitor_id, r.version, r.snapshot,
					ARRAY(SELECT jsonb_array_elements_text(r.changed_fields)), '{}', 'system', '', r.source
				FROM jsonb_to_recordset($2::jsonb)
					AS r(monitor_id uuid, version int, snapshot jsonb, changed_fields jsonb, source text)
			`, p.tenantID, payload); err != nil {
				return fmt.Errorf("insert system versions: %w", err)
			}
		}
		if len(pending) == 0 {
			return nil
		}
		return pruneVersions(ctx, tx, p.tenantID, p.ids)
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		p.fail(err)
	}
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
