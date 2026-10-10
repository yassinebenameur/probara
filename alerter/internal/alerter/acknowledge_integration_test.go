package alerter

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// ackStubType is a paging-style plugin registered only in this test binary,
// so dispatchAcknowledgements has an ack-capable channel type to target.
const ackStubType = "test_ack_stub"

type ackStubPlugin struct{}

func (ackStubPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Type:         ackStubType,
		Capabilities: []plugin.Capability{plugin.CapabilityAcknowledge},
	}
}
func (ackStubPlugin) Validate(json.RawMessage) error                     { return nil }
func (ackStubPlugin) Send(context.Context, plugin.DispatchRequest) error { return nil }

func init() { plugin.Register(ackStubPlugin{}) }

func insertTypedChannel(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID uuid.UUID, typ string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	mustExec(ctx, t, db, `
		INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $3, '{}'::jsonb, TRUE, NOW(), NOW())
	`, id, tenantID, typ)
	mustExec(ctx, t, db, `
		INSERT INTO tenant_default_channels (tenant_id, channel_id, delay_seconds, position)
		VALUES ($1, $2, 0, (SELECT COUNT(*) FROM tenant_default_channels WHERE tenant_id = $1))
	`, tenantID, id)
	return id
}

// An acknowledgement reaches each paging channel that was paged exactly once,
// even with two replicas racing, never reaches chat/email channels, and stops
// once the alert resolves.
func TestDispatchAcknowledgementsOncePerPagingChannel(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "ack")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	pagingID := insertTypedChannel(ctx, t, dbClient, tenantID, ackStubType)
	insertTypedChannel(ctx, t, dbClient, tenantID, "email")
	alertID := openDownAlert(ctx, t, dbClient, tenantID, monitorID)

	counter := newSendCounter(200 * time.Millisecond)
	replicas := newReplicaPair(dbClient, counter)
	runConcurrently(t, replicas, func(a *Alerter) error { return a.dispatchOpenAlerts(ctx) })
	if got := counter.count("created"); got != 2 {
		t.Fatalf("created sends = %d, want one per channel (2)", got)
	}

	// Not acknowledged yet: nothing to send.
	runConcurrently(t, replicas, func(a *Alerter) error { return a.dispatchAcknowledgements(ctx) })
	if got := counter.count("acknowledged"); got != 0 {
		t.Fatalf("acknowledged sends before ack = %d, want 0", got)
	}

	mustExec(ctx, t, dbClient, `UPDATE alerts SET status = 'acknowledged', acknowledged_at = NOW() WHERE id = $1`, alertID)
	for round := 0; round < 3; round++ {
		runConcurrently(t, replicas, func(a *Alerter) error { return a.dispatchAcknowledgements(ctx) })
	}
	if got := counter.count("acknowledged"); got != 1 {
		t.Fatalf("acknowledged sends = %d, want exactly 1 (paging channel only)", got)
	}
	var ackSentAt *time.Time
	if err := dbClient.QueryRowContext(ctx, `
		SELECT acknowledged_sent_at FROM alert_notification_states WHERE alert_id = $1 AND channel_id = $2
	`, alertID, pagingID).Scan(&ackSentAt); err != nil || ackSentAt == nil {
		t.Fatalf("acknowledged_sent_at not recorded: %v %v", ackSentAt, err)
	}
	if eventType, _, _ := notificationStateFor(ctx, t, dbClient, alertID, pagingID); eventType != "created" {
		t.Fatalf("ack must not overwrite last_event_type, got %q", eventType)
	}
}

func TestDispatchAcknowledgementsSkipsResolvedAlerts(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "ack-resolved")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	insertTypedChannel(ctx, t, dbClient, tenantID, ackStubType)
	alertID := openDownAlert(ctx, t, dbClient, tenantID, monitorID)

	counter := newSendCounter(0)
	a := newIntegrationAlerter(dbClient)
	a.sendFunc = counter.send
	if err := a.dispatchOpenAlerts(ctx); err != nil {
		t.Fatalf("dispatchOpenAlerts: %v", err)
	}
	mustExec(ctx, t, dbClient, `UPDATE alerts SET status = 'resolved', acknowledged_at = NOW(), resolved_at = NOW() WHERE id = $1`, alertID)
	if err := a.dispatchAcknowledgements(ctx); err != nil {
		t.Fatalf("dispatchAcknowledgements: %v", err)
	}
	if got := counter.count("acknowledged"); got != 0 {
		t.Fatalf("acknowledged sends for a resolved alert = %d, want 0", got)
	}
}

// A plugin.Permanent failure keeps the claim, so the synchronous path does not
// re-send (and re-fail) the same page every evaluation cycle; a transient
// failure releases it for a retry.
func TestDeliverNotificationKeepsClaimOnPermanentFailure(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "permanent")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	channelID := insertTypedChannel(ctx, t, dbClient, tenantID, "slack")
	alertID := openDownAlert(ctx, t, dbClient, tenantID, monitorID)

	attempts := 0
	a := newIntegrationAlerter(dbClient)
	a.sendFunc = func(context.Context, alertChannel, string, policyBinding, *alertRecord, *groupDetail, time.Time) error {
		attempts++
		return errors.New("connection reset")
	}
	for i := 0; i < 2; i++ {
		if err := a.dispatchOpenAlerts(ctx); err != nil {
			t.Fatalf("dispatchOpenAlerts: %v", err)
		}
	}
	if attempts != 2 {
		t.Fatalf("transient failure: attempts = %d, want a retry each cycle (2)", attempts)
	}
	if _, _, ok := notificationStateFor(ctx, t, dbClient, alertID, channelID); ok {
		t.Fatal("transient failure must release the claim")
	}

	attempts = 0
	a.sendFunc = func(context.Context, alertChannel, string, policyBinding, *alertRecord, *groupDetail, time.Time) error {
		attempts++
		return plugin.Permanent(errors.New("slack webhook returned status 404: no_service"))
	}
	for i := 0; i < 3; i++ {
		if err := a.dispatchOpenAlerts(ctx); err != nil {
			t.Fatalf("dispatchOpenAlerts: %v", err)
		}
	}
	if attempts != 1 {
		t.Fatalf("permanent failure: attempts = %d, want 1", attempts)
	}
	if eventType, _, ok := notificationStateFor(ctx, t, dbClient, alertID, channelID); !ok || eventType != "created" {
		t.Fatalf("permanent failure must keep the created claim, got %q ok=%v", eventType, ok)
	}
}

// The async worker holds the alert row FOR SHARE across its provider call.
// The alerter's claim must not queue behind it (that stalled dispatch to every
// other channel on webhook latency), but resolution still must.
func TestDeliverNotificationDoesNotWaitOnInFlightWorkerSend(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "sharelock")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	channelID := insertTypedChannel(ctx, t, dbClient, tenantID, "slack")
	alertID := openDownAlert(ctx, t, dbClient, tenantID, monitorID)

	// Stand-in for a worker mid-send on another channel of the same alert.
	worker, err := dbClient.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = worker.Rollback() }()
	if _, err := worker.ExecContext(ctx, `SELECT status FROM alerts WHERE id = $1 FOR SHARE`, alertID); err != nil {
		t.Fatal(err)
	}

	sent := 0
	a := newIntegrationAlerter(dbClient)
	a.sendFunc = func(context.Context, alertChannel, string, policyBinding, *alertRecord, *groupDetail, time.Time) error {
		sent++
		return nil
	}
	claimCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := a.dispatchOpenAlerts(claimCtx); err != nil {
		t.Fatalf("dispatchOpenAlerts: %v", err)
	}
	if sent != 1 {
		t.Fatalf("sent = %d, want the claim to proceed alongside the worker's share lock", sent)
	}
	if eventType, _, ok := notificationStateFor(ctx, t, dbClient, alertID, channelID); !ok || eventType != "created" {
		t.Fatalf("created claim not recorded: %q ok=%v", eventType, ok)
	}

	// Resolution is still ordered after the in-flight send.
	resolveCtx, cancelResolve := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancelResolve()
	if _, err := dbClient.ExecContext(resolveCtx, `UPDATE alerts SET status = 'resolved', resolved_at = NOW() WHERE id = $1`, alertID); err == nil {
		t.Fatal("resolution committed while a worker send held the alert")
	}
}
