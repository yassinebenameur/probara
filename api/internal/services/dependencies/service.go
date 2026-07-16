// Package dependencies manages monitor-to-monitor dependency edges
// (monitor_dependencies table): a monitor can declare upstream monitors it
// depends on, which the alerter uses to annotate downstream alerts with a
// likely root cause. Mirrors the groups service conventions: tenant scoping
// and acyclicity are enforced here, not in the schema.
package dependencies

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/db"
)

var (
	ErrDependencyCycle = errors.New("dependency would create a cycle")
	ErrMonitorNotFound = errors.New("monitor not found")
)

// Service handles monitor dependency business logic.
type Service struct {
	db db.DB
}

// NewService creates a new dependency service.
func NewService(database db.DB) *Service {
	return &Service{db: database}
}

// SetDependencies replaces the full upstream dependency set of a monitor.
// All targets must exist in the tenant; edges that would create a cycle
// (directly or transitively) are rejected with ErrDependencyCycle.
func (s *Service) SetDependencies(ctx context.Context, tenantID, monitorID uuid.UUID, dependsOnIDs []uuid.UUID) error {
	seen := make(map[uuid.UUID]bool, len(dependsOnIDs))
	deduped := make([]uuid.UUID, 0, len(dependsOnIDs))
	for _, id := range dependsOnIDs {
		if id == monitorID {
			return fmt.Errorf("%w: monitor cannot depend on itself", ErrDependencyCycle)
		}
		if !seen[id] {
			seen[id] = true
			deduped = append(deduped, id)
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin dependency transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var exists bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM monitors WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)
	`, monitorID, tenantID).Scan(&exists); err != nil {
		return fmt.Errorf("verify monitor: %w", err)
	}
	if !exists {
		return ErrMonitorNotFound
	}

	if len(deduped) > 0 {
		idStrings := make([]string, len(deduped))
		for i, id := range deduped {
			idStrings[i] = id.String()
		}
		var count int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM monitors
			WHERE id = ANY($1::uuid[]) AND tenant_id = $2 AND deleted_at IS NULL
		`, pq.Array(idStrings), tenantID).Scan(&count); err != nil {
			return fmt.Errorf("verify dependency targets: %w", err)
		}
		if count != len(deduped) {
			return fmt.Errorf("%w: one or more dependency targets do not exist", ErrMonitorNotFound)
		}
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM monitor_dependencies WHERE monitor_id = $1`, monitorID); err != nil {
		return fmt.Errorf("clear dependencies: %w", err)
	}

	for _, dependsOnID := range deduped {
		// Adding edge monitor→dependsOn creates a cycle iff the monitor is
		// already transitively upstream of dependsOn. Checked after the DELETE
		// so replacing a set that reorders edges doesn't false-positive, and
		// after earlier inserts so the new set is internally consistent too.
		var cycle bool
		if err := tx.QueryRowContext(ctx, `
			WITH RECURSIVE upstream AS (
				SELECT md.depends_on_id
				FROM monitor_dependencies md
				WHERE md.monitor_id = $1
				UNION
				SELECT md.depends_on_id
				FROM upstream u
				JOIN monitor_dependencies md ON md.monitor_id = u.depends_on_id
			)
			SELECT EXISTS (SELECT 1 FROM upstream WHERE depends_on_id = $2)
		`, dependsOnID, monitorID).Scan(&cycle); err != nil {
			return fmt.Errorf("validate dependency cycle: %w", err)
		}
		if cycle {
			var name string
			if err := tx.QueryRowContext(ctx,
				`SELECT name FROM monitors WHERE id = $1`, dependsOnID).Scan(&name); err != nil {
				name = dependsOnID.String()
			}
			return fmt.Errorf("%w: %q already depends on this monitor", ErrDependencyCycle, name)
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO monitor_dependencies (monitor_id, depends_on_id, created_at)
			VALUES ($1, $2, NOW())
			ON CONFLICT (monitor_id, depends_on_id) DO NOTHING
		`, monitorID, dependsOnID); err != nil {
			return fmt.Errorf("insert dependency: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit dependency transaction: %w", err)
	}
	return nil
}

// AddDependency creates a single dependency edge: monitor depends on dependsOn.
// Used by the interactive graph editor; SetDependencies remains the bulk path.
func (s *Service) AddDependency(ctx context.Context, tenantID, monitorID, dependsOnID uuid.UUID) error {
	if monitorID == dependsOnID {
		return fmt.Errorf("%w: monitor cannot depend on itself", ErrDependencyCycle)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin dependency transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM monitors
		WHERE id IN ($1, $2) AND tenant_id = $3 AND deleted_at IS NULL
	`, monitorID, dependsOnID, tenantID).Scan(&count); err != nil {
		return fmt.Errorf("verify monitors: %w", err)
	}
	if count != 2 {
		return ErrMonitorNotFound
	}

	var cycle bool
	if err := tx.QueryRowContext(ctx, `
		WITH RECURSIVE upstream AS (
			SELECT md.depends_on_id
			FROM monitor_dependencies md
			WHERE md.monitor_id = $1
			UNION
			SELECT md.depends_on_id
			FROM upstream u
			JOIN monitor_dependencies md ON md.monitor_id = u.depends_on_id
		)
		SELECT EXISTS (SELECT 1 FROM upstream WHERE depends_on_id = $2)
	`, dependsOnID, monitorID).Scan(&cycle); err != nil {
		return fmt.Errorf("validate dependency cycle: %w", err)
	}
	if cycle {
		var name string
		if err := tx.QueryRowContext(ctx,
			`SELECT name FROM monitors WHERE id = $1`, dependsOnID).Scan(&name); err != nil {
			name = dependsOnID.String()
		}
		return fmt.Errorf("%w: %q already depends on this monitor", ErrDependencyCycle, name)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO monitor_dependencies (monitor_id, depends_on_id, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (monitor_id, depends_on_id) DO NOTHING
	`, monitorID, dependsOnID); err != nil {
		return fmt.Errorf("insert dependency: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit dependency transaction: %w", err)
	}
	return nil
}

// RemoveDependency deletes a single dependency edge. Removing an edge that
// does not exist is a no-op, not an error (idempotent unlink).
func (s *Service) RemoveDependency(ctx context.Context, tenantID, monitorID, dependsOnID uuid.UUID) error {
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM monitor_dependencies md
		USING monitors m
		WHERE md.monitor_id = $1 AND md.depends_on_id = $2
		  AND m.id = md.monitor_id AND m.tenant_id = $3
	`, monitorID, dependsOnID, tenantID); err != nil {
		return fmt.Errorf("remove dependency: %w", err)
	}
	return nil
}

// GetDependencies returns the monitors the given monitor depends on (upstream).
func (s *Service) GetDependencies(ctx context.Context, tenantID, monitorID uuid.UUID) ([]models.DependencyMonitor, error) {
	return s.listRelated(ctx, tenantID, monitorID, `
		SELECT m.id, m.name, m.type, m.current_state, m.last_state_change_at
		FROM monitors m
		JOIN monitor_dependencies md ON md.depends_on_id = m.id
		WHERE md.monitor_id = $1 AND m.tenant_id = $2 AND m.deleted_at IS NULL
		ORDER BY m.name
	`)
}

// GetDependents returns the monitors that depend on the given monitor (downstream).
func (s *Service) GetDependents(ctx context.Context, tenantID, monitorID uuid.UUID) ([]models.DependencyMonitor, error) {
	return s.listRelated(ctx, tenantID, monitorID, `
		SELECT m.id, m.name, m.type, m.current_state, m.last_state_change_at
		FROM monitors m
		JOIN monitor_dependencies md ON md.monitor_id = m.id
		WHERE md.depends_on_id = $1 AND m.tenant_id = $2 AND m.deleted_at IS NULL
		ORDER BY m.name
	`)
}

func (s *Service) listRelated(ctx context.Context, tenantID, monitorID uuid.UUID, query string) ([]models.DependencyMonitor, error) {
	rows, err := s.db.QueryContext(ctx, query, monitorID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list dependencies: %w", err)
	}
	defer rows.Close()

	items := []models.DependencyMonitor{}
	for rows.Next() {
		var dm models.DependencyMonitor
		if err := rows.Scan(&dm.ID, &dm.Name, &dm.Type, &dm.CurrentState, &dm.LastStateChangeAt); err != nil {
			return nil, fmt.Errorf("scan dependency monitor: %w", err)
		}
		items = append(items, dm)
	}
	return items, rows.Err()
}

// GetDependencyGraph returns every monitor in the tenant that participates in
// at least one dependency edge, plus the directed edges (from depends on to).
func (s *Service) GetDependencyGraph(ctx context.Context, tenantID uuid.UUID) (*models.DependencyGraph, error) {
	graph := &models.DependencyGraph{
		Nodes: []models.DependencyMonitor{},
		Edges: []models.DependencyGraphEdge{},
	}

	edgeRows, err := s.db.QueryContext(ctx, `
		SELECT md.monitor_id, md.depends_on_id
		FROM monitor_dependencies md
		JOIN monitors src ON src.id = md.monitor_id
			AND src.tenant_id = $1 AND src.deleted_at IS NULL
		JOIN monitors dst ON dst.id = md.depends_on_id
			AND dst.tenant_id = $1 AND dst.deleted_at IS NULL
		ORDER BY md.created_at
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list dependency edges: %w", err)
	}
	defer edgeRows.Close()

	for edgeRows.Next() {
		var edge models.DependencyGraphEdge
		if err := edgeRows.Scan(&edge.From, &edge.To); err != nil {
			return nil, fmt.Errorf("scan dependency edge: %w", err)
		}
		graph.Edges = append(graph.Edges, edge)
	}
	if err := edgeRows.Err(); err != nil {
		return nil, err
	}

	nodeRows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.name, m.type, m.current_state, m.last_state_change_at
		FROM monitors m
		WHERE m.tenant_id = $1 AND m.deleted_at IS NULL
		  AND EXISTS (
			SELECT 1 FROM monitor_dependencies md
			WHERE md.monitor_id = m.id OR md.depends_on_id = m.id)
		ORDER BY m.name
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list dependency nodes: %w", err)
	}
	defer nodeRows.Close()

	for nodeRows.Next() {
		var dm models.DependencyMonitor
		if err := nodeRows.Scan(&dm.ID, &dm.Name, &dm.Type, &dm.CurrentState, &dm.LastStateChangeAt); err != nil {
			return nil, fmt.Errorf("scan dependency node: %w", err)
		}
		graph.Nodes = append(graph.Nodes, dm)
	}
	return graph, nodeRows.Err()
}

// GetDependsOnIDs returns the raw upstream IDs for a monitor, for read
// enrichment of the Monitor model (mirrors monitors repo GetMemberIDs).
func (s *Service) GetDependsOnIDs(ctx context.Context, monitorID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT md.depends_on_id
		FROM monitor_dependencies md
		JOIN monitors m ON m.id = md.depends_on_id
		WHERE md.monitor_id = $1 AND m.deleted_at IS NULL
		ORDER BY md.created_at
	`, monitorID)
	if err != nil {
		return nil, fmt.Errorf("get depends-on IDs: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan depends-on ID: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
