package models

import (
	"time"

	"github.com/google/uuid"
)

// MeshLocation is one node of the connectivity mesh: a location that
// advertises a mesh endpoint.
type MeshLocation struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	Connected    bool      `json:"connected"`
	MeshEndpoint string    `json:"mesh_endpoint"`
}

// MeshEdge is the measured state of one directed source→target path.
type MeshEdge struct {
	SourceLocationID uuid.UUID  `json:"source_location_id"`
	TargetLocationID uuid.UUID  `json:"target_location_id"`
	State            string     `json:"state"` // unknown|up|suspect|down
	LastLatencyMs    *int64     `json:"last_latency_ms,omitempty"`
	LastCheckAt      *time.Time `json:"last_check_at,omitempty"`
	LastError        *string    `json:"last_error,omitempty"`
	// Stale marks edges whose last probe is older than several intervals —
	// typically the source location's worker is down, so no results arrive
	// (expired mesh jobs are dropped, not error-reported).
	Stale     bool `json:"stale"`
	AlertOpen bool `json:"alert_open"`
}

// MeshResponse is the full matrix for a tenant.
type MeshResponse struct {
	Locations []MeshLocation `json:"locations"`
	Edges     []MeshEdge     `json:"edges"`
	// ProbeIntervalSeconds lets the UI derive expected freshness.
	ProbeIntervalSeconds int `json:"probe_interval_seconds"`
}

// MeshEdgeHistoryPoint is one probe sample for an edge's latency chart.
type MeshEdgeHistoryPoint struct {
	Status    string    `json:"status"`
	LatencyMs *int64    `json:"latency_ms,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// MeshProbeRequest asks a source location's worker to probe a target now
// (ephemeral request-reply; nothing persisted).
type MeshProbeRequest struct {
	SourceLocationID uuid.UUID `json:"source_location_id"`
	TargetLocationID uuid.UUID `json:"target_location_id"`
}
