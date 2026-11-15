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
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// A resolve the alerter claimed and queued before the alert relapsed is
// dropped by the worker: "recovered" must never reach a route after the alert
// failed again. A resolve that provably reached nobody (first attempt,
// nothing in the recipient ledger) hands its claim back to 'created', so the
// route counts as still paged and hears the relapse's own resolve; one that
// may have reached the provider (a redelivery) keeps 'resolved', so the route
// is told of the relapse instead; a route the alerter has re-claimed since is
// left alone. A resolve from an earlier episode of an alert that has
// recovered again is dropped too: that recovery has its own resolve.
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
	resolve := func(alertID, ch uuid.UUID, relapse int, attempt uint64) {
		t.Helper()
		raw, _ := json.Marshal(notifications.DispatchEnvelope{
			V: 1, ChannelID: ch.String(), ChannelType: muteStubType,
			AlertID: alertID.String(), EventType: "resolved",
			Event: notifications.AlertEvent{Type: "resolved", Timestamp: claimedAt,
				Alert: notifications.AlertDetails{ID: alertID.String(), RelapseCount: relapse}},
		})
		c := &Consumer{logger: logger.New("test", "error"), db: dbClient, encryptor: secrets.NoOpEncryptor{}}
		if err := c.handle(ctx, &queue.Message{Data: raw, NumDelivered: attempt}); err != nil {
			t.Fatalf("handle: %v", err)
		}
	}
	muteStub.take()

	// Reopened (relapse 1, open) while its episode-0 resolves were queued.
	reopened := uuid.New()
	exec(`INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, relapse_count, reopened_at,
			failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, 'availability', 'active', NOW() - INTERVAL '1 hour', 1, NOW(), 3, NOW(), NOW())`,
		reopened, tenantID, monitorID)
	waiting, retried, fanout, reclaimed := newChannel("waiting"), newChannel("retried"), newChannel("fanout"), newChannel("reclaimed")
	for _, ch := range []uuid.UUID{waiting, retried, fanout} {
		state(reopened, ch, "resolved", claimedAt)
	}
	state(reopened, reclaimed, "relapsed", claimedAt.Add(30*time.Second))
	exec(`INSERT INTO alert_notification_recipient_deliveries (alert_id, channel_id, event_key, recipient_hash)
		VALUES ($1, $2, 'resolved', 'h1')`, reopened, fanout)

	resolve(reopened, waiting, 0, 1)
	resolve(reopened, retried, 0, 2)
	resolve(reopened, fanout, 0, 1)
	resolve(reopened, reclaimed, 0, 1)
	if sent := muteStub.take(); len(sent) != 0 {
		t.Fatalf("resolves of the recovered episode were delivered after the relapse: %v", sent)
	}
	for ch, want := range map[uuid.UUID]string{waiting: "created", retried: "resolved", fanout: "resolved", reclaimed: "relapsed"} {
		if got := stateOf(reopened, ch); got != want {
			t.Errorf("route %s left at %q, want %q", ch, got, want)
		}
	}

	// Reopened and recovered again (relapse 1, resolved): the episode-0
	// resolve is stale; the episode-1 resolve goes out.
	recoveredAgain := uuid.New()
	exec(`INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, resolved_at, relapse_count, reopened_at,
			failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, 'availability', 'resolved', NOW() - INTERVAL '1 hour', NOW(), 1, NOW() - INTERVAL '5 minutes', 3, NOW(), NOW())`,
		recoveredAgain, tenantID, monitorID)
	stale, current := newChannel("stale"), newChannel("current")
	state(recoveredAgain, stale, "resolved", claimedAt)
	state(recoveredAgain, current, "resolved", claimedAt)
	resolve(recoveredAgain, stale, 0, 1)
	resolve(recoveredAgain, current, 1, 1)
	if sent := muteStub.take(); len(sent) != 1 || sent[0] != "resolved" {
		t.Fatalf("sends = %v, want only the current episode's resolve", sent)
	}
	// The stale resolve's route is handed back too: the sweep then sends it
	// the current episode's resolve (alerter dispatchPendingRecoveries).
	if got := stateOf(recoveredAgain, stale); got != "created" {
		t.Fatalf("stale route left at %q, want created", got)
	}
}

// A relapse notice (a "created" page of a reopened alert) held because the
// alert was acknowledged or snoozed after it was claimed hands its claim back
// to 'resolved', so the route is told of the relapse once the mute lifts.
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
	muteStub.take()

	raw, _ := json.Marshal(notifications.DispatchEnvelope{
		V: 1, ChannelID: ch.String(), ChannelType: muteStubType,
		AlertID: alertID.String(), EventType: "created",
		Event: notifications.AlertEvent{Type: "created", Timestamp: claimedAt,
			Alert: notifications.AlertDetails{ID: alertID.String(), RelapseCount: 1}},
	})
	c := &Consumer{logger: logger.New("test", "error"), db: dbClient, encryptor: secrets.NoOpEncryptor{}}
	if err := c.handle(ctx, &queue.Message{Data: raw, NumDelivered: 1}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if sent := muteStub.take(); len(sent) != 0 {
		t.Fatalf("a held relapse notice was delivered: %v", sent)
	}
	var lastEvent string
	if err := dbClient.QueryRowContext(ctx, `SELECT last_event_type FROM alert_notification_states WHERE alert_id = $1 AND channel_id = $2`,
		alertID, ch).Scan(&lastEvent); err != nil {
		t.Fatal(err)
	}
	if lastEvent != "resolved" {
		t.Fatalf("held relapse notice left the route at %q, want resolved", lastEvent)
	}
}
