package alerter

import "time"

// AlertEvent represents an alert event emitted by the alerter service.
type AlertEvent struct {
	Type      string       `json:"type"`
	TenantID  string       `json:"tenant_id"`
	Alert     AlertDetails `json:"alert"`
	Timestamp time.Time    `json:"timestamp"`
}

// AlertDetails contains details about the alert.
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
