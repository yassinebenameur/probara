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

// Migration 000101 adds the alert columns behind acknowledge, unacknowledge
// and snooze. Walk it down and back up through golang-migrate, as a rollback
// and re-upgrade would, with an alert carrying all three set.
func TestAlertMuteMigrationUpDownUp(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "mute-migration")
	monitor := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "api")

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
	columns := func() int {
		t.Helper()
		var n int
		if err := dbc.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM information_schema.columns
			WHERE table_name = 'alerts'
			  AND column_name IN ('acknowledged_by_user_id', 'unacknowledged_at', 'snoozed_until')`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	muteAlert := func() {
		t.Helper()
		userID := uuid.New()
		if _, err := dbc.ExecContext(ctx, `INSERT INTO admin_users (id, username, password_hash) VALUES ($1, $2, 'x')`,
			userID, "u-"+userID.String()); err != nil {
			t.Fatal(err)
		}
		if _, err := dbc.ExecContext(ctx, `
			INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, failure_count,
				acknowledged_at, acknowledged_by_user_id, unacknowledged_at, snoozed_until)
			VALUES ($1, $2, $3, 'availability', 'resolved', NOW(), 0, NOW(), $4, NOW(), NOW() + INTERVAL '1 hour')`,
			uuid.New(), tenant, monitor, userID); err != nil {
			t.Fatalf("insert a muted alert: %v", err)
		}
		// Deleting the user keeps the alert and forgets who acknowledged.
		if _, err := dbc.ExecContext(ctx, `DELETE FROM admin_users WHERE id = $1`, userID); err != nil {
			t.Fatalf("delete the acknowledging user: %v", err)
		}
	}

	migrateTo(101)
	if got := columns(); got != 3 {
		t.Fatalf("columns after 000101 = %d, want 3", got)
	}
	muteAlert()

	migrateTo(100)
	if got := columns(); got != 0 {
		t.Fatalf("columns after rolling back 000101 = %d, want 0", got)
	}

	migrateTo(101)
	if got := columns(); got != 3 {
		t.Fatalf("columns after re-applying 000101 = %d, want 3", got)
	}
	muteAlert()
}
