package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// MonitorConfigSnapshot is a monitor's configuration as one version of its
// history records it (monitor_config_versions.snapshot): exactly what
// PATCH /monitors/{id} can change, plus group membership. The config is
// masked: a secret reads "***" when set, so a snapshot never holds a secret
// and a restore can never bring one back.
//
// Left out, because they are not configuration a save sets: the pause state
// (enabled; pause and resume are recorded in the state timeline and the audit
// log, and a restore must not silently pause or resume a monitor), runtime
// state, and the generated push token and agent ID.
//
// A field added to UpdateMonitorRequest belongs here too, read in
// loadSnapshot and written back in RestoreRequest
// (api/internal/services/monitors).
type MonitorConfigSnapshot struct {
	Name                         string                   `json:"name"`
	Type                         MonitorType              `json:"type"`
	Config                       json.RawMessage          `json:"config"`
	IntervalSeconds              int                      `json:"interval_seconds"`
	TimeoutSeconds               int                      `json:"timeout_seconds"`
	Tags                         []string                 `json:"tags"`
	ConsecutiveFailuresThreshold int                      `json:"consecutive_failures_threshold"`
	NotificationMode             string                   `json:"notification_mode"`
	NotificationChannels         []MonitorSnapshotChannel `json:"notification_channels"`
	MemberAlertRollup            string                   `json:"member_alert_rollup"`
	DependencySuppression        string                   `json:"dependency_suppression"`
	LocationIDs                  []string                 `json:"location_ids"`
	LocationQuorum               int                      `json:"location_quorum"`
	DependsOnIDs                 []string                 `json:"depends_on_ids"`
	MemberIDs                    []string                 `json:"member_ids"`
}

// MonitorSnapshotChannel is one custom notification route in a snapshot.
type MonitorSnapshotChannel struct {
	ChannelID    string `json:"channel_id"`
	DelaySeconds int    `json:"delay_seconds"`
}

// MonitorVersionSummary is one entry of a monitor's version list.
type MonitorVersionSummary struct {
	Version int `json:"version"`
	// Source is the surface the save came through: ui, api, mcp, import,
	// discovery or restore.
	Source string `json:"source"`
	// ActorType is user, api_key or system; ActorLabel the username or key
	// name at the time of the save.
	ActorType  string     `json:"actor_type"`
	ActorID    *uuid.UUID `json:"actor_id,omitempty"`
	ActorLabel string     `json:"actor_label"`
	// RestoredFrom is the version a restore re-applied.
	RestoredFrom *int `json:"restored_from,omitempty"`
	// ChangedFields are the snapshot paths that differ from the previous
	// version (config.url, tags, …); empty for a monitor's first version.
	ChangedFields []string `json:"changed_fields"`
	// ChangedSecretFields name the secret config fields this save set,
	// replaced or cleared (password, secret_headers.Authorization, …). Their
	// values are never recorded.
	ChangedSecretFields []string  `json:"changed_secret_fields"`
	CreatedAt           time.Time `json:"created_at"`
}

// MonitorVersion is one version with its snapshot.
type MonitorVersion struct {
	MonitorVersionSummary
	Snapshot MonitorConfigSnapshot `json:"snapshot"`
}

// MonitorVersionListResponse is GET /monitors/{id}/versions, newest first.
type MonitorVersionListResponse struct {
	Items    []MonitorVersionSummary `json:"items"`
	Page     int                     `json:"page"`
	PageSize int                     `json:"page_size"`
	Total    int                     `json:"total"`
	// HistoryStartsAt is when the oldest kept version was recorded (absent
	// without any): history is not backfilled and only RetentionLimit
	// versions per monitor are kept.
	HistoryStartsAt *time.Time `json:"history_starts_at,omitempty"`
	// Truncated is true once older versions were pruned.
	Truncated      bool `json:"truncated"`
	RetentionLimit int  `json:"retention_limit"`
}

// MonitorVersionChange is one differing path between two snapshots. Before
// or After is absent when the path exists on one side only. Secret marks a
// secret config field: its values are only ever "***" (set) or absent (not
// set), and a secret replaced by another reads "***" on both sides.
type MonitorVersionChange struct {
	Path   string `json:"path"`
	Before any    `json:"before,omitempty"`
	After  any    `json:"after,omitempty"`
	Secret bool   `json:"secret,omitempty"`
}

// MonitorVersionDiffResponse is GET /monitors/{id}/versions/{v}/diff: the
// changes that lead from Against to Version.
type MonitorVersionDiffResponse struct {
	Version int `json:"version"`
	// Against is the version compared with, "current" for the live monitor,
	// or empty when Version is the oldest kept one (everything reads as
	// added).
	Against string                 `json:"against,omitempty"`
	Changes []MonitorVersionChange `json:"changes"`
}

// RestoreMonitorVersionResponse is POST /monitors/{id}/versions/{v}/restore.
type RestoreMonitorVersionResponse struct {
	Monitor      *Monitor `json:"monitor"`
	RestoredFrom int      `json:"restored_from"`
	// Version is the version the restore recorded; absent when the monitor
	// already matched and nothing was written.
	Version *int `json:"version,omitempty"`
	// DroppedReferences are channels, locations, dependencies and group
	// members the version named that no longer exist ("channel:<id>",
	// "location:<id>", "dependency:<id>", "member:<id>"); everything else was
	// applied.
	DroppedReferences []string `json:"dropped_references"`
	// SecretsToReenter are secret config fields the version had set that the
	// monitor no longer holds (password, secret_variables.token, …). A
	// restore never brings a secret back, so these stay empty until entered
	// again.
	SecretsToReenter []string `json:"secrets_to_reenter"`
}
