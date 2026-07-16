package maintenancewindows

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/db"
)

// ErrNotFound is returned when a maintenance window does not exist for the tenant.
var ErrNotFound = errors.New("maintenance window not found")

// Service handles maintenance window business logic.
type Service struct {
	db *db.Client
}

// NewService creates a new maintenance window service.
func NewService(db *db.Client) *Service {
	return &Service{db: db}
}

// Create validates and persists a maintenance window with its monitor targets.
func (s *Service) Create(ctx context.Context, tenantID uuid.UUID, req *models.CreateMaintenanceWindowRequest) (*models.MaintenanceWindow, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}
	if !req.EndsAt.After(req.StartsAt) {
		return nil, fmt.Errorf("ends_at must be after starts_at")
	}
	if !req.EndsAt.After(time.Now()) {
		return nil, fmt.Errorf("ends_at must be in the future")
	}
	monitorIDs, err := parseMonitorIDs(req.MonitorIDs)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if err := validateMonitorIDsTx(ctx, tx, tenantID, monitorIDs); err != nil {
		return nil, err
	}

	window := &models.MaintenanceWindow{
		ID:          uuid.New(),
		TenantID:    tenantID,
		Title:       title,
		Description: req.Description,
		StartsAt:    req.StartsAt,
		EndsAt:      req.EndsAt,
		MonitorIDs:  monitorIDs,
	}
	err = tx.QueryRowContext(ctx, `
		INSERT INTO maintenance_windows (id, tenant_id, title, description, starts_at, ends_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at, updated_at
	`, window.ID, tenantID, title, req.Description, req.StartsAt, req.EndsAt).
		Scan(&window.CreatedAt, &window.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to create maintenance window: %w", err)
	}

	if err := insertMonitorLinksTx(ctx, tx, window.ID, monitorIDs); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	window.Status = deriveStatus(window.StartsAt, window.EndsAt, time.Now())
	return s.Get(ctx, tenantID, window.ID)
}

// Get fetches a single maintenance window with its monitor references.
func (s *Service) Get(ctx context.Context, tenantID, windowID uuid.UUID) (*models.MaintenanceWindow, error) {
	windows, _, err := s.query(ctx, tenantID, "", uuid.Nil, &windowID, 1, 1)
	if err != nil {
		return nil, err
	}
	if len(windows) == 0 {
		return nil, ErrNotFound
	}
	return &windows[0], nil
}

// List returns maintenance windows filtered by derived status and/or monitor.
func (s *Service) List(ctx context.Context, tenantID uuid.UUID, status string, monitorID uuid.UUID, page, pageSize int) (*models.MaintenanceWindowListResponse, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	windows, total, err := s.query(ctx, tenantID, status, monitorID, nil, page, pageSize)
	if err != nil {
		return nil, err
	}
	return &models.MaintenanceWindowListResponse{
		Items:    windows,
		Page:     page,
		PageSize: pageSize,
		Total:    total,
	}, nil
}

// Update partially updates a window; when MonitorIDs is present the target set
// is replaced atomically.
func (s *Service) Update(ctx context.Context, tenantID, windowID uuid.UUID, req *models.UpdateMaintenanceWindowRequest) (*models.MaintenanceWindow, error) {
	existing, err := s.Get(ctx, tenantID, windowID)
	if err != nil {
		return nil, err
	}

	title := existing.Title
	if req.Title != nil {
		title = strings.TrimSpace(*req.Title)
		if title == "" {
			return nil, fmt.Errorf("title is required")
		}
	}
	description := existing.Description
	if req.Description != nil {
		description = *req.Description
	}
	startsAt := existing.StartsAt
	if req.StartsAt != nil {
		startsAt = *req.StartsAt
	}
	endsAt := existing.EndsAt
	if req.EndsAt != nil {
		endsAt = *req.EndsAt
	}
	if !endsAt.After(startsAt) {
		return nil, fmt.Errorf("ends_at must be after starts_at")
	}

	var monitorIDs []uuid.UUID
	if req.MonitorIDs != nil {
		monitorIDs, err = parseMonitorIDs(*req.MonitorIDs)
		if err != nil {
			return nil, err
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE maintenance_windows
		SET title = $1, description = $2, starts_at = $3, ends_at = $4, updated_at = NOW()
		WHERE id = $5 AND tenant_id = $6
	`, title, description, startsAt, endsAt, windowID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to update maintenance window: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}

	if req.MonitorIDs != nil {
		if err := validateMonitorIDsTx(ctx, tx, tenantID, monitorIDs); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM maintenance_window_monitors WHERE maintenance_window_id = $1`, windowID); err != nil {
			return nil, fmt.Errorf("failed to clear window monitors: %w", err)
		}
		if err := insertMonitorLinksTx(ctx, tx, windowID, monitorIDs); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}
	return s.Get(ctx, tenantID, windowID)
}

// Delete removes a maintenance window (hard delete; join rows cascade).
func (s *Service) Delete(ctx context.Context, tenantID, windowID uuid.UUID) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM maintenance_windows WHERE id = $1 AND tenant_id = $2`, windowID, tenantID)
	if err != nil {
		return fmt.Errorf("failed to delete maintenance window: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SnoozeMonitor creates a single-monitor window from now until the given time.
func (s *Service) SnoozeMonitor(ctx context.Context, tenantID, monitorID uuid.UUID, until time.Time) (*models.MaintenanceWindow, error) {
	if !until.After(time.Now()) {
		return nil, fmt.Errorf("snooze end time must be in the future")
	}

	var monitorName string
	err := s.db.QueryRowContext(ctx,
		`SELECT name FROM monitors WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		monitorID, tenantID).Scan(&monitorName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("monitor not found")
		}
		return nil, fmt.Errorf("failed to fetch monitor: %w", err)
	}

	return s.Create(ctx, tenantID, &models.CreateMaintenanceWindowRequest{
		Title:      "Snooze: " + monitorName,
		StartsAt:   time.Now(),
		EndsAt:     until,
		MonitorIDs: []string{monitorID.String()},
	})
}

// query is the shared SELECT for Get/List. windowID narrows to a single window;
// status filters by derived lifecycle; monitorID filters windows targeting it.
func (s *Service) query(ctx context.Context, tenantID uuid.UUID, status string, monitorID uuid.UUID, windowID *uuid.UUID, page, pageSize int) ([]models.MaintenanceWindow, int, error) {
	where := []string{"mw.tenant_id = $1"}
	args := []interface{}{tenantID}

	switch status {
	case "active":
		where = append(where, "mw.starts_at <= NOW() AND mw.ends_at > NOW()")
	case "upcoming":
		where = append(where, "mw.starts_at > NOW()")
	case "past":
		where = append(where, "mw.ends_at <= NOW()")
	case "":
	default:
		return nil, 0, fmt.Errorf("invalid status filter %q", status)
	}
	if monitorID != uuid.Nil {
		args = append(args, monitorID)
		where = append(where, fmt.Sprintf(
			`EXISTS (SELECT 1 FROM maintenance_window_monitors f WHERE f.maintenance_window_id = mw.id AND f.monitor_id = $%d)`,
			len(args)))
	}
	if windowID != nil {
		args = append(args, *windowID)
		where = append(where, fmt.Sprintf("mw.id = $%d", len(args)))
	}
	whereClause := strings.Join(where, " AND ")

	var total int
	countQuery := `SELECT COUNT(*) FROM maintenance_windows mw WHERE ` + whereClause
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count maintenance windows: %w", err)
	}

	args = append(args, pageSize, (page-1)*pageSize)
	query := fmt.Sprintf(`
		SELECT mw.id, mw.tenant_id, mw.title, mw.description, mw.starts_at, mw.ends_at,
		       mw.created_at, mw.updated_at,
		       COALESCE(array_agg(m.id ORDER BY m.name) FILTER (WHERE m.id IS NOT NULL), '{}'),
		       COALESCE(array_agg(m.name ORDER BY m.name) FILTER (WHERE m.id IS NOT NULL), '{}'),
		       COALESCE(array_agg(m.type ORDER BY m.name) FILTER (WHERE m.id IS NOT NULL), '{}')
		FROM maintenance_windows mw
		LEFT JOIN maintenance_window_monitors mwm ON mwm.maintenance_window_id = mw.id
		LEFT JOIN monitors m ON m.id = mwm.monitor_id AND m.deleted_at IS NULL
		WHERE %s
		GROUP BY mw.id
		ORDER BY mw.starts_at DESC, mw.id
		LIMIT $%d OFFSET $%d
	`, whereClause, len(args)-1, len(args))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list maintenance windows: %w", err)
	}
	defer rows.Close()

	now := time.Now()
	windows := make([]models.MaintenanceWindow, 0)
	for rows.Next() {
		var w models.MaintenanceWindow
		var ids []string
		var names, types []string
		if err := rows.Scan(
			&w.ID, &w.TenantID, &w.Title, &w.Description, &w.StartsAt, &w.EndsAt,
			&w.CreatedAt, &w.UpdatedAt,
			pq.Array(&ids), pq.Array(&names), pq.Array(&types),
		); err != nil {
			return nil, 0, fmt.Errorf("failed to scan maintenance window: %w", err)
		}
		for i := range ids {
			id, err := uuid.Parse(ids[i])
			if err != nil {
				continue
			}
			w.MonitorIDs = append(w.MonitorIDs, id)
			w.Monitors = append(w.Monitors, models.MaintenanceWindowMonitorRef{
				ID: id, Name: names[i], Type: types[i],
			})
		}
		w.Status = deriveStatus(w.StartsAt, w.EndsAt, now)
		windows = append(windows, w)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating maintenance windows: %w", err)
	}
	return windows, total, nil
}

func deriveStatus(startsAt, endsAt, now time.Time) models.MaintenanceWindowStatus {
	switch {
	case !endsAt.After(now):
		return models.MaintenanceWindowStatusPast
	case startsAt.After(now):
		return models.MaintenanceWindowStatusUpcoming
	default:
		return models.MaintenanceWindowStatusActive
	}
}

func parseMonitorIDs(raw []string) ([]uuid.UUID, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("at least one monitor is required")
	}
	seen := make(map[uuid.UUID]bool, len(raw))
	ids := make([]uuid.UUID, 0, len(raw))
	for _, s := range raw {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("invalid monitor id %q", s)
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// validateMonitorIDsTx ensures every id is a live monitor in the tenant.
func validateMonitorIDsTx(ctx context.Context, tx *sql.Tx, tenantID uuid.UUID, ids []uuid.UUID) error {
	idStrs := make([]string, len(ids))
	for i, id := range ids {
		idStrs[i] = id.String()
	}
	var count int
	err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM monitors
		WHERE id = ANY($1::uuid[]) AND tenant_id = $2 AND deleted_at IS NULL
	`, pq.Array(idStrs), tenantID).Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to validate monitors: %w", err)
	}
	if count != len(ids) {
		return fmt.Errorf("one or more monitors not found")
	}
	return nil
}

func insertMonitorLinksTx(ctx context.Context, tx *sql.Tx, windowID uuid.UUID, ids []uuid.UUID) error {
	for _, monitorID := range ids {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO maintenance_window_monitors (maintenance_window_id, monitor_id)
			VALUES ($1, $2) ON CONFLICT DO NOTHING
		`, windowID, monitorID); err != nil {
			return fmt.Errorf("failed to link monitor: %w", err)
		}
	}
	return nil
}
