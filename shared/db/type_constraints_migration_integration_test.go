//go:build !unit

package db_test

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// Migrations 000099 and 000100 open monitors_timeout_check to any active type
// and add the domain_expiry and location_offline alert kinds. This walks them
// down and back up through golang-migrate, as an upgrade or rollback would,
// and checks both constraints at each step.
func TestTypeConstraintMigrationsUpDownUp(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "constraints")
	anchor := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "anchor")

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

	insertMonitor := func(typ string, interval, timeout int) error {
		id := uuid.New()
		_, err := dbc.ExecContext(ctx, `
			INSERT INTO monitors (id, tenant_id, name, type, config, interval_seconds, timeout_seconds, enabled)
			VALUES ($1, $2, $3, $4, '{}'::jsonb, $5, $6, TRUE)`,
			id, tenant, typ+"-"+id.String(), typ, interval, timeout)
		return err
	}
	insertAlert := func(kind string) error {
		_, err := dbc.ExecContext(ctx, `
			INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, failure_count)
			VALUES ($1, $2, $3, $4, 'resolved', NOW(), 0)`,
			uuid.New(), tenant, anchor, kind)
		return err
	}
	expect := func(what string, err error, ok bool) {
		t.Helper()
		if ok && err != nil {
			t.Errorf("%s: rejected: %v", what, err)
		}
		if !ok && err == nil {
			t.Errorf("%s: accepted, want a constraint violation", what)
		}
	}
	newKinds := []string{"domain_expiry", "location_offline"}

	checkUp := func(phase string) {
		t.Helper()
		expect(phase+": unregistered active type, valid timeout", insertMonitor("future_active_type", 60, 10), true)
		expect(phase+": unregistered active type, zero timeout", insertMonitor("future_active_type", 60, 0), false)
		expect(phase+": unregistered active type, timeout == interval", insertMonitor("future_active_type", 60, 60), false)
		expect(phase+": http, timeout > interval", insertMonitor("http", 60, 90), false)
		expect(phase+": http, valid timeout", insertMonitor("http", 60, 30), true)
		for _, passive := range models.PassiveMonitorTypes {
			expect(phase+": passive "+passive+", timeout > interval", insertMonitor(passive, 60, 600), true)
			expect(phase+": passive "+passive+", zero timeout", insertMonitor(passive, 60, 0), true)
		}
		for _, kind := range append([]string{"availability", "tls_expiry"}, newKinds...) {
			expect(phase+": alert kind "+kind, insertAlert(kind), true)
		}
		// Kinds a later feature may add are refused until a migration opens them.
		for _, kind := range []string{"slo_burn", "check_warning", "bogus"} {
			expect(phase+": alert kind "+kind, insertAlert(kind), false)
		}
	}

	migrateTo(100)
	checkUp("up")

	migrateTo(99)
	for _, kind := range newKinds {
		var n int
		if err := dbc.QueryRowContext(ctx, `SELECT count(*) FROM alerts WHERE kind = $1`, kind).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("down 000100 kept %d %s alerts", n, kind)
		}
		expect("down 000100: alert kind "+kind, insertAlert(kind), false)
	}
	expect("down 000100: alert kind tls_expiry", insertAlert("tls_expiry"), true)

	// 000088's constraint names the active types, so the unregistered rows
	// must go before 000099 can be reverted.
	if _, err := dbc.ExecContext(ctx, `DELETE FROM monitors WHERE type = 'future_active_type'`); err != nil {
		t.Fatal(err)
	}
	migrateTo(98)
	expect("down 000099: unregistered active type", insertMonitor("future_active_type", 60, 10), false)
	for _, active := range []string{"http", "prometheus", "websocket"} {
		expect(fmt.Sprintf("down 000099: %s, valid timeout", active), insertMonitor(active, 60, 10), true)
		expect(fmt.Sprintf("down 000099: %s, timeout == interval", active), insertMonitor(active, 60, 60), false)
	}
	expect("down 000099: push, timeout > interval", insertMonitor("push", 60, 600), true)

	// Back to 000100, not the latest: 000103 gives location_offline its own
	// subject (no monitor), so a monitor-bound row of that kind is refused
	// from there on; location_offline_migration_integration_test.go walks it.
	migrateTo(100)
	checkUp("re-up")
}
