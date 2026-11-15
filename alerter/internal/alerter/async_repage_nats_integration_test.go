//go:build !unit

package alerter

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// With asynchronous dispatch, a page the delivery worker dropped and handed
// back (the alert was acknowledged while it waited on a retry) is claimed and
// published again after the unacknowledge. That second publish lands inside
// JetStream's duplicate window, so it must carry a message id of its own, or
// the stream would discard it while the claim commits as sent: the route
// would count as paged and never be.
func TestAsyncRepageAfterHandBackIsQueued(t *testing.T) {
	ctx := context.Background()
	srv, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats server not ready")
	}
	t.Cleanup(srv.Shutdown)
	nc, err := queue.NewClient(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	stream, err := nc.EnsureWorkQueueStream(ctx, "NOTIFICATIONS_REPAGE", []string{"alerts.dispatch.>"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "repage")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	channelID := insertTypedChannel(ctx, t, db, tenantID, ackStubType)
	alertID := openDownAlert(ctx, t, db, tenantID, monitorID)

	a := newIntegrationAlerter(db)
	a.nats = nc
	a.config.AsyncDispatch = true
	a.sendFunc = a.sendChannelNotification
	page := func() {
		t.Helper()
		if err := a.dispatchOpenAlerts(ctx); err != nil {
			t.Fatal(err)
		}
	}

	page()
	if _, _, ok := notificationStateFor(ctx, t, db, alertID, channelID); !ok {
		t.Fatal("the first page was not claimed")
	}
	// The worker held it for an acknowledgement and handed the claim back
	// (releaseMutedClaim); the alert was then unacknowledged.
	mustExec(ctx, t, db, `DELETE FROM alert_notification_states WHERE alert_id = $1 AND channel_id = $2`, alertID, channelID)
	page()
	if _, _, ok := notificationStateFor(ctx, t, db, alertID, channelID); !ok {
		t.Fatal("the re-page was not claimed")
	}

	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Msgs != 2 {
		t.Fatalf("queued dispatch messages = %d, want the page and the re-page (2)", info.State.Msgs)
	}
	consumer, err := nc.CreateConsumer(ctx, "NOTIFICATIONS_REPAGE", "repage-test")
	if err != nil {
		t.Fatal(err)
	}
	batch, err := consumer.Fetch(2, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	for msg := range batch.Messages() {
		var envelope notifications.DispatchEnvelope
		if err := json.Unmarshal(msg.Data(), &envelope); err != nil {
			t.Fatal(err)
		}
		events = append(events, envelope.EventType)
	}
	if len(events) != 2 || events[0] != "created" || events[1] != "created" {
		t.Fatalf("queued events = %v, want [created created]", events)
	}
}
