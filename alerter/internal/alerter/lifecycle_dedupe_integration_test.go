package alerter

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// sendCounter counts sends per event type and, optionally, holds each send
// open for a while so two replicas dispatching the same alert really overlap.
type sendCounter struct {
	hold time.Duration
	mu   sync.Mutex
	byEv map[string]int
	fail atomic.Bool
}

func newSendCounter(hold time.Duration) *sendCounter {
	return &sendCounter{hold: hold, byEv: map[string]int{}}
}

func (c *sendCounter) send(_ context.Context, _ alertChannel, eventType string, _ policyBinding, _ *alertRecord, _ *groupDetail, _ time.Time) error {
	if c.fail.Load() {
		return errors.New("channel unreachable")
	}
	time.Sleep(c.hold)
	c.mu.Lock()
	c.byEv[eventType]++
	c.mu.Unlock()
	return nil
}

func (c *sendCounter) count(eventType string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.byEv[eventType]
}

// newReplicaPair returns two alerters that share the database and the send
// counter — the production shape of `alerter.replicas: 2`.
func newReplicaPair(dbClient *shareddb.Client, counter *sendCounter) []*Alerter {
	replicas := make([]*Alerter, 2)
	for i := range replicas {
		a := newIntegrationAlerter(dbClient)
		a.sendFunc = counter.send
		replicas[i] = a
	}
	return replicas
}

// runConcurrently runs fn on every replica at the same time and reports the
// first error.
func runConcurrently(t *testing.T, replicas []*Alerter, fn func(*Alerter) error) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make(chan error, len(replicas))
	start := make(chan struct{})
	for _, a := range replicas {
		wg.Add(1)
		go func(a *Alerter) {
			defer wg.Done()
			<-start
			errs <- fn(a)
		}(a)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent run error = %v", err)
		}
	}
}

func openDownAlert(ctx context.Context, t *testing.T, dbClient *shareddb.Client, tenantID, monitorID uuid.UUID) uuid.UUID {
	t.Helper()
	// Open the alert without dispatching so the dispatch race is isolated.
	a := newIntegrationAlerter(dbClient)
	a.sendFunc = func(context.Context, alertChannel, string, policyBinding, *alertRecord, *groupDetail, time.Time) error {
		t.Fatalf("no channel should be routed while opening the alert")
		return nil
	}
	setStateForTest(ctx, t, dbClient, monitorID, "down", time.Now().UTC())
	if err := a.openAlertsForDownMonitors(ctx); err != nil {
		t.Fatalf("openAlertsForDownMonitors: %v", err)
	}
	var alertID uuid.UUID
	if err := dbClient.QueryRowContext(ctx, `SELECT id FROM alerts WHERE monitor_id = $1 AND status = 'active'`, monitorID).Scan(&alertID); err != nil {
		t.Fatalf("load alert: %v", err)
	}
	return alertID
}

func notificationStateFor(ctx context.Context, t *testing.T, dbClient *shareddb.Client, alertID, channelID uuid.UUID) (string, time.Time, bool) {
	t.Helper()
	var eventType string
	var sentAt time.Time
	err := dbClient.QueryRowContext(ctx, `
		SELECT last_event_type, last_sent_at FROM alert_notification_states
		WHERE alert_id = $1 AND channel_id = $2
	`, alertID, channelID).Scan(&eventType, &sentAt)
	if err != nil {
		return "", time.Time{}, false
	}
	return eventType, sentAt, true
}

// Two replicas dispatching the same freshly opened alert must produce exactly
// one "created" notification per channel. Before the claim, both read "no
// state yet", both sent, then both upserted.
func TestDispatchConcurrentReplicasSendCreatedOnce(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "dedupe")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	channelID := insertTestChannel(ctx, t, dbClient, tenantID, "email")
	mustExec(ctx, t, dbClient, `INSERT INTO tenant_default_channels (tenant_id, channel_id, delay_seconds, position) VALUES ($1, $2, 0, 0)`, tenantID, channelID)
	alertID := openDownAlert(ctx, t, dbClient, tenantID, monitorID)

	counter := newSendCounter(300 * time.Millisecond)
	replicas := newReplicaPair(dbClient, counter)

	for round := 0; round < 3; round++ {
		runConcurrently(t, replicas, func(a *Alerter) error { return a.dispatchOpenAlerts(ctx) })
	}

	if got := counter.count("created"); got != 1 {
		t.Fatalf("created sends = %d, want exactly 1", got)
	}
	if ev, _, ok := notificationStateFor(ctx, t, dbClient, alertID, channelID); !ok || ev != "created" {
		t.Fatalf("notification state = (%q, %v), want created", ev, ok)
	}
}

// Reminders race the same way: both replicas see last_sent_at older than the
// interval. Only the claim that moves last_sent_at forward may send.
func TestDispatchConcurrentReplicasSendReminderOnce(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "dedupe")
	mustExec(ctx, t, dbClient, `UPDATE tenants SET alert_reminder_seconds = 600 WHERE id = $1`, tenantID)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	channelID := insertTestChannel(ctx, t, dbClient, tenantID, "email")
	mustExec(ctx, t, dbClient, `INSERT INTO tenant_default_channels (tenant_id, channel_id, delay_seconds, position) VALUES ($1, $2, 0, 0)`, tenantID, channelID)
	alertID := openDownAlert(ctx, t, dbClient, tenantID, monitorID)
	mustExec(ctx, t, dbClient, `
		INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type)
		VALUES ($1, $2, NOW() - INTERVAL '2 hours', 'created')
	`, alertID, channelID)

	counter := newSendCounter(300 * time.Millisecond)
	replicas := newReplicaPair(dbClient, counter)
	runConcurrently(t, replicas, func(a *Alerter) error { return a.dispatchOpenAlerts(ctx) })

	if got := counter.count("reminder"); got != 1 {
		t.Fatalf("reminder sends = %d, want exactly 1", got)
	}
	if got := counter.count("created"); got != 0 {
		t.Fatalf("created sends = %d, want 0 (already notified)", got)
	}
	ev, sentAt, ok := notificationStateFor(ctx, t, dbClient, alertID, channelID)
	if !ok || ev != "reminder" || time.Since(sentAt) > time.Minute {
		t.Fatalf("notification state = (%q, %v, %v), want a fresh reminder", ev, sentAt, ok)
	}

	// Not due yet: a second pass right away must stay quiet.
	runConcurrently(t, replicas, func(a *Alerter) error { return a.dispatchOpenAlerts(ctx) })
	if got := counter.count("reminder"); got != 1 {
		t.Fatalf("reminder sends after immediate re-run = %d, want still 1", got)
	}
}

// Recovery: both replicas see the monitor up. Exactly one resolves the alert
// (guarded UPDATE) and only that one sends the resolution; the loser's
// ErrNoRows is a normal outcome, not a failure.
func TestDispatchConcurrentReplicasResolveOnce(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "dedupe")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	channelID := insertTestChannel(ctx, t, dbClient, tenantID, "email")
	mustExec(ctx, t, dbClient, `INSERT INTO tenant_default_channels (tenant_id, channel_id, delay_seconds, position) VALUES ($1, $2, 0, 0)`, tenantID, channelID)
	alertID := openDownAlert(ctx, t, dbClient, tenantID, monitorID)

	counter := newSendCounter(300 * time.Millisecond)
	replicas := newReplicaPair(dbClient, counter)
	runConcurrently(t, replicas, func(a *Alerter) error { return a.dispatchOpenAlerts(ctx) })
	if got := counter.count("created"); got != 1 {
		t.Fatalf("created sends = %d, want 1", got)
	}

	setStateForTest(ctx, t, dbClient, monitorID, "up", time.Now().UTC())
	runConcurrently(t, replicas, func(a *Alerter) error { return a.resolveAlertsForRecoveredMonitors(ctx) })

	if got := counter.count("resolved"); got != 1 {
		t.Fatalf("resolved sends = %d, want exactly 1", got)
	}
	if got := countAlerterAlertsByStatus(ctx, t, dbClient, monitorID, "resolved"); got != 1 {
		t.Fatalf("resolved alerts = %d, want 1", got)
	}
	if ev, _, ok := notificationStateFor(ctx, t, dbClient, alertID, channelID); !ok || ev != "resolved" {
		t.Fatalf("notification state = (%q, %v), want resolved", ev, ok)
	}
}

// A failed send must release the claim so the next cycle retries — the
// behaviour the old send-then-record order had, preserved under the claim.
func TestDispatchFailedSendReleasesClaim(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "dedupe")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	channelID := insertTestChannel(ctx, t, dbClient, tenantID, "email")
	mustExec(ctx, t, dbClient, `INSERT INTO tenant_default_channels (tenant_id, channel_id, delay_seconds, position) VALUES ($1, $2, 0, 0)`, tenantID, channelID)
	alertID := openDownAlert(ctx, t, dbClient, tenantID, monitorID)

	counter := newSendCounter(0)
	a := newIntegrationAlerter(dbClient)
	a.sendFunc = counter.send

	counter.fail.Store(true)
	if err := a.dispatchOpenAlerts(ctx); err != nil {
		t.Fatalf("dispatchOpenAlerts(failing) error = %v", err)
	}
	if _, _, ok := notificationStateFor(ctx, t, dbClient, alertID, channelID); ok {
		t.Fatalf("a failed send must not leave a notification state behind")
	}

	counter.fail.Store(false)
	if err := a.dispatchOpenAlerts(ctx); err != nil {
		t.Fatalf("dispatchOpenAlerts(retry) error = %v", err)
	}
	if got := counter.count("created"); got != 1 {
		t.Fatalf("created sends after retry = %d, want 1", got)
	}
	if ev, _, ok := notificationStateFor(ctx, t, dbClient, alertID, channelID); !ok || ev != "created" {
		t.Fatalf("notification state = (%q, %v), want created", ev, ok)
	}
}
