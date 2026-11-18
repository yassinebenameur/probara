package models

import (
	"encoding/json"

	"github.com/google/uuid"
)

// Config-as-code: POST /monitors/apply/plan and POST /monitors/apply take a
// monitors document (portable export version 2, every monitor with a key)
// and bring the workspace's keyed monitors in line with it.

// Apply actions, per monitor.
const (
	ApplyActionCreate    = "create"
	ApplyActionUpdate    = "update"
	ApplyActionUnchanged = "unchanged"
	ApplyActionDelete    = "delete"
)

// MonitorApplyRequest is the body of both endpoints.
type MonitorApplyRequest struct {
	// Document is the monitors document: the YAML or JSON text of a portable
	// export (version 2) as a JSON string, or the same document as a JSON
	// object.
	Document json.RawMessage `json:"document"`
	// Prune deletes the keyed monitors the document does not list. Apply
	// only; never touches a monitor without a key.
	Prune bool `json:"prune,omitempty"`
	// PruneScope limits prune to the keyed monitors whose key starts with
	// it (a team's prefix, "payments."): only they can be deleted, the guard
	// counts only them, and every key of the document must start with it.
	// Empty: prune covers every keyed monitor of the workspace.
	PruneScope string `json:"prune_scope,omitempty"`
	// Force lets a prune through the guard (an empty document, or more than
	// half of the keyed monitors in scope deleted) and lets a probable key
	// rename (a create and a delete with the same name and type) proceed.
	Force bool `json:"force,omitempty"`
	// Fingerprint is a plan's fingerprint: apply refuses (409) when the
	// document or the workspace's keyed monitors changed since that plan.
	Fingerprint string `json:"fingerprint,omitempty"`
}

// MonitorApplyPlan is what an apply of the document would do. Plan returns
// it; apply returns it as MonitorApplyResult once done, or with the reason
// it refused.
type MonitorApplyPlan struct {
	// Fingerprint identifies the document and the state of the workspace's
	// keyed monitors the plan was computed against.
	Fingerprint string `json:"fingerprint"`
	// Valid is false when Errors is not empty: apply would write nothing.
	Valid    bool                  `json:"valid"`
	Summary  MonitorApplySummary   `json:"summary"`
	Monitors []MonitorApplyItem    `json:"monitors"`
	Errors   []MonitorApplyError   `json:"errors"`
	Prune    MonitorApplyPruneInfo `json:"prune"`
	// ProbableRenames pairs a create and a delete with the same name and
	// type: most likely a changed key, which replaces the monitor (its
	// history, SLA intervals and links do not follow). Apply needs force to
	// go ahead while any is listed.
	ProbableRenames []MonitorApplyRename `json:"probable_renames"`
}

// MonitorApplyRename is a create and a delete that look like one monitor
// whose key changed.
type MonitorApplyRename struct {
	FromKey string      `json:"from_key"`
	ToKey   string      `json:"to_key"`
	Name    string      `json:"name"`
	Type    MonitorType `json:"type"`
}

// MonitorApplySummary counts the monitors per action. Delete counts the
// keyed monitors the document leaves out, deleted only with prune.
type MonitorApplySummary struct {
	Create    int `json:"create"`
	Update    int `json:"update"`
	Unchanged int `json:"unchanged"`
	Delete    int `json:"delete"`
}

// MonitorApplyItem is one monitor of the plan.
type MonitorApplyItem struct {
	Key       string      `json:"key"`
	Name      string      `json:"name"`
	Type      MonitorType `json:"type"`
	Action    string      `json:"action"`
	MonitorID *uuid.UUID  `json:"monitor_id,omitempty"`
	// Changes are the fields an update changes, as in the configuration
	// history diff: a secret is only ever "***" (set) or absent, never its
	// value. Group members and dependencies appear as member_keys and
	// depends_on, by key.
	Changes []MonitorVersionChange `json:"changes,omitempty"`
	Notes   []string               `json:"notes,omitempty"`
	// ProbableRename is the other key of a probable rename pair (see
	// MonitorApplyPlan.ProbableRenames).
	ProbableRename string `json:"probable_rename,omitempty"`
}

// MonitorApplyError is a reason the document cannot be applied. Index is
// the monitor's position in the document (-1 for the document itself).
type MonitorApplyError struct {
	Index   int    `json:"index"`
	Key     string `json:"key,omitempty"`
	Name    string `json:"name,omitempty"`
	Message string `json:"message"`
}

// MonitorApplyPruneInfo describes the deletions a prune would make and
// whether its guard lets them through without force.
type MonitorApplyPruneInfo struct {
	// Scope is the request's prune_scope; empty means the whole workspace.
	Scope   string `json:"scope,omitempty"`
	Deletes int    `json:"deletes"`
	// KeyedMonitors counts the keyed monitors in scope: the guard's base.
	KeyedMonitors int  `json:"keyed_monitors"`
	NeedsForce    bool `json:"needs_force"`
	// Reason says why force is needed.
	Reason string `json:"reason,omitempty"`
	// Affected are the monitors the document does not manage whose settings
	// a prune changes: a group losing a deleted member, a monitor losing a
	// deleted upstream. Each gets a system version when the prune runs.
	Affected []MonitorApplyAffected `json:"affected"`
}

// MonitorApplyAffected is a monitor outside the document that a prune
// changes, and why.
type MonitorApplyAffected struct {
	MonitorID uuid.UUID   `json:"monitor_id"`
	Key       string      `json:"key,omitempty"`
	Name      string      `json:"name"`
	Type      MonitorType `json:"type"`
	// Loses names the deleted monitors it loses, as "member:<key>" or
	// "dependency:<key>".
	Loses []string `json:"loses"`
}

// MonitorApplyResult is POST /monitors/apply's answer: the plan it carried
// out (created monitors carry their new id), whether it wrote anything, and
// whether it deleted the plan's delete entries. A refused apply answers with
// Error and Message set, as an API error, and Applied false.
type MonitorApplyResult struct {
	MonitorApplyPlan
	Applied bool   `json:"applied"`
	Pruned  bool   `json:"pruned"`
	Error   string `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
}
