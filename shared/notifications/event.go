package notifications

import (
	"fmt"
	"strings"
	"time"
)

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
	ID            string `json:"id"`
	MonitorID     string `json:"monitor_id"`
	MonitorName   string `json:"monitor_name"`
	AlertPolicyID string `json:"alert_policy_id"`
	PolicyName    string `json:"policy_name"`
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

	// Host-metric annotation: populated when Kind == "host_metric" so
	// notifications can report which metric breached and by how much.
	// MetricName is one of "cpu", "memory", "disk", "swap".
	MetricName     *string  `json:"metric_name,omitempty"`
	MetricValue    *float64 `json:"metric_value,omitempty"`
	ThresholdValue *float64 `json:"threshold_value,omitempty"`

	// Multi-location annotation: which locations were failing when the alert
	// fired (refreshed while it stays open), so one page carries the full
	// per-location breakdown instead of splitting it across N alerts.
	FailingLocations []FailingLocation `json:"failing_locations,omitempty"`
}

// FailingLocation is one down vantage point of a multi-location monitor.
type FailingLocation struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	DownSince *time.Time `json:"down_since,omitempty"`
}

// KindAvailability, KindLatencyAnomaly and KindHostMetric are the alert kinds.
const (
	KindAvailability   = "availability"
	KindLatencyAnomaly = "latency_anomaly"
	KindHostMetric     = "host_metric"
)

// IsLatencyAnomaly reports whether this alert is a latency degradation alert.
func (d AlertDetails) IsLatencyAnomaly() bool {
	return d.Kind == KindLatencyAnomaly
}

// IsHostMetric reports whether this alert is a host-metric threshold breach.
func (d AlertDetails) IsHostMetric() bool {
	return d.Kind == KindHostMetric
}

// MetricLabel returns a human-readable name for the breaching host metric,
// e.g. "CPU" or "memory". Empty when this is not a host-metric alert.
func (d AlertDetails) MetricLabel() string {
	if d.MetricName == nil {
		return ""
	}
	switch *d.MetricName {
	case "cpu":
		return "CPU"
	case "memory":
		return "memory"
	case "disk":
		return "disk"
	case "swap":
		return "swap"
	default:
		return *d.MetricName
	}
}

// HostMetricLabel returns a title-cased header label for a host_metric alert,
// e.g. "CPU Usage High" / "Memory Back to Normal", for use in notification
// titles and headers.
func (d AlertDetails) HostMetricLabel(eventType string) string {
	label := d.MetricLabel()
	if label == "" {
		label = "Host metric"
	} else {
		label = strings.ToUpper(label[:1]) + label[1:]
	}
	switch eventType {
	case "resolved":
		return label + " Back to Normal"
	case "reminder":
		return label + " Still High"
	default:
		return label + " Usage High"
	}
}

// MetricSummary renders the breach magnitude for a host_metric alert, e.g.
// "94.2% (threshold 90%)". Empty when this is not a host-metric alert or no
// value is available.
func (d AlertDetails) MetricSummary() string {
	if !d.IsHostMetric() || d.MetricValue == nil {
		return ""
	}
	if d.ThresholdValue != nil {
		return fmt.Sprintf("%.1f%% (threshold %.0f%%)", *d.MetricValue, *d.ThresholdValue)
	}
	return fmt.Sprintf("%.1f%%", *d.MetricValue)
}

// FailingLocationNames renders the failing locations as a comma-separated
// list, e.g. "eu-west, us-east". Empty for location-less monitors.
func (d AlertDetails) FailingLocationNames() string {
	if len(d.FailingLocations) == 0 {
		return ""
	}
	names := make([]string, len(d.FailingLocations))
	for i, l := range d.FailingLocations {
		names[i] = l.Name
	}
	return strings.Join(names, ", ")
}

// FailingLocationsSummary renders the failing-locations breakdown for
// plain-text notification bodies, e.g. "Failing Locations: eu-west, us-east".
// Empty for location-less monitors.
func (d AlertDetails) FailingLocationsSummary() string {
	names := d.FailingLocationNames()
	if names == "" {
		return ""
	}
	return "Failing Locations: " + names
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
	if d.IsHostMetric() {
		label := d.MetricLabel()
		if label == "" {
			label = "host metric"
		}
		if d.Status == "resolved" {
			return label + " back to normal"
		}
		return label + " usage high"
	}
	if d.Status == "resolved" {
		return "recovered"
	}
	return "is down"
}
