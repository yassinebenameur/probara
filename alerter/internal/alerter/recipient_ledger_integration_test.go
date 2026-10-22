//go:build !unit

package alerter

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// fanoutStubType is a multi-recipient plugin registered only in this test
// binary. It follows the RecipientLedger contract the SMS plugin follows:
// skip what the ledger reports delivered, record each accepted recipient.
const fanoutStubType = "test_fanout_stub"

var fanoutRecipients = []string{"+15557654321", "+447700900123", "+15550000001"}

type fanoutStubPlugin struct {
	mu      sync.Mutex
	failing map[string]bool
	texts   map[string]map[string]int // event type -> recipient -> texts
}

func (p *fanoutStubPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{Type: fanoutStubType, Capabilities: []plugin.Capability{plugin.CapabilityFollowUp}}
}
func (p *fanoutStubPlugin) Validate(json.RawMessage) error { return nil }

func (p *fanoutStubPlugin) Send(ctx context.Context, req plugin.DispatchRequest) error {
	if req.Recipients == nil {
		return plugin.Permanent(errors.New("delivery layer passed no recipient ledger"))
	}
	done, err := req.Recipients.Delivered(ctx, fanoutRecipients)
	if err != nil {
		return err
	}
	var failed bool
	for _, r := range fanoutRecipients {
		if done[r] {
			continue
		}
		p.mu.Lock()
		fail := p.failing[r]
		if !fail {
			if p.texts[req.Type()] == nil {
				p.texts[req.Type()] = map[string]int{}
			}
			p.texts[req.Type()][r]++
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

func (p *fanoutStubPlugin) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failing = map[string]bool{}
	p.texts = map[string]map[string]int{}
}

func (p *fanoutStubPlugin) setFailing(r string, fail bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failing[r] = fail
}

// assertTexts checks every recipient got exactly want texts of eventType.
func (p *fanoutStubPlugin) assertTexts(t *testing.T, eventType string, want int) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range fanoutRecipients {
		if got := p.texts[eventType][r]; got != want {
			t.Errorf("%s texts to %s = %d, want %d (all: %v)", eventType, r, got, want, p.texts[eventType])
		}
	}
}

var fanoutStub = &fanoutStubPlugin{}

func init() { plugin.Register(fanoutStub) }

// One recipient fails transiently: the claim rolls back and the next cycle
// — here on the other replica — retries. That retry must reach only the
// failed recipient, for the created event and for each reminder round, and
// a new reminder round must reach everyone again.
func TestDispatchRetryAfterPartialFanoutSkipsDeliveredRecipients(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "fanout")
	mustExec(ctx, t, dbClient, `UPDATE tenants SET alert_reminder_seconds = 600 WHERE id = $1`, tenantID)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	channelID := insertTypedChannel(ctx, t, dbClient, tenantID, fanoutStubType)
	alertID := openDownAlert(ctx, t, dbClient, tenantID, monitorID)

	fanoutStub.reset()
	replicas := make([]*Alerter, 2)
	for i := range replicas {
		a := newIntegrationAlerter(dbClient)
		a.sendFunc = a.sendChannelNotification
		replicas[i] = a
	}
	flaky := fanoutRecipients[1]

	// created: replica 0 reaches two of three, replica 1 retries the third.
	fanoutStub.setFailing(flaky, true)
	if err := replicas[0].dispatchOpenAlerts(ctx); err != nil {
		t.Fatalf("dispatch (partial): %v", err)
	}
	if _, _, ok := notificationStateFor(ctx, t, dbClient, alertID, channelID); ok {
		t.Fatalf("a partially failed send must release its claim")
	}
	fanoutStub.setFailing(flaky, false)
	if err := replicas[1].dispatchOpenAlerts(ctx); err != nil {
		t.Fatalf("dispatch (retry): %v", err)
	}
	fanoutStub.assertTexts(t, "created", 1)
	if ev, _, ok := notificationStateFor(ctx, t, dbClient, alertID, channelID); !ok || ev != "created" {
		t.Fatalf("notification state = (%q, %v), want created", ev, ok)
	}

	// Reminder round 1, partially failed then retried on the other replica.
	mustExec(ctx, t, dbClient, `UPDATE alert_notification_states SET last_sent_at = NOW() - INTERVAL '2 hours' WHERE alert_id = $1`, alertID)
	fanoutStub.setFailing(flaky, true)
	if err := replicas[0].dispatchOpenAlerts(ctx); err != nil {
		t.Fatalf("dispatch reminder (partial): %v", err)
	}
	fanoutStub.setFailing(flaky, false)
	if err := replicas[1].dispatchOpenAlerts(ctx); err != nil {
		t.Fatalf("dispatch reminder (retry): %v", err)
	}
	fanoutStub.assertTexts(t, "reminder", 1)

	// Not due: nothing more.
	if err := replicas[0].dispatchOpenAlerts(ctx); err != nil {
		t.Fatalf("dispatch reminder (not due): %v", err)
	}
	fanoutStub.assertTexts(t, "reminder", 1)

	// Reminder round 2 is a new event: everyone gets it once more.
	mustExec(ctx, t, dbClient, `UPDATE alert_notification_states SET last_sent_at = NOW() - INTERVAL '1 hour' WHERE alert_id = $1`, alertID)
	if err := replicas[1].dispatchOpenAlerts(ctx); err != nil {
		t.Fatalf("dispatch reminder round 2: %v", err)
	}
	fanoutStub.assertTexts(t, "reminder", 2)

	// The resolve reaches each recipient once, retried the same way.
	setStateForTest(ctx, t, dbClient, monitorID, "up", time.Now().UTC())
	fanoutStub.setFailing(flaky, true)
	if err := replicas[0].resolveAlertsForRecoveredMonitors(ctx); err != nil {
		t.Fatalf("resolve (partial): %v", err)
	}
	fanoutStub.setFailing(flaky, false)
	for _, a := range replicas {
		if err := a.dispatchPendingRecoveries(ctx); err != nil {
			t.Fatalf("recovery retry: %v", err)
		}
	}
	fanoutStub.assertTexts(t, "resolved", 1)
	fanoutStub.assertTexts(t, "created", 1)
}
