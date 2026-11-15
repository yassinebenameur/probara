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
	mustExec(ctx, t, db, `
		INSERT INTO locations (id, tenant_id, name, slug, mesh_endpoint, enabled, last_seen_at, created_at, updated_at)
		VALUES ($1, $2, $3, $3, $3 || '.mesh.internal:8080', TRUE, $4, NOW(), NOW())
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

// Fleet-silence guard: when no location's heartbeat reaches the platform
// (broker or heartbeat ingest down, database lag), every location looks
// offline at once; no alert opens while at least two recently seen
// locations exist and none is online. A location silent beyond the 7-day
// horizon never opens one, and a single-location fleet still alerts.
func TestLocationOfflineFleetSilenceAndHorizon(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "fleet-silence")
	other := testutil.InsertTenant(ctx, t, dbClient, "fleet-silence-other")
	a := newIntegrationAlerter(dbClient)
	run := func() {
		t.Helper()
		if err := a.evaluateLocationOffline(ctx); err != nil {
			t.Fatalf("evaluateLocationOffline: %v", err)
		}
	}
	east := insertLocationSeen(ctx, t, dbClient, tenantID, "east", dur(10*time.Minute))
	west := insertLocationSeen(ctx, t, dbClient, other, "west", dur(10*time.Minute))
	abandoned := insertLocationSeen(ctx, t, dbClient, tenantID, "abandoned", dur(8*24*time.Hour))
	run()
	for name, id := range map[string]uuid.UUID{"east": east, "west": west, "abandoned": abandoned} {
		if got := locationOfflineAlerts(ctx, t, dbClient, id, "active"); got != 0 {
			t.Fatalf("whole fleet silent: %s opened %d alerts", name, got)
		}
	}
	if hold := a.lastLocationHold; hold != "fleet_silent" {
		t.Fatalf("hold = %q, want fleet_silent", hold)
	}

	// One location reports (another workspace's: the guard is platform-wide
	// because the heartbeat path is), so the silent ones are really offline,
	// except the one beyond the horizon.
	mustExec(ctx, t, dbClient, `UPDATE locations SET last_seen_at = NOW() WHERE id = $1`, west)
	run()
	if locationOfflineAlerts(ctx, t, dbClient, east, "active") != 1 {
		t.Fatal("a silent location in a live fleet must alert")
	}
	if locationOfflineAlerts(ctx, t, dbClient, abandoned, "active") != 0 {
		t.Fatal("a location silent beyond the horizon must not alert")
	}

	// A single-location fleet still alerts: one page is no storm, and a dead
	// site looks the same as a platform fault there.
	mustExec(ctx, t, dbClient, `UPDATE locations SET deleted_at = NOW()`)
	solo := testutil.InsertTenant(ctx, t, dbClient, "solo")
	only := insertLocationSeen(ctx, t, dbClient, solo, "only", dur(10*time.Minute))
	run()
	if locationOfflineAlerts(ctx, t, dbClient, only, "active") != 1 {
		t.Fatal("the only location of the fleet must alert when it goes silent")
	}
}

// Startup guard: an alerter that just started does not trust last_seen_at
// until a heartbeat lands after its start (the heartbeat ingest is writing),
// or one offline threshold has passed.
func TestLocationOfflineHeldAtStartup(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "startup")
	dead := insertLocationSeen(ctx, t, dbClient, tenantID, "dead", dur(10*time.Minute))
	a := newIntegrationAlerter(dbClient)
	a.startedAt = time.Now()
	if err := a.evaluateLocationOffline(ctx); err != nil {
		t.Fatal(err)
	}
	if locationOfflineAlerts(ctx, t, dbClient, dead, "active") != 0 || a.lastLocationHold != "startup" {
		t.Fatalf("a fresh alerter opened an alert before any heartbeat (hold %q)", a.lastLocationHold)
	}

	// A heartbeat after the start proves the ingest writes: no more hold.
	insertLocationSeen(ctx, t, dbClient, tenantID, "alive", dur(0))
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
	b.startedAt = time.Now().Add(-monitorstate.LocationOfflineAfter - time.Second)
	if err := b.evaluateLocationOffline(ctx); err != nil {
		t.Fatal(err)
	}
	if locationOfflineAlerts(ctx, t, dbClient, lone, "active") != 1 {
		t.Fatal("the startup hold must end after one threshold")
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

// LocationOfflinePredicate (SQL) and LocationOffline (Go) are twins (S-F6).
func TestLocationOfflinePredicateMatchesGo(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "loc-twin")
	cases := map[string]*time.Duration{
		"never": nil, "fresh": dur(30 * time.Second), "under": dur(170 * time.Second),
		"over": dur(190 * time.Second), "long": dur(30 * 24 * time.Hour),
	}
	for name, ago := range cases {
		insertLocationSeen(ctx, t, dbClient, tenantID, name, ago)
	}
	disabled := insertLocationSeen(ctx, t, dbClient, tenantID, "disabled", dur(time.Hour))
	mustExec(ctx, t, dbClient, `UPDATE locations SET enabled = FALSE WHERE id = $1`, disabled)
	deleted := insertLocationSeen(ctx, t, dbClient, tenantID, "deleted", dur(time.Hour))
	mustExec(ctx, t, dbClient, `UPDATE locations SET deleted_at = NOW() WHERE id = $1`, deleted)

	rows, err := dbClient.QueryContext(ctx, `
		SELECT l.name, l.enabled, l.deleted_at IS NOT NULL, l.last_seen_at,
			`+monitorstate.LocationOfflinePredicate("l")+`, NOW()
		FROM locations l WHERE l.tenant_id = $1`, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen, offline := 0, 0
	for rows.Next() {
		var name string
		var enabled, isDeleted, sqlOffline bool
		var lastSeen *time.Time
		var now time.Time
		if err := rows.Scan(&name, &enabled, &isDeleted, &lastSeen, &sqlOffline, &now); err != nil {
			t.Fatal(err)
		}
		if goOffline := monitorstate.LocationOffline(enabled, isDeleted, lastSeen, now); goOffline != sqlOffline {
			t.Fatalf("%s: SQL offline=%v, Go offline=%v", name, sqlOffline, goOffline)
		}
		if sqlOffline {
			offline++
		}
		seen++
	}
	if seen != len(cases)+2 || offline != 2 {
		t.Fatalf("checked %d locations (%d offline), want %d (2 offline)", seen, offline, len(cases)+2)
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
