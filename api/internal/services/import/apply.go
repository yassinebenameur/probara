// Config-as-code: planning an apply of a monitors document (portable export
// version 2) against the workspace's keyed monitors. Executing it is
// apply_execute.go.

package importservice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/yassinebenameur/probara/api/internal/dbtx"
	"github.com/yassinebenameur/probara/api/internal/models"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
	"github.com/yassinebenameur/probara/api/internal/validation"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

// MaxApplyMonitors bounds the monitors one document may hold. An apply is
// one transaction holding the row locks of every monitor it changes (which
// result ingest of those monitors waits on), so it stays bounded.
const MaxApplyMonitors = 1000

// Errors the apply handlers map with errors.Is. Their text reaches clients.
var (
	// ErrApplyDocument: the document cannot be read (400).
	ErrApplyDocument = errors.New("invalid monitors document")
	// ErrApplyInvalid: the document reads but some monitors in it cannot be
	// applied; the plan's errors say why (422). Nothing was written.
	ErrApplyInvalid = errors.New("the document cannot be applied: see errors; nothing was written")
	// ErrApplyPlanChanged: the request carried a plan fingerprint and the
	// document or the workspace's keyed monitors changed since (409).
	ErrApplyPlanChanged = errors.New("the document or the workspace's keyed monitors changed since the plan: plan again; nothing was written")
	// ErrApplyPruneRefused: prune would delete what its guard refuses
	// without force (409).
	ErrApplyPruneRefused = errors.New("prune refused without force; nothing was written")
	// ErrApplyUnavailable: the service runs without the Postgres-backed
	// monitor service apply needs.
	ErrApplyUnavailable = errors.New("monitor apply is not available")
)

// applyMapping is the field mapping of a portable export row (portableRow).
var applyMapping = models.FieldMapping{
	Name: "name", Type: "type", Config: "config",
	IntervalSeconds: "interval_seconds", TimeoutSeconds: "timeout_seconds",
	Enabled: "enabled", Tags: "tags", GroupMembers: "group_members",
	ConsecutiveFailuresThreshold: "consecutive_failures_threshold",
	RecoveryThreshold:            "recovery_threshold",
}

// applyMonitorService is what apply needs of the monitors service beyond
// MonitorService: one transaction, the lock order, configuration history and
// the in-process secret comparison. *monitorservice.Service implements it.
type applyMonitorService interface {
	monitorservice.MonitorService
	InTx(ctx context.Context, fn func(ctx context.Context) error) error
	LockMonitors(ctx context.Context, tenantID uuid.UUID, write, read []uuid.UUID) error
	CaptureBaselines(ctx context.Context, tenantID uuid.UUID, monitorIDs ...uuid.UUID)
	RecordVersion(ctx context.Context, tenantID, monitorID uuid.UUID)
	Versions() *monitorservice.VersionStore
	PlanConfigWrites(ctx context.Context, tenantID uuid.UUID, writes []monitorservice.ConfigWrite, compareSecrets bool) ([]monitorservice.ConfigWritePlan, error)
}

func (s *Service) applyService() (applyMonitorService, error) {
	svc, ok := s.monitorService.(applyMonitorService)
	if !ok || svc.Versions() == nil || s.db == nil {
		return nil, ErrApplyUnavailable
	}
	return svc, nil
}

// decodeApplyDocument reads the request's document: YAML or JSON text in a
// JSON string, or a JSON object. Unknown fields are refused, so a misspelt
// depends_on or member_keys fails instead of being dropped.
func decodeApplyDocument(raw json.RawMessage) (*models.PortableMonitorExport, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, fmt.Errorf("%w: document is required", ErrApplyDocument)
	}
	text := trimmed
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrApplyDocument, err)
		}
		text = []byte(s)
	}
	var doc models.PortableMonitorExport
	decoder := yaml.NewDecoder(bytes.NewReader(text))
	decoder.KnownFields(true)
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrApplyDocument, err)
	}
	if doc.Kind != models.PortableMonitorExportKind {
		return nil, fmt.Errorf("%w: kind must be %q", ErrApplyDocument, models.PortableMonitorExportKind)
	}
	if doc.Version != models.PortableExportVersion2 {
		return nil, fmt.Errorf("%w: apply needs version %d (export with GET /monitors/export?keyed=true)", ErrApplyDocument, models.PortableExportVersion2)
	}
	if len(doc.Monitors) > MaxApplyMonitors {
		return nil, fmt.Errorf("%w: at most %d monitors per document", ErrApplyDocument, MaxApplyMonitors)
	}
	return &doc, nil
}

// applyMonitor is a live monitor of the workspace, as apply sees it.
type applyMonitor struct {
	id      uuid.UUID
	key     string
	name    string
	typ     models.MonitorType
	enabled bool
}

// applyState is the workspace an apply is planned against.
type applyState struct {
	monitors  map[uuid.UUID]*applyMonitor
	byKey     map[string]*applyMonitor
	deps      map[uuid.UUID][]uuid.UUID // dependent -> upstreams
	members   map[uuid.UUID][]uuid.UUID // group -> members
	snapshots map[uuid.UUID]*models.MonitorConfigSnapshot
	versions  map[uuid.UUID]int
	keyed     []*applyMonitor // sorted by key
}

func (s *Service) loadApplyState(ctx context.Context, svc applyMonitorService, tenantID uuid.UUID) (*applyState, error) {
	state := &applyState{
		monitors: map[uuid.UUID]*applyMonitor{},
		byKey:    map[string]*applyMonitor{},
		members:  map[uuid.UUID][]uuid.UUID{},
	}
	conn := dbtx.Of(ctx, s.db)
	rows, err := conn.QueryContext(ctx, `
		SELECT id, COALESCE(key, ''), name, type, enabled FROM monitors
		WHERE tenant_id = $1 AND deleted_at IS NULL
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("load monitors: %w", err)
	}
	for rows.Next() {
		m := &applyMonitor{}
		if err := rows.Scan(&m.id, &m.key, &m.name, &m.typ, &m.enabled); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan monitor: %w", err)
		}
		state.monitors[m.id] = m
		if m.key != "" {
			state.byKey[m.key] = m
			state.keyed = append(state.keyed, m)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(state.keyed, func(i, j int) bool { return state.keyed[i].key < state.keyed[j].key })

	if state.deps, err = s.loadDependencies(ctx, tenantID); err != nil {
		return nil, err
	}
	memberRows, err := conn.QueryContext(ctx, `
		SELECT mg.group_id, mg.monitor_id FROM monitor_groups mg
		JOIN monitors g ON g.id = mg.group_id AND g.tenant_id = $1 AND g.deleted_at IS NULL
		JOIN monitors m ON m.id = mg.monitor_id AND m.tenant_id = $1 AND m.deleted_at IS NULL
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("load group members: %w", err)
	}
	for memberRows.Next() {
		var groupID, memberID uuid.UUID
		if err := memberRows.Scan(&groupID, &memberID); err != nil {
			memberRows.Close()
			return nil, fmt.Errorf("scan group member: %w", err)
		}
		state.members[groupID] = append(state.members[groupID], memberID)
	}
	memberRows.Close()
	if err := memberRows.Err(); err != nil {
		return nil, err
	}

	keyedIDs := make([]uuid.UUID, 0, len(state.keyed))
	for _, m := range state.keyed {
		keyedIDs = append(keyedIDs, m.id)
	}
	store := svc.Versions()
	if state.snapshots, err = store.CurrentSnapshots(ctx, tenantID, keyedIDs); err != nil {
		return nil, err
	}
	if state.versions, err = store.LatestVersionNumbers(ctx, tenantID, keyedIDs); err != nil {
		return nil, err
	}
	return state, nil
}

// keyedRefs returns the keys of the keyed monitors among ids, sorted, and
// the ids of the unkeyed ones, which apply never changes.
func (st *applyState) keyedRefs(ids []uuid.UUID) (keys []string, unkeyed []uuid.UUID) {
	keys = []string{}
	for _, id := range ids {
		if m := st.monitors[id]; m != nil && m.key != "" {
			keys = append(keys, m.key)
		} else if m != nil {
			unkeyed = append(unkeyed, id)
		}
	}
	sort.Strings(keys)
	return keys, unkeyed
}

// applyEntry is one monitor of the document, as planned.
type applyEntry struct {
	index      int
	doc        models.PortableExportMonitor
	key        string
	existing   *applyMonitor
	current    *models.MonitorConfigSnapshot
	req        *models.CreateMonitorRequest
	enabled    bool
	depKeys    []string
	memberKeys []string
	config     monitorservice.ConfigWritePlan
	// update holds the monitor fields an existing monitor's save changes
	// (nil: none); depsChanged and membersChanged its edges.
	update         *models.UpdateMonitorRequest
	depsChanged    bool
	membersChanged bool
	item           int // index in the plan's Monitors
	failed         bool
}

// applyPlan is a computed plan: the API answer and what executing it needs.
type applyPlan struct {
	out     models.MonitorApplyPlan
	entries []*applyEntry
	byKey   map[string]*applyEntry
	deletes []*applyMonitor
	state   *applyState
}

func (p *applyPlan) fail(entry *applyEntry, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	if entry == nil {
		p.out.Errors = append(p.out.Errors, models.MonitorApplyError{Index: -1, Message: message})
		return
	}
	entry.failed = true
	p.out.Errors = append(p.out.Errors, models.MonitorApplyError{Index: entry.index, Key: entry.key, Name: entry.doc.Name, Message: message})
}

// PlanApply computes what applying the document would do, writing nothing.
// It needs read access only: secret values the document spells out are
// compared with the stored ones only for a caller with write access.
func (s *Service) PlanApply(ctx context.Context, tenantID uuid.UUID, req *models.MonitorApplyRequest) (*models.MonitorApplyPlan, error) {
	svc, err := s.applyService()
	if err != nil {
		return nil, err
	}
	doc, err := decodeApplyDocument(req.Document)
	if err != nil {
		return nil, err
	}
	plan, err := s.computeApplyPlan(ctx, svc, tenantID, doc, ctxpkg.CanWrite(ctx))
	if err != nil {
		return nil, err
	}
	return &plan.out, nil
}

// computeApplyPlan validates the document against the workspace and works
// out every monitor's action. Validation covers everything a save checks
// (registry, schedule, domain and location rules, test-call permission,
// secret conflicts) plus what only a document can get wrong: missing,
// malformed or duplicate keys, references to keys the document lacks,
// type changes, and dependency or group cycles.
func (s *Service) computeApplyPlan(ctx context.Context, svc applyMonitorService, tenantID uuid.UUID, doc *models.PortableMonitorExport, compareSecrets bool) (*applyPlan, error) {
	state, err := s.loadApplyState(ctx, svc, tenantID)
	if err != nil {
		return nil, err
	}
	plan := &applyPlan{
		out:   models.MonitorApplyPlan{Monitors: []models.MonitorApplyItem{}, Errors: []models.MonitorApplyError{}},
		byKey: map[string]*applyEntry{},
		state: state,
	}

	// Each monitor on its own: key, type, the references' shape.
	for i, m := range doc.Monitors {
		entry := &applyEntry{index: i, doc: m, key: m.Key}
		plan.entries = append(plan.entries, entry)
		switch {
		case m.Key == "":
			plan.fail(entry, "key is required: apply only manages monitors with a key (export with keyed=true, or give the monitor a key)")
			continue
		case validation.ValidateMonitorKey(m.Key) != nil:
			plan.fail(entry, "%v", validation.ValidateMonitorKey(m.Key))
			continue
		}
		if other, dup := plan.byKey[m.Key]; dup {
			plan.fail(entry, "key %q is also used by monitors[%d]", m.Key, other.index)
			continue
		}
		plan.byKey[m.Key] = entry
		if strings.TrimSpace(m.Name) == "" {
			plan.fail(entry, "name is required")
		}
		if !validation.DefaultRegistry.Has(m.Type) {
			plan.fail(entry, "unknown monitor type %q", m.Type)
			continue
		}
		if m.Type == models.MonitorTypeGroup && len(m.DependsOn) > 0 {
			plan.fail(entry, "a group cannot have depends_on")
		}
		if m.Type != models.MonitorTypeGroup && len(m.MemberKeys) > 0 {
			plan.fail(entry, "only a group has member_keys")
		}
		entry.depKeys = dedupeSorted(m.DependsOn)
		entry.memberKeys = dedupeSorted(m.MemberKeys)
		if existing := state.byKey[m.Key]; existing != nil {
			entry.existing = existing
			entry.current = state.snapshots[existing.id]
			if existing.typ != m.Type {
				plan.fail(entry, "type %s differs from the existing monitor's %s: apply never changes a type; give the entry a new key to replace the monitor", m.Type, existing.typ)
			}
		}
	}

	// References name monitors of the document, by key.
	for _, entry := range plan.entries {
		if entry.failed {
			continue
		}
		for _, list := range []struct {
			field string
			keys  []string
		}{{"depends_on", entry.depKeys}, {"member_keys", entry.memberKeys}} {
			for _, key := range list.keys {
				switch {
				case key == entry.key:
					plan.fail(entry, "%s names the monitor itself", list.field)
				case plan.byKey[key] == nil:
					plan.fail(entry, "%s names key %q, which is not in the document", list.field, key)
				}
			}
		}
	}
	plan.checkCycles()

	// Each monitor built and validated as a save would be.
	for _, entry := range plan.entries {
		if !entry.failed {
			plan.buildRequest(ctx, s, entry)
		}
	}

	// Configs, secrets included, compared in the monitors service.
	var writes []monitorservice.ConfigWrite
	var planned []*applyEntry
	for _, entry := range plan.entries {
		if entry.failed {
			continue
		}
		write := monitorservice.ConfigWrite{Type: entry.doc.Type, Config: entry.req.Config}
		if entry.existing != nil {
			id := entry.existing.id
			write.MonitorID = &id
			if entry.current != nil {
				write.Config = withServerOwnedFields(entry.doc.Type, entry.req.Config, entry.current.Config)
			}
		}
		writes = append(writes, write)
		planned = append(planned, entry)
	}
	configPlans, err := svc.PlanConfigWrites(ctx, tenantID, writes, compareSecrets)
	if err != nil {
		return nil, err
	}
	for i, entry := range planned {
		entry.config = configPlans[i]
		if entry.config.Err != nil {
			plan.fail(entry, "%v", entry.config.Err)
		}
	}

	for _, entry := range plan.entries {
		if entry.failed {
			continue
		}
		if err := plan.diff(ctx, entry); err != nil {
			return nil, err
		}
	}

	// Keyed monitors the document leaves out.
	for _, m := range state.keyed {
		if plan.byKey[m.key] != nil {
			continue
		}
		id := m.id
		plan.deletes = append(plan.deletes, m)
		plan.out.Monitors = append(plan.out.Monitors, models.MonitorApplyItem{
			Key: m.key, Name: m.name, Type: m.typ, Action: models.ApplyActionDelete, MonitorID: &id,
		})
		plan.out.Summary.Delete++
	}
	plan.out.Prune = pruneInfo(len(doc.Monitors), len(plan.deletes), len(state.keyed))

	plan.out.Valid = len(plan.out.Errors) == 0
	plan.out.Fingerprint, err = applyFingerprint(doc, state)
	if err != nil {
		return nil, err
	}
	return plan, nil
}

// pruneInfo is the prune guard: deleting keyed monitors needs force when the
// document is empty, or when it would delete more than half of the
// workspace's keyed monitors. Both usually mean the wrong file or workspace.
func pruneInfo(documentMonitors, deletes, keyed int) models.MonitorApplyPruneInfo {
	info := models.MonitorApplyPruneInfo{Deletes: deletes, KeyedMonitors: keyed}
	switch {
	case deletes == 0:
	case documentMonitors == 0:
		info.NeedsForce = true
		info.Reason = "the document lists no monitors, so prune would delete every keyed monitor"
	case deletes*2 > keyed:
		info.NeedsForce = true
		info.Reason = fmt.Sprintf("prune would delete %d of the workspace's %d keyed monitors, more than half", deletes, keyed)
	}
	return info
}

// buildRequest builds the entry's monitor through the importer's builders
// and validates it as the create and update handlers would.
func (p *applyPlan) buildRequest(ctx context.Context, s *Service, entry *applyEntry) {
	row := portableRow(entry.index, entry.doc)
	var req *models.CreateMonitorRequest
	var err error
	if entry.doc.Type == models.MonitorTypeGroup {
		// The members the group will have: its keyed members, plus the
		// unkeyed ones it has now, which apply leaves alone. New monitors
		// get their ids at execution; validation only needs the list.
		var members []string
		for _, key := range entry.memberKeys {
			if target := p.state.byKey[key]; target != nil {
				members = append(members, target.id.String())
			} else {
				members = append(members, "new:"+key)
			}
		}
		if entry.existing != nil {
			_, unkeyed := p.state.keyedRefs(p.state.members[entry.existing.id])
			for _, id := range unkeyed {
				members = append(members, id.String())
			}
		}
		if len(members) == 0 {
			p.fail(entry, "a group needs at least one member: list them in member_keys")
			return
		}
		req, err = s.buildGroupRequestFromConfig(row, applyMapping, members)
	} else {
		req, err = s.buildMonitorRequestFromConfig(row, applyMapping, string(entry.doc.Type))
	}
	if err != nil {
		p.fail(entry, "%v", err)
		return
	}
	key := entry.key
	req.Key = &key
	docEnabled := entry.doc.Enabled == nil || *entry.doc.Enabled
	req.Enabled = &docEnabled
	entry.req = req
	if err := validation.ValidateMonitor(req); err != nil {
		p.fail(entry, "%v", err)
		return
	}

	if entry.existing != nil && entry.current != nil {
		existing := monitorFromSnapshot(entry.existing, entry.current)
		if err := validation.ValidateMonitorUpdate(fullUpdateRequest(req), existing); err != nil {
			p.fail(entry, "%v", err)
		}
	}
}

// diff works out an entry's action and, for an existing monitor, the
// changes and the save that makes them.
func (p *applyPlan) diff(ctx context.Context, entry *applyEntry) error {
	req := entry.req
	item := models.MonitorApplyItem{Key: entry.key, Name: req.Name, Type: req.Type}
	docEnabled := *req.Enabled
	missing := entry.config.MissingSecrets
	for _, field := range missing {
		item.Notes = append(item.Notes, fmt.Sprintf("secret %s is masked in the document (\"***\") and the monitor holds none", field))
	}
	for _, field := range entry.config.UnverifiedSecrets {
		item.Notes = append(item.Notes, fmt.Sprintf("secret %s is set by the document; it is compared with the stored value only for callers with write access", field))
	}

	if entry.existing == nil {
		// A test call places real calls: creating one is a tenant admin's,
		// as on every write path (the save checks it again).
		if err := monitorservice.RequireCallPermission(ctx, req.Type, req.Config); err != nil {
			p.fail(entry, "%v", err)
			return nil
		}
		item.Action = models.ApplyActionCreate
		// A monitor whose document masks a secret it needs is created
		// paused, as the importers do, so it cannot page before someone
		// enters the secret and resumes it.
		entry.enabled = docEnabled && len(missing) == 0
		if docEnabled && !entry.enabled {
			item.Notes = append(item.Notes, "created disabled: enter the missing secrets, then enable it")
		}
		for _, m := range p.state.monitors {
			if m.key == "" && m.typ == req.Type && strings.EqualFold(strings.TrimSpace(m.name), strings.TrimSpace(req.Name)) {
				item.Notes = append(item.Notes, fmt.Sprintf("an unmanaged %s monitor named %q exists: give it the key %q before applying to manage it instead of creating another", m.typ, m.name, entry.key))
				break
			}
		}
		entry.depsChanged = len(entry.depKeys) > 0
		entry.membersChanged = len(entry.memberKeys) > 0
		entry.item = p.appendItem(item)
		p.out.Summary.Create++
		return nil
	}

	id := entry.existing.id
	item.MonitorID = &id
	current := entry.current
	if current == nil {
		return fmt.Errorf("monitor %s has no snapshot", id)
	}

	// Never turn on a monitor whose document masks a secret it lacks.
	entry.enabled = docEnabled
	if docEnabled && len(missing) > 0 && !entry.existing.enabled {
		entry.enabled = false
	}

	desired := *current
	desired.Name = req.Name
	desired.Config = entry.config.Masked
	desired.IntervalSeconds = req.IntervalSeconds
	desired.TimeoutSeconds = req.TimeoutSeconds
	desired.Tags = append([]string{}, req.Tags...)
	desired.ConsecutiveFailuresThreshold = valueOr(req.ConsecutiveFailuresThreshold, 2)
	desired.RecoveryThreshold = valueOr(req.RecoveryThreshold, 1)
	monitorservice.NormalizeSnapshot(&desired)

	secretFields := append(append([]string{}, entry.config.SecretChanges...), entry.config.UnverifiedSecrets...)
	changes, err := monitorservice.DiffSnapshots(current, &desired, secretFields)
	if err != nil {
		return err
	}
	if entry.config.Changed && !hasConfigChange(changes) {
		// What differs is masked on both sides (a credential in a URL).
		changes = append(changes, models.MonitorVersionChange{Path: "config", Secret: true})
	}
	if entry.existing.enabled != entry.enabled {
		changes = append(changes, models.MonitorVersionChange{Path: "enabled", Before: entry.existing.enabled, After: entry.enabled})
	}
	if entry.doc.Type == models.MonitorTypeGroup {
		currentKeys, _ := p.state.keyedRefs(p.state.members[id])
		if !equalStrings(currentKeys, entry.memberKeys) {
			entry.membersChanged = true
			changes = append(changes, models.MonitorVersionChange{Path: "member_keys", Before: currentKeys, After: entry.memberKeys})
		}
	} else {
		currentKeys, _ := p.state.keyedRefs(p.state.deps[id])
		if !equalStrings(currentKeys, entry.depKeys) {
			entry.depsChanged = true
			changes = append(changes, models.MonitorVersionChange{Path: "depends_on", Before: currentKeys, After: entry.depKeys})
		}
	}

	update := &models.UpdateMonitorRequest{}
	changed := false
	if desired.Name != current.Name {
		update.Name, changed = &desired.Name, true
	}
	if entry.config.Changed {
		update.Config, changed = entry.config.Config, true
	}
	if desired.IntervalSeconds != current.IntervalSeconds || desired.TimeoutSeconds != current.TimeoutSeconds {
		update.IntervalSeconds, update.TimeoutSeconds, changed = &desired.IntervalSeconds, &desired.TimeoutSeconds, true
	}
	if !equalStrings(desired.Tags, current.Tags) {
		update.Tags, changed = &desired.Tags, true
	}
	if desired.ConsecutiveFailuresThreshold != current.ConsecutiveFailuresThreshold {
		update.ConsecutiveFailuresThreshold, changed = &desired.ConsecutiveFailuresThreshold, true
	}
	if desired.RecoveryThreshold != current.RecoveryThreshold {
		update.RecoveryThreshold, changed = &desired.RecoveryThreshold, true
	}
	if entry.existing.enabled != entry.enabled {
		enabled := entry.enabled
		update.Enabled, changed = &enabled, true
	}
	if changed || entry.depsChanged || entry.membersChanged {
		// Changing a test-call monitor is a tenant admin's, as a PATCH is;
		// an unchanged one in the document needs nothing.
		for _, config := range []json.RawMessage{current.Config, req.Config} {
			if err := monitorservice.RequireCallPermission(ctx, req.Type, config); err != nil {
				p.fail(entry, "%v", err)
				return nil
			}
		}
	}
	if changed {
		entry.update = update
	}

	if len(changes) == 0 && entry.update == nil && !entry.depsChanged && !entry.membersChanged {
		item.Action = models.ApplyActionUnchanged
		p.out.Summary.Unchanged++
	} else {
		item.Action = models.ApplyActionUpdate
		item.Changes = changes
		p.out.Summary.Update++
	}
	entry.item = p.appendItem(item)
	return nil
}

func (p *applyPlan) appendItem(item models.MonitorApplyItem) int {
	p.out.Monitors = append(p.out.Monitors, item)
	return len(p.out.Monitors) - 1
}

// checkCycles refuses a document whose dependencies or group nesting, laid
// over the workspace's other edges (unkeyed ones are kept), form a cycle.
func (p *applyPlan) checkCycles() {
	for _, kind := range []struct {
		field   string
		current map[uuid.UUID][]uuid.UUID
		keys    func(*applyEntry) []string
	}{
		{"depends_on", p.state.deps, func(e *applyEntry) []string { return e.depKeys }},
		{"member_keys", p.state.members, func(e *applyEntry) []string { return e.memberKeys }},
	} {
		node := func(key string) string {
			if m := p.state.byKey[key]; m != nil {
				return m.id.String()
			}
			return "new:" + key
		}
		graph := map[string][]string{}
		managed := map[string]bool{}
		for _, entry := range p.entries {
			if entry.failed || entry.key == "" {
				continue
			}
			from := node(entry.key)
			managed[from] = true
			for _, key := range kind.keys(entry) {
				if p.byKey[key] != nil {
					graph[from] = append(graph[from], node(key))
				}
			}
		}
		for from, targets := range kind.current {
			fromNode := from.String()
			for _, target := range targets {
				m := p.state.monitors[target]
				// A managed monitor's keyed edges are the document's.
				if managed[fromNode] && m != nil && m.key != "" {
					continue
				}
				graph[fromNode] = append(graph[fromNode], target.String())
			}
		}
		cycle := findCycle(graph)
		if cycle == nil {
			continue
		}
		names := make([]string, 0, len(cycle))
		var culprit *applyEntry
		for _, n := range cycle {
			label := n
			if key, ok := strings.CutPrefix(n, "new:"); ok {
				label = key
			} else if id, err := uuid.Parse(n); err == nil {
				if m := p.state.monitors[id]; m != nil {
					label = m.name
					if m.key != "" {
						label = m.key
					}
				}
			}
			names = append(names, label)
			if culprit == nil {
				culprit = p.byKey[label]
			}
		}
		names = append(names, names[0])
		p.fail(culprit, "%s would form a cycle: %s", kind.field, strings.Join(names, " -> "))
	}
}

// findCycle returns the nodes of one cycle of the graph, or nil.
func findCycle(graph map[string][]string) []string {
	const (
		unseen = iota
		active
		done
	)
	state := map[string]int{}
	var stack []string
	var cycle []string
	var visit func(n string) bool
	visit = func(n string) bool {
		state[n] = active
		stack = append(stack, n)
		for _, next := range graph[n] {
			switch state[next] {
			case active:
				for i := len(stack) - 1; i >= 0; i-- {
					if stack[i] == next {
						cycle = append([]string{}, stack[i:]...)
						return true
					}
				}
			case unseen:
				if visit(next) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[n] = done
		return false
	}
	nodes := make([]string, 0, len(graph))
	for n := range graph {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	for _, n := range nodes {
		if state[n] == unseen && visit(n) {
			return cycle
		}
	}
	return nil
}

// applyFingerprint identifies the document and the state of the
// workspace's keyed monitors (each one's id, pause state, masked
// configuration and newest version number, which every save, secret
// changes included, advances). It holds no secret the caller did not send.
func applyFingerprint(doc *models.PortableMonitorExport, state *applyState) (string, error) {
	type keyedState struct {
		Key      string                        `json:"key"`
		ID       uuid.UUID                     `json:"id"`
		Enabled  bool                          `json:"enabled"`
		Version  int                           `json:"version"`
		Snapshot *models.MonitorConfigSnapshot `json:"snapshot"`
	}
	keyed := make([]keyedState, 0, len(state.keyed))
	for _, m := range state.keyed {
		keyed = append(keyed, keyedState{Key: m.key, ID: m.id, Enabled: m.enabled, Version: state.versions[m.id], Snapshot: state.snapshots[m.id]})
	}
	encoded, err := json.Marshal(struct {
		Document *models.PortableMonitorExport `json:"document"`
		Keyed    []keyedState                  `json:"keyed"`
	}{doc, keyed})
	if err != nil {
		return "", fmt.Errorf("fingerprint plan: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// withServerOwnedFields sets the fields the server owns in a document config
// (serverOwnedConfigFields) to their stored values, so a document never
// changes them and their absence is no difference.
func withServerOwnedFields(monitorType models.MonitorType, config, stored json.RawMessage) json.RawMessage {
	fields := serverOwnedConfigFields[monitorType]
	if len(fields) == 0 {
		return config
	}
	var cfg, from map[string]json.RawMessage
	if json.Unmarshal(config, &cfg) != nil || cfg == nil {
		return config
	}
	_ = json.Unmarshal(stored, &from)
	for _, field := range fields {
		if value, ok := from[field]; ok {
			cfg[field] = value
		} else {
			delete(cfg, field)
		}
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		return config
	}
	return out
}

// monitorFromSnapshot is the existing monitor as update validation reads it.
func monitorFromSnapshot(m *applyMonitor, snapshot *models.MonitorConfigSnapshot) *models.Monitor {
	monitor := &models.Monitor{
		ID: m.id, Name: snapshot.Name, Type: snapshot.Type, Config: snapshot.Config,
		IntervalSeconds: snapshot.IntervalSeconds, TimeoutSeconds: snapshot.TimeoutSeconds,
		ConsecutiveFailuresThreshold: snapshot.ConsecutiveFailuresThreshold,
		RecoveryThreshold:            snapshot.RecoveryThreshold, Enabled: m.enabled,
	}
	for _, raw := range snapshot.LocationIDs {
		if id, err := uuid.Parse(raw); err == nil {
			monitor.LocationIDs = append(monitor.LocationIDs, id)
		}
	}
	return monitor
}

// fullUpdateRequest is a create request as the update that sets every field
// the document carries, for update validation.
func fullUpdateRequest(req *models.CreateMonitorRequest) *models.UpdateMonitorRequest {
	tags := append([]string{}, req.Tags...)
	interval, timeout := req.IntervalSeconds, req.TimeoutSeconds
	return &models.UpdateMonitorRequest{
		Name: &req.Name, Key: req.Key, Config: req.Config,
		IntervalSeconds: &interval, TimeoutSeconds: &timeout, Tags: &tags,
		ConsecutiveFailuresThreshold: req.ConsecutiveFailuresThreshold,
		RecoveryThreshold:            req.RecoveryThreshold,
	}
}

func hasConfigChange(changes []models.MonitorVersionChange) bool {
	for _, change := range changes {
		if change.Path == "config" || strings.HasPrefix(change.Path, "config.") {
			return true
		}
	}
	return false
}

func valueOr(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func dedupeSorted(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}
