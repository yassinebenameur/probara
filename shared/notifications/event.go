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
	Status               string     `json:"status"`
	TriggeredAt          time.Time  `json:"triggered_at"`
	ResolvedAt           *time.Time `json:"resolved_at,omitempty"`
	FailureCount         int        `json:"failure_count"`
	LastError            *string    `json:"last_error,omitempty"`
	EmailSubjectTemplate *string    `json:"email_subject_template,omitempty"`
	EmailBodyTemplate    *string    `json:"email_body_template,omitempty"`
}
