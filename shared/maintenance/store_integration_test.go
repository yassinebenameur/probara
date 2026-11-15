//go:build !unit

package maintenance

// Occurrence writers against a real database (docs/state-semantics.md S-M5).
// Every writer takes `now` explicitly, so these tests move time by hand.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

type storeFixture struct {
	ctx     context.Context
	db      *shareddb.Client
	tenant  uuid.UUID
	monitor uuid.UUID
}

func newStoreFixture(t *testing.T) *storeFixture {
	t.Helper()
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	tenant := testutil.InsertTenant(ctx, t, db, "maintenance-store")
	return &storeFixture{ctx: ctx, db: db, tenant: tenant, monitor: testutil.InsertHTTPMonitor(ctx, t, db, tenant, "api")}
}

// window inserts a window with the given schedule, targeting the fixture's
// monitor, and materializes it as the API does at `now`.
func (f *storeFixture) window(t *testing.T, s Schedule, now time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if s.Repeat != nil {
		s.Start, s.End = now, now.Add(s.Repeat.Duration)
	}
	args := append([]interface{}{id, f.tenant}, ScheduleArgs(s)...)
	if _, err := f.db.ExecContext(f.ctx, `
		INSERT INTO maintenance_windows (id, tenant_id, title, starts_at, ends_at,
			recurrence_weekdays, recurrence_start_time, recurrence_duration_minutes,
			recurrence_timezone, recurrence_until)
		VALUES ($1, $2, 'w', $3, $4, $5, $6, $7, $8, $9)`, args...); err != nil {
		t.Fatalf("insert window: %v", err)
	}
	if _, err := f.db.ExecContext(f.ctx,
		`INSERT INTO maintenance_window_monitors (maintenance_window_id, monitor_id) VALUES ($1, $2)`, id, f.monitor); err != nil {
		t.Fatalf("link window: %v", err)
	}
	if _, err := Materialize(f.ctx, f.db, f.tenant, id, s, now); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	return id
}

func (f *storeFixture) occurrences(t *testing.T, id uuid.UUID) []Occurrence {
	t.Helper()
	rows, err := f.db.QueryContext(f.ctx,
		`SELECT starts_at, ends_at FROM maintenance_window_occurrences WHERE window_id = $1 AND NOT skipped ORDER BY starts_at`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []Occurrence
	for rows.Next() {
		var o Occurrence
		if err := rows.Scan(&o.Start, &o.End); err != nil {
			t.Fatal(err)
		}
		out = append(out, o)
	}
	return out
}

// covered is the monitor's maintenance over [from, to) through the shared
// fragment, the way the interval engine reads it.
func (f *storeFixture) covered(t *testing.T, from, to time.Time) []Occurrence {
	t.Helper()
	rows, err := f.db.QueryContext(f.ctx, `
		SELECT mo.starts_at, mo.ends_at
		FROM monitors m CROSS JOIN LATERAL `+OccurrenceRangesSQL("m.tenant_id", "m.id", "$2::timestamptz", "$3::timestamptz")+` mo
		WHERE m.id = $1 ORDER BY mo.starts_at`, f.monitor, from, to)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []Occurrence
	for rows.Next() {
		var o Occurrence
		if err := rows.Scan(&o.Start, &o.End); err != nil {
			t.Fatal(err)
		}
		out = append(out, o)
	}
	return out
}

func dailyAt(hour int, d time.Duration) *Rule {
	return &Rule{Weekdays: []time.Weekday{0, 1, 2, 3, 4, 5, 6}, StartMinute: hour * 60, Duration: d, Timezone: "UTC"}
}

// S-M5: the roll is idempotent, advances with time, and never re-creates
// what was skipped or ended.
func TestRollForward_IdempotentAndAdvancesTheHorizon(t *testing.T) {
	f := newStoreFixture(t)
	now0 := utc(2026, 6, 1, 0, 0)
	id := f.window(t, Schedule{Repeat: dailyAt(2, time.Hour)}, now0)
	initial := len(f.occurrences(t, id))
	if initial != 35 {
		t.Fatalf("materialized %d occurrences, want 35 (daily over 35 days)", initial)
	}

	if n, err := RollForward(f.ctx, f.db, now0); err != nil || n != 0 {
		t.Fatalf("RollForward at the save = %d, %v; want 0", n, err)
	}
	if n, err := RollForward(f.ctx, f.db, now0.Add(7*24*time.Hour)); err != nil || n != 7 {
		t.Fatalf("RollForward a week later = %d, %v; want 7", n, err)
	}
	if n, err := RollForward(f.ctx, f.db, now0.Add(7*24*time.Hour)); err != nil || n != 0 {
		t.Fatalf("second RollForward = %d, %v; want 0", n, err)
	}

	// Skip the last materialized occurrence: rolling must not re-create it.
	if _, err := f.db.ExecContext(f.ctx, `
		UPDATE maintenance_window_occurrences SET skipped = true
		WHERE window_id = $1 AND starts_at = (SELECT MAX(starts_at) FROM maintenance_window_occurrences WHERE window_id = $1)`, id); err != nil {
		t.Fatal(err)
	}
	before := len(f.occurrences(t, id))
	if n, err := RollForward(f.ctx, f.db, now0.Add(7*24*time.Hour+time.Hour)); err != nil || n != 0 {
		t.Fatalf("RollForward after a skip = %d, %v; want 0", n, err)
	}
	if got := len(f.occurrences(t, id)); got != before {
		t.Fatalf("skipped occurrence came back: %d -> %d", before, got)
	}

	// A deleted window and a one-off are never rolled.
	if _, err := f.db.ExecContext(f.ctx, `UPDATE maintenance_windows SET deleted_at = $2 WHERE id = $1`, id, now0); err != nil {
		t.Fatal(err)
	}
	f.window(t, Schedule{Start: now0, End: now0.Add(time.Hour)}, now0)
	if n, err := RollForward(f.ctx, f.db, now0.Add(30*24*time.Hour)); err != nil || n != 0 {
		t.Fatalf("RollForward over a deleted window and a one-off = %d, %v; want 0", n, err)
	}
}

// S-M5: until stops the roll; a roll that missed an occurrence's start
// covers it from the roll on, never before.
func TestRollForward_UntilAndMissedStarts(t *testing.T) {
	f := newStoreFixture(t)
	now0 := utc(2026, 6, 1, 0, 0)
	r := dailyAt(2, 2*time.Hour)
	r.Until = "2026-07-10"
	id := f.window(t, Schedule{Repeat: r}, now0)
	// Materialized through 5 July; the roll adds 6-10 July and stops there.
	if n, err := RollForward(f.ctx, f.db, utc(2026, 7, 1, 0, 0)); err != nil || n != 5 {
		t.Fatalf("RollForward toward until = %d, %v; want 5", n, err)
	}
	occ := f.occurrences(t, id)
	if last := occ[len(occ)-1].Start; !last.Equal(utc(2026, 7, 10, 2, 0)) {
		t.Fatalf("last occurrence starts %v, want the until date", last)
	}

	// Nothing materialized past an occurrence start (scheduler down): the
	// roll during it covers from the roll.
	id2 := f.window(t, Schedule{Repeat: dailyAt(2, 2*time.Hour)}, now0)
	if _, err := f.db.ExecContext(f.ctx, `DELETE FROM maintenance_window_occurrences WHERE window_id = $1 AND starts_at > $2`, id2, utc(2026, 6, 3, 0, 0)); err != nil {
		t.Fatal(err)
	}
	roll := utc(2026, 6, 3, 3, 0)
	if _, err := RollForward(f.ctx, f.db, roll); err != nil {
		t.Fatal(err)
	}
	occ = f.occurrences(t, id2)
	if !occ[2].Start.Equal(roll) || !occ[2].End.Equal(utc(2026, 6, 3, 4, 0)) {
		t.Fatalf("missed occurrence = %+v, want [roll, 04:00)", occ[2])
	}
}

// S-M5: a schedule edit replaces the future only. Started occurrences keep
// their start; the running one takes the end the new schedule gives now.
func TestReschedule_EditsApplyFromNow(t *testing.T) {
	f := newStoreFixture(t)
	now0 := utc(2026, 6, 1, 0, 0)
	id := f.window(t, Schedule{Repeat: dailyAt(2, time.Hour)}, now0)

	// During 5 June's occurrence, move the window to 03:00 for 2 hours: the
	// running one ends at the edit (03:00 has not started), the rest move.
	edit := utc(2026, 6, 5, 2, 30)
	first, err := Reschedule(f.ctx, f.db, f.tenant, id, Schedule{Repeat: dailyAt(3, 2*time.Hour)}, edit)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Start.Equal(utc(2026, 6, 5, 3, 0)) {
		t.Fatalf("first occurrence of the new schedule = %v, want 5 June 03:00", first.Start)
	}
	occ := f.occurrences(t, id)
	for i := 0; i < 4; i++ {
		day := utc(2026, 6, 1+i, 2, 0)
		if !occ[i].Start.Equal(day) || !occ[i].End.Equal(day.Add(time.Hour)) {
			t.Fatalf("history rewritten: occurrence %d = %+v", i, occ[i])
		}
	}
	if !occ[4].Start.Equal(utc(2026, 6, 5, 2, 0)) || !occ[4].End.Equal(edit) {
		t.Fatalf("running occurrence = %+v, want it ended at the edit", occ[4])
	}
	if !occ[5].Start.Equal(utc(2026, 6, 5, 3, 0)) || occ[5].End.Sub(occ[5].Start) != 2*time.Hour {
		t.Fatalf("next occurrence = %+v, want 03:00 for 2h", occ[5])
	}

	// During that 03:00 occurrence, extend to 4 hours: it keeps its start.
	edit = utc(2026, 6, 5, 4, 0)
	if _, err := Reschedule(f.ctx, f.db, f.tenant, id, Schedule{Repeat: dailyAt(3, 4*time.Hour)}, edit); err != nil {
		t.Fatal(err)
	}
	occ = f.occurrences(t, id)
	if !occ[5].Start.Equal(utc(2026, 6, 5, 3, 0)) || !occ[5].End.Equal(utc(2026, 6, 5, 7, 0)) {
		t.Fatalf("extended occurrence = %+v", occ[5])
	}

	// A schedule that covers the present when nothing runs starts at the
	// edit, never earlier.
	id2 := f.window(t, Schedule{Repeat: dailyAt(22, time.Hour)}, now0)
	edit = utc(2026, 6, 2, 10, 30)
	if _, err := Reschedule(f.ctx, f.db, f.tenant, id2, Schedule{Repeat: dailyAt(10, time.Hour)}, edit); err != nil {
		t.Fatal(err)
	}
	cov := f.covered(t, utc(2026, 6, 2, 9, 0), utc(2026, 6, 2, 12, 0))
	if len(cov) != 1 || !cov[0].Start.Equal(edit) || !cov[0].End.Equal(utc(2026, 6, 2, 11, 0)) {
		t.Fatalf("coverage after the edit = %+v, want [edit, 11:00)", cov)
	}
}

// S-M5: end-now and skip-next leave the schedule alone; skipped
// occurrences cover nothing; Retire keeps the past.
func TestEndNowSkipNextAndRetire(t *testing.T) {
	f := newStoreFixture(t)
	now0 := utc(2026, 6, 1, 0, 0)
	id := f.window(t, Schedule{Repeat: dailyAt(2, time.Hour)}, now0)

	ended, err := EndNow(f.ctx, f.db, f.tenant, id, utc(2026, 6, 1, 2, 15))
	if err != nil || !ended {
		t.Fatalf("EndNow = %v, %v", ended, err)
	}
	if ended, _ := EndNow(f.ctx, f.db, f.tenant, id, utc(2026, 6, 1, 2, 20)); ended {
		t.Fatal("EndNow with nothing running reported an end")
	}
	// An occurrence that starts at this very instant is skipped instead.
	if ended, err := EndNow(f.ctx, f.db, f.tenant, id, utc(2026, 6, 2, 2, 0)); err != nil || !ended {
		t.Fatalf("EndNow at a start = %v, %v", ended, err)
	}

	skipped, err := SkipNext(f.ctx, f.db, f.tenant, id, utc(2026, 6, 2, 12, 0))
	if err != nil || skipped == nil || !skipped.Start.Equal(utc(2026, 6, 3, 2, 0)) {
		t.Fatalf("SkipNext = %+v, %v; want 3 June", skipped, err)
	}
	cov := f.covered(t, utc(2026, 6, 1, 0, 0), utc(2026, 6, 5, 0, 0))
	want := []Occurrence{
		{utc(2026, 6, 1, 2, 0), utc(2026, 6, 1, 2, 15)},
		{utc(2026, 6, 4, 2, 0), utc(2026, 6, 4, 3, 0)},
	}
	if len(cov) != len(want) {
		t.Fatalf("coverage = %+v, want %+v", cov, want)
	}
	for i := range want {
		if !cov[i].Start.Equal(want[i].Start) || !cov[i].End.Equal(want[i].End) {
			t.Fatalf("coverage = %+v, want %+v", cov, want)
		}
	}

	started, err := Retire(f.ctx, f.db, f.tenant, id, utc(2026, 6, 4, 2, 30))
	if err != nil || !started {
		t.Fatalf("Retire = %v, %v; want history kept", started, err)
	}
	cov = f.covered(t, utc(2026, 6, 1, 0, 0), utc(2026, 7, 31, 0, 0))
	if len(cov) != 2 || !cov[1].End.Equal(utc(2026, 6, 4, 2, 30)) {
		t.Fatalf("coverage after retire = %+v", cov)
	}

	future := f.window(t, Schedule{Start: utc(2026, 8, 1, 0, 0), End: utc(2026, 8, 1, 1, 0)}, now0)
	if started, err := Retire(f.ctx, f.db, f.tenant, future, now0); err != nil || started {
		t.Fatalf("Retire of a window that never started = %v, %v; want nothing to keep", started, err)
	}
}
