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

// Migration 000120 (status page custom domains) walks up, down and up
// again: hostnames are unique across workspaces, stored lower-case, limited
// to the three statuses, and leave with their page.
func TestStatusPageDomainsMigrationUpDownUp(t *testing.T) {
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
	fails := func(q string, args ...interface{}) bool {
		t.Helper()
		_, err := dbc.ExecContext(ctx, q, args...)
		return err != nil
	}
	exists := func(q string, args ...interface{}) bool {
		t.Helper()
		var ok bool
		if err := dbc.QueryRowContext(ctx, q, args...).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	hasSchema := func() bool {
		return exists(`SELECT to_regclass('public.status_page_domains') IS NOT NULL`) &&
			exists(`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'status_page_push_subscriptions' AND column_name = 'origin_host')`)
	}

	migrateTo(114)
	tenantA := testutil.InsertTenant(ctx, t, dbc, "domains-migration-a")
	tenantB := testutil.InsertTenant(ctx, t, dbc, "domains-migration-b")
	pageA := testutil.InsertStatusPage(ctx, t, dbc, tenantA, "domains-a", "A")
	pageB := testutil.InsertStatusPage(ctx, t, dbc, tenantB, "domains-b", "B")
	exec(`INSERT INTO status_page_push_subscriptions (status_page_id, endpoint, p256dh, auth) VALUES ($1, 'https://push.example.com/sub/1', 'k', 'a')`, pageA)

	migrateTo(120)
	if !hasSchema() {
		t.Fatal("schema missing after up")
	}
	if !exists(`SELECT origin_host IS NULL FROM status_page_push_subscriptions WHERE status_page_id = $1`, pageA) {
		t.Fatal("an existing subscription got an origin host")
	}
	exec(`INSERT INTO status_page_domains (status_page_id, hostname, verification_token) VALUES ($1, 'status.example.com', 't1')`, pageA)
	if !exists(`SELECT status = 'pending' AND next_check_at <= NOW() FROM status_page_domains WHERE hostname = 'status.example.com'`) {
		t.Fatal("a new domain is not pending and due")
	}
	if !fails(`INSERT INTO status_page_domains (status_page_id, hostname, verification_token) VALUES ($1, 'status.example.com', 't2')`, pageB) {
		t.Fatal("another workspace took a hostname already in use")
	}
	if !fails(`INSERT INTO status_page_domains (status_page_id, hostname, verification_token) VALUES ($1, 'Status.Example.org', 't3')`, pageB) {
		t.Fatal("an upper-case hostname was stored")
	}
	if !fails(`INSERT INTO status_page_domains (status_page_id, hostname, verification_token) VALUES ($1, 'status.example.org.', 't3')`, pageB) {
		t.Fatal("a trailing-dot hostname was stored")
	}
	if !fails(`UPDATE status_page_domains SET status = 'gone' WHERE hostname = 'status.example.com'`) {
		t.Fatal("an unknown status was accepted")
	}
	exec(`DELETE FROM status_pages WHERE id = $1`, pageA)
	if exists(`SELECT EXISTS (SELECT 1 FROM status_page_domains)`) {
		t.Fatal("domains outlived their page")
	}
	exec(`INSERT INTO status_page_domains (status_page_id, hostname, verification_token, status) VALUES ($1, 'status.example.com', 't4', 'verified')`, pageB)

	migrateTo(114)
	if exists(`SELECT to_regclass('public.status_page_domains') IS NOT NULL`) {
		t.Fatal("status_page_domains survived the down migration")
	}
	if exists(`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'status_page_push_subscriptions' AND column_name = 'origin_host')`) {
		t.Fatal("origin_host survived the down migration")
	}

	migrateTo(120)
	if !hasSchema() {
		t.Fatal("re-upgrade did not recreate the schema")
	}
}
