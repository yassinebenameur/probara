//go:build !unit

package notifications

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/shared/testutil"
)

const reopenStubType = "test_reopen_stub"

// reopenPlugin records deliveries (event and relapse) and can hold a
// "resolved" send open, so a test can reopen the alert while it is in flight.
type reopenPlugin struct {
	mu      sync.Mutex
	order   []string
	started chan struct{}
	release chan struct{}
}

func (p *reopenPlugin) Manifest() plugin.Manifest      { return plugin.Manifest{Type: reopenStubType} }
func (p *reopenPlugin) Validate(json.RawMessage) error { return nil }
func (p *reopenPlugin) Send(_ context.Context, req plugin.DispatchRequest) error {
	if req.Type() == "resolved" && p.started != nil {
		close(p.started)
		p.started = nil
		<-p.release
	}
	p.mu.Lock()
	p.order = append(p.order, req.Type())
	p.mu.Unlock()
	return nil
}

func (p *reopenPlugin) delivered() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.order...)
}

var reopenStub = &reopenPlugin{}

func mustExecWorker(ctx context.Context, t *testing.T, db *shareddb.Client, q string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(ctx, q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func init() { plugin.Register(reopenStub) }

// Resolves now take the alert row lock too, because alerts can reopen. A
// resolve mid-send holds a reopen back until it is out (so the relapse's
// trigger lands after it), and a resolve still queued when the alert
// reopened is dropped: delivered after the relapse's trigger, it would close
// the provider incident of a live outage.
func TestHandle_ReopenSupersedesQueuedResolve(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "reopen")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	channelID, alertID := uuid.New(), uuid.New()
	mustExecWorker(ctx, t, dbClient, `INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, 'pager', $3, '{}'::jsonb, TRUE, NOW(), NOW())`, channelID, tenantID, reopenStubType)
	mustExecWorker(ctx, t, dbClient, `INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, kind, status,
			triggered_at, resolved_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, NULL, 'availability', 'resolved', NOW() - INTERVAL '5 minutes', NOW(), 3, NOW(), NOW())`,
		alertID, tenantID, monitorID)

	worker := func() *Consumer {
		return &Consumer{logger: logger.New("test", "error"), db: dbClient, encryptor: secrets.NoOpEncryptor{}}
	}
	envelope := func(eventType string, relapse int) *queue.Message {
		raw, _ := json.Marshal(notifications.DispatchEnvelope{
			V: 1, ChannelID: channelID.String(), ChannelType: reopenStubType,
			AlertID: alertID.String(), EventType: eventType,
			Event: notifications.AlertEvent{Type: eventType, Alert: notifications.AlertDetails{RelapseCount: relapse}},
		})
		return &queue.Message{Data: raw}
	}
	reopen := `UPDATE alerts SET status = 'active', resolved_at = NULL, relapse_count = relapse_count + 1,
		reopened_at = NOW(), updated_at = NOW() WHERE id = $1 AND status = 'resolved'`

	// 1. A resolve in flight holds the reopen back until it is delivered.
	reopenStub.started = make(chan struct{})
	reopenStub.release = make(chan struct{})
	started := reopenStub.started
	inFlight := make(chan error, 1)
	go func() { inFlight <- worker().handle(ctx, envelope("resolved", 0)) }()
	<-started
	reopened := make(chan error, 1)
	go func() {
		_, err := dbClient.ExecContext(ctx, reopen, alertID)
		reopened <- err
	}()
	select {
	case err := <-reopened:
		t.Fatalf("the reopen committed while the resolve was still in flight (err=%v)", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(reopenStub.release)
	if err := <-inFlight; err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := <-reopened; err != nil {
		t.Fatalf("reopen: %v", err)
	}
	// The relapse's page follows.
	if err := worker().handle(ctx, envelope("created", 1)); err != nil {
		t.Fatalf("relapse page: %v", err)
	}

	// 2. A redelivered copy of the episode-0 resolve is now stale: dropped.
	if err := worker().handle(ctx, envelope("resolved", 0)); err != nil {
		t.Fatalf("stale resolve: %v", err)
	}
	// So is a retried episode-0 trigger.
	if err := worker().handle(ctx, envelope("created", 0)); err != nil {
		t.Fatalf("stale trigger: %v", err)
	}
	if got := reopenStub.delivered(); len(got) != 2 || got[0] != "resolved" || got[1] != "created" {
		t.Fatalf("delivery order = %v, want [resolved created]", got)
	}

	// 3. The relapse's own resolve goes out.
	mustExecWorker(ctx, t, dbClient, `UPDATE alerts SET status = 'resolved', resolved_at = NOW() WHERE id = $1`, alertID)
	if err := worker().handle(ctx, envelope("resolved", 1)); err != nil {
		t.Fatalf("relapse resolve: %v", err)
	}
	if got := reopenStub.delivered(); len(got) != 3 || got[2] != "resolved" {
		t.Fatalf("delivery order = %v, want the relapse's resolve last", got)
	}
}

// A relapse's page held by an ack (the operator acknowledged the relapse
// while its page sat on a retry backoff) hands its 'reopened' claim back to
// 'resolved', so the alerter claims the reopened page again after an unack.
func TestHandle_MutedRelapsePageReleasesReopenedClaim(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "reopen-muted")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	channelID, alertID := uuid.New(), uuid.New()
	mustExecWorker(ctx, t, dbClient, `INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, 'pager', $3, '{}'::jsonb, TRUE, NOW(), NOW())`, channelID, tenantID, reopenStubType)
	mustExecWorker(ctx, t, dbClient, `INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, kind, status,
			triggered_at, acknowledged_at, relapse_count, reopened_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, NULL, 'availability', 'acknowledged', NOW() - INTERVAL '5 minutes', NOW(), 1, NOW(), 3, NOW(), NOW())`,
		alertID, tenantID, monitorID)
	claimedAt := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Minute)
	mustExecWorker(ctx, t, dbClient, `INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type, created_at, updated_at)
		VALUES ($1, $2, $3, 'reopened', NOW(), NOW())`, alertID, channelID, claimedAt)

	raw, _ := json.Marshal(notifications.DispatchEnvelope{
		V: 1, ChannelID: channelID.String(), ChannelType: reopenStubType,
		AlertID: alertID.String(), EventType: "created", StopOnAck: true,
		Event: notifications.AlertEvent{Type: "created", Timestamp: claimedAt, Alert: notifications.AlertDetails{RelapseCount: 1}},
	})
	before := len(reopenStub.delivered())
	w := &Consumer{logger: logger.New("test", "error"), db: dbClient, encryptor: secrets.NoOpEncryptor{}}
	if err := w.handle(ctx, &queue.Message{Data: raw}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(reopenStub.delivered()) != before {
		t.Fatal("a relapse page held by the ack was delivered")
	}
	var lastEvent string
	if err := dbClient.QueryRowContext(ctx, `SELECT last_event_type FROM alert_notification_states WHERE alert_id = $1`, alertID).Scan(&lastEvent); err != nil {
		t.Fatal(err)
	}
	if lastEvent != "resolved" {
		t.Fatalf("held relapse page left the route at %q, want resolved", lastEvent)
	}
}

// A resolve dropped because the alert reopened while it sat on a retry
// backoff never reached the provider, so the route's 'resolved' claim is
// handed back to 'created': the route still counts as paged and hears the
// relapse's resolve. A route the 'reopened' claim has re-paged since (its
// claim no longer carries the resolve's timestamp) is left alone.
func TestHandle_SupersededResolveReleasesItsClaim(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "reopen-release")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	alertID := uuid.New()
	mustExecWorker(ctx, t, dbClient, `INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, kind, status,
			triggered_at, relapse_count, reopened_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, NULL, 'availability', 'active', NOW() - INTERVAL '20 minutes', 1, NOW(), 3, NOW(), NOW())`,
		alertID, tenantID, monitorID)
	resolvedAt := time.Now().UTC().Truncate(time.Microsecond).Add(-2 * time.Minute)
	route := func(name, lastEvent string, lastSent time.Time) uuid.UUID {
		id := uuid.New()
		mustExecWorker(ctx, t, dbClient, `INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
			VALUES ($1, $2, $3, $4, '{}'::jsonb, TRUE, NOW(), NOW())`, id, tenantID, name, reopenStubType)
		mustExecWorker(ctx, t, dbClient, `INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type, created_at, updated_at)
			VALUES ($1, $2, $3, $4, NOW(), NOW())`, alertID, id, lastSent, lastEvent)
		return id
	}
	waiting := route("waiting", "resolved", resolvedAt)
	repaged := route("repaged", "reopened", resolvedAt.Add(time.Minute))

	before := len(reopenStub.delivered())
	for _, ch := range []uuid.UUID{waiting, repaged} {
		raw, _ := json.Marshal(notifications.DispatchEnvelope{
			V: 1, ChannelID: ch.String(), ChannelType: reopenStubType,
			AlertID: alertID.String(), EventType: "resolved",
			Event: notifications.AlertEvent{Type: "resolved", Timestamp: resolvedAt, Alert: notifications.AlertDetails{RelapseCount: 0}},
		})
		w := &Consumer{logger: logger.New("test", "error"), db: dbClient, encryptor: secrets.NoOpEncryptor{}}
		if err := w.handle(ctx, &queue.Message{Data: raw}); err != nil {
			t.Fatalf("handle: %v", err)
		}
	}
	if len(reopenStub.delivered()) != before {
		t.Fatal("a resolve of an earlier episode was delivered after the reopen")
	}
	stateOf := func(ch uuid.UUID) string {
		var lastEvent string
		if err := dbClient.QueryRowContext(ctx, `SELECT last_event_type FROM alert_notification_states WHERE alert_id = $1 AND channel_id = $2`,
			alertID, ch).Scan(&lastEvent); err != nil {
			t.Fatal(err)
		}
		return lastEvent
	}
	if got := stateOf(waiting); got != "created" {
		t.Fatalf("dropped resolve left the route at %q, want created", got)
	}
	if got := stateOf(repaged); got != "reopened" {
		t.Fatalf("re-paged route moved to %q, want reopened", got)
	}
}
