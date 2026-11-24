//go:build !unit

package analytics

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/shared/monitorstate"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// S-T7 through the interval engine: while a down monitor is passing but not
// yet confirmed it is still down, so that time integrates as unplanned
// downtime and the outage ends at the confirming pass, never re-dated to the
// first one.
func TestIntegrateTimeline_UnconfirmedRecoveryIsDowntime(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	repo := NewRepository(dbClient)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "timeline-recovery")
	m := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "flapping")
	if _, err := dbClient.ExecContext(ctx,
		`UPDATE monitors SET consecutive_failures_threshold = 1, recovery_threshold = 3 WHERE id = $1`, m); err != nil {
		t.Fatal(err)
	}
	dbNow := func() time.Time {
		t.Helper()
		var now time.Time
		if err := dbClient.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			t.Fatal(err)
		}
		return now
	}
	record := func(status string) monitorstate.Transition {
		t.Helper()
		now := time.Now()
		tr, err := monitorstate.Record(ctx, dbClient.DB, monitorstate.Result{
			MonitorID: m, TenantID: tenantID, JobID: uuid.New(), Status: status,
			ResultSource: "monitor", StartedAt: now, CompletedAt: now,
		})
		if err != nil {
			t.Fatalf("Record(%s): %v", status, err)
		}
		return tr
	}

	if tr := record("failure"); tr.To != monitorstate.StateDown {
		t.Fatalf("failure: %+v, want down", tr)
	}
	start := dbNow()
	for n := 1; n <= 2; n++ {
		if tr := record("success"); tr.To != monitorstate.StateDown || tr.ConsecutiveSuccesses != n {
			t.Fatalf("pass %d: %+v, want down with %d passes [S-T7]", n, tr, n)
		}
	}
	time.Sleep(300 * time.Millisecond)
	beforeConfirming := dbNow()
	if tr := record("success"); tr.To != monitorstate.StateUp || !tr.ClosedOutage {
		t.Fatalf("third pass: %+v, want up [S-T7]", tr)
	}

	unconfirmed := TimeRange{Start: start, End: beforeConfirming}
	res, err := repo.IntegrateTimeline(ctx, tenantID, []uuid.UUID{m}, []TimeRange{unconfirmed}, TimelineOpts{})
	if err != nil {
		t.Fatalf("IntegrateTimeline: %v", err)
	}
	got := res.PerMonitor[m][0]
	assertClose(t, got.Available, 0)
	assertClose(t, got.UnplannedDown, unconfirmed.Seconds())

	outages, err := repo.Outages(ctx, tenantID, []uuid.UUID{m}, TimeRange{Start: start.Add(-time.Minute), End: dbNow()}, TimelineOpts{})
	if err != nil {
		t.Fatalf("Outages: %v", err)
	}
	var own []Outage // Outages also lists the serial composite (MonitorID nil)
	for _, o := range outages {
		if o.MonitorID != nil && *o.MonitorID == m {
			own = append(own, o)
		}
	}
	if len(own) != 1 || own[0].Ongoing || own[0].End.Before(beforeConfirming) {
		t.Fatalf("outages = %+v, want one closed outage ending at the confirming pass (after %v)", own, beforeConfirming)
	}
}
