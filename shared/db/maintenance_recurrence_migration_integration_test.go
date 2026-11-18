//go:build !unit

package db_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/testutil"
)

// Migrations 000109 (recurrence + occurrences) and 000110 (target history +
// soft delete) walk down and back up as a rollback and re-upgrade would:
// existing one-off windows survive both ways and are backfilled into
// exactly one occurrence each.
func TestMaintenanceRecurrenceMigrationsUpDownUp(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(file), "migrations")
	driver, err := postgres.WithInstance(dbc.DB, &postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithDatabaseInstance("file://"+dir, "postgres", driver)
	if err != nil {
		t.Fatal(err)
	}
	// Not m.Close(): the driver would close dbc.DB, which cleanup owns.
	migrateTo := func(version uint) {
		t.Helper()
		if err := m.Migrate(version); err != nil && err != migrate.ErrNoChange {
			t.Fatalf("migrate to %d: %v", version, err)
		}
	}
	exec := func(q string, args ...interface{}) {
		t.Helper()
		if _, err := dbc.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// The version just below 000109, whatever number it has.
	var before uint
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if n, err := strconv.Atoi(e.Name()[:6]); err == nil && n < 109 && uint(n) > before {
			before = uint(n)
		}
	}

	migrateTo(before)
	tenant := testutil.InsertTenant(ctx, t, dbc, "migration")
	monitor := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "api")
	now := time.Now().UTC().Truncate(time.Second)
	spans := map[uuid.UUID][2]time.Time{
		uuid.New(): {now.Add(-48 * time.Hour), now.Add(-47 * time.Hour)},
		uuid.New(): {now.Add(24 * time.Hour), now.Add(26 * time.Hour)},
	}
	for id, s := range spans {
		exec(`INSERT INTO maintenance_windows (id, tenant_id, title, starts_at, ends_at) VALUES ($1, $2, 'w', $3, $4)`, id, tenant, s[0], s[1])
		exec(`INSERT INTO maintenance_window_monitors (maintenance_window_id, monitor_id) VALUES ($1, $2)`, id, monitor)
	}

	checkBackfill := func() {
		t.Helper()
		for id, s := range spans {
			var n int
			var start, end time.Time
			if err := dbc.QueryRowContext(ctx, `
				SELECT COUNT(*), MIN(starts_at), MAX(ends_at) FROM maintenance_window_occurrences WHERE window_id = $1`, id).
				Scan(&n, &start, &end); err != nil {
				t.Fatal(err)
			}
			if n != 1 || !start.Equal(s[0]) || !end.Equal(s[1]) {
				t.Fatalf("window %s: %d occurrences %v–%v, want exactly its span %v–%v", id, n, start, end, s[0], s[1])
			}
		}
		var unboundedLinks int
		if err := dbc.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM maintenance_window_monitors WHERE linked_at = '-infinity'`).Scan(&unboundedLinks); err != nil {
			t.Fatal(err)
		}
		if unboundedLinks != len(spans) {
			t.Fatalf("%d links cover all occurrences, want %d (links made with the window)", unboundedLinks, len(spans))
		}
	}

	migrateTo(110)
	checkBackfill()

	// A repeating window and a deleted one, then down: the repeating one
	// falls back to a one-off over its stored first occurrence, the deleted
	// one goes, the one-offs are untouched.
	repeating, deleted := uuid.New(), uuid.New()
	exec(`INSERT INTO maintenance_windows (id, tenant_id, title, starts_at, ends_at,
			recurrence_weekdays, recurrence_start_time, recurrence_duration_minutes, recurrence_timezone)
		VALUES ($1, $2, 'r', $3, $4, '{2,4}', '02:00', 90, 'Europe/Paris')`, repeating, tenant, now.Add(time.Hour), now.Add(150*time.Minute))
	exec(`INSERT INTO maintenance_windows (id, tenant_id, title, starts_at, ends_at, deleted_at)
		VALUES ($1, $2, 'd', $3, $4, NOW())`, deleted, tenant, now.Add(-time.Hour), now)
	migrateTo(before)
	var count int
	if err := dbc.QueryRowContext(ctx, `SELECT COUNT(*) FROM maintenance_windows`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != len(spans)+1 {
		t.Fatalf("%d windows after down, want the %d one-offs and the repeating one", count, len(spans))
	}
	var occTable bool
	if err := dbc.QueryRowContext(ctx, `SELECT to_regclass('maintenance_window_occurrences') IS NOT NULL`).Scan(&occTable); err != nil {
		t.Fatal(err)
	}
	if occTable {
		t.Fatal("maintenance_window_occurrences survived the down migration")
	}

	migrateTo(110)
	spans[repeating] = [2]time.Time{now.Add(time.Hour), now.Add(150 * time.Minute)}
	exec(`INSERT INTO maintenance_window_monitors (maintenance_window_id, monitor_id) VALUES ($1, $2)`, repeating, monitor)
	checkBackfill()
}
