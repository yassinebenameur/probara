//go:build !unit

package alerter

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/alertrouting"
	"github.com/yassinebenameur/probara/shared/anomaly"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// relapseSend is one sendFunc call: the channel, the plugin-facing event
// type, the paging episode (relapse count) it carried, and whether it was
// claimed as the relapse notice.
type relapseSend struct {
	channel uuid.UUID
	event   string
	relapse int
	notice  bool
	// paged / previous: the episode the channel's provider incident belongs
	// to, and on a relapse notice the one of its page before.
	paged    int
	previous *int
}

// relapseRecorder records every send, and can fail them all.
type relapseRecorder struct {
	mu    sync.Mutex
	sends []relapseSend
	fail  atomic.Bool
}

func (r *relapseRecorder) send(_ context.Context, ch alertChannel, eventType string, _ alertSubject, alert *alertRecord, _ *groupDetail, _ time.Time) error {
	if r.fail.Load() {
		return errors.New("channel unreachable")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sends = append(r.sends, relapseSend{channel: ch.ID, event: eventType, relapse: alert.RelapseCount, notice: alert.RelapseNotice,
		paged: alert.PagedRelapse, previous: alert.PreviousPagedRelapse})
	return nil
}

// notices counts the relapse notices sent to channel.
func (r *relapseRecorder) notices(channel uuid.UUID) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.sends {
		if s.channel == channel && s.notice {
			n++
		}
	}
	return n
}

// count counts the sends of event to channel in episode relapse (-1: any).
func (r *relapseRecorder) count(channel uuid.UUID, event string, relapse int) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.sends {
		if s.channel == channel && s.event == event && (relapse < 0 || s.relapse == relapse) {
			n++
		}
	}
	return n
}

func setReopenCooldown(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID uuid.UUID, minutes int) {
	t.Helper()
	mustExec(ctx, t, db, `UPDATE tenants SET alert_reopen_cooldown_minutes = $2 WHERE id = $1`, tenantID, minutes)
}

// goDown marks the monitor down and runs the opener.
func goDown(ctx context.Context, t *testing.T, db *shareddb.Client, a *Alerter, monitorID uuid.UUID) {
	t.Helper()
	setStateForTest(ctx, t, db, monitorID, "down", time.Now().UTC())
	if err := a.openAlertsForDownMonitors(ctx); err != nil {
		t.Fatalf("openAlertsForDownMonitors: %v", err)
	}
}

// goUp marks the monitor up and runs the resolver (which sends the resolve
// notices to the routes that were paged).
func goUp(ctx context.Context, t *testing.T, db *shareddb.Client, a *Alerter, monitorID uuid.UUID) {
	t.Helper()
	setStateForTest(ctx, t, db, monitorID, "up", time.Now().UTC())
	if err := a.resolveAlertsForRecoveredMonitors(ctx); err != nil {
		t.Fatalf("resolveAlertsForRecoveredMonitors: %v", err)
	}
}

type alertRow struct {
	id           uuid.UUID
	status       string
	relapseCount int
	triggeredAt  time.Time
	reopenedAt   sql.NullTime
}

// alertRowsFor returns the monitor's alerts of kind, oldest first.
func alertRowsFor(ctx context.Context, t *testing.T, db *shareddb.Client, monitorID uuid.UUID, kind string) []alertRow {
	t.Helper()
	rows, err := db.QueryContext(ctx, `
		SELECT id, status, relapse_count, triggered_at, reopened_at FROM alerts
		WHERE monitor_id = $1 AND kind = $2 ORDER BY created_at, id`, monitorID, kind)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []alertRow
	for rows.Next() {
		var r alertRow
		if err := rows.Scan(&r.id, &r.status, &r.relapseCount, &r.triggeredAt, &r.reopenedAt); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// backdateResolve moves the alert's resolve `ago` into the past.
func backdateResolve(ctx context.Context, t *testing.T, db *shareddb.Client, alertID uuid.UUID, ago time.Duration) {
	t.Helper()
	mustExec(ctx, t, db, `UPDATE alerts SET resolved_at = NOW() - make_interval(secs => $2) WHERE id = $1`, alertID, ago.Seconds())
}

// With the cooldown unset (the default), a monitor that fails again right
// after recovering opens a new alert, exactly as before the feature.
func TestRelapseOffOpensNewAlert(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "relapse-off")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	a := newIntegrationAlerter(db)

	goDown(ctx, t, db, a, monitorID)
	goUp(ctx, t, db, a, monitorID)
	goDown(ctx, t, db, a, monitorID)

	rows := alertRowsFor(ctx, t, db, monitorID, "availability")
	if len(rows) != 2 || rows[0].status != "resolved" || rows[1].status != "active" || rows[1].relapseCount != 0 {
		t.Fatalf("alerts = %+v, want the first resolved and a second, new one", rows)
	}
}

// Within the cooldown the same alert reopens: back to active with its
// relapse count, its first trigger kept, its earlier acknowledgement and
// snooze gone but its first acknowledgement kept for SLA reports, the relapse
// recorded in its history and on its auto incident. The route that heard the
// recovery is told once that it relapsed, and the relapse's own recovery
// reaches it again.
func TestRelapseReopensWithinCooldown(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "relapse-on")
	setReopenCooldown(ctx, t, db, tenantID, 10)
	mustExec(ctx, t, db, `UPDATE tenants SET auto_create_incident = TRUE WHERE id = $1`, tenantID)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	route := insertRoute(ctx, t, db, tenantID, "ops", 0)
	rec := &relapseRecorder{}
	a := newIntegrationAlerter(db)
	a.sendFunc = rec.send

	goDown(ctx, t, db, a, monitorID)
	dispatch(ctx, t, a)
	first := alertRowsFor(ctx, t, db, monitorID, "availability")[0]
	// Acknowledged and snoozed in the first episode.
	mustExec(ctx, t, db, `UPDATE alerts SET status = 'acknowledged', acknowledged_at = NOW(), first_acknowledged_at = NOW(),
		first_ack_episode_started_at = triggered_at, snoozed_until = NOW() + INTERVAL '1 hour', snoozed_at = NOW() WHERE id = $1`, first.id)
	goUp(ctx, t, db, a, monitorID)
	if rec.count(route, "created", 0) != 1 || rec.count(route, "resolved", 0) != 1 {
		t.Fatalf("first episode sends = %+v", rec.sends)
	}

	goDown(ctx, t, db, a, monitorID)
	rows := alertRowsFor(ctx, t, db, monitorID, "availability")
	if len(rows) != 1 {
		t.Fatalf("alerts = %+v, want the one alert reopened", rows)
	}
	got := rows[0]
	if got.id != first.id || got.status != "active" || got.relapseCount != 1 || !got.reopenedAt.Valid ||
		!got.triggeredAt.Equal(first.triggeredAt) {
		t.Fatalf("reopened alert = %+v (first %+v)", got, first)
	}
	var acked, snoozed, firstAcked, autoResolved, resolved bool
	if err := db.QueryRowContext(ctx, `SELECT acknowledged_at IS NOT NULL, snoozed_until IS NOT NULL,
			first_acknowledged_at IS NOT NULL AND first_ack_episode_started_at IS NOT NULL, auto_resolved, resolved_at IS NOT NULL
		FROM alerts WHERE id = $1`, got.id).Scan(&acked, &snoozed, &firstAcked, &autoResolved, &resolved); err != nil {
		t.Fatal(err)
	}
	if acked || snoozed || !firstAcked || autoResolved || resolved {
		t.Fatalf("after reopen: acked=%v snoozed=%v firstAckKept=%v autoResolved=%v resolved=%v", acked, snoozed, firstAcked, autoResolved, resolved)
	}
	// recovered_at is the alerter's resolve time and reopened_at the
	// database's: the history row carries both, whatever their clocks say.
	var relapses int
	var matches bool
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*), BOOL_AND(r.reopened_at = a.reopened_at AND r.recovered_at IS NOT NULL)
		FROM alert_relapses r JOIN alerts a ON a.id = r.alert_id WHERE r.alert_id = $1`,
		got.id).Scan(&relapses, &matches); err != nil || relapses != 1 || !matches {
		t.Fatalf("relapse history: %d rows, consistent=%v, err=%v", relapses, matches, err)
	}
	var incidents, relapseEntries int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT ia.incident_id),
			(SELECT COUNT(*) FROM incident_timeline_entries e JOIN incident_alerts x ON x.incident_id = e.incident_id
			 WHERE x.alert_id = $1 AND e.message = $2)
		FROM incident_alerts ia WHERE ia.alert_id = $1`, got.id, alertRelapsedMessage).Scan(&incidents, &relapseEntries); err != nil {
		t.Fatal(err)
	}
	if incidents != 1 || relapseEntries != 1 {
		t.Fatalf("incidents=%d relapse entries=%d, want the one auto incident with one relapse entry", incidents, relapseEntries)
	}

	// Told once that it relapsed, as a page in episode 1.
	dispatch(ctx, t, a)
	dispatch(ctx, t, a)
	if n := rec.count(route, "created", 1); n != 1 {
		t.Fatalf("relapse notices = %d, want 1 (sends %+v)", n, rec.sends)
	}
	if lastEvent, _, _ := notificationStateFor(ctx, t, db, got.id, route); lastEvent != alertrouting.EventRelapsed {
		t.Fatalf("route state = %q, want relapsed", lastEvent)
	}

	goUp(ctx, t, db, a, monitorID)
	if n := rec.count(route, "resolved", 1); n != 1 {
		t.Fatalf("relapse resolves = %d, want 1 (sends %+v)", n, rec.sends)
	}
	var auto bool
	if err := db.QueryRowContext(ctx, `SELECT auto_resolved FROM alerts WHERE id = $1`, got.id).Scan(&auto); err != nil || !auto {
		t.Fatalf("auto_resolved after the relapse recovered = %v (%v)", auto, err)
	}
}

// Past the cooldown, a new alert opens.
func TestRelapseOutsideCooldownOpensNewAlert(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "relapse-late")
	setReopenCooldown(ctx, t, db, tenantID, 10)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	a := newIntegrationAlerter(db)

	goDown(ctx, t, db, a, monitorID)
	goUp(ctx, t, db, a, monitorID)
	backdateResolve(ctx, t, db, alertRowsFor(ctx, t, db, monitorID, "availability")[0].id, 11*time.Minute)
	goDown(ctx, t, db, a, monitorID)

	rows := alertRowsFor(ctx, t, db, monitorID, "availability")
	if len(rows) != 2 || rows[0].status != "resolved" || rows[0].relapseCount != 0 || rows[1].relapseCount != 0 {
		t.Fatalf("alerts = %+v, want a new alert", rows)
	}
}

// A manual resolve is a person's decision that the alert is over: the
// subject failing again opens a new alert, however soon. So does failing
// again after a newer alert of the subject was resolved by hand.
func TestRelapseNeverReopensManualResolve(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "relapse-manual")
	setReopenCooldown(ctx, t, db, tenantID, 10)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	a := newIntegrationAlerter(db)

	goDown(ctx, t, db, a, monitorID)
	// What the API's resolve does: it never sets auto_resolved.
	mustExec(ctx, t, db, `UPDATE alerts SET status = 'resolved', resolved_at = NOW() WHERE monitor_id = $1`, monitorID)
	goDown(ctx, t, db, a, monitorID)
	rows := alertRowsFor(ctx, t, db, monitorID, "availability")
	if len(rows) != 2 || rows[1].relapseCount != 0 || rows[0].status != "resolved" {
		t.Fatalf("alerts = %+v, want a new alert after a manual resolve", rows)
	}

	// Even when the older alert is an auto-resolved one inside the
	// cooldown, a newer alert resolved by hand is the subject's latest, and
	// the older one never comes back past it.
	mustExec(ctx, t, db, `UPDATE alerts SET auto_resolved = TRUE WHERE id = $1`, rows[0].id)
	mustExec(ctx, t, db, `UPDATE alerts SET status = 'resolved', resolved_at = NOW() WHERE id = $1`, rows[1].id)
	goDown(ctx, t, db, a, monitorID)
	rows = alertRowsFor(ctx, t, db, monitorID, "availability")
	if len(rows) != 3 || rows[0].status != "resolved" || rows[1].status != "resolved" || rows[2].relapseCount != 0 {
		t.Fatalf("alerts = %+v, want a third, new alert", rows)
	}
}

// After a reopen, escalation tiers and reminders count from the reopen, as
// after an unacknowledge: a tier that never fired waits its full delay from
// the reopen, and a route that heard the relapse notice gets its next
// reminder one interval after it.
func TestRelapseClocksCountFromTheReopen(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "relapse-clocks")
	setReopenCooldown(ctx, t, db, tenantID, 30)
	mustExec(ctx, t, db, `UPDATE tenants SET alert_reminder_seconds = 300 WHERE id = $1`, tenantID)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	first := insertRoute(ctx, t, db, tenantID, "first", 0)
	tier := insertRoute(ctx, t, db, tenantID, "tier", 10*time.Minute)
	rec := &relapseRecorder{}
	a := newIntegrationAlerter(db)
	a.sendFunc = rec.send

	goDown(ctx, t, db, a, monitorID)
	dispatch(ctx, t, a)
	goUp(ctx, t, db, a, monitorID)
	alertID := alertRowsFor(ctx, t, db, monitorID, "availability")[0].id
	// The first episode was long (an hour) and recovered a minute ago.
	mustExec(ctx, t, db, `UPDATE alerts SET triggered_at = NOW() - INTERVAL '1 hour' WHERE id = $1`, alertID)

	goDown(ctx, t, db, a, monitorID)
	dispatch(ctx, t, a)
	if rec.count(first, "created", 1) != 1 || rec.notices(first) != 1 {
		t.Fatalf("relapse notice to the first route: %+v", rec.sends)
	}
	if n := rec.count(tier, "created", -1); n != 0 {
		t.Fatalf("tier paged %d times right after the reopen, want its delay counted from the reopen", n)
	}

	// Six minutes after the reopen: the reminder interval (5 min) has run
	// since the relapse notice, the tier's 10 minutes have not.
	mustExec(ctx, t, db, `UPDATE alerts SET reopened_at = NOW() - INTERVAL '6 minutes' WHERE id = $1`, alertID)
	mustExec(ctx, t, db, `UPDATE alert_notification_states SET last_sent_at = NOW() - INTERVAL '6 minutes' WHERE alert_id = $1`, alertID)
	dispatch(ctx, t, a)
	if rec.count(first, "reminder", 1) != 1 || rec.count(tier, "created", -1) != 0 {
		t.Fatalf("six minutes in: %+v", rec.sends)
	}

	mustExec(ctx, t, db, `UPDATE alerts SET reopened_at = NOW() - INTERVAL '11 minutes' WHERE id = $1`, alertID)
	dispatch(ctx, t, a)
	// The tier never heard the alert: its first page in the relapse is an
	// ordinary page, not a relapse notice.
	if rec.count(tier, "created", 1) != 1 || rec.notices(tier) != 0 {
		t.Fatalf("tier after its delay from the reopen: %+v", rec.sends)
	}
	if lastEvent, _, _ := notificationStateFor(ctx, t, db, alertID, tier); lastEvent != "created" {
		t.Fatalf("tier state = %q, want created", lastEvent)
	}
}

// A relapse that happens while the earlier recovery's resolve is still
// pending (its send failed, the sweep retries it) drops that resolve: the
// route is never told "recovered" after the alert relapsed. It is still
// re-paged: its last page is from the episode that recovered, and for a
// paging provider whose incident is still open (and maybe acknowledged) only
// a page reaches anyone, never a reminder. The relapse's own recovery then
// reaches it.
func TestRelapseDropsPendingResolve(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "relapse-pending")
	setReopenCooldown(ctx, t, db, tenantID, 10)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	route := insertRoute(ctx, t, db, tenantID, "ops", 0)
	rec := &relapseRecorder{}
	a := newIntegrationAlerter(db)
	a.sendFunc = rec.send

	goDown(ctx, t, db, a, monitorID)
	dispatch(ctx, t, a)
	alertID := alertRowsFor(ctx, t, db, monitorID, "availability")[0].id
	// Acknowledged in the first episode: a paging provider holds the
	// incident acknowledged, so only a new page reaches anyone.
	acknowledge(ctx, t, db, alertID)
	rec.fail.Store(true)
	goUp(ctx, t, db, a, monitorID)
	if lastEvent, _, _ := notificationStateFor(ctx, t, db, alertID, route); lastEvent != "created" {
		t.Fatalf("failed resolve left the route at %q, want created (pending)", lastEvent)
	}

	goDown(ctx, t, db, a, monitorID)
	rec.fail.Store(false)
	if err := a.dispatchPendingRecoveries(ctx); err != nil {
		t.Fatal(err)
	}
	dispatch(ctx, t, a)
	if n := rec.count(route, "resolved", -1); n != 0 {
		t.Fatalf("the earlier recovery reached the route after the relapse: %+v", rec.sends)
	}
	if n := rec.notices(route); n != 1 {
		t.Fatalf("relapse notices to a route that missed the recovery = %d, want 1: %+v", n, rec.sends)
	}
	for _, sent := range rec.sends {
		if sent.notice && (sent.paged != 1 || sent.previous == nil || *sent.previous != 0) {
			t.Fatalf("relapse notice keyed paged=%d previous=%v, want a new incident (1) closing the first (0)", sent.paged, sent.previous)
		}
	}
	if n := rec.count(route, "reminder", -1); n != 0 {
		t.Fatalf("reminders instead of a page: %+v", rec.sends)
	}

	goUp(ctx, t, db, a, monitorID)
	if n := rec.count(route, "resolved", 1); n != 1 {
		t.Fatalf("relapse resolves = %d, want 1 (sends %+v)", n, rec.sends)
	}
}

// Two replicas evaluating the same relapse reopen it once and tell each
// route once.
func TestRelapseReplicasReopenAndNotifyOnce(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "relapse-replicas")
	setReopenCooldown(ctx, t, db, tenantID, 10)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	routes := []uuid.UUID{insertRoute(ctx, t, db, tenantID, "a", 0), insertRoute(ctx, t, db, tenantID, "b", 0)}
	counter := newSendCounter(100 * time.Millisecond)
	replicas := newReplicaPair(db, counter)

	setStateForTest(ctx, t, db, monitorID, "down", time.Now().UTC())
	runConcurrently(t, replicas, func(a *Alerter) error { return a.openAlertsForDownMonitors(ctx) })
	runConcurrently(t, replicas, func(a *Alerter) error { return a.dispatchOpenAlerts(ctx) })
	setStateForTest(ctx, t, db, monitorID, "up", time.Now().UTC())
	runConcurrently(t, replicas, func(a *Alerter) error { return a.resolveAlertsForRecoveredMonitors(ctx) })

	for round := 0; round < 3; round++ {
		setStateForTest(ctx, t, db, monitorID, "down", time.Now().UTC())
		runConcurrently(t, replicas, func(a *Alerter) error { return a.openAlertsForDownMonitors(ctx) })
	}
	rows := alertRowsFor(ctx, t, db, monitorID, "availability")
	if len(rows) != 1 || rows[0].relapseCount != 1 || rows[0].status != "active" {
		t.Fatalf("alerts = %+v, want one alert reopened once", rows)
	}
	var history int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_relapses WHERE alert_id = $1`, rows[0].id).Scan(&history); err != nil || history != 1 {
		t.Fatalf("relapse history rows = %d (%v)", history, err)
	}
	for i := 0; i < 2; i++ {
		runConcurrently(t, replicas, func(a *Alerter) error { return a.dispatchOpenAlerts(ctx) })
	}
	// Two created pages in episode 0, two relapse notices (sent as created).
	if got := counter.count("created"); got != 2*len(routes) {
		t.Fatalf("created sends = %d, want %d (one page and one relapse notice per route)", got, 2*len(routes))
	}
	for _, route := range routes {
		if lastEvent, _, _ := notificationStateFor(ctx, t, db, rows[0].id, route); lastEvent != alertrouting.EventRelapsed {
			t.Fatalf("route %s state = %q", route, lastEvent)
		}
	}
}

// The reopen matches the alert's whole identity: a host metric relapses per
// series, a mesh alert per edge, and latency and certificate alerts per
// monitor and kind. A different series or edge opens its own alert.
func TestRelapseEachIdentityBearingKind(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "relapse-kinds")
	setReopenCooldown(ctx, t, db, tenantID, 10)
	a := newIntegrationAlerter(db)

	countKind := func(where string, args ...any) (total, relapsed int) {
		t.Helper()
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*), COUNT(*) FILTER (WHERE relapse_count > 0) FROM alerts WHERE `+where,
			args...).Scan(&total, &relapsed); err != nil {
			t.Fatal(err)
		}
		return total, relapsed
	}
	resolveAll := func(where string, args ...any) {
		t.Helper()
		rows, err := db.QueryContext(ctx, `SELECT id FROM alerts WHERE status <> 'resolved' AND `+where, args...)
		if err != nil {
			t.Fatal(err)
		}
		var ids []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		rows.Close()
		for _, id := range ids {
			if _, err := a.resolveAlert(ctx, id, resolvedRecovered); err != nil {
				t.Fatal(err)
			}
		}
	}

	t.Run("host_metric per series", func(t *testing.T) {
		monitorID := testutil.InsertAgentMonitor(ctx, t, db, tenantID, "host", "agent-1", 60)
		c := hostMetricConfig{monitorID: monitorID, tenantID: tenantID, monitorName: "host"}
		cpu := breachingSeries{seriesKey: "system.cpu.utilization", value: 0.95, threshold: 0.9}
		disk := breachingSeries{seriesKey: "system.filesystem.utilization{mountpoint=/}", value: 0.97, threshold: 0.9}
		for _, b := range []breachingSeries{cpu, disk} {
			if err := a.openHostMetricAlert(ctx, c, b); err != nil {
				t.Fatal(err)
			}
		}
		resolveAll(`monitor_id = $1 AND metric_name = $2`, monitorID, cpu.seriesKey)
		cpu.value = 0.99
		if err := a.openHostMetricAlert(ctx, c, cpu); err != nil {
			t.Fatal(err)
		}
		if total, relapsed := countKind(`monitor_id = $1 AND kind = 'host_metric'`, monitorID); total != 2 || relapsed != 1 {
			t.Fatalf("host_metric alerts = %d (relapsed %d), want cpu reopened beside disk", total, relapsed)
		}
		var value float64
		if err := db.QueryRowContext(ctx, `SELECT metric_value FROM alerts WHERE monitor_id = $1 AND metric_name = $2`,
			monitorID, cpu.seriesKey).Scan(&value); err != nil || value != 0.99 {
			t.Fatalf("reopened metric_value = %v (%v), want the new reading", value, err)
		}
		// A series that never had an alert opens a new one.
		mem := breachingSeries{seriesKey: "system.memory.utilization", value: 0.95, threshold: 0.9}
		if err := a.openHostMetricAlert(ctx, c, mem); err != nil {
			t.Fatal(err)
		}
		if total, relapsed := countKind(`monitor_id = $1 AND kind = 'host_metric'`, monitorID); total != 3 || relapsed != 1 {
			t.Fatalf("host_metric alerts = %d (relapsed %d)", total, relapsed)
		}
	})

	t.Run("mesh_edge per edge", func(t *testing.T) {
		src := insertMeshLocation(ctx, t, db, tenantID, "eu", "eu")
		dst := insertMeshLocation(ctx, t, db, tenantID, "us", "us")
		other := insertMeshLocation(ctx, t, db, tenantID, "ap", "ap")
		edge := meshEdgeSubject{tenantID: tenantID, sourceID: src, targetID: dst, sourceName: "eu", targetName: "us", failCount: 3}
		reverse := meshEdgeSubject{tenantID: tenantID, sourceID: dst, targetID: src, sourceName: "us", targetName: "eu", failCount: 3}
		third := meshEdgeSubject{tenantID: tenantID, sourceID: src, targetID: other, sourceName: "eu", targetName: "ap", failCount: 3}
		if err := a.openMeshEdgeAlert(ctx, edge); err != nil {
			t.Fatal(err)
		}
		resolveAll(`kind = 'mesh_edge' AND source_location_id = $1 AND target_location_id = $2`, src, dst)
		for _, e := range []meshEdgeSubject{reverse, third, edge} {
			if err := a.openMeshEdgeAlert(ctx, e); err != nil {
				t.Fatal(err)
			}
		}
		if total, relapsed := countKind(`kind = 'mesh_edge' AND tenant_id = $1`, tenantID); total != 3 || relapsed != 1 {
			t.Fatalf("mesh alerts = %d (relapsed %d), want the edge reopened and the other two new", total, relapsed)
		}
		var relapsedEdge bool
		if err := db.QueryRowContext(ctx, `SELECT relapse_count = 1 FROM alerts
			WHERE kind = 'mesh_edge' AND source_location_id = $1 AND target_location_id = $2`, src, dst).Scan(&relapsedEdge); err != nil || !relapsedEdge {
			t.Fatalf("the down edge did not reopen: %v %v", relapsedEdge, err)
		}
	})

	t.Run("latency_anomaly and tls_expiry per monitor", func(t *testing.T) {
		monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "web")
		lc := anomalyConfig{monitorID: monitorID, tenantID: tenantID, monitorName: "web"}
		tc := tlsExpiryConfig{monitorID: monitorID, tenantID: tenantID, monitorName: "web", minDays: 14}
		verdict := anomaly.Verdict{Anomalous: true, Score: 5, BaselineMs: 100, ObservedMs: 900}
		if err := a.openLatencyAnomalyAlert(ctx, lc, verdict); err != nil {
			t.Fatal(err)
		}
		if err := a.openTLSExpiryAlert(ctx, tc, certExpiry{days: 10}); err != nil {
			t.Fatal(err)
		}
		resolveAll(`monitor_id = $1`, monitorID)
		if err := a.openLatencyAnomalyAlert(ctx, lc, verdict); err != nil {
			t.Fatal(err)
		}
		if err := a.openTLSExpiryAlert(ctx, tc, certExpiry{days: 9}); err != nil {
			t.Fatal(err)
		}
		for _, kind := range []string{notifications.KindLatencyAnomaly, notifications.KindTLSExpiry} {
			rows := alertRowsFor(ctx, t, db, monitorID, kind)
			if len(rows) != 1 || rows[0].relapseCount != 1 || rows[0].status != "active" {
				t.Fatalf("%s alerts = %+v, want one reopened", kind, rows)
			}
		}
		// An availability alert on the same monitor is its own identity.
		goDown(ctx, t, db, a, monitorID)
		if rows := alertRowsFor(ctx, t, db, monitorID, "availability"); len(rows) != 1 || rows[0].relapseCount != 0 {
			t.Fatalf("availability alerts = %+v", rows)
		}
	})
}

// The relapse notice's pre-filter (openAlertEventDue over the columns the
// dispatch query selects) and its claim (deliverNotification's locked
// re-read and the relapsed claim) must decide every case the same, like
// TestMutePreFilterMatchesClaim for the mute.
func TestRelapsePreFilterMatchesClaim(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "relapse-pair")
	ch := insertTestChannel(ctx, t, db, tenantID, "chat")
	const interval = 10 * time.Minute

	cases := []struct {
		name        string
		status      string
		snoozed     bool
		reopenedAgo time.Duration
		state       string        // the route's row; "" for none
		sentAgo     time.Duration // its last_sent_at
		paged       int           // its paged_relapse (the alert is in episode 1)
		want        string
		unackedAgo  time.Duration // >0: unacknowledged that long ago (after the reopen)
	}{
		{"heard the recovery", "active", false, time.Minute, "resolved", 2 * time.Minute, 0, alertrouting.EventRelapsed, 0},
		{"heard the recovery, acknowledged since", "acknowledged", false, time.Minute, "resolved", 2 * time.Minute, 0, "", 0},
		{"heard the recovery, snoozed since", "active", true, time.Minute, "resolved", 2 * time.Minute, 0, "", 0},
		{"never paged", "active", false, time.Minute, "", 0, 0, "created", 0},
		{"told of the relapse", "active", false, time.Minute, "relapsed", 30 * time.Second, 1, "", 0},
		{"told of the relapse long ago", "active", false, 20 * time.Minute, "relapsed", 11 * time.Minute, 1, "reminder", 0},
		{"first paged in this episode", "active", false, 20 * time.Minute, "created", 11 * time.Minute, 1, "reminder", 0},
		{"missed the recovery, reopened just now", "active", false, time.Minute, "created", 30 * time.Minute, 0, alertrouting.EventRelapsed, 0},
		{"missed the recovery after a reminder", "active", false, 11 * time.Minute, "reminder", 30 * time.Minute, 0, alertrouting.EventRelapsed, 0},
		{"missed the recovery, acknowledged since", "acknowledged", false, 11 * time.Minute, "created", 30 * time.Minute, 0, "", 0},
		// Clocks after the reopen: an unacknowledge after the relapse notice
		// restarts the reminder interval from itself.
		{"told of the relapse, unacknowledged just now", "active", false, 20 * time.Minute, "relapsed", 11 * time.Minute, 1, "", time.Minute},
		{"told of the relapse, unacknowledged long ago", "active", false, 30 * time.Minute, "relapsed", 25 * time.Minute, 1, "reminder", 12 * time.Minute},
	}
	a := newIntegrationAlerter(db)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, tc.name)
			alertID := uuid.New()
			mustExec(ctx, t, db, `
				INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, acknowledged_at, snoozed_until,
					relapse_count, reopened_at, failure_count, created_at, updated_at)
				VALUES ($1, $2, $3, 'availability', $4::alert_status, NOW() - INTERVAL '1 hour',
					CASE WHEN $4::alert_status = 'acknowledged' THEN NOW() END,
					CASE WHEN $5 THEN NOW() + INTERVAL '1 hour' END,
					1, NOW() - make_interval(secs => $6), 1, NOW(), NOW())
			`, alertID, tenantID, monitorID, tc.status, tc.snoozed, tc.reopenedAgo.Seconds())
			if tc.unackedAgo > 0 {
				mustExec(ctx, t, db, `UPDATE alerts SET unacknowledged_at = NOW() - make_interval(secs => $2) WHERE id = $1`, alertID, tc.unackedAgo.Seconds())
			}
			mustExec(ctx, t, db, `INSERT INTO alert_relapses (alert_id, relapse_number, recovered_at, reopened_at)
				SELECT id, 1, reopened_at - INTERVAL '3 minutes', reopened_at FROM alerts WHERE id = $1`, alertID)
			if tc.state != "" {
				mustExec(ctx, t, db, `
					INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type, paged_relapse, created_at, updated_at)
					VALUES ($1, $2, NOW() - make_interval(secs => $3), $4, $5, NOW(), NOW())
				`, alertID, ch, tc.sentAgo.Seconds(), tc.state, tc.paged)
			}

			var gate dispatchGate
			var resumedAt sql.NullTime
			var eligibleSince time.Time
			if err := db.QueryRowContext(ctx, `
				SELECT `+alertrouting.MutedPredicate("al")+`, `+alertrouting.DispatchResumedAtExpr("al")+`,
					`+alertrouting.DispatchEligibleSinceExpr("al", "te")+`, al.relapse_count
				FROM alerts al JOIN tenants te ON te.id = al.tenant_id WHERE al.id = $1
			`, alertID).Scan(&gate.Muted, &resumedAt, &eligibleSince, &gate.RelapseCount); err != nil {
				t.Fatal(err)
			}
			gate.ResumedAt = resumedAt.Time
			states, err := a.loadNotificationStates(ctx, []uuid.UUID{alertID})
			if err != nil {
				t.Fatal(err)
			}
			// The database's clock, which wrote every timestamp above: a
			// container clock behind the host's must not turn a fresh send
			// into an old one.
			var now time.Time
			if err := db.QueryRowContext(ctx, `SELECT NOW()`).Scan(&now); err != nil {
				t.Fatal(err)
			}
			state := getNotificationState(states, alertID, ch)
			pre := openAlertEventDue(state, now.Sub(eligibleSince), 0, now, interval, gate)
			if pre != tc.want {
				t.Fatalf("pre-filter = %q, want %q", pre, tc.want)
			}

			// Claim: the one event this state can be due for.
			event := "created"
			switch {
			case state == nil:
			case state.PagedRelapse < gate.RelapseCount:
				event = alertrouting.EventRelapsed
				// The pre-filter never sends such a route a reminder, and
				// neither does the claim.
				if sent, err := a.deliverNotification(ctx, "reminder", alertSubject{MonitorID: monitorID, TenantID: tenantID},
					&alertRecord{ID: alertID, TenantID: tenantID, MonitorID: monitorID}, nil,
					alertChannel{ID: ch, Type: "webhook", IsActive: true}, now, interval); err != nil || sent {
					t.Fatalf("reminder claim on a route last paged in an earlier episode: sent=%v err=%v", sent, err)
				}
			default:
				event = "reminder"
			}
			var sentType string
			var sentRelapse, sentPaged int
			var sentNotice bool
			var sentPrevious *int
			a.sendFunc = func(_ context.Context, _ alertChannel, eventType string, _ alertSubject, alert *alertRecord, _ *groupDetail, _ time.Time) error {
				sentType, sentRelapse, sentNotice, sentPaged, sentPrevious = eventType, alert.RelapseCount, alert.RelapseNotice, alert.PagedRelapse, alert.PreviousPagedRelapse
				return nil
			}
			sent, err := a.deliverNotification(ctx, event, alertSubject{MonitorID: monitorID, TenantID: tenantID},
				&alertRecord{ID: alertID, TenantID: tenantID, MonitorID: monitorID}, nil,
				alertChannel{ID: ch, Type: "webhook", IsActive: true}, now, interval)
			if err != nil {
				t.Fatal(err)
			}
			if sent != (tc.want == event) {
				t.Fatalf("claim of %s sent=%v, pre-filter said %q", event, sent, pre)
			}
			if sent && event == alertrouting.EventRelapsed && (sentType != "created" || sentRelapse != 1 || !sentNotice ||
				sentPaged != 1 || sentPrevious == nil || *sentPrevious != tc.paged) {
				t.Fatalf("relapse notice went out as %q (episode %d, notice %v, paged %d, previous %v), want a created notice paging episode 1 after %d",
					sentType, sentRelapse, sentNotice, sentPaged, sentPrevious, tc.paged)
			}
			if sent && event != alertrouting.EventRelapsed && sentNotice {
				t.Fatalf("%s carried the relapse notice flag", event)
			}
		})
	}
}

// Only a recovery makes an alert reopenable (auto_resolved). Every other way
// the alerter closes an alert leaves it closed for good, so the subject
// coming back opens a new alert: a paused, deleted or emptied monitor, a
// removed rule or threshold, stale host readings, a vanished mesh edge.
func TestRelapseOnlyAfterARecovery(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "relapse-scope")
	setReopenCooldown(ctx, t, db, tenantID, 10)
	a := newIntegrationAlerter(db)
	autoResolved := func(alertID uuid.UUID) bool {
		t.Helper()
		var status string
		var auto bool
		if err := db.QueryRowContext(ctx, `SELECT status, auto_resolved FROM alerts WHERE id = $1`, alertID).Scan(&status, &auto); err != nil {
			t.Fatal(err)
		}
		if status != "resolved" {
			t.Fatalf("alert %s is %s, want resolved", alertID, status)
		}
		return auto
	}
	openAlert := func(kind string, monitorID uuid.UUID, metric string) uuid.UUID {
		t.Helper()
		id := uuid.New()
		mustExec(ctx, t, db, `INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, failure_count, metric_name, created_at, updated_at)
			VALUES ($1, $2, $3, $4, 'active', NOW(), 1, NULLIF($5, ''), NOW(), NOW())`, id, tenantID, monitorID, kind, metric)
		return id
	}

	t.Run("pause then resume opens a new alert", func(t *testing.T) {
		monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "paused")
		goDown(ctx, t, db, a, monitorID)
		first := alertRowsFor(ctx, t, db, monitorID, "availability")[0].id
		mustExec(ctx, t, db, `UPDATE monitors SET enabled = FALSE WHERE id = $1`, monitorID)
		if err := a.resolveAlertsForRecoveredMonitors(ctx); err != nil {
			t.Fatal(err)
		}
		if autoResolved(first) {
			t.Fatal("a paused monitor's alert must not be reopenable")
		}
		mustExec(ctx, t, db, `UPDATE monitors SET enabled = TRUE WHERE id = $1`, monitorID)
		goDown(ctx, t, db, a, monitorID)
		if rows := alertRowsFor(ctx, t, db, monitorID, "availability"); len(rows) != 2 || rows[1].relapseCount != 0 {
			t.Fatalf("alerts = %+v, want a new alert after the resume", rows)
		}
	})

	t.Run("availability: up is a recovery, deleted is not", func(t *testing.T) {
		up := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "up")
		goDown(ctx, t, db, a, up)
		goUp(ctx, t, db, a, up)
		if !autoResolved(alertRowsFor(ctx, t, db, up, "availability")[0].id) {
			t.Fatal("a monitor back up must leave its alert reopenable")
		}
		gone := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "gone")
		goDown(ctx, t, db, a, gone)
		mustExec(ctx, t, db, `UPDATE monitors SET deleted_at = NOW() WHERE id = $1`, gone)
		if err := a.resolveAlertsForRecoveredMonitors(ctx); err != nil {
			t.Fatal(err)
		}
		if autoResolved(alertRowsFor(ctx, t, db, gone, "availability")[0].id) {
			t.Fatal("a deleted monitor's alert must not be reopenable")
		}
	})

	t.Run("group emptied or paused is not a recovery", func(t *testing.T) {
		group := testutil.InsertGroupMonitor(ctx, t, db, tenantID, "empty-group")
		id := openAlert("availability", group, "")
		mustExec(ctx, t, db, `UPDATE monitors SET current_state = 'unknown' WHERE id = $1`, group)
		if err := a.resolveAlertsForRecoveredMonitors(ctx); err != nil {
			t.Fatal(err)
		}
		if autoResolved(id) {
			t.Fatal("an emptied group's alert must not be reopenable")
		}
	})

	t.Run("mesh: up is a recovery, a vanished edge is not", func(t *testing.T) {
		src := insertMeshLocation(ctx, t, db, tenantID, "eu", "eu-scope")
		dst := insertMeshLocation(ctx, t, db, tenantID, "us", "us-scope")
		other := insertMeshLocation(ctx, t, db, tenantID, "ap", "ap-scope")
		insertMeshEdgeState(ctx, t, db, tenantID, src, dst, "down", 3, "timeout")
		insertMeshEdgeState(ctx, t, db, tenantID, src, other, "down", 3, "timeout")
		if err := a.openMeshEdgeAlerts(ctx); err != nil {
			t.Fatal(err)
		}
		setMeshEdgeState(ctx, t, db, src, dst, "up")
		mustExec(ctx, t, db, `DELETE FROM location_mesh_state WHERE source_location_id = $1 AND target_location_id = $2`, src, other)
		if err := a.resolveMeshEdgeAlerts(ctx); err != nil {
			t.Fatal(err)
		}
		var recovered, vanished bool
		if err := db.QueryRowContext(ctx, `SELECT
				BOOL_OR(auto_resolved) FILTER (WHERE target_location_id = $2),
				BOOL_OR(auto_resolved) FILTER (WHERE target_location_id = $3)
			FROM alerts WHERE kind = 'mesh_edge' AND source_location_id = $1 AND status = 'resolved'`, src, dst, other).Scan(&recovered, &vanished); err != nil {
			t.Fatal(err)
		}
		if !recovered || vanished {
			t.Fatalf("auto_resolved: recovered edge %v (want true), vanished edge %v (want false)", recovered, vanished)
		}
	})

	t.Run("host metric: back within the rule is a recovery, stale or removed is not", func(t *testing.T) {
		monitorID := testutil.InsertAgentMonitor(ctx, t, db, tenantID, "host", "agent-scope", 60)
		c := hostMetricConfig{monitorID: monitorID, tenantID: tenantID, monitorName: "host"}
		cleared := openAlert("host_metric", monitorID, "system.cpu.utilization")
		stale := openAlert("host_metric", monitorID, "system.memory.utilization")
		if err := a.resolveClearedHostMetricAlerts(ctx, c, map[string]bool{},
			map[string]bool{"system.cpu.utilization": true}, time.Now()); err != nil {
			t.Fatal(err)
		}
		if !autoResolved(cleared) || autoResolved(stale) {
			t.Fatalf("auto_resolved: fresh reading back within the rule %v (want true), stale series %v (want false)",
				autoResolved(cleared), autoResolved(stale))
		}
		orphan := openAlert("host_metric", monitorID, "system.cpu.utilization")
		if err := a.resolveOrphanHostMetricAlerts(ctx, nil); err != nil {
			t.Fatal(err)
		}
		if autoResolved(orphan) {
			t.Fatal("a host metric alert whose rules were removed must not be reopenable")
		}
	})

	t.Run("latency: back to baseline is a recovery, no readings is not", func(t *testing.T) {
		anomalies := newAnomalyAlerter(db)
		enableTenantAnomaly(ctx, t, db, tenantID)
		now := time.Now().UTC()
		evaluate := func() {
			t.Helper()
			if err := anomalies.evaluateLatencyAnomalies(ctx); err != nil {
				t.Fatal(err)
			}
		}
		latest := func(monitorID uuid.UUID) uuid.UUID {
			t.Helper()
			rows := alertRowsFor(ctx, t, db, monitorID, "latency_anomaly")
			if len(rows) == 0 {
				t.Fatal("no latency alert opened")
			}
			return rows[len(rows)-1].id
		}
		slowThen := func(name string, then func(monitorID uuid.UUID)) uuid.UUID {
			t.Helper()
			monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, name)
			seedFlatBaseline(ctx, t, db, tenantID, monitorID, now, 12, 100)
			for _, age := range []time.Duration{10, 30, 60, 90} {
				testutil.InsertCheckResult(ctx, t, db, tenantID, monitorID, now.Add(-age*time.Second), "success", "monitor", testutil.IntPtr(500))
			}
			evaluate()
			mustExec(ctx, t, db, `UPDATE check_results SET created_at = $2, started_at = $2, completed_at = $2 WHERE monitor_id = $1`,
				monitorID, now.Add(-10*time.Minute))
			then(monitorID)
			evaluate()
			return latest(monitorID)
		}
		// Hard down: only failures in the window, so no latency to judge.
		down := slowThen("slow-then-down", func(monitorID uuid.UUID) {
			for _, age := range []time.Duration{10, 30, 60, 90} {
				testutil.InsertCheckResult(ctx, t, db, tenantID, monitorID, now.Add(-age*time.Second), "failure", "monitor", nil)
			}
		})
		if autoResolved(down) {
			t.Fatal("a latency alert closed for lack of readings must not be reopenable")
		}
		fast := slowThen("slow-then-fast", func(monitorID uuid.UUID) {
			for _, age := range []time.Duration{10, 30, 60, 90} {
				testutil.InsertCheckResult(ctx, t, db, tenantID, monitorID, now.Add(-age*time.Second), "success", "monitor", testutil.IntPtr(100))
			}
		})
		if !autoResolved(fast) {
			t.Fatal("fresh readings back at baseline must leave the alert reopenable")
		}
	})

	t.Run("tls and latency: cleared is a recovery, orphaned is not", func(t *testing.T) {
		monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "cert")
		tlsAlert := openAlert("tls_expiry", monitorID, "")
		latency := openAlert("latency_anomaly", monitorID, "")
		a.resolveTLSExpiryAlert(ctx, tlsAlert, tenantID, monitorID, "cert", time.Now(), time.Now(), resolvedRecovered)
		if err := a.resolveLatencyAnomalyAlert(ctx, monitorID, tenantID, "cert", resolvedRecovered); err != nil {
			t.Fatal(err)
		}
		if !autoResolved(tlsAlert) || !autoResolved(latency) {
			t.Fatal("a renewed certificate and a latency back to baseline must leave their alerts reopenable")
		}
		tlsOrphan := openAlert("tls_expiry", monitorID, "")
		latencyOrphan := openAlert("latency_anomaly", monitorID, "")
		if err := a.resolveOrphanTLSExpiryAlerts(ctx, nil); err != nil {
			t.Fatal(err)
		}
		if err := a.resolveOrphanLatencyAlerts(ctx, nil); err != nil {
			t.Fatal(err)
		}
		if autoResolved(tlsOrphan) || autoResolved(latencyOrphan) {
			t.Fatal("alerts closed because their threshold or detection was removed must not be reopenable")
		}
	})
}

// A reopen waits only briefly for the row lock a resolve being delivered
// holds (the alert row FOR SHARE, up to the delivery timeout): past
// reopenLockTimeout it gives up for this tick without opening a new alert,
// and the next tick reopens it.
func TestRelapseReopenGivesUpOnALockedAlert(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "relapse-lock")
	setReopenCooldown(ctx, t, db, tenantID, 10)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	a := newIntegrationAlerter(db)

	goDown(ctx, t, db, a, monitorID)
	goUp(ctx, t, db, a, monitorID)
	alertID := alertRowsFor(ctx, t, db, monitorID, "availability")[0].id

	// A sender delivering the resolve holds the row FOR SHARE.
	holder, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback() }()
	if _, err := holder.ExecContext(ctx, `SELECT 1 FROM alerts WHERE id = $1 FOR SHARE`, alertID); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	goDown(ctx, t, db, a, monitorID)
	if waited := time.Since(started); waited > reopenLockTimeout+5*time.Second {
		t.Fatalf("the opener waited %s on the locked alert", waited)
	}
	rows := alertRowsFor(ctx, t, db, monitorID, "availability")
	if len(rows) != 1 || rows[0].status != "resolved" {
		t.Fatalf("alerts while locked = %+v, want the one alert, still resolved (no new alert)", rows)
	}

	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	goDown(ctx, t, db, a, monitorID)
	rows = alertRowsFor(ctx, t, db, monitorID, "availability")
	if len(rows) != 1 || rows[0].status != "active" || rows[0].relapseCount != 1 {
		t.Fatalf("alerts after the lock = %+v, want the alert reopened", rows)
	}
}

// A dispatch loop takes its now before iterating, so another replica can
// recover and reopen the alert meanwhile. A page or relapse notice claimed
// with that stale now belongs to the new episode, and its time (the event
// timestamp, the acknowledge link's issue time, the claim's last_sent_at)
// must not predate the reopen, or the link it carries is refused as one from
// an earlier episode. A now already past the reopen is kept as is.
func TestClaimWithAStaleNowNeverPredatesTheReopen(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "stale-now")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	heard := insertRoute(ctx, t, db, tenantID, "heard", 0)
	fresh := insertRoute(ctx, t, db, tenantID, "fresh", 0)
	later := insertRoute(ctx, t, db, tenantID, "later", 0)
	alertID := uuid.New()
	mustExec(ctx, t, db, `
		INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, relapse_count, reopened_at,
			failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, 'availability', 'active', NOW() - INTERVAL '1 hour', 1, NOW(), 3, NOW(), NOW())`,
		alertID, tenantID, monitorID)
	mustExec(ctx, t, db, `INSERT INTO alert_relapses (alert_id, relapse_number, recovered_at, reopened_at)
		SELECT id, 1, reopened_at - INTERVAL '2 minutes', reopened_at FROM alerts WHERE id = $1`, alertID)
	// Paged in episode 0 and heard its recovery: due the relapse notice.
	mustExec(ctx, t, db, `INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type, paged_relapse)
		VALUES ($1, $2, NOW() - INTERVAL '3 minutes', 'resolved', 0)`, alertID, heard)
	var reopenedAt time.Time
	if err := db.QueryRowContext(ctx, `SELECT reopened_at FROM alerts WHERE id = $1`, alertID).Scan(&reopenedAt); err != nil {
		t.Fatal(err)
	}

	var stamps []time.Time
	a := newIntegrationAlerter(db)
	a.sendFunc = func(_ context.Context, _ alertChannel, _ string, _ alertSubject, _ *alertRecord, _ *groupDetail, now time.Time) error {
		stamps = append(stamps, now)
		return nil
	}
	record := alertRecord{ID: alertID, TenantID: tenantID, MonitorID: monitorID}
	binding := alertSubject{MonitorID: monitorID, TenantID: tenantID}
	stale := reopenedAt.Add(-time.Minute)
	afterReopen := reopenedAt.Add(time.Second)
	for _, tc := range []struct {
		event   string
		channel uuid.UUID
		now     time.Time
		want    time.Time
	}{
		{alertrouting.EventRelapsed, heard, stale, reopenedAt},
		{"created", fresh, stale, reopenedAt},
		{"created", later, afterReopen, afterReopen},
	} {
		stamps = nil
		sent, err := a.deliverNotification(ctx, tc.event, binding, &record, nil,
			alertChannel{ID: tc.channel, Type: "webhook", IsActive: true}, tc.now, 0)
		if err != nil || !sent {
			t.Fatalf("%s claim: sent=%v err=%v", tc.event, sent, err)
		}
		if len(stamps) != 1 || !stamps[0].Equal(tc.want) {
			t.Fatalf("%s sent with time %v, want %v", tc.event, stamps, tc.want)
		}
		if _, sentAt, ok := notificationStateFor(ctx, t, db, alertID, tc.channel); !ok || !sentAt.Equal(tc.want) {
			t.Fatalf("%s claim recorded at %v, want %v (the event's time)", tc.event, sentAt, tc.want)
		}
	}
}
