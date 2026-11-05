package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// MonitorConfigSnapshot is the configuration of a monitor as one version of
// its history records it (monitor_config_versions.snapshot). The config is
// masked: secret fields read "***" when set, so a snapshot never holds a
// secret and a restore can never bring one back. Pause state (enabled),
// runtime state and the generated push token / agent ID are not
// configuration and are left out. ID lists are sorted sets.
type MonitorConfigSnapshot struct {
	Name                         string                   `json:"name"`
	Type                         MonitorType              `json:"type"`
	Config                       json.RawMessage          `json:"config"`
	IntervalSeconds              int                      `json:"interval_seconds"`
	TimeoutSeconds               int                      `json:"timeout_seconds"`
	Tags                         []string                 `json:"tags"`
	ConsecutiveFailuresThreshold int                      `json:"consecutive_failures_threshold"`
	RecoveryThreshold            int                      `json:"recovery_threshold"`
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
	ChannelID      string `json:"channel_id"`
	DelaySeconds   int    `json:"delay_seconds"`
	NotifyAfterAck bool   `json:"notify_after_ack"`
}

// MonitorVersionSummary is one entry of a monitor's version list.
type MonitorVersionSummary struct {
	Version int `json:"version"`
	// Source is the surface the change came through: api, ui, mcp, import,
	// apply, restore or discovery.
	Source string `json:"source"`
	// ActorType is user, api_key or system; ActorLabel the username or key
	// name at the time of the change.
	ActorType  string     `json:"actor_type"`
	ActorID    *uuid.UUID `json:"actor_id,omitempty"`
	ActorLabel string     `json:"actor_label"`
	// RestoredFrom is the version a restore re-applied.
	RestoredFrom *int `json:"restored_from,omitempty"`
	// ChangedFields are the snapshot paths that differ from the previous
	// retained version (config.url, tags, …); empty for the first one.
	ChangedFields []string `json:"changed_fields"`
	// ChangedSecretFields are the secret config fields whose stored value
	// changed in this write (set, replaced or cleared). Their values are
	// never recorded.
	ChangedSecretFields []string  `json:"changed_secret_fields"`
	SnapshotHash        string    `json:"snapshot_hash"`
	CreatedAt           time.Time `json:"created_at"`
}

// MonitorVersion is one version with its snapshot.
type MonitorVersion struct {
	MonitorVersionSummary
	Snapshot MonitorConfigSnapshot `json:"snapshot"`
}

// MonitorVersionListResponse is GET /monitors/{id}/versions.
type MonitorVersionListResponse struct {
	Items []MonitorVersionSummary `json:"items"`
	// HistoryStartsAt is when the oldest retained version was recorded (nil
	// without any): history is not backfilled, and only RetentionLimit
	// versions per monitor are kept.
	HistoryStartsAt *time.Time `json:"history_starts_at,omitempty"`
	// Truncated is true when older versions were pruned by retention.
	Truncated      bool `json:"truncated"`
	RetentionLimit int  `json:"retention_limit"`
}

// MonitorVersionChange is one differing path between two snapshots. Before
// or After is absent when the path only exists on one side.
type MonitorVersionChange struct {
	Path   string `json:"path"`
	Before any    `json:"before,omitempty"`
	After  any    `json:"after,omitempty"`
}

// MonitorVersionDiffResponse is GET /monitors/{id}/versions/{v}/diff: the
// changes that lead from Against to Version.
type MonitorVersionDiffResponse struct {
	Version int `json:"version"`
	// Against is the version compared with, "current" for the live monitor,
	// or empty when Version is the oldest retained one (everything reads as
	// added).
	Against string                 `json:"against,omitempty"`
	Changes []MonitorVersionChange `json:"changes"`
}

// RestoreMonitorVersionResponse is POST /monitors/{id}/versions/{v}/restore.
type RestoreMonitorVersionResponse struct {
	Monitor      *Monitor `json:"monitor"`
	RestoredFrom int      `json:"restored_from"`
	// DroppedReferences are channels, locations, dependencies and members
	// the version named that no longer exist ("channel:<id>", …); the
	// restore applied everything else.
	DroppedReferences []string `json:"dropped_references"`
}
