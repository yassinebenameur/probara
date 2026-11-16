//go:build !unit

package notifications

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/recipientledger"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// A resolve the alerter claimed and queued before the alert relapsed is
// dropped by the worker while the alert is open again: "recovered" must
// never reach a route after the alert failed again. A resolve that provably
// reached nobody (no attempt marker and nothing in the recipient ledger, on
// any delivery attempt) hands its claim back to 'created', so the route also
// hears the relapse's own resolve if the relapse recovers before its delay;
// one that may have reached the provider (an earlier attempt's marker, or a
// recipient recorded) keeps 'resolved'; a route the alerter has re-claimed
// since is left alone. A paging channel's resolve goes out
// anyway: it closes only its own episode's incident. Once the alert has
// recovered again, a resolve queued in an earlier episode is delivered
// whatever its attempt: a route whose escalation delay outlasted the relapse
// heard neither the relapse nor its recovery, so this resolve is the one
// that closes its provider's incident.
func TestHandle_ResolveSupersededByRelapse(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "relapse-worker")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
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
	claimedAt := time.Now().Add(-time.Minute).UTC().Truncate(time.Microsecond)
	state := func(alertID, ch uuid.UUID, lastEvent string, at time.Time) {
		exec(`INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type, created_at, updated_at)
			VALUES ($1, $2, $3, $4, NOW(), NOW())`, alertID, ch, at, lastEvent)
	}
	stateOf := func(alertID, ch uuid.UUID) string {
		t.Helper()
		var lastEvent string
		if err := dbClient.QueryRowContext(ctx, `SELECT last_event_type FROM alert_notification_states WHERE alert_id = $1 AND channel_id = $2`,
			alertID, ch).Scan(&lastEvent); err != nil {
			t.Fatal(err)
		}
		return lastEvent
	}
	resolveOn := func(channelType string, alertID, ch uuid.UUID, relapse int, attempt uint64) {
		t.Helper()
		raw, _ := json.Marshal(notifications.DispatchEnvelope{
			V: 1, ChannelID: ch.String(), ChannelType: channelType,
			AlertID: alertID.String(), EventType: "resolved",
			Event: notifications.AlertEvent{Type: "resolved", Timestamp: claimedAt,
				Alert: notifications.AlertDetails{ID: alertID.String(), RelapseCount: relapse}},
		})
		c := &Consumer{logger: logger.New("test", "error"), db: dbClient, encryptor: secrets.NoOpEncryptor{}}
		if err := c.handle(ctx, &queue.Message{Data: raw, NumDelivered: attempt}); err != nil {
			t.Fatalf("handle: %v", err)
		}
	}
	resolve := func(alertID, ch uuid.UUID, relapse int, attempt uint64) {
		t.Helper()
		resolveOn(muteStubType, alertID, ch, relapse, attempt)
	}
	pagingChannel := func(name string) uuid.UUID {
		id := uuid.New()
		exec(`INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
			VALUES ($1, $2, $3, $4, '{}'::jsonb, TRUE, NOW(), NOW())`, id, tenantID, name, pagingStubType)
		return id
	}
	muteStub.take()
	pagingStub.sends = nil

	// Reopened (relapse 1, open) while its episode-0 resolves were queued.
	reopened := uuid.New()
	exec(`INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, relapse_count, reopened_at,
			failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, 'availability', 'active', NOW() - INTERVAL '1 hour', 1, NOW(), 3, NOW(), NOW())`,
		reopened, tenantID, monitorID)
	waiting, retried, fanout, reclaimed := newChannel("waiting"), newChannel("retried"), newChannel("fanout"), newChannel("reclaimed")
	timedOut := newChannel("timed-out")
	for _, ch := range []uuid.UUID{waiting, retried, fanout, timedOut} {
		state(reopened, ch, "resolved", claimedAt)
	}
	// An earlier attempt that timed out after sending keeps its marker.
	if _, err := recipientledger.MarkAttempt(ctx, dbClient, reopened, timedOut, "resolved"); err != nil {
		t.Fatal(err)
	}
	state(reopened, reclaimed, "relapsed", claimedAt.Add(30*time.Second))
	exec(`INSERT INTO alert_notification_recipient_deliveries (alert_id, channel_id, event_key, recipient_hash)
		VALUES ($1, $2, 'resolved', 'h1')`, reopened, fanout)

	resolve(reopened, waiting, 0, 1)
	resolve(reopened, retried, 0, 2)
	resolve(reopened, fanout, 0, 1)
	resolve(reopened, reclaimed, 0, 1)
	resolve(reopened, timedOut, 0, 2)
	if sent := muteStub.take(); len(sent) != 0 {
		t.Fatalf("resolves of the recovered episode were delivered after the relapse: %v", sent)
	}
	// "retried" is a redelivery whose earlier attempt left no marker (it
	// failed before anything was accepted): handed back like a first attempt.
	for ch, want := range map[uuid.UUID]string{waiting: "created", retried: "created", fanout: "resolved", reclaimed: "relapsed", timedOut: "resolved"} {
		if got := stateOf(reopened, ch); got != want {
			t.Errorf("route %s left at %q, want %q", ch, got, want)
		}
	}
	pager := pagingChannel("pager")
	state(reopened, pager, "resolved", claimedAt)
	resolveOn(pagingStubType, reopened, pager, 0, 2)
	if len(pagingStub.sends) != 1 || pagingStub.sends[0] != "resolved" {
		t.Fatalf("paging sends = %v, want the earlier episode's resolve", pagingStub.sends)
	}

	// Reopened and recovered again (relapse 1, resolved) before a route with
	// a long escalation delay heard the relapse: its episode-0 resolve, on a
	// retry, still goes out, on chat and paging channels alike.
	recoveredAgain := uuid.New()
	exec(`INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, resolved_at, relapse_count, reopened_at,
			failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, 'availability', 'resolved', NOW() - INTERVAL '1 hour', NOW(), 1, NOW() - INTERVAL '5 minutes', 3, NOW(), NOW())`,
		recoveredAgain, tenantID, monitorID)
	stale, current, stalePager := newChannel("stale"), newChannel("current"), pagingChannel("stale-pager")
	state(recoveredAgain, stale, "resolved", claimedAt)
	state(recoveredAgain, current, "resolved", claimedAt)
	state(recoveredAgain, stalePager, "resolved", claimedAt)
	resolve(recoveredAgain, stale, 0, 2)
	resolve(recoveredAgain, current, 1, 1)
	resolveOn(pagingStubType, recoveredAgain, stalePager, 0, 2)
	if sent := muteStub.take(); len(sent) != 2 || sent[0] != "resolved" || sent[1] != "resolved" {
		t.Fatalf("sends = %v, want both resolves", sent)
	}
	if len(pagingStub.sends) != 2 {
		t.Fatalf("paging sends = %v, want the stale episode's resolve too", pagingStub.sends)
	}
	if got := stateOf(recoveredAgain, stale); got != "resolved" {
		t.Fatalf("stale route left at %q, want resolved", got)
	}
}

// A relapse notice (a "created" page claimed as the relapse) held because the
// alert was acknowledged or snoozed after it was claimed hands its claim back:
// the route's paged episode goes back to its previous page's, so it is told
// of the relapse once the mute lifts.
func TestHandle_MutedRelapseNoticeReleasesItsClaim(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "relapse-muted")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	alertID, ch := uuid.New(), uuid.New()
	for _, q := range []string{
		`INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, acknowledged_at, relapse_count, reopened_at,
			failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, 'availability', 'acknowledged', NOW() - INTERVAL '1 hour', NOW(), 1, NOW() - INTERVAL '1 minute', 3, NOW(), NOW())`,
	} {
		if _, err := dbClient.ExecContext(ctx, q, alertID, tenantID, monitorID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := dbClient.ExecContext(ctx, `INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, 'ops', $3, '{}'::jsonb, TRUE, NOW(), NOW())`, ch, tenantID, muteStubType); err != nil {
		t.Fatal(err)
	}
	claimedAt := time.Now().Add(-30 * time.Second).UTC().Truncate(time.Microsecond)
	if _, err := dbClient.ExecContext(ctx, `INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type, created_at, updated_at)
		VALUES ($1, $2, $3, 'relapsed', NOW(), NOW())`, alertID, ch, claimedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := dbClient.ExecContext(ctx, `UPDATE alert_notification_states SET paged_relapse = 1 WHERE alert_id = $1`, alertID); err != nil {
		t.Fatal(err)
	}
	muteStub.take()

	previous := 0
	raw, _ := json.Marshal(notifications.DispatchEnvelope{
		V: 1, ChannelID: ch.String(), ChannelType: muteStubType,
		AlertID: alertID.String(), EventType: "created",
		Event: notifications.AlertEvent{Type: "created", Timestamp: claimedAt,
			Alert: notifications.AlertDetails{ID: alertID.String(), RelapseCount: 1, RelapseNotice: true,
				PagedRelapse: 1, PreviousPagedRelapse: &previous}},
		// The alerter's ledger key for the relapse notice: its own claim, in
		// the episode, so it never reads a first page's marker.
		DeliveryKey: notifications.RecipientEventKeyFor("relapsed", time.Time{}, 1),
	})
	c := &Consumer{logger: logger.New("test", "error"), db: dbClient, encryptor: secrets.NoOpEncryptor{}}
	if err := c.handle(ctx, &queue.Message{Data: raw, NumDelivered: 1}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if sent := muteStub.take(); len(sent) != 0 {
		t.Fatalf("a held relapse notice was delivered: %v", sent)
	}
	var paged int
	if err := dbClient.QueryRowContext(ctx, `SELECT paged_relapse FROM alert_notification_states WHERE alert_id = $1 AND channel_id = $2`,
		alertID, ch).Scan(&paged); err != nil {
		t.Fatal(err)
	}
	if paged != 0 {
		t.Fatalf("held relapse notice left the route paged in episode %d, want 0 (due again)", paged)
	}
	var reason string
	if err := dbClient.QueryRowContext(ctx, `SELECT reason FROM alert_notification_deliveries WHERE alert_id = $1 AND channel_id = $2 AND event_type = 'relapsed'`,
		alertID, ch).Scan(&reason); err != nil || reason != "muted_requeued" {
		t.Fatalf("delivery history of the held relapse notice: reason %q (%v), want muted_requeued under event relapsed", reason, err)
	}

	// A redelivery after an attempt that may have reached the provider (its
	// marker stayed) keeps the claim: the route counts as told of the relapse.
	if _, err := dbClient.ExecContext(ctx, `UPDATE alert_notification_states SET paged_relapse = 1 WHERE alert_id = $1`, alertID); err != nil {
		t.Fatal(err)
	}
	if _, err := recipientledger.MarkAttempt(ctx, dbClient, alertID, ch, notifications.RecipientEventKeyFor("relapsed", time.Time{}, 1)); err != nil {
		t.Fatal(err)
	}
	if err := c.handle(ctx, &queue.Message{Data: raw, NumDelivered: 2}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if err := dbClient.QueryRowContext(ctx, `SELECT paged_relapse FROM alert_notification_states WHERE alert_id = $1 AND channel_id = $2`,
		alertID, ch).Scan(&paged); err != nil || paged != 1 {
		t.Fatalf("a possibly delivered relapse notice was handed back: paged %d (%v)", paged, err)
	}
}
