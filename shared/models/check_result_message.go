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

// CheckResultSubjectForLocation isolates private-worker publications at the
// broker boundary. Only platform workers may publish the unsuffixed subject.
func CheckResultSubjectForLocation(baseSubject, locationID string) string {
	return baseSubject + ".loc." + locationID
}

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
	// Mesh marks the result as an inter-location mesh probe (LocationID is the
	// source). Ingest routes it to location_mesh_state/mesh_probe_results
	// instead of the monitor pipeline — MonitorID is empty for these.
	Mesh *MeshResultInfo `json:"mesh,omitempty"`
	// LocationSignature authenticates private-location results. Default-fleet
	// results leave it empty.
	LocationSignature string `json:"location_signature,omitempty"`
}

// MeshResultInfo carries the mesh-specific half of a probe result's edge key.
type MeshResultInfo struct {
	TargetLocationID string `json:"target_location_id"`
}

// DedupeID is the Nats-Msg-Id header value used for JetStream's publish-side
// dedupe window. The durable dedupe is the unique index on
// check_results(job_id, result_source).
func (m CheckResultMessage) DedupeID() string {
	return m.JobID + "/" + m.ResultSource
}
