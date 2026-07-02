// Package locations manages private check locations: tenant-scoped worker
// deployments addressed by per-location NATS subjects.
package locations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/db"
)

// ErrNotFound is returned when a location does not exist for the tenant.
var ErrNotFound = errors.New("location not found")

// connectedWindow is how fresh last_seen_at must be for a location to count
// as connected. Workers heartbeat every ~15s, so a minute tolerates a few
// missed beats without flapping.
const connectedWindow = time.Minute

// Service handles location business logic.
type Service struct {
	db *db.Client
}

// NewService creates a new locations service.
func NewService(database *db.Client) *Service {
	return &Service{db: database}
}

const locationColumns = `
	l.id, l.tenant_id, l.name, l.slug, l.description, l.enabled,
	l.last_seen_at, l.created_at, l.updated_at,
	(SELECT COUNT(*) FROM monitor_locations ml
	 JOIN monitors m ON m.id = ml.monitor_id AND m.deleted_at IS NULL
	 WHERE ml.location_id = l.id) AS monitor_count`

// Create registers a new location.
func (s *Service) Create(ctx context.Context, tenantID uuid.UUID, req *models.CreateLocationRequest) (*models.Location, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if len(name) > 100 {
		return nil, fmt.Errorf("name must be at most 100 characters")
	}

	id := uuid.New()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, description)
		VALUES ($1, $2, $3, $4, $5)
	`, id, tenantID, name, slugify(name), req.Description)
	if err != nil {
		if strings.Contains(err.Error(), "idx_locations_tenant_name") {
			return nil, fmt.Errorf("a location named %q already exists", name)
		}
		return nil, fmt.Errorf("failed to create location: %w", err)
	}
	return s.Get(ctx, tenantID, id)
}

// Get fetches a single location.
func (s *Service) Get(ctx context.Context, tenantID, locationID uuid.UUID) (*models.Location, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+locationColumns+`
		FROM locations l
		WHERE l.id = $1 AND l.tenant_id = $2 AND l.deleted_at IS NULL
	`, locationID, tenantID)

	location, err := scanLocation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get location: %w", err)
	}
	return location, nil
}

// List returns the tenant's locations.
func (s *Service) List(ctx context.Context, tenantID uuid.UUID, page, pageSize int) (*models.LocationListResponse, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}

	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM locations WHERE tenant_id = $1 AND deleted_at IS NULL`,
		tenantID).Scan(&total); err != nil {
		return nil, fmt.Errorf("failed to count locations: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT `+locationColumns+`
		FROM locations l
		WHERE l.tenant_id = $1 AND l.deleted_at IS NULL
		ORDER BY l.name
		LIMIT $2 OFFSET $3
	`, tenantID, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, fmt.Errorf("failed to list locations: %w", err)
	}
	defer rows.Close()

	locations := make([]models.Location, 0)
	for rows.Next() {
		location, err := scanLocation(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan location: %w", err)
		}
		locations = append(locations, *location)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating locations: %w", err)
	}

	return &models.LocationListResponse{
		Items:    locations,
		Page:     page,
		PageSize: pageSize,
		Total:    total,
	}, nil
}

// Update renames or toggles a location.
func (s *Service) Update(ctx context.Context, tenantID, locationID uuid.UUID, req *models.UpdateLocationRequest) (*models.Location, error) {
	existing, err := s.Get(ctx, tenantID, locationID)
	if err != nil {
		return nil, err
	}

	name := existing.Name
	slug := existing.Slug
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
		if name == "" {
			return nil, fmt.Errorf("name is required")
		}
		if len(name) > 100 {
			return nil, fmt.Errorf("name must be at most 100 characters")
		}
		slug = slugify(name)
	}
	description := existing.Description
	if req.Description != nil {
		description = req.Description
	}
	enabled := existing.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	_, err = s.db.ExecContext(ctx, `
		UPDATE locations
		SET name = $1, slug = $2, description = $3, enabled = $4, updated_at = NOW()
		WHERE id = $5 AND tenant_id = $6 AND deleted_at IS NULL
	`, name, slug, description, enabled, locationID, tenantID)
	if err != nil {
		if strings.Contains(err.Error(), "idx_locations_tenant_name") {
			return nil, fmt.Errorf("a location named %q already exists", name)
		}
		return nil, fmt.Errorf("failed to update location: %w", err)
	}
	return s.Get(ctx, tenantID, locationID)
}

// Delete soft-deletes a location and untangles the monitors that used it:
// join and per-location state rows are removed, each affected monitor's
// quorum is clamped to its remaining location count, and monitors dropping
// to zero locations restart the legacy state machine from 'unknown'.
// Returns how many monitors were affected.
func (s *Service) Delete(ctx context.Context, tenantID, locationID uuid.UUID) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		UPDATE locations SET deleted_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, locationID, tenantID)
	if err != nil {
		return 0, fmt.Errorf("failed to delete location: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, ErrNotFound
	}

	rows, err := tx.QueryContext(ctx,
		`DELETE FROM monitor_locations WHERE location_id = $1 RETURNING monitor_id`, locationID)
	if err != nil {
		return 0, fmt.Errorf("failed to detach monitors: %w", err)
	}
	var affected []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, fmt.Errorf("failed to scan detached monitor: %w", err)
		}
		affected = append(affected, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("error iterating detached monitors: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM monitor_location_state WHERE location_id = $1`, locationID); err != nil {
		return 0, fmt.Errorf("failed to clear location state: %w", err)
	}

	if len(affected) > 0 {
		// Clamp quorum to what's left; monitors with no locations left return
		// to the default fleet and restart from 'unknown' so the legacy state
		// machine isn't seeded with a quorum-derived value.
		if _, err := tx.ExecContext(ctx, `
			UPDATE monitors m
			SET location_quorum = GREATEST(1, LEAST(m.location_quorum, sub.remaining)),
				current_state = CASE WHEN sub.remaining = 0 THEN 'unknown' ELSE m.current_state END,
				consecutive_failures = CASE WHEN sub.remaining = 0 THEN 0 ELSE m.consecutive_failures END,
				updated_at = NOW()
			FROM (
				SELECT m2.id, COUNT(ml.location_id)::int AS remaining
				FROM monitors m2
				LEFT JOIN monitor_locations ml ON ml.monitor_id = m2.id
				WHERE m2.id = ANY($1)
				GROUP BY m2.id
			) sub
			WHERE m.id = sub.id
		`, pq.Array(affected)); err != nil {
			return 0, fmt.Errorf("failed to reclamp monitor quorums: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit transaction: %w", err)
	}
	return len(affected), nil
}

// rowScanner is satisfied by *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanLocation(row rowScanner) (*models.Location, error) {
	var l models.Location
	if err := row.Scan(
		&l.ID, &l.TenantID, &l.Name, &l.Slug, &l.Description, &l.Enabled,
		&l.LastSeenAt, &l.CreatedAt, &l.UpdatedAt, &l.MonitorCount,
	); err != nil {
		return nil, err
	}
	l.Connected = IsConnected(l.LastSeenAt)
	return &l, nil
}

// IsConnected reports whether a heartbeat timestamp is fresh enough to show
// the location as connected.
func IsConnected(lastSeenAt *time.Time) bool {
	return lastSeenAt != nil && time.Since(*lastSeenAt) < connectedWindow
}

var slugInvalidChars = regexp.MustCompile(`[^a-z0-9]+`)

// slugify derives a display slug from the name. Slugs are cosmetic — NATS
// subjects key on the location UUID, so collisions are harmless.
func slugify(name string) string {
	slug := strings.Trim(slugInvalidChars.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if slug == "" {
		slug = "location"
	}
	if len(slug) > 60 {
		slug = strings.Trim(slug[:60], "-")
	}
	return slug
}
