//go:build !unit

package alerter

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// channelRecorder counts sends per (channel, event).
type channelRecorder struct {
	mu   sync.Mutex
	sent map[uuid.UUID]map[string]int
}

func newChannelRecorder() *channelRecorder {
	return &channelRecorder{sent: map[uuid.UUID]map[string]int{}}
}

func (r *channelRecorder) send(_ context.Context, ch alertChannel, eventType string, _ policyBinding, _ *alertRecord, _ *groupDetail, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sent[ch.ID] == nil {
		r.sent[ch.ID] = map[string]int{}
	}
	r.sent[ch.ID][eventType]++
	return nil
}

func (r *channelRecorder) count(ch uuid.UUID, eventType string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sent[ch][eventType]
}

func insertDefaultRoute(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID uuid.UUID, name string, delaySeconds int, notifyAfterAck bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	mustExec(ctx, t, db, `
		INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, 'webhook', '{"url":"https://example.com/hook"}'::jsonb, TRUE, NOW(), NOW())
	`, id, tenantID, name)
	mustExec(ctx, t, db, `
		INSERT INTO tenant_default_channels (tenant_id, channel_id, delay_seconds, position, notify_after_ack)
		VALUES ($1, $2, $3, (SELECT COUNT(*) FROM tenant_default_channels WHERE tenant_id = $1), $4)
	`, tenantID, id, delaySeconds, notifyAfterAck)
	return id
}

// Acknowledging an alert stops its reminders and its unfired escalation
// tiers (stop-on-ack, the default), except on a notify_after_ack route.
// Unacknowledging resumes both clocks from the unack: nothing fires at once,
// and the tier and the reminder come due one delay / interval later.
func TestAckStopsPagingAndUnackResumesClocks(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "ack-stops")
	mustExec(ctx, t, dbClient, `UPDATE tenants SET alert_reminder_seconds = 300 WHERE id = $1`, tenantID)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	first := insertDefaultRoute(ctx, t, dbClient, tenantID, "first", 0, false)
	second := insertDefaultRoute(ctx, t, dbClient, tenantID, "second-tier", 600, false)
	room := insertDefaultRoute(ctx, t, dbClient, tenantID, "status-room", 0, true)
	alertID := openDownAlert(ctx, t, dbClient, tenantID, monitorID)

	rec := newChannelRecorder()
	a := newIntegrationAlerter(dbClient)
	a.sendFunc = rec.send
	dispatch := func() {
		t.Helper()
		if err := a.dispatchOpenAlerts(ctx); err != nil {
			t.Fatalf("dispatchOpenAlerts: %v", err)
		}
	}

	dispatch()
	if rec.count(first, "created") != 1 || rec.count(room, "created") != 1 || rec.count(second, "created") != 0 {
		t.Fatalf("first tier: got first=%d room=%d second=%d created", rec.count(first, "created"), rec.count(room, "created"), rec.count(second, "created"))
	}

	// Acknowledge, then let every clock run well past due: 30 minutes since
	// the alert opened (second tier due at 10) and since the last sends
	// (reminders due every 5).
	mustExec(ctx, t, dbClient, `UPDATE alerts SET status = 'acknowledged', acknowledged_at = NOW(),
		triggered_at = NOW() - INTERVAL '30 minutes' WHERE id = $1`, alertID)
	mustExec(ctx, t, dbClient, `UPDATE alert_notification_states SET last_sent_at = NOW() - INTERVAL '30 minutes' WHERE alert_id = $1`, alertID)
	dispatch()
	if got := rec.count(first, "reminder"); got != 0 {
		t.Fatalf("acknowledged alert sent %d reminders on a stop-on-ack route", got)
	}
	if got := rec.count(second, "created"); got != 0 {
		t.Fatalf("acknowledged alert fired its unfired tier (%d)", got)
	}
	if got := rec.count(room, "reminder"); got != 1 {
		t.Fatalf("notify_after_ack route reminders = %d, want 1", got)
	}

	// Unacknowledge: the clocks restart from now, so nothing fires yet.
	mustExec(ctx, t, dbClient, `UPDATE alerts SET status = 'active', unacknowledged_at = NOW(),
		acknowledged_at = NULL WHERE id = $1`, alertID)
	dispatch()
	if rec.count(first, "reminder") != 0 || rec.count(second, "created") != 0 {
		t.Fatalf("unack released overdue pages at once: reminder=%d tier=%d",
			rec.count(first, "reminder"), rec.count(second, "created"))
	}

	// Six minutes after the unack: the reminder (5 min) is due, the second
	// tier (10 min) not yet.
	mustExec(ctx, t, dbClient, `UPDATE alerts SET unacknowledged_at = NOW() - INTERVAL '6 minutes' WHERE id = $1`, alertID)
	dispatch()
	if rec.count(first, "reminder") != 1 || rec.count(second, "created") != 0 {
		t.Fatalf("6 min after unack: reminder=%d (want 1) tier=%d (want 0)",
			rec.count(first, "reminder"), rec.count(second, "created"))
	}

	// Eleven minutes after the unack the second tier fires.
	mustExec(ctx, t, dbClient, `UPDATE alerts SET unacknowledged_at = NOW() - INTERVAL '11 minutes' WHERE id = $1`, alertID)
	dispatch()
	if got := rec.count(second, "created"); got != 1 {
		t.Fatalf("second tier after unack = %d, want 1", got)
	}
}

// A tenant that turned stop-on-ack off keeps paging acknowledged alerts, as
// before the setting existed.
func TestAckStopsDisabledKeepsPaging(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "ack-keeps")
	mustExec(ctx, t, dbClient, `UPDATE tenants SET alert_reminder_seconds = 300, ack_stops_notifications = FALSE WHERE id = $1`, tenantID)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	ch := insertDefaultRoute(ctx, t, dbClient, tenantID, "pager", 0, false)
	alertID := openDownAlert(ctx, t, dbClient, tenantID, monitorID)

	rec := newChannelRecorder()
	a := newIntegrationAlerter(dbClient)
	a.sendFunc = rec.send
	if err := a.dispatchOpenAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	mustExec(ctx, t, dbClient, `UPDATE alerts SET status = 'acknowledged', acknowledged_at = NOW() WHERE id = $1`, alertID)
	mustExec(ctx, t, dbClient, `UPDATE alert_notification_states SET last_sent_at = NOW() - INTERVAL '30 minutes' WHERE alert_id = $1`, alertID)
	if err := a.dispatchOpenAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	if got := rec.count(ch, "reminder"); got != 1 {
		t.Fatalf("reminders with stop-on-ack off = %d, want 1", got)
	}
}

// A snoozed alert sends nothing until snoozed_until; the end of the snooze
// restarts the clocks like an unack does.
func TestSnoozeMutesUntilItEnds(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "snooze")
	mustExec(ctx, t, dbClient, `UPDATE tenants SET alert_reminder_seconds = 300 WHERE id = $1`, tenantID)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	ch := insertDefaultRoute(ctx, t, dbClient, tenantID, "pager", 0, false)
	room := insertDefaultRoute(ctx, t, dbClient, tenantID, "room", 0, true)
	alertID := openDownAlert(ctx, t, dbClient, tenantID, monitorID)

	mustExec(ctx, t, dbClient, `UPDATE alerts SET snoozed_until = NOW() + INTERVAL '1 hour' WHERE id = $1`, alertID)
	rec := newChannelRecorder()
	a := newIntegrationAlerter(dbClient)
	a.sendFunc = rec.send
	if err := a.dispatchOpenAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.count(ch, "created") != 0 || rec.count(room, "created") != 0 {
		t.Fatal("a snoozed alert paged (snooze mutes notify_after_ack routes too)")
	}

	// Snooze ended 6 minutes ago: the first tier (delay 0) fires.
	mustExec(ctx, t, dbClient, `UPDATE alerts SET snoozed_until = NOW() - INTERVAL '6 minutes',
		triggered_at = NOW() - INTERVAL '2 hours' WHERE id = $1`, alertID)
	if err := a.dispatchOpenAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.count(ch, "created") != 1 {
		t.Fatalf("created after snooze end = %d, want 1", rec.count(ch, "created"))
	}
}

// The claim re-reads the ack and snooze under the alert row lock, so a
// dispatcher that loaded the alert before an ack committed still sends
// nothing on a stop-on-ack route.
func TestDeliverNotificationRechecksAckGateUnderLock(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "ack-claim")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	chID := insertDefaultRoute(ctx, t, dbClient, tenantID, "pager", 0, false)
	alertID := openDownAlert(ctx, t, dbClient, tenantID, monitorID)
	mustExec(ctx, t, dbClient, `UPDATE alerts SET status = 'acknowledged', acknowledged_at = NOW() WHERE id = $1`, alertID)

	rec := newChannelRecorder()
	a := newIntegrationAlerter(dbClient)
	a.sendFunc = rec.send
	record := alertRecord{ID: alertID, TenantID: tenantID, MonitorID: monitorID}
	channel := alertChannel{ID: chID, Type: "webhook", IsActive: true}
	binding := policyBinding{MonitorID: monitorID, TenantID: tenantID}

	sent, err := a.deliverNotification(ctx, "created", binding, &record, nil, channel, time.Now(), 0, true)
	if err != nil || sent {
		t.Fatalf("stop-on-ack created on an acked alert: sent=%v err=%v", sent, err)
	}
	if _, _, ok := notificationStateFor(ctx, t, dbClient, alertID, chID); ok {
		t.Fatal("a held event must not keep a claim")
	}
	sent, err = a.deliverNotification(ctx, "created", binding, &record, nil, channel, time.Now(), 0, false)
	if err != nil || !sent {
		t.Fatalf("notify_after_ack created on an acked alert: sent=%v err=%v", sent, err)
	}

	mustExec(ctx, t, dbClient, `UPDATE alerts SET status = 'active', snoozed_until = NOW() + INTERVAL '1 hour' WHERE id = $1`, alertID)
	mustExec(ctx, t, dbClient, `UPDATE alert_notification_states SET last_sent_at = NOW() - INTERVAL '1 hour' WHERE alert_id = $1`, alertID)
	sent, err = a.deliverNotification(ctx, "reminder", binding, &record, nil, channel, time.Now(), time.Minute, false)
	if err != nil || sent {
		t.Fatalf("reminder on a snoozed alert: sent=%v err=%v", sent, err)
	}
}

// An unacknowledge restarts the clocks only on routes the ack held: a
// notify_after_ack route, or every route of a tenant that keeps paging on
// ack, keeps its reminder and tier schedule (through the claim's own
// resume point, not just the pre-filter).
func TestUnackRestartsOnlyHeldRoutes(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "unack-held")
	mustExec(ctx, t, dbClient, `UPDATE tenants SET alert_reminder_seconds = 300 WHERE id = $1`, tenantID)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	held := insertDefaultRoute(ctx, t, dbClient, tenantID, "held", 0, false)
	room := insertDefaultRoute(ctx, t, dbClient, tenantID, "room", 0, true)
	roomTier := insertDefaultRoute(ctx, t, dbClient, tenantID, "room-tier", 600, true)
	alertID := openDownAlert(ctx, t, dbClient, tenantID, monitorID)

	rec := newChannelRecorder()
	a := newIntegrationAlerter(dbClient)
	a.sendFunc = rec.send
	if err := a.dispatchOpenAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	// Opened 30 minutes ago, paged then, acked and unacked a minute ago.
	mustExec(ctx, t, dbClient, `UPDATE alerts SET triggered_at = NOW() - INTERVAL '30 minutes',
		unacknowledged_at = NOW() - INTERVAL '1 minute' WHERE id = $1`, alertID)
	mustExec(ctx, t, dbClient, `UPDATE alert_notification_states SET last_sent_at = NOW() - INTERVAL '30 minutes' WHERE alert_id = $1`, alertID)
	if err := a.dispatchOpenAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	if got := rec.count(held, "reminder"); got != 0 {
		t.Fatalf("held route reminded %d times a minute after the unack", got)
	}
	if got := rec.count(room, "reminder"); got != 1 {
		t.Fatalf("notify_after_ack route reminders = %d, want 1 (the ack never held it)", got)
	}
	if got := rec.count(roomTier, "created"); got != 1 {
		t.Fatalf("notify_after_ack 10-minute tier = %d, want 1 (counted from the trigger)", got)
	}

	// Through the claim alone: a tenant that keeps paging on ack.
	mustExec(ctx, t, dbClient, `UPDATE tenants SET ack_stops_notifications = FALSE WHERE id = $1`, tenantID)
	record := alertRecord{ID: alertID, TenantID: tenantID, MonitorID: monitorID}
	sent, err := a.deliverNotification(ctx, "reminder", policyBinding{MonitorID: monitorID, TenantID: tenantID},
		&record, nil, alertChannel{ID: held, Type: "webhook", IsActive: true}, time.Now(), 5*time.Minute, false)
	if err != nil || !sent {
		t.Fatalf("reminder on a route the ack never held: sent=%v err=%v", sent, err)
	}
}

// Alerts on retired alert policies follow the tenant's stop-on-ack too.
func TestLegacyPolicyDispatchHonoursAck(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "legacy-ack")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	chID := insertDefaultRoute(ctx, t, dbClient, tenantID, "pager", 0, false)
	alertID := openDownAlert(ctx, t, dbClient, tenantID, monitorID)
	mustExec(ctx, t, dbClient, `UPDATE alerts SET status = 'acknowledged', acknowledged_at = NOW() WHERE id = $1`, alertID)
	mustExec(ctx, t, dbClient, `
		INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type, created_at, updated_at)
		VALUES ($1, $2, NOW() - INTERVAL '1 hour', 'created', NOW(), NOW())`, alertID, chID)

	rec := newChannelRecorder()
	a := newIntegrationAlerter(dbClient)
	a.sendFunc = rec.send
	channels := []alertChannel{{ID: chID, Type: "webhook", IsActive: true}}
	remind := func() {
		states, err := a.loadNotificationStates(ctx, []uuid.UUID{alertID})
		if err != nil {
			t.Fatal(err)
		}
		a.dispatchNotifications(ctx, "reminder", policyBinding{MonitorID: monitorID, TenantID: tenantID},
			&alertRecord{ID: alertID, TenantID: tenantID, MonitorID: monitorID}, channels, states, nil, false, time.Now(), 5*time.Minute)
	}
	remind()
	if got := rec.count(chID, "reminder"); got != 0 {
		t.Fatalf("legacy policy reminder on an acknowledged alert = %d, want 0", got)
	}
	mustExec(ctx, t, dbClient, `UPDATE tenants SET ack_stops_notifications = FALSE WHERE id = $1`, tenantID)
	remind()
	if got := rec.count(chID, "reminder"); got != 1 {
		t.Fatalf("legacy policy reminder with stop-on-ack off = %d, want 1", got)
	}
}
