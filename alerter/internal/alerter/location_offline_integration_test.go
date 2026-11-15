//go:build !unit

package alerter

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/alertrouting"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/monitorstate"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// insertLocationSeen seeds a mesh-participating location whose last
// heartbeat was seenAgo ago (nil: never seen).
func insertLocationSeen(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID uuid.UUID, name string, seenAgo *time.Duration) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var lastSeen any
	if seenAgo != nil {
		lastSeen = time.Now().Add(-*seenAgo)
	}
	// Enabled long ago, so only the heartbeat decides (S-F6 also waits one
	// threshold after a re-enable).
	mustExec(ctx, t, db, `
		INSERT INTO locations (id, tenant_id, name, slug, mesh_endpoint, enabled, last_seen_at, enabled_at, created_at, updated_at)
		VALUES ($1, $2, $3, $3, $3 || '.mesh.internal:8080', TRUE, $4, NOW() - INTERVAL '30 days', NOW(), NOW())
	`, id, tenantID, name, lastSeen)
	return id
}

// insertDefaultChannel adds a webhook channel to the tenant's default routes.
func insertDefaultChannel(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID uuid.UUID, name string, delaySeconds int) uuid.UUID {
	t.Helper()
	channelID := insertTestChannel(ctx, t, db, tenantID, name)
	mustExec(ctx, t, db, `
		INSERT INTO tenant_default_channels (tenant_id, channel_id, delay_seconds, position)
		VALUES ($1, $2, $3, (SELECT COUNT(*) FROM tenant_default_channels WHERE tenant_id = $1))
	`, tenantID, channelID, delaySeconds)
	return channelID
}

func locationOfflineAlerts(ctx context.Context, t *testing.T, db *shareddb.Client, locationID uuid.UUID, status string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM alerts WHERE kind = 'location_offline' AND source_location_id = $1 AND status = $2
	`, locationID, status).Scan(&n); err != nil {
		t.Fatalf("count location offline alerts: %v", err)
	}
	return n
}

func latestResolveReason(ctx context.Context, t *testing.T, db *shareddb.Client, locationID uuid.UUID) string {
	t.Helper()
	var reason *string
	if err := db.QueryRowContext(ctx, `
		SELECT resolve_reason FROM alerts WHERE kind = 'location_offline' AND source_location_id = $1
		ORDER BY resolved_at DESC NULLS LAST LIMIT 1`, locationID).Scan(&reason); err != nil || reason == nil {
		t.Fatalf("resolve_reason = %v (%v)", reason, err)
	}
	return *reason
}

func dur(d time.Duration) *time.Duration { return &d }

// setSeenAgo moves a location's last heartbeat to `ago` before the database
// clock.
func setSeenAgo(ctx context.Context, t *testing.T, db *shareddb.Client, locationID uuid.UUID, ago time.Duration) {
	t.Helper()
	mustExec(ctx, t, db, `UPDATE locations SET last_seen_at = NOW() - make_interval(secs => $2) WHERE id = $1`, locationID, ago.Seconds())
}

// S-F6: a location silent past 180s raises exactly one location_offline alert
// across two replicas, routed to the tenant default channels and only there;
// never-seen, disabled and recently seen locations raise none; recovery
// resolves it and sends the resolve notice once to the channel it paged.
func TestLocationOfflineLifecycle(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "loc-offline")
	defaultID := insertDefaultChannel(ctx, t, dbClient, tenantID, "ops", 0)
	// A channel that exists but is no default route must hear nothing.
	otherID := insertTestChannel(ctx, t, dbClient, tenantID, "not-routed")

	dead := insertLocationSeen(ctx, t, dbClient, tenantID, "dead", dur(10*time.Minute))
	fresh := insertLocationSeen(ctx, t, dbClient, tenantID, "fresh", dur(10*time.Second))
	quiet := insertLocationSeen(ctx, t, dbClient, tenantID, "quiet", dur(2*time.Minute))
	never := insertLocationSeen(ctx, t, dbClient, tenantID, "never", nil)
	disabled := insertLocationSeen(ctx, t, dbClient, tenantID, "disabled", dur(time.Hour))
	mustExec(ctx, t, dbClient, `UPDATE locations SET enabled = FALSE WHERE id = $1`, disabled)
	for _, name := range []string{"only-here", "also-fresh"} {
		monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, name)
		mustExec(ctx, t, dbClient, `INSERT INTO monitor_locations (monitor_id, location_id) VALUES ($1, $2)`, monitorID, dead)
		if name == "also-fresh" {
			mustExec(ctx, t, dbClient, `INSERT INTO monitor_locations (monitor_id, location_id) VALUES ($1, $2)`, monitorID, fresh)
		}
	}

	counter := newSendCounter(50 * time.Millisecond)
	replicas := newReplicaPair(dbClient, counter)
	runConcurrently(t, replicas, func(a *Alerter) error { return a.evaluateLocationOffline(ctx) })

	if got := locationOfflineAlerts(ctx, t, dbClient, dead, "active"); got != 1 {
		t.Fatalf("offline location alerts = %d, want exactly 1 across replicas", got)
	}
	for name, id := range map[string]uuid.UUID{"fresh": fresh, "quiet (120s)": quiet, "never": never, "disabled": disabled} {
		if got := locationOfflineAlerts(ctx, t, dbClient, id, "active"); got != 0 {
			t.Fatalf("%s location opened %d alerts", name, got)
		}
	}
	if got := counter.count("created"); got != 1 {
		t.Fatalf("created sends = %d, want 1 (one tenant default route, once across replicas)", got)
	}
	var alertID uuid.UUID
	var monitorIDCol, targetCol *uuid.UUID
	if err := dbClient.QueryRowContext(ctx, `
		SELECT id, monitor_id, target_location_id FROM alerts WHERE kind = 'location_offline' AND source_location_id = $1
	`, dead).Scan(&alertID, &monitorIDCol, &targetCol); err != nil || monitorIDCol != nil || targetCol != nil {
		t.Fatalf("alert subject: monitor_id=%v target=%v err=%v", monitorIDCol, targetCol, err)
	}
	if _, _, ok := notificationStateFor(ctx, t, dbClient, alertID, defaultID); !ok {
		t.Fatal("tenant default route has no created claim")
	}
	if _, _, ok := notificationStateFor(ctx, t, dbClient, alertID, otherID); ok {
		t.Fatal("a channel outside the tenant defaults was notified")
	}

	// The record carries the location's blast radius for the wording.
	spy := &meshSendSpy{}
	a := newIntegrationAlerter(dbClient)
	var captured []alertRecord
	a.sendFunc = func(_ context.Context, channel alertChannel, eventType string, binding alertSubject, alert *alertRecord, _ *groupDetail, _ time.Time) error {
		spy.record(eventType, channel.ID, binding.MonitorName)
		captured = append(captured, *alert)
		return nil
	}
	mustExec(ctx, t, dbClient, `UPDATE tenants SET alert_reminder_seconds = 60 WHERE id = $1`, tenantID)
	mustExec(ctx, t, dbClient, `UPDATE alert_notification_states SET last_sent_at = NOW() - INTERVAL '2 minutes' WHERE alert_id = $1`, alertID)
	if err := a.evaluateLocationOffline(ctx); err != nil {
		t.Fatal(err)
	}
	reminders := spy.byType("reminder")
	if len(reminders) != 1 || reminders[0].monitorName != "dead" || reminders[0].channelID != defaultID {
		t.Fatalf("reminders = %+v, want one to the default route naming the location", reminders)
	}
	if r := captured[0]; r.LocationMonitorCount != 2 || r.LocationOnlyMonitorCount != 1 || r.LocationLastSeenAt == nil {
		t.Fatalf("record annotation = total %d, only %d, last seen %v; want 2, 1, set", r.LocationMonitorCount, r.LocationOnlyMonitorCount, r.LocationLastSeenAt)
	}

	// The location reports again: resolved once, and the paged route hears it.
	mustExec(ctx, t, dbClient, `UPDATE locations SET last_seen_at = NOW() WHERE id = $1`, dead)
	runConcurrently(t, replicas, func(a *Alerter) error { return a.evaluateLocationOffline(ctx) })
	if got := locationOfflineAlerts(ctx, t, dbClient, dead, "resolved"); got != 1 {
		t.Fatalf("resolved offline alerts = %d, want 1", got)
	}
	if got := latestResolveReason(ctx, t, dbClient, dead); got != "recovered" {
		t.Fatalf("resolve_reason = %q, want recovered", got)
	}
	if got := counter.count("resolved"); got != 1 {
		t.Fatalf("resolved sends = %d, want 1", got)
	}
	if got := counter.count("created"); got != 1 {
		t.Fatalf("created sends after recovery = %d, want still 1", got)
	}
}

// The resolve notice survives a failed send: the recovery sweep retries it
// with the location as the subject.
func TestLocationOfflineResolveNoticeRetried(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "loc-offline-retry")
	insertDefaultChannel(ctx, t, dbClient, tenantID, "ops", 0)
	dead := insertLocationSeen(ctx, t, dbClient, tenantID, "factory", dur(10*time.Minute))

	a := newIntegrationAlerter(dbClient)
	if err := a.evaluateLocationOffline(ctx); err != nil {
		t.Fatal(err)
	}
	mustExec(ctx, t, dbClient, `UPDATE locations SET enabled = FALSE WHERE id = $1`, dead)
	failing := newSendCounter(0)
	failing.fail.Store(true)
	a.sendFunc = failing.send
	if err := a.evaluateLocationOffline(ctx); err != nil {
		t.Fatal(err)
	}
	if got := latestResolveReason(ctx, t, dbClient, dead); got != "location_disabled" {
		t.Fatalf("resolve_reason = %q, want location_disabled", got)
	}

	var got []alertRecord
	var names []string
	a.sendFunc = func(_ context.Context, _ alertChannel, eventType string, binding alertSubject, alert *alertRecord, _ *groupDetail, _ time.Time) error {
		if eventType == "resolved" {
			got = append(got, *alert)
			names = append(names, binding.MonitorName)
		}
		return nil
	}
	if err := a.dispatchPendingRecoveries(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || names[0] != "factory" || got[0].SourceLocationName == nil || *got[0].SourceLocationName != "factory" ||
		got[0].ResolveReason != "location_disabled" {
		t.Fatalf("retried resolve = %+v names=%v", got, names)
	}
}

// Disabling or deleting a location resolves its open alert, with a reason
// that keeps the closing notice from claiming it came back.
func TestLocationOfflineResolvesWhenDisabledOrDeleted(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "loc-offline-resolve")
	a := newIntegrationAlerter(dbClient)
	run := func() {
		t.Helper()
		if err := a.evaluateLocationOffline(ctx); err != nil {
			t.Fatalf("evaluateLocationOffline: %v", err)
		}
	}
	// A reporting location keeps the fleet alive (fleet-silence guard).
	insertLocationSeen(ctx, t, dbClient, tenantID, "healthy", dur(5*time.Second))
	disabledLater := insertLocationSeen(ctx, t, dbClient, tenantID, "disabled-later", dur(time.Hour))
	deletedLater := insertLocationSeen(ctx, t, dbClient, tenantID, "deleted-later", dur(time.Hour))
	run()
	if locationOfflineAlerts(ctx, t, dbClient, disabledLater, "active") != 1 || locationOfflineAlerts(ctx, t, dbClient, deletedLater, "active") != 1 {
		t.Fatal("both silent locations should alert")
	}
	mustExec(ctx, t, dbClient, `UPDATE locations SET enabled = FALSE WHERE id = $1`, disabledLater)
	mustExec(ctx, t, dbClient, `UPDATE locations SET deleted_at = NOW() WHERE id = $1`, deletedLater)
	run()
	if got := latestResolveReason(ctx, t, dbClient, disabledLater); got != "location_disabled" {
		t.Fatalf("disabled location resolve_reason = %q", got)
	}
	if got := latestResolveReason(ctx, t, dbClient, deletedLater); got != "location_deleted" {
		t.Fatalf("deleted location resolve_reason = %q", got)
	}
	run()
	if locationOfflineAlerts(ctx, t, dbClient, disabledLater, "active")+locationOfflineAlerts(ctx, t, dbClient, deletedLater, "active") != 0 {
		t.Fatal("a disabled or deleted location reopened its alert")
	}
}

// Fleet-silence guard (S-F6). When the heartbeat path stalls, every
// location goes quiet together but crosses the 180 s threshold one by one,
// because heartbeats are staggered: no alert may open for the locations last
// heard within a minute of the newest heartbeat, on either of two replicas
// ticking at different moments. A location that went quiet well before them
// is a real outage and alerts; a location beyond the 7-day horizon never
// does; and after 15 minutes of silence the held locations alert too.
func TestLocationOfflineFleetSilenceStaggered(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "fleet-silence")
	other := testutil.InsertTenant(ctx, t, dbClient, "fleet-silence-other")
	// Staggered last heartbeats: east has already crossed the threshold,
	// west and north (another workspace: the heartbeat path is shared) not
	// yet. early went quiet ten minutes before them.
	east := insertLocationSeen(ctx, t, dbClient, tenantID, "east", dur(181*time.Second))
	west := insertLocationSeen(ctx, t, dbClient, tenantID, "west", dur(170*time.Second))
	north := insertLocationSeen(ctx, t, dbClient, other, "north", dur(160*time.Second))
	early := insertLocationSeen(ctx, t, dbClient, tenantID, "early", dur(10*time.Minute))
	abandoned := insertLocationSeen(ctx, t, dbClient, tenantID, "abandoned", dur(8*24*time.Hour))
	cluster := map[string]uuid.UUID{"east": east, "west": west, "north": north}
	// Time passes for every location alike.
	seen := map[uuid.UUID]time.Duration{east: 181 * time.Second, west: 170 * time.Second, north: 160 * time.Second, early: 10 * time.Minute}
	elapse := func(d time.Duration) {
		t.Helper()
		for id, ago := range seen {
			seen[id] = ago + d
			setSeenAgo(ctx, t, dbClient, id, seen[id])
		}
	}
	held := func(phase string) {
		t.Helper()
		for name, id := range cluster {
			if got := locationOfflineAlerts(ctx, t, dbClient, id, "active"); got != 0 {
				t.Fatalf("%s: %s opened %d alerts while the fleet went quiet together", phase, name, got)
			}
		}
	}

	r1, r2 := newIntegrationAlerter(dbClient), newIntegrationAlerter(dbClient)
	if err := r1.evaluateLocationOffline(ctx); err != nil {
		t.Fatal(err)
	}
	held("replica 1")
	if r1.lastLocationHold != "fleet_silent" {
		t.Fatalf("hold = %q, want fleet_silent", r1.lastLocationHold)
	}
	if locationOfflineAlerts(ctx, t, dbClient, early, "active") != 1 {
		t.Fatal("a location that went quiet well before the cluster must alert")
	}
	if locationOfflineAlerts(ctx, t, dbClient, abandoned, "active") != 0 {
		t.Fatal("a location silent beyond the horizon must not alert")
	}

	// 25 s later every cluster member is past the threshold; the other
	// replica ticks now.
	elapse(25 * time.Second)
	if err := r2.evaluateLocationOffline(ctx); err != nil {
		t.Fatal(err)
	}
	held("replica 2")
	elapse(5 * time.Minute)
	if err := r1.evaluateLocationOffline(ctx); err != nil {
		t.Fatal(err)
	}
	held("replica 1, 5 min on")

	// Fifteen minutes after the newest heartbeat the hold ends: a heartbeat
	// path down that long is worth a page.
	elapse(10 * time.Minute)
	if err := r2.evaluateLocationOffline(ctx); err != nil {
		t.Fatal(err)
	}
	for name, id := range cluster {
		if got := locationOfflineAlerts(ctx, t, dbClient, id, "active"); got != 1 {
			t.Fatalf("after the hold limit %s has %d alerts, want 1", name, got)
		}
	}
	if r2.lastLocationHold != "" {
		t.Fatalf("hold = %q after the limit", r2.lastLocationHold)
	}
}

// Two real outages in a row on a two-location install: the second site's
// death is not a "fleet went quiet together" pattern (the first went quiet
// twenty minutes earlier), so it alerts. A lone location still alerts.
func TestLocationOfflineConsecutiveOutagesBothAlert(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "consecutive")
	a := newIntegrationAlerter(dbClient)
	run := func() {
		t.Helper()
		if err := a.evaluateLocationOffline(ctx); err != nil {
			t.Fatalf("evaluateLocationOffline: %v", err)
		}
	}
	siteA := insertLocationSeen(ctx, t, dbClient, tenantID, "site-a", dur(25*time.Minute))
	siteB := insertLocationSeen(ctx, t, dbClient, tenantID, "site-b", dur(5*time.Second))
	run()
	if locationOfflineAlerts(ctx, t, dbClient, siteA, "active") != 1 {
		t.Fatal("site A must alert")
	}
	setSeenAgo(ctx, t, dbClient, siteB, 200*time.Second)
	run()
	if locationOfflineAlerts(ctx, t, dbClient, siteB, "active") != 1 {
		t.Fatalf("site B must alert too (hold %q)", a.lastLocationHold)
	}

	solo := testutil.InsertTenant(ctx, t, dbClient, "solo")
	mustExec(ctx, t, dbClient, `UPDATE locations SET deleted_at = NOW()`)
	only := insertLocationSeen(ctx, t, dbClient, solo, "only", dur(10*time.Minute))
	run()
	if locationOfflineAlerts(ctx, t, dbClient, only, "active") != 1 {
		t.Fatal("the only location of the fleet must alert when it goes silent")
	}
}

// A location re-enabled less than one threshold ago is not offline yet, so
// it does not page before its worker had a chance to come back.
func TestLocationOfflineWaitsAfterReEnable(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "re-enable")
	loc := insertLocationSeen(ctx, t, dbClient, tenantID, "returning", dur(48*time.Hour))
	mustExec(ctx, t, dbClient, `UPDATE locations SET enabled_at = NOW() - INTERVAL '10 seconds' WHERE id = $1`, loc)
	a := newIntegrationAlerter(dbClient)
	if err := a.evaluateLocationOffline(ctx); err != nil {
		t.Fatal(err)
	}
	if locationOfflineAlerts(ctx, t, dbClient, loc, "active") != 0 {
		t.Fatal("a location re-enabled 10 s ago paged")
	}
	mustExec(ctx, t, dbClient, `UPDATE locations SET enabled_at = NOW() - INTERVAL '181 seconds' WHERE id = $1`, loc)
	if err := a.evaluateLocationOffline(ctx); err != nil {
		t.Fatal(err)
	}
	if locationOfflineAlerts(ctx, t, dbClient, loc, "active") != 1 {
		t.Fatal("still silent one threshold after the re-enable: must alert")
	}
}

// Startup guard: an alerter that just started (start time read from the
// database clock) does not trust last_seen_at until a heartbeat lands after
// its start, or one offline threshold has passed.
func TestLocationOfflineHeldAtStartup(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "startup")
	dead := insertLocationSeen(ctx, t, dbClient, tenantID, "dead", dur(10*time.Minute))
	a := newIntegrationAlerter(dbClient)
	a.startupHold = true
	if err := a.evaluateLocationOffline(ctx); err != nil {
		t.Fatal(err)
	}
	if a.startedAt.IsZero() {
		t.Fatal("the start time must come from the database on the first evaluation")
	}
	if locationOfflineAlerts(ctx, t, dbClient, dead, "active") != 0 || a.lastLocationHold != "startup" {
		t.Fatalf("a fresh alerter opened an alert before any heartbeat (hold %q)", a.lastLocationHold)
	}

	// A heartbeat after the start proves the ingest writes: no more hold.
	insertLocationSeen(ctx, t, dbClient, tenantID, "alive", nil)
	mustExec(ctx, t, dbClient, `UPDATE locations SET last_seen_at = NOW() WHERE name = 'alive'`)
	if err := a.evaluateLocationOffline(ctx); err != nil {
		t.Fatal(err)
	}
	if locationOfflineAlerts(ctx, t, dbClient, dead, "active") != 1 {
		t.Fatal("the hold must end once a heartbeat lands after startup")
	}

	// Or the threshold passes with none (a one-location fleet that died).
	solo := testutil.InsertTenant(ctx, t, dbClient, "startup-solo")
	mustExec(ctx, t, dbClient, `UPDATE locations SET deleted_at = NOW()`)
	lone := insertLocationSeen(ctx, t, dbClient, solo, "lone", dur(10*time.Minute))
	b := newIntegrationAlerter(dbClient)
	b.startupHold = true
	if err := dbClient.QueryRowContext(ctx, `SELECT NOW() - INTERVAL '181 seconds'`).Scan(&b.startedAt); err != nil {
		t.Fatal(err)
	}
	if err := b.evaluateLocationOffline(ctx); err != nil {
		t.Fatal(err)
	}
	if locationOfflineAlerts(ctx, t, dbClient, lone, "active") != 1 {
		t.Fatal("the startup hold must end after one threshold")
	}
}

// While the guard holds a location's own alert, nothing would page for its
// mesh edges if they were held too: they page for themselves.
func TestMeshEdgePagesWhileLocationAlertIsHeld(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "mesh-guard")
	insertDefaultChannel(ctx, t, dbClient, tenantID, "ops", 0)
	x := insertLocationSeen(ctx, t, dbClient, tenantID, "x", dur(200*time.Second))
	y := insertLocationSeen(ctx, t, dbClient, tenantID, "y", dur(195*time.Second))
	insertMeshEdgeState(ctx, t, dbClient, tenantID, x, y, "down", 3, "dial tcp: i/o timeout")

	a, spy := newMeshAlerter(dbClient)
	if err := a.evaluateLocationOffline(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.evaluateMeshEdges(ctx); err != nil {
		t.Fatal(err)
	}
	if a.lastLocationHold != "fleet_silent" || locationOfflineAlerts(ctx, t, dbClient, x, "active")+locationOfflineAlerts(ctx, t, dbClient, y, "active") != 0 {
		t.Fatalf("setup: the location alerts must be held (hold %q)", a.lastLocationHold)
	}
	created := spy.byType("created")
	if len(created) != 1 || created[0].monitorName != "mesh: x → y" {
		t.Fatalf("mesh pages while the location alerts are held = %+v, want the edge", created)
	}
}

// A mesh edge whose endpoint location stopped reporting stays open but
// sends nothing, from the moment the location misses its connected window
// (edges into a dead site go down well before it counts as offline); once
// the location reports again a still-down edge pages. An edge that already
// paged stops its reminders while the location is out. A location silent
// beyond the horizon, with no offline alert to speak for it, silences
// nothing.
func TestMeshEdgeOfOfflineLocationIsQuiet(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "mesh-offline")
	mustExec(ctx, t, dbClient, `UPDATE tenants SET alert_reminder_seconds = 60 WHERE id = $1`, tenantID)
	insertDefaultChannel(ctx, t, dbClient, tenantID, "ops", 0)
	alive := insertLocationSeen(ctx, t, dbClient, tenantID, "alive", dur(5*time.Second))
	dying := insertLocationSeen(ctx, t, dbClient, tenantID, "dying", dur(90*time.Second))
	dead := insertLocationSeen(ctx, t, dbClient, tenantID, "dead", dur(time.Hour))
	insertMeshEdgeState(ctx, t, dbClient, tenantID, alive, dying, "down", 3, "dial tcp: i/o timeout")
	insertMeshEdgeState(ctx, t, dbClient, tenantID, dead, alive, "down", 3, "dial tcp: i/o timeout")

	a, spy := newMeshAlerter(dbClient)
	tick := func() {
		t.Helper()
		if err := a.evaluateLocationOffline(ctx); err != nil {
			t.Fatal(err)
		}
		if err := a.evaluateMeshEdges(ctx); err != nil {
			t.Fatal(err)
		}
	}
	tick()
	if countMeshEdgeAlerts(ctx, t, dbClient, alive, dying, "active") != 1 || countMeshEdgeAlerts(ctx, t, dbClient, dead, alive, "active") != 1 {
		t.Fatal("the mesh edge alerts must still open")
	}
	if locationOfflineAlerts(ctx, t, dbClient, dying, "active") != 0 || locationOfflineAlerts(ctx, t, dbClient, dead, "active") != 1 {
		t.Fatal("90s silent is not offline yet; an hour silent is")
	}
	for _, e := range spy.byType("created") {
		if e.monitorName != "dead" {
			t.Fatalf("a mesh edge of a silent location paged: %+v", e)
		}
	}

	// The API reports why each edge is quiet with the same predicate.
	var held int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM alerts al WHERE al.kind = 'mesh_edge' AND `+alertrouting.SuppressedByOfflineLocationPredicate("al"),
	).Scan(&held); err != nil || held != 2 {
		t.Fatalf("held mesh edges = %d (%v), want 2", held, err)
	}

	// dying reports again while its edge is still down: that edge pages now.
	mustExec(ctx, t, dbClient, `UPDATE locations SET last_seen_at = NOW() WHERE id = $1`, dying)
	tick()
	edgePages := 0
	for _, e := range spy.byType("created") {
		if e.monitorName == "mesh: alive → dying" {
			edgePages++
		}
	}
	if edgePages != 1 {
		t.Fatalf("edge after the location reconnected: created = %d, want 1", edgePages)
	}

	// dying goes quiet again: the edge that already paged sends no reminder.
	mustExec(ctx, t, dbClient, `UPDATE locations SET last_seen_at = NOW() - INTERVAL '90 seconds' WHERE id = $1`, dying)
	mustExec(ctx, t, dbClient, `UPDATE alert_notification_states SET last_sent_at = NOW() - INTERVAL '2 minutes'`)
	tick()
	for _, e := range spy.byType("reminder") {
		if e.monitorName != "dead" {
			t.Fatalf("a held mesh edge sent a reminder: %+v", e)
		}
	}

	// Silent beyond the horizon and no offline alert: nothing speaks for
	// the edge, so it pages.
	other := testutil.InsertTenant(ctx, t, dbClient, "mesh-abandoned")
	insertDefaultChannel(ctx, t, dbClient, other, "ops", 0)
	watcher := insertLocationSeen(ctx, t, dbClient, other, "watcher", dur(5*time.Second))
	gone := insertLocationSeen(ctx, t, dbClient, other, "gone", dur(8*24*time.Hour))
	insertMeshEdgeState(ctx, t, dbClient, other, watcher, gone, "down", 3, "dial tcp: i/o timeout")
	tick()
	if locationOfflineAlerts(ctx, t, dbClient, gone, "active") != 0 {
		t.Fatal("a location beyond the horizon opened an offline alert")
	}
	abandonedPages := 0
	for _, e := range spy.byType("created") {
		if e.monitorName == "mesh: watcher → gone" {
			abandonedPages++
		}
	}
	if abandonedPages != 1 {
		t.Fatalf("edge into an abandoned location: created = %d, want 1", abandonedPages)
	}
}

// The offline and disconnected predicates (SQL) and their Go twins agree,
// including exactly at the 180 s and 60 s boundaries and the re-enable
// grace: the rows are written and read in one transaction, so NOW() is one
// instant for every row (S-F6).
func TestLocationPredicatesMatchGo(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "loc-twin")
	tx, err := dbClient.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	type row struct {
		name                string
		seenAgo, enabledAgo *string
		enabled, deleted    bool
	}
	str := func(s string) *string { return &s }
	long := str("30 days")
	rows := []row{
		{"never", nil, long, true, false},
		{"fresh", str("5 seconds"), long, true, false},
		{"connected_edge", str("59.999999 seconds"), long, true, false},
		{"disconnected_edge", str("60 seconds"), long, true, false},
		{"offline_under", str("179.999999 seconds"), long, true, false},
		{"offline_edge", str("180 seconds"), long, true, false},
		{"long", str("30 days"), long, true, false},
		{"enabled_under", str("1 hour"), str("179.999999 seconds"), true, false},
		{"enabled_edge", str("1 hour"), str("180 seconds"), true, false},
		{"disabled", str("1 hour"), long, false, false},
		{"deleted", str("1 hour"), long, true, true},
	}
	for _, r := range rows {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO locations (id, tenant_id, name, slug, enabled, last_seen_at, enabled_at, deleted_at)
			VALUES ($1, $2, $3, $3, $4, NOW() - $5::interval, NOW() - $6::interval, CASE WHEN $7 THEN NOW() END)`,
			uuid.New(), tenantID, r.name, r.enabled, r.seenAgo, *r.enabledAgo, r.deleted); err != nil {
			t.Fatal(err)
		}
	}
	res, err := tx.QueryContext(ctx, `
		SELECT l.name, l.enabled, l.deleted_at IS NOT NULL, l.last_seen_at, l.enabled_at,
			`+monitorstate.LocationOfflinePredicate("l")+`, `+monitorstate.LocationDisconnectedPredicate("l")+`, NOW()
		FROM locations l WHERE l.tenant_id = $1`, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Close()
	want := map[string][2]bool{ // offline, disconnected
		"never": {false, false}, "fresh": {false, false},
		"connected_edge": {false, false}, "disconnected_edge": {false, true},
		"offline_under": {false, true}, "offline_edge": {true, true}, "long": {true, true},
		"enabled_under": {false, true}, "enabled_edge": {true, true},
		"disabled": {false, false}, "deleted": {false, false},
	}
	seen := 0
	for res.Next() {
		var name string
		var enabled, isDeleted, sqlOffline, sqlDisconnected bool
		var lastSeen *time.Time
		var enabledAt, now time.Time
		if err := res.Scan(&name, &enabled, &isDeleted, &lastSeen, &enabledAt, &sqlOffline, &sqlDisconnected, &now); err != nil {
			t.Fatal(err)
		}
		goOffline := monitorstate.LocationOffline(enabled, isDeleted, lastSeen, enabledAt, now)
		goDisconnected := monitorstate.LocationDisconnected(enabled, isDeleted, lastSeen, now)
		if goOffline != sqlOffline || goDisconnected != sqlDisconnected {
			t.Fatalf("%s: SQL offline=%v disconnected=%v, Go offline=%v disconnected=%v",
				name, sqlOffline, sqlDisconnected, goOffline, goDisconnected)
		}
		if w := want[name]; w != [2]bool{sqlOffline, sqlDisconnected} {
			t.Fatalf("%s: offline=%v disconnected=%v, want %v", name, sqlOffline, sqlDisconnected, w)
		}
		seen++
	}
	if seen != len(rows) {
		t.Fatalf("checked %d locations, want %d", seen, len(rows))
	}
}

// TenantTargetsSQL is TargetsSQL for a monitor in 'default' mode, minus the
// monitor column: a monitor-less alert reaches exactly the channels a
// monitor following the workspace defaults would.
func TestTenantTargetsSQLMatchesDefaults(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "tenant-targets")
	other := testutil.InsertTenant(ctx, t, dbClient, "tenant-targets-other")
	insertDefaultChannel(ctx, t, dbClient, tenantID, "first", 0)
	insertDefaultChannel(ctx, t, dbClient, tenantID, "second", 300)
	insertDefaultChannel(ctx, t, dbClient, other, "elsewhere", 0)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "follows-defaults")
	mustExec(ctx, t, dbClient, `UPDATE monitors SET notification_mode = 'default' WHERE id = $1`, monitorID)

	type target struct {
		channel uuid.UUID
		delay   int
		pos     int
	}
	read := func(q string, args ...any) []target {
		t.Helper()
		rows, err := dbClient.QueryContext(ctx, `SELECT channel_id, delay_seconds, pos FROM (`+q+`) x ORDER BY pos, channel_id`, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []target
		for rows.Next() {
			var tg target
			if err := rows.Scan(&tg.channel, &tg.delay, &tg.pos); err != nil {
				t.Fatal(err)
			}
			out = append(out, tg)
		}
		return out
	}
	monitorTargets := read(`SELECT channel_id, delay_seconds, pos FROM (`+alertrouting.TargetsSQL()+`) mt`, tenantID, pq.Array([]uuid.UUID{monitorID}))
	tenantTargets := read(alertrouting.TenantTargetsSQL(), tenantID)
	if len(tenantTargets) != 2 || len(monitorTargets) != len(tenantTargets) {
		t.Fatalf("tenant targets %+v, monitor targets %+v", tenantTargets, monitorTargets)
	}
	for i := range tenantTargets {
		if tenantTargets[i] != monitorTargets[i] {
			t.Fatalf("row %d: tenant %+v != monitor %+v", i, tenantTargets[i], monitorTargets[i])
		}
	}
}

// An operator resolves a location alert while the location is still silent:
// the paged channel hears "closed by an operator" (resolve_reason manual,
// set by the API), never "back online", and a new alert opens and pages on
// the next evaluation, as availability alerts do.
func TestLocationOfflineManualResolveReopens(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "manual-resolve")
	insertDefaultChannel(ctx, t, dbClient, tenantID, "ops", 0)
	dead := insertLocationSeen(ctx, t, dbClient, tenantID, "dead", dur(10*time.Minute))

	a := newIntegrationAlerter(dbClient)
	type sent struct {
		event  string
		reason string
	}
	var sends []sent
	a.sendFunc = func(_ context.Context, _ alertChannel, eventType string, _ alertSubject, alert *alertRecord, _ *groupDetail, _ time.Time) error {
		sends = append(sends, sent{eventType, alert.ResolveReason})
		return nil
	}
	if err := a.evaluateLocationOffline(ctx); err != nil {
		t.Fatal(err)
	}
	mustExec(ctx, t, dbClient, `
		UPDATE alerts SET status = 'resolved', resolved_at = NOW(), resolve_reason = 'manual'
		WHERE kind = 'location_offline' AND source_location_id = $1`, dead)
	if err := a.evaluateLocationOffline(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.dispatchPendingRecoveries(ctx); err != nil {
		t.Fatal(err)
	}
	want := []sent{{"created", ""}, {"created", ""}, {"resolved", "manual"}}
	if len(sends) != len(want) {
		t.Fatalf("sends = %+v, want %+v", sends, want)
	}
	for i := range want {
		if sends[i] != want[i] {
			t.Fatalf("sends = %+v, want %+v", sends, want)
		}
	}
	if locationOfflineAlerts(ctx, t, dbClient, dead, "active") != 1 || locationOfflineAlerts(ctx, t, dbClient, dead, "resolved") != 1 {
		t.Fatal("the still-silent location must have a new open alert next to the closed one")
	}
}
