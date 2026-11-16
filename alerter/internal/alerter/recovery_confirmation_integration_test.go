//go:build !unit

package alerter

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/monitorstate"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// S-T7 (docs/state-semantics.md): the availability alert follows the
// monitor's state, not its latest check. While a down monitor is passing but
// its recovery is not yet confirmed, the alert stays open; it resolves on the
// confirming pass.
func TestLifecycleUnconfirmedRecoveryKeepsAlertOpen(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "lc-recovery")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	mustExec(ctx, t, dbClient,
		`UPDATE monitors SET consecutive_failures_threshold = 1, recovery_threshold = 3 WHERE id = $1`, monitorID)
	a := newIntegrationAlerter(dbClient)

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
	lifecycle := func() {
		t.Helper()
		if err := a.runLifecycle(ctx); err != nil {
			t.Fatalf("runLifecycle: %v", err)
		}
	}

	record("failure")
	lifecycle()
	if got := countAlerterAlertsByStatus(ctx, t, dbClient, monitorID, "active"); got != 1 {
		t.Fatalf("active alerts after the outage = %d, want 1", got)
	}

	// Two passes: the latest check is a success, the monitor is still down.
	record("success")
	record("success")
	lifecycle()
	if got := countAlerterAlertsByStatus(ctx, t, dbClient, monitorID, "active"); got != 1 {
		t.Fatalf("active alerts while recovering = %d, want 1 [S-T7]", got)
	}
	if got := countAlerterAlertsByStatus(ctx, t, dbClient, monitorID, "resolved"); got != 0 {
		t.Fatalf("resolved alerts before confirmation = %d, want 0 [S-T7]", got)
	}

	record("success")
	lifecycle()
	if got := countAlerterAlertsByStatus(ctx, t, dbClient, monitorID, "resolved"); got != 1 {
		t.Fatalf("resolved alerts after the confirming pass = %d, want 1 [S-T7]", got)
	}
	if got := countAlerterAlertsByStatus(ctx, t, dbClient, monitorID, "active"); got != 0 {
		t.Fatalf("active alerts after confirmation = %d, want 0", got)
	}
}
