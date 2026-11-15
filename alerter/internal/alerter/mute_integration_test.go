//go:build !unit

package alerter

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/alertrouting"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// muteRecorder counts sends per (channel, event type).
type muteRecorder struct {
	mu   sync.Mutex
	sent map[uuid.UUID]map[string]int
}

func newMuteRecorder() *muteRecorder { return &muteRecorder{sent: map[uuid.UUID]map[string]int{}} }

func (r *muteRecorder) send(_ context.Context, ch alertChannel, eventType string, _ alertSubject, _ *alertRecord, _ *groupDetail, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sent[ch.ID] == nil {
		r.sent[ch.ID] = map[string]int{}
	}
	r.sent[ch.ID][eventType]++
	return nil
}

func (r *muteRecorder) count(channelID uuid.UUID, eventType string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sent[channelID][eventType]
}

// insertRoute adds a tenant default route with an escalation delay.
func insertRoute(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID uuid.UUID, name string, delay time.Duration) uuid.UUID {
	t.Helper()
	id := insertTestChannel(ctx, t, db, tenantID, name)
	mustExec(ctx, t, db, `
		INSERT INTO tenant_default_channels (tenant_id, channel_id, delay_seconds, position)
		VALUES ($1, $2, $3, (SELECT COUNT(*) FROM tenant_default_channels WHERE tenant_id = $1))
	`, tenantID, id, int(delay.Seconds()))
	return id
}

// ageAlert moves the alert's trigger and every page it sent `ago` back.
func ageAlert(ctx context.Context, t *testing.T, db *shareddb.Client, alertID uuid.UUID, ago time.Duration) {
	t.Helper()
	mustExec(ctx, t, db, `UPDATE alerts SET triggered_at = NOW() - make_interval(secs => $2) WHERE id = $1`, alertID, ago.Seconds())
	mustExec(ctx, t, db, `UPDATE alert_notification_states SET last_sent_at = NOW() - make_interval(secs => $2) WHERE alert_id = $1`,
		alertID, ago.Seconds())
}

func acknowledge(ctx context.Context, t *testing.T, db *shareddb.Client, alertID uuid.UUID) {
	t.Helper()
	mustExec(ctx, t, db, `UPDATE alerts SET status = 'acknowledged', acknowledged_at = NOW() WHERE id = $1`, alertID)
}

// unacknowledgeAt does what the API's unacknowledge does, `ago` in the past.
func unacknowledgeAt(ctx context.Context, t *testing.T, db *shareddb.Client, alertID uuid.UUID, ago time.Duration) {
	t.Helper()
	mustExec(ctx, t, db, `UPDATE alerts SET status = 'active', acknowledged_at = NULL,
		unacknowledged_at = NOW() - make_interval(secs => $2) WHERE id = $1`, alertID, ago.Seconds())
}

// snoozeUntil sets the snooze end relative to now: positive is a running
// snooze, negative one that ended that long ago, zero an unsnooze right now.
func snoozeUntil(ctx context.Context, t *testing.T, db *shareddb.Client, alertID uuid.UUID, in time.Duration) {
	t.Helper()
	mustExec(ctx, t, db, `UPDATE alerts SET snoozed_until = NOW() + make_interval(secs => $2) WHERE id = $1`, alertID, in.Seconds())
}

func dispatch(ctx context.Context, t *testing.T, a *Alerter) {
	t.Helper()
	if err := a.dispatchOpenAlerts(ctx); err != nil {
		t.Fatalf("dispatchOpenAlerts: %v", err)
	}
}

// After an acknowledgement the only notification is the resolve: no
// reminder on the route already paged, no first page on the escalation tier
// whose delay had not run out, through the dispatch loop and through the
// claim alone (a dispatcher that read the alert just before the ack).
func TestAckStopsEverythingButTheResolve(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "ack-stops")
	mustExec(ctx, t, db, `UPDATE tenants SET alert_reminder_seconds = 300 WHERE id = $1`, tenantID)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	first := insertRoute(ctx, t, db, tenantID, "first", 0)
	tier := insertRoute(ctx, t, db, tenantID, "tier", 10*time.Minute)
	alertID := openDownAlert(ctx, t, db, tenantID, monitorID)

	rec := newMuteRecorder()
	a := newIntegrationAlerter(db)
	a.sendFunc = rec.send
	dispatch(ctx, t, a)
	if rec.count(first, "created") != 1 || rec.count(tier, "created") != 0 {
		t.Fatalf("opening pages: first=%d tier=%d", rec.count(first, "created"), rec.count(tier, "created"))
	}

	acknowledge(ctx, t, db, alertID)
	ageAlert(ctx, t, db, alertID, 30*time.Minute)
	dispatch(ctx, t, a)
	if got := rec.count(first, "reminder"); got != 0 {
		t.Fatalf("reminders after the ack = %d, want 0", got)
	}
	if got := rec.count(tier, "created"); got != 0 {
		t.Fatalf("escalation tier paged after the ack = %d, want 0", got)
	}

	record := alertRecord{ID: alertID, TenantID: tenantID, MonitorID: monitorID}
	binding := alertSubject{MonitorID: monitorID, TenantID: tenantID}
	for _, tc := range []struct {
		event   string
		channel uuid.UUID
	}{{"reminder", first}, {"created", tier}} {
		sent, err := a.deliverNotification(ctx, tc.event, binding, &record, nil,
			alertChannel{ID: tc.channel, Type: "webhook", IsActive: true}, time.Now(), 5*time.Minute)
		if err != nil || sent {
			t.Fatalf("claim of %s on an acknowledged alert: sent=%v err=%v", tc.event, sent, err)
		}
	}
	if _, _, ok := notificationStateFor(ctx, t, db, alertID, tier); ok {
		t.Fatal("a muted page must leave no claim behind")
	}

	setStateForTest(ctx, t, db, monitorID, "up", time.Now().UTC())
	if err := a.resolveAlertsForRecoveredMonitors(ctx); err != nil {
		t.Fatal(err)
	}
	if got := rec.count(first, "resolved"); got != 1 {
		t.Fatalf("resolve notices to the paged route = %d, want 1", got)
	}
	if got := rec.count(tier, "resolved"); got != 0 {
		t.Fatalf("resolve notices to the never-paged tier = %d, want 0", got)
	}
}

// The mute is kind-agnostic: latency, host-metric and certificate alerts stop
// reminding once acknowledged, and remind again once active.
func TestAckMutesEveryAlertKind(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "ack-kinds")
	mustExec(ctx, t, db, `UPDATE tenants SET alert_reminder_seconds = 300 WHERE id = $1`, tenantID)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	ch := insertRoute(ctx, t, db, tenantID, "chat", 0)
	for _, kind := range []string{"latency_anomaly", "host_metric", "tls_expiry"} {
		id := uuid.New()
		mustExec(ctx, t, db, `
			INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, acknowledged_at,
				failure_count, metric_name, metric_value, created_at, updated_at)
			VALUES ($1, $2, $3, $4::text, 'acknowledged', NOW() - INTERVAL '30 minutes', NOW(), 1,
				CASE WHEN $4::text = 'host_metric' THEN 'system.cpu.utilization' END, 1, NOW(), NOW())
		`, id, tenantID, monitorID, kind)
		mustExec(ctx, t, db, `
			INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type, created_at, updated_at)
			VALUES ($1, $2, NOW() - INTERVAL '30 minutes', 'created', NOW(), NOW())
		`, id, ch)
	}

	rec := newMuteRecorder()
	a := newIntegrationAlerter(db)
	a.sendFunc = rec.send
	dispatch(ctx, t, a)
	if got := rec.count(ch, "reminder"); got != 0 {
		t.Fatalf("reminders on acknowledged alerts = %d, want 0", got)
	}
	mustExec(ctx, t, db, `UPDATE alerts SET status = 'active', acknowledged_at = NULL WHERE tenant_id = $1`, tenantID)
	dispatch(ctx, t, a)
	if got := rec.count(ch, "reminder"); got != 3 {
		t.Fatalf("reminders once active = %d, want one per alert kind (3)", got)
	}
}

// An unacknowledge resumes reminders and unfired tiers with their clocks
// counted from the unacknowledge: no burst of "missed" reminders, no
// immediate tier, and never the page again for the route that had it.
func TestUnackResumesFromTheUnack(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "unack")
	mustExec(ctx, t, db, `UPDATE tenants SET alert_reminder_seconds = 300 WHERE id = $1`, tenantID)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	first := insertRoute(ctx, t, db, tenantID, "first", 0)
	tier := insertRoute(ctx, t, db, tenantID, "tier", 10*time.Minute)
	alertID := openDownAlert(ctx, t, db, tenantID, monitorID)

	rec := newMuteRecorder()
	a := newIntegrationAlerter(db)
	a.sendFunc = rec.send
	dispatch(ctx, t, a)
	acknowledge(ctx, t, db, alertID)
	ageAlert(ctx, t, db, alertID, 30*time.Minute)
	dispatch(ctx, t, a)

	unacknowledgeAt(ctx, t, db, alertID, time.Minute)
	dispatch(ctx, t, a)
	if r, c := rec.count(first, "reminder"), rec.count(tier, "created"); r != 0 || c != 0 {
		t.Fatalf("a minute after the unack: reminders=%d tier pages=%d, want none", r, c)
	}
	if got := rec.count(first, "created"); got != 1 {
		t.Fatalf("pages to the route already paged = %d, want the original 1", got)
	}

	unacknowledgeAt(ctx, t, db, alertID, 6*time.Minute)
	dispatch(ctx, t, a)
	if r, c := rec.count(first, "reminder"), rec.count(tier, "created"); r != 1 || c != 0 {
		t.Fatalf("six minutes after the unack: reminders=%d tier pages=%d, want 1 and 0", r, c)
	}

	unacknowledgeAt(ctx, t, db, alertID, 11*time.Minute)
	dispatch(ctx, t, a)
	if r, c := rec.count(first, "reminder"), rec.count(tier, "created"); r != 1 || c != 1 {
		t.Fatalf("eleven minutes after the unack: reminders=%d tier pages=%d, want 1 and 1", r, c)
	}
}

// A snooze mutes reminders and unfired tiers without acknowledging; when it
// runs out, or is cancelled early, both resume counted from its end. The
// resolve is sent while snoozed.
func TestSnoozeMutesThenResumes(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "snooze")
	mustExec(ctx, t, db, `UPDATE tenants SET alert_reminder_seconds = 300 WHERE id = $1`, tenantID)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	first := insertRoute(ctx, t, db, tenantID, "first", 0)
	tier := insertRoute(ctx, t, db, tenantID, "tier", 10*time.Minute)
	alertID := openDownAlert(ctx, t, db, tenantID, monitorID)

	rec := newMuteRecorder()
	a := newIntegrationAlerter(db)
	a.sendFunc = rec.send
	dispatch(ctx, t, a)

	snoozeUntil(ctx, t, db, alertID, time.Hour)
	ageAlert(ctx, t, db, alertID, 30*time.Minute)
	dispatch(ctx, t, a)
	if r, c := rec.count(first, "reminder"), rec.count(tier, "created"); r != 0 || c != 0 {
		t.Fatalf("while snoozed: reminders=%d tier pages=%d, want none", r, c)
	}

	// Ran out a minute ago: clocks count from its end.
	snoozeUntil(ctx, t, db, alertID, -time.Minute)
	dispatch(ctx, t, a)
	if r, c := rec.count(first, "reminder"), rec.count(tier, "created"); r != 0 || c != 0 {
		t.Fatalf("a minute after the snooze ended: reminders=%d tier pages=%d, want none", r, c)
	}
	snoozeUntil(ctx, t, db, alertID, -6*time.Minute)
	dispatch(ctx, t, a)
	if r, c := rec.count(first, "reminder"), rec.count(tier, "created"); r != 1 || c != 0 {
		t.Fatalf("six minutes after the snooze ended: reminders=%d tier pages=%d, want 1 and 0", r, c)
	}

	// Cancelled early (the API sets the end to now): nothing fires at once.
	ageAlert(ctx, t, db, alertID, 30*time.Minute)
	snoozeUntil(ctx, t, db, alertID, time.Hour)
	snoozeUntil(ctx, t, db, alertID, 0)
	dispatch(ctx, t, a)
	if r, c := rec.count(first, "reminder"), rec.count(tier, "created"); r != 1 || c != 0 {
		t.Fatalf("right after an unsnooze: reminders=%d tier pages=%d, want the earlier 1 and 0", r, c)
	}

	snoozeUntil(ctx, t, db, alertID, time.Hour)
	setStateForTest(ctx, t, db, monitorID, "up", time.Now().UTC())
	if err := a.resolveAlertsForRecoveredMonitors(ctx); err != nil {
		t.Fatal(err)
	}
	if got := rec.count(first, "resolved"); got != 1 {
		t.Fatalf("resolve notices while snoozed = %d, want 1", got)
	}
}

// Mesh-edge alerts run their own dispatch loop; the mute and the resumed
// clocks apply there too, and the resolve still goes out.
func TestMeshAlertAckAndSnooze(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "mesh-mute")
	mustExec(ctx, t, db, `UPDATE tenants SET alert_reminder_seconds = 300 WHERE id = $1`, tenantID)
	sourceID := insertMeshLocation(ctx, t, db, tenantID, "Paris", "paris")
	targetID := insertMeshLocation(ctx, t, db, tenantID, "Frankfurt", "frankfurt")
	insertMeshEdgeState(ctx, t, db, tenantID, sourceID, targetID, "down", 5, "timeout")
	ch := insertRoute(ctx, t, db, tenantID, "mesh", 0)
	tier := insertRoute(ctx, t, db, tenantID, "mesh-tier", 10*time.Minute)

	rec := newMuteRecorder()
	a := newIntegrationAlerter(db)
	a.sendFunc = rec.send
	evaluate := func() {
		t.Helper()
		if err := a.evaluateMeshEdges(ctx); err != nil {
			t.Fatal(err)
		}
	}
	evaluate()
	if got := rec.count(ch, "created"); got != 1 {
		t.Fatalf("mesh page = %d, want 1", got)
	}
	var alertID uuid.UUID
	if err := db.QueryRowContext(ctx, `SELECT id FROM alerts WHERE kind = 'mesh_edge' AND tenant_id = $1`, tenantID).Scan(&alertID); err != nil {
		t.Fatal(err)
	}

	acknowledge(ctx, t, db, alertID)
	ageAlert(ctx, t, db, alertID, 30*time.Minute)
	evaluate()
	if r, c := rec.count(ch, "reminder"), rec.count(tier, "created"); r != 0 || c != 0 {
		t.Fatalf("acknowledged mesh alert: reminders=%d tier pages=%d, want none", r, c)
	}

	unacknowledgeAt(ctx, t, db, alertID, time.Minute)
	evaluate()
	if r, c := rec.count(ch, "reminder"), rec.count(tier, "created"); r != 0 || c != 0 {
		t.Fatalf("a minute after the unack: reminders=%d tier pages=%d, want none", r, c)
	}
	unacknowledgeAt(ctx, t, db, alertID, 11*time.Minute)
	evaluate()
	if r, c := rec.count(ch, "reminder"), rec.count(tier, "created"); r != 1 || c != 1 {
		t.Fatalf("eleven minutes after the unack: reminders=%d tier pages=%d, want 1 and 1", r, c)
	}

	ageAlert(ctx, t, db, alertID, 30*time.Minute)
	snoozeUntil(ctx, t, db, alertID, time.Hour)
	evaluate()
	if got := rec.count(ch, "reminder"); got != 1 {
		t.Fatalf("snoozed mesh alert reminded: %d, want the earlier 1", got)
	}

	setMeshEdgeState(ctx, t, db, sourceID, targetID, "up")
	evaluate()
	if r1, r2 := rec.count(ch, "resolved"), rec.count(tier, "resolved"); r1 != 1 || r2 != 1 {
		t.Fatalf("mesh resolve notices while snoozed = %d, %d, want 1 each", r1, r2)
	}
}

// Two replicas dispatching right after an unacknowledge send each due
// event once: one reminder to the paged route, one page to the tier that
// came due, and nothing on the next round.
func TestUnackReplicasSendOnce(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "unack-replicas")
	mustExec(ctx, t, db, `UPDATE tenants SET alert_reminder_seconds = 300 WHERE id = $1`, tenantID)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	insertRoute(ctx, t, db, tenantID, "first", 0)
	insertRoute(ctx, t, db, tenantID, "tier", 10*time.Minute)
	alertID := openDownAlert(ctx, t, db, tenantID, monitorID)

	counter := newSendCounter(200 * time.Millisecond)
	replicas := newReplicaPair(db, counter)
	if err := replicas[0].dispatchOpenAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	acknowledge(ctx, t, db, alertID)
	ageAlert(ctx, t, db, alertID, 30*time.Minute)
	runConcurrently(t, replicas, func(a *Alerter) error { return a.dispatchOpenAlerts(ctx) })
	if counter.count("reminder") != 0 || counter.count("created") != 1 {
		t.Fatalf("while acknowledged: reminders=%d pages=%d", counter.count("reminder"), counter.count("created"))
	}

	unacknowledgeAt(ctx, t, db, alertID, 11*time.Minute)
	for round := 0; round < 2; round++ {
		runConcurrently(t, replicas, func(a *Alerter) error { return a.dispatchOpenAlerts(ctx) })
	}
	if got := counter.count("reminder"); got != 1 {
		t.Fatalf("reminders after the unack = %d, want exactly 1", got)
	}
	if got := counter.count("created"); got != 2 {
		t.Fatalf("pages after the unack = %d, want the original plus the tier (2)", got)
	}
}

// The in-memory pre-filter (the mute and resume point the dispatch query
// selects, then openAlertEventDue) and the claim (deliverNotification's
// locked re-read and claimNotificationTx) must decide every case the same.
// Delays are left at zero: the escalation delay is the pre-filter's rule
// alone (the created claim does not re-check it).
func TestMutePreFilterMatchesClaim(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "mute-pair")
	ch := insertTestChannel(ctx, t, db, tenantID, "chat")
	const interval = 10 * time.Minute
	const none = time.Duration(0)

	cases := []struct {
		name       string
		status     string
		snoozeIn   time.Duration // >0 running, <0 ended that long ago, 0 never snoozed
		unackedAgo time.Duration // 0: never unacknowledged
		sentAgo    time.Duration // 0: channel never paged
		want       string
	}{
		{"fresh", "active", none, none, none, "created"},
		{"acknowledged, never paged", "acknowledged", none, none, none, ""},
		{"snoozed, never paged", "active", time.Hour, none, none, ""},
		{"snooze over, never paged", "active", -time.Minute, none, none, "created"},
		{"reminder due", "active", none, none, 30 * time.Minute, "reminder"},
		{"reminder not due", "active", none, none, 5 * time.Minute, ""},
		{"acknowledged", "acknowledged", none, none, 30 * time.Minute, ""},
		{"snoozed", "active", time.Hour, none, 30 * time.Minute, ""},
		{"acknowledged and snoozed", "acknowledged", time.Hour, none, 30 * time.Minute, ""},
		{"snooze just over", "active", -time.Minute, none, 30 * time.Minute, ""},
		{"snooze long over", "active", -11 * time.Minute, none, 30 * time.Minute, "reminder"},
		{"just unacknowledged", "active", none, time.Minute, 30 * time.Minute, ""},
		{"unacknowledged long ago", "active", none, 11 * time.Minute, 30 * time.Minute, "reminder"},
		{"unacknowledged long ago, sent since", "active", none, 11 * time.Minute, 5 * time.Minute, ""},
		{"later of unack and snooze end wins", "active", -11 * time.Minute, time.Minute, 30 * time.Minute, ""},
		{"acknowledged again after an unack", "acknowledged", none, 11 * time.Minute, 30 * time.Minute, ""},
	}

	a := newIntegrationAlerter(db)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, tc.name)
			alertID := uuid.New()
			mustExec(ctx, t, db, `
				INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, acknowledged_at,
					snoozed_until, unacknowledged_at, failure_count, created_at, updated_at)
				VALUES ($1, $2, $3, 'availability', $4::alert_status, NOW() - INTERVAL '1 hour',
					CASE WHEN $4::alert_status = 'acknowledged' THEN NOW() END,
					CASE WHEN $5::double precision <> 0 THEN NOW() + make_interval(secs => $5::double precision) END,
					CASE WHEN $6::double precision <> 0 THEN NOW() - make_interval(secs => $6::double precision) END,
					1, NOW(), NOW())
			`, alertID, tenantID, monitorID, tc.status, tc.snoozeIn.Seconds(), tc.unackedAgo.Seconds())
			if tc.sentAgo != 0 {
				mustExec(ctx, t, db, `
					INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type, created_at, updated_at)
					VALUES ($1, $2, NOW() - make_interval(secs => $3), 'created', NOW(), NOW())
				`, alertID, ch, tc.sentAgo.Seconds())
			}

			// Pre-filter: the columns the dispatch query selects, then the twin.
			var gate dispatchGate
			var resumedAt sql.NullTime
			var eligibleSince time.Time
			if err := db.QueryRowContext(ctx, `
				SELECT `+alertrouting.MutedPredicate("al")+`, `+alertrouting.DispatchResumedAtExpr("al")+`,
					`+alertrouting.DispatchEligibleSinceExpr("al", "te")+`
				FROM alerts al JOIN tenants te ON te.id = al.tenant_id WHERE al.id = $1
			`, alertID).Scan(&gate.Muted, &resumedAt, &eligibleSince); err != nil {
				t.Fatal(err)
			}
			gate.ResumedAt = resumedAt.Time
			states, err := a.loadNotificationStates(ctx, []uuid.UUID{alertID})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			state := getNotificationState(states, alertID, ch)
			pre := openAlertEventDue(state, now.Sub(eligibleSince), 0, now, interval, gate)
			if pre != tc.want {
				t.Fatalf("pre-filter = %q, want %q", pre, tc.want)
			}

			// Claim: the one event this state can be due for.
			event := "created"
			if state != nil {
				event = "reminder"
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
		})
	}
}
