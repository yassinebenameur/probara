package maintenancewindows

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/maintenance"
)

// ErrNotFound is returned when a maintenance window does not exist for the tenant.
var ErrNotFound = errors.New("maintenance window not found")

// InvalidError is a request the caller must fix (HTTP 400). Its message is
// shown to the user as is.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

func invalid(format string, args ...interface{}) error {
	return &InvalidError{Msg: fmt.Sprintf(format, args...)}
}

const (
	previewDefaultCount = 10
	previewMaxCount     = 50
	// listedOccurrences caps MaintenanceWindow.Occurrences.
	listedOccurrences = 5
)

// Service handles maintenance window business logic. A window is a rule;
// its occurrences (shared/maintenance) are what covers monitors (S-M5), and
// every write here keeps them in step in the same transaction.
type Service struct {
	db *db.Client
}

// NewService creates a new maintenance window service.
func NewService(db *db.Client) *Service {
	return &Service{db: db}
}

// buildRule turns the wire fields into a normalized rule. A nil recurrence
// is a one-off.
func buildRule(startsAt, endsAt time.Time, rec *models.MaintenanceRecurrence) (maintenance.Rule, error) {
	r := maintenance.Rule{StartsAt: startsAt, Duration: endsAt.Sub(startsAt)}
	if rec != nil {
		r.Frequency = maintenance.Frequency(strings.TrimSpace(rec.Frequency))
		r.Interval = rec.Interval
		for _, d := range rec.Weekdays {
			r.Weekdays = append(r.Weekdays, time.Weekday(d))
		}
		r.MonthDay = rec.MonthDay
		r.Cron = rec.Cron
		r.Timezone = strings.TrimSpace(rec.Timezone)
		r.Until = rec.Until
	}
	n, err := r.Normalize()
	if err != nil {
		return n, &InvalidError{Msg: err.Error()}
	}
	return n, nil
}

func recurrenceOf(r maintenance.Rule) models.MaintenanceRecurrence {
	rec := models.MaintenanceRecurrence{
		Frequency: string(r.Frequency),
		Interval:  r.Interval,
		MonthDay:  r.MonthDay,
		Cron:      r.Cron,
		Timezone:  r.Timezone,
		Until:     r.Until,
	}
	if rec.Frequency == "" {
		rec.Frequency = string(maintenance.FrequencyNone)
	}
	for _, d := range r.Weekdays {
		rec.Weekdays = append(rec.Weekdays, int(d))
	}
	return rec
}

// ruleKey identifies a rule's schedule, so an edit that leaves it unchanged
// (title, description, monitors) leaves the occurrences alone.
func ruleKey(r maintenance.Rule) string {
	return fmt.Sprint(r.StartsAt.UTC().UnixNano(), r.Duration, maintenance.RuleArgs(r))
}

// checkHasFuture rejects a rule that would never cover anything again.
func checkHasFuture(r maintenance.Rule, now time.Time) error {
	if r.Frequency == maintenance.FrequencyNone {
		if !r.StartsAt.Add(r.Duration).After(now) {
			return invalid("ends_at must be in the future")
		}
		return nil
	}
	next, err := maintenance.Next(r, now, 1)
	if err != nil {
		return &InvalidError{Msg: err.Error()}
	}
	if len(next) == 0 {
		return invalid("the recurrence has no occurrence after now")
	}
	return nil
}

// ruleError maps a rule that does not expand to a 400. Normalize already
// enforces the density Expand checks (maintenance.checkDensity), so this is
// a guard, never the expected path.
func ruleError(err error) error {
	if errors.Is(err, maintenance.ErrTooManyOccurrences) {
		return &InvalidError{Msg: err.Error()}
	}
	return err
}

func txNow(ctx context.Context, tx *sql.Tx) (time.Time, error) {
	var now time.Time
	if err := tx.QueryRowContext(ctx, `SELECT NOW()`).Scan(&now); err != nil {
		return now, fmt.Errorf("read transaction clock: %w", err)
	}
	return now, nil
}

// Create validates and persists a maintenance window with its monitor
// targets and its occurrences up to the horizon.
func (s *Service) Create(ctx context.Context, tenantID uuid.UUID, req *models.CreateMaintenanceWindowRequest) (*models.MaintenanceWindow, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return nil, invalid("title is required")
	}
	if !req.EndsAt.After(req.StartsAt) {
		return nil, invalid("ends_at must be after starts_at")
	}
	rule, err := buildRule(req.StartsAt, req.EndsAt, req.Recurrence)
	if err != nil {
		return nil, err
	}
	if err := checkHasFuture(rule, time.Now()); err != nil {
		return nil, err
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

	windowID := uuid.New()
	args := append([]interface{}{windowID, tenantID, title, req.Description, req.StartsAt, req.EndsAt},
		maintenance.RuleArgs(rule)...)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO maintenance_windows (id, tenant_id, title, description, starts_at, ends_at,
			recurrence, interval_n, by_weekday, by_monthday, cron_expr, timezone, recurrence_until)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`, args...); err != nil {
		return nil, fmt.Errorf("failed to create maintenance window: %w", err)
	}

	if err := insertMonitorLinksTx(ctx, tx, windowID, monitorIDs); err != nil {
		return nil, err
	}
	now, err := txNow(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := maintenance.Materialize(ctx, tx, tenantID, windowID, rule, now); err != nil {
		return nil, ruleError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}
	return s.Get(ctx, tenantID, windowID)
}

// Preview expands a rule without saving it: the next occurrences that end
// after now (POST /maintenance-windows/preview).
func (s *Service) Preview(ctx context.Context, req *models.PreviewMaintenanceWindowRequest) (*models.PreviewMaintenanceWindowResponse, error) {
	if !req.EndsAt.After(req.StartsAt) {
		return nil, invalid("ends_at must be after starts_at")
	}
	rule, err := buildRule(req.StartsAt, req.EndsAt, req.Recurrence)
	if err != nil {
		return nil, err
	}
	count := req.Count
	if count <= 0 {
		count = previewDefaultCount
	}
	if count > previewMaxCount {
		count = previewMaxCount
	}
	occ, err := maintenance.Next(rule, time.Now(), count)
	if err != nil {
		return nil, &InvalidError{Msg: err.Error()}
	}
	out := &models.PreviewMaintenanceWindowResponse{
		Recurrence:  recurrenceOf(rule),
		Occurrences: make([]models.MaintenanceOccurrence, 0, len(occ)),
	}
	for _, o := range occ {
		out.Occurrences = append(out.Occurrences, models.MaintenanceOccurrence{StartsAt: o.Start, EndsAt: o.End})
	}
	return out, nil
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

// Update partially updates a window. Both a changed target set and a changed
// schedule (times or recurrence) apply as of now, under the window's row
// lock (which the horizon roller also takes): ended occurrences are history
// and keep what they covered, the one in progress keeps its start, future
// ones are regenerated (maintenance.Reschedule); a removed monitor stays
// covered by everything before the edit, an added one is covered from it on
// (maintenance.Retarget).
func (s *Service) Update(ctx context.Context, tenantID, windowID uuid.UUID, req *models.UpdateMaintenanceWindowRequest) (*models.MaintenanceWindow, error) {
	existing, err := s.Get(ctx, tenantID, windowID)
	if err != nil {
		return nil, err
	}

	title := existing.Title
	if req.Title != nil {
		title = strings.TrimSpace(*req.Title)
		if title == "" {
			return nil, invalid("title is required")
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
		return nil, invalid("ends_at must be after starts_at")
	}
	recurrence := existing.Recurrence
	if req.Recurrence != nil {
		recurrence = *req.Recurrence
	}
	rule, err := buildRule(startsAt, endsAt, &recurrence)
	if err != nil {
		return nil, err
	}
	oldRule, err := buildRule(existing.StartsAt, existing.EndsAt, &existing.Recurrence)
	rescheduled := err != nil || ruleKey(oldRule) != ruleKey(rule)

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

	args := append([]interface{}{title, description, startsAt, endsAt, windowID, tenantID}, maintenance.RuleArgs(rule)...)
	res, err := tx.ExecContext(ctx, `
		UPDATE maintenance_windows
		SET title = $1, description = $2, starts_at = $3, ends_at = $4,
			recurrence = $7, interval_n = $8, by_weekday = $9, by_monthday = $10,
			cron_expr = $11, timezone = $12, recurrence_until = $13, updated_at = NOW()
		WHERE id = $5 AND tenant_id = $6
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to update maintenance window: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}

	now, err := txNow(ctx, tx)
	if err != nil {
		return nil, err
	}
	if req.MonitorIDs != nil {
		if err := validateMonitorIDsTx(ctx, tx, tenantID, monitorIDs); err != nil {
			return nil, err
		}
		if err := maintenance.Retarget(ctx, tx, windowID, monitorIDs, now); err != nil {
			return nil, err
		}
	}
	if rescheduled {
		if err := maintenance.Reschedule(ctx, tx, tenantID, windowID, rule, now); err != nil {
			return nil, ruleError(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}
	return s.Get(ctx, tenantID, windowID)
}

// EndOccurrence ends the window's occurrence in progress now, keeping the
// rule: a repeating window runs again at its next occurrence. (A one-off is
// ended by setting ends_at, which keeps its rule and occurrence in step.)
func (s *Service) EndOccurrence(ctx context.Context, tenantID, windowID uuid.UUID) (*models.MaintenanceWindow, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	var locked uuid.UUID
	err = tx.QueryRowContext(ctx, `
		SELECT id FROM maintenance_windows WHERE id = $1 AND tenant_id = $2 FOR UPDATE`,
		windowID, tenantID).Scan(&locked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock maintenance window: %w", err)
	}
	now, err := txNow(ctx, tx)
	if err != nil {
		return nil, err
	}
	ended, err := maintenance.EndInProgress(ctx, tx, tenantID, windowID, now)
	if err != nil {
		return nil, err
	}
	if !ended {
		return nil, invalid("no occurrence of this window is in progress")
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
		return nil, invalid("snooze end time must be in the future")
	}

	var monitorName string
	err := s.db.QueryRowContext(ctx,
		`SELECT name FROM monitors WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		monitorID, tenantID).Scan(&monitorName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, invalid("monitor not found")
		}
		return nil, fmt.Errorf("failed to fetch monitor: %w", err)
	}

	// Start on the database clock, the one every coverage predicate reads,
	// so the snooze is in force the moment it is created.
	var now time.Time
	if err := s.db.QueryRowContext(ctx, `SELECT NOW()`).Scan(&now); err != nil {
		return nil, fmt.Errorf("read database clock: %w", err)
	}
	return s.Create(ctx, tenantID, &models.CreateMaintenanceWindowRequest{
		Title:      "Snooze: " + monitorName,
		StartsAt:   now,
		EndsAt:     until,
		MonitorIDs: []string{monitorID.String()},
	})
}

// query is the shared SELECT for Get/List. windowID narrows to a single window;
// status filters by derived lifecycle; monitorID filters windows targeting it.
// The lifecycle comes from the occurrences, never the rule (S-M5): active
// while one is in progress; upcoming while one is ahead or the rule still
// repeats; past otherwise.
func (s *Service) query(ctx context.Context, tenantID uuid.UUID, status string, monitorID uuid.UUID, windowID *uuid.UUID, page, pageSize int) ([]models.MaintenanceWindow, int, error) {
	where := []string{"mw.tenant_id = $1"}
	args := []interface{}{tenantID}

	// Each lifecycle lists what an operator reaches for first, so a page cut
	// keeps it: active by soonest end, upcoming by soonest start, past (and
	// the unfiltered list) newest first.
	orderBy := "mw.starts_at DESC, mw.id"
	switch status {
	case "active":
		where = append(where, "mw.phase = 'active'")
		orderBy = "mw.active_end ASC, mw.id"
	case "upcoming":
		where = append(where, "mw.phase = 'upcoming'")
		orderBy = "mw.next_start ASC NULLS LAST, mw.id"
	case "past":
		where = append(where, "mw.phase = 'past'")
		orderBy = "COALESCE(mw.last_end, mw.ends_at) DESC, mw.id"
	case "":
	default:
		return nil, 0, invalid("invalid status filter %q", status)
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

	from := `(
		SELECT p.*,
			CASE
				WHEN p.active_end IS NOT NULL THEN 'active'
				WHEN p.next_start IS NOT NULL
					OR (p.recurrence <> 'none' AND (p.recurrence_until IS NULL OR p.recurrence_until > NOW())) THEN 'upcoming'
				ELSE 'past'
			END AS phase
		FROM (
			SELECT w.*,
				(SELECT MAX(o.ends_at) FROM maintenance_window_occurrences o
				 WHERE o.window_id = w.id AND o.starts_at <= NOW() AND o.ends_at > NOW()) AS active_end,
				(SELECT MIN(o.starts_at) FROM maintenance_window_occurrences o
				 WHERE o.window_id = w.id AND o.starts_at > NOW()) AS next_start,
				(SELECT MAX(o.ends_at) FROM maintenance_window_occurrences o
				 WHERE o.window_id = w.id AND o.ends_at <= NOW()) AS last_end
			FROM maintenance_windows w
			WHERE w.tenant_id = $1
		) p
	) mw`

	var total int
	countQuery := `SELECT COUNT(*) FROM ` + from + ` WHERE ` + whereClause
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count maintenance windows: %w", err)
	}

	args = append(args, pageSize, (page-1)*pageSize)
	query := fmt.Sprintf(`
		SELECT mw.id, mw.tenant_id, mw.title, mw.description, mw.created_at, mw.updated_at, mw.phase,
		       %s,
		       COALESCE((
		           SELECT json_agg(json_build_object('id', m.id, 'name', m.name, 'type', m.type) ORDER BY m.name, m.id)
		           FROM maintenance_window_monitors mwm
		           JOIN monitors m ON m.id = mwm.monitor_id AND m.deleted_at IS NULL
		           WHERE mwm.maintenance_window_id = mw.id), '[]'),
		       COALESCE((
		           SELECT json_agg(json_build_object('starts_at', o.starts_at, 'ends_at', o.ends_at) ORDER BY o.starts_at)
		           FROM (
		               SELECT starts_at, ends_at FROM maintenance_window_occurrences
		               WHERE window_id = mw.id AND ends_at > NOW()
		               ORDER BY starts_at LIMIT %d) o), '[]')
		FROM %s
		WHERE %s
		ORDER BY %s
		LIMIT $%d OFFSET $%d
	`, maintenance.RuleColumnsFor("mw"), listedOccurrences, from, whereClause, orderBy, len(args)-1, len(args))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list maintenance windows: %w", err)
	}
	defer rows.Close()

	windows := make([]models.MaintenanceWindow, 0)
	for rows.Next() {
		var w models.MaintenanceWindow
		var phase string
		var monitorsJSON, occurrencesJSON []byte
		rule, err := maintenance.ScanRule(func(dest ...interface{}) error {
			all := append([]interface{}{&w.ID, &w.TenantID, &w.Title, &w.Description, &w.CreatedAt, &w.UpdatedAt, &phase}, dest...)
			return rows.Scan(append(all, &monitorsJSON, &occurrencesJSON)...)
		})
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan maintenance window: %w", err)
		}
		w.StartsAt = rule.StartsAt
		w.EndsAt = rule.StartsAt.Add(rule.Duration)
		w.Status = models.MaintenanceWindowStatus(phase)
		w.Recurrence = recurrenceOf(rule)
		if err := json.Unmarshal(monitorsJSON, &w.Monitors); err != nil {
			return nil, 0, fmt.Errorf("decode window monitors: %w", err)
		}
		for _, m := range w.Monitors {
			w.MonitorIDs = append(w.MonitorIDs, m.ID)
		}
		if err := json.Unmarshal(occurrencesJSON, &w.Occurrences); err != nil {
			return nil, 0, fmt.Errorf("decode window occurrences: %w", err)
		}
		if w.Occurrences == nil {
			w.Occurrences = []models.MaintenanceOccurrence{}
		}
		windows = append(windows, w)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating maintenance windows: %w", err)
	}
	return windows, total, nil
}

func parseMonitorIDs(raw []string) ([]uuid.UUID, error) {
	if len(raw) == 0 {
		return nil, invalid("at least one monitor is required")
	}
	seen := make(map[uuid.UUID]bool, len(raw))
	ids := make([]uuid.UUID, 0, len(raw))
	for _, s := range raw {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, invalid("invalid monitor id %q", s)
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
		return invalid("one or more monitors not found")
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
