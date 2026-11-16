//go:build !unit

package alerter

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// asyncDispatchHarness is an alerter publishing dispatch envelopes to an
// in-process JetStream, as with ALERTER_ASYNC_DISPATCH=true.
type asyncDispatchHarness struct {
	nc     *queue.Client
	stream jetstream.Stream
}

func newAsyncDispatchHarness(ctx context.Context, t *testing.T) asyncDispatchHarness {
	t.Helper()
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
	// The production stream keeps the server's default 2-minute duplicate
	// window; every publish below lands inside it.
	stream, err := nc.EnsureWorkQueueStream(ctx, "NOTIFICATIONS_DEDUP", []string{"alerts.dispatch.>"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return asyncDispatchHarness{nc: nc, stream: stream}
}

// queued returns the event type and message id of every queued envelope.
func (h asyncDispatchHarness) queued(ctx context.Context, t *testing.T) (events, ids []string) {
	t.Helper()
	info, err := h.stream.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for seq := info.State.FirstSeq; info.State.Msgs > 0 && seq <= info.State.LastSeq; seq++ {
		msg, err := h.stream.GetMsg(ctx, seq)
		if err != nil {
			t.Fatal(err)
		}
		var envelope notifications.DispatchEnvelope
		if err := json.Unmarshal(msg.Data, &envelope); err != nil {
			t.Fatal(err)
		}
		events = append(events, envelope.EventType)
		ids = append(ids, msg.Header.Get(queue.MsgIDHeader))
	}
	return events, ids
}

// A claim that published but never committed (here: the send reports a
// transient failure after the publish, as a lost PubAck or a failed commit
// would) is retried with the same message id, so JetStream keeps exactly one
// message for the slot instead of two deliveries of one page or reminder.
func TestAsyncRetriedClaimRepublishesTheSameMessage(t *testing.T) {
	ctx := context.Background()
	h := newAsyncDispatchHarness(ctx, t)
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "dedup")
	mustExec(ctx, t, db, `UPDATE tenants SET alert_reminder_seconds = 300 WHERE id = $1`, tenantID)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	channelID := insertTypedChannel(ctx, t, db, tenantID, ackStubType)
	alertID := openDownAlert(ctx, t, db, tenantID, monitorID)

	a := newIntegrationAlerter(db)
	a.nats = h.nc
	a.config.AsyncDispatch = true
	failAfterPublish := true
	a.sendFunc = func(ctx context.Context, ch alertChannel, ev string, b alertSubject, al *alertRecord, g *groupDetail, now time.Time) error {
		if err := a.sendChannelNotification(ctx, ch, ev, b, al, g, now); err != nil {
			return err
		}
		if failAfterPublish {
			return errors.New("commit lost after publish")
		}
		return nil
	}
	dispatch := func() {
		t.Helper()
		if err := a.dispatchOpenAlerts(ctx); err != nil {
			t.Fatal(err)
		}
	}

	dispatch() // published, claim rolled back
	if _, _, ok := notificationStateFor(ctx, t, db, alertID, channelID); ok {
		t.Fatal("the failed claim must roll back")
	}
	failAfterPublish = false
	dispatch() // the retry claims again and commits
	if _, _, ok := notificationStateFor(ctx, t, db, alertID, channelID); !ok {
		t.Fatal("the retried claim did not commit")
	}
	if events, ids := h.queued(ctx, t); len(events) != 1 || events[0] != "created" {
		t.Fatalf("queued after a retried page = %v %v, want one created", events, ids)
	}

	// The same for a reminder round.
	mustExec(ctx, t, db, `UPDATE alert_notification_states SET last_sent_at = NOW() - INTERVAL '10 minutes' WHERE alert_id = $1`, alertID)
	failAfterPublish = true
	dispatch()
	failAfterPublish = false
	dispatch()
	events, ids := h.queued(ctx, t)
	if len(events) != 2 || events[1] != "reminder" {
		t.Fatalf("queued after a retried reminder = %v %v, want created then one reminder", events, ids)
	}
	if ids[0] == ids[1] {
		t.Fatalf("page and reminder share a message id %q", ids[0])
	}
}

// A page the worker handed back (an acknowledgement held it before it
// reached anyone) is re-paged after the unacknowledge under a new message id,
// so JetStream queues it even inside the duplicate window: the claim never
// commits for a page that was never sent.
func TestAsyncRepageAfterHandBackInsideTheDedupWindow(t *testing.T) {
	ctx := context.Background()
	h := newAsyncDispatchHarness(ctx, t)
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "repage")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	channelID := insertTypedChannel(ctx, t, db, tenantID, ackStubType)
	alertID := openDownAlert(ctx, t, db, tenantID, monitorID)

	a := newIntegrationAlerter(db)
	a.nats = h.nc
	a.config.AsyncDispatch = true
	a.sendFunc = a.sendChannelNotification
	dispatch := func() {
		t.Helper()
		if err := a.dispatchOpenAlerts(ctx); err != nil {
			t.Fatal(err)
		}
	}

	dispatch()
	// Acknowledged before the page was delivered; the worker drops it and
	// hands its claim back (releaseMutedClaim). Seconds later: unacknowledged.
	acknowledge(ctx, t, db, alertID)
	mustExec(ctx, t, db, `DELETE FROM alert_notification_states WHERE alert_id = $1 AND channel_id = $2`, alertID, channelID)
	dispatch()
	unacknowledgeAt(ctx, t, db, alertID, 0)
	dispatch()
	if _, _, ok := notificationStateFor(ctx, t, db, alertID, channelID); !ok {
		t.Fatal("the re-page was not claimed")
	}
	events, ids := h.queued(ctx, t)
	if len(events) != 2 || events[0] != "created" || events[1] != "created" {
		t.Fatalf("queued = %v %v, want the page and the re-page", events, ids)
	}
	if ids[0] == ids[1] {
		t.Fatalf("the re-page reused the page's message id %q", ids[0])
	}
}
