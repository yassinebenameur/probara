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

// Dependency-aware alerting (docs/state-semantics.md S-M4): a downstream whose
// upstream dependency is down opens its alert but dispatches nothing while the
// tenant/monitor policy is on; the root cause's own notification lists it; and
// once the upstream recovers the downstream stays quiet for the grace period,
// then pages as a normal DOWN if it is still down.

// sentEvent is one captured sendFunc call.
type sentEvent struct {
	monitorID uuid.UUID
	eventType string
	record    alertRecord
}

// sentRecorder swaps sendFunc for an in-memory capture.
type sentRecorder struct {
	mu     sync.Mutex
	events []sentEvent
}

func (r *sentRecorder) install(a *Alerter) {
	a.sendFunc = func(_ context.Context, _ alertChannel, eventType string, _ policyBinding, alert *alertRecord, _ *groupDetail, _ time.Time) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.events = append(r.events, sentEvent{monitorID: alert.MonitorID, eventType: eventType, record: *alert})
		return nil
	}
}

func (r *sentRecorder) count(monitorID uuid.UUID, eventType string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.events {
		if e.monitorID == monitorID && e.eventType == eventType {
			n++
		}
	}
	return n
}

func (r *sentRecorder) last(monitorID uuid.UUID, eventType string) (alertRecord, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.events) - 1; i >= 0; i-- {
		if r.events[i].monitorID == monitorID && r.events[i].eventType == eventType {
			return r.events[i].record, true
		}
	}
	return alertRecord{}, false
}

// dependencyFixture is the standard two-monitor chain: downstream depends on
// upstream, tenant default channel routed, alerter with a capture sendFunc.
type dependencyFixture struct {
	db           *shareddb.Client
	tenantID     uuid.UUID
	upstreamID   uuid.UUID
	downstreamID uuid.UUID
	channelID    uuid.UUID
	alerter      *Alerter
	sent         *sentRecorder
}

func newDependencyFixture(ctx context.Context, t *testing.T, tenantSuppression bool, graceSeconds int) *dependencyFixture {
	t.Helper()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "dep")
	mustExec(ctx, t, dbClient, `
		UPDATE tenants SET dependency_suppression_enabled = $2, dependency_suppression_grace_seconds = $3
		WHERE id = $1`, tenantID, tenantSuppression, graceSeconds)
	upstreamID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "Postgres prod")
	downstreamID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "Backend API")
	insertDependencyForTest(ctx, t, dbClient, downstreamID, upstreamID)
	channelID := insertTestChannel(ctx, t, dbClient, tenantID, "hook")
	mustExec(ctx, t, dbClient,
		`INSERT INTO tenant_default_channels (tenant_id, channel_id) VALUES ($1, $2)`, tenantID, channelID)

	a := newIntegrationAlerter(dbClient)
	sent := &sentRecorder{}
	sent.install(a)
	return &dependencyFixture{
		db: dbClient, tenantID: tenantID, upstreamID: upstreamID, downstreamID: downstreamID,
		channelID: channelID, alerter: a, sent: sent,
	}
}

func (f *dependencyFixture) tick(ctx context.Context, t *testing.T) {
	t.Helper()
	if err := f.alerter.runLifecycle(ctx); err != nil {
		t.Fatalf("runLifecycle error = %v", err)
	}
}

// ageRootCauseClearedAt rewinds the grace clock so the test does not have to
// wait out a real grace period.
func (f *dependencyFixture) ageRootCauseClearedAt(ctx context.Context, t *testing.T, monitorID uuid.UUID, by time.Duration) {
	t.Helper()
	mustExec(ctx, t, f.db, `
		UPDATE alerts SET root_cause_cleared_at = root_cause_cleared_at - $2::interval
		WHERE monitor_id = $1 AND status IN ('active', 'acknowledged')`,
		monitorID, by.String())
}

func (f *dependencyFixture) openAlertCount(ctx context.Context, t *testing.T, monitorID uuid.UUID) int {
	t.Helper()
	var n int
	if err := f.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM alerts WHERE monitor_id = $1 AND status IN ('active', 'acknowledged')`,
		monitorID).Scan(&n); err != nil {
		t.Fatalf("count open alerts: %v", err)
	}
	return n
}

func TestDependencySuppressionSilencesDownstreamAndListsItOnRootCause(t *testing.T) {
	// S-M4 core case: upstream and downstream both down, tenant policy on.
	// Only the upstream pages, and its notification names the downstream.
	ctx := context.Background()
	f := newDependencyFixture(ctx, t, true, 120)

	setStateForTest(ctx, t, f.db, f.upstreamID, "down", time.Now().UTC().Add(-time.Minute))
	setStateForTest(ctx, t, f.db, f.downstreamID, "down", time.Now().UTC())
	f.tick(ctx, t)
	f.tick(ctx, t) // a second tick must not page the downstream either

	if got := f.openAlertCount(ctx, t, f.downstreamID); got != 1 {
		t.Fatalf("downstream must still open its alert (visible, resolvable); open alerts = %d", got)
	}
	if got := f.sent.count(f.downstreamID, "created"); got != 0 {
		t.Fatalf("downstream created notifications = %d, want 0 (suppressed by dependency)", got)
	}
	if got := f.sent.count(f.upstreamID, "created"); got != 1 {
		t.Fatalf("upstream created notifications = %d, want 1", got)
	}
	up, _ := f.sent.last(f.upstreamID, "created")
	if up.ImpactedCount != 1 || len(up.ImpactedMonitors) != 1 || up.ImpactedMonitors[0].Name != "Backend API" {
		t.Fatalf("upstream record impacted = %+v (count %d), want [Backend API]", up.ImpactedMonitors, up.ImpactedCount)
	}
}

func TestDependencySuppressionOffByDefaultKeepsPagingDownstream(t *testing.T) {
	// Opt-in: with the tenant default off and no monitor override, the
	// downstream pages exactly as before, annotated but not silenced, and the
	// upstream lists nothing (it is not standing in for anyone).
	ctx := context.Background()
	f := newDependencyFixture(ctx, t, false, 120)

	setStateForTest(ctx, t, f.db, f.upstreamID, "down", time.Now().UTC().Add(-time.Minute))
	setStateForTest(ctx, t, f.db, f.downstreamID, "down", time.Now().UTC())
	f.tick(ctx, t)

	if got := f.sent.count(f.downstreamID, "created"); got != 1 {
		t.Fatalf("downstream created notifications = %d, want 1 (policy off)", got)
	}
	down, _ := f.sent.last(f.downstreamID, "created")
	if down.RootCauseMonitorID == nil || *down.RootCauseMonitorID != f.upstreamID {
		t.Fatalf("downstream must still carry the root-cause annotation, got %v", down.RootCauseMonitorID)
	}
	up, _ := f.sent.last(f.upstreamID, "created")
	if up.ImpactedCount != 0 {
		t.Fatalf("upstream impacted count = %d, want 0 when nothing is suppressed", up.ImpactedCount)
	}
}

func TestDependencySuppressionMonitorOverrideBeatsTenantDefault(t *testing.T) {
	ctx := context.Background()

	t.Run("off overrides tenant on", func(t *testing.T) {
		f := newDependencyFixture(ctx, t, true, 120)
		mustExec(ctx, t, f.db, `UPDATE monitors SET dependency_suppression = 'off' WHERE id = $1`, f.downstreamID)
		setStateForTest(ctx, t, f.db, f.upstreamID, "down", time.Now().UTC().Add(-time.Minute))
		setStateForTest(ctx, t, f.db, f.downstreamID, "down", time.Now().UTC())
		f.tick(ctx, t)
		if got := f.sent.count(f.downstreamID, "created"); got != 1 {
			t.Fatalf("downstream with dependency_suppression='off' created = %d, want 1", got)
		}
	})

	t.Run("on overrides tenant off", func(t *testing.T) {
		f := newDependencyFixture(ctx, t, false, 120)
		mustExec(ctx, t, f.db, `UPDATE monitors SET dependency_suppression = 'on' WHERE id = $1`, f.downstreamID)
		setStateForTest(ctx, t, f.db, f.upstreamID, "down", time.Now().UTC().Add(-time.Minute))
		setStateForTest(ctx, t, f.db, f.downstreamID, "down", time.Now().UTC())
		f.tick(ctx, t)
		if got := f.sent.count(f.downstreamID, "created"); got != 0 {
			t.Fatalf("downstream with dependency_suppression='on' created = %d, want 0", got)
		}
		up, _ := f.sent.last(f.upstreamID, "created")
		if up.ImpactedCount != 1 {
			t.Fatalf("upstream impacted count = %d, want 1", up.ImpactedCount)
		}
	})
}

func TestDependencySuppressionPagesDownstreamAfterGraceWhenUpstreamRecovers(t *testing.T) {
	// The upstream recovers but the downstream stays down: nothing during the
	// grace period, then a normal DOWN once it is unexplained for long enough.
	ctx := context.Background()
	f := newDependencyFixture(ctx, t, true, 120)

	setStateForTest(ctx, t, f.db, f.upstreamID, "down", time.Now().UTC().Add(-time.Minute))
	setStateForTest(ctx, t, f.db, f.downstreamID, "down", time.Now().UTC())
	f.tick(ctx, t)
	if got := f.sent.count(f.downstreamID, "created"); got != 0 {
		t.Fatalf("downstream paged while upstream down: created = %d", got)
	}

	setStateForTest(ctx, t, f.db, f.upstreamID, "up", time.Now().UTC())
	f.tick(ctx, t)
	if got := f.sent.count(f.downstreamID, "created"); got != 0 {
		t.Fatalf("downstream paged inside the grace period: created = %d", got)
	}
	var clearedAt *time.Time
	if err := f.db.QueryRowContext(ctx, `
		SELECT root_cause_cleared_at FROM alerts WHERE monitor_id = $1 AND status IN ('active', 'acknowledged')`,
		f.downstreamID).Scan(&clearedAt); err != nil {
		t.Fatalf("load root_cause_cleared_at: %v", err)
	}
	if clearedAt == nil {
		t.Fatalf("root_cause_cleared_at must be stamped when the upstream recovers")
	}

	f.ageRootCauseClearedAt(ctx, t, f.downstreamID, 3*time.Minute)
	f.tick(ctx, t)
	if got := f.sent.count(f.downstreamID, "created"); got != 1 {
		t.Fatalf("downstream must page once the grace has elapsed: created = %d, want 1", got)
	}
	down, _ := f.sent.last(f.downstreamID, "created")
	if down.RootCauseMonitorID != nil {
		t.Fatalf("late-paging downstream must not claim a root cause any more, got %v", down.RootCauseMonitorID)
	}
}

func TestDependencySuppressionEscalationDelayCountsFromEligibility(t *testing.T) {
	// A channel with an escalation delay must be measured from the end of the
	// grace period, not from triggered_at: a downstream that was suppressed for
	// an hour must not fire its tier-2 pager the instant it becomes eligible.
	ctx := context.Background()
	f := newDependencyFixture(ctx, t, true, 0)
	mustExec(ctx, t, f.db, `UPDATE tenant_default_channels SET delay_seconds = 600 WHERE tenant_id = $1`, f.tenantID)

	setStateForTest(ctx, t, f.db, f.upstreamID, "down", time.Now().UTC().Add(-2*time.Hour))
	setStateForTest(ctx, t, f.db, f.downstreamID, "down", time.Now().UTC().Add(-time.Hour))
	f.tick(ctx, t)
	// Backdate the downstream alert so it is well past the channel delay.
	mustExec(ctx, t, f.db, `UPDATE alerts SET triggered_at = NOW() - interval '1 hour' WHERE monitor_id = $1`, f.downstreamID)

	setStateForTest(ctx, t, f.db, f.upstreamID, "up", time.Now().UTC())
	f.tick(ctx, t) // clears the root cause; grace is 0 so the alert is eligible now
	if got := f.sent.count(f.downstreamID, "created"); got != 0 {
		t.Fatalf("delayed channel fired immediately on eligibility: created = %d", got)
	}

	f.ageRootCauseClearedAt(ctx, t, f.downstreamID, 11*time.Minute)
	f.tick(ctx, t)
	if got := f.sent.count(f.downstreamID, "created"); got != 1 {
		t.Fatalf("delayed channel must fire once the delay has elapsed since eligibility: created = %d", got)
	}
}

func TestDependencySuppressionNeverAnnouncedAlertSendsNoResolved(t *testing.T) {
	// A downstream that recovers while suppressed was never announced, so no
	// "recovered" goes out for it either; the upstream's resolved still does.
	ctx := context.Background()
	f := newDependencyFixture(ctx, t, true, 120)

	setStateForTest(ctx, t, f.db, f.upstreamID, "down", time.Now().UTC().Add(-time.Minute))
	setStateForTest(ctx, t, f.db, f.downstreamID, "down", time.Now().UTC())
	f.tick(ctx, t)

	setStateForTest(ctx, t, f.db, f.upstreamID, "up", time.Now().UTC())
	setStateForTest(ctx, t, f.db, f.downstreamID, "up", time.Now().UTC())
	f.tick(ctx, t)

	if got := f.openAlertCount(ctx, t, f.downstreamID); got != 0 {
		t.Fatalf("downstream alert must resolve normally, open = %d", got)
	}
	if got := f.sent.count(f.downstreamID, "resolved"); got != 0 {
		t.Fatalf("downstream resolved notifications = %d, want 0 (never announced)", got)
	}
	if got := f.sent.count(f.upstreamID, "resolved"); got != 1 {
		t.Fatalf("upstream resolved notifications = %d, want 1", got)
	}
}

func TestDependencySuppressionStopsRemindersButKeepsResolvedForAnnouncedAlert(t *testing.T) {
	// Out-of-order detection: the downstream paged first, then the upstream is
	// found down. Reminders stop while explained; the recovery is still sent
	// because the outage was announced.
	ctx := context.Background()
	f := newDependencyFixture(ctx, t, true, 120)
	mustExec(ctx, t, f.db, `UPDATE tenants SET alert_reminder_seconds = 60 WHERE id = $1`, f.tenantID)

	setStateForTest(ctx, t, f.db, f.downstreamID, "down", time.Now().UTC())
	f.tick(ctx, t)
	if got := f.sent.count(f.downstreamID, "created"); got != 1 {
		t.Fatalf("downstream created = %d, want 1 before any upstream is down", got)
	}

	setStateForTest(ctx, t, f.db, f.upstreamID, "down", time.Now().UTC())
	f.tick(ctx, t)
	// Make a reminder due and confirm none goes out while suppressed.
	mustExec(ctx, t, f.db, `UPDATE alert_notification_states SET last_sent_at = NOW() - interval '10 minutes'`)
	f.tick(ctx, t)
	if got := f.sent.count(f.downstreamID, "reminder"); got != 0 {
		t.Fatalf("downstream reminder sent while explained by upstream: %d", got)
	}

	setStateForTest(ctx, t, f.db, f.downstreamID, "up", time.Now().UTC())
	f.tick(ctx, t)
	if got := f.sent.count(f.downstreamID, "resolved"); got != 1 {
		t.Fatalf("downstream resolved = %d, want 1 (it was announced)", got)
	}
}

func TestDependencySuppressionCoexistsWithGroupRollup(t *testing.T) {
	// A member of a 'group'-rollup group that also has a down upstream stays
	// silent under either rule; the group alert and the upstream alert page.
	ctx := context.Background()
	f := newDependencyFixture(ctx, t, true, 120)
	groupID := testutil.InsertGroupMonitor(ctx, t, f.db, f.tenantID, "Checkout")
	testutil.AddMonitorToGroup(ctx, t, f.db, f.downstreamID, groupID)
	mustExec(ctx, t, f.db, `UPDATE monitors SET member_alert_rollup = 'group' WHERE id = $1`, groupID)

	setStateForTest(ctx, t, f.db, f.upstreamID, "down", time.Now().UTC().Add(-time.Minute))
	setStateForTest(ctx, t, f.db, f.downstreamID, "down", time.Now().UTC())
	f.tick(ctx, t)

	if got := f.sent.count(f.downstreamID, "created"); got != 0 {
		t.Fatalf("rolled-up, dependency-explained member paged: created = %d", got)
	}
	if got := f.sent.count(f.upstreamID, "created"); got != 1 {
		t.Fatalf("upstream created = %d, want 1", got)
	}
	if got := f.sent.count(groupID, "created"); got != 1 {
		t.Fatalf("group created = %d, want 1", got)
	}
}
