package alerter

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func insertMaintenanceWindow(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID uuid.UUID, startsAt, endsAt time.Time, monitorIDs ...uuid.UUID) uuid.UUID {
	t.Helper()
	windowID := uuid.New()
	mustExec(ctx, t, db, `
		INSERT INTO maintenance_windows (id, tenant_id, title, starts_at, ends_at)
		VALUES ($1, $2, 'test window', $3, $4)
	`, windowID, tenantID, startsAt, endsAt)
	for _, monitorID := range monitorIDs {
		mustExec(ctx, t, db, `
			INSERT INTO maintenance_window_monitors (maintenance_window_id, monitor_id)
			VALUES ($1, $2)
		`, windowID, monitorID)
	}
	return windowID
}

func expireMaintenanceWindow(ctx context.Context, t *testing.T, db *shareddb.Client, windowID uuid.UUID) {
	t.Helper()
	mustExec(ctx, t, db, `
		UPDATE maintenance_windows SET ends_at = NOW() - INTERVAL '1 second' WHERE id = $1
	`, windowID)
}

// Down during an active window must not open an alert; once the window ends
// and the monitor is still down, the next tick opens it.
func TestLifecycleMaintenanceSuppressesNewAlerts(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "mw")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	windowID := insertMaintenanceWindow(ctx, t, dbClient, tenantID,
		time.Now().Add(-time.Minute), time.Now().Add(time.Hour), monitorID)

	a := newIntegrationAlerter(dbClient)
	setStateForTest(ctx, t, dbClient, monitorID, "down", time.Now().UTC())
	if err := a.runLifecycle(ctx); err != nil {
		t.Fatalf("runLifecycle error = %v", err)
	}
	if got := countAlerterAlertsByStatus(ctx, t, dbClient, monitorID, "active"); got != 0 {
		t.Fatalf("active alerts during maintenance = %d, want 0", got)
	}

	expireMaintenanceWindow(ctx, t, dbClient, windowID)
	if err := a.runLifecycle(ctx); err != nil {
		t.Fatalf("runLifecycle(after window) error = %v", err)
	}
	if got := countAlerterAlertsByStatus(ctx, t, dbClient, monitorID, "active"); got != 1 {
		t.Fatalf("active alerts after window ended = %d, want 1 (still down)", got)
	}
}

// An alert already open when the window starts stays open but stops
// dispatching (no not-yet-fired escalation tier fires); dispatch resumes once
// the window ends.
func TestLifecycleMaintenanceMutesDispatchOfOpenAlert(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "mw")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	immediateCh := insertTestChannel(ctx, t, dbClient, tenantID, "teams")
	delayedCh := insertTestChannel(ctx, t, dbClient, tenantID, "pagerduty")
	mustExec(ctx, t, dbClient, `
		INSERT INTO tenant_default_channels (tenant_id, channel_id, delay_seconds, position)
		VALUES ($1, $2, 0, 0), ($1, $3, 600, 1)
	`, tenantID, immediateCh, delayedCh)

	a := newIntegrationAlerter(dbClient)
	setStateForTest(ctx, t, dbClient, monitorID, "down", time.Now().UTC())
	if err := a.runLifecycle(ctx); err != nil {
		t.Fatalf("runLifecycle(open) error = %v", err)
	}
	if !notificationStateExists(ctx, t, dbClient, monitorID, immediateCh) {
		t.Fatalf("immediate channel did not fire before maintenance")
	}
	if got := countAlerterAlertsByStatus(ctx, t, dbClient, monitorID, "active"); got != 1 {
		t.Fatalf("active alerts = %d, want 1", got)
	}

	// Maintenance starts; the delayed tier becomes due but must stay silent.
	windowID := insertMaintenanceWindow(ctx, t, dbClient, tenantID,
		time.Now().Add(-time.Minute), time.Now().Add(time.Hour), monitorID)
	mustExec(ctx, t, dbClient, `
		UPDATE alerts SET triggered_at = NOW() - INTERVAL '11 minutes'
		WHERE monitor_id = $1 AND status = 'active'
	`, monitorID)
	if err := a.runLifecycle(ctx); err != nil {
		t.Fatalf("runLifecycle(muted) error = %v", err)
	}
	if got := countAlerterAlertsByStatus(ctx, t, dbClient, monitorID, "active"); got != 1 {
		t.Fatalf("open alert must stay open during maintenance, active = %d", got)
	}
	if notificationStateExists(ctx, t, dbClient, monitorID, delayedCh) {
		t.Fatalf("delayed channel fired during maintenance window")
	}

	expireMaintenanceWindow(ctx, t, dbClient, windowID)
	if err := a.runLifecycle(ctx); err != nil {
		t.Fatalf("runLifecycle(resumed) error = %v", err)
	}
	if !notificationStateExists(ctx, t, dbClient, monitorID, delayedCh) {
		t.Fatalf("delayed channel did not fire after window ended")
	}
}

// A window targeting a group suppresses alerts for its members.
func TestLifecycleMaintenanceGroupTargetCoversMembers(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "mw")
	memberID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "member")
	groupID := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "group")
	testutil.AddMonitorToGroup(ctx, t, dbClient, memberID, groupID)
	insertMaintenanceWindow(ctx, t, dbClient, tenantID,
		time.Now().Add(-time.Minute), time.Now().Add(time.Hour), groupID)

	a := newIntegrationAlerter(dbClient)
	setStateForTest(ctx, t, dbClient, memberID, "down", time.Now().UTC())
	if err := a.runLifecycle(ctx); err != nil {
		t.Fatalf("runLifecycle error = %v", err)
	}
	if got := countAlerterAlertsByStatus(ctx, t, dbClient, memberID, "active"); got != 0 {
		t.Fatalf("member alert opened despite group maintenance window, got %d", got)
	}
	// The group itself is targeted directly, so its derived-down alert is
	// suppressed as well.
	if got := countAlerterAlertsByStatus(ctx, t, dbClient, groupID, "active"); got != 0 {
		t.Fatalf("group alert opened despite maintenance window, got %d", got)
	}
}

// Recovery during a window resolves the pre-existing alert normally.
func TestLifecycleMaintenanceRecoveryResolvesOpenAlert(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "mw")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")

	a := newIntegrationAlerter(dbClient)
	setStateForTest(ctx, t, dbClient, monitorID, "down", time.Now().UTC())
	if err := a.runLifecycle(ctx); err != nil {
		t.Fatalf("runLifecycle(open) error = %v", err)
	}
	insertMaintenanceWindow(ctx, t, dbClient, tenantID,
		time.Now().Add(-time.Minute), time.Now().Add(time.Hour), monitorID)

	setStateForTest(ctx, t, dbClient, monitorID, "up", time.Now().UTC())
	if err := a.runLifecycle(ctx); err != nil {
		t.Fatalf("runLifecycle(recovery) error = %v", err)
	}
	if got := countAlerterAlertsByStatus(ctx, t, dbClient, monitorID, "resolved"); got != 1 {
		t.Fatalf("resolved alerts = %d, want 1", got)
	}
	if got := countAlerterAlertsByStatus(ctx, t, dbClient, monitorID, "active"); got != 0 {
		t.Fatalf("active alerts after recovery = %d, want 0", got)
	}
}
