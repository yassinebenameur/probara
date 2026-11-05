//go:build !unit

package db_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// Migrations 000099 and 000100 open monitors_timeout_check to any active type
// and reserve four alert kinds. This walks them down and back up and checks
// both constraints on each side.
func TestOpenTypeConstraintMigrations(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "constraints")
	anchor := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "anchor")

	_, file, _, _ := runtime.Caller(0)
	migrate := func(name string) {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(filepath.Dir(file), "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := dbc.ExecContext(ctx, string(b)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	insertMonitor := func(typ string, interval, timeout int) (uuid.UUID, error) {
		id := uuid.New()
		_, err := dbc.ExecContext(ctx, `
			INSERT INTO monitors (id, tenant_id, name, type, config, interval_seconds, timeout_seconds, enabled)
			VALUES ($1, $2, $3, $4, '{}'::jsonb, $5, $6, TRUE)`,
			id, tenant, typ+"-"+id.String(), typ, interval, timeout)
		return id, err
	}
	location := uuid.New()
	if _, err := dbc.ExecContext(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug) VALUES ($1, $2, 'site', 'site')`, location, tenant); err != nil {
		t.Fatal(err)
	}
	insertAlert := func(kind string) error {
		// location_offline's subject is a location, not a monitor (000102).
		monitorID, sourceID := &anchor, (*uuid.UUID)(nil)
		if kind == "location_offline" {
			monitorID, sourceID = nil, &location
		}
		_, err := dbc.ExecContext(ctx, `
			INSERT INTO alerts (id, tenant_id, monitor_id, source_location_id, kind, status, triggered_at, failure_count)
			VALUES ($1, $2, $3, $4, $5, 'resolved', NOW(), 0)`,
			uuid.New(), tenant, monitorID, sourceID, kind)
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
	newKinds := []string{"domain_expiry", "slo_burn", "location_offline", "check_warning"}

	checkOpen := func(phase string) {
		t.Helper()
		_, err := insertMonitor("future_active_type", 60, 10)
		expect(phase+": unregistered active type, valid timeout", err, true)
		_, err = insertMonitor("future_active_type", 60, 0)
		expect(phase+": unregistered active type, zero timeout", err, false)
		_, err = insertMonitor("future_active_type", 60, 60)
		expect(phase+": unregistered active type, timeout == interval", err, false)
		_, err = insertMonitor("http", 60, 90)
		expect(phase+": http, timeout > interval", err, false)
		_, err = insertMonitor("http", 60, 30)
		expect(phase+": http, valid timeout", err, true)
		for _, passive := range models.PassiveMonitorTypes {
			_, err = insertMonitor(passive, 60, 600)
			expect(phase+": passive "+passive+", timeout > interval", err, true)
			_, err = insertMonitor(passive, 60, 0)
			expect(phase+": passive "+passive+", zero timeout", err, true)
		}
		for _, kind := range append([]string{"availability", "tls_expiry"}, newKinds...) {
			expect(phase+": alert kind "+kind, insertAlert(kind), true)
		}
		expect(phase+": alert kind bogus", insertAlert("bogus"), false)
	}

	checkOpen("up")

	migrate("000100_add_expiry_slo_location_warning_alert_kinds.down.sql")
	for _, kind := range newKinds {
		var n int
		if err := dbc.QueryRowContext(ctx, `SELECT count(*) FROM alerts WHERE kind = $1`, kind).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("down 000100 kept %d %s alerts", n, kind)
		}
		expect("down: alert kind "+kind, insertAlert(kind), false)
	}
	expect("down: alert kind tls_expiry", insertAlert("tls_expiry"), true)

	// 000088's constraint names the active types, so the unregistered row
	// must go before 000099 can be reverted.
	if _, err := dbc.ExecContext(ctx, `DELETE FROM monitors WHERE type = 'future_active_type'`); err != nil {
		t.Fatal(err)
	}
	migrate("000099_generalize_monitor_timeout_check.down.sql")
	_, err := insertMonitor("future_active_type", 60, 10)
	expect("down: unregistered active type", err, false)
	_, err = insertMonitor("prometheus", 60, 10)
	expect("down: prometheus, valid timeout", err, true)
	_, err = insertMonitor("push", 60, 600)
	expect("down: push, timeout > interval", err, true)

	migrate("000099_generalize_monitor_timeout_check.up.sql")
	migrate("000100_add_expiry_slo_location_warning_alert_kinds.up.sql")
	checkOpen("re-up")
}
