package models

import (
	"encoding/json"
	"time"
)

const (
	// CheckResultStream is the JetStream work-queue stream carrying check
	// results from workers back to the platform-side ingest consumer.
	CheckResultStream = "CHECK_RESULTS"
	// CheckResultSubject is the subject workers publish CheckResultMessage to.
	CheckResultSubject = "check.results"
)

// CheckResultMessage is the wire format for one executed (or expired) check.
// Workers publish it instead of writing check_results directly, so remote
// location workers only ever need NATS reachability — never Postgres.
type CheckResultMessage struct {
	Version              string          `json:"version"` // "v1"
	JobID                string          `json:"job_id"`
	MonitorID            string          `json:"monitor_id"`
	TenantID             string          `json:"tenant_id"`
	LocationID           string          `json:"location_id,omitempty"` // empty = default fleet
	Status               string          `json:"status"`                // success|failure|error
	ResultSource         string          `json:"result_source"`         // monitor|platform (platform = expired job)
	HTTPStatus           *int            `json:"http_status,omitempty"`
	LatencyMs            *int64          `json:"latency_ms,omitempty"`
	ErrorMessage         *string         `json:"error_message,omitempty"`
	MatchedBodySubstring bool            `json:"matched_body_substring"`
	MetricsData          json.RawMessage `json:"metrics_data,omitempty"`
	StartedAt            time.Time       `json:"started_at"`
	CompletedAt          time.Time       `json:"completed_at"`
}

// DedupeID is the Nats-Msg-Id header value used for JetStream's publish-side
// dedupe window. The durable dedupe is the unique index on
// check_results(job_id, result_source).
func (m CheckResultMessage) DedupeID() string {
	return m.JobID + "/" + m.ResultSource
}
