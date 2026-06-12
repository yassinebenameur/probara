package models

import (
	"time"

	"github.com/google/uuid"
)

// DependencyMonitor is a lightweight monitor reference returned by the
// dependency endpoints (upstream/downstream lists and graph nodes).
type DependencyMonitor struct {
	ID                uuid.UUID   `json:"id"`
	Name              string      `json:"name"`
	Type              MonitorType `json:"type"`
	CurrentState      string      `json:"current_state"`
	LastStateChangeAt *time.Time  `json:"last_state_change_at,omitempty"`
}

// DependencyListResponse wraps an upstream or downstream dependency list.
type DependencyListResponse struct {
	Items []DependencyMonitor `json:"items"`
}

// DependencyGraphEdge is one directed dependency: From depends on To.
type DependencyGraphEdge struct {
	From uuid.UUID `json:"from"`
	To   uuid.UUID `json:"to"`
}

// DependencyGraph is the tenant-wide dependency graph: every monitor that
// participates in at least one dependency edge, plus the edges.
type DependencyGraph struct {
	Nodes []DependencyMonitor   `json:"nodes"`
	Edges []DependencyGraphEdge `json:"edges"`
}
