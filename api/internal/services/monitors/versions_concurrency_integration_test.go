//go:build !unit

package monitors_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/api/internal/dbtx"
	"github.com/yassinebenameur/probara/api/internal/models"
	alertchannelservice "github.com/yassinebenameur/probara/api/internal/services/alertchannels"
	groupservice "github.com/yassinebenameur/probara/api/internal/services/groups"
	locationservice "github.com/yassinebenameur/probara/api/internal/services/locations"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// deadlockCount reads the test database's deadlock counter. Idle pool
// connections are closed first: a backend flushes its statistics when it
// exits, so a deadlock its victim saw is counted.
func deadlockCount(t *testing.T, ctx context.Context, db *shareddb.Client) int64 {
	t.Helper()
	db.DB.SetMaxIdleConns(0)
	db.DB.SetMaxIdleConns(2)
	time.Sleep(500 * time.Millisecond)
	var n int64
	if _, err := db.ExecContext(ctx, `SELECT pg_stat_clear_snapshot()`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT deadlocks FROM pg_stat_database WHERE datname = current_database()`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// race runs the saves at once with the conflict retry off, so a lock-order
// mistake surfaces as an error and in the deadlock counter instead of being
// retried away.
func race(t *testing.T, ctx context.Context, db *shareddb.Client, rounds int, setup func(round int) []func() error) {
	t.Helper()
	dbtx.SetRetry(false)
	defer dbtx.SetRetry(true)
	before := deadlockCount(t, ctx, db)
	for round := 0; round < rounds; round++ {
		fns := setup(round)
		var wg sync.WaitGroup
		errs := make([]error, len(fns))
		for i, fn := range fns {
			wg.Add(1)
			go func() { defer wg.Done(); errs[i] = fn() }()
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatalf("round %d: concurrent save failed: %v", round, err)
			}
		}
	}
	if after := deadlockCount(t, ctx, db); after != before {
		t.Fatalf("%d deadlocks during the race", after-before)
	}
}

// Transactions that touch several monitors lock them in one order
// (api/internal/dbtx): a bulk edit racing a location delete over the same
// monitors, a save racing the delete of a channel it routes to, and a group
// save racing the delete of one of its members never deadlock.
func TestSavesTouchingSeveralMonitorsDoNotDeadlock_Integration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, db, "versions-concurrency")
	svc := monitorservice.NewService(monitorservice.NewPostgresRepository(db))
	locations := locationservice.NewService(db)
	channels := alertchannelservice.NewService(db, secrets.NoOpEncryptor{})
	groups := groupservice.NewService(db)

	ids := make([]uuid.UUID, 0, 40)
	for i := 0; i < 40; i++ {
		ids = append(ids, testutil.InsertHTTPMonitor(ctx, t, db, tenant, fmt.Sprintf("m-%d", i)))
	}

	t.Run("bulk edits overlapping", func(t *testing.T) {
		race(t, ctx, db, 5, func(round int) []func() error {
			a, b := 2+round%3, 5+round%3
			return []func() error{
				func() error { _, err := svc.BulkUpdateAlerting(ctx, tenant, ids[:30], &a, nil, nil); return err },
				func() error { _, err := svc.BulkUpdateAlerting(ctx, tenant, ids[10:], &b, nil, nil); return err },
			}
		})
	})

	t.Run("bulk edit vs location delete", func(t *testing.T) {
		race(t, ctx, db, 5, func(round int) []func() error {
			location, err := locations.Create(ctx, tenant, &models.CreateLocationRequest{Name: fmt.Sprintf("loc-%d", round)})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO monitor_locations (monitor_id, location_id) SELECT unnest($1::uuid[]), $2`, pq.Array(ids), location.ID); err != nil {
				t.Fatal(err)
			}
			threshold := 1 + round
			return []func() error{
				func() error { _, err := svc.BulkUpdateAlerting(ctx, tenant, ids, &threshold, nil, nil); return err },
				func() error { _, err := locations.Delete(ctx, tenant, location.ID); return err },
			}
		})
	})

	t.Run("save vs delete of a channel it routes to", func(t *testing.T) {
		custom := "custom"
		race(t, ctx, db, 8, func(round int) []func() error {
			var doomed, kept uuid.UUID
			for _, id := range []*uuid.UUID{&doomed, &kept} {
				if err := db.QueryRowContext(ctx, `INSERT INTO alert_channels (tenant_id, name, type, config) VALUES ($1, $2, 'webhook', '{}') RETURNING id`,
					tenant, uuid.NewString()).Scan(id); err != nil {
					t.Fatal(err)
				}
			}
			y := ids[round]
			if _, err := svc.UpdateMonitor(ctx, tenant, y, &models.UpdateMonitorRequest{NotificationMode: &custom,
				NotificationChannels: []models.MonitorChannelAssignment{{ChannelID: doomed.String()}, {ChannelID: kept.String()}}}); err != nil {
				t.Fatal(err)
			}
			name := fmt.Sprintf("routed-%d", round)
			return []func() error{
				func() error {
					_, err := svc.UpdateMonitor(ctx, tenant, y, &models.UpdateMonitorRequest{Name: &name,
						NotificationChannels: []models.MonitorChannelAssignment{{ChannelID: kept.String()}}})
					return err
				},
				func() error { return channels.DeleteAlertChannel(ctx, tenant, doomed) },
			}
		})
	})

	t.Run("group save vs delete of a member", func(t *testing.T) {
		group := testutil.InsertGroupMonitor(ctx, t, db, tenant, "edge")
		race(t, ctx, db, 8, func(round int) []func() error {
			member := testutil.InsertHTTPMonitor(ctx, t, db, tenant, fmt.Sprintf("member-%d", round))
			if err := groups.AddMonitorsToGroup(ctx, tenant, group, []uuid.UUID{member}); err != nil {
				t.Fatal(err)
			}
			svc.RecordVersion(ctx, tenant, group)
			name := fmt.Sprintf("edge-%d", round)
			return []func() error{
				func() error {
					_, err := svc.UpdateMonitor(ctx, tenant, group, &models.UpdateMonitorRequest{Name: &name})
					return err
				},
				func() error { return svc.DeleteMonitor(ctx, tenant, member) },
			}
		})
		var systemVersions int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM monitor_config_versions WHERE monitor_id = $1 AND source = 'system'`, group).Scan(&systemVersions); err != nil {
			t.Fatal(err)
		}
		if systemVersions != 8 {
			t.Fatalf("member deletes recorded %d system versions for the group, want 8", systemVersions)
		}
	})
}

// A recording failure never fails a location delete.
func TestLocationDeleteSurvivesARecordingFailure_Integration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, db, "versions-location-failure")
	locations := locationservice.NewService(db)
	location, err := locations.Create(ctx, tenant, &models.CreateLocationRequest{Name: "paris"})
	if err != nil {
		t.Fatal(err)
	}
	monitor := testutil.InsertHTTPMonitor(ctx, t, db, tenant, "m")
	if _, err := db.ExecContext(ctx, `INSERT INTO monitor_locations (monitor_id, location_id) VALUES ($1, $2)`, monitor, location.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE FUNCTION refuse_versions() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'versions refused'; END $$;
		CREATE TRIGGER refuse_versions BEFORE INSERT ON monitor_config_versions FOR EACH ROW EXECUTE FUNCTION refuse_versions();
	`); err != nil {
		t.Fatal(err)
	}
	if affected, err := locations.Delete(ctx, tenant, location.ID); err != nil || affected != 1 {
		t.Fatalf("location delete = %d, %v", affected, err)
	}
}
