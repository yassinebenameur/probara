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

// Migration 000107 adds the notification delivery history. Walk it down and
// back up through golang-migrate with rows in it, as a rollback and
// re-upgrade would.
func TestDeliveryHistoryMigrationUpDownUp(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "deliveries-migration")
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
	tableExists := func() bool {
		t.Helper()
		var ok bool
		if err := dbc.QueryRowContext(ctx, `SELECT to_regclass('alert_notification_deliveries') IS NOT NULL`).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	alertID, channelID := uuid.New(), uuid.New()
	if _, err := dbc.ExecContext(ctx, `INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, failure_count)
		VALUES ($1, $2, $3, 'availability', 'active', NOW(), 1)`, alertID, tenant, monitor); err != nil {
		t.Fatal(err)
	}
	if _, err := dbc.ExecContext(ctx, `INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active)
		VALUES ($1, $2, 'hook', 'webhook', '{}'::jsonb, TRUE)`, channelID, tenant); err != nil {
		t.Fatal(err)
	}

	for round := 0; round < 2; round++ {
		migrateTo(107)
		if !tableExists() {
			t.Fatal("alert_notification_deliveries missing at 000107")
		}
		if _, err := dbc.ExecContext(ctx, `
			INSERT INTO alert_notification_deliveries (tenant_id, alert_id, channel_id, event_type, attempt, path, outcome)
			VALUES ($1, $2, $3, 'created', 1, 'sync', 'delivered'),
			       ($1, NULL, $3, 'created', 1, 'sync', 'failed')`, tenant, alertID, channelID); err == nil {
			t.Fatal("a row with neither an alert nor the test flag was accepted")
		}
		if _, err := dbc.ExecContext(ctx, `
			INSERT INTO alert_notification_deliveries (tenant_id, alert_id, channel_id, event_type, attempt, path, outcome, is_test)
			VALUES ($1, $2, $3, 'created', 1, 'sync', 'delivered', FALSE),
			       ($1, NULL, $3, 'created', 1, 'sync', 'failed', TRUE)`, tenant, alertID, channelID); err != nil {
			t.Fatalf("insert deliveries: %v", err)
		}
		migrateTo(106)
		if tableExists() {
			t.Fatal("alert_notification_deliveries still present after rolling back 000107")
		}
	}
	migrateTo(107)
	if !tableExists() {
		t.Fatal("alert_notification_deliveries missing after re-applying 000107")
	}
	// The writer's attempt numbering counts under the alerter's claim; it
	// needs its own index.
	var indexed bool
	if err := dbc.QueryRowContext(ctx, `SELECT to_regclass('idx_alert_notification_deliveries_attempts') IS NOT NULL`).Scan(&indexed); err != nil || !indexed {
		t.Fatalf("attempt-numbering index missing (%v)", err)
	}
}
