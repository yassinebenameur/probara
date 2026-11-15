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

	"github.com/yassinebenameur/probara/api/internal/apperr"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/pagination"
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
	previewDefaultCount = 5
	previewMaxCount     = 20

	errTimesWithRecurrence = "starts_at and ends_at apply to one-off windows; a repeating window takes its times from recurrence"
)

// Service handles maintenance window business logic. A window is a
// schedule; its occurrences (shared/maintenance) are what covers monitors
// (docs/state-semantics.md S-M5), and every write here keeps them in step in
// the same transaction, under the window's row lock where it changes an
// existing window.
type Service struct {
	db *db.Client
}

// NewService creates a new maintenance window service.
func NewService(db *db.Client) *Service {
	return &Service{db: db}
}

// ruleFromRequest turns a wire recurrence into a normalized rule; nil for
// frequency "none".
func ruleFromRequest(rec *models.MaintenanceRecurrence) (*maintenance.Rule, error) {
	switch strings.TrimSpace(rec.Frequency) {
	case models.MaintenanceRecurrenceNone:
		if len(rec.Weekdays) > 0 || rec.StartTime != "" || rec.DurationMinutes != 0 || rec.Timezone != "" || rec.Until != "" {
			return nil, invalid("recurrence fields apply to frequency %q only", models.MaintenanceRecurrenceWeekly)
		}
		return nil, nil
	case models.MaintenanceRecurrenceWeekly:
	default:
		return nil, invalid("recurrence frequency must be %q or %q", models.MaintenanceRecurrenceWeekly, models.MaintenanceRecurrenceNone)
	}
	minute, err := maintenance.ParseStartTime(rec.StartTime)
	if err != nil {
		return nil, &InvalidError{Msg: err.Error()}
	}
	r := maintenance.Rule{
		StartMinute: minute,
		Duration:    time.Duration(rec.DurationMinutes) * time.Minute,
		Timezone:    rec.Timezone,
		Until:       rec.Until,
	}
	for _, d := range rec.Weekdays {
		r.Weekdays = append(r.Weekdays, time.Weekday(d))
	}
	n, err := r.Normalize()
	if err != nil {
		return nil, &InvalidError{Msg: err.Error()}
	}
	return &n, nil
}

func recurrenceOf(r *maintenance.Rule) *models.MaintenanceRecurrence {
	if r == nil {
		return nil
	}
	rec := &models.MaintenanceRecurrence{
		Frequency:       models.MaintenanceRecurrenceWeekly,
		StartTime:       maintenance.FormatStartTime(r.StartMinute),
		DurationMinutes: int(r.Duration / time.Minute),
		Timezone:        r.Timezone,
		Until:           r.Until,
	}
	for _, d := range r.Weekdays {
		rec.Weekdays = append(rec.Weekdays, int(d))
	}
	return rec
}

// ruleError maps a rule that does not expand to a 400. Normalize already
// enforces the bound Expand checks, so this is a guard.
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
// targets and its occurrences.
func (s *Service) Create(ctx context.Context, tenantID uuid.UUID, req *models.CreateMaintenanceWindowRequest) (*models.MaintenanceWindow, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}
	var sched maintenance.Schedule
	var rule *maintenance.Rule
	if req.Recurrence != nil {
		var err error
		if rule, err = ruleFromRequest(req.Recurrence); err != nil {
			return nil, err
		}
	}
	if rule != nil {
		if !req.StartsAt.IsZero() || !req.EndsAt.IsZero() {
			return nil, invalid(errTimesWithRecurrence)
		}
		sched = maintenance.Schedule{Repeat: rule}
		ok, err := sched.HasFuture(time.Now())
		if err != nil {
			return nil, ruleError(err)
		}
		if !ok {
			return nil, invalid("the recurrence has no occurrence after now")
		}
	} else {
		if !req.EndsAt.After(req.StartsAt) {
			return nil, fmt.Errorf("ends_at must be after starts_at")
		}
		if !req.EndsAt.After(time.Now()) {
			return nil, fmt.Errorf("ends_at must be in the future")
		}
		sched = maintenance.Schedule{Start: req.StartsAt, End: req.EndsAt}
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
	now, err := txNow(ctx, tx)
	if err != nil {
		return nil, err
	}
	if rule != nil {
		// Placeholder until the first occurrence is known (below).
		sched.Start, sched.End = now, now.Add(rule.Duration)
	}

	windowID := uuid.New()
	args := append([]interface{}{windowID, tenantID, title, req.Description}, maintenance.ScheduleArgs(sched)...)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO maintenance_windows (id, tenant_id, title, description, starts_at, ends_at,
			recurrence_weekdays, recurrence_start_time, recurrence_duration_minutes,
			recurrence_timezone, recurrence_until)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, args...); err != nil {
		return nil, fmt.Errorf("failed to create maintenance window: %w", err)
	}
	if err := insertMonitorLinksTx(ctx, tx, windowID, monitorIDs); err != nil {
		return nil, err
	}
	first, err := maintenance.Materialize(ctx, tx, tenantID, windowID, sched, now)
	if err != nil {
		return nil, ruleError(err)
	}
	if rule != nil && first != nil {
		if err := setFirstOccurrenceTx(ctx, tx, windowID, *first); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}
	return s.Get(ctx, tenantID, windowID)
}

// setFirstOccurrenceTx stores a repeating window's first occurrence of its
// current schedule as its starts_at/ends_at (informational, S-M5).
func setFirstOccurrenceTx(ctx context.Context, tx *sql.Tx, windowID uuid.UUID, o maintenance.Occurrence) error {
	if _, err := tx.ExecContext(ctx,
		`UPDATE maintenance_windows SET starts_at = $2, ends_at = $3 WHERE id = $1`,
		windowID, o.Start.UTC(), o.End.UTC()); err != nil {
		return fmt.Errorf("store first maintenance occurrence: %w", err)
	}
	return nil
}

// Preview returns a recurrence's next occurrences without saving anything
// (POST /maintenance-windows/preview).
func (s *Service) Preview(ctx context.Context, req *models.PreviewMaintenanceRecurrenceRequest) (*models.PreviewMaintenanceRecurrenceResponse, error) {
	rule, err := ruleFromRequest(&req.Recurrence)
	if err != nil {
		return nil, err
	}
	if rule == nil {
		return nil, invalid("preview needs a %q recurrence", models.MaintenanceRecurrenceWeekly)
	}
	count := req.Count
	if count <= 0 {
		count = previewDefaultCount
	}
	if count > previewMaxCount {
		count = previewMaxCount
	}
	now := time.Now()
	occ, err := rule.Next(now, count)
	if err != nil {
		return nil, ruleError(err)
	}
	out := &models.PreviewMaintenanceRecurrenceResponse{Occurrences: make([]models.MaintenanceOccurrence, 0, len(occ))}
	for _, o := range occ {
		status := models.MaintenanceOccurrenceUpcoming
		if !o.Start.After(now) {
			status = models.MaintenanceOccurrenceActive
		}
		out.Occurrences = append(out.Occurrences, models.MaintenanceOccurrence{StartsAt: o.Start, EndsAt: o.End, Status: status})
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
	page, pageSize = pagination.Normalize(page, pageSize, pagination.DefaultSize, pagination.MaxSize)
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

// lockWindowTx locks a live window's row (the lock the horizon roller also
// takes) and returns its title, description and schedule.
func lockWindowTx(ctx context.Context, tx *sql.Tx, tenantID, windowID uuid.UUID) (string, string, maintenance.Schedule, error) {
	var title, description string
	var sc maintenance.ScheduleScanner
	err := tx.QueryRowContext(ctx, `
		SELECT mw.title, mw.description, `+maintenance.ScheduleColumns("mw")+`
		FROM maintenance_windows mw
		WHERE mw.id = $1 AND mw.tenant_id = $2 AND mw.deleted_at IS NULL
		FOR UPDATE`, windowID, tenantID).Scan(append([]interface{}{&title, &description}, sc.Dest()...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", maintenance.Schedule{}, ErrNotFound
	}
	if err != nil {
		return "", "", maintenance.Schedule{}, fmt.Errorf("lock maintenance window: %w", err)
	}
	sched, err := sc.Schedule()
	return title, description, sched, err
}

// Update partially updates a window. A changed schedule and a changed target
// set both apply as of now, under the window's row lock: occurrences that
// have started keep their start, the one in progress takes the end the new
// schedule gives the present (or ends now), future ones are regenerated
// (maintenance.Reschedule); a removed monitor stays covered by everything
// before the edit, an added one is covered from it on (maintenance.Retarget).
func (s *Service) Update(ctx context.Context, tenantID, windowID uuid.UUID, req *models.UpdateMaintenanceWindowRequest) (*models.MaintenanceWindow, error) {
	var monitorIDs []uuid.UUID
	if req.MonitorIDs != nil {
		var err error
		if monitorIDs, err = parseMonitorIDs(*req.MonitorIDs); err != nil {
			return nil, err
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	title, description, old, err := lockWindowTx(ctx, tx, tenantID, windowID)
	if err != nil {
		return nil, err
	}
	if req.Title != nil {
		title = strings.TrimSpace(*req.Title)
		if title == "" {
			return nil, fmt.Errorf("title is required")
		}
	}
	if req.Description != nil {
		description = *req.Description
	}
	next, err := nextSchedule(old, req)
	if err != nil {
		return nil, err
	}
	now, err := txNow(ctx, tx)
	if err != nil {
		return nil, err
	}

	if !next.SameAs(old) {
		if err := checkNewScheduleTx(ctx, tx, tenantID, windowID, next, now); err != nil {
			return nil, err
		}
		first, err := maintenance.Reschedule(ctx, tx, tenantID, windowID, next, now)
		if err != nil {
			return nil, ruleError(err)
		}
		if next.Repeat != nil && first != nil {
			next.Start, next.End = first.Start, first.End
		}
	}

	args := append([]interface{}{windowID, tenantID, title, description}, maintenance.ScheduleArgs(next)...)
	if _, err := tx.ExecContext(ctx, `
		UPDATE maintenance_windows
		SET title = $3, description = $4, starts_at = $5, ends_at = $6,
			recurrence_weekdays = $7, recurrence_start_time = $8, recurrence_duration_minutes = $9,
			recurrence_timezone = $10, recurrence_until = $11, updated_at = NOW()
		WHERE id = $1 AND tenant_id = $2
	`, args...); err != nil {
		return nil, fmt.Errorf("failed to update maintenance window: %w", err)
	}

	if req.MonitorIDs != nil {
		if err := validateMonitorIDsTx(ctx, tx, tenantID, monitorIDs); err != nil {
			return nil, err
		}
		if err := maintenance.Retarget(ctx, tx, windowID, monitorIDs, now); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}
	return s.Get(ctx, tenantID, windowID)
}

// nextSchedule applies an update request to a window's schedule. A sent
// recurrence replaces the whole rule ("none" makes the window a one-off,
// which then needs both times); a repeating window takes no times.
func nextSchedule(old maintenance.Schedule, req *models.UpdateMaintenanceWindowRequest) (maintenance.Schedule, error) {
	repeating := old.Repeat != nil
	var rule *maintenance.Rule
	if req.Recurrence != nil {
		var err error
		if rule, err = ruleFromRequest(req.Recurrence); err != nil {
			return old, err
		}
		repeating = rule != nil
	} else if repeating {
		rule = old.Repeat
	}
	if repeating {
		if req.StartsAt != nil || req.EndsAt != nil {
			return old, invalid(errTimesWithRecurrence)
		}
		return maintenance.Schedule{Start: old.Start, End: old.End, Repeat: rule}, nil
	}
	if old.Repeat != nil && (req.StartsAt == nil || req.EndsAt == nil) {
		return old, invalid("starts_at and ends_at are required to make a repeating window a one-off")
	}
	next := maintenance.Schedule{Start: old.Start, End: old.End}
	if req.StartsAt != nil {
		next.Start = *req.StartsAt
	}
	if req.EndsAt != nil {
		next.End = *req.EndsAt
	}
	if !next.End.After(next.Start) {
		return old, fmt.Errorf("ends_at must be after starts_at")
	}
	return next, nil
}

// checkNewScheduleTx refuses a schedule edit that would never cover anything
// again. A one-off may end at or before now only to end the occurrence in
// progress: rewriting a span that is over would change history.
func checkNewScheduleTx(ctx context.Context, tx *sql.Tx, tenantID, windowID uuid.UUID, next maintenance.Schedule, now time.Time) error {
	ok, err := next.HasFuture(now)
	if err != nil {
		return ruleError(err)
	}
	if ok {
		return nil
	}
	if next.Repeat != nil {
		return invalid("the recurrence has no occurrence after now")
	}
	var running bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM maintenance_window_occurrences
		WHERE window_id = $1 AND tenant_id = $2 AND NOT skipped AND starts_at <= $3 AND ends_at > $3)`,
		windowID, tenantID, now).Scan(&running); err != nil {
		return fmt.Errorf("read maintenance occurrence in progress: %w", err)
	}
	if !running {
		return fmt.Errorf("ends_at must be in the future")
	}
	return nil
}

// EndOccurrence ends the window's occurrence in progress now. A repeating
// window keeps its schedule and runs again at its next occurrence; a one-off
// is over, and its ends_at says so.
func (s *Service) EndOccurrence(ctx context.Context, tenantID, windowID uuid.UUID) (*models.MaintenanceWindow, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	_, _, sched, err := lockWindowTx(ctx, tx, tenantID, windowID)
	if err != nil {
		return nil, err
	}
	now, err := txNow(ctx, tx)
	if err != nil {
		return nil, err
	}
	ended, err := maintenance.EndNow(ctx, tx, tenantID, windowID, now)
	if err != nil {
		return nil, err
	}
	if !ended {
		return nil, invalid("no occurrence of this window is in progress")
	}
	if sched.Repeat == nil {
		if _, err := tx.ExecContext(ctx, `
			UPDATE maintenance_windows SET ends_at = $2, updated_at = NOW()
			WHERE id = $1 AND starts_at < $2 AND ends_at > $2`, windowID, now); err != nil {
			return nil, fmt.Errorf("end maintenance window: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}
	return s.Get(ctx, tenantID, windowID)
}

// SkipNextOccurrence skips a repeating window's next occurrence that has not
// started; the series carries on after it.
func (s *Service) SkipNextOccurrence(ctx context.Context, tenantID, windowID uuid.UUID) (*models.MaintenanceWindow, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	_, _, sched, err := lockWindowTx(ctx, tx, tenantID, windowID)
	if err != nil {
		return nil, err
	}
	if sched.Repeat == nil {
		return nil, invalid("only a repeating window has a next occurrence to skip; edit or delete a one-off window")
	}
	now, err := txNow(ctx, tx)
	if err != nil {
		return nil, err
	}
	skipped, err := maintenance.SkipNext(ctx, tx, tenantID, windowID, now)
	if err != nil {
		return nil, err
	}
	if skipped == nil {
		return nil, invalid("this window has no upcoming occurrence to skip")
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}
	return s.Get(ctx, tenantID, windowID)
}

// ListOccurrences pages through a window's occurrences: "upcoming" (the
// default: in progress and ahead, soonest first, skipped ones included) or
// "past" (ended, most recent first).
func (s *Service) ListOccurrences(ctx context.Context, tenantID, windowID uuid.UUID, status string, page, pageSize int) (*models.MaintenanceOccurrenceListResponse, error) {
	page, pageSize = pagination.Normalize(page, pageSize, pagination.DefaultSize, pagination.MaxSize)
	var where, order string
	switch status {
	case "", models.MaintenanceOccurrenceUpcoming:
		where, order = "o.ends_at > NOW()", "o.starts_at ASC"
	case models.MaintenanceOccurrencePast:
		where, order = "o.ends_at <= NOW()", "o.starts_at DESC"
	default:
		return nil, invalid("invalid status filter %q", status)
	}
	var exists bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM maintenance_windows WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)`,
		windowID, tenantID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("failed to read maintenance window: %w", err)
	}
	if !exists {
		return nil, ErrNotFound
	}
	out := &models.MaintenanceOccurrenceListResponse{Items: []models.MaintenanceOccurrence{}, Page: page, PageSize: pageSize}
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM maintenance_window_occurrences o
		WHERE o.window_id = $1 AND o.tenant_id = $2 AND `+where, windowID, tenantID).Scan(&out.Total); err != nil {
		return nil, fmt.Errorf("failed to count maintenance occurrences: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT o.starts_at, o.ends_at,
			CASE
				WHEN o.skipped THEN 'skipped'
				WHEN o.ends_at <= NOW() THEN 'past'
				WHEN o.starts_at <= NOW() THEN 'active'
				ELSE 'upcoming'
			END
		FROM maintenance_window_occurrences o
		WHERE o.window_id = $1 AND o.tenant_id = $2 AND `+where+`
		ORDER BY `+order+`
		LIMIT $3 OFFSET $4`, windowID, tenantID, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, fmt.Errorf("failed to list maintenance occurrences: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var o models.MaintenanceOccurrence
		if err := rows.Scan(&o.StartsAt, &o.EndsAt, &o.Status); err != nil {
			return nil, fmt.Errorf("failed to scan maintenance occurrence: %w", err)
		}
		out.Items = append(out.Items, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating maintenance occurrences: %w", err)
	}
	return out, nil
}

// Delete removes a maintenance window. It stops covering now: the
// occurrence in progress ends and later ones go. A window that already
// covered time is kept, hidden, as history (SLA reports and the interval
// engine read it back, S-M5); one that never started is removed outright.
func (s *Service) Delete(ctx context.Context, tenantID, windowID uuid.UUID) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	if _, _, _, err := lockWindowTx(ctx, tx, tenantID, windowID); err != nil {
		return err
	}
	now, err := txNow(ctx, tx)
	if err != nil {
		return err
	}
	started, err := maintenance.Retire(ctx, tx, tenantID, windowID, now)
	if err != nil {
		return err
	}
	if started {
		_, err = tx.ExecContext(ctx,
			`UPDATE maintenance_windows SET deleted_at = $2, updated_at = NOW() WHERE id = $1`, windowID, now)
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM maintenance_windows WHERE id = $1`, windowID)
	}
	if err != nil {
		return fmt.Errorf("failed to delete maintenance window: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
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
			return nil, apperr.ErrMonitorNotFound
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
// The lifecycle comes from the occurrences, never the window's own times
// (S-M5): active while one that is not skipped is in progress, upcoming
// while one is ahead, past otherwise. Deleted windows (kept as history) are
// never listed.
func (s *Service) query(ctx context.Context, tenantID uuid.UUID, status string, monitorID uuid.UUID, windowID *uuid.UUID, page, pageSize int) ([]models.MaintenanceWindow, int, error) {
	where := []string{"TRUE"}
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
		orderBy = "mw.next_start ASC, mw.id"
	case "past":
		where = append(where, "mw.phase = 'past'")
		orderBy = "COALESCE(mw.last_end, mw.ends_at) DESC, mw.id"
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

	from := `(
		SELECT p.*,
			CASE
				WHEN p.active_end IS NOT NULL THEN 'active'
				WHEN p.next_start IS NOT NULL THEN 'upcoming'
				ELSE 'past'
			END AS phase
		FROM (
			SELECT w.*,
				(SELECT MAX(o.ends_at) FROM maintenance_window_occurrences o
				 WHERE o.window_id = w.id AND NOT o.skipped AND o.starts_at <= NOW() AND o.ends_at > NOW()) AS active_end,
				(SELECT MIN(o.starts_at) FROM maintenance_window_occurrences o
				 WHERE o.window_id = w.id AND o.starts_at > NOW()) AS next_start,
				(SELECT MAX(o.ends_at) FROM maintenance_window_occurrences o
				 WHERE o.window_id = w.id AND NOT o.skipped AND o.ends_at <= NOW()) AS last_end
			FROM maintenance_windows w
			WHERE w.tenant_id = $1 AND w.deleted_at IS NULL
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
		       nx.starts_at, nx.ends_at,
		       COALESCE((
		           SELECT json_agg(json_build_object('id', m.id, 'name', m.name, 'type', m.type) ORDER BY m.name, m.id)
		           FROM maintenance_window_monitors mwm
		           JOIN monitors m ON m.id = mwm.monitor_id AND m.deleted_at IS NULL
		           WHERE mwm.maintenance_window_id = mw.id), '[]')
		FROM %s
		LEFT JOIN LATERAL (
		    SELECT o.starts_at, o.ends_at FROM maintenance_window_occurrences o
		    WHERE o.window_id = mw.id AND NOT o.skipped AND o.ends_at > NOW()
		    ORDER BY o.starts_at LIMIT 1
		) nx ON TRUE
		WHERE %s
		ORDER BY %s
		LIMIT $%d OFFSET $%d
	`, maintenance.ScheduleColumns("mw"), from, whereClause, orderBy, len(args)-1, len(args))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list maintenance windows: %w", err)
	}
	defer rows.Close()

	now := time.Now()
	windows := make([]models.MaintenanceWindow, 0)
	for rows.Next() {
		var w models.MaintenanceWindow
		var phase string
		var sc maintenance.ScheduleScanner
		var nextStart, nextEnd sql.NullTime
		var monitorsJSON []byte
		dest := append([]interface{}{&w.ID, &w.TenantID, &w.Title, &w.Description, &w.CreatedAt, &w.UpdatedAt, &phase}, sc.Dest()...)
		if err := rows.Scan(append(dest, &nextStart, &nextEnd, &monitorsJSON)...); err != nil {
			return nil, 0, fmt.Errorf("failed to scan maintenance window: %w", err)
		}
		sched, err := sc.Schedule()
		if err != nil {
			return nil, 0, err
		}
		w.StartsAt, w.EndsAt = sched.Start, sched.End
		w.Status = models.MaintenanceWindowStatus(phase)
		w.Recurrence = recurrenceOf(sched.Repeat)
		if nextStart.Valid && nextEnd.Valid {
			o := models.MaintenanceOccurrence{StartsAt: nextStart.Time, EndsAt: nextEnd.Time, Status: models.MaintenanceOccurrenceUpcoming}
			if !o.StartsAt.After(now) {
				o.Status = models.MaintenanceOccurrenceActive
			}
			w.NextOccurrence = &o
		}
		if err := json.Unmarshal(monitorsJSON, &w.Monitors); err != nil {
			return nil, 0, fmt.Errorf("decode window monitors: %w", err)
		}
		for _, m := range w.Monitors {
			w.MonitorIDs = append(w.MonitorIDs, m.ID)
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
		return nil, fmt.Errorf("at least one monitor is required")
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
		return fmt.Errorf("one or more monitors not found")
	}
	return nil
}

// insertMonitorLinksTx links a new window's monitors. Links made with the
// window cover all of its occurrences (linked_at keeps its '-infinity'
// default); later target edits go through maintenance.Retarget.
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
