// Package mesh serves the inter-location connectivity matrix: participating
// locations plus per-edge state measured by the scheduler/worker mesh loop.
package mesh

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/locations"
	"github.com/yassinebenameur/probara/shared/db"
)

// staleAfterIntervals is how many missed probe intervals mark an edge stale
// (expired mesh jobs vanish rather than error, so staleness is derived from
// last_check_at age).
const staleAfterIntervals = 3

// Service reads mesh state for the API.
type Service struct {
	db *db.Client
	// probeIntervalSeconds mirrors the scheduler's MESH_PROBE_INTERVAL_SECONDS
	// so staleness and UI freshness hints agree with the actual cadence.
	probeIntervalSeconds int
}

// NewService creates a mesh service.
func NewService(database *db.Client, probeIntervalSeconds int) *Service {
	if probeIntervalSeconds <= 0 {
		probeIntervalSeconds = 30
	}
	return &Service{db: database, probeIntervalSeconds: probeIntervalSeconds}
}

// GetMesh returns the tenant's mesh nodes and directed edges.
func (s *Service) GetMesh(ctx context.Context, tenantID uuid.UUID) (*models.MeshResponse, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, last_seen_at, mesh_endpoint
		FROM locations
		WHERE tenant_id = $1 AND deleted_at IS NULL AND enabled = TRUE
		  AND COALESCE(mesh_endpoint, '') <> ''
		ORDER BY name
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to list mesh locations: %w", err)
	}
	defer rows.Close()

	nodes := make([]models.MeshLocation, 0)
	for rows.Next() {
		var n models.MeshLocation
		var lastSeenAt *time.Time
		if err := rows.Scan(&n.ID, &n.Name, &lastSeenAt, &n.MeshEndpoint); err != nil {
			return nil, fmt.Errorf("failed to scan mesh location: %w", err)
		}
		n.Connected = locations.IsConnected(lastSeenAt)
		nodes = append(nodes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating mesh locations: %w", err)
	}

	edgeRows, err := s.db.QueryContext(ctx, `
		SELECT ms.source_location_id, ms.target_location_id, ms.current_state,
			ms.last_latency_ms, ms.last_check_at, ms.last_error,
			EXISTS (
				SELECT 1 FROM alerts a
				WHERE a.kind = 'mesh_edge'
				  AND a.status IN ('active', 'acknowledged')
				  AND a.source_location_id = ms.source_location_id
				  AND a.target_location_id = ms.target_location_id
			) AS alert_open
		FROM location_mesh_state ms
		WHERE ms.tenant_id = $1
		ORDER BY ms.source_location_id, ms.target_location_id
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to list mesh edges: %w", err)
	}
	defer edgeRows.Close()

	staleCutoff := time.Now().Add(-time.Duration(staleAfterIntervals*s.probeIntervalSeconds) * time.Second)
	edges := make([]models.MeshEdge, 0)
	for edgeRows.Next() {
		var e models.MeshEdge
		if err := edgeRows.Scan(
			&e.SourceLocationID, &e.TargetLocationID, &e.State,
			&e.LastLatencyMs, &e.LastCheckAt, &e.LastError, &e.AlertOpen,
		); err != nil {
			return nil, fmt.Errorf("failed to scan mesh edge: %w", err)
		}
		e.Stale = e.LastCheckAt == nil || e.LastCheckAt.Before(staleCutoff)
		edges = append(edges, e)
	}
	if err := edgeRows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating mesh edges: %w", err)
	}

	return &models.MeshResponse{
		Locations:            nodes,
		Edges:                edges,
		ProbeIntervalSeconds: s.probeIntervalSeconds,
	}, nil
}

// GetEdgeHistory returns recent probe samples for one directed edge, oldest
// first, within the given window.
func (s *Service) GetEdgeHistory(ctx context.Context, tenantID, sourceID, targetID uuid.UUID, window time.Duration, limit int) ([]models.MeshEdgeHistoryPoint, error) {
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT status, latency_ms, created_at
		FROM mesh_probe_results
		WHERE tenant_id = $1
		  AND source_location_id = $2
		  AND target_location_id = $3
		  AND created_at >= $4
		ORDER BY created_at DESC
		LIMIT $5
	`, tenantID, sourceID, targetID, time.Now().Add(-window), limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query mesh edge history: %w", err)
	}
	defer rows.Close()

	points := make([]models.MeshEdgeHistoryPoint, 0)
	for rows.Next() {
		var p models.MeshEdgeHistoryPoint
		if err := rows.Scan(&p.Status, &p.LatencyMs, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan mesh history point: %w", err)
		}
		points = append(points, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating mesh history: %w", err)
	}

	// Reverse to oldest-first for charting.
	for i, j := 0, len(points)-1; i < j; i, j = i+1, j-1 {
		points[i], points[j] = points[j], points[i]
	}
	return points, nil
}

// GetProbeTarget resolves the target location's mesh endpoint for a probe-now
// request, verifying both locations belong to the tenant and participate.
func (s *Service) GetProbeTarget(ctx context.Context, tenantID, sourceID, targetID uuid.UUID) (string, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM locations
		WHERE tenant_id = $1 AND deleted_at IS NULL AND enabled = TRUE
		  AND COALESCE(mesh_endpoint, '') <> ''
		  AND id IN ($2, $3)
	`, tenantID, sourceID, targetID).Scan(&count); err != nil {
		return "", fmt.Errorf("failed to verify mesh locations: %w", err)
	}
	if count != 2 {
		return "", locations.ErrNotFound
	}

	var endpoint string
	if err := s.db.QueryRowContext(ctx, `
		SELECT mesh_endpoint FROM locations WHERE id = $1
	`, targetID).Scan(&endpoint); err != nil {
		return "", fmt.Errorf("failed to read target endpoint: %w", err)
	}
	return endpoint, nil
}
