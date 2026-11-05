package maintenance

// Occurrence materialization (S-M5). Three writers, one table:
//
//   - Materialize: a new window gets its occurrences up to the horizon (a
//     one-off gets its single occurrence however far ahead it is).
//   - Reschedule: an edit takes effect at the moment it is saved. Occurrences
//     that have ended are history and never change (SLA reports read them).
//     An occurrence in progress keeps its start and takes the end the new
//     rule gives the present moment — so extending a running window, or
//     ending it now, is an ordinary edit — and ends at the edit when the new
//     rule does not cover the present. Every future occurrence is replaced.
//     Maintenance is never declared retroactively: a new occurrence already
//     under way at the edit starts at the edit.
//   - RollForward: the scheduler's pass extends every live repeating rule to
//     the horizon, adding only occurrences that start after the last one
//     materialized, so a rescheduled window is never re-expanded into its
//     own past. It works under the window's row lock, the one every API edit
//     holds, so it never rolls a rule an edit has just replaced.
//
// Targets follow the same edit rule (Retarget): a link covers occurrences
// only from maintenance_window_monitors.linked_at on, and a removed link
// moves to maintenance_window_past_targets with the span it was in force,
// so what an ended occurrence covered never changes either.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/tenancy"
)

// DB is the slice of *sql.DB / *sql.Tx / *sql.Conn the writers need.
type DB interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// RuleColumns is the maintenance_windows select list ScanRule reads, in order.
const RuleColumns = `starts_at, ends_at, recurrence, interval_n, by_weekday, by_monthday, cron_expr, timezone, recurrence_until`

// ScanRule reads a rule selected with RuleColumns (optionally alias-prefixed
// via RuleColumnsFor).
func ScanRule(scan func(dest ...interface{}) error) (Rule, error) {
	var (
		r         Rule
		startsAt  time.Time
		endsAt    time.Time
		frequency string
		weekdays  pq.Int64Array
		monthDay  sql.NullInt64
		cronExpr  sql.NullString
		until     sql.NullTime
	)
	if err := scan(&startsAt, &endsAt, &frequency, &r.Interval, &weekdays, &monthDay, &cronExpr, &r.Timezone, &until); err != nil {
		return r, err
	}
	r.StartsAt = startsAt
	r.Duration = endsAt.Sub(startsAt)
	r.Frequency = Frequency(frequency)
	for _, d := range weekdays {
		r.Weekdays = append(r.Weekdays, time.Weekday(d))
	}
	r.MonthDay = int(monthDay.Int64)
	r.Cron = cronExpr.String
	if until.Valid {
		u := until.Time
		r.Until = &u
	}
	return r, nil
}

// RuleColumnsFor is RuleColumns with every column qualified by alias.
func RuleColumnsFor(alias string) string {
	p := alias + "."
	return p + `starts_at, ` + p + `ends_at, ` + p + `recurrence, ` + p + `interval_n, ` + p + `by_weekday, ` +
		p + `by_monthday, ` + p + `cron_expr, ` + p + `timezone, ` + p + `recurrence_until`
}

// RuleArgs returns the column values for the recurrence columns of
// maintenance_windows, in this order: recurrence, interval_n, by_weekday,
// by_monthday, cron_expr, timezone, recurrence_until. starts_at/ends_at
// (StartsAt and StartsAt+Duration) are written by the caller.
func RuleArgs(r Rule) []interface{} {
	days := make(pq.Int64Array, 0, len(r.Weekdays))
	for _, d := range r.Weekdays {
		days = append(days, int64(d))
	}
	var monthDay, cronExpr, until interface{}
	if r.MonthDay != 0 {
		monthDay = r.MonthDay
	}
	if r.Cron != "" {
		cronExpr = r.Cron
	}
	if r.Until != nil {
		until = r.Until.UTC()
	}
	interval := r.Interval
	if interval < 1 {
		interval = 1
	}
	tz := r.Timezone
	if tz == "" {
		tz = "UTC"
	}
	freq := r.Frequency
	if freq == "" {
		freq = FrequencyNone
	}
	return []interface{}{string(freq), interval, days, monthDay, cronExpr, tz, until}
}

// upcoming expands what a new or edited rule contributes from now on: the
// occurrences overlapping [now, now+Horizon), or a one-off's single
// occurrence if it has not ended, however far ahead it starts.
func upcoming(r Rule, now time.Time) ([]Occurrence, error) {
	if r.Frequency == "" || r.Frequency == FrequencyNone {
		occ := Occurrence{Start: r.StartsAt, End: r.StartsAt.Add(r.Duration)}
		if occ.End.After(now) {
			return []Occurrence{occ}, nil
		}
		return nil, nil
	}
	return Expand(r, now, now.Add(Horizon))
}

// Materialize writes a newly created window's occurrences. Run it in the
// transaction that inserts the window.
func Materialize(ctx context.Context, q DB, tenantID, windowID uuid.UUID, r Rule, now time.Time) error {
	occ, err := upcoming(r, now)
	if err != nil {
		return err
	}
	return insertOccurrences(ctx, q, tenantID, windowID, occ)
}

// Reschedule applies an edited rule to a window's occurrences as of now (see
// the file comment). Run it in the transaction that updates the window, with
// now taken from that transaction's clock.
func Reschedule(ctx context.Context, q DB, tenantID, windowID uuid.UUID, r Rule, now time.Time) error {
	occ, err := upcoming(r, now)
	if err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `
		DELETE FROM maintenance_window_occurrences
		WHERE window_id = $1 AND tenant_id = $2 AND starts_at >= $3`, windowID, tenantID, now); err != nil {
		return fmt.Errorf("clear future maintenance occurrences: %w", err)
	}

	var activeEnd time.Time
	var future []Occurrence
	for _, o := range occ {
		if o.Start.After(now) {
			future = append(future, o)
		} else if o.End.After(activeEnd) {
			activeEnd = o.End
		}
	}

	// In-progress occurrences (started before now, still running).
	newEnd := now
	if !activeEnd.IsZero() {
		newEnd = activeEnd
	}
	res, err := q.ExecContext(ctx, `
		UPDATE maintenance_window_occurrences SET ends_at = $4
		WHERE window_id = $1 AND tenant_id = $2 AND starts_at < $3 AND ends_at > $3`,
		windowID, tenantID, now, newEnd)
	if err != nil {
		return fmt.Errorf("reschedule maintenance occurrence in progress: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 && !activeEnd.IsZero() {
		// The new rule covers the present but nothing was running: it starts
		// now, never earlier.
		future = append([]Occurrence{{Start: now, End: activeEnd}}, future...)
	}
	return insertOccurrences(ctx, q, tenantID, windowID, future)
}

// EndInProgress ends the window's occurrence in progress at now, leaving its
// rule and later occurrences alone ("end this one early"). It reports
// whether an occurrence was running. Run it with now from the caller's
// transaction clock.
func EndInProgress(ctx context.Context, q DB, tenantID, windowID uuid.UUID, now time.Time) (bool, error) {
	// One that started exactly now has no past to keep.
	if _, err := q.ExecContext(ctx, `
		DELETE FROM maintenance_window_occurrences
		WHERE window_id = $1 AND tenant_id = $2 AND starts_at = $3`, windowID, tenantID, now); err != nil {
		return false, fmt.Errorf("end maintenance occurrence: %w", err)
	}
	res, err := q.ExecContext(ctx, `
		UPDATE maintenance_window_occurrences SET ends_at = $3
		WHERE window_id = $1 AND tenant_id = $2 AND starts_at < $3 AND ends_at > $3`, windowID, tenantID, now)
	if err != nil {
		return false, fmt.Errorf("end maintenance occurrence: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// TxDB is a DB that can also begin transactions (*sql.DB, *db.Client).
type TxDB interface {
	DB
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// liveRepeatingSQL selects the windows the roller extends, over alias mw
// with the roll instant as $1.
const liveRepeatingSQL = `mw.recurrence <> 'none'
		  AND (mw.recurrence_until IS NULL OR mw.recurrence_until > $1)`

// RollForward extends every live repeating window's occurrences to now +
// Horizon and returns how many it added. The scheduler runs it under an
// advisory lock; it is idempotent, so an overlapping run only wastes work.
//
// Each window rolls in its own transaction that first locks the window row
// (FOR UPDATE), the lock every API edit holds while it rewrites the rule and
// its occurrences (Update's UPDATE, EndOccurrence's FOR UPDATE, Delete).
// The rule and the last materialized start are read only after the lock is
// granted, so a roll never inserts occurrences of a rule an edit has just
// replaced. A rule that does not expand is skipped, never stalling the other
// windows, and reported in the returned error.
func RollForward(ctx context.Context, q TxDB, now time.Time) (int, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT mw.id FROM maintenance_windows mw
		WHERE `+liveRepeatingSQL+`
		  AND `+tenancy.LiveTenantSQL("mw.tenant_id")+`
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
		return 0, nil // deleted since it was listed
	}
	if err != nil {
		return 0, fmt.Errorf("lock maintenance window: %w", err)
	}
	// A separate statement: under READ COMMITTED it takes a snapshot after
	// the lock was granted, so it sees a concurrent edit's rule and
	// occurrences (a subquery of the locking statement would not).
	var last sql.NullTime
	r, err := ScanRule(func(dest ...interface{}) error {
		return tx.QueryRowContext(ctx, `
			SELECT `+RuleColumnsFor("mw")+`,
				(SELECT MAX(o.starts_at) FROM maintenance_window_occurrences o WHERE o.window_id = mw.id)
			FROM maintenance_windows mw
			WHERE mw.id = $2 AND `+liveRepeatingSQL+`
			  AND `+tenancy.LiveTenantSQL("mw.tenant_id"), now, windowID).Scan(append(dest, &last)...)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil // an edit made it a one-off or ended the series
	}
	if err != nil {
		return 0, fmt.Errorf("read maintenance rule: %w", err)
	}
	occ, err := Expand(r, now, now.Add(Horizon))
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

func insertOccurrences(ctx context.Context, q DB, tenantID, windowID uuid.UUID, occ []Occurrence) error {
	if len(occ) == 0 {
		return nil
	}
	starts := make([]time.Time, len(occ))
	ends := make([]time.Time, len(occ))
	for i, o := range occ {
		starts[i], ends[i] = o.Start.UTC(), o.End.UTC()
	}
	// The window must still exist in the tenant: a roll racing a delete
	// inserts nothing instead of failing on the foreign key.
	_, err := q.ExecContext(ctx, `
		INSERT INTO maintenance_window_occurrences (window_id, tenant_id, starts_at, ends_at)
		SELECT mw.id, mw.tenant_id, o.s, o.e
		FROM maintenance_windows mw
		CROSS JOIN unnest($3::timestamptz[], $4::timestamptz[]) AS o(s, e)
		WHERE mw.id = $1 AND mw.tenant_id = $2
		ON CONFLICT (window_id, starts_at) DO NOTHING`,
		windowID, tenantID, pq.Array(starts), pq.Array(ends))
	if err != nil {
		return fmt.Errorf("insert maintenance occurrences: %w", err)
	}
	return nil
}

// Retarget replaces a window's monitor targets as of now, the way Reschedule
// replaces its schedule: a monitor that stays keeps its link; a new one is
// covered from now on; a removed one keeps covering, through
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
		      WHERE o.window_id = l.maintenance_window_id
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
