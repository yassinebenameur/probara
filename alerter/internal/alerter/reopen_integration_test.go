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

// relapseRecorder records every send with the paging episode it carried.
type relapseRecorder struct {
	mu    sync.Mutex
	sends []relapseSend
}

type relapseSend struct {
	channel uuid.UUID
	event   string
	relapse int
	alertID uuid.UUID
}

func (r *relapseRecorder) send(_ context.Context, ch alertChannel, eventType string, _ policyBinding, alert *alertRecord, _ *groupDetail, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sends = append(r.sends, relapseSend{channel: ch.ID, event: eventType, relapse: alert.RelapseCount, alertID: alert.ID})
	return nil
}

func (r *relapseRecorder) count(ch uuid.UUID, event string, relapse int) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.sends {
		if s.channel == ch && s.event == event && s.relapse == relapse {
			n++
		}
	}
	return n
}

func availabilityAlertIDs(ctx context.Context, t *testing.T, db *shareddb.Client, monitorID uuid.UUID) []uuid.UUID {
	t.Helper()
	rows, err := db.QueryContext(ctx, `SELECT id FROM alerts WHERE monitor_id = $1 AND kind = 'availability' ORDER BY triggered_at`, monitorID)
	if err != nil {
		t.Fatalf("list alerts: %v", err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// A monitor that goes down again within the tenant's reopen cooldown of an
// auto-resolve gets its alert back (relapse_count+1) instead of a new one.
// The relapse is a new paging episode: the route that heard the resolve is
// paged again exactly once (claimed as "reopened", sent as "created" with
// the relapse count), the later tier counts from the reopen, and the next
// recovery resolves it again.
func TestReopenWithinCooldownPagesTheRelapse(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "reopen")
	mustExec(ctx, t, dbClient, `UPDATE tenants SET alert_reopen_cooldown_seconds = 300, alert_reminder_seconds = 0 WHERE id = $1`, tenantID)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	first := insertDefaultRoute(ctx, t, dbClient, tenantID, "first", 0, false)
	second := insertDefaultRoute(ctx, t, dbClient, tenantID, "second-tier", 600, false)

	rec := &relapseRecorder{}
	a := newIntegrationAlerter(dbClient)
	a.sendFunc = rec.send
	tick := func() {
		t.Helper()
		if err := a.runLifecycle(ctx); err != nil {
			t.Fatalf("runLifecycle: %v", err)
		}
	}

	setStateForTest(ctx, t, dbClient, monitorID, "down", time.Now().UTC())
	tick()
	if rec.count(first, "created", 0) != 1 {
		t.Fatalf("first page: %+v", rec.sends)
	}
	setStateForTest(ctx, t, dbClient, monitorID, "up", time.Now().UTC())
	tick()
	if rec.count(first, "resolved", 0) != 1 {
		t.Fatalf("first resolve: %+v", rec.sends)
	}

	setStateForTest(ctx, t, dbClient, monitorID, "down", time.Now().UTC())
	tick()
	tick() // no second page on the next tick
	ids := availabilityAlertIDs(ctx, t, dbClient, monitorID)
	if len(ids) != 1 {
		t.Fatalf("alerts after relapse = %d, want the one reopened alert", len(ids))
	}
	var status string
	var relapse int
	var reopenedAt *time.Time
	if err := dbClient.QueryRowContext(ctx, `SELECT status, relapse_count, reopened_at FROM alerts WHERE id = $1`, ids[0]).
		Scan(&status, &relapse, &reopenedAt); err != nil {
		t.Fatal(err)
	}
	if status != "active" || relapse != 1 || reopenedAt == nil {
		t.Fatalf("reopened alert = %s relapse %d reopened_at %v", status, relapse, reopenedAt)
	}
	if got := rec.count(first, "created", 1); got != 1 {
		t.Fatalf("relapse pages on the resolved route = %d, want 1 (%+v)", got, rec.sends)
	}
	if eventType, _, ok := notificationStateFor(ctx, t, dbClient, ids[0], first); !ok || eventType != "reopened" {
		t.Fatalf("route state after the relapse page = %q (%v), want reopened", eventType, ok)
	}

	// The second tier counts from the reopen, not from the first trigger
	// (which is now long enough ago that it would be due).
	mustExec(ctx, t, dbClient, `UPDATE alerts SET triggered_at = NOW() - INTERVAL '1 hour' WHERE id = $1`, ids[0])
	tick()
	if got := rec.count(second, "created", 1); got != 0 {
		t.Fatalf("second tier fired %d times before its delay from the reopen", got)
	}
	mustExec(ctx, t, dbClient, `UPDATE alerts SET reopened_at = NOW() - INTERVAL '11 minutes' WHERE id = $1`, ids[0])
	tick()
	if got := rec.count(second, "created", 1); got != 1 {
		t.Fatalf("second tier after its delay from the reopen = %d, want 1", got)
	}

	setStateForTest(ctx, t, dbClient, monitorID, "up", time.Now().UTC())
	tick()
	if rec.count(first, "resolved", 1) != 1 || rec.count(second, "resolved", 1) != 1 {
		t.Fatalf("relapse resolve: %+v", rec.sends)
	}
}

// Nothing else reopens: a cooldown of 0 (the default), a resolve older than
// the cooldown, and a manual resolve all open a new alert.
func TestReopenOnlyRecentAutoResolves(t *testing.T) {
	cases := []struct {
		name        string
		cooldown    int
		resolvedAgo time.Duration
		resolvedVia any
		wantReopen  bool
	}{
		{"within_cooldown", 300, time.Minute, nil, true},
		{"cooldown_off", 0, time.Minute, nil, false},
		{"cooldown_elapsed", 300, 10 * time.Minute, nil, false},
		{"manual_resolve", 300, time.Minute, "ui", false},
	}
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenantID := testutil.InsertTenant(ctx, t, dbClient, "reopen-"+tc.name)
			mustExec(ctx, t, dbClient, `UPDATE tenants SET alert_reopen_cooldown_seconds = $2 WHERE id = $1`, tenantID, tc.cooldown)
			monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
			old := uuid.New()
			mustExec(ctx, t, dbClient, `
				INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, resolved_at, resolved_via,
					failure_count, created_at, updated_at)
				VALUES ($1, $2, $3, 'availability', 'resolved', NOW() - INTERVAL '1 hour', NOW() - ($4::double precision * INTERVAL '1 second'), $5, 3, NOW(), NOW())
			`, old, tenantID, monitorID, tc.resolvedAgo.Seconds(), tc.resolvedVia)

			a := newIntegrationAlerter(dbClient)
			setStateForTest(ctx, t, dbClient, monitorID, "down", time.Now().UTC())
			if err := a.openAlertsForDownMonitors(ctx); err != nil {
				t.Fatalf("openAlertsForDownMonitors: %v", err)
			}
			ids := availabilityAlertIDs(ctx, t, dbClient, monitorID)
			var oldStatus string
			if err := dbClient.QueryRowContext(ctx, `SELECT status FROM alerts WHERE id = $1`, old).Scan(&oldStatus); err != nil {
				t.Fatal(err)
			}
			if tc.wantReopen {
				if len(ids) != 1 || oldStatus != "active" {
					t.Fatalf("want the old alert reopened: %d alerts, old status %s", len(ids), oldStatus)
				}
				return
			}
			if len(ids) != 2 || oldStatus != "resolved" {
				t.Fatalf("want a new alert: %d alerts, old status %s", len(ids), oldStatus)
			}
		})
	}
}

// Only the monitor's latest alert can reopen: a manual resolve of the
// latest one also shields every older auto-resolved alert.
func TestReopenNeverSkipsTheLatestAlert(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "reopen-latest")
	mustExec(ctx, t, dbClient, `UPDATE tenants SET alert_reopen_cooldown_seconds = 3600 WHERE id = $1`, tenantID)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	mustExec(ctx, t, dbClient, `
		INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, resolved_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, 'availability', 'resolved', NOW() - INTERVAL '30 minutes', NOW() - INTERVAL '20 minutes', 3, NOW(), NOW())
	`, uuid.New(), tenantID, monitorID)
	mustExec(ctx, t, dbClient, `
		INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, resolved_at, resolved_via, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, 'availability', 'resolved', NOW() - INTERVAL '10 minutes', NOW() - INTERVAL '5 minutes', 'api', 3, NOW(), NOW())
	`, uuid.New(), tenantID, monitorID)

	a := newIntegrationAlerter(dbClient)
	setStateForTest(ctx, t, dbClient, monitorID, "down", time.Now().UTC())
	if err := a.openAlertsForDownMonitors(ctx); err != nil {
		t.Fatalf("openAlertsForDownMonitors: %v", err)
	}
	var reopened int
	if err := dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM alerts WHERE monitor_id = $1 AND relapse_count > 0`, monitorID).Scan(&reopened); err != nil {
		t.Fatal(err)
	}
	if n := len(availabilityAlertIDs(ctx, t, dbClient, monitorID)); n != 3 || reopened != 0 {
		t.Fatalf("alerts = %d (want 3), reopened = %d (want 0)", n, reopened)
	}
}

// A reopen is a new paging episode: it clears the previous episode's
// acknowledgement, unacknowledge and snooze, and the one-shot "ack sent"
// mark, so the relapse pages and a new ack reaches paging channels again.
// The alert's open incident gets a "relapsed" timeline entry.
func TestReopenStartsANewEpisode(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "reopen-episode")
	mustExec(ctx, t, dbClient, `UPDATE tenants SET alert_reopen_cooldown_seconds = 300, auto_create_incident = TRUE WHERE id = $1`, tenantID)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	route := insertDefaultRoute(ctx, t, dbClient, tenantID, "pager", 0, false)

	rec := &relapseRecorder{}
	a := newIntegrationAlerter(dbClient)
	a.sendFunc = rec.send
	alertID := openDownAlert(ctx, t, dbClient, tenantID, monitorID)
	if err := a.dispatchOpenAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	// Acknowledged (and the ack sent), snoozed, then auto-resolved.
	mustExec(ctx, t, dbClient, `UPDATE alerts SET status = 'resolved', resolved_at = NOW() - INTERVAL '1 minute',
		acknowledged_at = NOW() - INTERVAL '2 minutes', acknowledged_via = 'ui', unacknowledged_at = NOW() - INTERVAL '3 minutes',
		snoozed_until = NOW() + INTERVAL '1 hour' WHERE id = $1`, alertID)
	mustExec(ctx, t, dbClient, `UPDATE alert_notification_states SET last_event_type = 'resolved', acknowledged_sent_at = NOW() WHERE alert_id = $1`, alertID)

	setStateForTest(ctx, t, dbClient, monitorID, "down", time.Now().UTC())
	if err := a.openAlertsForDownMonitors(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	var ackedAt, unackedAt, snoozedUntil, ackSent *time.Time
	var ackVia *string
	if err := dbClient.QueryRowContext(ctx, `SELECT status, acknowledged_at, acknowledged_via, unacknowledged_at, snoozed_until
		FROM alerts WHERE id = $1`, alertID).Scan(&status, &ackedAt, &ackVia, &unackedAt, &snoozedUntil); err != nil {
		t.Fatal(err)
	}
	if status != "active" || ackedAt != nil || ackVia != nil || unackedAt != nil || snoozedUntil != nil {
		t.Fatalf("reopen kept episode state: status=%s acked=%v via=%v unacked=%v snoozed=%v", status, ackedAt, ackVia, unackedAt, snoozedUntil)
	}
	if err := dbClient.QueryRowContext(ctx, `SELECT acknowledged_sent_at FROM alert_notification_states WHERE alert_id = $1`, alertID).Scan(&ackSent); err != nil {
		t.Fatal(err)
	}
	if ackSent != nil {
		t.Fatal("reopen kept the previous episode's ack-sent mark")
	}
	if err := a.dispatchOpenAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	if got := rec.count(route, "created", 1); got != 1 {
		t.Fatalf("relapse of a previously acked+snoozed alert paged %d times, want 1", got)
	}

	var relapsedEntries int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM incident_timeline_entries e
		JOIN incident_alerts ia ON ia.incident_id = e.incident_id
		WHERE ia.alert_id = $1 AND e.metadata->>'event' = 'relapsed' AND (e.metadata->>'relapse_count')::int = 1
	`, alertID).Scan(&relapsedEntries); err != nil {
		t.Fatal(err)
	}
	if relapsedEntries != 1 {
		t.Fatalf("relapsed timeline entries = %d, want 1", relapsedEntries)
	}
}

// Two replicas evaluating the same relapse reopen it once: one wins the
// guarded UPDATE, the other matches nothing and its INSERT conflicts.
func TestReopenIsRaceSafeAcrossReplicas(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "reopen-race")
	mustExec(ctx, t, dbClient, `UPDATE tenants SET alert_reopen_cooldown_seconds = 300 WHERE id = $1`, tenantID)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	mustExec(ctx, t, dbClient, `
		INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, resolved_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, 'availability', 'resolved', NOW() - INTERVAL '10 minutes', NOW() - INTERVAL '1 minute', 3, NOW(), NOW())
	`, uuid.New(), tenantID, monitorID)
	setStateForTest(ctx, t, dbClient, monitorID, "down", time.Now().UTC())

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- newIntegrationAlerter(dbClient).openAlertsForDownMonitors(ctx)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("replica: %v", err)
		}
	}
	var alerts, relapse int
	if err := dbClient.QueryRowContext(ctx, `SELECT COUNT(*), MAX(relapse_count) FROM alerts WHERE monitor_id = $1`, monitorID).
		Scan(&alerts, &relapse); err != nil {
		t.Fatal(err)
	}
	if alerts != 1 || relapse != 1 {
		t.Fatalf("alerts = %d relapse = %d, want one alert reopened once", alerts, relapse)
	}
}

// workerReleasesSupersededResolve does what the async delivery worker does
// (worker notifications releaseSupersededResolveClaim) when it drops a
// resolve because the alert reopened while the resolve sat on a retry
// backoff: it hands the route's 'resolved' claim back to 'created',
// matching the claim by its timestamp.
func workerReleasesSupersededResolve(ctx context.Context, t *testing.T, db *shareddb.Client, alertID, channelID uuid.UUID, claimedAt time.Time) {
	t.Helper()
	mustExec(ctx, t, db, `
		UPDATE alert_notification_states
		SET last_event_type = 'created', updated_at = NOW()
		WHERE alert_id = $1 AND channel_id = $2
		  AND last_event_type = 'resolved' AND last_sent_at = $3::timestamptz`, alertID, channelID, claimedAt)
}

// Async dispatch: the episode-0 resolve is published (its 'resolved' claim
// commits) but waits on the worker's retry backoff, the alert reopens, and
// the worker drops the stale resolve and hands the claim back. The route's
// tier delay has not elapsed from the reopen, so it is never re-paged, and
// the relapse recovers first: the route must still hear the relapse's
// resolve, or the provider incident it opened in episode 0 stays open.
func TestSupersededAsyncResolveStillResolvesTheRelapse(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "reopen-async")
	mustExec(ctx, t, dbClient, `UPDATE tenants SET alert_reopen_cooldown_seconds = 300, alert_reminder_seconds = 0 WHERE id = $1`, tenantID)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	route := insertDefaultRoute(ctx, t, dbClient, tenantID, "pager", 600, false)

	rec := &relapseRecorder{}
	a := newIntegrationAlerter(dbClient)
	// Publishing succeeds; delivery is the worker's business.
	a.sendFunc = rec.send
	tick := func() {
		t.Helper()
		if err := a.runLifecycle(ctx); err != nil {
			t.Fatalf("runLifecycle: %v", err)
		}
	}

	setStateForTest(ctx, t, dbClient, monitorID, "down", time.Now().UTC())
	tick()
	ids := availabilityAlertIDs(ctx, t, dbClient, monitorID)
	if len(ids) != 1 {
		t.Fatalf("alerts = %d, want 1", len(ids))
	}
	alertID := ids[0]
	mustExec(ctx, t, dbClient, `UPDATE alerts SET triggered_at = NOW() - INTERVAL '11 minutes' WHERE id = $1`, alertID)
	tick()
	if rec.count(route, "created", 0) != 1 {
		t.Fatalf("episode 0 page after the tier delay: %+v", rec.sends)
	}

	setStateForTest(ctx, t, dbClient, monitorID, "up", time.Now().UTC())
	tick()
	if rec.count(route, "resolved", 0) != 1 {
		t.Fatalf("episode 0 resolve not published: %+v", rec.sends)
	}
	eventType, claimedAt, ok := notificationStateFor(ctx, t, dbClient, alertID, route)
	if !ok || eventType != "resolved" {
		t.Fatalf("route after the published resolve = %q (%v), want resolved", eventType, ok)
	}

	// The resolve is still on the worker's backoff when the monitor goes
	// down again: the alert reopens and the route is not due (600s from the
	// reopen).
	setStateForTest(ctx, t, dbClient, monitorID, "down", time.Now().UTC())
	tick()
	var relapse int
	if err := dbClient.QueryRowContext(ctx, `SELECT relapse_count FROM alerts WHERE id = $1`, alertID).Scan(&relapse); err != nil {
		t.Fatal(err)
	}
	if relapse != 1 {
		t.Fatalf("relapse_count = %d, want 1", relapse)
	}
	if got := rec.count(route, "created", 1); got != 0 {
		t.Fatalf("delayed route paged %d times before its delay from the reopen", got)
	}

	// The redelivered resolve(0) is dropped as superseded and its claim handed back.
	workerReleasesSupersededResolve(ctx, t, dbClient, alertID, route, claimedAt)
	tick()
	if got := rec.count(route, "created", 1); got != 0 {
		t.Fatalf("route still paged by the provider's open incident was re-paged %d times", got)
	}

	// The relapse recovers before the route's delay: its resolve reaches the route.
	setStateForTest(ctx, t, dbClient, monitorID, "up", time.Now().UTC())
	tick()
	if got := rec.count(route, "resolved", 1); got != 1 {
		t.Fatalf("relapse resolve on the route = %d, want 1 (%+v)", got, rec.sends)
	}
}
