//go:build !unit

package alerter

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
	"github.com/yassinebenameur/probara/shared/notifications/deliverylog"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/recipientledger"
	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/shared/testutil"
)

const deliveryLogStubType = "test_delivery_log_stub"

const (
	stubWebhook = "https://hooks.example.com/services/T0001/B0002/SECRET-PATH-VALUE"
	stubToken   = "tok-very-secret-123456"
)

// deliveryLogStub fails or succeeds on demand, and its failures echo the
// channel's webhook URL and token the way careless provider errors do.
type deliveryLogStub struct {
	mu   sync.Mutex
	next func(cfg plugin.ChannelRef) error
}

func (p *deliveryLogStub) Manifest() plugin.Manifest {
	return plugin.Manifest{Type: deliveryLogStubType, Fields: []plugin.Field{
		{Key: "webhook_url", Type: plugin.FieldTypeURL, Secret: true},
		{Key: "token", Type: plugin.FieldTypeSecret, Secret: true},
	}}
}
func (p *deliveryLogStub) Validate(json.RawMessage) error { return nil }
func (p *deliveryLogStub) Send(_ context.Context, req plugin.DispatchRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.next == nil {
		return nil
	}
	return p.next(req.Channel)
}
func (p *deliveryLogStub) set(f func(plugin.ChannelRef) error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.next = f
}

var deliveryStub = &deliveryLogStub{}

func init() { plugin.Register(deliveryStub) }

type deliveryRow struct {
	EventType, Outcome, Path string
	Attempt                  int
	Reason, Error, Recipient *string
	RecipientHash            *string
	ProviderStatus           *int
	DurationMs               *int
	TenantID                 uuid.UUID
}

func deliveryRows(ctx context.Context, t *testing.T, db *shareddb.Client, channelID uuid.UUID) []deliveryRow {
	t.Helper()
	rows, err := db.QueryContext(ctx, `
		SELECT event_type, outcome, path, attempt, reason, error, recipient, recipient_hash, provider_status, duration_ms, tenant_id
		FROM alert_notification_deliveries WHERE channel_id = $1 ORDER BY created_at, recipient`, channelID)
	if err != nil {
		t.Fatalf("load deliveries: %v", err)
	}
	defer rows.Close()
	var out []deliveryRow
	for rows.Next() {
		var r deliveryRow
		if err := rows.Scan(&r.EventType, &r.Outcome, &r.Path, &r.Attempt, &r.Reason, &r.Error, &r.Recipient,
			&r.RecipientHash, &r.ProviderStatus, &r.DurationMs, &r.TenantID); err != nil {
			t.Fatalf("scan delivery: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// A synchronous send records every attempt: the transient failure whose
// claim rolled back (through the pool, so it survives), the retry that
// delivered, and a permanent failure that keeps its claim, each with the
// provider status where there is one and with the channel's secrets kept
// out of the stored error.
func TestDeliveryHistoryRecordsSyncAttempts(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "delivery-sync")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	channelID := insertTypedChannel(ctx, t, dbClient, tenantID, deliveryLogStubType)
	mustExec(ctx, t, dbClient, `UPDATE alert_channels SET config = $2 WHERE id = $1`, channelID,
		fmt.Sprintf(`{"webhook_url":%q,"token":%q}`, stubWebhook, stubToken))
	alertID := openDownAlert(ctx, t, dbClient, tenantID, monitorID)

	a := newIntegrationAlerter(dbClient)
	a.encryptor = secrets.NoOpEncryptor{}
	a.sendFunc = a.sendChannelNotification

	// 1. Transient failure echoing the webhook URL and token.
	deliveryStub.set(func(ch plugin.ChannelRef) error {
		return fmt.Errorf("Post %q: Authorization: Bearer %s rejected (token %s): connection reset",
			ch.String("webhook_url"), ch.String("token"), ch.String("token"))
	})
	if err := a.dispatchOpenAlerts(ctx); err != nil {
		t.Fatalf("dispatch (failing): %v", err)
	}
	if _, _, ok := notificationStateFor(ctx, t, dbClient, alertID, channelID); ok {
		t.Fatal("a transient failure must release its claim")
	}
	// 2. The next cycle's retry delivers.
	deliveryStub.set(nil)
	if err := a.dispatchOpenAlerts(ctx); err != nil {
		t.Fatalf("dispatch (retry): %v", err)
	}
	// 3. The recovery notice is rejected for good.
	deliveryStub.set(func(plugin.ChannelRef) error {
		return plugin.CheckResponse(&http.Response{StatusCode: http.StatusForbidden, Body: http.NoBody, Header: http.Header{}}, "stub webhook")
	})
	setStateForTest(ctx, t, dbClient, monitorID, "up", time.Now().UTC())
	if err := a.resolveAlertsForRecoveredMonitors(ctx); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if ev, _, _ := notificationStateFor(ctx, t, dbClient, alertID, channelID); ev != "resolved" {
		t.Fatalf("state after a permanent failure = %q, want the claim kept (resolved)", ev)
	}

	got := deliveryRows(ctx, t, dbClient, channelID)
	if len(got) != 3 {
		t.Fatalf("rows = %+v, want 3", got)
	}
	want := []struct {
		event, outcome string
		attempt        int
	}{{"created", "failed", 1}, {"created", "delivered", 2}, {"resolved", "permanent_failure", 1}}
	for i, w := range want {
		r := got[i]
		if r.EventType != w.event || r.Outcome != w.outcome || r.Attempt != w.attempt || r.Path != "sync" || r.TenantID != tenantID {
			t.Errorf("row %d = %+v, want %+v on the sync path", i, r, w)
		}
		if r.DurationMs == nil {
			t.Errorf("row %d has no duration", i)
		}
	}
	if got[1].Error != nil {
		t.Errorf("delivered row carries an error: %q", str(got[1].Error))
	}
	if got[2].ProviderStatus == nil || *got[2].ProviderStatus != http.StatusForbidden {
		t.Errorf("permanent failure provider_status = %v, want 403", got[2].ProviderStatus)
	}
	stored := str(got[0].Error)
	if stored == "" || !strings.Contains(stored, "connection reset") {
		t.Errorf("stored error %q lost the cause", stored)
	}
	var all strings.Builder
	rows, err := dbClient.QueryContext(ctx, `SELECT COALESCE(error, '') FROM alert_notification_deliveries`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		all.WriteString(s)
	}
	rows.Close()
	for _, secret := range []string{stubToken, "SECRET-PATH-VALUE", "/services/"} {
		if strings.Contains(all.String(), secret) {
			t.Errorf("the delivery history stored %q: %q", secret, all.String())
		}
	}

	// A send handed to the delivery workers is theirs to record.
	_, trace := withSendTrace(ctx)
	trace.queued = true
	a.recordDelivery(ctx, nil, trace, "reminder", alertID, channelID, errors.New("boom"))
	if n := len(deliveryRows(ctx, t, dbClient, channelID)); n != 3 {
		t.Fatalf("rows after a queued send = %d, want 3", n)
	}
}

// A fan-out channel gets one row per recipient each attempt tried: the retry
// of a partial failure adds a row for the recipient it reached and none for
// the ones an earlier attempt already had.
func TestDeliveryHistoryRecordsEachRecipientOnce(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "delivery-fanout")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	channelID := insertTypedChannel(ctx, t, dbClient, tenantID, fanoutStubType)
	openDownAlert(ctx, t, dbClient, tenantID, monitorID)

	fanoutStub.reset()
	a := newIntegrationAlerter(dbClient)
	a.sendFunc = a.sendChannelNotification
	flaky := fanoutRecipients[1]
	fanoutStub.setFailing(flaky, true)
	if err := a.dispatchOpenAlerts(ctx); err != nil {
		t.Fatalf("dispatch (partial): %v", err)
	}
	fanoutStub.setFailing(flaky, false)
	if err := a.dispatchOpenAlerts(ctx); err != nil {
		t.Fatalf("dispatch (retry): %v", err)
	}
	fanoutStub.assertTexts(t, "created", 1)

	byHash := map[string][]deliveryRow{}
	for _, r := range deliveryRows(ctx, t, dbClient, channelID) {
		byHash[str(r.RecipientHash)] = append(byHash[str(r.RecipientHash)], r)
	}
	for _, rcpt := range fanoutRecipients {
		rows := byHash[recipientledger.Hash(rcpt)]
		wantRows := 1
		if rcpt == flaky {
			wantRows = 2
		}
		if len(rows) != wantRows {
			t.Fatalf("rows for %s = %+v, want %d", rcpt, rows, wantRows)
		}
		last := rows[len(rows)-1]
		if last.Outcome != "delivered" || last.Attempt != wantRows || strings.Contains(str(last.Recipient), rcpt[:6]) {
			t.Errorf("last row for %s = %+v (recipient %q), want delivered, attempt %d, masked", rcpt, last, str(last.Recipient), wantRows)
		}
		if rcpt == flaky && (rows[0].Outcome != "failed" || rows[0].Error == nil) {
			t.Errorf("first row for the flaky recipient = %+v, want failed with an error", rows[0])
		}
	}
	if len(byHash) != len(fanoutRecipients) {
		t.Fatalf("rows keyed by %d recipients, want %d", len(byHash), len(fanoutRecipients))
	}
}

// Monitor-less alerts are recorded like any other: the offline-location page
// and the mesh-edge page each leave a row on the default channel, and the
// per-channel read names their locations instead of a monitor.
func TestDeliveryHistoryRecordsLocationAndMeshAlerts(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "delivery-locations")
	channelID := insertDefaultChannel(ctx, t, dbClient, tenantID, "ops", 0)
	alive := insertLocationSeen(ctx, t, dbClient, tenantID, "alive", dur(5*time.Second))
	peer := insertLocationSeen(ctx, t, dbClient, tenantID, "peer", dur(5*time.Second))
	insertLocationSeen(ctx, t, dbClient, tenantID, "dead", dur(10*time.Minute))
	insertMeshEdgeState(ctx, t, dbClient, tenantID, alive, peer, "down", 3, "dial tcp: i/o timeout")

	a, _ := newMeshAlerter(dbClient)
	if err := a.evaluateLocationOffline(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.evaluateMeshEdges(ctx); err != nil {
		t.Fatal(err)
	}

	page, err := deliverylog.ListForChannel(ctx, dbClient, tenantID, channelID, nil, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]deliverylog.Delivery{}
	for _, d := range page.Items {
		if d.AlertKind != nil {
			kinds[*d.AlertKind] = d
		}
	}
	offline, ok := kinds["location_offline"]
	if !ok || offline.Outcome != deliverylog.OutcomeDelivered || offline.EventType != "created" ||
		offline.MonitorName != nil || str(offline.SourceLocationName) != "dead" {
		t.Fatalf("location_offline row = %+v (all: %+v)", offline, page.Items)
	}
	mesh, ok := kinds["mesh_edge"]
	if !ok || mesh.Outcome != deliverylog.OutcomeDelivered || mesh.MonitorName != nil ||
		str(mesh.SourceLocationName) != "alive" || str(mesh.TargetLocationName) != "peer" {
		t.Fatalf("mesh_edge row = %+v (all: %+v)", mesh, page.Items)
	}
	if offline.AlertID == nil {
		t.Fatal("location_offline row lost its alert")
	}
	alertPage, err := deliverylog.ListForAlert(ctx, dbClient, tenantID, *offline.AlertID, 1, 50)
	if err != nil || alertPage.Total != 1 {
		t.Fatalf("per-alert read of a monitor-less alert = %+v, %v", alertPage, err)
	}
}

// A history write that cannot complete (the table locked by another session,
// as an overloaded database would leave it waiting) must not cost the claim:
// the page went out, so its claim commits, the next cycle does not send it
// again, and a permanent failure keeps the claim that stops re-sends.
func TestDeliveryHistoryBlockedWriteKeepsTheClaim(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "delivery-blocked")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	sentOK := insertTypedChannel(ctx, t, dbClient, tenantID, "webhook")
	rejected := insertTypedChannel(ctx, t, dbClient, tenantID, "slack")
	alertID := openDownAlert(ctx, t, dbClient, tenantID, monitorID)

	sends := map[uuid.UUID]int{}
	var mu sync.Mutex
	a := newIntegrationAlerter(dbClient)
	a.sendFunc = func(_ context.Context, ch alertChannel, _ string, _ alertSubject, _ *alertRecord, _ *groupDetail, _ time.Time) error {
		mu.Lock()
		sends[ch.ID]++
		mu.Unlock()
		if ch.ID == rejected {
			return plugin.Permanent(errors.New("webhook deleted"))
		}
		return nil
	}

	locker, err := dbClient.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = locker.Rollback() }()
	if _, err := locker.ExecContext(ctx, `LOCK TABLE alert_notification_deliveries IN EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	if err := a.dispatchOpenAlerts(ctx); err != nil {
		t.Fatalf("dispatch with the history blocked: %v", err)
	}
	for _, ch := range []uuid.UUID{sentOK, rejected} {
		if ev, _, ok := notificationStateFor(ctx, t, dbClient, alertID, ch); !ok || ev != "created" {
			t.Fatalf("claim on %s = (%q, %v), want created kept", ch, ev, ok)
		}
	}
	_ = locker.Rollback()
	if err := a.dispatchOpenAlerts(ctx); err != nil {
		t.Fatalf("next cycle: %v", err)
	}
	if sends[sentOK] != 1 || sends[rejected] != 1 {
		t.Fatalf("sends = %v, want one each: a lost claim would page again", sends)
	}
}
