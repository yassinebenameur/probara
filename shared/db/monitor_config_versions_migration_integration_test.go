//go:build !unit

package db_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/yassinebenameur/probara/shared/testutil"
)

// The monitor_config_versions migration walks down and back up as a rollback
// and re-upgrade would, and its table cascades with the monitor and the
// workspace. The files are found by name so the test survives renumbering.
func TestMonitorConfigVersionsMigrationUpDownUp(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	_, file, _, _ := runtime.Caller(0)
	read := func(direction string) string {
		t.Helper()
		matches, err := filepath.Glob(filepath.Join(filepath.Dir(file), "migrations", "*_monitor_config_versions."+direction+".sql"))
		if err != nil || len(matches) != 1 {
			t.Fatalf("find %s migration: %v %v", direction, matches, err)
		}
		raw, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := dbc.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	tableExists := func() bool {
		t.Helper()
		var v bool
		if err := dbc.QueryRowContext(ctx, `SELECT to_regclass('monitor_config_versions') IS NOT NULL`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	if !tableExists() {
		t.Fatal("migrations did not create monitor_config_versions")
	}
	exec(read("down"))
	if tableExists() {
		t.Fatal("down migration left monitor_config_versions")
	}
	exec(read("up"))
	if !tableExists() {
		t.Fatal("up migration after down did not recreate monitor_config_versions")
	}

	tenant := testutil.InsertTenant(ctx, t, dbc, "versions-migration")
	monitor := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "versioned")
	insert := `INSERT INTO monitor_config_versions (tenant_id, monitor_id, version, snapshot, actor_type, source)
		VALUES ($1, $2, $3, '{}', 'system', $4)`
	exec(insert, tenant, monitor, 1, "ui")
	if _, err := dbc.ExecContext(ctx, insert, tenant, monitor, 1, "ui"); err == nil {
		t.Fatal("a duplicate (monitor, version) was accepted")
	}
	if _, err := dbc.ExecContext(ctx, insert, tenant, monitor, 2, "nonsense"); err == nil {
		t.Fatal("an unknown source was accepted")
	}
	exec(`DELETE FROM monitors WHERE id = $1`, monitor)
	var n int
	if err := dbc.QueryRowContext(ctx, `SELECT count(*) FROM monitor_config_versions WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("deleting the monitor left %d versions", n)
	}
}
