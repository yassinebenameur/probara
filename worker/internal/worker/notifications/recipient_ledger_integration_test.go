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
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// fanoutStubType is a multi-recipient plugin following the RecipientLedger
// contract the SMS plugin follows.
const fanoutStubType = "test_worker_fanout_stub"

var fanoutRecipients = []string{"+15557654321", "+447700900123", "+15550000001"}

type fanoutPlugin struct {
	mu      sync.Mutex
	failing map[string]bool // by "<recipient>" or "<event>|<recipient>"
	texts   map[string]int  // ledger-independent count per "<event>|<recipient>"
}

func (p *fanoutPlugin) Manifest() plugin.Manifest      { return plugin.Manifest{Type: fanoutStubType} }
func (p *fanoutPlugin) Validate(json.RawMessage) error { return nil }
func (p *fanoutPlugin) Send(ctx context.Context, req plugin.DispatchRequest) error {
	if req.Recipients == nil {
		return plugin.Permanent(errors.New("delivery layer passed no recipient ledger"))
	}
	done, err := req.Recipients.Delivered(ctx, fanoutRecipients)
	if err != nil {
		return err
	}
	failed := false
	for _, r := range fanoutRecipients {
		if done[r] {
			continue
		}
		p.mu.Lock()
		fail := p.failing[r] || p.failing[req.Type()+"|"+r]
		if !fail {
			p.texts[req.Type()+"|"+r]++
		}
		p.mu.Unlock()
		if fail {
			failed = true
			continue
		}
		if err := req.Recipients.Record(ctx, r); err != nil {
			return err
		}
	}
	if failed {
		return errors.New("one recipient unreachable")
	}
	return nil
}

func (p *fanoutPlugin) assertTexts(t *testing.T, eventType string, want int) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range fanoutRecipients {
		if got := p.texts[eventType+"|"+r]; got != want {
			t.Errorf("%s texts to %s = %d, want %d", eventType, r, got, want)
		}
	}
}

var fanoutStub = &fanoutPlugin{}

func init() { plugin.Register(fanoutStub) }

// A JetStream redelivery — to another worker, or this one after a restart —
// of a partially delivered event must reach only the recipients the first
// attempt missed. Distinct reminder rounds still reach everyone.
func TestHandle_RedeliverySkipsDeliveredRecipients(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "fanout")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	channelID, alertID := uuid.New(), uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, 'sms', $3, '{}'::jsonb, TRUE, NOW(), NOW())
	`, channelID, tenantID, fanoutStubType); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, kind, status,
			triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, NULL, 'availability', 'active', NOW(), 3, NOW(), NOW())
	`, alertID, tenantID, monitorID); err != nil {
		t.Fatalf("insert alert: %v", err)
	}

	fanoutStub.failing = map[string]bool{}
	fanoutStub.texts = map[string]int{}
	newWorker := func() *Consumer {
		return &Consumer{logger: logger.New("test", "error"), db: dbClient, encryptor: secrets.NoOpEncryptor{}}
	}
	message := func(eventType, deliveryKey string, ts time.Time, delivery uint64) *queue.Message {
		raw, _ := json.Marshal(notifications.DispatchEnvelope{
			V: 1, ChannelID: channelID.String(), ChannelType: fanoutStubType,
			AlertID: alertID.String(), EventType: eventType,
			Event:       notifications.AlertEvent{Type: eventType, Timestamp: ts},
			DeliveryKey: deliveryKey,
		})
		return &queue.Message{Data: raw, NumDelivered: delivery}
	}
	flaky := fanoutRecipients[1]
	now := time.Now().UTC()

	// created: worker A reaches two of three and asks for a redelivery;
	// worker B gets it and texts only the third.
	fanoutStub.failing[flaky] = true
	if err := newWorker().handle(ctx, message("created", "created", now, 1)); err == nil {
		t.Fatalf("partial delivery must ask for a redelivery")
	}
	fanoutStub.failing[flaky] = false
	if err := newWorker().handle(ctx, message("created", "created", now, 2)); err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	fanoutStub.assertTexts(t, "created", 1)

	// Two reminder rounds, keyed by the alerter's claim: each reaches
	// everyone once, the first one through a partial failure.
	round1 := notifications.RecipientEventKey("reminder", now.Add(-2*time.Hour))
	round2 := notifications.RecipientEventKey("reminder", now.Add(-time.Hour))
	fanoutStub.failing[flaky] = true
	if err := newWorker().handle(ctx, message("reminder", round1, now, 1)); err == nil {
		t.Fatalf("partial reminder must ask for a redelivery")
	}
	fanoutStub.failing[flaky] = false
	if err := newWorker().handle(ctx, message("reminder", round1, now, 2)); err != nil {
		t.Fatalf("reminder redelivery: %v", err)
	}
	fanoutStub.assertTexts(t, "reminder", 1)
	if err := newWorker().handle(ctx, message("reminder", round2, now.Add(time.Hour), 1)); err != nil {
		t.Fatalf("reminder round 2: %v", err)
	}
	fanoutStub.assertTexts(t, "reminder", 2)

	// An envelope from an alerter that predates delivery_key falls back to
	// the event timestamp, which every redelivery of that message repeats.
	legacyTS := now.Add(2 * time.Hour)
	fanoutStub.failing[flaky] = true
	if err := newWorker().handle(ctx, message("reminder", "", legacyTS, 1)); err == nil {
		t.Fatalf("partial legacy reminder must ask for a redelivery")
	}
	fanoutStub.failing[flaky] = false
	if err := newWorker().handle(ctx, message("reminder", "", legacyTS, 2)); err != nil {
		t.Fatalf("legacy reminder redelivery: %v", err)
	}
	fanoutStub.assertTexts(t, "reminder", 3)
}

// ackFanoutPlugin is the fan-out stub on a channel that tracks
// acknowledgement state, so a late trigger fires the overtaken-ack re-send.
type ackFanoutPlugin struct{ fanoutPlugin }

const ackFanoutStubType = "test_worker_ack_fanout_stub"

func (p *ackFanoutPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{Type: ackFanoutStubType, Capabilities: []plugin.Capability{plugin.CapabilityAcknowledge}}
}

var ackFanoutStub = &ackFanoutPlugin{}

func init() { plugin.Register(ackFanoutStub) }

// The acknowledgement re-sent after an overtaking trigger must reach every
// recipient again: the original ack already recorded them all under
// "acknowledged", and it was a no-op at the provider. A redelivery of the
// trigger still skips whoever either send already reached.
func TestHandle_OvertakenAckReachesRecipientsTheFirstAckReached(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "ack-fanout")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	channelID, alertID := uuid.New(), uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, 'oncall', $3, '{}'::jsonb, TRUE, NOW(), NOW())
	`, channelID, tenantID, ackFanoutStubType); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, kind, status,
			triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, NULL, 'availability', 'acknowledged', NOW(), 3, NOW(), NOW())
	`, alertID, tenantID, monitorID); err != nil {
		t.Fatalf("insert alert: %v", err)
	}

	ackFanoutStub.failing = map[string]bool{}
	ackFanoutStub.texts = map[string]int{}
	worker := &Consumer{logger: logger.New("test", "error"), db: dbClient, encryptor: secrets.NoOpEncryptor{}}
	now := time.Now().UTC()
	message := func(eventType string, delivery uint64) *queue.Message {
		raw, _ := json.Marshal(notifications.DispatchEnvelope{
			V: 1, ChannelID: channelID.String(), ChannelType: ackFanoutStubType,
			AlertID: alertID.String(), EventType: eventType,
			Event:       notifications.AlertEvent{Type: eventType, Timestamp: now},
			DeliveryKey: eventType,
		})
		return &queue.Message{Data: raw, NumDelivered: delivery}
	}

	// The ack overtakes the trigger and reaches everyone.
	if err := worker.handle(ctx, message("acknowledged", 1)); err != nil {
		t.Fatalf("acknowledged: %v", err)
	}
	ackFanoutStub.assertTexts(t, "acknowledged", 1)

	// The late trigger lands; its re-ack misses one recipient.
	flaky := "acknowledged|" + fanoutRecipients[2]
	ackFanoutStub.failing[flaky] = true
	if err := worker.handle(ctx, message("created", 1)); err == nil {
		t.Fatalf("partial re-ack must ask for a redelivery")
	}
	ackFanoutStub.assertTexts(t, "created", 1)
	ackFanoutStub.failing[flaky] = false
	if err := worker.handle(ctx, message("created", 2)); err != nil {
		t.Fatalf("trigger redelivery: %v", err)
	}
	ackFanoutStub.assertTexts(t, "created", 1)
	ackFanoutStub.assertTexts(t, "acknowledged", 2)
}
