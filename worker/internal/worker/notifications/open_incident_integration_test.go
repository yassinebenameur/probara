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
// back to exactly the row the claim replaced: a route that had heard the
// earlier recovery is 'resolved' in that episode again, with its time, as
// if the acknowledgement had come before the claim (then nothing is
// claimed), so the relapse's own resolve skips it either way.
func TestHandle_HeldRelapseNoticeRestoresTheRow(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "held-notice")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	alertID, ch := uuid.New(), uuid.New()
	heardRecovery := time.Now().Add(-10 * time.Minute).UTC().Truncate(time.Microsecond)
	claimedAt := time.Now().Add(-30 * time.Second).UTC().Truncate(time.Microsecond)
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
	zero := 0
	raw, _ := json.Marshal(notifications.DispatchEnvelope{
		V: 1, ChannelID: ch.String(), ChannelType: muteStubType,
		AlertID: alertID.String(), EventType: "created",
		DeliveryKey: notifications.RecipientEventKeyFor("relapsed", time.Time{}, 1),
		Event: notifications.AlertEvent{Type: "created", Timestamp: claimedAt,
			Alert: notifications.AlertDetails{ID: alertID.String(), RelapseCount: 1, RelapseNotice: true,
				PagedRelapse: 1, PreviousPagedRelapse: &zero}},
		Release: &notifications.ClaimRelease{EventType: "resolved", SentAt: &heardRecovery, PagedRelapse: 0},
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
	if lastEvent != "resolved" || !lastSent.Equal(heardRecovery) || paged != 0 {
		t.Fatalf("row after the hand-back = (%s, %s, %d), want (resolved, %s, 0): as if never claimed", lastEvent, lastSent, paged, heardRecovery)
	}
}
