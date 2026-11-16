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

// Migration 000105 adds relapse reopen: the tenant cooldown, the alert's
// relapse columns, its relapse history, each route's paged episode and the
// 'relapsed' notification state. Walk it down and back up with data in every
// piece, as a rollback and re-upgrade would: the down maps a relapse notice
// back to a page. The up only touches catalog metadata (no backfill, CHECKs
// NOT VALID), yet its CHECKs hold for every write.
func TestAlertRelapseMigrationUpDownUp(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "relapse-migration")
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
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := dbc.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	relapseColumns := func() int {
		t.Helper()
		var n int
		if err := dbc.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM information_schema.columns
			WHERE (table_name = 'alerts' AND column_name IN ('relapse_count', 'reopened_at', 'auto_resolved', 'first_ack_episode_started_at'))
			   OR (table_name = 'tenants' AND column_name = 'alert_reopen_cooldown_minutes')
			   OR (table_name = 'alert_notification_states' AND column_name = 'paged_relapse')
			   OR table_name = 'alert_relapses'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	channel := uuid.New()
	exec(`INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, 'ops', 'webhook', '{}'::jsonb, TRUE, NOW(), NOW())`, channel, tenant)

	for round := 0; round < 2; round++ {
		migrateTo(102)
		if got := relapseColumns(); got != 0 {
			t.Fatalf("relapse columns at 000102 = %d, want 0", got)
		}
		// An alert acknowledged before the migration.
		acked := uuid.New()
		exec(`INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, failure_count,
				acknowledged_at, first_acknowledged_at)
			VALUES ($1, $2, $3, 'availability', 'resolved', NOW() - INTERVAL '1 hour', 0, NOW(), NOW())`, acked, tenant, monitor)

		migrateTo(105)
		if got := relapseColumns(); got != 10 {
			t.Fatalf("relapse columns after 000105 = %d, want 10", got)
		}
		// No backfill: NULL reads as the first episode (triggered_at).
		var untouched bool
		if err := dbc.QueryRowContext(ctx, `SELECT first_ack_episode_started_at IS NULL AND relapse_count = 0 AND NOT auto_resolved
			FROM alerts WHERE id = $1`, acked).Scan(&untouched); err != nil || !untouched {
			t.Fatalf("pre-migration alert not left at the defaults: %v %v", untouched, err)
		}
		exec(`UPDATE tenants SET alert_reopen_cooldown_minutes = 30 WHERE id = $1`, tenant)
		relapsed := uuid.New()
		exec(`INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, failure_count, relapse_count, reopened_at)
			VALUES ($1, $2, $3, 'latency_anomaly', 'active', NOW() - INTERVAL '1 hour', 0, 1, NOW())`, relapsed, tenant, monitor)
		exec(`INSERT INTO alert_relapses (alert_id, relapse_number, recovered_at, reopened_at)
			VALUES ($1, 1, NOW() - INTERVAL '2 minutes', NOW())`, relapsed)
		exec(`INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type)
			VALUES ($1, $2, NOW(), 'relapsed')`, relapsed, channel)
		if _, err := dbc.ExecContext(ctx, `UPDATE tenants SET alert_reopen_cooldown_minutes = 1441 WHERE id = $1`, tenant); err == nil {
			t.Fatal("a cooldown over a day must be refused")
		}
		if _, err := dbc.ExecContext(ctx, `UPDATE alerts SET relapse_count = -1 WHERE id = $1`, relapsed); err == nil {
			t.Fatal("a negative relapse count must be refused")
		}
		if _, err := dbc.ExecContext(ctx, `UPDATE alert_notification_states SET last_event_type = 'bogus' WHERE alert_id = $1`, relapsed); err == nil {
			t.Fatal("an unknown notification state must be refused")
		}

		migrateTo(102)
		if got := relapseColumns(); got != 0 {
			t.Fatalf("relapse columns after rolling back 000105 = %d, want 0", got)
		}
		var lastEvent string
		if err := dbc.QueryRowContext(ctx, `SELECT last_event_type FROM alert_notification_states WHERE alert_id = $1`,
			relapsed).Scan(&lastEvent); err != nil || lastEvent != "created" {
			t.Fatalf("relapse notice state after rollback = %q (%v), want created", lastEvent, err)
		}
		exec(`DELETE FROM alerts WHERE id IN ($1, $2)`, acked, relapsed)
	}
	migrateTo(105)
}
