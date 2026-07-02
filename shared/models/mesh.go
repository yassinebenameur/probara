package models

import "time"

// MeshEchoPath is the HTTP path every worker serves for inter-location mesh
// probes. The response identifies which location answered, so probers can
// detect misrouted or stale endpoints.
const MeshEchoPath = "/mesh/echo"

// MeshEchoResponse is the body of a successful GET /mesh/echo.
type MeshEchoResponse struct {
	LocationID string    `json:"location_id"` // empty on default-fleet workers
	Hostname   string    `json:"hostname"`
	Timestamp  time.Time `json:"timestamp"`
}
