//go:build !unit

package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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

const deliveryStubType = "test_worker_delivery_log_stub"

// scriptedPlugin returns its scripted errors in order, then nil.
type scriptedPlugin struct {
	mu     sync.Mutex
	script []error
}

func (p *scriptedPlugin) Manifest() plugin.Manifest      { return plugin.Manifest{Type: deliveryStubType} }
func (p *scriptedPlugin) Validate(json.RawMessage) error { return nil }
func (p *scriptedPlugin) Send(context.Context, plugin.DispatchRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.script) == 0 {
		return nil
	}
	err := p.script[0]
	p.script = p.script[1:]
	return err
}

var scriptedStub = &scriptedPlugin{}

func init() { plugin.Register(scriptedStub) }

type workerDeliveryRow struct {
	eventType, path, outcome, errText, channelName string
	attempt                                        int
}

// Every attempt the worker makes, and every event it drops, leaves a row:
// a transient failure then the redelivery that succeeds, a permanent
// failure, an event dropped as superseded (here by the resolve), and one
// held by the ack.
func TestHandle_RecordsDeliveryOutcomes(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "delivery-log")
	channelID := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, 'ops-webhook', $3, '{}'::jsonb, TRUE, NOW(), NOW())
	`, channelID, tenantID, deliveryStubType); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	// One monitor per alert: a monitor holds at most one open alert per kind
	// (idx_alerts_one_open_per_monitor_kind).
	newAlert := func(status string) uuid.UUID {
		id := uuid.New()
		monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API "+status+" "+id.String()[:8])
		if _, err := dbClient.ExecContext(ctx, `
			INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, kind, status,
				triggered_at, failure_count, created_at, updated_at)
			VALUES ($1, $2, $3, NULL, 'availability', $4, NOW(), 3, NOW(), NOW())
		`, id, tenantID, monitorID, status); err != nil {
			t.Fatalf("insert alert: %v", err)
		}
		return id
	}
	worker := &Consumer{logger: logger.New("test", "error"), db: dbClient, encryptor: secrets.NoOpEncryptor{}}
	message := func(alertID uuid.UUID, eventType string, stopOnAck bool, delivery uint64) *queue.Message {
		raw, _ := json.Marshal(notifications.DispatchEnvelope{
			V: 1, ChannelID: channelID.String(), ChannelType: deliveryStubType,
			AlertID: alertID.String(), EventType: eventType,
			Event:     notifications.AlertEvent{Type: eventType, Timestamp: time.Now().UTC()},
			StopOnAck: stopOnAck,
		})
		return &queue.Message{Data: raw, NumDelivered: delivery}
	}
	rows := func(alertID uuid.UUID) []workerDeliveryRow {
		t.Helper()
		res, err := dbClient.QueryContext(ctx, `
			SELECT event_type, path, outcome, COALESCE(error, ''), channel_name, attempt
			FROM alert_notification_deliveries WHERE alert_id = $1 ORDER BY attempt, created_at`, alertID)
		if err != nil {
			t.Fatalf("load rows: %v", err)
		}
		defer res.Close()
		var out []workerDeliveryRow
		for res.Next() {
			var r workerDeliveryRow
			if err := res.Scan(&r.eventType, &r.path, &r.outcome, &r.errText, &r.channelName, &r.attempt); err != nil {
				t.Fatalf("scan: %v", err)
			}
			out = append(out, r)
		}
		return out
	}

	// Transient failure, then the redelivery succeeds.
	retried := newAlert("active")
	scriptedStub.script = []error{errors.New("provider returned 503 for https://example.test/hook?token=abc123secret")}
	if err := worker.handle(ctx, message(retried, "created", false, 1)); err == nil {
		t.Fatalf("a transient failure must ask for a redelivery")
	}
	if err := worker.handle(ctx, message(retried, "created", false, 2)); err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	got := rows(retried)
	if len(got) != 2 || got[0].outcome != "failed" || got[0].attempt != 1 || got[0].path != "async" ||
		got[1].outcome != "delivered" || got[1].attempt != 2 || got[1].channelName != "ops-webhook" {
		t.Fatalf("retried rows = %+v, want failed attempt 1 then delivered attempt 2", got)
	}
	if strings.Contains(got[0].errText, "abc123secret") || !strings.Contains(got[0].errText, "503") {
		t.Fatalf("stored error = %q, want the status without the token", got[0].errText)
	}

	// Permanent failure: acked, recorded once.
	rejected := newAlert("active")
	scriptedStub.script = []error{plugin.Permanent(errors.New("channel not found (404)"))}
	if err := worker.handle(ctx, message(rejected, "created", false, 1)); err != nil {
		t.Fatalf("a permanent failure must not be retried: %v", err)
	}
	if got := rows(rejected); len(got) != 1 || got[0].outcome != "permanent_failure" {
		t.Fatalf("rejected rows = %+v, want one permanent_failure", got)
	}

	// Superseded: the alert resolved before the reminder was delivered.
	resolved := newAlert("resolved")
	if err := worker.handle(ctx, message(resolved, "reminder", false, 3)); err != nil {
		t.Fatalf("superseded: %v", err)
	}
	if got := rows(resolved); len(got) != 1 || got[0].outcome != "dropped_superseded" || got[0].attempt != 3 {
		t.Fatalf("resolved rows = %+v, want one dropped_superseded attempt 3", got)
	}

	// Held by the ack on a stop-on-ack route.
	acked := newAlert("acknowledged")
	if err := worker.handle(ctx, message(acked, "created", true, 1)); err != nil {
		t.Fatalf("held by ack: %v", err)
	}
	if got := rows(acked); len(got) != 1 || got[0].outcome != "dropped_ack" || got[0].eventType != "created" {
		t.Fatalf("acked rows = %+v, want one dropped_ack", got)
	}
}
