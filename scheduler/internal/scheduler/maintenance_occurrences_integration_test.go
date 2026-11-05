//go:build !unit

package scheduler

// Horizon roller tests (docs/state-semantics.md S-M5): repeating windows are
// materialized up to maintenance.Horizon ahead, idempotently, and never into
// the past.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/maintenance"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func insertRuleWindow(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID uuid.UUID, r maintenance.Rule) uuid.UUID {
	t.Helper()
	id := uuid.New()
	args := append([]interface{}{id, tenantID, r.StartsAt, r.StartsAt.Add(r.Duration)}, maintenance.RuleArgs(r)...)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO maintenance_windows (id, tenant_id, title, starts_at, ends_at,
			recurrence, interval_n, by_weekday, by_monthday, cron_expr, timezone, recurrence_until)
		VALUES ($1, $2, 'rolled', $3, $4, $5, $6, $7, $8, $9, $10, $11)`, args...); err != nil {
		t.Fatalf("insert rule window: %v", err)
	}
	return id
}

func occurrenceStarts(ctx context.Context, t *testing.T, db *shareddb.Client, windowID uuid.UUID) []time.Time {
	t.Helper()
	rows, err := db.QueryContext(ctx, `SELECT starts_at FROM maintenance_window_occurrences WHERE window_id = $1 ORDER BY starts_at`, windowID)
	if err != nil {
		t.Fatalf("query occurrences: %v", err)
	}
	defer rows.Close()
	var out []time.Time
	for rows.Next() {
		var ts time.Time
		if err := rows.Scan(&ts); err != nil {
			t.Fatalf("scan occurrence: %v", err)
		}
		out = append(out, ts)
	}
	return out
}

func TestMaintenanceRollerExtendsRepeatingWindows(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := setupRollupTestDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "maint-roll")
	now := time.Now().UTC()
	daily, err := maintenance.Rule{
		StartsAt: now.Truncate(time.Hour).Add(-47 * time.Hour), Duration: 30 * time.Minute,
		Frequency: maintenance.FrequencyDaily,
	}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	dailyID := insertRuleWindow(ctx, t, dbClient, tenantID, daily)
	oneOff, _ := maintenance.Rule{StartsAt: now.Add(60 * 24 * time.Hour), Duration: time.Hour}.Normalize()
	oneOffID := insertRuleWindow(ctx, t, dbClient, tenantID, oneOff)
	ended := now.Add(-time.Hour)
	finished, _ := maintenance.Rule{
		StartsAt: now.Add(-72 * time.Hour), Duration: time.Minute, Frequency: maintenance.FrequencyDaily, Until: &ended,
	}.Normalize()
	finishedID := insertRuleWindow(ctx, t, dbClient, tenantID, finished)

	s := newTestScheduler(dbClient)
	if err := s.rollMaintenanceOccurrences(ctx); err != nil {
		t.Fatalf("roll: %v", err)
	}
	starts := occurrenceStarts(ctx, t, dbClient, dailyID)
	if len(starts) < 35 || len(starts) > 36 {
		t.Fatalf("daily window rolled %d occurrences, want one per day of the 35-day horizon", len(starts))
	}
	// Nothing materialized into the past: the earliest is the one in
	// progress or the next one.
	if starts[0].Before(now.Add(-31*time.Minute)) || starts[len(starts)-1].After(now.Add(maintenance.Horizon)) {
		t.Fatalf("occurrences span %v..%v, outside [now, now+horizon)", starts[0], starts[len(starts)-1])
	}
	if got := occurrenceStarts(ctx, t, dbClient, oneOffID); len(got) != 0 {
		t.Fatalf("the roller must leave one-off windows alone, got %v", got)
	}
	if got := occurrenceStarts(ctx, t, dbClient, finishedID); len(got) != 0 {
		t.Fatalf("a finished series must not roll, got %v", got)
	}

	// Idempotent: a second pass adds nothing.
	if err := s.rollMaintenanceOccurrences(ctx); err != nil {
		t.Fatalf("second roll: %v", err)
	}
	if again := occurrenceStarts(ctx, t, dbClient, dailyID); len(again) != len(starts) {
		t.Fatalf("second roll changed %d -> %d occurrences", len(starts), len(again))
	}

	// Only occurrences after the last materialized one are added: drop the
	// tail and keep a clipped head that does not sit on the rule's grid.
	if _, err := dbClient.ExecContext(ctx, `DELETE FROM maintenance_window_occurrences WHERE window_id = $1 AND starts_at > $2`,
		dailyID, starts[2]); err != nil {
		t.Fatal(err)
	}
	if err := s.rollMaintenanceOccurrences(ctx); err != nil {
		t.Fatalf("third roll: %v", err)
	}
	if again := occurrenceStarts(ctx, t, dbClient, dailyID); len(again) != len(starts) {
		t.Fatalf("refill rolled %d occurrences, want %d", len(again), len(starts))
	}

	// A soft-deleted workspace's windows do not roll.
	if _, err := dbClient.ExecContext(ctx, `DELETE FROM maintenance_window_occurrences WHERE window_id = $1`, dailyID); err != nil {
		t.Fatal(err)
	}
	if _, err := dbClient.ExecContext(ctx, `UPDATE tenants SET deleted_at = NOW() WHERE id = $1`, tenantID); err != nil {
		t.Fatal(err)
	}
	if err := s.rollMaintenanceOccurrences(ctx); err != nil {
		t.Fatalf("roll after delete: %v", err)
	}
	if got := occurrenceStarts(ctx, t, dbClient, dailyID); len(got) != 0 {
		t.Fatalf("deleted workspace rolled %d occurrences", len(got))
	}
}

// A roll that races an API edit waits for the edit's row lock and then rolls
// the edited rule: it never inserts occurrences of the rule the edit
// replaced.
func TestMaintenanceRollerSerializesWithEdits(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := setupRollupTestDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "maint-roll-race")
	now := time.Now().UTC()
	old, err := maintenance.Rule{
		StartsAt: now.Truncate(24 * time.Hour).Add(-48*time.Hour + 2*time.Hour), Duration: 30 * time.Minute,
		Frequency: maintenance.FrequencyDaily,
	}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	windowID := insertRuleWindow(ctx, t, dbClient, tenantID, old)
	s := newTestScheduler(dbClient)
	if err := s.rollMaintenanceOccurrences(ctx); err != nil {
		t.Fatalf("roll: %v", err)
	}
	// Leave the horizon's tail for the racing roll to fill.
	if _, err := dbClient.ExecContext(ctx, `
		DELETE FROM maintenance_window_occurrences WHERE window_id = $1 AND starts_at > $2`,
		windowID, now.Add(20*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	// The API edit (Update): the rule moves from 02:00 to 05:00 UTC.
	edited := old
	edited.StartsAt = old.StartsAt.Add(3 * time.Hour)
	tx, err := dbClient.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE maintenance_windows SET starts_at = $2, ends_at = $3, updated_at = NOW() WHERE id = $1`,
		windowID, edited.StartsAt, edited.StartsAt.Add(edited.Duration)); err != nil {
		t.Fatal(err)
	}
	var editAt time.Time
	if err := tx.QueryRowContext(ctx, `SELECT NOW()`).Scan(&editAt); err != nil {
		t.Fatal(err)
	}
	if err := maintenance.Reschedule(ctx, tx, tenantID, windowID, edited, editAt); err != nil {
		t.Fatal(err)
	}

	rolled := make(chan error, 1)
	go func() { rolled <- s.rollMaintenanceOccurrences(ctx) }()
	// Commit once the roll is waiting on the row lock (or has finished,
	// which is the bug this test guards against).
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		if err := dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_locks WHERE NOT granted`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 || len(rolled) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-rolled; err != nil {
		t.Fatalf("racing roll: %v", err)
	}

	rows, err := dbClient.QueryContext(ctx, `
		SELECT starts_at FROM maintenance_window_occurrences WHERE window_id = $1 AND starts_at > $2 ORDER BY starts_at`,
		windowID, editAt)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var ts time.Time
		if err := rows.Scan(&ts); err != nil {
			t.Fatal(err)
		}
		n++
		if h := ts.UTC().Hour(); h != 5 {
			t.Fatalf("occurrence at %v after the edit: only the edited 05:00 rule may run", ts.UTC())
		}
	}
	if n < 34 {
		t.Fatalf("edited rule has %d future occurrences, want the full horizon", n)
	}
}
