package models

import (
	"time"

	"github.com/google/uuid"
)

// DashboardRange controls dashboard aggregation windows.
type DashboardRange string

const (
	DashboardRange24h DashboardRange = "24h"
	DashboardRange7d  DashboardRange = "7d"
	DashboardRange30d DashboardRange = "30d"
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
}

// DashboardOverviewResponse is the aggregated dashboard payload.
type DashboardOverviewResponse struct {
	Range          DashboardRange           `json:"range"`
	GeneratedAt    time.Time                `json:"generated_at"`
	Stats          DashboardStats           `json:"stats"`
	Trend          []DashboardTrendPoint    `json:"trend"`
	Activity24h    []DashboardActivityHour  `json:"activity_24h"`
	MonitorHealth  []DashboardMonitorHealth `json:"monitor_health"`
	RecentFailures []DashboardFailureEvent  `json:"recent_failures"`
	RecentAlerts   []AlertWithDetails       `json:"recent_alerts"`
}

// DashboardStats contains KPI counters and summary values.
type DashboardStats struct {
	TotalMonitors  int     `json:"total_monitors"`
	ActiveMonitors int     `json:"active_monitors"`
	HTTPMonitors   int     `json:"http_monitors"`
	AgentMonitors  int     `json:"agent_monitors"`
	OverallUptime  float64 `json:"overall_uptime"`
	AvgResponseMS  float64 `json:"avg_response_ms"`
}

// DashboardTrendPoint is a time-bucketed trend point.
type DashboardTrendPoint struct {
	BucketStart  time.Time `json:"bucket_start"`
	Label        string    `json:"label"`
	Uptime       float64   `json:"uptime"`
	ResponseTime float64   `json:"response_time"`
	TotalChecks  int       `json:"total_checks"`
}

// DashboardActivityHour is the checks/failures bucket for the 24-hour activity chart.
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
	LatestStatus  *string    `json:"latest_status"`
	LatestCheckAt *time.Time `json:"latest_check_at"`
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
