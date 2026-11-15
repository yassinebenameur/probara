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

const muteStubType = "test_mute_stub"

type mutePlugin struct {
	mu    sync.Mutex
	sends []string
}

func (p *mutePlugin) Manifest() plugin.Manifest      { return plugin.Manifest{Type: muteStubType} }
func (p *mutePlugin) Validate(json.RawMessage) error { return nil }
func (p *mutePlugin) Send(_ context.Context, req plugin.DispatchRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sends = append(p.sends, req.Type())
	return nil
}

func (p *mutePlugin) take() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.sends
	p.sends = nil
	return out
}

var muteStub = &mutePlugin{}

func init() { plugin.Register(muteStub) }

// A page or reminder the alerter claimed and queued before the alert was
// acknowledged or snoozed is dropped by the worker. A page that provably
// reached nobody (first attempt, nothing in the recipient ledger) hands its
// claim back, so it fires once the mute lifts; every other held page keeps
// it, so the channel still gets the acknowledgement and the recovery notice:
//   - a multi-recipient page that reached one recipient before failing for
//     another (a ledger entry, redelivered);
//   - a single-recipient page whose earlier attempt may have reached the
//     provider (a lost reply, redelivered), with its acknowledged_sent_at.
//
// A reminder keeps its claim too, a claim that moved on since is left alone,
// and the suspected-cause follow-up and the resolve are never held.
func TestHandle_MutedEventsDroppedAndClaimsReleased(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "mute")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	alertID := uuid.New()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := dbClient.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	newChannel := func(name string) uuid.UUID {
		id := uuid.New()
		exec(`INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
			VALUES ($1, $2, $3, $4, '{}'::jsonb, TRUE, NOW(), NOW())`, id, tenantID, name, muteStubType)
		return id
	}
	freshCh, fanoutCh, lostReplyCh, firstReachedCh, remindCh, staleCh, causeCh :=
		newChannel("fresh"), newChannel("fanout"), newChannel("lost-reply"), newChannel("first-reached"),
		newChannel("remind"), newChannel("stale"), newChannel("cause")
	exec(`INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, 'availability', 'active', NOW() - INTERVAL '1 hour', 3, NOW(), NOW())`, alertID, tenantID, monitorID)

	// The alerter's claims, each stamped with the time it published under.
	claimedAt := time.Now().Add(-time.Minute).UTC().Truncate(time.Microsecond)
	for _, ch := range []uuid.UUID{freshCh, fanoutCh, lostReplyCh, firstReachedCh, staleCh} {
		exec(`INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type, created_at, updated_at)
			VALUES ($1, $2, $3, 'created', NOW(), NOW())`, alertID, ch, claimedAt)
	}
	// The provider may have the incident: the acknowledgement already went.
	exec(`UPDATE alert_notification_states SET acknowledged_sent_at = NOW() WHERE alert_id = $1 AND channel_id = $2`, alertID, lostReplyCh)
	exec(`INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type, created_at, updated_at)
		VALUES ($1, $2, $3, 'reminder', NOW(), NOW())`, alertID, remindCh, claimedAt)
	exec(`INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type, created_at, updated_at)
		VALUES ($1, $2, NOW() - INTERVAL '1 hour', 'created', NOW(), NOW())`, alertID, causeCh)
	// Recipients an earlier attempt reached.
	for _, ch := range []uuid.UUID{fanoutCh, firstReachedCh} {
		exec(`INSERT INTO alert_notification_recipient_deliveries (alert_id, channel_id, event_key, recipient_hash)
			VALUES ($1, $2, 'created', 'h1')`, alertID, ch)
	}

	envelope := func(ch uuid.UUID, eventType string, at time.Time, attempt uint64) *queue.Message {
		raw, _ := json.Marshal(notifications.DispatchEnvelope{
			V: 1, ChannelID: ch.String(), ChannelType: muteStubType,
			AlertID: alertID.String(), EventType: eventType,
			Event: notifications.AlertEvent{Type: eventType, Timestamp: at},
		})
		return &queue.Message{Data: raw, NumDelivered: attempt}
	}
	handle := func(msg *queue.Message) {
		t.Helper()
		c := &Consumer{logger: logger.New("test", "error"), db: dbClient, encryptor: secrets.NoOpEncryptor{}}
		if err := c.handle(ctx, msg); err != nil {
			t.Fatalf("handle: %v", err)
		}
	}
	rowExists := func(ch uuid.UUID) bool {
		var n int
		if err := dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_notification_states WHERE alert_id = $1 AND channel_id = $2`,
			alertID, ch).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}
	muteStub.take()

	// Acknowledged after the alerter published.
	exec(`UPDATE alerts SET status = 'acknowledged', acknowledged_at = NOW() WHERE id = $1`, alertID)
	handle(envelope(freshCh, "created", claimedAt, 1))
	handle(envelope(fanoutCh, "created", claimedAt, 2))
	handle(envelope(lostReplyCh, "created", claimedAt, 2))
	handle(envelope(firstReachedCh, "created", claimedAt, 1))
	handle(envelope(remindCh, "reminder", claimedAt, 1))
	handle(envelope(staleCh, "created", claimedAt.Add(-time.Hour), 1))
	if got := muteStub.take(); len(got) != 0 {
		t.Fatalf("sends after the ack = %v, want none", got)
	}
	if rowExists(freshCh) {
		t.Fatal("a page that reached nobody must hand its claim back, or the channel is never paged after an unacknowledge")
	}
	for name, ch := range map[string]uuid.UUID{
		"a redelivered page that reached a recipient": fanoutCh,
		"a redelivered page whose reply was lost":     lostReplyCh,
		"a page with a recorded recipient":            firstReachedCh,
		"a held reminder":                             remindCh,
		"a claim that moved on since":                 staleCh,
	} {
		if !rowExists(ch) {
			t.Fatalf("%s must keep its claim", name)
		}
	}
	var ackSentAt *time.Time
	if err := dbClient.QueryRowContext(ctx, `SELECT acknowledged_sent_at FROM alert_notification_states WHERE alert_id = $1 AND channel_id = $2`,
		alertID, lostReplyCh).Scan(&ackSentAt); err != nil || ackSentAt == nil {
		t.Fatalf("the provider acknowledgement record must survive: %v %v", ackSentAt, err)
	}

	// The follow-up is never held, acknowledged or snoozed.
	handle(envelope(causeCh, "suspected_cause", claimedAt, 1))
	exec(`UPDATE alerts SET status = 'active', acknowledged_at = NULL, unacknowledged_at = NOW(),
		snoozed_until = NOW() + INTERVAL '1 hour' WHERE id = $1`, alertID)
	handle(envelope(causeCh, "suspected_cause", claimedAt, 2))
	handle(envelope(remindCh, "reminder", claimedAt, 1))
	if got := muteStub.take(); len(got) != 2 || got[0] != "suspected_cause" || got[1] != "suspected_cause" {
		t.Fatalf("sends while acknowledged then snoozed = %v, want the two follow-ups only", got)
	}

	// The snooze ended: the queued reminder goes out again.
	exec(`UPDATE alerts SET snoozed_until = NOW() - INTERVAL '1 second' WHERE id = $1`, alertID)
	handle(envelope(remindCh, "reminder", claimedAt, 2))
	if got := muteStub.take(); len(got) != 1 || got[0] != "reminder" {
		t.Fatalf("sends after the snooze ended = %v, want [reminder]", got)
	}

	// The resolve reaches every channel that kept its claim, however the
	// alert ended.
	exec(`UPDATE alerts SET status = 'resolved', resolved_at = NOW(), snoozed_until = NOW() + INTERVAL '1 hour' WHERE id = $1`, alertID)
	handle(envelope(fanoutCh, "resolved", time.Now(), 1))
	if got := muteStub.take(); len(got) != 1 || got[0] != "resolved" {
		t.Fatalf("sends after resolve = %v, want [resolved]", got)
	}
}

// The worker holds the alert row FOR SHARE from its check through the send,
// so an acknowledgement committed while a reminder is in flight waits for it,
// and one committed before the check stops it: an ack never loses the race.
func TestHandle_AckWaitsForInFlightReminder(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "mute-race")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	channelID, alertID := uuid.New(), uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, 'pager', $3, '{}'::jsonb, TRUE, NOW(), NOW())`, channelID, tenantID, raceStubType); err != nil {
		t.Fatal(err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, 'availability', 'active', NOW(), 3, NOW(), NOW())`, alertID, tenantID, monitorID); err != nil {
		t.Fatal(err)
	}

	raceStub.order = nil
	raceStub.started = make(chan struct{})
	raceStub.release = make(chan struct{})
	defer func() { raceStub.started = nil }()
	msg := func(eventType string) *queue.Message {
		raw, _ := json.Marshal(notifications.DispatchEnvelope{
			V: 1, ChannelID: channelID.String(), ChannelType: raceStubType,
			AlertID: alertID.String(), EventType: eventType,
			Event: notifications.AlertEvent{Type: eventType, Timestamp: time.Now()},
		})
		return &queue.Message{Data: raw}
	}
	newWorker := func() *Consumer {
		return &Consumer{logger: logger.New("test", "error"), db: dbClient, encryptor: secrets.NoOpEncryptor{}}
	}

	// racePlugin holds a "created" send open: the page is mid-send.
	inFlight := make(chan error, 1)
	go func() { inFlight <- newWorker().handle(ctx, msg("created")) }()
	<-raceStub.started

	acked := make(chan error, 1)
	go func() {
		_, err := dbClient.ExecContext(ctx, `UPDATE alerts SET status = 'acknowledged', acknowledged_at = NOW() WHERE id = $1`, alertID)
		acked <- err
	}()
	select {
	case err := <-acked:
		t.Fatalf("acknowledgement committed while a send was in flight (err=%v)", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(raceStub.release)
	if err := <-inFlight; err != nil {
		t.Fatal(err)
	}
	if err := <-acked; err != nil {
		t.Fatal(err)
	}

	// Queued behind the ack: dropped.
	if err := newWorker().handle(ctx, msg("reminder")); err != nil {
		t.Fatal(err)
	}
	raceStub.mu.Lock()
	defer raceStub.mu.Unlock()
	if len(raceStub.order) != 1 || raceStub.order[0] != "created" {
		t.Fatalf("deliveries = %v, want only the page that was already in flight", raceStub.order)
	}
}
