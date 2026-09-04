package alerter

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestRecoveryDispatchRetriesAfterFailureAndRestart(t *testing.T) {
	ctx := context.Background()
	database, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, database, "recovery-retry")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, database, tenantID, "API")
	channelID := insertTestChannel(ctx, t, database, tenantID, "webhook")
	mustExec(ctx, t, database, `INSERT INTO tenant_default_channels (tenant_id, channel_id) VALUES ($1, $2)`, tenantID, channelID)
	alertID := openDownAlert(ctx, t, database, tenantID, monitorID)
	counter := newSendCounter(20 * time.Millisecond)
	a := newIntegrationAlerter(database)
	a.sendFunc = counter.send
	if err := a.dispatchOpenAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	if counter.count("created") != 1 {
		t.Fatal("initial notification missing")
	}
	counter.fail.Store(true)
	setStateForTest(ctx, t, database, monitorID, "up", time.Now())
	if err := a.resolveAlertsForRecoveredMonitors(ctx); err != nil {
		t.Fatal(err)
	}
	if event, _, _ := notificationStateFor(ctx, t, database, alertID, channelID); event != "created" {
		t.Fatalf("failed recovery advanced notification state: %s", event)
	}
	if err := a.dispatchPendingRecoveries(ctx); err != nil {
		t.Fatal(err)
	}
	counter.fail.Store(false)
	// Fresh replicas reconstruct pending work exclusively from persisted data.
	replicas := newReplicaPair(database, counter)
	runConcurrently(t, replicas, func(a *Alerter) error { return a.dispatchPendingRecoveries(ctx) })
	runConcurrently(t, replicas, func(a *Alerter) error { return a.dispatchPendingRecoveries(ctx) })
	if got := counter.count("resolved"); got != 1 {
		t.Fatalf("recovery sends = %d, want 1", got)
	}
	if event, _, _ := notificationStateFor(ctx, t, database, alertID, channelID); event != "resolved" {
		t.Fatalf("recovery state = %s", event)
	}
}

func TestRecoveryDispatchDoesNotStarveFreshOutagesOrLaterRecoveries(t *testing.T) {
	ctx := context.Background()
	database, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, database, "fair recovery")
	channel := insertTestChannel(ctx, t, database, tenant, "webhook")
	mustExec(ctx, t, database, `INSERT INTO tenant_default_channels (tenant_id, channel_id) VALUES ($1, $2)`, tenant, channel)
	a := newIntegrationAlerter(database)
	var pending []uuid.UUID
	for _, name := range []string{"old outage one", "old outage two"} {
		monitor := testutil.InsertHTTPMonitor(ctx, t, database, tenant, name)
		alert := openDownAlert(ctx, t, database, tenant, monitor)
		mustExec(ctx, t, database, `INSERT INTO alert_notification_states
			(alert_id, channel_id, last_sent_at, last_event_type) VALUES ($1, $2, NOW(), 'created')`, alert, channel)
		if _, err := a.resolveAlert(ctx, alert, time.Now()); err != nil {
			t.Fatal(err)
		}
		setStateForTest(ctx, t, database, monitor, "up", time.Now())
		pending = append(pending, alert)
	}
	if pending[0].String() > pending[1].String() {
		pending[0], pending[1] = pending[1], pending[0]
	}
	fresh := testutil.InsertHTTPMonitor(ctx, t, database, tenant, "new outage")
	setStateForTest(ctx, t, database, fresh, "down", time.Now())
	created := false
	a.sendFunc = func(ctx context.Context, _ alertChannel, event string, _ policyBinding, alert *alertRecord, _ *groupDetail, _ time.Time) error {
		if event == "created" {
			created = true
			return nil
		}
		if alert.ID == pending[0] {
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}
	cycle, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := a.runLifecycle(cycle); err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("stalled recovery prevented the new outage notification")
	}
	if a.recoveryCursor != pending[0] {
		t.Fatalf("retry cursor=%s, want failed alert %s", a.recoveryCursor, pending[0])
	}
	if err := a.dispatchPendingRecoveries(ctx); err != nil {
		t.Fatal(err)
	}
	if event, _, _ := notificationStateFor(ctx, t, database, pending[1], channel); event != "resolved" {
		t.Fatalf("later recovery was starved: %s", event)
	}
	if event, _, _ := notificationStateFor(ctx, t, database, pending[0], channel); event != "created" {
		t.Fatalf("failed recovery must remain retryable: %s", event)
	}
}

func TestRecoveryDispatchRecoversCrashAfterResolutionCommit(t *testing.T) {
	ctx := context.Background()
	database, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, database, "recovery-crash")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, database, tenantID, "API")
	channelID := insertTestChannel(ctx, t, database, tenantID, "webhook")
	alertID := openDownAlert(ctx, t, database, tenantID, monitorID)
	mustExec(ctx, t, database, `INSERT INTO alert_notification_states
		(alert_id, channel_id, last_sent_at, last_event_type) VALUES ($1, $2, NOW(), 'created')`, alertID, channelID)
	a := newIntegrationAlerter(database)
	resolvedAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	if _, err := a.resolveAlert(ctx, alertID, resolvedAt); err != nil {
		t.Fatal(err)
	}
	// No notifyFiredChannels call: simulate a process dying after commit.
	a = newIntegrationAlerter(database)
	counter := newSendCounter(0)
	a.sendFunc = counter.send
	if err := a.dispatchPendingRecoveries(ctx); err != nil {
		t.Fatal(err)
	}
	if got := counter.count("resolved"); got != 1 {
		t.Fatalf("recovery sends = %d, want 1", got)
	}
}
