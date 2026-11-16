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

	"github.com/yassinebenameur/probara/api/internal/models"
	locationservice "github.com/yassinebenameur/probara/api/internal/services/locations"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func reversed(ids []uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, len(ids))
	for i, id := range ids {
		out[len(ids)-1-i] = id
	}
	return out
}

// Transactions that touch many monitors take their locks in one order
// (api/internal/dbtx), so overlapping bulk edits in opposite orders, and a
// bulk edit racing a location delete over the same monitors, all succeed.
func TestSavesTouchingManyMonitorsDoNotDeadlock_Integration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, db, "versions-concurrency")
	svc := monitorservice.NewService(monitorservice.NewPostgresRepository(db))
	locations := locationservice.NewService(db)

	ids := make([]uuid.UUID, 0, 40)
	for i := 0; i < 40; i++ {
		ids = append(ids, testutil.InsertHTTPMonitor(ctx, t, db, tenant, fmt.Sprintf("m-%d", i)))
	}
	run := func(fns ...func() error) {
		t.Helper()
		var wg sync.WaitGroup
		errs := make([]error, len(fns))
		for i, fn := range fns {
			wg.Add(1)
			go func() { defer wg.Done(); errs[i] = fn() }()
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatalf("concurrent save failed: %v", err)
			}
		}
	}

	for round := 0; round < 5; round++ {
		a, b := 2+round%3, 5+round%3
		run(
			func() error { _, err := svc.BulkUpdateAlerting(ctx, tenant, ids, &a, nil, nil); return err },
			func() error { _, err := svc.BulkUpdateAlerting(ctx, tenant, reversed(ids), &b, nil, nil); return err },
		)
	}

	for round := 0; round < 5; round++ {
		location, err := locations.Create(ctx, tenant, &models.CreateLocationRequest{Name: fmt.Sprintf("loc-%d", round)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO monitor_locations (monitor_id, location_id) SELECT unnest($1::uuid[]), $2`, pq.Array(ids), location.ID); err != nil {
			t.Fatal(err)
		}
		threshold := 1 + round
		run(
			func() error {
				_, err := svc.BulkUpdateAlerting(ctx, tenant, reversed(ids), &threshold, nil, nil)
				return err
			},
			func() error { _, err := locations.Delete(ctx, tenant, location.ID); return err },
		)
	}
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
