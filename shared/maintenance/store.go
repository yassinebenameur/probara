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
//     own past.

import (
	"context"
	"database/sql"
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

// RollForward extends every live repeating window's occurrences to now +
// Horizon and returns how many it added. The scheduler runs it under an
// advisory lock; it is idempotent, so an overlapping run only wastes work.
func RollForward(ctx context.Context, q DB, now time.Time) (int, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT mw.id, mw.tenant_id, `+RuleColumnsFor("mw")+`,
			(SELECT MAX(o.starts_at) FROM maintenance_window_occurrences o WHERE o.window_id = mw.id)
		FROM maintenance_windows mw
		WHERE mw.recurrence <> 'none'
		  AND (mw.recurrence_until IS NULL OR mw.recurrence_until > $1)
		  AND `+tenancy.LiveTenantSQL("mw.tenant_id")+`
		ORDER BY mw.id`, now)
	if err != nil {
		return 0, fmt.Errorf("list repeating maintenance windows: %w", err)
	}
	type pending struct {
		tenantID, windowID uuid.UUID
		occ                []Occurrence
	}
	var work []pending
	for rows.Next() {
		var p pending
		var last sql.NullTime
		r, err := ScanRule(func(dest ...interface{}) error {
			return rows.Scan(append(append([]interface{}{&p.windowID, &p.tenantID}, dest...), &last)...)
		})
		if err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan maintenance rule: %w", err)
		}
		occ, err := Expand(r, now, now.Add(Horizon))
		if err != nil {
			// A stored rule that no longer expands (it was valid when saved)
			// must not stall every other window.
			continue
		}
		for _, o := range occ {
			if last.Valid && !o.Start.After(last.Time) {
				continue
			}
			if o.Start.Before(now) {
				// Missed while nothing rolled: covered from now, never before.
				o.Start = now
			}
			p.occ = append(p.occ, o)
		}
		if len(p.occ) > 0 {
			work = append(work, p)
		}
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate maintenance rules: %w", err)
	}
	added := 0
	for _, p := range work {
		if err := insertOccurrences(ctx, q, p.tenantID, p.windowID, p.occ); err != nil {
			return added, err
		}
		added += len(p.occ)
	}
	return added, nil
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
