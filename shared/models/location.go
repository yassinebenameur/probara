package models

import "time"

// LocationHeartbeatSubject is the core-NATS subject location workers publish
// liveness heartbeats to. The ingest consumer fans them into
// locations.last_seen_at, which the UI reads as connected/disconnected.
const LocationHeartbeatSubject = "locations.heartbeat"

// LocationHeartbeat is one worker liveness beacon.
type LocationHeartbeat struct {
	LocationID string    `json:"location_id"`
	Hostname   string    `json:"hostname,omitempty"`
	Version    string    `json:"version,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}
