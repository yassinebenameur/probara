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

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/present"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// cardStubType is a chat plugin that posts a card, saves it and edits it
// (plugin.CapabilityMessageUpdate), recording what each send drew.
const cardStubType = "test_card_stub"

type cardSend struct {
	eventType    string
	status       string
	snoozedUntil *time.Time
}

type cardStubPlugin struct {
	mu     sync.Mutex
	sends  []cardSend
	script []error // results of the next "updated" sends; nil once it runs out
}

func (p *cardStubPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{Type: cardStubType, Capabilities: []plugin.Capability{plugin.CapabilityMessageUpdate}}
}
func (p *cardStubPlugin) Validate(json.RawMessage) error { return nil }
func (p *cardStubPlugin) Send(ctx context.Context, req plugin.DispatchRequest) error {
	p.mu.Lock()
	p.sends = append(p.sends, cardSend{req.Type(), req.Event.Alert.Status, req.Event.Alert.SnoozedUntil})
	var scripted error
	if req.Type() == present.EventUpdated && len(p.script) > 0 {
		scripted, p.script = p.script[0], p.script[1:]
	}
	p.mu.Unlock()
	if scripted != nil {
		return scripted
	}
	if req.Type() == present.EventCreated && req.Messages != nil {
		return req.Messages.Save(ctx, json.RawMessage(`{"ts":"1.2"}`))
	}
	if req.Type() == present.EventUpdated && req.Messages != nil {
		if _, ok, err := req.Messages.Load(ctx); err != nil || !ok {
			return errors.New("updated without a saved card")
		}
	}
	return nil
}

func (p *cardStubPlugin) take() []cardSend {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.sends
	p.sends = nil
	return out
}

var cardStub = &cardStubPlugin{}

func init() { plugin.Register(cardStub) }

func cardRendered(ctx context.Context, t *testing.T, db *shareddb.Client, alertID uuid.UUID) string {
	t.Helper()
	var state string
	if err := db.QueryRowContext(ctx, `SELECT rendered_state FROM alert_notification_messages WHERE alert_id = $1`, alertID).Scan(&state); err != nil {
		t.Fatalf("load card: %v", err)
	}
	return state
}

// A chat card follows its alert: the trigger saves it, and every later state
// (acknowledge, snooze, snooze end with unacknowledge, resolve) rewrites it
// exactly once, through the real send path and the delivery history. A
// failed rewrite rolls its claim back and retries; a card of an earlier
// paging episode is left alone.
func TestCardFollowsAlertState(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "cards")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	insertTypedChannel(ctx, t, db, tenantID, cardStubType)
	alertID := openDownAlert(ctx, t, db, tenantID, monitorID)

	a := newDeliveryLogAlerter(db)
	cardStub.take()
	if err := a.dispatchOpenAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	if got := cardStub.take(); len(got) != 1 || got[0].eventType != "created" {
		t.Fatalf("trigger sends = %+v", got)
	}
	if got := cardRendered(ctx, t, db, alertID); got != "active" {
		t.Fatalf("card after trigger = %q", got)
	}
	reconcile := func() []cardSend {
		t.Helper()
		if err := a.dispatchCardUpdates(ctx); err != nil {
			t.Fatalf("dispatchCardUpdates: %v", err)
		}
		return cardStub.take()
	}
	if got := reconcile(); len(got) != 0 {
		t.Fatalf("an up-to-date card was rewritten: %+v", got)
	}

	mustExec(ctx, t, db, `UPDATE alerts SET status = 'acknowledged', acknowledged_at = NOW() WHERE id = $1`, alertID)
	if got := reconcile(); len(got) != 1 || got[0].eventType != "updated" || got[0].status != "acknowledged" || got[0].snoozedUntil != nil {
		t.Fatalf("after ack: %+v", got)
	}
	if got := cardRendered(ctx, t, db, alertID); got != "acknowledged" {
		t.Fatalf("card state after ack = %q", got)
	}
	if got := reconcile(); len(got) != 0 {
		t.Fatalf("rewrote the card twice: %+v", got)
	}

	until := time.Now().Add(time.Hour).Truncate(time.Second)
	mustExec(ctx, t, db, `UPDATE alerts SET snoozed_until = $2 WHERE id = $1`, alertID, until)
	got := reconcile()
	if len(got) != 1 || got[0].snoozedUntil == nil || !got[0].snoozedUntil.Equal(until) {
		t.Fatalf("after snooze: %+v", got)
	}
	if state := cardRendered(ctx, t, db, alertID); !strings.HasPrefix(state, "acknowledged:snoozed:") {
		t.Fatalf("card state after snooze = %q", state)
	}

	// Unacknowledged with the snooze over; the first rewrite fails and
	// leaves the card as it was, the next cycle retries.
	mustExec(ctx, t, db, `UPDATE alerts SET status = 'active', acknowledged_at = NULL, unacknowledged_at = NOW(),
		snoozed_until = NOW() - INTERVAL '1 minute' WHERE id = $1`, alertID)
	cardStub.mu.Lock()
	cardStub.script = []error{errors.New("slack is down")}
	cardStub.mu.Unlock()
	if got := reconcile(); len(got) != 1 {
		t.Fatalf("failing rewrite sends = %+v", got)
	}
	if state := cardRendered(ctx, t, db, alertID); !strings.HasPrefix(state, "acknowledged:snoozed:") {
		t.Fatalf("a failed rewrite moved the card to %q", state)
	}
	if got := reconcile(); len(got) != 1 || got[0].status != "active" || got[0].snoozedUntil != nil {
		t.Fatalf("retry: %+v", got)
	}
	if state := cardRendered(ctx, t, db, alertID); state != "active" {
		t.Fatalf("card after unack = %q", state)
	}

	mustExec(ctx, t, db, `UPDATE alerts SET status = 'resolved', resolved_at = NOW() WHERE id = $1`, alertID)
	if got := reconcile(); len(got) != 1 || got[0].status != "resolved" {
		t.Fatalf("after resolve: %+v", got)
	}
	if got := reconcile(); len(got) != 0 {
		t.Fatalf("a resolved card was rewritten again: %+v", got)
	}

	// The alert relapses: the resolved card belongs to the previous episode
	// and stays as it is until the relapse posts its own.
	mustExec(ctx, t, db, `UPDATE alerts SET status = 'active', resolved_at = NULL, relapse_count = 1, reopened_at = NOW() WHERE id = $1`, alertID)
	if got := reconcile(); len(got) != 0 {
		t.Fatalf("rewrote the previous episode's card: %+v", got)
	}

	// Every rewrite is in the delivery history under its own round; the
	// failed one and its retry share theirs (attempts 1 and 2).
	var updated []deliveryRow
	for _, r := range deliveryRows(ctx, t, db, alertID) {
		if r.eventType == "updated" {
			updated = append(updated, r)
		}
	}
	if len(updated) != 5 {
		t.Fatalf("updated deliveries = %+v", updated)
	}
	keys := map[string]bool{}
	for _, r := range updated {
		if !strings.HasPrefix(r.deliveryKey, "updated:") {
			t.Fatalf("delivery key %q", r.deliveryKey)
		}
		keys[r.deliveryKey] = true
	}
	if len(keys) != 4 || updated[2].outcome != "failed" || updated[3].attempt != 2 || updated[3].deliveryKey != updated[2].deliveryKey {
		t.Fatalf("rounds = %+v", updated)
	}
}

// Two alerter replicas reconciling the same card at once rewrite it once.
func TestCardUpdateClaimedOnce(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "cards-race")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	insertTypedChannel(ctx, t, db, tenantID, cardStubType)
	alertID := openDownAlert(ctx, t, db, tenantID, monitorID)
	a1, a2 := newDeliveryLogAlerter(db), newDeliveryLogAlerter(db)
	if err := a1.dispatchOpenAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	cardStub.take()
	mustExec(ctx, t, db, `UPDATE alerts SET status = 'acknowledged', acknowledged_at = NOW() WHERE id = $1`, alertID)

	var wg sync.WaitGroup
	for _, a := range []*Alerter{a1, a2} {
		wg.Add(1)
		go func(a *Alerter) {
			defer wg.Done()
			if err := a.dispatchCardUpdates(ctx); err != nil {
				t.Errorf("dispatchCardUpdates: %v", err)
			}
		}(a)
	}
	wg.Wait()
	if got := cardStub.take(); len(got) != 1 {
		t.Fatalf("racing replicas sent %d rewrites, want 1", len(got))
	}
}
