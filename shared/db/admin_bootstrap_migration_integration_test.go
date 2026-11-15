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

// Migration 000106 records that a superadmin has existed: backfilled from
// existing rows on the way up, then kept by a trigger. This walks it down and
// back up as a rollback and re-upgrade would.
func TestAdminBootstrapClosedMigrationUpDownUp(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

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
	migrateTo := func(version uint) {
		t.Helper()
		if err := m.Migrate(version); err != nil && err != migrate.ErrNoChange {
			t.Fatalf("migrate to %d: %v", version, err)
		}
	}
	exec := func(q string) {
		t.Helper()
		if _, err := dbc.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	closed := func() bool {
		t.Helper()
		var v bool
		if err := dbc.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM admin_bootstrap_closed)`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	tableExists := func() bool {
		t.Helper()
		var v bool
		if err := dbc.QueryRowContext(ctx, `SELECT to_regclass('admin_bootstrap_closed') IS NOT NULL`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	// Down: the table, trigger and function go; writes still work.
	migrateTo(100)
	if tableExists() {
		t.Fatal("admin_bootstrap_closed survived the down migration")
	}
	exec(`INSERT INTO admin_users (username, password_hash, platform_role) VALUES ('member', 'x', 'member')`)

	// Up with only a member: not closed.
	migrateTo(106)
	if closed() {
		t.Fatal("an install with only a member counts as having had a superadmin")
	}

	// Down, a (disabled) superadmin appears, up: the backfill closes it.
	migrateTo(100)
	exec(`INSERT INTO admin_users (username, password_hash, platform_role, disabled_at) VALUES ('old-admin', 'x', 'superadmin', NOW())`)
	migrateTo(106)
	if !closed() {
		t.Fatal("the backfill missed an existing superadmin")
	}

	// The trigger: a promotion on a fresh flag sets it.
	exec(`DELETE FROM admin_bootstrap_closed`)
	exec(`UPDATE admin_users SET username = 'renamed' WHERE username = 'member'`)
	if closed() {
		t.Fatal("a write that does not make a superadmin set the flag")
	}
	exec(`UPDATE admin_users SET platform_role = 'superadmin' WHERE username = 'renamed'`)
	if !closed() {
		t.Fatal("promoting a user did not set the flag")
	}
	// Idempotent: another superadmin does not fail on the existing row.
	exec(`INSERT INTO admin_users (username, password_hash, platform_role) VALUES ('another', 'x', 'superadmin')`)
}
