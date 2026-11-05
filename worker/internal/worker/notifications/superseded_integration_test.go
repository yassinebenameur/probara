//go:build !unit

package notifications

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/shared/testutil"
)

const raceStubType = "test_race_stub"

// racePlugin records delivery order and can hold a "created" send open, so a
// test can resolve the alert while that send is in flight.
type racePlugin struct {
	mu      sync.Mutex
	order   []string
	started chan struct{}
	release chan struct{}
}

func (p *racePlugin) Manifest() plugin.Manifest      { return plugin.Manifest{Type: raceStubType} }
func (p *racePlugin) Validate(json.RawMessage) error { return nil }
func (p *racePlugin) Send(_ context.Context, req plugin.DispatchRequest) error {
	if req.Type() == "created" && p.started != nil {
		close(p.started)
		<-p.release
	}
	p.mu.Lock()
	p.order = append(p.order, req.Type())
	p.mu.Unlock()
	return nil
}

var raceStub = &racePlugin{}

func init() { plugin.Register(raceStub) }

// Worker A reads the alert as active and starts delivering a delayed
// trigger; meanwhile the alerter resolves the alert and worker B delivers
// the resolve. The resolve must reach the provider after the trigger, or
// PagerDuty/Opsgenie keep an incident open that nothing closes. The share
// lock A holds makes the resolution wait for A's send.
func TestHandle_ResolveWaitsForInFlightTrigger(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "race")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	channelID, alertID := uuid.New(), uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, 'pager', $3, '{}'::jsonb, TRUE, NOW(), NOW())
	`, channelID, tenantID, raceStubType); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, kind, status,
			triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, NULL, 'availability', 'active', NOW(), 3, NOW(), NOW())
	`, alertID, tenantID, monitorID); err != nil {
		t.Fatalf("insert alert: %v", err)
	}

	raceStub.order = nil
	raceStub.started = make(chan struct{})
	raceStub.release = make(chan struct{})
	defer func() { raceStub.started = nil }()

	newWorker := func() *Consumer {
		return &Consumer{logger: logger.New("test", "error"), db: dbClient, encryptor: secrets.NoOpEncryptor{}}
	}
	envelope := func(eventType string) *queue.Message {
		raw, _ := json.Marshal(notifications.DispatchEnvelope{
			V: 1, ChannelID: channelID.String(), ChannelType: raceStubType,
			AlertID: alertID.String(), EventType: eventType,
			Event: notifications.AlertEvent{Type: eventType},
		})
		return &queue.Message{Data: raw}
	}

	workerA := make(chan error, 1)
	go func() { workerA <- newWorker().handle(ctx, envelope("created")) }()
	<-raceStub.started // A passed the status check and is mid-send

	resolved := make(chan error, 1)
	go func() {
		_, err := dbClient.ExecContext(ctx,
			`UPDATE alerts SET status = 'resolved', resolved_at = NOW(), updated_at = NOW() WHERE id = $1`, alertID)
		resolved <- err
	}()
	select {
	case err := <-resolved:
		t.Fatalf("resolution committed while the trigger was still in flight (err=%v)", err)
	case <-time.After(300 * time.Millisecond):
	}

	close(raceStub.release)
	if err := <-workerA; err != nil {
		t.Fatalf("worker A: %v", err)
	}
	if err := <-resolved; err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// Worker B delivers the resolve the alerter publishes after committing.
	if err := newWorker().handle(ctx, envelope("resolved")); err != nil {
		t.Fatalf("worker B: %v", err)
	}
	// A redelivered copy of the trigger (JetStream retry) is now stale.
	if err := newWorker().handle(ctx, envelope("created")); err != nil {
		t.Fatalf("redelivered trigger: %v", err)
	}

	raceStub.mu.Lock()
	defer raceStub.mu.Unlock()
	if len(raceStub.order) != 2 || raceStub.order[0] != "created" || raceStub.order[1] != "resolved" {
		t.Fatalf("delivery order = %v, want [created resolved]", raceStub.order)
	}
}

// A tier published with StopOnAck and delivered after the operator
// acknowledged is dropped under the row lock; so is anything on a snoozed
// alert. Runs the real query against a migrated schema.
func TestHandle_StopOnAckDropsAfterAck(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "stop-on-ack")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	channelID, alertID := uuid.New(), uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, 'pager', $3, '{}'::jsonb, TRUE, NOW(), NOW())
	`, channelID, tenantID, raceStubType); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, kind, status,
			triggered_at, acknowledged_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, NULL, 'availability', 'acknowledged', NOW(), NOW(), 3, NOW(), NOW())
	`, alertID, tenantID, monitorID); err != nil {
		t.Fatalf("insert alert: %v", err)
	}

	raceStub.mu.Lock()
	raceStub.order = nil
	raceStub.mu.Unlock()
	worker := &Consumer{logger: logger.New("test", "error"), db: dbClient, encryptor: secrets.NoOpEncryptor{}}
	deliver := func(eventType string, stopOnAck bool) {
		t.Helper()
		raw, _ := json.Marshal(notifications.DispatchEnvelope{
			V: 1, ChannelID: channelID.String(), ChannelType: raceStubType,
			AlertID: alertID.String(), EventType: eventType, StopOnAck: stopOnAck,
			Event: notifications.AlertEvent{Type: eventType},
		})
		if err := worker.handle(ctx, &queue.Message{Data: raw}); err != nil {
			t.Fatalf("handle %s: %v", eventType, err)
		}
	}
	sends := func() int {
		raceStub.mu.Lock()
		defer raceStub.mu.Unlock()
		return len(raceStub.order)
	}

	deliver("reminder", true)
	if n := sends(); n != 0 {
		t.Fatalf("StopOnAck reminder on an acknowledged alert was delivered (%d sends)", n)
	}
	deliver("reminder", false) // a notify_after_ack route keeps hearing
	if n := sends(); n != 1 {
		t.Fatalf("notify_after_ack reminder: %d sends, want 1", n)
	}

	if _, err := dbClient.ExecContext(ctx,
		`UPDATE alerts SET snoozed_until = NOW() + INTERVAL '1 hour' WHERE id = $1`, alertID); err != nil {
		t.Fatalf("snooze: %v", err)
	}
	deliver("reminder", false)
	if n := sends(); n != 1 {
		t.Fatalf("reminder on a snoozed alert was delivered (%d sends)", n)
	}
}

// A trigger waiting on a retry backoff when the operator acknowledges is
// dropped, and its claim is released under the lock: the alerter's created
// slot is free again, so after the unack the route is paged (a paging
// provider ignores reminders, so a kept claim would never page it again).
// A held suspected-cause follow-up likewise frees its one-shot slot.
func TestHandle_MutedDropReleasesClaim(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "release-claim")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	channelID, alertID := uuid.New(), uuid.New()
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := dbClient.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, 'pager', $3, '{}'::jsonb, TRUE, NOW(), NOW())`, channelID, tenantID, raceStubType)
	mustExec(`INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, kind, status, triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, NULL, 'availability', 'active', NOW(), 3, NOW(), NOW())`, alertID, tenantID, monitorID)

	// The alerter claimed "created" at claimedAt and published the trigger;
	// the first delivery failed, and the message waits on the backoff.
	claimedAt := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Minute)
	mustExec(`INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type, created_at, updated_at)
		VALUES ($1, $2, $3, 'created', NOW(), NOW())`, alertID, channelID, claimedAt)

	raceStub.mu.Lock()
	raceStub.order = nil
	raceStub.mu.Unlock()
	worker := &Consumer{logger: logger.New("test", "error"), db: dbClient, encryptor: secrets.NoOpEncryptor{}}
	deliver := func(eventType string, at time.Time) {
		t.Helper()
		raw, _ := json.Marshal(notifications.DispatchEnvelope{
			V: 1, ChannelID: channelID.String(), ChannelType: raceStubType,
			AlertID: alertID.String(), EventType: eventType, StopOnAck: true,
			Event: notifications.AlertEvent{Type: eventType, Timestamp: at},
		})
		if err := worker.handle(ctx, &queue.Message{Data: raw}); err != nil {
			t.Fatalf("handle %s: %v", eventType, err)
		}
	}
	sends := func() []string {
		raceStub.mu.Lock()
		defer raceStub.mu.Unlock()
		return append([]string(nil), raceStub.order...)
	}
	stateRows := func() int {
		var n int
		if err := dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_notification_states WHERE alert_id = $1`, alertID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Acknowledged during the backoff: the retry is dropped and the claim released.
	mustExec(`UPDATE alerts SET status = 'acknowledged', acknowledged_at = NOW() WHERE id = $1`, alertID)
	deliver("created", claimedAt)
	if got := sends(); len(got) != 0 {
		t.Fatalf("held trigger delivered: %v", got)
	}
	if n := stateRows(); n != 0 {
		t.Fatalf("held trigger kept its claim (%d state rows)", n)
	}

	// Unacknowledged: the alerter finds the slot free and claims created
	// again; that trigger is delivered.
	mustExec(`UPDATE alerts SET status = 'active', acknowledged_at = NULL, unacknowledged_at = NOW() WHERE id = $1`, alertID)
	reclaimedAt := time.Now().UTC().Truncate(time.Microsecond)
	mustExec(`INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type, created_at, updated_at)
		VALUES ($1, $2, $3, 'created', NOW(), NOW())`, alertID, channelID, reclaimedAt)
	deliver("created", reclaimedAt)
	if got := sends(); len(got) != 1 || got[0] != "created" {
		t.Fatalf("trigger after unack: sends = %v, want [created]", got)
	}

	// A stale copy of the first trigger must not release the new claim.
	mustExec(`UPDATE alerts SET snoozed_until = NOW() + INTERVAL '1 hour' WHERE id = $1`, alertID)
	deliver("created", claimedAt)
	if n := stateRows(); n != 1 {
		t.Fatalf("a stale held trigger released the newer claim (%d state rows)", n)
	}

	// A suspected-cause follow-up held by the snooze frees its one-shot slot.
	followUpAt := reclaimedAt.Add(time.Minute)
	mustExec(`UPDATE alert_notification_states SET suspected_cause_sent_at = $2 WHERE alert_id = $1`, alertID, followUpAt)
	deliver("suspected_cause", followUpAt)
	var sentAt *time.Time
	if err := dbClient.QueryRowContext(ctx, `SELECT suspected_cause_sent_at FROM alert_notification_states WHERE alert_id = $1`, alertID).Scan(&sentAt); err != nil {
		t.Fatal(err)
	}
	if sentAt != nil {
		t.Fatal("a held suspected-cause follow-up kept its one-shot claim")
	}
}
