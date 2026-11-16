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

// S-T7: a monitor that went down inside a maintenance window and is already
// passing again when the window closes does not get a fresh outage page; if
// it fails again before confirming, the outage is real and the alert opens.
func TestLifecycleRecoveringAfterMaintenanceOpensNoAlert(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "mw-recovery")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	windowID := insertMaintenanceWindow(ctx, t, dbClient, tenantID,
		time.Now().Add(-time.Minute), time.Now().Add(time.Hour), monitorID)
	a := newIntegrationAlerter(dbClient)

	mustExec(ctx, t, dbClient, `
		UPDATE monitors SET current_state = 'down', consecutive_successes = 1, recovery_threshold = 3 WHERE id = $1`, monitorID)
	expireMaintenanceWindow(ctx, t, dbClient, windowID)
	if err := a.runLifecycle(ctx); err != nil {
		t.Fatalf("runLifecycle: %v", err)
	}
	if got := countAlerterAlertsByStatus(ctx, t, dbClient, monitorID, "active"); got != 0 {
		t.Fatalf("active alerts for a recovering monitor = %d, want 0 [S-T7]", got)
	}

	mustExec(ctx, t, dbClient, `UPDATE monitors SET consecutive_successes = 0 WHERE id = $1`, monitorID)
	if err := a.runLifecycle(ctx); err != nil {
		t.Fatalf("runLifecycle: %v", err)
	}
	if got := countAlerterAlertsByStatus(ctx, t, dbClient, monitorID, "active"); got != 1 {
		t.Fatalf("active alerts after the recovery failed = %d, want 1", got)
	}
}

// The group path: a group down only through members that are confirming
// their recovery opens no group alert; one member down and not recovering is
// enough to open it.
func TestLifecycleGroupDownOnlyThroughRecoveringMembersOpensNoAlert(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "group-recovery")
	groupID := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "Platform")
	a1 := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "api-1")
	a2 := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "api-2")
	for _, member := range []uuid.UUID{a1, a2} {
		mustExec(ctx, t, dbClient, `INSERT INTO monitor_groups (group_id, monitor_id) VALUES ($1, $2)`, groupID, member)
	}
	// One alert for the group; members are rolled up.
	mustExec(ctx, t, dbClient, `UPDATE monitors SET member_alert_rollup = 'group' WHERE id = $1`, groupID)
	mustExec(ctx, t, dbClient, `UPDATE monitors SET current_state = 'up' WHERE id = $1`, a2)
	mustExec(ctx, t, dbClient, `UPDATE monitors SET current_state = 'down', consecutive_successes = 1, recovery_threshold = 3 WHERE id = $1`, a1)
	a := newIntegrationAlerter(dbClient)

	if err := a.runLifecycle(ctx); err != nil {
		t.Fatalf("runLifecycle: %v", err)
	}
	for _, id := range []uuid.UUID{groupID, a1} {
		if got := countAlerterAlertsByStatus(ctx, t, dbClient, id, "active"); got != 0 {
			t.Fatalf("active alerts on %s while its only down member recovers = %d, want 0 [S-T7]", id, got)
		}
	}

	mustExec(ctx, t, dbClient, `UPDATE monitors SET current_state = 'down', consecutive_successes = 0 WHERE id = $1`, a2)
	if err := a.runLifecycle(ctx); err != nil {
		t.Fatalf("runLifecycle: %v", err)
	}
	if got := countAlerterAlertsByStatus(ctx, t, dbClient, groupID, "active"); got != 1 {
		t.Fatalf("group alerts with a member down and not recovering = %d, want 1", got)
	}
}
