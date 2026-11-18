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

// Migration 000118 adds config-as-code keys: monitors.key, unique per
// workspace among live monitors only, and the "apply" configuration version
// source. This walks it down (an apply version reads as "api" after) and
// back up through golang-migrate.
func TestMonitorKeysMigrationUpDownUp(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "monitor-keys-migration")
	first := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "first")
	second := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "second")

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
	if err := m.Migrate(118); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate to 118: %v", err)
	}

	checkUp := func(phase string) {
		t.Helper()
		if _, err := dbc.ExecContext(ctx, `UPDATE monitors SET key = NULL WHERE tenant_id = $1`, tenant); err != nil {
			t.Fatalf("%s: clear keys: %v", phase, err)
		}
		if _, err := dbc.ExecContext(ctx, `UPDATE monitors SET key = 'web' WHERE id = $1`, first); err != nil {
			t.Fatalf("%s: set key: %v", phase, err)
		}
		if _, err := dbc.ExecContext(ctx, `UPDATE monitors SET key = 'web' WHERE id = $1`, second); err == nil {
			t.Errorf("%s: a second live monitor took the key", phase)
		}
		if _, err := dbc.ExecContext(ctx, `UPDATE monitors SET deleted_at = NOW() WHERE id = $1`, first); err != nil {
			t.Fatal(err)
		}
		if _, err := dbc.ExecContext(ctx, `UPDATE monitors SET key = 'web' WHERE id = $1`, second); err != nil {
			t.Errorf("%s: a deleted monitor's key was not freed: %v", phase, err)
		}
		if _, err := dbc.ExecContext(ctx, `UPDATE monitors SET deleted_at = NULL, key = NULL WHERE tenant_id = $1`, tenant); err != nil {
			t.Fatal(err)
		}
		if _, err := dbc.ExecContext(ctx, `
			INSERT INTO monitor_config_versions (tenant_id, monitor_id, version, snapshot, actor_type, source)
			VALUES ($1, $2, 1, '{}', 'user', 'apply')
		`, tenant, first); err != nil {
			t.Errorf("%s: source apply refused: %v", phase, err)
		}
	}
	checkUp("up")

	if err := m.Steps(-1); err != nil {
		t.Fatalf("down 000118: %v", err)
	}
	if _, err := dbc.ExecContext(ctx, `SELECT key FROM monitors LIMIT 1`); err == nil {
		t.Error("down 000118 kept monitors.key")
	}
	var source string
	if err := dbc.QueryRowContext(ctx, `SELECT source FROM monitor_config_versions WHERE monitor_id = $1`, first).Scan(&source); err != nil || source != "api" {
		t.Errorf("down 000118: apply version source = %q (%v), want api", source, err)
	}
	if _, err := dbc.ExecContext(ctx, `DELETE FROM monitor_config_versions WHERE monitor_id = $1`, first); err != nil {
		t.Fatal(err)
	}

	if err := m.Steps(1); err != nil {
		t.Fatalf("re-up 000118: %v", err)
	}
	checkUp("re-up")
}
