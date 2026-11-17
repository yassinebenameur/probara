// Config-as-code: executing an apply plan (apply.go) in one transaction.

package importservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/dbtx"
	"github.com/yassinebenameur/probara/api/internal/models"
	depservice "github.com/yassinebenameur/probara/api/internal/services/dependencies"
	groupservice "github.com/yassinebenameur/probara/api/internal/services/groups"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
)

// applyLockKey names the workspace's apply advisory lock.
func applyLockKey(tenantID uuid.UUID) string {
	return "monitor-apply:" + tenantID.String()
}

// Apply brings the workspace's keyed monitors in line with the document.
//
// Atomicity: all or nothing. The whole apply is one transaction (the monitors
// service's InTx, which retries once after a deadlock), so a failure at any
// step leaves the workspace as it was. It starts with a transaction-level
// advisory lock per workspace, taken on the transaction's own connection and
// released by its commit or rollback, so two applies to one workspace run one
// after the other and the second plans against the first's result. The plan
// is computed again under that lock; a request carrying a fingerprint is
// refused when it no longer matches. Then the rows of every monitor the plan
// changes or deletes (and of every monitor whose settings a delete rewrites)
// are locked FOR UPDATE, the targets of new edges FOR KEY SHARE, in one
// ascending pass before the first write (lock order, api/internal/dbtx), and
// the writes go through the monitors, groups and dependencies services, so
// every save rule applies and each created or changed monitor records one
// configuration version with source "apply". Unchanged monitors are not
// touched, so applying the same document twice writes nothing the second
// time. The size of a document is bounded (MaxApplyMonitors) because its row
// locks are held until the commit.
func (s *Service) Apply(ctx context.Context, tenantID uuid.UUID, req *models.MonitorApplyRequest) (*models.MonitorApplyResult, error) {
	svc, err := s.applyService()
	if err != nil {
		return nil, err
	}
	doc, err := decodeApplyDocument(req.Document)
	if err != nil {
		return nil, err
	}
	ctx = monitorservice.WithVersionSource(ctx, monitorservice.VersionSourceApply)

	var result *models.MonitorApplyResult
	err = svc.InTx(ctx, func(ctx context.Context) error {
		result = nil
		tx, ok := dbtx.From(ctx)
		if !ok {
			return ErrApplyUnavailable
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, applyLockKey(tenantID)); err != nil {
			return fmt.Errorf("lock workspace for apply: %w", err)
		}
		// Apply always compares secrets: it needs write access.
		plan, err := s.computeApplyPlan(ctx, svc, tenantID, doc, true)
		if err != nil {
			return err
		}
		result = &models.MonitorApplyResult{MonitorApplyPlan: plan.out}
		switch {
		case !plan.out.Valid:
			return ErrApplyInvalid
		case req.Fingerprint != "" && req.Fingerprint != plan.out.Fingerprint:
			return ErrApplyPlanChanged
		case req.Prune && plan.out.Prune.NeedsForce && !req.Force:
			return fmt.Errorf("%w: %s", ErrApplyPruneRefused, plan.out.Prune.Reason)
		}
		pruned, err := s.executeApplyPlan(ctx, svc, tenantID, plan, req.Prune)
		if err != nil {
			return err
		}
		result = &models.MonitorApplyResult{MonitorApplyPlan: plan.out, Pruned: pruned}
		result.Applied = plan.out.Summary.Create+plan.out.Summary.Update > 0 || pruned
		return nil
	})
	if err != nil {
		if result != nil && isApplyRefusal(err) {
			return result, err
		}
		return nil, err
	}
	return result, nil
}

// isApplyRefusal reports an error that answers with the plan.
func isApplyRefusal(err error) bool {
	return errors.Is(err, ErrApplyInvalid) || errors.Is(err, ErrApplyPlanChanged) || errors.Is(err, ErrApplyPruneRefused)
}

// executeApplyPlan writes a valid plan inside the apply's transaction. It
// reports whether it deleted the plan's delete entries.
func (s *Service) executeApplyPlan(ctx context.Context, svc applyMonitorService, tenantID uuid.UUID, plan *applyPlan, prune bool) (bool, error) {
	st := plan.state
	groups := groupservice.NewService(s.db)
	deps := depservice.NewService(s.db)

	ids := map[string]uuid.UUID{}
	for key, m := range st.byKey {
		ids[key] = m.id
	}

	// Row locks first, in lock order.
	var write, read []uuid.UUID
	for _, entry := range plan.entries {
		if entry.existing != nil && (entry.update != nil || entry.depsChanged || entry.membersChanged) {
			write = append(write, entry.existing.id)
		}
		if entry.depsChanged || entry.membersChanged {
			for _, key := range append(append([]string{}, entry.depKeys...), entry.memberKeys...) {
				if target := st.byKey[key]; target != nil {
					read = append(read, target.id)
				}
			}
		}
	}
	var deleteIDs []uuid.UUID
	if prune {
		for _, m := range plan.deletes {
			deleteIDs = append(deleteIDs, m.id)
		}
	}
	if len(deleteIDs) > 0 {
		affected, err := svc.Versions().MonitorsAffectedByDelete(ctx, tenantID, deleteIDs)
		if err != nil {
			return false, err
		}
		write = append(append(write, deleteIDs...), affected...)
	}
	if err := svc.LockMonitors(ctx, tenantID, write, read); err != nil {
		return false, err
	}
	svc.CaptureBaselines(ctx, tenantID, write...)

	saveCtx, versions := monitorservice.DeferVersions(ctx)
	create := func(entry *applyEntry) error {
		req := *entry.req
		enabled := entry.enabled
		req.Enabled = &enabled
		if entry.doc.Type == models.MonitorTypeGroup {
			members, err := resolveKeys(ids, entry.memberKeys)
			if err != nil {
				return err
			}
			if req.Config, err = withMonitorIDs(req.Config, members); err != nil {
				return err
			}
		}
		monitor, err := svc.CreateMonitor(saveCtx, tenantID, &req)
		if err != nil {
			return fmt.Errorf("create %q: %w", entry.key, err)
		}
		ids[entry.key] = monitor.ID
		id := monitor.ID
		plan.out.Monitors[entry.item].MonitorID = &id
		if entry.doc.Type == models.MonitorTypeGroup {
			members, _ := resolveKeys(ids, entry.memberKeys)
			if err := groups.AddMonitorsToGroup(saveCtx, tenantID, monitor.ID, members); err != nil {
				return fmt.Errorf("add members of %q: %w", entry.key, err)
			}
		}
		return nil
	}

	// New monitors, groups after their members.
	for _, entry := range plan.entries {
		if entry.existing == nil && entry.doc.Type != models.MonitorTypeGroup {
			if err := create(entry); err != nil {
				return false, err
			}
		}
	}
	for _, entry := range newGroupsInOrder(plan) {
		if err := create(entry); err != nil {
			return false, err
		}
	}

	// Existing monitors' settings. A group whose members change gets its
	// config.monitor_ids rewritten to the new membership, as a PATCH does.
	for _, entry := range plan.entries {
		if entry.existing == nil {
			continue
		}
		update := entry.update
		if entry.doc.Type == models.MonitorTypeGroup && entry.membersChanged {
			if update == nil {
				update = &models.UpdateMonitorRequest{}
			}
			members, err := desiredMembers(st, ids, entry)
			if err != nil {
				return false, err
			}
			if update.Config, err = withMonitorIDs(entry.config.Config, members); err != nil {
				return false, err
			}
		}
		if update == nil {
			continue
		}
		if _, err := svc.UpdateMonitor(saveCtx, tenantID, entry.existing.id, update); err != nil {
			return false, fmt.Errorf("update %q: %w", entry.key, err)
		}
	}

	// Edges: every removal before any addition, so a nesting or dependency
	// that changes direction never meets its old self in the cycle checks.
	for _, entry := range plan.entries {
		if entry.existing == nil || !entry.membersChanged {
			continue
		}
		desired, err := desiredMembers(st, ids, entry)
		if err != nil {
			return false, err
		}
		if removed := minus(st.members[entry.existing.id], desired); len(removed) > 0 {
			if err := groups.RemoveMonitorsFromGroup(saveCtx, tenantID, entry.existing.id, removed); err != nil {
				return false, fmt.Errorf("remove members of %q: %w", entry.key, err)
			}
		}
	}
	for _, entry := range plan.entries {
		if entry.existing == nil || !entry.depsChanged {
			continue
		}
		desired, err := desiredDependencies(st, ids, entry)
		if err != nil {
			return false, err
		}
		current := st.deps[entry.existing.id]
		if kept := intersect(current, desired); len(kept) < len(current) {
			if err := deps.SetDependencies(saveCtx, tenantID, entry.existing.id, kept); err != nil {
				return false, fmt.Errorf("dependencies of %q: %w", entry.key, err)
			}
		}
	}
	for _, entry := range plan.entries {
		if entry.existing == nil || !entry.membersChanged {
			continue
		}
		desired, err := desiredMembers(st, ids, entry)
		if err != nil {
			return false, err
		}
		if added := minus(desired, st.members[entry.existing.id]); len(added) > 0 {
			if err := groups.AddMonitorsToGroup(saveCtx, tenantID, entry.existing.id, added); err != nil {
				return false, fmt.Errorf("add members of %q: %w", entry.key, err)
			}
		}
		svc.RecordVersion(saveCtx, tenantID, entry.existing.id)
	}
	for _, entry := range plan.entries {
		if !entry.depsChanged {
			continue
		}
		desired, err := desiredDependencies(st, ids, entry)
		if err != nil {
			return false, err
		}
		id := ids[entry.key]
		if err := deps.SetDependencies(saveCtx, tenantID, id, desired); err != nil {
			return false, fmt.Errorf("dependencies of %q: %w", entry.key, err)
		}
		svc.RecordVersion(saveCtx, tenantID, id)
	}

	// The versions of this apply's saves before the prune: a delete records
	// system versions of the monitors it rewrites, which would otherwise
	// take this apply's changes as their own.
	versions.Flush(saveCtx)

	if len(deleteIDs) == 0 {
		return false, nil
	}
	if _, err := svc.BulkDeleteMonitors(ctx, tenantID, deleteIDs); err != nil {
		return false, fmt.Errorf("prune: %w", err)
	}
	return true, nil
}

// newGroupsInOrder lists the groups the plan creates, every group after the
// new groups it holds (the document's nesting is acyclic once validated).
func newGroupsInOrder(plan *applyPlan) []*applyEntry {
	var out []*applyEntry
	done := map[string]bool{}
	var visit func(entry *applyEntry)
	visit = func(entry *applyEntry) {
		if done[entry.key] {
			return
		}
		done[entry.key] = true
		for _, key := range entry.memberKeys {
			if member := plan.byKey[key]; member != nil && member.existing == nil && member.doc.Type == models.MonitorTypeGroup {
				visit(member)
			}
		}
		out = append(out, entry)
	}
	for _, entry := range plan.entries {
		if entry.existing == nil && entry.doc.Type == models.MonitorTypeGroup {
			visit(entry)
		}
	}
	return out
}

func resolveKeys(ids map[string]uuid.UUID, keys []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(keys))
	for _, key := range keys {
		id, ok := ids[key]
		if !ok {
			return nil, fmt.Errorf("key %q was not created", key)
		}
		out = append(out, id)
	}
	return out, nil
}

// desiredMembers is an existing group's membership after the apply: its
// keyed members from the document plus the unkeyed members it has now.
func desiredMembers(st *applyState, ids map[string]uuid.UUID, entry *applyEntry) ([]uuid.UUID, error) {
	keyed, err := resolveKeys(ids, entry.memberKeys)
	if err != nil {
		return nil, err
	}
	_, unkeyed := st.keyedRefs(st.members[entry.existing.id])
	return append(unkeyed, keyed...), nil
}

// desiredDependencies is a monitor's upstreams after the apply: the keyed
// ones from the document plus, for an existing monitor, its unkeyed ones.
func desiredDependencies(st *applyState, ids map[string]uuid.UUID, entry *applyEntry) ([]uuid.UUID, error) {
	keyed, err := resolveKeys(ids, entry.depKeys)
	if err != nil {
		return nil, err
	}
	if entry.existing == nil {
		return keyed, nil
	}
	_, unkeyed := st.keyedRefs(st.deps[entry.existing.id])
	return append(unkeyed, keyed...), nil
}

// withMonitorIDs sets a group config's monitor_ids.
func withMonitorIDs(config json.RawMessage, members []uuid.UUID) (json.RawMessage, error) {
	cfg := map[string]any{}
	if len(config) > 0 {
		if err := json.Unmarshal(config, &cfg); err != nil {
			return nil, fmt.Errorf("decode group config: %w", err)
		}
	}
	list := make([]string, 0, len(members))
	for _, id := range members {
		list = append(list, id.String())
	}
	cfg["monitor_ids"] = list
	return json.Marshal(cfg)
}

func minus(a, b []uuid.UUID) []uuid.UUID {
	drop := map[uuid.UUID]bool{}
	for _, id := range b {
		drop[id] = true
	}
	var out []uuid.UUID
	for _, id := range a {
		if !drop[id] {
			out = append(out, id)
		}
	}
	return out
}

func intersect(a, b []uuid.UUID) []uuid.UUID {
	keep := map[uuid.UUID]bool{}
	for _, id := range b {
		keep[id] = true
	}
	out := []uuid.UUID{}
	for _, id := range a {
		if keep[id] {
			out = append(out, id)
		}
	}
	return out
}
