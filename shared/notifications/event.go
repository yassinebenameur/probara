package notifications

import "time"

// AlertEvent is the canonical alert event published by the alerter to NATS and
// consumed by the worker (async notification dispatch) and the API
// (SSE broadcast). Plugin implementations also receive this type via
// plugin.DispatchRequest.Event so they can render rich, native messages.
type AlertEvent struct {
	Type      string       `json:"type"`
	TenantID  string       `json:"tenant_id"`
	Alert     AlertDetails `json:"alert"`
	Timestamp time.Time    `json:"timestamp"`
}

// AlertDetails carries the alert payload for a single alert event.
type AlertDetails struct {
	ID                   string     `json:"id"`
	MonitorID            string     `json:"monitor_id"`
	MonitorName          string     `json:"monitor_name"`
	AlertPolicyID        string     `json:"alert_policy_id"`
	PolicyName           string     `json:"policy_name"`
	// Kind distinguishes an availability outage ("availability") from a latency
	// degradation ("latency_anomaly"). Empty is treated as "availability".
	Kind                 string     `json:"kind,omitempty"`
	Status               string     `json:"status"`
	TriggeredAt          time.Time  `json:"triggered_at"`
	ResolvedAt           *time.Time `json:"resolved_at,omitempty"`
	FailureCount         int        `json:"failure_count"`
	LastError            *string    `json:"last_error,omitempty"`
	EmailSubjectTemplate *string    `json:"email_subject_template,omitempty"`
	EmailBodyTemplate    *string    `json:"email_body_template,omitempty"`

	// Root-cause annotation: set when an upstream dependency of this alert's
	// monitor is down, so notifications can say "likely caused by X".
	RootCauseMonitorID   *string    `json:"root_cause_monitor_id,omitempty"`
	RootCauseMonitorName *string    `json:"root_cause_monitor_name,omitempty"`
	RootCauseDownSince   *time.Time `json:"root_cause_down_since,omitempty"`

	// Latency-anomaly annotation: populated when Kind == "latency_anomaly" so
	// notifications can report the degradation magnitude.
	BaselineLatencyMs *float64 `json:"baseline_latency_ms,omitempty"`
	ObservedLatencyMs *float64 `json:"observed_latency_ms,omitempty"`
	AnomalyScore      *float64 `json:"anomaly_score,omitempty"`
}

// KindAvailability and KindLatencyAnomaly are the alert kinds.
const (
	KindAvailability   = "availability"
	KindLatencyAnomaly = "latency_anomaly"
)

// IsLatencyAnomaly reports whether this alert is a latency degradation alert.
func (d AlertDetails) IsLatencyAnomaly() bool {
	return d.Kind == KindLatencyAnomaly
}

// Headline returns a short, kind-aware noun phrase for the alert condition,
// e.g. "is down" or "latency degraded", suitable for notification titles.
func (d AlertDetails) Headline() string {
	if d.IsLatencyAnomaly() {
		if d.Status == "resolved" {
			return "latency recovered"
		}
		return "latency degraded"
	}
	if d.Status == "resolved" {
		return "recovered"
	}
	return "is down"
}
