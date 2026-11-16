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

	"github.com/yassinebenameur/probara/shared/testutil"
)

// Migrations 000116 (report schedules, deliveries, unsubscribes) and 000117
// (sla_reports.issued_by_schedule) walk up, down and up again; tenant
// deletion cascades through the new tables.
func TestSLAReportScheduleMigrationsUpDownUp(t *testing.T) {
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
	exists := func(q string) bool {
		t.Helper()
		var ok bool
		if err := dbc.QueryRowContext(ctx, q).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	tableExists := func(name string) bool {
		return exists(`SELECT to_regclass('public.` + name + `') IS NOT NULL`)
	}
	columnExists := func() bool {
		return exists(`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'sla_reports' AND column_name = 'issued_by_schedule')`)
	}
	// The version just below 000116, whatever number it has.
	var before uint
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if n, err := strconv.Atoi(e.Name()[:6]); err == nil && n < 116 && uint(n) > before {
			before = uint(n)
		}
	}

	seed := func() {
		t.Helper()
		tenant := testutil.InsertTenant(ctx, t, dbc, "reports-"+strconv.Itoa(len(entries)))
		var slaID, scheduleID string
		if err := dbc.QueryRowContext(ctx, `INSERT INTO slas (tenant_id, name, target_pct, tags) VALUES ($1, 'x', 99.9, '{a}') RETURNING id`, tenant).Scan(&slaID); err != nil {
			t.Fatal(err)
		}
		if err := dbc.QueryRowContext(ctx, `
			INSERT INTO sla_report_schedules (tenant_id, sla_id, recipients, next_run_at) VALUES ($1, $2, '{ops@example.com}', NOW()) RETURNING id
		`, tenant, slaID).Scan(&scheduleID); err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO sla_report_deliveries (schedule_id, tenant_id, period_key, address_key, address) VALUES ($1, $2, '2026-09', 'k', 'ops@example.com')`, scheduleID, tenant)
		exec(`INSERT INTO sla_report_unsubscribes (schedule_id, address_key) VALUES ($1, 'k')`, scheduleID)
		exec(`INSERT INTO sla_reports (tenant_id, sla_id, period_key, period_start, period_end, target_pct, issued_by_schedule, data)
			VALUES ($1, $2, '2026-09', '2026-09-01', '2026-10-01', 99.9, TRUE, '{}')`, tenant, slaID)
		// The recipient cap and the status set are enforced.
		if _, err := dbc.ExecContext(ctx, `UPDATE sla_report_schedules SET recipients = '{}' WHERE id = $1`, scheduleID); err == nil {
			t.Fatal("an empty recipient list was accepted")
		}
		if _, err := dbc.ExecContext(ctx, `UPDATE sla_report_deliveries SET status = 'queued' WHERE schedule_id = $1`, scheduleID); err == nil {
			t.Fatal("an unknown delivery status was accepted")
		}
		exec(`DELETE FROM tenants WHERE id = $1`, tenant)
		for _, table := range []string{"sla_report_schedules", "sla_report_deliveries", "sla_report_unsubscribes"} {
			if exists(`SELECT EXISTS (SELECT 1 FROM ` + table + `)`) {
				t.Fatalf("%s kept rows of a deleted tenant", table)
			}
		}
	}

	migrateTo(117)
	seed()

	migrateTo(before)
	for _, table := range []string{"sla_report_schedules", "sla_report_deliveries", "sla_report_unsubscribes"} {
		if tableExists(table) {
			t.Fatalf("%s survived the down migration", table)
		}
	}
	if columnExists() {
		t.Fatal("sla_reports.issued_by_schedule survived the down migration")
	}

	migrateTo(117)
	if !tableExists("sla_report_schedules") || !columnExists() {
		t.Fatal("re-upgrade did not recreate the schema")
	}
	seed()
}
