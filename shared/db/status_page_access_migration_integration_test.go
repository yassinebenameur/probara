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

// Migration 000114 (status page visibility, visitor password, kiosk tokens)
// walks up, down and up again; existing pages stay public, an unknown
// visibility is refused and deleting a page drops its tokens.
func TestStatusPageAccessMigrationUpDownUp(t *testing.T) {
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
	exists := func(q string, args ...interface{}) bool {
		t.Helper()
		var ok bool
		if err := dbc.QueryRowContext(ctx, q, args...).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	hasColumn := func(column string) bool {
		return exists(`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'status_pages' AND column_name = $1)`, column)
	}

	migrateTo(113)
	tenant := testutil.InsertTenant(ctx, t, dbc, "page-access-migration")
	before := testutil.InsertStatusPage(ctx, t, dbc, tenant, "before-access", "Before")

	migrateTo(114)
	if !exists(`SELECT visibility = 'public' AND password_hash IS NULL AND password_version = 0 FROM status_pages WHERE id = $1`, before) {
		t.Fatal("an existing page did not stay public")
	}
	if _, err := dbc.ExecContext(ctx, `UPDATE status_pages SET visibility = 'secret' WHERE id = $1`, before); err == nil {
		t.Fatal("an unknown visibility was accepted")
	}
	exec(`UPDATE status_pages SET visibility = 'private' WHERE id = $1`, before)
	exec(`INSERT INTO status_page_access_tokens (status_page_id, label, token_hash) VALUES ($1, 'Lobby', 'h1')`, before)
	if _, err := dbc.ExecContext(ctx, `INSERT INTO status_page_access_tokens (status_page_id, label, token_hash) VALUES ($1, 'Dup', 'h1')`, before); err == nil {
		t.Fatal("a duplicate token hash was accepted")
	}
	exec(`DELETE FROM status_pages WHERE id = $1`, before)
	if exists(`SELECT EXISTS (SELECT 1 FROM status_page_access_tokens)`) {
		t.Fatal("tokens outlived their page")
	}

	migrateTo(113)
	if hasColumn("visibility") || hasColumn("password_hash") || hasColumn("password_version") {
		t.Fatal("access columns survived the down migration")
	}
	if exists(`SELECT to_regclass('public.status_page_access_tokens') IS NOT NULL`) {
		t.Fatal("status_page_access_tokens survived the down migration")
	}

	migrateTo(114)
	if !hasColumn("visibility") || !exists(`SELECT to_regclass('public.status_page_access_tokens') IS NOT NULL`) {
		t.Fatal("re-upgrade did not recreate the schema")
	}
}
