package models

import (
	"time"

	"github.com/google/uuid"
)

// DashboardRange controls dashboard aggregation windows.
type DashboardRange string

const (
	DashboardRange1h   DashboardRange = "1h"
	DashboardRange24h  DashboardRange = "24h"
	DashboardRange7d   DashboardRange = "7d"
	DashboardRange30d  DashboardRange = "30d"
	DashboardRange90d  DashboardRange = "90d"
	DashboardRange365d DashboardRange = "365d"
)

// DashboardFailureState represents whether a failure is still firing or resolved.
type DashboardFailureState string

const (
	DashboardFailureStateFiring   DashboardFailureState = "firing"
	DashboardFailureStateResolved DashboardFailureState = "resolved"
)

// DashboardOverviewQuery represents query params for dashboard overview.
type DashboardOverviewQuery struct {
	Range         DashboardRange `json:"range"`
	FailuresLimit int            `json:"failures_limit"`
	AlertsLimit   int            `json:"alerts_limit"`
	Tags          []string       `json:"tags"`
}

// DashboardListQuery represents query params for dashboard list endpoints.
type DashboardListQuery struct {
	Range DashboardRange `json:"range"`
	Limit int            `json:"limit"`
	Tags  []string       `json:"tags"`
}

// DashboardOverviewResponse is the aggregated dashboard payload.
type DashboardOverviewResponse struct {
	Range           DashboardRange            `json:"range"`
	GeneratedAt     time.Time                 `json:"generated_at"`
	AvailableTags   []string                  `json:"available_tags"`
	Stats           DashboardStats            `json:"stats"`
	Trend           []DashboardTrendPoint     `json:"trend"`
	Activity24h     []DashboardActivityHour   `json:"activity_24h"`
	OpsSummary      DashboardOpsSummary       `json:"ops_summary"`
	MonitorHealth   []DashboardMonitorHealth  `json:"monitor_health"`
	ProblemMonitors []DashboardProblemMonitor `json:"problem_monitors"`
	RecentFailures  []DashboardFailureEvent   `json:"recent_failures"`
	RecentAlerts    []AlertWithDetails        `json:"recent_alerts"`
}

// DashboardSummaryResponse is the dashboard's lightweight first-paint payload.
//
// For Range = 24h, semantic note:
//   - Stats are computed over an EXACT ROLLING window (now - 24h, now].
//   - Trend and Activity24h are 24 HOUR-ALIGNED buckets ending in the current
//     incomplete hour.
//
// As a consequence, summing Trend.TotalChecks or Activity24h.Checks is NOT
// guaranteed to equal Stats over the same range — the chart represents hourly
// history, the scalar represents the exact rolling window.
type DashboardSummaryResponse struct {
	Range         DashboardRange           `json:"range"`
	GeneratedAt   time.Time                `json:"generated_at"`
	AvailableTags []string                 `json:"available_tags"`
	GroupTags     []string                 `json:"group_tags"`
	Stats         DashboardStats           `json:"stats"`
	Trend         []DashboardTrendPoint    `json:"trend"`
	Activity24h   []DashboardActivityHour  `json:"activity_24h"`
	OpsSummary    DashboardOpsSummary      `json:"ops_summary"`
	MonitorHealth []DashboardMonitorHealth `json:"monitor_health"`
	Groups        []DashboardGroup         `json:"groups"`
}

// DashboardProblemMonitorsResponse contains the heavy problem monitors section payload.
type DashboardProblemMonitorsResponse struct {
	Range           DashboardRange            `json:"range"`
	GeneratedAt     time.Time                 `json:"generated_at"`
	ProblemMonitors []DashboardProblemMonitor `json:"problem_monitors"`
}

// DashboardRecentFailuresResponse contains the recent failures section payload.
type DashboardRecentFailuresResponse struct {
	Range          DashboardRange          `json:"range"`
	GeneratedAt    time.Time               `json:"generated_at"`
	RecentFailures []DashboardFailureEvent `json:"recent_failures"`
}

// DashboardRecentAlertsResponse contains the recent alerts section payload.
type DashboardRecentAlertsResponse struct {
	Range        DashboardRange     `json:"range"`
	GeneratedAt  time.Time          `json:"generated_at"`
	RecentAlerts []AlertWithDetails `json:"recent_alerts"`
}

// DashboardStats are 24h-range scalars computed over the EXACT ROLLING window
// (now - 24h, now]. They are not derived from the per-hour Trend/Activity24h
// buckets and may differ from naive sums of those.
type DashboardStats struct {
	TotalMonitors  int `json:"total_monitors"`
	ActiveMonitors int `json:"active_monitors"`
	HTTPMonitors   int `json:"http_monitors"`
	AgentMonitors  int `json:"agent_monitors"`
	// OverallUptime is null when no monitor had a check in the window —
	// no data must never render as 0% or 100% (S-D1, docs/state-semantics.md).
	OverallUptime *float64 `json:"overall_uptime"`
	AvgResponseMS float64  `json:"avg_response_ms"`
}

// DashboardTrendPoint is one bucket in the trend series.
//
// For Range = 24h the bucket is the hour starting at BucketStart [bucket, bucket+1h);
// the most recent bucket may cover the current incomplete hour.
type DashboardTrendPoint struct {
	BucketStart  time.Time `json:"bucket_start"`
	Label        string    `json:"label"`
	Uptime       float64   `json:"uptime"`
	ResponseTime float64   `json:"response_time"`
	TotalChecks  int       `json:"total_checks"`
}

// DashboardActivityHour is one hour-aligned bucket of check counts.
// The most recent entry covers the current incomplete hour.
type DashboardActivityHour struct {
	BucketStart time.Time `json:"bucket_start"`
	Label       string    `json:"label"`
	Checks      int       `json:"checks"`
	Failures    int       `json:"failures"`
}

// DashboardMonitorHealth is a lightweight monitor health row for the dashboard grid.
type DashboardMonitorHealth struct {
	MonitorID     uuid.UUID  `json:"monitor_id"`
	MonitorName   string     `json:"monitor_name"`
	Enabled       bool       `json:"enabled"`
	InMaintenance bool       `json:"in_maintenance"`
	LatestStatus  *string    `json:"latest_status"`
	LatestCheckAt *time.Time `json:"latest_check_at"`
}

// DashboardOpsSummary contains current operational counts for the fleet.
type DashboardOpsSummary struct {
	UpMonitors          int `json:"up_monitors"`
	DownMonitors        int `json:"down_monitors"`
	PausedMonitors      int `json:"paused_monitors"`
	MaintenanceMonitors int `json:"maintenance_monitors"`
	ActiveAlerts        int `json:"active_alerts"`
	AcknowledgedAlerts  int `json:"acknowledged_alerts"`
	// UnroutedMonitors counts active monitors whose alerts would notify
	// nobody — no active channel resolves for them (shared/alertrouting).
	// Paused monitors are excluded: they never alert in the first place.
	UnroutedMonitors int `json:"unrouted_monitors"`
}

// DashboardProblemMonitor represents a monitor that needs attention for the selected range.
type DashboardProblemMonitor struct {
	MonitorID       uuid.UUID  `json:"monitor_id"`
	MonitorName     string     `json:"monitor_name"`
	CurrentStatus   *string    `json:"current_status"`
	CurrentState    string     `json:"current_state"`
	FailureCount    int        `json:"failure_count"`
	ErrorCount      int        `json:"error_count"`
	Uptime          float64    `json:"uptime"`
	LatestFailureAt *time.Time `json:"latest_failure_at"`
	// LatestErrorMessage is the error_message of the monitor's most recent
	// failing check inside the requested range (nil when the check recorded
	// no message or the failures fell outside raw check_results retention).
	LatestErrorMessage *string `json:"latest_error_message,omitempty"`
}

// DashboardFailureEvent represents a recent failing check and its current state.
type DashboardFailureEvent struct {
	CheckResultID uuid.UUID             `json:"check_result_id"`
	MonitorID     uuid.UUID             `json:"monitor_id"`
	MonitorName   string                `json:"monitor_name"`
	Status        string                `json:"status"`
	ResultSource  string                `json:"result_source"`
	ErrorMessage  *string               `json:"error_message"`
	LatencyMS     *int                  `json:"latency_ms"`
	OccurredAt    time.Time             `json:"occurred_at"`
	State         DashboardFailureState `json:"state"`
	ResolvedAt    *time.Time            `json:"resolved_at"`
}

// DashboardGroupMember is a preview row inside a group: top members sorted worst-uptime-first.
// Uptime is null when the member had no checks in the window (paused, new) —
// never a synthetic 100% (S-D1).
type DashboardGroupMember struct {
	MonitorID     uuid.UUID `json:"monitor_id"`
	MonitorName   string    `json:"monitor_name"`
	Uptime        *float64  `json:"uptime"`
	CurrentStatus *string   `json:"current_status"`
}

// DashboardGroup is a single tag-derived service group.
// Tag is *string so the sentinel for the ungrouped row can be nil (rendered as `"tag": null` in JSON).
// Uptime is the mean over members WITH data; null when no member has any.
type DashboardGroup struct {
	Tag            *string                `json:"tag"`
	MonitorCount   int                    `json:"monitor_count"`
	Uptime         *float64               `json:"uptime"`
	AttentionCount int                    `json:"attention_count"`
	WorstMember    *DashboardGroupMember  `json:"worst_member"`
	Members        []DashboardGroupMember `json:"members"`
}

// DashboardGroupSparklineQuery represents query params for the per-group sparkline endpoint.
type DashboardGroupSparklineQuery struct {
	Tag   *string // nil = ungrouped sentinel
	Range DashboardRange
	Tags  []string // top-level dashboard tag filter
}

// DashboardGroupSparklineResponse is the lazy per-group uptime sparkline.
// A null bucket means no checks landed in it (S-D1) — render a gap, not 100%.
type DashboardGroupSparklineResponse struct {
	Tag     *string        `json:"tag"`
	Range   DashboardRange `json:"range"`
	Buckets []*float64     `json:"buckets"`
}
