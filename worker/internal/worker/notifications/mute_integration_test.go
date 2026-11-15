//go:build !unit

package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/recipientledger"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/shared/testutil"
)

const muteStubType = "test_mute_stub"

type mutePlugin struct {
	mu    sync.Mutex
	sends []string
	// fail, by channel ID, makes that channel's next send return this
	// instead of succeeding (each entry is used once). A send recorded in
	// sends reached the provider; a failing one is recorded only if
	// partial is set, which also reports one recipient delivered first.
	fail    map[string]error
	partial map[string]bool
}

func (p *mutePlugin) Manifest() plugin.Manifest      { return plugin.Manifest{Type: muteStubType} }
func (p *mutePlugin) Validate(json.RawMessage) error { return nil }
func (p *mutePlugin) Send(ctx context.Context, req plugin.DispatchRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err, ok := p.fail[req.Channel.ID]; ok {
		delete(p.fail, req.Channel.ID)
		if p.partial[req.Channel.ID] && req.Recipients != nil {
			if rerr := req.Recipients.Record(ctx, "+15550001"); rerr != nil {
				return rerr
			}
		}
		return err
	}
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
// acknowledged or snoozed is dropped by the worker. Whether a dropped page
// hands its claim back depends on what earlier attempts may have delivered,
// recorded by the page attempt marker (page_marker.go) and the recipient
// ledger, never on the delivery count:
//   - attempt 1 answered 429, then the alert was snoozed: nothing was
//     accepted, so the claim is released and the page goes out after the
//     snooze;
//   - attempt 1 timed out after the request was written (ambiguous), then
//     the alert was acknowledged: the claim and acknowledged_sent_at stay;
//   - a multi-recipient page reached one recipient before failing for the
//     next with a non-2xx: kept;
//   - a worker died between writing the marker and the send: kept (the
//     safe side);
//   - a held reminder keeps its claim, and a claim that moved on is left.
//
// The suspected-cause follow-up and the resolve are never held, and a
// delivered resolve clears the channel's page marker.
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
	rateLimitedCh, timeoutCh, fanoutCh, crashCh, remindCh, staleCh, causeCh :=
		newChannel("rate-limited"), newChannel("timeout"), newChannel("fanout"), newChannel("crash"),
		newChannel("remind"), newChannel("stale"), newChannel("cause")
	exec(`INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, 'availability', 'active', NOW() - INTERVAL '1 hour', 3, NOW(), NOW())`, alertID, tenantID, monitorID)

	// The alerter's claims, each stamped with the time it published under.
	claimedAt := time.Now().Add(-time.Minute).UTC().Truncate(time.Microsecond)
	for _, ch := range []uuid.UUID{rateLimitedCh, timeoutCh, fanoutCh, crashCh, staleCh} {
		exec(`INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type, created_at, updated_at)
			VALUES ($1, $2, $3, 'created', NOW(), NOW())`, alertID, ch, claimedAt)
	}
	exec(`INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type, created_at, updated_at)
		VALUES ($1, $2, $3, 'reminder', NOW(), NOW())`, alertID, remindCh, claimedAt)
	exec(`INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type, created_at, updated_at)
		VALUES ($1, $2, NOW() - INTERVAL '1 hour', 'created', NOW(), NOW())`, alertID, causeCh)

	envelope := func(ch uuid.UUID, eventType string, at time.Time, attempt uint64) *queue.Message {
		raw, _ := json.Marshal(notifications.DispatchEnvelope{
			V: 1, ChannelID: ch.String(), ChannelType: muteStubType,
			AlertID: alertID.String(), EventType: eventType,
			Event: notifications.AlertEvent{Type: eventType, Timestamp: at},
		})
		return &queue.Message{Data: raw, NumDelivered: attempt}
	}
	worker := &Consumer{logger: logger.New("test", "error"), db: dbClient, encryptor: secrets.NoOpEncryptor{}}
	handle := func(msg *queue.Message) {
		t.Helper()
		if err := worker.handle(ctx, msg); err != nil {
			t.Fatalf("handle: %v", err)
		}
	}
	handleFailing := func(msg *queue.Message) {
		t.Helper()
		if err := worker.handle(ctx, msg); err == nil {
			t.Fatal("a failed send must ask for a redelivery")
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
	markers := func(ch uuid.UUID) int {
		var n int
		if err := dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_notification_recipient_deliveries
			WHERE alert_id = $1 AND channel_id = $2 AND recipient_hash = 'attempt'`, alertID, ch).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	muteStub.mu.Lock()
	muteStub.sends = nil
	muteStub.fail = map[string]error{
		rateLimitedCh.String(): plugin.RetryAfter(plugin.NotDelivered(errors.New("returned status 429")), 10*time.Second),
		timeoutCh.String():     errors.New("post: context deadline exceeded"),
		fanoutCh.String():      plugin.NotDelivered(errors.New("returned status 503")),
	}
	muteStub.partial = map[string]bool{fanoutCh.String(): true}
	muteStub.mu.Unlock()

	// First attempts, while the alert is still active: all fail.
	handleFailing(envelope(rateLimitedCh, "created", claimedAt, 1))
	handleFailing(envelope(timeoutCh, "created", claimedAt, 1))
	handleFailing(envelope(fanoutCh, "created", claimedAt, 1))
	if m := markers(rateLimitedCh); m != 0 {
		t.Fatalf("a 429 proves nothing was accepted: markers = %d, want 0", m)
	}
	if m := markers(timeoutCh); m != 1 {
		t.Fatalf("an ambiguous timeout keeps its marker: markers = %d, want 1", m)
	}
	// A worker that died after the marker and before the send.
	if _, err := recipientledger.MarkAttempt(ctx, dbClient, alertID, crashCh, pageKey); err != nil {
		t.Fatal(err)
	}
	// The provider may have the timed-out incident: the acknowledgement went.
	exec(`UPDATE alert_notification_states SET acknowledged_sent_at = NOW() WHERE alert_id = $1 AND channel_id = $2`, alertID, timeoutCh)

	// Acknowledged; the redeliveries meet the mute.
	exec(`UPDATE alerts SET status = 'acknowledged', acknowledged_at = NOW() WHERE id = $1`, alertID)
	for _, ch := range []uuid.UUID{rateLimitedCh, timeoutCh, fanoutCh, crashCh} {
		handle(envelope(ch, "created", claimedAt, 2))
	}
	handle(envelope(remindCh, "reminder", claimedAt, 1))
	handle(envelope(staleCh, "created", claimedAt.Add(-time.Hour), 1))
	if got := muteStub.take(); len(got) != 0 {
		t.Fatalf("sends after the ack = %v, want none", got)
	}
	if rowExists(rateLimitedCh) {
		t.Fatal("a page that reached nobody (429) must hand its claim back, whatever the delivery count")
	}
	for name, ch := range map[string]uuid.UUID{
		"an ambiguous timeout":                        timeoutCh,
		"a multi-recipient page that reached someone": fanoutCh,
		"a crash between marker and send":             crashCh,
		"a held reminder":                             remindCh,
		"a claim that moved on since":                 staleCh,
	} {
		if !rowExists(ch) {
			t.Fatalf("%s must keep its claim", name)
		}
	}
	var ackSentAt *time.Time
	if err := dbClient.QueryRowContext(ctx, `SELECT acknowledged_sent_at FROM alert_notification_states WHERE alert_id = $1 AND channel_id = $2`,
		alertID, timeoutCh).Scan(&ackSentAt); err != nil || ackSentAt == nil {
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

	// The snooze ended: the queued reminder goes out again, and the alerter's
	// re-page of the released route (a new claim) is delivered.
	exec(`UPDATE alerts SET snoozed_until = NOW() - INTERVAL '1 second' WHERE id = $1`, alertID)
	handle(envelope(remindCh, "reminder", claimedAt, 2))
	repagedAt := time.Now().UTC().Truncate(time.Microsecond)
	exec(`INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type, created_at, updated_at)
		VALUES ($1, $2, $3, 'created', NOW(), NOW())`, alertID, rateLimitedCh, repagedAt)
	handle(envelope(rateLimitedCh, "created", repagedAt, 1))
	if got := muteStub.take(); len(got) != 2 || got[0] != "reminder" || got[1] != "created" {
		t.Fatalf("sends after the snooze ended = %v, want [reminder created]", got)
	}

	// The resolve reaches a channel that kept its claim, and clears the
	// channel's page marker.
	exec(`UPDATE alerts SET status = 'resolved', resolved_at = NOW(), snoozed_until = NOW() + INTERVAL '1 hour' WHERE id = $1`, alertID)
	handle(envelope(timeoutCh, "resolved", time.Now(), 1))
	if got := muteStub.take(); len(got) != 1 || got[0] != "resolved" {
		t.Fatalf("sends after resolve = %v, want [resolved]", got)
	}
	if m := markers(timeoutCh); m != 0 {
		t.Fatalf("a delivered resolve must clear the page marker: markers = %d", m)
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
