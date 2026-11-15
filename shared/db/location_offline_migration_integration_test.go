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
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/testutil"
)

// Migration 000103 gives location_offline alerts a location subject (no
// monitor, no target), one open alert per location, and alerts.resolve_reason.
// This walks it down and back up through golang-migrate.
func TestLocationOfflineAlertMigrationUpDownUp(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "location-offline-migration")
	monitor := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "anchor")
	location, target := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{location, target} {
		if _, err := dbc.ExecContext(ctx, `INSERT INTO locations (id, tenant_id, name, slug) VALUES ($1, $2, $3, $3)`,
			id, tenant, id.String()); err != nil {
			t.Fatal(err)
		}
	}

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
	if err := m.Migrate(103); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate to 103: %v", err)
	}

	insert := func(kind, status string, monitorID, source, targetID any) error {
		_, err := dbc.ExecContext(ctx, `
			INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, failure_count,
				source_location_id, target_location_id, resolve_reason)
			VALUES ($1, $2, $3, $4, $5::alert_status, NOW(), 0, $6, $7, CASE WHEN $5::alert_status = 'resolved' THEN 'recovered' END)`,
			uuid.New(), tenant, monitorID, kind, status, source, targetID)
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
	checkUp := func(phase string) {
		t.Helper()
		expect(phase+": location subject", insert("location_offline", "active", nil, location, nil), true)
		expect(phase+": second open alert for the location", insert("location_offline", "acknowledged", nil, location, nil), false)
		expect(phase+": resolved alerts do not collide", insert("location_offline", "resolved", nil, location, nil), true)
		expect(phase+": with a monitor", insert("location_offline", "resolved", monitor, location, nil), false)
		expect(phase+": with a target", insert("location_offline", "resolved", nil, location, target), false)
		expect(phase+": without a location", insert("location_offline", "resolved", nil, nil, nil), false)
		expect(phase+": mesh edge unchanged", insert("mesh_edge", "resolved", nil, location, target), true)
		expect(phase+": mesh edge still needs a target", insert("mesh_edge", "resolved", nil, location, nil), false)
		expect(phase+": availability still needs a monitor", insert("availability", "resolved", nil, nil, nil), false)
		expect(phase+": availability", insert("availability", "resolved", monitor, nil, nil), true)
	}
	checkUp("up")

	if err := m.Steps(-1); err != nil {
		t.Fatalf("down 000103: %v", err)
	}
	var n int
	if err := dbc.QueryRowContext(ctx, `SELECT count(*) FROM alerts WHERE kind = 'location_offline'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("down 000103 kept %d location_offline alerts", n)
	}
	if _, err := dbc.ExecContext(ctx, `SELECT resolve_reason FROM alerts LIMIT 1`); err == nil {
		t.Error("down 000103 kept alerts.resolve_reason")
	}
	if _, err := dbc.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, failure_count, source_location_id)
		VALUES ($1, $2, NULL, 'location_offline', 'active', NOW(), 0, $3)`, uuid.New(), tenant, location); err == nil {
		t.Error("down 000103: a monitor-less location_offline alert was accepted")
	}

	if err := m.Steps(1); err != nil {
		t.Fatalf("re-up 000103: %v", err)
	}
	if _, err := dbc.ExecContext(ctx, `DELETE FROM alerts`); err != nil {
		t.Fatal(err)
	}
	checkUp("re-up")
}

// Migration 000104 adds locations.enabled_at, backfilled from created_at and
// defaulting to NOW() for new rows; down drops it.
func TestLocationEnabledAtMigrationUpDownUp(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "location-enabled-at")

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
	if err := m.Migrate(103); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate to 103: %v", err)
	}
	old := uuid.New()
	if _, err := dbc.ExecContext(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, created_at) VALUES ($1, $2, 'old', 'old', NOW() - INTERVAL '40 days')`,
		old, tenant); err != nil {
		t.Fatal(err)
	}
	check := func(phase string) {
		t.Helper()
		var backfilled bool
		if err := dbc.QueryRowContext(ctx, `SELECT enabled_at = created_at FROM locations WHERE id = $1`, old).Scan(&backfilled); err != nil || !backfilled {
			t.Fatalf("%s: existing location enabled_at not backfilled from created_at (%v)", phase, err)
		}
		var fresh bool
		if err := dbc.QueryRowContext(ctx, `
			INSERT INTO locations (id, tenant_id, name, slug) VALUES ($1, $2, $3, $3)
			RETURNING enabled_at > NOW() - INTERVAL '1 minute'`, uuid.New(), tenant, phase).Scan(&fresh); err != nil || !fresh {
			t.Fatalf("%s: new location enabled_at not defaulted to NOW() (%v)", phase, err)
		}
		if _, err := dbc.ExecContext(ctx, `UPDATE locations SET enabled_at = NULL WHERE id = $1`, old); err == nil {
			t.Fatalf("%s: enabled_at accepted NULL", phase)
		}
	}
	if err := m.Migrate(104); err != nil {
		t.Fatalf("up 000104: %v", err)
	}
	check("up")
	if err := m.Steps(-1); err != nil {
		t.Fatalf("down 000104: %v", err)
	}
	if _, err := dbc.ExecContext(ctx, `SELECT enabled_at FROM locations LIMIT 1`); err == nil {
		t.Fatal("down 000104 kept locations.enabled_at")
	}
	if err := m.Steps(1); err != nil {
		t.Fatalf("re-up 000104: %v", err)
	}
	check("re-up")
}
