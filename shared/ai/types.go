// Package ai provides local typed decisions over bounded monitoring evidence.
package ai

import (
	"context"
	"encoding/json"
	"time"
)

// RootCauseAnalyzer is the domain-facing interface. Callers (the worker) build
// an AnalysisInput from incident evidence and receive a structured diagnosis.
type RootCauseAnalyzer interface {
	Analyze(ctx context.Context, in AnalysisInput) (AnalysisResult, error)
}

// AnalysisInput is local context. Only the MonitorEvidence allowlist crosses
// the inference boundary; text, timelines, IDs and names stay with the caller.
type AnalysisInput struct {
	Incident    IncidentContext      `json:"incident"`
	Alerts      []AlertContext       `json:"alerts,omitempty"`
	Monitors    []MonitorContext     `json:"monitors,omitempty"`
	Maintenance []MaintenanceContext `json:"active_maintenance,omitempty"`
	WindowStart time.Time            `json:"window_start"`
	WindowEnd   time.Time            `json:"window_end"`
	Incomplete  []string             `json:"incomplete_evidence,omitempty"`
}

// IncidentContext describes the incident under analysis.
type IncidentContext struct {
	Title     string    `json:"title"`
	Summary   string    `json:"summary,omitempty"`
	Severity  string    `json:"severity,omitempty"`
	State     string    `json:"state,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// AlertContext is one alert linked to the incident, including the deterministic
// dependency-graph root cause already computed by the alerter (if any).
type AlertContext struct {
	MonitorName                string    `json:"monitor_name"`
	Status                     string    `json:"status"`
	FailureCount               int       `json:"failure_count"`
	LastError                  string    `json:"last_error,omitempty"`
	TriggeredAt                time.Time `json:"triggered_at"`
	DependencyRootCauseMonitor string    `json:"dependency_root_cause_monitor,omitempty"`
}

// MonitorContext stores window summaries and sanitized transition samples.
type MonitorContext struct {
	Ref             string          `json:"ref"`
	Affected        bool            `json:"affected"`
	Summary         MonitorEvidence `json:"window_summary"`
	Name            string          `json:"name"`
	Type            string          `json:"type"`
	CurrentState    string          `json:"current_state,omitempty"`
	DependencyRoots []string        `json:"dependency_roots,omitempty"`
	DependsOn       []string        `json:"depends_on,omitempty"`
	RecentChecks    []CheckContext  `json:"recent_checks,omitempty"`
}

// CheckContext is a single recent check_results row. Metrics carries the
// agent/HTTP metrics_data JSONB (DNS/connect/TLS/TTFB timings, cert fields).
type CheckContext struct {
	Status        string          `json:"status"`
	HTTPStatus    *int            `json:"http_status,omitempty"`
	LatencyMS     *int            `json:"latency_ms,omitempty"`
	ErrorMessage  string          `json:"error_message,omitempty"`
	Metrics       json.RawMessage `json:"metrics,omitempty"`
	ErrorCategory string          `json:"error_category,omitempty"`
	CheckedAt     time.Time       `json:"checked_at"`
}

// MaintenanceContext is an active maintenance window covering affected
// monitors, so the model doesn't blame expected downtime.
type MaintenanceContext struct {
	Title    string    `json:"title"`
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
	Monitors []string  `json:"monitors,omitempty"`
}

// AnalysisResult is the structured diagnosis. It maps 1:1 onto the
// incident_ai_analyses columns. Prose is rendered by the versioned catalogue.
type AnalysisResult struct {
	Model               string          `json:"-"`
	Metadata            Metadata        `json:"metadata"`
	Summary             string          `json:"summary"`
	ProbableRootCause   string          `json:"probable_root_cause"`
	ContributingFactors []string        `json:"contributing_factors"`
	RecommendedActions  []string        `json:"recommended_actions"`
	Confidence          string          `json:"confidence"`
	Evidence            json.RawMessage `json:"evidence,omitempty"`
}
