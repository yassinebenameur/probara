// Package locations manages private check locations: tenant-scoped worker
// deployments addressed by per-location NATS subjects.
package locations

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/locationauth"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// ErrNotFound is returned when a location does not exist for the tenant.
var ErrNotFound = errors.New("location not found")

// ErrInvalidWorkerCredential deliberately does not distinguish a missing,
// disabled, deleted location from a wrong credential.
var ErrInvalidWorkerCredential = errors.New("invalid location worker credential")

// connectedWindow is how fresh last_seen_at must be for a location to count
// as connected. Workers heartbeat every ~15s, so a minute tolerates a few
// missed beats without flapping.
const connectedWindow = time.Minute

// Service handles location business logic.
type Service struct {
	db        *db.Client
	encryptor secrets.Encryptor
}

// NewService creates a new locations service.
func NewService(database *db.Client) *Service {
	return &Service{db: database, encryptor: secrets.NoOpEncryptor{}}
}

// ConfigureEncryption wires the platform at-rest encryptor used to protect
// location credentials in Postgres. Remote workers receive only their own
// credential, never the platform master key.
func (s *Service) ConfigureEncryption(encryptor secrets.Encryptor) {
	if encryptor != nil {
		s.encryptor = encryptor
	}
}

const locationColumns = `
	l.id, l.tenant_id, l.name, l.slug, l.description, l.enabled,
	l.last_seen_at, l.mesh_endpoint, l.created_at, l.updated_at,
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

	meshEndpoint, err := normalizeMeshEndpoint(req.MeshEndpoint)
	if err != nil {
		return nil, err
	}

	id := uuid.New()
	credential, err := newWorkerCredential()
	if err != nil {
		return nil, err
	}
	encryptedCredential, err := s.encryptor.Encrypt(credential)
	if err != nil {
		return nil, fmt.Errorf("encrypt location worker credential: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, description, mesh_endpoint, worker_credential)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, id, tenantID, name, slugify(name), req.Description, meshEndpoint, encryptedCredential)
	if err != nil {
		if strings.Contains(err.Error(), "idx_locations_tenant_name") {
			return nil, fmt.Errorf("a location named %q already exists", name)
		}
		return nil, fmt.Errorf("failed to create location: %w", err)
	}
	return s.Get(ctx, tenantID, id)
}

func newWorkerCredential() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("generate location worker credential: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(key), nil
}

// workerCredential returns the location's plaintext credential. Existing
// locations created before migration 77 are issued one atomically on their
// first deploy-info request.
func (s *Service) workerCredential(ctx context.Context, tenantID, locationID uuid.UUID) (string, error) {
	var stored sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT worker_credential
		FROM locations
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, locationID, tenantID).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("load location worker credential: %w", err)
	}
	if !stored.Valid || stored.String == "" {
		credential, err := newWorkerCredential()
		if err != nil {
			return "", err
		}
		encrypted, err := s.encryptor.Encrypt(credential)
		if err != nil {
			return "", fmt.Errorf("encrypt location worker credential: %w", err)
		}
		if err := s.db.QueryRowContext(ctx, `
			UPDATE locations
			SET worker_credential = COALESCE(worker_credential, $1), updated_at = NOW()
			WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
			RETURNING worker_credential
		`, encrypted, locationID, tenantID).Scan(&stored); err != nil {
			return "", fmt.Errorf("issue location worker credential: %w", err)
		}
	}
	credential, err := s.encryptor.Decrypt(stored.String)
	if err != nil {
		return "", fmt.Errorf("decrypt location worker credential: %w", err)
	}
	return credential, nil
}

// AuthenticateWorker validates broker auth-callout credentials for one
// private location. locationID is the NATS username and credential the
// password. Only active locations authenticate.
func (s *Service) AuthenticateWorker(ctx context.Context, locationID, credential string) error {
	id, err := uuid.Parse(locationID)
	if err != nil || credential == "" {
		return ErrInvalidWorkerCredential
	}
	var stored string
	if err := s.db.QueryRowContext(ctx, `
		SELECT worker_credential
		FROM locations
		WHERE id = $1 AND enabled = TRUE AND deleted_at IS NULL
		  AND worker_credential IS NOT NULL
	`, id).Scan(&stored); err != nil {
		return ErrInvalidWorkerCredential
	}
	expected, err := s.encryptor.Decrypt(stored)
	if err != nil {
		return fmt.Errorf("decrypt location worker credential: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(expected), []byte(credential)) != 1 {
		return ErrInvalidWorkerCredential
	}
	return nil
}

// ProtectMonitorConfigForWorker replaces platform-encrypted monitor secrets
// with a location-only envelope before an on-demand or test job leaves the
// trusted platform. The platform master key is never sent to remote workers.
func (s *Service) ProtectMonitorConfigForWorker(ctx context.Context, tenantID, locationID uuid.UUID, monitorType string, config []byte) ([]byte, error) {
	credential, err := s.workerCredential(ctx, tenantID, locationID)
	if err != nil {
		return nil, err
	}
	return s.protectMonitorConfigForCredential(credential, monitorType, config)
}

func (s *Service) protectMonitorConfigForCredential(credential, monitorType string, config []byte) ([]byte, error) {
	if !secrets.HasMonitorSecrets(monitorType) {
		return config, nil
	}
	plaintext, err := secrets.DecryptMonitorConfig(s.encryptor, monitorType, config)
	if err != nil {
		return nil, fmt.Errorf("decrypt monitor config for private location: %w", err)
	}
	locationEncryptor, err := locationauth.ConfigEncryptor(credential)
	if err != nil {
		return nil, fmt.Errorf("derive private-location config key: %w", err)
	}
	protected, err := secrets.EncryptMonitorConfig(locationEncryptor, monitorType, plaintext)
	if err != nil {
		return nil, fmt.Errorf("encrypt private-location monitor config: %w", err)
	}
	return protected, nil
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
	meshEndpoint := existing.MeshEndpoint
	if req.MeshEndpoint != nil {
		meshEndpoint, err = normalizeMeshEndpoint(req.MeshEndpoint)
		if err != nil {
			return nil, err
		}
	}

	_, err = s.db.ExecContext(ctx, `
		UPDATE locations
		SET name = $1, slug = $2, description = $3, enabled = $4, mesh_endpoint = $5, updated_at = NOW()
		WHERE id = $6 AND tenant_id = $7 AND deleted_at IS NULL
	`, name, slug, description, enabled, meshEndpoint, locationID, tenantID)
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

	// S-O1 (docs/state-semantics.md): lock every affected monitor row, in
	// deterministic order, before deleting membership/state rows — result
	// ingest locks the monitor first and then upserts state, so deleting
	// child rows without the monitor locks can deadlock against it or lose
	// to a re-upsert of the just-removed location's state.
	rows, err := tx.QueryContext(ctx, `
		SELECT id FROM monitors
		WHERE id IN (SELECT monitor_id FROM monitor_locations WHERE location_id = $1)
		ORDER BY id
		FOR UPDATE
	`, locationID)
	if err != nil {
		return 0, fmt.Errorf("failed to lock affected monitors: %w", err)
	}
	var affected []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, fmt.Errorf("failed to scan affected monitor: %w", err)
		}
		affected = append(affected, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("error iterating affected monitors: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM monitor_locations WHERE location_id = $1`, locationID); err != nil {
		return 0, fmt.Errorf("failed to detach monitors: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM monitor_location_state WHERE location_id = $1`, locationID); err != nil {
		return 0, fmt.Errorf("failed to clear location state: %w", err)
	}

	// Monitors losing their last location are reset to 'unknown' below;
	// capture them (pre-reset state, memberships already deleted) so the
	// transition lands on their timeline too.
	var resetIDs []uuid.UUID
	if len(affected) > 0 {
		resetRows, err := tx.QueryContext(ctx, `
			SELECT id FROM monitors
			WHERE id = ANY($1)
			  AND current_state <> 'unknown'
			  AND NOT EXISTS (SELECT 1 FROM monitor_locations ml WHERE ml.monitor_id = monitors.id)
		`, pq.Array(affected))
		if err != nil {
			return 0, fmt.Errorf("failed to find monitors losing all locations: %w", err)
		}
		for resetRows.Next() {
			var id uuid.UUID
			if err := resetRows.Scan(&id); err != nil {
				resetRows.Close()
				return 0, fmt.Errorf("failed to scan reset monitor: %w", err)
			}
			resetIDs = append(resetIDs, id)
		}
		resetRows.Close()
		if err := resetRows.Err(); err != nil {
			return 0, fmt.Errorf("error iterating reset monitors: %w", err)
		}
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

	if len(resetIDs) > 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE monitor_state_intervals SET ended_at = NOW()
			WHERE monitor_id = ANY($1) AND ended_at IS NULL
		`, pq.Array(resetIDs)); err != nil {
			return 0, fmt.Errorf("failed to close state intervals: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO monitor_state_intervals (tenant_id, monitor_id, state, reason, started_at)
			SELECT tenant_id, id, 'unknown', 'location_change', NOW()
			FROM monitors WHERE id = ANY($1)
		`, pq.Array(resetIDs)); err != nil {
			return 0, fmt.Errorf("failed to open state intervals: %w", err)
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
		&l.LastSeenAt, &l.MeshEndpoint, &l.CreatedAt, &l.UpdatedAt, &l.MonitorCount,
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

// normalizeMeshEndpoint validates a host:port mesh endpoint. Nil or empty
// clears it (NULL — the location leaves the mesh; the scheduler's edge sync
// removes its edges next tick).
func normalizeMeshEndpoint(raw *string) (*string, error) {
	if raw == nil {
		return nil, nil
	}
	endpoint := strings.TrimSpace(*raw)
	if endpoint == "" {
		return nil, nil
	}
	if len(endpoint) > 255 {
		return nil, fmt.Errorf("mesh_endpoint must be at most 255 characters")
	}
	host, portStr, err := net.SplitHostPort(endpoint)
	if err != nil || strings.TrimSpace(host) == "" {
		return nil, fmt.Errorf("mesh_endpoint must be host:port")
	}
	if port, err := strconv.Atoi(portStr); err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("mesh_endpoint port must be between 1 and 65535")
	}
	return &endpoint, nil
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
