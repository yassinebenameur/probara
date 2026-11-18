//go:build !unit

package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

const keyedStubType = "test_keyed_stub"

// keyedPlugin is a paging provider: it records each call as
// "<event> <episode>", the episode its incident key would carry, and can
// fail the next page sent to a channel.
type keyedPlugin struct {
	mu        sync.Mutex
	calls     []string
	failPages map[string]error
}

func (p *keyedPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{Type: keyedStubType, Capabilities: []plugin.Capability{plugin.CapabilityAcknowledge}}
}
func (p *keyedPlugin) Validate(json.RawMessage) error { return nil }
func (p *keyedPlugin) Send(_ context.Context, req plugin.DispatchRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err, ok := p.failPages[req.Channel.ID]; ok && req.Type() == "created" {
		return err
	}
	p.calls = append(p.calls, fmt.Sprintf("%s %d", req.Type(), req.Event.Alert.PagedRelapse))
	return nil
}
func (p *keyedPlugin) take() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.calls
	p.calls = nil
	return out
}

var keyedStub = &keyedPlugin{failPages: map[string]error{}}

func init() { plugin.Register(keyedStub) }

// A paging route's incident is the one its last delivered page opened. A
// relapse notice claimed but never delivered opens nothing, so everything
// that closes an incident targets the delivered episode: the notice's own
// close of the earlier incident when the notice is dropped (the alert
// resolved again first) or fails for good (permanently, or on its last
// retry), and the next resolve. A delivered notice records its episode.
func TestHandle_PagingIncidentFollowsDeliveredPages(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "open-incident")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := dbClient.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	claimedAt := time.Now().Add(-time.Minute).UTC().Truncate(time.Microsecond)
	newCase := func(status string) (alertID, ch uuid.UUID) {
		alertID, ch = uuid.New(), uuid.New()
		// One monitor per case: one open alert per monitor and kind.
		monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API "+alertID.String())
		resolvedAt := any(nil)
		if status == "resolved" {
			resolvedAt = time.Now()
		}
		exec(`INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, resolved_at, relapse_count, reopened_at,
				failure_count, created_at, updated_at)
			VALUES ($1, $2, $3, 'availability', $4::alert_status, NOW() - INTERVAL '1 hour', $5, 1, NOW() - INTERVAL '5 minutes', 3, NOW(), NOW())`,
			alertID, tenantID, monitorID, status, resolvedAt)
		exec(`INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
			VALUES ($1, $2, 'pager', $3, '{}'::jsonb, TRUE, NOW(), NOW())`, ch, tenantID, keyedStubType)
		// Paged and delivered in episode 0; the relapse notice of episode 1
		// claimed (paged_relapse 1) but not delivered.
		exec(`INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type, paged_relapse, delivered_relapse)
			VALUES ($1, $2, $3, 'relapsed', 1, 0)`, alertID, ch, claimedAt)
		return alertID, ch
	}
	zero := 0
	notice := func(alertID, ch uuid.UUID, attempt uint64) error {
		raw, _ := json.Marshal(notifications.DispatchEnvelope{
			V: 1, ChannelID: ch.String(), ChannelType: keyedStubType,
			AlertID: alertID.String(), EventType: "created",
			DeliveryKey: notifications.RecipientEventKeyFor("relapsed", time.Time{}, 1),
			Event: notifications.AlertEvent{Type: "created", Timestamp: claimedAt,
				Alert: notifications.AlertDetails{ID: alertID.String(), RelapseCount: 1, RelapseNotice: true,
					PagedRelapse: 1, PreviousPagedRelapse: &zero}},
			Release: &notifications.ClaimRelease{EventType: "created", PagedRelapse: 0},
		})
		c := &Consumer{logger: logger.New("test", "error"), db: dbClient, encryptor: secrets.NoOpEncryptor{}}
		return c.handle(ctx, &queue.Message{Data: raw, NumDelivered: attempt})
	}
	resolve := func(alertID, ch uuid.UUID, claimed int) {
		t.Helper()
		raw, _ := json.Marshal(notifications.DispatchEnvelope{
			V: 1, ChannelID: ch.String(), ChannelType: keyedStubType,
			AlertID: alertID.String(), EventType: "resolved",
			DeliveryKey: notifications.RecipientEventKeyFor("resolved", time.Time{}, 1),
			Event: notifications.AlertEvent{Type: "resolved", Timestamp: time.Now(),
				Alert: notifications.AlertDetails{ID: alertID.String(), RelapseCount: 1, PagedRelapse: claimed}},
		})
		c := &Consumer{logger: logger.New("test", "error"), db: dbClient, encryptor: secrets.NoOpEncryptor{}}
		if err := c.handle(ctx, &queue.Message{Data: raw, NumDelivered: 1}); err != nil {
			t.Fatalf("handle resolve: %v", err)
		}
	}
	delivered := func(alertID, ch uuid.UUID) int {
		t.Helper()
		var n int
		if err := dbClient.QueryRowContext(ctx, `SELECT delivered_relapse FROM alert_notification_states WHERE alert_id = $1 AND channel_id = $2`,
			alertID, ch).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	equal := func(got []string, want ...string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}
	keyedStub.take()

	t.Run("dropped: the alert resolved again before the notice went out", func(t *testing.T) {
		alertID, ch := newCase("resolved")
		if err := notice(alertID, ch, 1); err != nil {
			t.Fatal(err)
		}
		if got := keyedStub.take(); !equal(got, "resolved 0") {
			t.Fatalf("calls = %v, want only the close of the episode-0 incident", got)
		}
		// The relapse's resolve, claimed for episode 1, still targets the
		// incident actually open: episode 0 (a no-op now, never a key that
		// was never opened).
		resolve(alertID, ch, 1)
		if got := keyedStub.take(); !equal(got, "resolved 0") {
			t.Fatalf("resolve calls = %v, want the episode-0 incident", got)
		}
		var rows int
		if err := dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_notification_deliveries
			WHERE alert_id = $1 AND event_type = 'resolved' AND delivery_key = 'resolved' AND outcome = 'delivered'`, alertID).Scan(&rows); err != nil || rows != 1 {
			t.Fatalf("history rows for the close = %d (%v), want 1", rows, err)
		}
	})

	t.Run("failed for good: permanently, or on the last retry", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			err     error
			attempt uint64
			closes  bool
		}{
			{"transient, retried later", errors.New("502"), 1, false},
			{"transient, last attempt", errors.New("502"), MaxDeliver, true},
			{"permanent", plugin.Permanent(errors.New("revoked")), 1, true},
		} {
			alertID, ch := newCase("active")
			keyedStub.mu.Lock()
			keyedStub.failPages[ch.String()] = tc.err
			keyedStub.mu.Unlock()
			_ = notice(alertID, ch, tc.attempt)
			got := keyedStub.take()
			if tc.closes != equal(got, "resolved 0") || (!tc.closes && len(got) != 0) {
				t.Fatalf("%s: calls = %v, close expected %v", tc.name, got, tc.closes)
			}
			if d := delivered(alertID, ch); d != 0 {
				t.Fatalf("%s: delivered episode = %d, want 0 (nothing delivered)", tc.name, d)
			}
		}
	})

	t.Run("delivered: the notice closes the earlier incident and becomes the open one", func(t *testing.T) {
		alertID, ch := newCase("active")
		if err := notice(alertID, ch, 1); err != nil {
			t.Fatal(err)
		}
		if got := keyedStub.take(); !equal(got, "created 1") {
			t.Fatalf("calls = %v, want the relapse page", got)
		}
		if d := delivered(alertID, ch); d != 1 {
			t.Fatalf("delivered episode = %d, want 1", d)
		}
	})
}

// A relapse notice held by an acknowledgement after it was claimed is handed
// back to the row the claim replaced, with its time and episode, as if the
// acknowledgement had come before the claim. A replaced 'resolved' stays
// 'resolved' only when that recovery notice possibly reached the channel
// (its delivery key's attempt marker, which a delivered resolve keeps): then
// the relapse's own resolve skips the route, as it would have. A recovery
// notice that never reached it (still on a retry, or dropped once the alert
// reopened) hands the row back as 'created', so the route hears the next
// recovery.
func TestHandle_HeldRelapseNoticeRestoresTheRow(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "held-notice")
	recoveryAt := time.Now().Add(-10 * time.Minute).UTC().Truncate(time.Microsecond)
	claimedAt := time.Now().Add(-30 * time.Second).UTC().Truncate(time.Microsecond)
	for _, tc := range []struct {
		name  string
		heard bool
		want  string
	}{
		{"the recovery notice reached the channel", true, "resolved"},
		{"the recovery notice never reached the channel", false, "created"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API "+tc.want)
			alertID, ch := uuid.New(), uuid.New()
			for _, q := range []struct {
				sql  string
				args []any
			}{
				{`INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, acknowledged_at, relapse_count, reopened_at,
					failure_count, created_at, updated_at)
				VALUES ($1, $2, $3, 'availability', 'acknowledged', NOW() - INTERVAL '1 hour', NOW(), 1, NOW() - INTERVAL '1 minute', 3, NOW(), NOW())`,
					[]any{alertID, tenantID, monitorID}},
				{`INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
				VALUES ($1, $2, 'chat', $3, '{}'::jsonb, TRUE, NOW(), NOW())`, []any{ch, tenantID, muteStubType}},
				{`INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type, paged_relapse, delivered_relapse)
				VALUES ($1, $2, $3, 'relapsed', 1, 0)`, []any{alertID, ch, claimedAt}},
			} {
				if _, err := dbClient.ExecContext(ctx, q.sql, q.args...); err != nil {
					t.Fatal(err)
				}
			}
			if tc.heard {
				// What a delivered episode-0 resolve leaves: its own marker.
				if _, err := recipientledger.MarkAttempt(ctx, dbClient, alertID, ch, notifications.RecipientEventKeyFor("resolved", time.Time{}, 0)); err != nil {
					t.Fatal(err)
				}
			}
			zero := 0
			raw, _ := json.Marshal(notifications.DispatchEnvelope{
				V: 1, ChannelID: ch.String(), ChannelType: muteStubType,
				AlertID: alertID.String(), EventType: "created",
				DeliveryKey: notifications.RecipientEventKeyFor("relapsed", time.Time{}, 1),
				Event: notifications.AlertEvent{Type: "created", Timestamp: claimedAt,
					Alert: notifications.AlertDetails{ID: alertID.String(), RelapseCount: 1, RelapseNotice: true,
						PagedRelapse: 1, PreviousPagedRelapse: &zero}},
				Release: &notifications.ClaimRelease{EventType: "resolved", SentAt: &recoveryAt, PagedRelapse: 0},
			})
			muteStub.take()
			c := &Consumer{logger: logger.New("test", "error"), db: dbClient, encryptor: secrets.NoOpEncryptor{}}
			if err := c.handle(ctx, &queue.Message{Data: raw, NumDelivered: 1}); err != nil {
				t.Fatal(err)
			}
			if sent := muteStub.take(); len(sent) != 0 {
				t.Fatalf("a held relapse notice was sent: %v", sent)
			}
			var lastEvent string
			var lastSent time.Time
			var paged int
			if err := dbClient.QueryRowContext(ctx, `SELECT last_event_type, last_sent_at, paged_relapse FROM alert_notification_states
				WHERE alert_id = $1 AND channel_id = $2`, alertID, ch).Scan(&lastEvent, &lastSent, &paged); err != nil {
				t.Fatal(err)
			}
			if lastEvent != tc.want || !lastSent.Equal(recoveryAt) || paged != 0 {
				t.Fatalf("row after the hand-back = (%s, %s, %d), want (%s, %s, 0)", lastEvent, lastSent, paged, tc.want, recoveryAt)
			}
		})
	}
}

// The recovery notice of episode 0 fails transiently and waits on a retry;
// the alert reopens and the relapse notice is claimed; the retried recovery
// notice is dropped (the alert is open again) while the row says 'relapsed',
// so it hands nothing back; the alert is acknowledged and the retried relapse
// notice is held. The channel never heard the first recovery, so it must
// hear the relapse's: the alerter's resolve claim must win on the row the
// held notice handed back, and the worker deliver it.
func TestHandle_HeldRelapseNoticeAfterADroppedRecoveryStillGetsTheNextRecovery(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "held-after-drop")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	alertID, ch := uuid.New(), uuid.New()
	exec := func(q string, args ...any) int64 {
		t.Helper()
		res, err := dbClient.ExecContext(ctx, q, args...)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		n, _ := res.RowsAffected()
		return n
	}
	exec(`INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, 'availability', 'active', NOW() - INTERVAL '1 hour', 3, NOW(), NOW())`, alertID, tenantID, monitorID)
	exec(`INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, 'chat', $3, '{}'::jsonb, TRUE, NOW(), NOW())`, ch, tenantID, muteStubType)
	// Paged and delivered in episode 0.
	exec(`INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type, paged_relapse, delivered_relapse)
		VALUES ($1, $2, NOW() - INTERVAL '50 minutes', 'created', 0, 0)`, alertID, ch)

	c := &Consumer{logger: logger.New("test", "error"), db: dbClient, encryptor: secrets.NoOpEncryptor{}}
	handle := func(env notifications.DispatchEnvelope, attempt uint64) error {
		raw, _ := json.Marshal(env)
		return c.handle(ctx, &queue.Message{Data: raw, NumDelivered: attempt})
	}
	failNext := func() {
		muteStub.mu.Lock()
		defer muteStub.mu.Unlock()
		if muteStub.fail == nil {
			muteStub.fail = map[string]error{}
		}
		muteStub.fail[ch.String()] = plugin.NotDelivered(errors.New("returned status 503"))
	}
	row := func() (string, int) {
		t.Helper()
		var event string
		var paged int
		if err := dbClient.QueryRowContext(ctx, `SELECT last_event_type, paged_relapse FROM alert_notification_states
			WHERE alert_id = $1 AND channel_id = $2`, alertID, ch).Scan(&event, &paged); err != nil {
			t.Fatal(err)
		}
		return event, paged
	}
	// The alerter's resolve claim (claimNotificationTx): wins unless the row
	// already says 'resolved'.
	claimResolve := func(at time.Time) bool {
		return exec(`UPDATE alert_notification_states SET last_sent_at = $3, last_event_type = 'resolved', updated_at = NOW()
			WHERE alert_id = $1 AND channel_id = $2 AND last_event_type <> 'resolved'`, alertID, ch, at) == 1
	}
	resolveEnvelope := func(at time.Time, episode int) notifications.DispatchEnvelope {
		return notifications.DispatchEnvelope{
			V: 1, ChannelID: ch.String(), ChannelType: muteStubType,
			AlertID: alertID.String(), EventType: "resolved",
			DeliveryKey: notifications.RecipientEventKeyFor("resolved", time.Time{}, episode),
			Event: notifications.AlertEvent{Type: "resolved", Timestamp: at,
				Alert: notifications.AlertDetails{ID: alertID.String(), RelapseCount: episode, PagedRelapse: 0}},
		}
	}
	muteStub.take()

	// 1. The alert recovers; the recovery notice is claimed and queued, and
	// the provider answers 503: it waits on its retry backoff.
	exec(`UPDATE alerts SET status = 'resolved', resolved_at = NOW(), updated_at = NOW() WHERE id = $1`, alertID)
	recoveredAt := time.Now().Add(-5 * time.Minute).UTC().Truncate(time.Microsecond)
	if !claimResolve(recoveredAt) {
		t.Fatal("first recovery claim lost")
	}
	firstRecovery := resolveEnvelope(recoveredAt, 0)
	failNext()
	if err := handle(firstRecovery, 1); err == nil {
		t.Fatal("the failing recovery notice was not retried")
	}

	// 2. The monitor fails again and the alert reopens; the relapse notice is
	// claimed (resolved -> relapsed), and its first attempt fails too.
	exec(`UPDATE alerts SET status = 'active', resolved_at = NULL, relapse_count = 1, reopened_at = NOW(), updated_at = NOW()
		WHERE id = $1`, alertID)
	claimedAt := time.Now().Add(-4 * time.Minute).UTC().Truncate(time.Microsecond)
	if exec(`UPDATE alert_notification_states SET last_sent_at = $3, last_event_type = 'relapsed', paged_relapse = 1, updated_at = NOW()
		WHERE alert_id = $1 AND channel_id = $2 AND paged_relapse < 1`, alertID, ch, claimedAt) != 1 {
		t.Fatal("relapse claim lost")
	}
	zero := 0
	notice := notifications.DispatchEnvelope{
		V: 1, ChannelID: ch.String(), ChannelType: muteStubType,
		AlertID: alertID.String(), EventType: "created",
		DeliveryKey: notifications.RecipientEventKeyFor("relapsed", time.Time{}, 1),
		Event: notifications.AlertEvent{Type: "created", Timestamp: claimedAt,
			Alert: notifications.AlertDetails{ID: alertID.String(), RelapseCount: 1, RelapseNotice: true,
				PagedRelapse: 1, PreviousPagedRelapse: &zero}},
		Release: &notifications.ClaimRelease{EventType: "resolved", SentAt: &recoveredAt, PagedRelapse: 0},
	}
	failNext()
	if err := handle(notice, 1); err == nil {
		t.Fatal("the failing relapse notice was not retried")
	}

	// 3. The recovery notice's retry is dropped: the alert is open again.
	// The row no longer records its claim, so nothing is handed back.
	if err := handle(firstRecovery, 2); err != nil {
		t.Fatal(err)
	}
	if event, _ := row(); event != "relapsed" {
		t.Fatalf("row after the dropped recovery = %s, want relapsed", event)
	}

	// 4. An operator acknowledges; the relapse notice's retry is held.
	exec(`UPDATE alerts SET status = 'acknowledged', acknowledged_at = NOW(), updated_at = NOW() WHERE id = $1`, alertID)
	if err := handle(notice, 2); err != nil {
		t.Fatal(err)
	}
	if sent := muteStub.take(); len(sent) != 0 {
		t.Fatalf("sent so far = %v, want nothing (every send failed, was dropped or held)", sent)
	}
	if event, paged := row(); event != "created" || paged != 0 {
		t.Fatalf("row after the held notice = (%s, %d), want (created, 0): the channel never heard the recovery", event, paged)
	}

	// 5. The alert recovers while still acknowledged: the alerter claims the
	// relapse's recovery notice for this route and the worker delivers it.
	exec(`UPDATE alerts SET status = 'resolved', resolved_at = NOW(), updated_at = NOW() WHERE id = $1`, alertID)
	finalAt := time.Now().UTC().Truncate(time.Microsecond)
	if !claimResolve(finalAt) {
		t.Fatal("the relapse's recovery was not claimed for a route that never heard a recovery")
	}
	if err := handle(resolveEnvelope(finalAt, 1), 1); err != nil {
		t.Fatal(err)
	}
	if sent := muteStub.take(); len(sent) != 1 || sent[0] != "resolved" {
		t.Fatalf("sent = %v, want the relapse's recovery notice", sent)
	}
}
