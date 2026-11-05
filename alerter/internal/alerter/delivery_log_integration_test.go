//go:build !unit

package alerter

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/config"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// deliveryStubType is a single-target plugin whose next results the test
// scripts; nil once the script runs out.
const deliveryStubType = "test_delivery_log_stub"

type deliveryStubPlugin struct {
	mu     sync.Mutex
	script []error
	sends  int
}

func (p *deliveryStubPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{Type: deliveryStubType, Capabilities: []plugin.Capability{plugin.CapabilityFollowUp}}
}
func (p *deliveryStubPlugin) Validate(json.RawMessage) error { return nil }
func (p *deliveryStubPlugin) Send(context.Context, plugin.DispatchRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sends++
	if len(p.script) == 0 {
		return nil
	}
	err := p.script[0]
	p.script = p.script[1:]
	return err
}

func (p *deliveryStubPlugin) reset(script ...error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.script, p.sends = script, 0
}

var deliveryStub = &deliveryStubPlugin{}

func init() { plugin.Register(deliveryStub) }

type deliveryRow struct {
	channelName, eventType, deliveryKey, path, outcome string
	attempt                                            int
	errText                                            string
	total, delivered                                   int
}

func deliveryRows(ctx context.Context, t *testing.T, db *shareddb.Client, alertID uuid.UUID) []deliveryRow {
	t.Helper()
	rows, err := db.QueryContext(ctx, `
		SELECT channel_name, event_type, delivery_key, path, outcome, attempt,
			COALESCE(error, ''), COALESCE(recipients_total, 0), COALESCE(recipients_delivered, 0)
		FROM alert_notification_deliveries WHERE alert_id = $1
		ORDER BY created_at, attempt`, alertID)
	if err != nil {
		t.Fatalf("load delivery rows: %v", err)
	}
	defer rows.Close()
	var out []deliveryRow
	for rows.Next() {
		var r deliveryRow
		if err := rows.Scan(&r.channelName, &r.eventType, &r.deliveryKey, &r.path, &r.outcome, &r.attempt,
			&r.errText, &r.total, &r.delivered); err != nil {
			t.Fatalf("scan delivery row: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func newDeliveryLogAlerter(db *shareddb.Client) *Alerter {
	a := newIntegrationAlerter(db)
	a.sendFunc = a.recordDeliveries(a.sendChannelNotification)
	return a
}

// A synchronous send that succeeds leaves one delivered row, numbered 1,
// with the channel's name at send time.
func TestDeliveryLog_SyncSuccess(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "delivery-ok")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	insertTypedChannel(ctx, t, dbClient, tenantID, deliveryStubType)
	alertID := openDownAlert(ctx, t, dbClient, tenantID, monitorID)

	deliveryStub.reset()
	if err := newDeliveryLogAlerter(dbClient).dispatchOpenAlerts(ctx); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	rows := deliveryRows(ctx, t, dbClient, alertID)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one", rows)
	}
	r := rows[0]
	if r.outcome != "delivered" || r.path != "sync" || r.attempt != 1 || r.eventType != "created" ||
		r.deliveryKey != "created" || r.channelName != deliveryStubType || r.errText != "" || r.total != 0 {
		t.Fatalf("row = %+v, want a delivered sync created attempt 1", r)
	}
}

// A transient failure keeps its row although the claim rolls back; the next
// cycle's retry is attempt 2. The stored error has the channel URL's token
// stripped.
func TestDeliveryLog_TransientThenRetry(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "delivery-retry")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	insertTypedChannel(ctx, t, dbClient, tenantID, deliveryStubType)
	alertID := openDownAlert(ctx, t, dbClient, tenantID, monitorID)

	const token = "T000/B000/XXXXXXXXXXXXXXXXXXXXXXXX"
	deliveryStub.reset(errors.New(`Post "https://hooks.slack.com/services/` + token + `": context deadline exceeded`))
	a := newDeliveryLogAlerter(dbClient)
	if err := a.dispatchOpenAlerts(ctx); err != nil {
		t.Fatalf("dispatch (failing): %v", err)
	}
	if err := a.dispatchOpenAlerts(ctx); err != nil {
		t.Fatalf("dispatch (retry): %v", err)
	}
	rows := deliveryRows(ctx, t, dbClient, alertID)
	if len(rows) != 2 || rows[0].outcome != "failed" || rows[0].attempt != 1 ||
		rows[1].outcome != "delivered" || rows[1].attempt != 2 {
		t.Fatalf("rows = %+v, want failed attempt 1 then delivered attempt 2", rows)
	}
	if strings.Contains(rows[0].errText, token) || !strings.Contains(rows[0].errText, "hooks.slack.com") ||
		!strings.Contains(rows[0].errText, "deadline exceeded") {
		t.Fatalf("stored error = %q, want the host and cause without the token", rows[0].errText)
	}
}

// A permanent failure is recorded once: the claim is kept, so the next cycle
// neither re-sends nor records again.
func TestDeliveryLog_PermanentFailure(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "delivery-permanent")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	insertTypedChannel(ctx, t, dbClient, tenantID, deliveryStubType)
	alertID := openDownAlert(ctx, t, dbClient, tenantID, monitorID)

	deliveryStub.reset(plugin.Permanent(errors.New("webhook returned 404")))
	a := newDeliveryLogAlerter(dbClient)
	for range 2 {
		if err := a.dispatchOpenAlerts(ctx); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
	}
	rows := deliveryRows(ctx, t, dbClient, alertID)
	if len(rows) != 1 || rows[0].outcome != "permanent_failure" || !strings.Contains(rows[0].errText, "404") {
		t.Fatalf("rows = %+v, want one permanent_failure", rows)
	}
}

// With asynchronous dispatch the alerter records the hand-off as queued (and
// a failed publish as failed); the worker records the delivery itself.
func TestDeliveryLog_AsyncHandOff(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "delivery-async")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	channelID := insertTypedChannel(ctx, t, dbClient, tenantID, deliveryStubType)
	alertID := openDownAlert(ctx, t, dbClient, tenantID, monitorID)

	a := newIntegrationAlerter(dbClient)
	a.config = &config.AlerterConfig{AsyncDispatch: true}
	a.nats = &queue.Client{} // never used: the wrapped publish is a stub
	publishErrs := []error{nil, errors.New("nats: timeout")}
	publish := a.recordDeliveries(func(context.Context, alertChannel, string, policyBinding, *alertRecord, *groupDetail, time.Time) error {
		err := publishErrs[0]
		publishErrs = publishErrs[1:]
		return err
	})
	channel := alertChannel{ID: channelID, Name: "ops", Type: deliveryStubType, IsActive: true}
	alert := &alertRecord{ID: alertID}
	now := time.Now().UTC()
	_ = publish(ctx, channel, "created", policyBinding{}, alert, nil, now)
	_ = publish(ctx, channel, "resolved", policyBinding{}, alert, nil, now)

	rows := deliveryRows(ctx, t, dbClient, alertID)
	if len(rows) != 2 ||
		rows[0].outcome != "queued" || rows[0].path != "async" || rows[0].channelName != "ops" ||
		rows[1].outcome != "failed" || rows[1].eventType != "resolved" || rows[1].errText != "nats: timeout" {
		t.Fatalf("rows = %+v, want queued created then failed resolved, both async", rows)
	}
}
