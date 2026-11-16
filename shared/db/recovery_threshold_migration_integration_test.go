//go:build !unit

package db_test

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"

	"github.com/yassinebenameur/probara/shared/testutil"
)

// Migration 000111 adds recovery confirmation (docs/state-semantics.md S-T7):
// monitors.recovery_threshold (default 1, 1-10), and the consecutive_successes
// counters and recovery_pending memory on monitors and monitor_location_state. This walks it down and back
// up through golang-migrate, with an existing monitor taking the default.
func TestRecoveryThresholdMigrationUpDownUp(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "recovery-threshold-migration")
	monitor := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "existing")

	_, file, _, _ := runtime.Caller(0)
	driver, err := postgres.WithInstance(dbc.DB, &postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithDatabaseInstance("file://"+filepath.Join(filepath.Dir(file), "migrations"), "postgres", driver)
	if err != nil {
		t.Fatal(err)
	}
	// Not m.Close(): the driver would close dbc.DB, which cleanup owns.
	if err := m.Migrate(111); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate to 111: %v", err)
	}

	checkUp := func(phase string) {
		t.Helper()
		var threshold, successes int
		if err := dbc.QueryRowContext(ctx,
			`SELECT recovery_threshold, consecutive_successes FROM monitors WHERE id = $1`, monitor,
		).Scan(&threshold, &successes); err != nil {
			t.Fatalf("%s: read columns: %v", phase, err)
		}
		if threshold != 1 || successes != 0 {
			t.Errorf("%s: existing monitor = %d/%d, want the single-sample default 1/0", phase, threshold, successes)
		}
		for _, bad := range []int{0, 11} {
			if _, err := dbc.ExecContext(ctx, `UPDATE monitors SET recovery_threshold = $2 WHERE id = $1`, monitor, bad); err == nil {
				t.Errorf("%s: recovery_threshold %d accepted, want the 1-10 check to refuse it", phase, bad)
			}
		}
		if _, err := dbc.ExecContext(ctx, `UPDATE monitors SET recovery_threshold = 10 WHERE id = $1`, monitor); err != nil {
			t.Errorf("%s: recovery_threshold 10 refused: %v", phase, err)
		}
		if _, err := dbc.ExecContext(ctx, `UPDATE monitors SET recovery_threshold = 1 WHERE id = $1`, monitor); err != nil {
			t.Fatal(err)
		}
		for _, probe := range []string{
			`SELECT consecutive_successes, recovery_pending FROM monitor_location_state LIMIT 1`,
		} {
			if _, err := dbc.ExecContext(ctx, probe); err != nil {
				t.Errorf("%s: %s: %v", phase, probe, err)
			}
		}
		var pending bool
		if err := dbc.QueryRowContext(ctx, `SELECT recovery_pending FROM monitors WHERE id = $1`, monitor).Scan(&pending); err != nil || pending {
			t.Errorf("%s: existing monitor recovery_pending = %v (%v), want false", phase, pending, err)
		}
	}
	checkUp("up")

	if err := m.Steps(-1); err != nil {
		t.Fatalf("down 000111: %v", err)
	}
	for _, probe := range []string{
		`SELECT recovery_threshold FROM monitors LIMIT 1`,
		`SELECT consecutive_successes FROM monitors LIMIT 1`,
		`SELECT recovery_pending FROM monitors LIMIT 1`,
		`SELECT consecutive_successes FROM monitor_location_state LIMIT 1`,
		`SELECT recovery_pending FROM monitor_location_state LIMIT 1`,
	} {
		if _, err := dbc.ExecContext(ctx, probe); err == nil {
			t.Errorf("down 000111 kept a column: %s", probe)
		}
	}

	if err := m.Steps(1); err != nil {
		t.Fatalf("re-up 000111: %v", err)
	}
	checkUp("re-up")
}
