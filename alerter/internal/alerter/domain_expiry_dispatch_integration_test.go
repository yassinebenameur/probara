//go:build !unit

package alerter

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// domainSend is one notification a channel received.
type domainSend struct {
	event   string
	alertID uuid.UUID
	unknown bool // the alert carried no value ("expiry unknown")
}

type domainSendLog struct {
	mu    sync.Mutex
	sends []domainSend
}

func (l *domainSendLog) send(_ context.Context, _ alertChannel, eventType string, _ alertSubject, alert *alertRecord, _ *groupDetail, _ time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sends = append(l.sends, domainSend{event: eventType, alertID: alert.ID, unknown: alert.MetricValue == nil})
	return nil
}

func (l *domainSendLog) take() []domainSend {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.sends
	l.sends = nil
	return out
}

// domainDispatchSetup is a domain monitor routed to one default channel,
// with a 10-minute reminder interval, and an alerter whose sends are logged.
func domainDispatchSetup(ctx context.Context, t *testing.T) (*shareddb.Client, uuid.UUID, uuid.UUID, uuid.UUID, *Alerter, *domainSendLog) {
	t.Helper()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "domain-dispatch")
	mustExec(ctx, t, dbClient, `UPDATE tenants SET alert_reminder_seconds = 600 WHERE id = $1`, tenantID)
	channelID := insertTypedChannel(ctx, t, dbClient, tenantID, "slack")
	monitorID := insertDomainMonitor(ctx, t, dbClient, tenantID, `{"domain":"example.com","min_days_valid":30}`)
	log := &domainSendLog{}
	a := newIntegrationAlerter(dbClient)
	a.sendFunc = log.send
	return dbClient, tenantID, monitorID, channelID, a, log
}

func tick(ctx context.Context, t *testing.T, a *Alerter) {
	t.Helper()
	if err := a.evaluateDomainExpiry(ctx); err != nil {
		t.Fatalf("evaluateDomainExpiry: %v", err)
	}
	if err := a.dispatchOpenAlerts(ctx); err != nil {
		t.Fatalf("dispatchOpenAlerts: %v", err)
	}
}

// expectSends compares the events received, in order, with their form.
func expectSends(t *testing.T, got []domainSend, want ...domainSend) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("sends = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].event != want[i].event || got[i].unknown != want[i].unknown {
			t.Fatalf("send %d = %+v, want %+v (all: %+v)", i, got[i], want[i], got)
		}
	}
}

func backdateLastSend(ctx context.Context, t *testing.T, db *shareddb.Client, channelID uuid.UUID) {
	t.Helper()
	mustExec(ctx, t, db, `UPDATE alert_notification_states SET last_sent_at = NOW() - INTERVAL '2 hours' WHERE channel_id = $1`, channelID)
}

// A domain entering its window pages the channel, reminds it, and tells it
// when the registration is renewed — through the real dispatch path.
func TestDomainExpiryNotifiesCreatedReminderResolved(t *testing.T) {
	ctx := context.Background()
	db, tenantID, monitorID, channelID, a, log := domainDispatchSetup(ctx, t)
	now := time.Now().UTC()

	insertDomainResult(ctx, t, db, tenantID, monitorID, now.Add(-time.Hour), "success", knownExpiry(12))
	tick(ctx, t, a)
	expectSends(t, log.take(), domainSend{event: "created"})
	tick(ctx, t, a) // nothing new before the reminder interval
	expectSends(t, log.take())

	backdateLastSend(ctx, t, db, channelID)
	tick(ctx, t, a)
	expectSends(t, log.take(), domainSend{event: "reminder"})

	insertDomainResult(ctx, t, db, tenantID, monitorID, now.Add(-time.Minute), "success", knownExpiry(365))
	tick(ctx, t, a)
	expectSends(t, log.take(), domainSend{event: "resolved"})
}

// The "expiry unknown" form notifies the same way, and when a date becomes
// readable inside the window the unknown alert resolves and a dated one
// pages as new: channels are told the alert changed meaning.
func TestDomainExpiryUnknownNotifiesAndChangesForm(t *testing.T) {
	ctx := context.Background()
	db, tenantID, monitorID, channelID, a, log := domainDispatchSetup(ctx, t)
	now := time.Now().UTC()

	insertDomainResult(ctx, t, db, tenantID, monitorID, now.Add(-3*time.Hour), "success", unknownExpiry)
	insertDomainResult(ctx, t, db, tenantID, monitorID, now.Add(-2*time.Hour), "success", unknownExpiry)
	tick(ctx, t, a)
	expectSends(t, log.take(), domainSend{event: "created", unknown: true})

	backdateLastSend(ctx, t, db, channelID)
	tick(ctx, t, a)
	expectSends(t, log.take(), domainSend{event: "reminder", unknown: true})

	insertDomainResult(ctx, t, db, tenantID, monitorID, now.Add(-time.Hour), "success", knownExpiry(5))
	tick(ctx, t, a)
	sends := log.take()
	expectSends(t, sends, domainSend{event: "resolved", unknown: true}, domainSend{event: "created"})
	if sends[0].alertID == sends[1].alertID {
		t.Fatal("the dated alert must be a new alert")
	}
	if got := domainAlerts(ctx, t, db, monitorID); len(got) != 2 || got[0].status != "resolved" || got[1].status != "active" {
		t.Fatalf("alerts = %+v", got)
	}

	// And back: dated → unknown (the date ages out of the lookback).
	mustExec(ctx, t, db, `UPDATE check_results SET created_at = NOW() - INTERVAL '8 days'
		WHERE monitor_id = $1 AND metrics_data #>> '{domain,expires_at}' IS NOT NULL`, monitorID)
	tick(ctx, t, a)
	expectSends(t, log.take(), domainSend{event: "resolved"}, domainSend{event: "created", unknown: true})

	// Renewal readable again and far away: the unknown alert resolves.
	insertDomainResult(ctx, t, db, tenantID, monitorID, now.Add(-time.Minute), "success", knownExpiry(300))
	tick(ctx, t, a)
	expectSends(t, log.take(), domainSend{event: "resolved", unknown: true})
}
