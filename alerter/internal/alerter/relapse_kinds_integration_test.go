//go:build !unit

package alerter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// An offline location that reports again and goes offline again within the
// cooldown reopens its alert, and the channel that heard the recovery gets
// one relapse notice (a new provider incident, closing the earlier one). A
// location disabled meanwhile is not a recovery: re-enabled and offline
// again, it opens a new alert.
func TestRelapseLocationOffline(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "relapse-location")
	setReopenCooldown(ctx, t, db, tenantID, 10)
	route := insertDefaultChannel(ctx, t, db, tenantID, "ops", 0)
	loc := insertLocationSeen(ctx, t, db, tenantID, "eu", dur(10*time.Minute))
	rec := &relapseRecorder{}
	a := newIntegrationAlerter(db)
	a.sendFunc = rec.send
	evaluate := func() {
		t.Helper()
		if err := a.evaluateLocationOffline(ctx); err != nil {
			t.Fatal(err)
		}
	}
	type row struct {
		id      uuid.UUID
		status  string
		relapse int
		auto    bool
	}
	alerts := func() []row {
		t.Helper()
		rows, err := db.QueryContext(ctx, `SELECT id, status, relapse_count, auto_resolved FROM alerts
			WHERE kind = 'location_offline' AND source_location_id = $1 ORDER BY created_at, id`, loc)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.status, &r.relapse, &r.auto); err != nil {
				t.Fatal(err)
			}
			out = append(out, r)
		}
		return out
	}

	evaluate()
	setSeenAgo(ctx, t, db, loc, 10*time.Second)
	evaluate()
	if got := alerts(); len(got) != 1 || got[0].status != "resolved" || !got[0].auto {
		t.Fatalf("after it reported again: %+v, want one alert resolved as a recovery", got)
	}
	setSeenAgo(ctx, t, db, loc, 10*time.Minute)
	evaluate()
	got := alerts()
	if len(got) != 1 || got[0].status != "active" || got[0].relapse != 1 {
		t.Fatalf("after it went offline again: %+v, want the alert reopened", got)
	}
	if rec.count(route, "created", 0) != 1 || rec.count(route, "resolved", 0) != 1 || rec.notices(route) != 1 {
		t.Fatalf("sends = %+v, want a page, its recovery and one relapse notice", rec.sends)
	}
	for _, s := range rec.sends {
		if s.notice && (s.paged != 1 || s.previous == nil || *s.previous != 0) {
			t.Fatalf("relapse notice paged=%d previous=%v, want 1 after 0", s.paged, s.previous)
		}
	}

	// The delivery history names the relapse notice as such.
	var notices int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_notification_deliveries
		WHERE alert_id = $1 AND channel_id = $2 AND event_type = 'relapsed' AND outcome = 'delivered'`, got[0].id, route).Scan(&notices); err != nil || notices != 1 {
		t.Fatalf("relapse notices in the delivery history = %d (%v), want 1", notices, err)
	}

	// Disabled: closed, but not reopenable.
	mustExec(ctx, t, db, `UPDATE locations SET enabled = FALSE WHERE id = $1`, loc)
	evaluate()
	if got := alerts(); got[0].status != "resolved" || got[0].auto {
		t.Fatalf("after disabling: %+v, want resolved and not reopenable", got)
	}
	mustExec(ctx, t, db, `UPDATE locations SET enabled = TRUE, enabled_at = NOW() - INTERVAL '30 days' WHERE id = $1`, loc)
	evaluate()
	if got := alerts(); len(got) != 2 || got[1].status != "active" || got[1].relapse != 0 {
		t.Fatalf("after re-enabling while offline: %+v, want a new alert", got)
	}

	// Disabled and re-enabled within a tick while still silent: the
	// re-enable grace stops it counting as offline, which closes the alert,
	// but no heartbeat came, so that is no recovery and the alert opened
	// once the grace ends is a new one.
	mustExec(ctx, t, db, `UPDATE locations SET enabled_at = NOW() WHERE id = $1`, loc)
	evaluate()
	if got := alerts(); got[1].status != "resolved" || got[1].auto {
		t.Fatalf("after a re-enable without a heartbeat: %+v, want resolved and not reopenable", got)
	}
	mustExec(ctx, t, db, `UPDATE locations SET enabled_at = NOW() - INTERVAL '30 days' WHERE id = $1`, loc)
	evaluate()
	if got := alerts(); len(got) != 3 || got[2].status != "active" || got[2].relapse != 0 {
		t.Fatalf("after the grace ended: %+v, want a new alert", got)
	}
}

// A renewed domain relapsing inside its window reopens its alert. A change
// of form ("expiry unknown" vs a dated warning) closes the alert as not a
// recovery and opens a new one: neither form ever reopens as the other.
func TestRelapseDomainExpiry(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "relapse-domain")
	setReopenCooldown(ctx, t, db, tenantID, 10)
	monitorID := insertDomainMonitor(ctx, t, db, tenantID, `{"domain":"example.com","min_days_valid":30}`)
	m := domainExpiryMonitor{monitorID: monitorID, tenantID: tenantID, monitorName: "example.com registration", minDays: 30}
	a := newIntegrationAlerter(db)
	days := func(n float64) *float64 { return &n }
	open := func(value *float64) {
		t.Helper()
		if err := a.openDomainExpiryAlert(ctx, m, value, "domain: test"); err != nil {
			t.Fatal(err)
		}
	}
	type row struct {
		status  string
		relapse int
		auto    bool
		unknown bool
	}
	alerts := func() []row {
		t.Helper()
		rows, err := db.QueryContext(ctx, `SELECT status, relapse_count, auto_resolved, metric_value IS NULL FROM alerts
			WHERE monitor_id = $1 AND kind = 'domain_expiry' ORDER BY created_at, id`, monitorID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.status, &r.relapse, &r.auto, &r.unknown); err != nil {
				t.Fatal(err)
			}
			out = append(out, r)
		}
		return out
	}

	open(days(10))
	if err := a.resolveDomainExpiryAlert(ctx, monitorID, tenantID, m.monitorName, time.Now(), resolvedRecovered); err != nil {
		t.Fatal(err)
	}
	open(days(9))
	if got := alerts(); len(got) != 1 || got[0].status != "active" || got[0].relapse != 1 {
		t.Fatalf("after renewal then a new warning: %+v, want the alert reopened", got)
	}

	// Dated -> unknown: the dated alert closes (not a recovery) and an
	// "expiry unknown" alert opens.
	open(nil)
	got := alerts()
	if len(got) != 2 || got[0].status != "resolved" || got[0].auto || !got[1].unknown || got[1].relapse != 0 {
		t.Fatalf("after the switch to unknown: %+v", got)
	}
	// Unknown -> dated within the cooldown: a new alert, not the dated one
	// reopened, and the unknown one is not reopenable either.
	open(days(8))
	got = alerts()
	if len(got) != 3 || got[1].status != "resolved" || got[1].auto || got[2].unknown || got[2].relapse != 0 || got[0].relapse != 1 {
		t.Fatalf("after the switch back to dated: %+v", got)
	}
}

// Asynchronous dispatch message ids carry the episode: the resolve of the
// first episode and the resolve of the relapse are two messages, and a
// relapse notice whose claim rolled back after it was published republishes
// the same id, so JetStream keeps one message for it.
func TestAsyncRelapseMessageIDs(t *testing.T) {
	ctx := context.Background()
	h := newAsyncDispatchHarness(ctx, t)
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "relapse-async")
	setReopenCooldown(ctx, t, db, tenantID, 10)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	insertTypedChannel(ctx, t, db, tenantID, ackStubType)

	a := newIntegrationAlerter(db)
	a.nats = h.nc
	a.config.AsyncDispatch = true
	failAfterPublish := false
	a.sendFunc = func(ctx context.Context, ch alertChannel, ev string, b alertSubject, al *alertRecord, g *groupDetail, now time.Time) error {
		if err := a.sendChannelNotification(ctx, ch, ev, b, al, g, now); err != nil {
			return err
		}
		if failAfterPublish {
			return errors.New("commit lost after publish")
		}
		return nil
	}

	goDown(ctx, t, db, a, monitorID)
	dispatch(ctx, t, a)
	goUp(ctx, t, db, a, monitorID)
	goDown(ctx, t, db, a, monitorID) // relapse 1
	failAfterPublish = true
	dispatch(ctx, t, a) // relapse notice published, claim rolled back
	failAfterPublish = false
	dispatch(ctx, t, a) // the retry claims again and commits
	dispatch(ctx, t, a) // nothing more is due
	goUp(ctx, t, db, a, monitorID)

	events, ids := h.queued(ctx, t)
	want := []string{"created", "resolved", "created", "resolved"}
	if len(events) != len(want) {
		t.Fatalf("queued = %v %v, want %v", events, ids, want)
	}
	seen := map[string]bool{}
	for i, ev := range events {
		if ev != want[i] {
			t.Fatalf("queued = %v, want %v", events, want)
		}
		if seen[ids[i]] {
			t.Fatalf("message id %q reused: %v", ids[i], ids)
		}
		seen[ids[i]] = true
	}
	if ids[1] == ids[3] {
		t.Fatalf("both episodes' resolves share message id %q", ids[1])
	}
}

// The resolve of a relapse targets the incident open at the provider: the
// episode of the route's last delivered page. A relapse notice the provider
// rejected for good keeps its claim but opened nothing, so the relapse's
// resolve still closes the first episode's incident, never a key that was
// never opened.
func TestRelapseResolveTargetsTheDeliveredIncident(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "relapse-delivered")
	setReopenCooldown(ctx, t, db, tenantID, 10)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	route := insertRoute(ctx, t, db, tenantID, "pager", 0)
	rec := &relapseRecorder{}
	a := newIntegrationAlerter(db)
	a.sendFunc = func(ctx context.Context, ch alertChannel, ev string, b alertSubject, al *alertRecord, g *groupDetail, now time.Time) error {
		if al.RelapseNotice {
			return plugin.Permanent(errors.New("routing key revoked"))
		}
		return rec.send(ctx, ch, ev, b, al, g, now)
	}

	goDown(ctx, t, db, a, monitorID)
	dispatch(ctx, t, a)
	goUp(ctx, t, db, a, monitorID)
	goDown(ctx, t, db, a, monitorID)
	dispatch(ctx, t, a) // the relapse notice fails permanently: claim kept
	alertID := alertRowsFor(ctx, t, db, monitorID, "availability")[0].id
	var paged, delivered int
	if err := db.QueryRowContext(ctx, `SELECT paged_relapse, delivered_relapse FROM alert_notification_states
		WHERE alert_id = $1 AND channel_id = $2`, alertID, route).Scan(&paged, &delivered); err != nil {
		t.Fatal(err)
	}
	if paged != 1 || delivered != 0 {
		t.Fatalf("after a rejected relapse notice: paged %d delivered %d, want 1 and 0", paged, delivered)
	}
	goUp(ctx, t, db, a, monitorID)
	var resolves []relapseSend
	for _, s := range rec.sends {
		if s.event == "resolved" && s.relapse == 1 {
			resolves = append(resolves, s)
		}
	}
	if len(resolves) != 1 || resolves[0].paged != 0 {
		t.Fatalf("relapse resolves = %+v, want one for the episode-0 incident", resolves)
	}
}

// An acknowledgement that comes before the relapse notice is claimed holds
// it: nothing is claimed, and the route that heard the earlier recovery
// stays 'resolved' in episode 0, so the relapse's resolve skips it. (The
// other order, the claim first and the worker handing it back, restores the
// same row: TestHandle_HeldRelapseNoticeRestoresTheRow.)
func TestRelapseNoticeHeldBeforeItsClaim(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "relapse-held")
	setReopenCooldown(ctx, t, db, tenantID, 10)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	route := insertRoute(ctx, t, db, tenantID, "chat", 0)
	rec := &relapseRecorder{}
	a := newIntegrationAlerter(db)
	a.sendFunc = rec.send

	goDown(ctx, t, db, a, monitorID)
	dispatch(ctx, t, a)
	goUp(ctx, t, db, a, monitorID)
	alertID := alertRowsFor(ctx, t, db, monitorID, "availability")[0].id
	_, heardAt, _ := notificationStateFor(ctx, t, db, alertID, route)
	goDown(ctx, t, db, a, monitorID)
	acknowledge(ctx, t, db, alertID)
	dispatch(ctx, t, a)
	lastEvent, lastSent, _ := notificationStateFor(ctx, t, db, alertID, route)
	var paged int
	if err := db.QueryRowContext(ctx, `SELECT paged_relapse FROM alert_notification_states WHERE alert_id = $1 AND channel_id = $2`,
		alertID, route).Scan(&paged); err != nil {
		t.Fatal(err)
	}
	if lastEvent != "resolved" || !lastSent.Equal(heardAt) || paged != 0 {
		t.Fatalf("row = (%s, %s, %d), want (resolved, %s, 0): untouched", lastEvent, lastSent, paged, heardAt)
	}
	goUp(ctx, t, db, a, monitorID)
	if n := rec.count(route, "resolved", -1); n != 1 {
		t.Fatalf("recovery notices = %d, want only the first (the relapse was never announced here)", n)
	}
}
