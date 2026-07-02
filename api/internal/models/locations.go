package models

import (
	"time"

	"github.com/google/uuid"
)

// Location is a private check location: a remote worker deployment (NATS-only
// connectivity) that runs checks from inside a customer network.
type Location struct {
	ID          uuid.UUID `json:"id"`
	TenantID    uuid.UUID `json:"tenant_id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	Description *string   `json:"description,omitempty"`
	Enabled     bool      `json:"enabled"`
	// Connected is derived from LastSeenAt freshness (worker heartbeats).
	Connected  bool       `json:"connected"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	// MeshEndpoint (host:port) opts the location into the connectivity mesh:
	// other locations probe this address's /mesh/echo. Empty = not in mesh.
	MeshEndpoint *string `json:"mesh_endpoint,omitempty"`
	// MonitorCount is how many live monitors currently target this location.
	MonitorCount int       `json:"monitor_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// CreateLocationRequest creates a location.
type CreateLocationRequest struct {
	Name         string  `json:"name"`
	Description  *string `json:"description,omitempty"`
	MeshEndpoint *string `json:"mesh_endpoint,omitempty"`
}

// UpdateLocationRequest renames or toggles a location. MeshEndpoint set to an
// empty string clears it (opting the location out of the mesh).
type UpdateLocationRequest struct {
	Name         *string `json:"name,omitempty"`
	Description  *string `json:"description,omitempty"`
	Enabled      *bool   `json:"enabled,omitempty"`
	MeshEndpoint *string `json:"mesh_endpoint,omitempty"`
}

// LocationListResponse is a paginated list of locations.
type LocationListResponse struct {
	Items    []Location `json:"items"`
	Page     int        `json:"page"`
	PageSize int        `json:"page_size"`
	Total    int        `json:"total"`
}

// LocationDeployInfo carries ready-to-paste deployment snippets for a
// location's worker. Only NATS connectivity is required — no database URL.
type LocationDeployInfo struct {
	LocationID        uuid.UUID         `json:"location_id"`
	NATSURL           string            `json:"nats_url"`
	DockerRunCommand  string            `json:"docker_run_command"`
	DockerComposeYAML string            `json:"docker_compose_yaml"`
	Env               map[string]string `json:"env"`
}

// MonitorLocationStatus is the per-location breakdown embedded in a monitor:
// the location's identity plus its per-location check state.
type MonitorLocationStatus struct {
	ID            uuid.UUID  `json:"id"`
	Name          string     `json:"name"`
	Connected     bool       `json:"connected"`
	CurrentState  string     `json:"current_state"`
	LastLatencyMs *int64     `json:"last_latency_ms,omitempty"`
	LastCheckAt   *time.Time `json:"last_check_at,omitempty"`
}
