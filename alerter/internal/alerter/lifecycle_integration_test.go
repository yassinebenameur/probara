package alerter

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func setStateForTest(ctx context.Context, t *testing.T, db *shareddb.Client, monitorID uuid.UUID, state string, changedAt time.Time) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
		UPDATE monitors SET current_state = $2, last_state_change_at = $3, updated_at = NOW()
		WHERE id = $1
	`, monitorID, state, changedAt); err != nil {
		t.Fatalf("set state: %v", err)
	}
}

func mustExec(ctx context.Context, t *testing.T, db *shareddb.Client, q string, args ...interface{}) {
	t.Helper()
	if _, err := db.ExecContext(ctx, q, args...); err != nil {
		t.Fatalf("exec %s: %v", q, err)
	}
}

func insertTestChannel(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, 'webhook', '{"url":"https://example.com/hook"}'::jsonb, TRUE, NOW(), NOW())
	`, id, tenantID, name); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	return id
}

func notificationStateExists(ctx context.Context, t *testing.T, db *shareddb.Client, monitorID, channelID uuid.UUID) bool {
	t.Helper()
	var exists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM alert_notification_states ans
			JOIN alerts a ON a.id = ans.alert_id
			WHERE a.monitor_id = $1 AND ans.channel_id = $2
		)
	`, monitorID, channelID).Scan(&exists); err != nil {
		t.Fatalf("check notification state: %v", err)
	}
	return exists
}

func TestLifecycleOpensOneAlertPerOutage(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "lc")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	a := newIntegrationAlerter(dbClient)

	setStateForTest(ctx, t, dbClient, monitorID, "down", time.Now().UTC())
	for i := 0; i < 3; i++ {
		if err := a.runLifecycle(ctx); err != nil {
			t.Fatalf("runLifecycle(%d) error = %v", i, err)
		}
	}
	if got := countAlerterAlertsByStatus(ctx, t, dbClient, monitorID, "active"); got != 1 {
		t.Fatalf("active alerts = %d, want 1", got)
	}

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

func TestLifecycleSuspectNeverAlerts(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "lc")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	a := newIntegrationAlerter(dbClient)

	setStateForTest(ctx, t, dbClient, monitorID, "suspect", time.Now().UTC())
	if err := a.runLifecycle(ctx); err != nil {
		t.Fatalf("runLifecycle error = %v", err)
	}
	if got := countAlerterAlertsByStatus(ctx, t, dbClient, monitorID, "active"); got != 0 {
		t.Fatalf("suspect must not alert, got %d active", got)
	}
}

func TestLifecycleEscalationFiresByAlertAge(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "lc")
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
		t.Fatalf("immediate channel did not fire")
	}
	if notificationStateExists(ctx, t, dbClient, monitorID, delayedCh) {
		t.Fatalf("delayed channel fired too early")
	}

	mustExec(ctx, t, dbClient, `
		UPDATE alerts SET triggered_at = NOW() - INTERVAL '11 minutes'
		WHERE monitor_id = $1 AND status = 'active'
	`, monitorID)
	if err := a.runLifecycle(ctx); err != nil {
		t.Fatalf("runLifecycle(escalate) error = %v", err)
	}
	if !notificationStateExists(ctx, t, dbClient, monitorID, delayedCh) {
		t.Fatalf("delayed channel did not fire after delay elapsed")
	}
}

func TestLifecycleGroupMemberIsSuppressed(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "lc")
	memberID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "member")
	groupID := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "group")
	testutil.AddMonitorToGroup(ctx, t, dbClient, memberID, groupID)
	channelID := insertTestChannel(ctx, t, dbClient, tenantID, "teams")
	mustExec(ctx, t, dbClient,
		`INSERT INTO tenant_default_channels (tenant_id, channel_id) VALUES ($1, $2)`, tenantID, channelID)

	a := newIntegrationAlerter(dbClient)
	setStateForTest(ctx, t, dbClient, memberID, "down", time.Now().UTC())
	if err := a.runLifecycle(ctx); err != nil {
		t.Fatalf("runLifecycle error = %v", err)
	}
	if got := countAlerterAlertsByStatus(ctx, t, dbClient, memberID, "active"); got != 1 {
		t.Fatalf("member active alerts = %d, want 1", got)
	}
	if got := countAlerterAlertsByStatus(ctx, t, dbClient, groupID, "active"); got != 1 {
		t.Fatalf("group active alerts = %d, want 1 (refreshGroupStates should mark group down)", got)
	}
	if notificationStateExists(ctx, t, dbClient, memberID, channelID) {
		t.Fatalf("member notified despite group suppression")
	}
	if !notificationStateExists(ctx, t, dbClient, groupID, channelID) {
		t.Fatalf("group alert did not notify")
	}
}

func TestLifecycleAutoCreatesIncidentWhenToggleOn(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "lc")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	mustExec(ctx, t, dbClient, `UPDATE tenants SET auto_create_incident = TRUE WHERE id = $1`, tenantID)

	a := newIntegrationAlerter(dbClient)
	setStateForTest(ctx, t, dbClient, monitorID, "down", time.Now().UTC())
	// run twice: second cycle must not duplicate the incident
	for i := 0; i < 2; i++ {
		if err := a.runLifecycle(ctx); err != nil {
			t.Fatalf("runLifecycle(%d) error = %v", i, err)
		}
	}

	var incidentCount int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM incidents
		WHERE tenant_id = $1 AND is_auto_created = TRUE AND auto_monitor_id = $2
	`, tenantID, monitorID).Scan(&incidentCount); err != nil {
		t.Fatalf("count incidents: %v", err)
	}
	if incidentCount != 1 {
		t.Fatalf("auto incident count = %d, want 1", incidentCount)
	}

	var linkCount int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM incident_alerts ia
		JOIN alerts al ON al.id = ia.alert_id
		WHERE al.monitor_id = $1
	`, monitorID).Scan(&linkCount); err != nil {
		t.Fatalf("count incident alert links: %v", err)
	}
	if linkCount != 1 {
		t.Fatalf("incident alert link count = %d, want 1", linkCount)
	}
}

func TestLifecycleCustomListOverridesDefault(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "lc")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	defaultCh := insertTestChannel(ctx, t, dbClient, tenantID, "teams")
	customCh := insertTestChannel(ctx, t, dbClient, tenantID, "slack-voice")
	mustExec(ctx, t, dbClient,
		`INSERT INTO tenant_default_channels (tenant_id, channel_id) VALUES ($1, $2)`, tenantID, defaultCh)
	mustExec(ctx, t, dbClient,
		`UPDATE monitors SET notification_mode = 'custom' WHERE id = $1`, monitorID)
	mustExec(ctx, t, dbClient,
		`INSERT INTO monitor_channels (monitor_id, channel_id) VALUES ($1, $2)`, monitorID, customCh)

	a := newIntegrationAlerter(dbClient)
	setStateForTest(ctx, t, dbClient, monitorID, "down", time.Now().UTC())
	if err := a.runLifecycle(ctx); err != nil {
		t.Fatalf("runLifecycle error = %v", err)
	}
	if notificationStateExists(ctx, t, dbClient, monitorID, defaultCh) {
		t.Fatalf("default channel fired despite custom mode")
	}
	if !notificationStateExists(ctx, t, dbClient, monitorID, customCh) {
		t.Fatalf("custom channel did not fire")
	}
}
