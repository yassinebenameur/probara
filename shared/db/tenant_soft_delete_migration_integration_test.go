//go:build !unit

package db_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"

	"github.com/yassinebenameur/probara/shared/testutil"
)

// The workspace soft-delete migration walked down and back up: names
// are unique among live workspaces only on the way up, and the way down
// finishes workspaces still waiting for the purge so the restored
// full-table name constraint holds.
func TestTenantSoftDeleteMigrationUpDownUp(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(file), "migrations")
	// This migration's number and the one below it, found by name so a
	// renumbering does not break the test.
	var softDelete, below uint
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var numbers []uint
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		n, err := strconv.ParseUint(name[:6], 10, 32)
		if err != nil {
			t.Fatal(err)
		}
		numbers = append(numbers, uint(n))
		if strings.HasSuffix(name, "_tenant_soft_delete.up.sql") {
			softDelete = uint(n)
		}
	}
	for _, n := range numbers {
		if n < softDelete && n > below {
			below = n
		}
	}
	if softDelete == 0 || below == 0 {
		t.Fatal("tenant_soft_delete migration not found")
	}
	driver, err := postgres.WithInstance(dbc.DB, &postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithDatabaseInstance("file://"+dir, "postgres", driver)
	if err != nil {
		t.Fatal(err)
	}
	// Not m.Close(): the driver would close dbc.DB, which cleanup owns.
	version, _, err := m.Version()
	if err != nil {
		t.Fatal(err)
	}
	migrateTo := func(v uint) {
		t.Helper()
		if err := m.Migrate(v); err != nil && err != migrate.ErrNoChange {
			t.Fatalf("migrate to %d: %v", v, err)
		}
	}
	exec := func(q string) error {
		_, err := dbc.ExecContext(ctx, q)
		return err
	}
	must := func(q string) {
		t.Helper()
		if err := exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	count := func(q string) int {
		t.Helper()
		var n int
		if err := dbc.QueryRowContext(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	must(`INSERT INTO tenants (name) VALUES ('Acme')`)
	must(`UPDATE tenants SET deleted_at = NOW() WHERE name = 'Acme'`)
	must(`INSERT INTO tenants (name) VALUES ('Acme')`) // a deleted name is free
	if err := exec(`INSERT INTO tenants (name) VALUES ('Acme')`); err == nil {
		t.Fatal("two live workspaces share a name")
	}
	must(`INSERT INTO monitors (tenant_id, name, type, config, interval_seconds, timeout_seconds)
		SELECT id, 'm', 'http', '{}', 60, 30 FROM tenants WHERE name = 'Acme' AND deleted_at IS NOT NULL`)

	// Down: the workspace waiting for the purge is finished (with its data),
	// the live one stays, and names are unique across the table again.
	migrateTo(below)
	if got := count(`SELECT COUNT(*) FROM tenants WHERE name = 'Acme'`); got != 1 {
		t.Fatalf("after down: %d workspaces named Acme, want 1", got)
	}
	if got := count(`SELECT COUNT(*) FROM monitors WHERE name = 'm'`); got != 0 {
		t.Fatalf("after down: the deleted workspace's monitors survived (%d)", got)
	}
	if count(`SELECT COUNT(*) FROM information_schema.columns WHERE table_name = 'tenants' AND column_name IN ('deleted_at', 'deleted_by')`) != 0 {
		t.Fatal("after down: the soft-delete columns survived")
	}
	if err := exec(`INSERT INTO tenants (name) VALUES ('Acme')`); err == nil {
		t.Fatal("after down: duplicate names are accepted")
	}

	// Up again.
	migrateTo(uint(version))
	must(`UPDATE tenants SET deleted_at = NOW() WHERE name = 'Acme'`)
	must(`INSERT INTO tenants (name) VALUES ('Acme')`)
}
