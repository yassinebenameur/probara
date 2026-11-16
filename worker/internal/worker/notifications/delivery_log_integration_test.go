//go:build !unit

package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/netguard"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	_ "github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/gotify"
	_ "github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/ntfy"
	"github.com/yassinebenameur/probara/shared/notifications/recipientledger"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/shared/testutil"
)

const (
	deliveryLogStubType = "test_delivery_log_stub"
	deliveryFanoutType  = "test_delivery_fanout_stub"

	stubWebhook = "https://hooks.example.com/services/T0001/B0002/SECRET-PATH-VALUE"
	stubToken   = "tok-very-secret-123456"
)

var deliveryFanoutRecipients = []string{"+15557654321", "+447700900123", "+15550000001"}

// deliveryLogStub returns whatever next says; with fanout it texts
// deliveryFanoutRecipients through the ledger, failing the ones in failing.
type deliveryLogStub struct {
	typ     string
	mu      sync.Mutex
	next    func(plugin.ChannelRef) error
	failing map[string]bool
}

func (p *deliveryLogStub) Manifest() plugin.Manifest {
	return plugin.Manifest{Type: p.typ, Fields: []plugin.Field{
		{Key: "webhook_url", Type: plugin.FieldTypeURL, Secret: true},
		{Key: "token", Type: plugin.FieldTypeSecret, Secret: true},
	}}
}
func (p *deliveryLogStub) Validate(json.RawMessage) error { return nil }
func (p *deliveryLogStub) Send(ctx context.Context, req plugin.DispatchRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.typ == deliveryFanoutType {
		done, err := req.Recipients.Delivered(ctx, deliveryFanoutRecipients)
		if err != nil {
			return err
		}
		failed := false
		for _, r := range deliveryFanoutRecipients {
			if done[r] {
				continue
			}
			if p.failing[r] {
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
	if p.next == nil {
		return nil
	}
	return p.next(req.Channel)
}

var (
	deliveryStub       = &deliveryLogStub{typ: deliveryLogStubType}
	deliveryFanoutStub = &deliveryLogStub{typ: deliveryFanoutType, failing: map[string]bool{}}
)

func init() {
	plugin.Register(deliveryStub)
	plugin.Register(deliveryFanoutStub)
}

type workerDeliveryRow struct {
	Outcome, Path  string
	Attempt        int
	Reason, Error  *string
	RecipientHash  *string
	ProviderStatus *int
}

func workerDeliveryRows(ctx context.Context, t *testing.T, db *shareddb.Client, alertID, channelID uuid.UUID) []workerDeliveryRow {
	t.Helper()
	rows, err := db.QueryContext(ctx, `
		SELECT outcome, path, attempt, reason, error, recipient_hash, provider_status
		FROM alert_notification_deliveries WHERE alert_id = $1 AND channel_id = $2
		ORDER BY created_at, recipient_hash`, alertID, channelID)
	if err != nil {
		t.Fatalf("load deliveries: %v", err)
	}
	defer rows.Close()
	var out []workerDeliveryRow
	for rows.Next() {
		var r workerDeliveryRow
		if err := rows.Scan(&r.Outcome, &r.Path, &r.Attempt, &r.Reason, &r.Error, &r.RecipientHash, &r.ProviderStatus); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func strOf(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

type deliveryFixture struct {
	ctx      context.Context
	t        *testing.T
	db       *shareddb.Client
	tenantID uuid.UUID
	n        int
	// at is every envelope's event time: the time the alerter's claim
	// records, which releaseMutedClaim matches.
	at time.Time
}

func newDeliveryFixture(ctx context.Context, t *testing.T, db *shareddb.Client, name string) *deliveryFixture {
	return &deliveryFixture{ctx: ctx, t: t, db: db, tenantID: testutil.InsertTenant(ctx, t, db, name),
		at: time.Now().Add(-time.Minute).UTC().Truncate(time.Microsecond)}
}

// claim stands in for the alerter's claim of eventType at f.at.
func (f *deliveryFixture) claim(alertID, channelID uuid.UUID, eventType string) {
	f.t.Helper()
	f.exec(`INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())`, alertID, channelID, f.at, eventType)
}

func (f *deliveryFixture) claimed(alertID, channelID uuid.UUID) bool {
	f.t.Helper()
	var ok bool
	if err := f.db.QueryRowContext(f.ctx, `SELECT EXISTS (SELECT 1 FROM alert_notification_states WHERE alert_id = $1 AND channel_id = $2)`,
		alertID, channelID).Scan(&ok); err != nil {
		f.t.Fatal(err)
	}
	return ok
}

func (f *deliveryFixture) exec(q string, args ...any) {
	f.t.Helper()
	if _, err := f.db.ExecContext(f.ctx, q, args...); err != nil {
		f.t.Fatalf("%s: %v", q, err)
	}
}

// alert opens an alert on its own monitor (one open alert per monitor).
func (f *deliveryFixture) alert(status string) uuid.UUID {
	f.t.Helper()
	f.n++
	monitorID := testutil.InsertHTTPMonitor(f.ctx, f.t, f.db, f.tenantID, fmt.Sprintf("api-%d", f.n))
	id := uuid.New()
	f.exec(`INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, 'availability', $4, NOW() - INTERVAL '1 hour', 3, NOW(), NOW())`, id, f.tenantID, monitorID, status)
	if status == "resolved" {
		f.exec(`UPDATE alerts SET resolved_at = NOW() WHERE id = $1`, id)
	}
	if status == "acknowledged" {
		f.exec(`UPDATE alerts SET acknowledged_at = NOW() WHERE id = $1`, id)
	}
	return id
}

func (f *deliveryFixture) channel(typ string, active bool) uuid.UUID {
	f.t.Helper()
	id := uuid.New()
	f.exec(`INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6, NOW(), NOW())`, id, f.tenantID, "ch-"+id.String()[:8], typ,
		fmt.Sprintf(`{"webhook_url":%q,"token":%q}`, stubWebhook, stubToken), active)
	return id
}

func (f *deliveryFixture) handle(alertID, channelID uuid.UUID, channelType, eventType string, attempt uint64) error {
	f.t.Helper()
	raw, _ := json.Marshal(notifications.DispatchEnvelope{
		V: 1, ChannelID: channelID.String(), ChannelType: channelType,
		AlertID: alertID.String(), EventType: eventType,
		Event: notifications.AlertEvent{Type: eventType, Timestamp: f.at},
	})
	c := &Consumer{logger: logger.New("test", "error"), db: f.db, encryptor: secrets.NoOpEncryptor{}}
	return c.handle(f.ctx, &queue.Message{Data: raw, NumDelivered: attempt})
}

// Each queued attempt gets a row with its JetStream attempt number: a
// transient failure (secrets kept out of its error), a permanent rejection
// with the provider's status, a delivery, and the last attempt JetStream
// makes, marked retries_exhausted.
func TestHandle_RecordsAsyncAttempts(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	f := newDeliveryFixture(ctx, t, dbClient, "delivery-async")

	alertID := f.alert("active")
	ch := f.channel(deliveryLogStubType, true)
	leak := func(c plugin.ChannelRef) error {
		return fmt.Errorf("Post %q: token=%s: 503 from upstream", c.String("webhook_url"), c.String("token"))
	}
	deliveryStub.next = leak
	if err := f.handle(alertID, ch, deliveryLogStubType, "created", 1); err == nil {
		t.Fatal("a transient failure must be retried")
	}
	deliveryStub.next = nil
	if err := f.handle(alertID, ch, deliveryLogStubType, "created", 2); err != nil {
		t.Fatalf("delivery: %v", err)
	}
	deliveryStub.next = func(plugin.ChannelRef) error {
		return plugin.CheckResponse(&http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody, Header: http.Header{}}, "stub webhook")
	}
	if err := f.handle(alertID, ch, deliveryLogStubType, "reminder", 1); err != nil {
		t.Fatalf("a permanent failure is acked: %v", err)
	}
	deliveryStub.next = leak
	_ = f.handle(alertID, ch, deliveryLogStubType, "acknowledged", MaxDeliver)
	deliveryStub.next = nil

	got := workerDeliveryRows(ctx, t, dbClient, alertID, ch)
	// The acknowledgement is dropped, not sent: the alert is not
	// acknowledged. The last-attempt case needs an acknowledged alert, below.
	want := []struct {
		outcome, reason string
		attempt         int
	}{{"failed", "", 1}, {"delivered", "", 2}, {"permanent_failure", "", 1}, {"dropped", "unacknowledged", MaxDeliver}}
	if len(got) != len(want) {
		t.Fatalf("rows = %+v, want %d", got, len(want))
	}
	for i, w := range want {
		if got[i].Outcome != w.outcome || strOf(got[i].Reason) != w.reason || got[i].Attempt != w.attempt || got[i].Path != "async" {
			t.Errorf("row %d = %+v (reason %q), want %+v", i, got[i], strOf(got[i].Reason), w)
		}
	}
	if got[2].ProviderStatus == nil || *got[2].ProviderStatus != http.StatusNotFound {
		t.Errorf("provider_status = %v, want 404", got[2].ProviderStatus)
	}
	stored := strOf(got[0].Error)
	for _, secret := range []string{stubToken, "SECRET-PATH-VALUE", "/services/"} {
		if strings.Contains(stored, secret) {
			t.Errorf("stored error %q contains %q", stored, secret)
		}
	}
	if !strings.Contains(stored, "hooks.example.com") || !strings.Contains(stored, "503 from upstream") {
		t.Errorf("stored error %q lost the provider host or the cause", stored)
	}

	// The worker's last attempt that still fails: nothing retries it.
	acked := f.alert("acknowledged")
	deliveryStub.next = leak
	_ = f.handle(acked, ch, deliveryLogStubType, "acknowledged", MaxDeliver)
	deliveryStub.next = nil
	last := workerDeliveryRows(ctx, t, dbClient, acked, ch)
	if len(last) != 1 || last[0].Outcome != "failed" || strOf(last[0].Reason) != "retries_exhausted" {
		t.Fatalf("last attempt rows = %+v, want failed/retries_exhausted", last)
	}
}

// Every event the worker drops on purpose leaves a dropped row with its
// reason, and is never sent.
func TestHandle_RecordsDrops(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	f := newDeliveryFixture(ctx, t, dbClient, "delivery-drops")
	sent := 0
	deliveryStub.next = func(plugin.ChannelRef) error { sent++; return nil }
	defer func() { deliveryStub.next = nil }()

	for _, tc := range []struct {
		name, alertStatus, event, reason string
		active                           bool
		channelType                      string
	}{
		{"page held by an acknowledgement, reached nobody: claim handed back", "acknowledged", "created", "muted_requeued", true, deliveryLogStubType},
		{"page held by a snooze, reached nobody: claim handed back", "snoozed", "created", "muted_requeued", true, deliveryLogStubType},
		{"reminder held by an acknowledgement: claim kept", "acknowledged", "reminder", "muted", true, deliveryLogStubType},
		{"reminder superseded by the resolve", "resolved", "reminder", "resolved", true, deliveryLogStubType},
		{"acknowledgement after an unacknowledge", "active", "acknowledged", "unacknowledged", true, deliveryLogStubType},
		{"channel deactivated", "active", "created", "channel_inactive", false, deliveryLogStubType},
		{"channel type changed", "active", "created", "channel_changed", true, muteStubType},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status := tc.alertStatus
			if status == "snoozed" {
				status = "active"
			}
			alertID := f.alert(status)
			if tc.alertStatus == "snoozed" {
				f.exec(`UPDATE alerts SET snoozed_until = NOW() + INTERVAL '1 hour' WHERE id = $1`, alertID)
			}
			ch := f.channel(tc.channelType, tc.active)
			if tc.reason == "muted_requeued" || tc.reason == "muted" {
				f.claim(alertID, ch, tc.event)
			}
			if err := f.handle(alertID, ch, deliveryLogStubType, tc.event, 1); err != nil {
				t.Fatalf("handle: %v", err)
			}
			got := workerDeliveryRows(ctx, t, dbClient, alertID, ch)
			if len(got) != 1 || got[0].Outcome != "dropped" || strOf(got[0].Reason) != tc.reason {
				t.Fatalf("rows = %+v (reason %q), want one dropped/%s", got, strOf(got[0].Reason), tc.reason)
			}
			if tc.reason == "muted" || tc.reason == "muted_requeued" {
				if kept, want := f.claimed(alertID, ch), tc.reason == "muted"; kept != want {
					t.Fatalf("claim kept = %v, want %v", kept, want)
				}
			}
		})
	}
	if sent != 0 {
		t.Fatalf("dropped events sent %d times", sent)
	}
}

// A redelivered fan-out message records the recipients it reached on this
// attempt only: no second row for the ones the first attempt delivered.
func TestHandle_RecordsEachRecipientOnceAcrossRedeliveries(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	f := newDeliveryFixture(ctx, t, dbClient, "delivery-fanout")
	alertID := f.alert("active")
	ch := f.channel(deliveryFanoutType, true)
	flaky := deliveryFanoutRecipients[2]

	deliveryFanoutStub.failing = map[string]bool{flaky: true}
	if err := f.handle(alertID, ch, deliveryFanoutType, "created", 1); err == nil {
		t.Fatal("a partial failure must be retried")
	}
	deliveryFanoutStub.failing = map[string]bool{}
	if err := f.handle(alertID, ch, deliveryFanoutType, "created", 2); err != nil {
		t.Fatalf("redelivery: %v", err)
	}

	perRecipient := map[string][]workerDeliveryRow{}
	for _, r := range workerDeliveryRows(ctx, t, dbClient, alertID, ch) {
		perRecipient[strOf(r.RecipientHash)] = append(perRecipient[strOf(r.RecipientHash)], r)
	}
	for _, rcpt := range deliveryFanoutRecipients {
		rows := perRecipient[recipientledger.Hash(rcpt)]
		if rcpt != flaky {
			if len(rows) != 1 || rows[0].Outcome != "delivered" || rows[0].Attempt != 1 {
				t.Errorf("rows for %s = %+v, want one delivered on attempt 1", rcpt, rows)
			}
			continue
		}
		if len(rows) != 2 || rows[0].Outcome != "failed" || rows[1].Outcome != "delivered" || rows[1].Attempt != 2 {
			t.Errorf("rows for the flaky recipient = %+v, want failed then delivered on attempt 2", rows)
		}
	}
}

// The page attempt marker decides whether a held page is handed back, and the
// history says which way it went: a page whose earlier attempt failed with
// proof nothing was accepted (a non-2xx answer) is handed back when the mute
// holds its retry; one whose earlier attempt was ambiguous (a timeout after
// sending) keeps its claim. Every attempt is recorded either way.
func TestHandle_RecordsHeldPagesByMarker(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	f := newDeliveryFixture(ctx, t, dbClient, "delivery-marker")
	defer func() { deliveryStub.next = nil }()

	for _, tc := range []struct {
		name   string
		fail   func(plugin.ChannelRef) error
		reason string
	}{
		{"refused: nothing was accepted", func(plugin.ChannelRef) error {
			return plugin.CheckResponse(&http.Response{StatusCode: http.StatusServiceUnavailable, Body: http.NoBody, Header: http.Header{}}, "stub webhook")
		}, "muted_requeued"},
		{"timed out after sending: possibly reached", func(plugin.ChannelRef) error {
			return errors.New("stub webhook: context deadline exceeded")
		}, "muted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			alertID := f.alert("active")
			ch := f.channel(deliveryLogStubType, true)
			f.claim(alertID, ch, "created")
			deliveryStub.next = tc.fail
			if err := f.handle(alertID, ch, deliveryLogStubType, "created", 1); err == nil {
				t.Fatal("a transient failure must be retried")
			}
			f.exec(`UPDATE alerts SET status = 'acknowledged', acknowledged_at = NOW() WHERE id = $1`, alertID)
			if err := f.handle(alertID, ch, deliveryLogStubType, "created", 2); err != nil {
				t.Fatalf("held retry: %v", err)
			}
			got := workerDeliveryRows(ctx, t, dbClient, alertID, ch)
			if len(got) != 2 || got[0].Outcome != "failed" || got[0].Attempt != 1 ||
				got[1].Outcome != "dropped" || strOf(got[1].Reason) != tc.reason || got[1].Attempt != 2 {
				t.Fatalf("rows = %+v, want failed then dropped/%s", got, tc.reason)
			}
			if kept, want := f.claimed(alertID, ch), tc.reason == "muted"; kept != want {
				t.Fatalf("claim kept = %v, want %v", kept, want)
			}
		})
	}
}

// ntfy and Gotify are recorded like every channel: here the default egress
// policy refuses their loopback server, a permanent egress_blocked failure,
// and neither the topic (in the URL) nor the app token reaches the table.
func TestHandle_RecordsNtfyAndGotify(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	prev := plugin.CurrentRuntime()
	plugin.Configure(plugin.Runtime{Egress: netguard.Policy{BlockPrivate: true}})
	t.Cleanup(func() { plugin.Configure(prev) })
	f := newDeliveryFixture(ctx, t, dbClient, "delivery-push")

	for _, tc := range []struct{ typ, config, secret string }{
		{"ntfy", `{"server_url":"https://127.0.0.1:8443","topic":"secret-topic-4b1d9e","priority":"4"}`, "secret-topic-4b1d9e"},
		{"gotify", `{"server_url":"https://127.0.0.1:8443","app_token":"AppTokenSecret123","priority":"8"}`, "AppTokenSecret123"},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			alertID := f.alert("active")
			ch := uuid.New()
			f.exec(`INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
				VALUES ($1, $2, $3, $3, $4::jsonb, TRUE, NOW(), NOW())`, ch, f.tenantID, tc.typ, tc.config)
			if err := f.handle(alertID, ch, tc.typ, "created", 1); err != nil {
				t.Fatalf("a refused destination is acked: %v", err)
			}
			got := workerDeliveryRows(ctx, t, dbClient, alertID, ch)
			if len(got) != 1 || got[0].Outcome != "permanent_failure" || strOf(got[0].Reason) != "egress_blocked" {
				t.Fatalf("rows = %+v (reason %q), want permanent_failure/egress_blocked", got, strOf(got[0].Reason))
			}
			if strings.Contains(strOf(got[0].Error), tc.secret) {
				t.Fatalf("stored error %q contains the channel secret", strOf(got[0].Error))
			}
		})
	}
}
