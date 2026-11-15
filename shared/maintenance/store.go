package maintenance

// Occurrence materialization (S-M5). Every writer of
// maintenance_window_occurrences lives here:
//
//   - Materialize: a new window gets its occurrences. A one-off gets its
//     single span as given (however far ahead it is); a repeating window
//     gets every occurrence up to the horizon, the one already under way
//     (if any) starting now, never earlier.
//   - Reschedule: a schedule edit takes effect at the moment it is saved.
//     Occurrences that have started are history: their start never moves.
//     The one in progress takes the end the new schedule gives the present
//     moment, or ends at the edit when the new schedule does not cover the
//     present. Every future occurrence is replaced, except that a skipped
//     occurrence the new schedule still has stays skipped. Coverage is
//     never declared retroactively: a new occurrence already under way at
//     the edit starts at the edit.
//   - EndNow / SkipNext: end the occurrence in progress now, or skip the
//     next one, leaving the schedule alone.
//   - Retire: a deleted window stops covering now; what it covered before
//     stays.
//   - RollForward: the scheduler's pass extends every live repeating window
//     to the horizon, adding only occurrences that start after the last one
//     materialized, so it never re-creates a skipped or ended occurrence and
//     never re-expands a rescheduled window into its own past.
//
// Writers that change an existing window run under the window's row lock
// (SELECT ... FOR UPDATE in the API and in rollWindow), so a roll never
// inserts occurrences of a schedule an edit has just replaced.
//
// Targets follow the same edit rule (Retarget): a link covers occurrences
// only from maintenance_window_monitors.linked_at on, and a removed link
// moves to maintenance_window_past_targets with the span it was in force,
// so what an earlier occurrence covered never changes either.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// DB is the slice of *sql.DB / *sql.Tx / *sql.Conn the writers need.
type DB interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// TxDB is a DB that can also begin transactions (*sql.DB, *db.Client).
type TxDB interface {
	DB
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// Schedule is what a window's occurrences come from: a one-off span, or a
// weekly Rule when Repeat is set. For a repeating window Start/End are the
// stored first occurrence of its current schedule, informational only.
type Schedule struct {
	Start  time.Time
	End    time.Time
	Repeat *Rule
}

// SameAs reports whether two schedules generate the same occurrences, so an
// edit that leaves the schedule alone (title, description, monitors) leaves
// the occurrences alone.
func (s Schedule) SameAs(o Schedule) bool {
	if (s.Repeat == nil) != (o.Repeat == nil) {
		return false
	}
	if s.Repeat != nil {
		return s.Repeat.Equal(*o.Repeat)
	}
	return s.Start.Equal(o.Start) && s.End.Equal(o.End)
}

// upcoming is what the schedule covers that ends after now: a one-off's
// span, or a rule's occurrences overlapping [now, now+Horizon).
func (s Schedule) upcoming(now time.Time) ([]Occurrence, error) {
	if s.Repeat == nil {
		if s.End.After(now) {
			return []Occurrence{{Start: s.Start, End: s.End}}, nil
		}
		return nil, nil
	}
	return s.Repeat.Expand(now, now.Add(Horizon))
}

// HasFuture reports whether the schedule covers anything after now.
func (s Schedule) HasFuture(now time.Time) (bool, error) {
	if s.Repeat == nil {
		return s.End.After(now), nil
	}
	next, err := s.Repeat.Next(now, 1)
	return len(next) > 0, err
}

// ScheduleColumns is the maintenance_windows select list a ScheduleScanner
// reads, qualified by alias.
func ScheduleColumns(alias string) string {
	p := alias + "."
	return p + `starts_at, ` + p + `ends_at, ` + p + `recurrence_weekdays, ` +
		`to_char(` + p + `recurrence_start_time, 'HH24:MI'), ` + p + `recurrence_duration_minutes, ` +
		p + `recurrence_timezone, to_char(` + p + `recurrence_until, 'YYYY-MM-DD')`
}

// ScheduleScanner receives the ScheduleColumns of one row:
// rows.Scan(append(other, s.Dest()...)...), then s.Schedule().
type ScheduleScanner struct {
	start, end time.Time
	weekdays   pq.Int64Array
	startTime  sql.NullString
	minutes    sql.NullInt64
	timezone   sql.NullString
	until      sql.NullString
}

// Dest returns the scan destinations, in ScheduleColumns order.
func (s *ScheduleScanner) Dest() []interface{} {
	return []interface{}{&s.start, &s.end, &s.weekdays, &s.startTime, &s.minutes, &s.timezone, &s.until}
}

// Schedule returns the scanned schedule.
func (s *ScheduleScanner) Schedule() (Schedule, error) {
	out := Schedule{Start: s.start, End: s.end}
	if s.weekdays == nil {
		return out, nil
	}
	minute, err := ParseStartTime(s.startTime.String)
	if err != nil {
		return out, fmt.Errorf("stored maintenance start time %q: %w", s.startTime.String, err)
	}
	r := Rule{
		StartMinute: minute,
		Duration:    time.Duration(s.minutes.Int64) * time.Minute,
		Timezone:    s.timezone.String,
		Until:       s.until.String,
	}
	for _, d := range s.weekdays {
		r.Weekdays = append(r.Weekdays, time.Weekday(d))
	}
	out.Repeat = &r
	return out, nil
}

// ScheduleArgs returns the values of the columns ScheduleColumns names, in
// that order (starts_at, ends_at, recurrence_weekdays,
// recurrence_start_time, recurrence_duration_minutes, recurrence_timezone,
// recurrence_until), with NULL recurrence columns for a one-off.
func ScheduleArgs(s Schedule) []interface{} {
	args := []interface{}{s.Start.UTC(), s.End.UTC()}
	if s.Repeat == nil {
		return append(args, nil, nil, nil, nil, nil)
	}
	days := make(pq.Int64Array, 0, len(s.Repeat.Weekdays))
	for _, d := range s.Repeat.Weekdays {
		days = append(days, int64(d))
	}
	var until interface{}
	if s.Repeat.Until != "" {
		until = s.Repeat.Until
	}
	return append(args, days, FormatStartTime(s.Repeat.StartMinute),
		int64(s.Repeat.Duration/time.Minute), s.Repeat.Timezone, until)
}

// Materialize writes a new window's occurrences and returns the first one
// (a repeating window stores it as its starts_at/ends_at). Run it in the
// transaction that inserts the window, with now from that transaction's
// clock.
func Materialize(ctx context.Context, q DB, tenantID, windowID uuid.UUID, s Schedule, now time.Time) (*Occurrence, error) {
	if s.Repeat == nil {
		occ := []Occurrence{{Start: s.Start, End: s.End}}
		return &occ[0], insertOccurrences(ctx, q, tenantID, windowID, occ)
	}
	occ, err := s.upcoming(now)
	if err != nil {
		return nil, err
	}
	for i := range occ {
		if occ[i].Start.Before(now) {
			occ[i].Start = now // already under way: covered from now, never before
		}
	}
	if err := insertOccurrences(ctx, q, tenantID, windowID, occ); err != nil {
		return nil, err
	}
	if len(occ) == 0 {
		return nil, nil
	}
	return &occ[0], nil
}

// Reschedule applies an edited schedule to a window's occurrences as of now
// (see the file comment) and returns the first occurrence of the new
// schedule: the one in progress, else the next. Run it in the transaction
// that updates the window, under its row lock, with now from that
// transaction's clock.
func Reschedule(ctx context.Context, q DB, tenantID, windowID uuid.UUID, s Schedule, now time.Time) (*Occurrence, error) {
	occ, err := s.upcoming(now)
	if err != nil {
		return nil, err
	}
	var active *Occurrence
	var future []Occurrence
	for i, o := range occ {
		if o.Start.After(now) {
			future = append(future, o)
		} else if active == nil || o.End.After(active.End) {
			active = &occ[i]
		}
	}
	keepSkipped := make([]time.Time, len(future))
	for i, o := range future {
		keepSkipped[i] = o.Start.UTC()
	}
	if _, err := q.ExecContext(ctx, `
		DELETE FROM maintenance_window_occurrences
		WHERE window_id = $1 AND tenant_id = $2 AND starts_at >= $3
		  AND NOT (skipped AND starts_at = ANY($4::timestamptz[]))`,
		windowID, tenantID, now, pq.Array(keepSkipped)); err != nil {
		return nil, fmt.Errorf("clear future maintenance occurrences: %w", err)
	}

	newEnd := now
	if active != nil {
		newEnd = active.End
	}
	rows, err := q.QueryContext(ctx, `
		UPDATE maintenance_window_occurrences SET ends_at = $4
		WHERE window_id = $1 AND tenant_id = $2 AND starts_at < $3 AND ends_at > $3 AND NOT skipped
		RETURNING starts_at`, windowID, tenantID, now, newEnd)
	if err != nil {
		return nil, fmt.Errorf("reschedule maintenance occurrence in progress: %w", err)
	}
	var running *time.Time
	for rows.Next() {
		var st time.Time
		if err := rows.Scan(&st); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan maintenance occurrence: %w", err)
		}
		if running == nil || st.Before(*running) {
			running = &st
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate maintenance occurrences: %w", err)
	}

	var first *Occurrence
	switch {
	case running != nil && active != nil:
		first = &Occurrence{Start: *running, End: active.End}
	case active != nil:
		// The new schedule covers the present but nothing is running: the
		// new occurrence starts now, never earlier. For a repeating window,
		// an occurrence of the series that already started (and was ended
		// early) or was skipped since active.Start is that same occurrence,
		// handled already, so an unrelated edit does not revive it. A
		// one-off edited to cover the present again does run again.
		handled := false
		if s.Repeat != nil {
			if err := q.QueryRowContext(ctx, `
				SELECT EXISTS (SELECT 1 FROM maintenance_window_occurrences
				WHERE window_id = $1 AND tenant_id = $2 AND starts_at >= $3 AND starts_at <= $4)`,
				windowID, tenantID, active.Start, now).Scan(&handled); err != nil {
				return nil, fmt.Errorf("read maintenance occurrence in progress: %w", err)
			}
		}
		if !handled {
			start := active.Start
			if start.Before(now) {
				start = now
			}
			future = append([]Occurrence{{Start: start, End: active.End}}, future...)
		}
	}
	if err := insertOccurrences(ctx, q, tenantID, windowID, future); err != nil {
		return nil, err
	}
	if first == nil && len(future) > 0 {
		first = &future[0]
	}
	return first, nil
}

// EndNow ends the window's occurrence in progress at now, leaving its
// schedule and later occurrences alone, and reports whether one was running.
// One that started at this very instant has no past to keep and is skipped
// instead. Run it under the window's row lock with now from the
// transaction's clock.
func EndNow(ctx context.Context, q DB, tenantID, windowID uuid.UUID, now time.Time) (bool, error) {
	res, err := q.ExecContext(ctx, `
		UPDATE maintenance_window_occurrences SET skipped = true
		WHERE window_id = $1 AND tenant_id = $2 AND starts_at = $3 AND NOT skipped`, windowID, tenantID, now)
	if err != nil {
		return false, fmt.Errorf("end maintenance occurrence: %w", err)
	}
	skipped, _ := res.RowsAffected()
	res, err = q.ExecContext(ctx, `
		UPDATE maintenance_window_occurrences SET ends_at = $3
		WHERE window_id = $1 AND tenant_id = $2 AND starts_at < $3 AND ends_at > $3 AND NOT skipped`,
		windowID, tenantID, now)
	if err != nil {
		return false, fmt.Errorf("end maintenance occurrence: %w", err)
	}
	ended, _ := res.RowsAffected()
	return skipped+ended > 0, nil
}

// SkipNext marks the window's next occurrence that has not started (and is
// not already skipped) as skipped, and returns it, or nil when there is
// none. Run it under the window's row lock with now from the transaction's
// clock.
func SkipNext(ctx context.Context, q DB, tenantID, windowID uuid.UUID, now time.Time) (*Occurrence, error) {
	var o Occurrence
	err := q.QueryRowContext(ctx, `
		UPDATE maintenance_window_occurrences SET skipped = true
		WHERE window_id = $1 AND tenant_id = $2 AND starts_at = (
			SELECT MIN(starts_at) FROM maintenance_window_occurrences
			WHERE window_id = $1 AND tenant_id = $2 AND starts_at > $3 AND NOT skipped)
		RETURNING starts_at, ends_at`, windowID, tenantID, now).Scan(&o.Start, &o.End)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("skip maintenance occurrence: %w", err)
	}
	return &o, nil
}

// Retire stops a window being deleted from covering anything after now: the
// occurrence in progress ends now and every later one goes. It reports
// whether the window covered any time before now; such a window is history
// (SLA reports and the interval engine read it back) and must be kept,
// soft-deleted, while one that never started can be removed. Run it under
// the window's row lock with now from the transaction's clock.
func Retire(ctx context.Context, q DB, tenantID, windowID uuid.UUID, now time.Time) (bool, error) {
	if _, err := EndNow(ctx, q, tenantID, windowID, now); err != nil {
		return false, err
	}
	if _, err := q.ExecContext(ctx, `
		DELETE FROM maintenance_window_occurrences
		WHERE window_id = $1 AND tenant_id = $2 AND starts_at >= $3`, windowID, tenantID, now); err != nil {
		return false, fmt.Errorf("remove future maintenance occurrences: %w", err)
	}
	var started bool
	if err := q.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM maintenance_window_occurrences
		WHERE window_id = $1 AND tenant_id = $2 AND starts_at < $3 AND NOT skipped)`,
		windowID, tenantID, now).Scan(&started); err != nil {
		return false, fmt.Errorf("read maintenance history: %w", err)
	}
	return started, nil
}

// liveRepeatingSQL selects the windows the roller extends, over alias mw.
// The until bound is a coarse pre-filter (two days of slack for any zone);
// Expand applies the exact one.
const liveRepeatingSQL = `mw.recurrence_weekdays IS NOT NULL
		  AND mw.deleted_at IS NULL
		  AND (mw.recurrence_until IS NULL OR mw.recurrence_until >= ($1::timestamptz - INTERVAL '2 days')::date)`

// RollForward extends every live repeating window's occurrences to now +
// Horizon and returns how many it added. The scheduler runs it under a
// session advisory lock; it is idempotent, so a second replica or an
// overlapping run only wastes work.
//
// Each window rolls in its own transaction that first locks the window row
// (FOR UPDATE), the lock every API edit holds while it rewrites the schedule
// and its occurrences. The schedule and the last materialized start are read
// only after the lock is granted, so a roll never inserts occurrences of a
// schedule an edit has just replaced. A window whose rule does not expand is
// skipped, never stalling the others, and reported in the returned error.
func RollForward(ctx context.Context, q TxDB, now time.Time) (int, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT mw.id FROM maintenance_windows mw
		WHERE `+liveRepeatingSQL+`
		ORDER BY mw.id`, now)
	if err != nil {
		return 0, fmt.Errorf("list repeating maintenance windows: %w", err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan maintenance window: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate maintenance windows: %w", err)
	}
	added := 0
	var skipped []error
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return added, err
		}
		n, err := rollWindow(ctx, q, id, now)
		var expandErr *rollExpandError
		if errors.As(err, &expandErr) {
			skipped = append(skipped, err)
			continue
		}
		if err != nil {
			return added, err
		}
		added += n
	}
	return added, errors.Join(skipped...)
}

// rollExpandError is a stored rule that no longer expands.
type rollExpandError struct {
	windowID uuid.UUID
	err      error
}

func (e *rollExpandError) Error() string {
	return fmt.Sprintf("maintenance window %s skipped: %v", e.windowID, e.err)
}

func (e *rollExpandError) Unwrap() error { return e.err }

// rollWindow extends one window under its row lock (see RollForward).
func rollWindow(ctx context.Context, q TxDB, windowID uuid.UUID, now time.Time) (int, error) {
	tx, err := q.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin maintenance roll: %w", err)
	}
	defer tx.Rollback()

	var tenantID uuid.UUID
	err = tx.QueryRowContext(ctx, `SELECT tenant_id FROM maintenance_windows WHERE id = $1 FOR UPDATE`, windowID).Scan(&tenantID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil // removed since it was listed
	}
	if err != nil {
		return 0, fmt.Errorf("lock maintenance window: %w", err)
	}
	// A separate statement: under READ COMMITTED it takes a snapshot after
	// the lock was granted, so it sees a concurrent edit's schedule and
	// occurrences (a subquery of the locking statement would not).
	var sc ScheduleScanner
	var last sql.NullTime
	err = tx.QueryRowContext(ctx, `
		SELECT `+ScheduleColumns("mw")+`,
			(SELECT MAX(o.starts_at) FROM maintenance_window_occurrences o WHERE o.window_id = mw.id)
		FROM maintenance_windows mw
		WHERE mw.id = $2 AND `+liveRepeatingSQL, now, windowID).Scan(append(sc.Dest(), &last)...)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil // an edit made it a one-off, or it was deleted
	}
	if err != nil {
		return 0, fmt.Errorf("read maintenance schedule: %w", err)
	}
	s, err := sc.Schedule()
	if err != nil {
		return 0, &rollExpandError{windowID: windowID, err: err}
	}
	occ, err := s.upcoming(now)
	if err != nil {
		return 0, &rollExpandError{windowID: windowID, err: err}
	}
	var add []Occurrence
	for _, o := range occ {
		if last.Valid && !o.Start.After(last.Time) {
			continue
		}
		if o.Start.Before(now) {
			// Missed while nothing rolled: covered from now, never before.
			o.Start = now
		}
		add = append(add, o)
	}
	if len(add) == 0 {
		return 0, nil
	}
	if err := insertOccurrences(ctx, tx, tenantID, windowID, add); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit maintenance roll: %w", err)
	}
	return len(add), nil
}

// insertOccurrences adds occurrences, keeping any row already at the same
// start. A skipped row there stays skipped and takes the new end, so the
// occurrence list shows the span the schedule now gives it.
func insertOccurrences(ctx context.Context, q DB, tenantID, windowID uuid.UUID, occ []Occurrence) error {
	if len(occ) == 0 {
		return nil
	}
	starts := make([]time.Time, len(occ))
	ends := make([]time.Time, len(occ))
	for i, o := range occ {
		starts[i], ends[i] = o.Start.UTC(), o.End.UTC()
	}
	_, err := q.ExecContext(ctx, `
		INSERT INTO maintenance_window_occurrences (window_id, tenant_id, starts_at, ends_at)
		SELECT $1, $2, o.s, o.e
		FROM unnest($3::timestamptz[], $4::timestamptz[]) AS o(s, e)
		ON CONFLICT (window_id, starts_at) DO UPDATE SET ends_at = EXCLUDED.ends_at
		WHERE maintenance_window_occurrences.skipped`,
		windowID, tenantID, pq.Array(starts), pq.Array(ends))
	if err != nil {
		return fmt.Errorf("insert maintenance occurrences: %w", err)
	}
	return nil
}

// Retarget replaces a window's monitor targets as of now, the way
// Reschedule replaces its schedule: a monitor that stays keeps its link; a
// new one is covered from now on; a removed one keeps covering, through
// maintenance_window_past_targets, every occurrence (or part of one) before
// now. Run it in the transaction that updates the window, with now from
// that transaction's clock; ids must already be validated for the tenant.
func Retarget(ctx context.Context, q DB, windowID uuid.UUID, ids []uuid.UUID, now time.Time) error {
	keep := make([]string, len(ids))
	for i, id := range ids {
		keep[i] = id.String()
	}
	// Only a link that covered some occurrence time leaves history behind.
	if _, err := q.ExecContext(ctx, `
		INSERT INTO maintenance_window_past_targets (maintenance_window_id, monitor_id, linked_at, unlinked_at)
		SELECT l.maintenance_window_id, l.monitor_id, l.linked_at, $3
		FROM maintenance_window_monitors l
		WHERE l.maintenance_window_id = $1
		  AND NOT (l.monitor_id = ANY($2::uuid[]))
		  AND l.linked_at < $3
		  AND EXISTS (
		      SELECT 1 FROM maintenance_window_occurrences o
		      WHERE o.window_id = l.maintenance_window_id AND NOT o.skipped
		        AND o.starts_at < $3 AND o.ends_at > l.linked_at)
		ON CONFLICT DO NOTHING`, windowID, pq.Array(keep), now); err != nil {
		return fmt.Errorf("keep removed maintenance targets: %w", err)
	}
	if _, err := q.ExecContext(ctx, `
		DELETE FROM maintenance_window_monitors
		WHERE maintenance_window_id = $1 AND NOT (monitor_id = ANY($2::uuid[]))`,
		windowID, pq.Array(keep)); err != nil {
		return fmt.Errorf("remove maintenance targets: %w", err)
	}
	if _, err := q.ExecContext(ctx, `
		INSERT INTO maintenance_window_monitors (maintenance_window_id, monitor_id, linked_at)
		SELECT $1, m, $3 FROM unnest($2::uuid[]) AS m
		ON CONFLICT (maintenance_window_id, monitor_id) DO NOTHING`,
		windowID, pq.Array(keep), now); err != nil {
		return fmt.Errorf("add maintenance targets: %w", err)
	}
	return nil
}
