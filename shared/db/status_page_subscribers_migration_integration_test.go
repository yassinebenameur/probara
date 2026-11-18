//go:build !unit

package db_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/testutil"
)

// Migrations 000120-000122 add status-page email subscribers, the
// notification outbox with its deliveries, automatic publication and
// incident push deliveries. Walk them down and back up through
// golang-migrate with rows in every table, as a rollback and re-upgrade
// would.
func TestStatusPageSubscribersMigrationsUpDownUp(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "subscribers-migration")
	monitor := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "api")
	page := testutil.InsertStatusPage(ctx, t, dbc, tenant, "subs-migration", "Subs")

	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(file), "migrations")
	// The version below 000120, whatever number it has once every branch
	// is merged.
	before := uint(0)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if len(e.Name()) < 6 {
			continue
		}
		if n, err := strconv.Atoi(e.Name()[:6]); err == nil && n < 120 && uint(n) > before {
			before = uint(n)
		}
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
	migrateTo := func(version uint) {
		t.Helper()
		if err := m.Migrate(version); err != nil && err != migrate.ErrNoChange {
			t.Fatalf("migrate to %d: %v", version, err)
		}
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := dbc.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exists := func(rel string) bool {
		t.Helper()
		var ok bool
		if err := dbc.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, rel).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	column := func(table, col string) bool {
		t.Helper()
		var ok bool
		if err := dbc.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = $1 AND column_name = $2)`, table, col).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}

	for round := 0; round < 2; round++ {
		migrateTo(122)
		subscriber, incident, outbox := uuid.New(), uuid.New(), uuid.New()
		exec(`INSERT INTO status_page_subscribers (id, tenant_id, status_page_id, email, email_hash, inbox_hash, state) VALUES ($1, $2, $3, 'a@example.com', 'h', 'h', 'confirmed')`,
			subscriber, tenant, page)
		exec(`INSERT INTO incidents (id, tenant_id, title, summary, state) VALUES ($1, $2, 't', 's', 'investigating')`, incident, tenant)
		exec(`INSERT INTO incident_status_page_publications (incident_id, status_page_id, tenant_id, auto_published, public_title) VALUES ($1, $2, $3, TRUE, 'Service disruption')`, incident, page, tenant)
		exec(`INSERT INTO status_page_confirmation_sends (inbox_hash) VALUES ('h')`)
		exec(`WITH mw AS (
				INSERT INTO maintenance_windows (tenant_id, title, starts_at, ends_at) VALUES ($1, 'w', NOW(), NOW() + INTERVAL '1 hour') RETURNING id)
			INSERT INTO status_page_notification_outbox (tenant_id, status_page_id, event_kind, maintenance_window_id, dedupe_key)
			SELECT $1, $2, 'maintenance_cancelled', mw.id, 'c' FROM mw`, tenant, page)
		exec(`INSERT INTO status_page_notification_outbox (id, tenant_id, status_page_id, event_kind, incident_id, dedupe_key) VALUES ($1, $2, $3, 'incident_published', $4, 'k')`,
			outbox, tenant, page, incident)
		exec(`INSERT INTO status_page_notification_deliveries (outbox_id, subscriber_id, tenant_id) VALUES ($1, $2, $3)`, outbox, subscriber, tenant)
		exec(`INSERT INTO status_page_push_deliveries (status_page_id, incident_id, interval_id, kind, monitor_name) VALUES ($1, $2, $3, 'incident_published', 't')`,
			page, incident, outbox)
		exec(`INSERT INTO status_page_push_deliveries (status_page_id, monitor_id, interval_id, kind, monitor_name) VALUES ($1, $2, $3, 'down', 'api')`,
			page, monitor, uuid.New())
		if _, err := dbc.ExecContext(ctx, `INSERT INTO status_page_push_deliveries (status_page_id, interval_id, kind, monitor_name) VALUES ($1, $2, 'down', 'x')`, page, uuid.New()); err == nil {
			t.Fatal("a monitor transition without a monitor was accepted")
		}
		if _, err := dbc.ExecContext(ctx, `INSERT INTO status_page_notification_outbox (tenant_id, status_page_id, event_kind, dedupe_key) VALUES ($1, $2, 'incident_updated', 'x')`, tenant, page); err == nil {
			t.Fatal("an incident event without an incident was accepted")
		}

		migrateTo(before)
		if exists("status_page_subscribers") || exists("status_page_notification_outbox") || exists("status_page_notification_deliveries") || exists("status_page_confirmation_sends") {
			t.Fatal("subscriber tables still present after rolling back")
		}
		if column("incident_status_page_publications", "auto_published") || column("incident_status_page_publications", "public_title") || column("status_page_push_deliveries", "incident_id") {
			t.Fatal("000122 columns still present after rolling back")
		}
		var monitorRows int
		if err := dbc.QueryRowContext(ctx, `SELECT COUNT(*) FROM status_page_push_deliveries WHERE monitor_id = $1`, monitor).Scan(&monitorRows); err != nil || monitorRows != round+1 {
			t.Fatalf("monitor push deliveries after rollback = %d (%v), want them kept", monitorRows, err)
		}
		exec(`DELETE FROM incidents WHERE id = $1`, incident)
	}
	migrateTo(122)
	if !exists("idx_sp_notification_deliveries_due") || !exists("idx_status_page_subscribers_confirm_due") {
		t.Fatal("indexes missing after re-applying")
	}
}
