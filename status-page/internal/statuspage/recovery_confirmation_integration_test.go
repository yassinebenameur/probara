//go:build !unit

package statuspage

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/monitorstate"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// S-T7 (docs/state-semantics.md): a public page shows a monitor down until
// its recovery is confirmed, even though its latest check passed — the page
// reads the monitor's state, never its last result.
func TestStatusPageShowsDownUntilRecoveryConfirmed(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient))
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "status-recovery")
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "recovery", "Recovery")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "flapping")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, monitorID, 0)
	if _, err := dbClient.ExecContext(ctx,
		`UPDATE monitors SET consecutive_failures_threshold = 1, recovery_threshold = 2 WHERE id = $1`, monitorID); err != nil {
		t.Fatal(err)
	}
	record := func(status string) {
		t.Helper()
		now := time.Now()
		if _, err := monitorstate.Record(ctx, dbClient.DB, monitorstate.Result{
			MonitorID: monitorID, TenantID: tenantID, JobID: uuid.New(), Status: status,
			ResultSource: "monitor", StartedAt: now, CompletedAt: now,
		}); err != nil {
			t.Fatalf("Record(%s): %v", status, err)
		}
	}
	assertStatus := func(want string) {
		t.Helper()
		current, err := svc.GetMonitorCurrentStatus(ctx, monitorID, tenantID)
		if err != nil {
			t.Fatalf("GetMonitorCurrentStatus: %v", err)
		}
		page, err := svc.GetStatusPageBySlug(ctx, "recovery")
		if err != nil {
			t.Fatalf("GetStatusPageBySlug: %v", err)
		}
		if current.Status != want || findMonitorStatus(t, page.Monitors, monitorID).Status != want {
			t.Fatalf("status = %q / page %q, want %q [S-T7]",
				current.Status, findMonitorStatus(t, page.Monitors, monitorID).Status, want)
		}
	}

	record("failure")
	record("success") // 1 of 2: the latest check passed, the monitor is still down
	assertStatus("down")
	record("success") // confirmed
	assertStatus("up")
}
