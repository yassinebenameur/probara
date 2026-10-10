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
