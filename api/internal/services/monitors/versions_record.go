package monitors

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
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
// or records a configuration version of (write), FOR KEY SHARE for those it
// only points at (read: the other end of a new dependency or group member
// edge, whose foreign-key check would otherwise take that lock later, out of
// order). The FOR UPDATE lock is also what serializes version numbering:
// every path that records a monitor's version holds its row. Ids of other
// tenants lock nothing.
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

func (scope *saveScope) baselinesFor(ids []uuid.UUID) map[uuid.UUID]*models.MonitorConfigSnapshot {
	out := map[uuid.UUID]*models.MonitorConfigSnapshot{}
	if scope == nil {
		return out
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	for _, id := range ids {
		if baseline := scope.baselines[id]; baseline != nil {
			out[id] = baseline
		}
	}
	return out
}

// captureBaselinesTx reads, set-based, the configuration of the monitors
// among ids that have no history yet (two statements plus a snapshot load,
// whatever the count).
func captureBaselinesTx(ctx context.Context, tx versionDB, tenantID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]*models.MonitorConfigSnapshot, error) {
	versioned, err := queryStrings(ctx, tx, `
		SELECT DISTINCT monitor_id::text FROM monitor_config_versions WHERE tenant_id = $1 AND monitor_id = ANY($2)
	`, tenantID, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	has := make(map[string]bool, len(versioned))
	for _, id := range versioned {
		has[id] = true
	}
	var missing []uuid.UUID
	for _, id := range ids {
		if !has[id.String()] {
			missing = append(missing, id)
		}
	}
	return loadSnapshots(ctx, tx, tenantID, missing)
}

// CaptureBaselines reads, on the save's transaction and before its first
// write, the configuration of the monitors that have no history yet, so the
// version the save records can be preceded by a baseline: version 1 is the
// configuration before the monitor's first recorded change. Every save path
// that changes existing monitors calls it first (updateMonitor, the bulk
// edit for a whole batch, the member and dependency routes); a create has
// nothing to capture. It runs set-based under one savepoint, reads through
// the transaction itself, never a second connection, and a failure only
// loses the baselines.
func (s *Service) CaptureBaselines(ctx context.Context, tenantID uuid.UUID, monitorIDs ...uuid.UUID) {
	scope := scopeFrom(ctx)
	tx, ok := dbtx.From(ctx)
	if s.versions == nil || scope == nil || !ok {
		return
	}
	scope.mu.Lock()
	var todo []uuid.UUID
	for _, id := range sortedIDs(monitorIDs) {
		if _, done := scope.baselines[id]; !done {
			scope.baselines[id] = nil
			todo = append(todo, id)
		}
	}
	scope.mu.Unlock()
	if len(todo) == 0 {
		return
	}
	var captured map[uuid.UUID]*models.MonitorConfigSnapshot
	err := dbtx.Savepoint(ctx, tx, "monitor_config_baseline", func() error {
		var err error
		captured, err = captureBaselinesTx(ctx, tx, tenantID, todo)
		return err
	})
	if err != nil {
		s.versionLogger().WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"monitors":  len(todo),
		}).Warn("Failed to capture monitor config baselines")
		return
	}
	scope.mu.Lock()
	for id, snapshot := range captured {
		scope.baselines[id] = snapshot
	}
	scope.mu.Unlock()
}

// CaptureBaseline is CaptureBaselines for one monitor.
func (s *Service) CaptureBaseline(ctx context.Context, tenantID, monitorID uuid.UUID) {
	s.CaptureBaselines(ctx, tenantID, monitorID)
}

// RecordVersionTx records a version inside the caller's transaction, for
// paths that create monitors through the repository instead of the service
// (discovery approval, beside CreateInTx). The source is set explicitly;
// the actor comes from ctx. Unlike the service path it returns its error,
// so the caller's transaction rolls back with it.
func RecordVersionTx(ctx context.Context, tx *sql.Tx, tenantID, monitorID uuid.UUID, source string) error {
	meta := versionMetaFromContext(WithVersionSource(ctx, source), nil)
	_, err := recordSetTx(ctx, tx, tenantID, []recordEntry{{monitorID: monitorID}}, meta)
	return err
}

// recordEntry is one monitor a save records: its secret changes and, for a
// monitor without history, its baseline.
type recordEntry struct {
	monitorID     uuid.UUID
	secretChanges []string
	baseline      *models.MonitorConfigSnapshot
}

// recordSetTx records the versions of every monitor of one save in a handful
// of statements, whatever their number: the row locks (a no-op, the save
// holds them already), the latest versions, the new snapshots, one INSERT per
// systemVersionChunk rows, one prune. Each monitor's snapshot is compared
// with its latest version (or its baseline): a repeat that changed no secret
// records nothing; a monitor without history gets its baseline as version 1
// (source "baseline") and the change as version 2. meta is the save's actor,
// source and restore. It returns the version recorded per monitor.
func recordSetTx(ctx context.Context, tx versionDB, tenantID uuid.UUID, entries []recordEntry, meta versionMeta) (map[uuid.UUID]int, error) {
	recorded := map[uuid.UUID]int{}
	ids := make([]uuid.UUID, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.monitorID)
	}
	ids = sortedIDs(ids)
	if len(ids) == 0 {
		return recorded, nil
	}
	// Version numbering is serialized by the monitor rows (lock order,
	// api/internal/dbtx): every recorder holds them FOR UPDATE, so this only
	// re-asserts what the save took before its first write.
	if err := LockMonitorsTx(ctx, tx, tenantID, ids, nil); err != nil {
		return nil, err
	}
	latest, err := latestVersions(ctx, tx, tenantID, ids)
	if err != nil {
		return nil, err
	}
	current, err := loadSnapshots(ctx, tx, tenantID, ids)
	if err != nil {
		return nil, err
	}
	type insertRow struct {
		MonitorID           uuid.UUID       `json:"monitor_id"`
		Version             int             `json:"version"`
		Snapshot            json.RawMessage `json:"snapshot"`
		ChangedFields       []string        `json:"changed_fields"`
		ChangedSecretFields []string        `json:"changed_secret_fields"`
		ActorType           string          `json:"actor_type"`
		ActorID             *uuid.UUID      `json:"actor_id"`
		Source              string          `json:"source"`
		RestoredFrom        *int            `json:"restored_from"`
	}
	var pending []insertRow
	for _, entry := range entries {
		snapshot := current[entry.monitorID]
		if snapshot == nil {
			continue // deleted, or another tenant's
		}
		secretChanges := entry.secretChanges
		if secretChanges == nil {
			secretChanges = []string{}
		}
		rows, err := versionRows(snapshot, latest[entry.monitorID], entry.baseline, secretChanges)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			insert := insertRow{
				MonitorID: entry.monitorID, Version: row.version, Snapshot: row.snapshot, ChangedFields: row.changedFields,
				ChangedSecretFields: secretChanges, ActorType: meta.actor.typ, ActorID: meta.actor.id,
				Source: meta.source, RestoredFrom: meta.restoredFrom,
			}
			if row.source == VersionSourceBaseline {
				insert.ChangedSecretFields, insert.ActorType, insert.ActorID = []string{}, VersionActorSystem, nil
				insert.Source, insert.RestoredFrom = VersionSourceBaseline, nil
			}
			pending = append(pending, insert)
			recorded[entry.monitorID] = row.version
		}
	}
	for start := 0; start < len(pending); start += systemVersionChunk {
		payload, err := json.Marshal(pending[start:min(start+systemVersionChunk, len(pending))])
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO monitor_config_versions
				(tenant_id, monitor_id, version, snapshot, changed_fields, changed_secret_fields,
				 actor_type, actor_id, actor_label, source, restored_from)
			SELECT $1, r.monitor_id, r.version, r.snapshot,
				ARRAY(SELECT jsonb_array_elements_text(r.changed_fields)),
				ARRAY(SELECT jsonb_array_elements_text(r.changed_secret_fields)),
				r.actor_type, r.actor_id,
				COALESCE(CASE r.actor_type
					WHEN 'user' THEN (SELECT username FROM admin_users WHERE id = r.actor_id)
					WHEN 'api_key' THEN (SELECT name FROM api_keys WHERE id = r.actor_id)
				END, ''),
				r.source, r.restored_from
			FROM jsonb_to_recordset($2::jsonb) AS r(monitor_id uuid, version int, snapshot jsonb,
				changed_fields jsonb, changed_secret_fields jsonb, actor_type text, actor_id uuid,
				source text, restored_from int)
		`, tenantID, payload); err != nil {
			return nil, fmt.Errorf("insert monitor versions: %w", err)
		}
	}
	if len(pending) > 0 {
		if err := pruneVersions(ctx, tx, tenantID, ids); err != nil {
			return nil, err
		}
	}
	return recorded, nil
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

// pendingVersionRow is one version to insert.
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

// recordSet writes the versions of one save in its transaction (or its
// own), under a single savepoint: a failure to record rolls back the
// versions only, never the save they describe. Baselines come from the
// save's scope (CaptureBaselines).
func (v *VersionStore) recordSet(ctx context.Context, tenantID uuid.UUID, entries []recordEntry, meta versionMeta) (map[uuid.UUID]int, error) {
	ids := make([]uuid.UUID, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.monitorID)
	}
	baselines := scopeFrom(ctx).baselinesFor(ids)
	for i := range entries {
		if entries[i].baseline == nil {
			entries[i].baseline = baselines[entries[i].monitorID]
		}
	}
	var recorded map[uuid.UUID]int
	err := dbtx.Run(ctx, v.db, func(ctx context.Context, tx *sql.Tx) error {
		return dbtx.Savepoint(ctx, tx, "monitor_config_version", func() error {
			var err error
			recorded, err = recordSetTx(ctx, tx, tenantID, entries, meta)
			return err
		})
	})
	if err != nil {
		return nil, err
	}
	return recorded, nil
}

// systemVersionChunk bounds the rows one INSERT of versions carries.
const systemVersionChunk = 500

// SystemVersions records a version with source and actor "system" for every
// monitor whose settings a change outside a save rewrites: a deleted
// location, channel or member monitor. Without it, the next save of each
// monitor, whoever makes it, would be credited with that change.
//
// The change must hold the affected monitors' rows FOR UPDATE before its
// first write (lock order, api/internal/dbtx): that lock serializes their
// version numbering, so no other lock is taken, whatever their number. It is
// set-based, so a change touching thousands of monitors stays a short
// transaction: PrepareSystemVersionsTx (before the change's writes) reads
// the baselines of monitors without history in a handful of statements, and
// RecordTx (after them) records every version in a handful more. Both run
// under one savepoint each and only log a failure: recording never fails or
// rolls back the change it describes.
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
		var err error
		p.baselines, err = captureBaselinesTx(ctx, tx, tenantID, p.ids)
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
	entries := make([]recordEntry, 0, len(p.ids))
	for _, id := range p.ids {
		entries = append(entries, recordEntry{monitorID: id, baseline: p.baselines[id]})
	}
	meta := versionMeta{source: VersionSourceSystem, actor: versionActor{typ: VersionActorSystem}}
	if err := dbtx.Savepoint(ctx, tx, "monitor_system_versions", func() error {
		_, err := recordSetTx(ctx, tx, p.tenantID, entries, meta)
		return err
	}); err != nil {
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
