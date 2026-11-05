//go:build !unit

package alerter

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/monitorstate"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// insertLocationSeen seeds a location whose last heartbeat was seenAgo ago
// (nil: never seen).
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

func dur(d time.Duration) *time.Duration { return &d }

// S-F6: an enabled location that reported before and has been silent past
// the tenant threshold opens one location_offline alert (replicas racing
// included) routed to the tenant defaults; never-seen, disabled and
// recently-seen locations open none; the alert resolves when the location
// reports again, and an acknowledgement stops its reminders.
func TestLocationOfflineLifecycle(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "loc-offline")
	mustExec(ctx, t, dbClient, `UPDATE tenants SET alert_reminder_seconds = 300 WHERE id = $1`, tenantID)
	channelID := insertDefaultRoute(ctx, t, dbClient, tenantID, "ops", 0, false)

	dead := insertLocationSeen(ctx, t, dbClient, tenantID, "dead", dur(10*time.Minute))
	fresh := insertLocationSeen(ctx, t, dbClient, tenantID, "fresh", dur(20*time.Second))
	never := insertLocationSeen(ctx, t, dbClient, tenantID, "never", nil)
	disabled := insertLocationSeen(ctx, t, dbClient, tenantID, "disabled", dur(time.Hour))
	mustExec(ctx, t, dbClient, `UPDATE locations SET enabled = FALSE WHERE id = $1`, disabled)
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "via-dead")
	mustExec(ctx, t, dbClient, `INSERT INTO monitor_locations (monitor_id, location_id) VALUES ($1, $2)`, monitorID, dead)

	counter := newSendCounter(50 * time.Millisecond)
	replicas := newReplicaPair(dbClient, counter)
	runConcurrently(t, replicas, func(a *Alerter) error { return a.evaluateLocationOffline(ctx) })

	if got := locationOfflineAlerts(ctx, t, dbClient, dead, "active"); got != 1 {
		t.Fatalf("offline location alerts = %d, want exactly 1 across replicas", got)
	}
	for name, id := range map[string]uuid.UUID{"fresh": fresh, "never": never, "disabled": disabled} {
		if got := locationOfflineAlerts(ctx, t, dbClient, id, "active"); got != 0 {
			t.Fatalf("%s location opened %d alerts", name, got)
		}
	}
	if got := counter.count("created"); got != 1 {
		t.Fatalf("created sends = %d, want 1 (tenant default route, once)", got)
	}
	var alertID uuid.UUID
	var monitorIDCol *uuid.UUID
	if err := dbClient.QueryRowContext(ctx, `
		SELECT id, monitor_id FROM alerts WHERE kind = 'location_offline' AND source_location_id = $1
	`, dead).Scan(&alertID, &monitorIDCol); err != nil || monitorIDCol != nil {
		t.Fatalf("alert subject: monitor_id=%v err=%v", monitorIDCol, err)
	}
	if _, _, ok := notificationStateFor(ctx, t, dbClient, alertID, channelID); !ok {
		t.Fatal("tenant default route has no created claim")
	}

	// Acknowledged: no reminder however long it stays offline.
	mustExec(ctx, t, dbClient, `UPDATE alerts SET status = 'acknowledged', acknowledged_at = NOW() WHERE id = $1`, alertID)
	mustExec(ctx, t, dbClient, `UPDATE alert_notification_states SET last_sent_at = NOW() - INTERVAL '1 hour' WHERE alert_id = $1`, alertID)
	runConcurrently(t, replicas, func(a *Alerter) error { return a.evaluateLocationOffline(ctx) })
	if got := counter.count("reminder"); got != 0 {
		t.Fatalf("reminders on an acknowledged offline alert = %d, want 0", got)
	}

	// The location reports again: resolved, and the paged route hears it.
	mustExec(ctx, t, dbClient, `UPDATE locations SET last_seen_at = NOW() WHERE id = $1`, dead)
	runConcurrently(t, replicas, func(a *Alerter) error { return a.evaluateLocationOffline(ctx) })
	if got := locationOfflineAlerts(ctx, t, dbClient, dead, "resolved"); got != 1 {
		t.Fatalf("resolved offline alerts = %d, want 1", got)
	}
	if got := counter.count("resolved"); got != 1 {
		t.Fatalf("resolved sends = %d, want 1", got)
	}
}

// Disabling, deleting, or turning the tenant's offline alerts off resolves
// an open alert; a tenant with them off never opens one.
func TestLocationOfflineResolvesWhenNoLongerApplicable(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "loc-offline-resolve")
	a, _ := newMeshAlerter(dbClient)
	run := func() {
		t.Helper()
		if err := a.evaluateLocationOffline(ctx); err != nil {
			t.Fatalf("evaluateLocationOffline: %v", err)
		}
	}

	disabledLater := insertLocationSeen(ctx, t, dbClient, tenantID, "disabled-later", dur(time.Hour))
	deletedLater := insertLocationSeen(ctx, t, dbClient, tenantID, "deleted-later", dur(time.Hour))
	run()
	if locationOfflineAlerts(ctx, t, dbClient, disabledLater, "active") != 1 || locationOfflineAlerts(ctx, t, dbClient, deletedLater, "active") != 1 {
		t.Fatal("both silent locations should alert")
	}
	mustExec(ctx, t, dbClient, `UPDATE locations SET enabled = FALSE WHERE id = $1`, disabledLater)
	mustExec(ctx, t, dbClient, `UPDATE locations SET deleted_at = NOW() WHERE id = $1`, deletedLater)
	run()
	if locationOfflineAlerts(ctx, t, dbClient, disabledLater, "resolved") != 1 || locationOfflineAlerts(ctx, t, dbClient, deletedLater, "resolved") != 1 {
		t.Fatal("disabled and deleted locations must resolve their alerts")
	}

	// Threshold: 10 minutes; a location silent for 5 is not offline yet.
	other := testutil.InsertTenant(ctx, t, dbClient, "loc-offline-threshold")
	mustExec(ctx, t, dbClient, `UPDATE tenants SET location_offline_after_seconds = 600 WHERE id = $1`, other)
	quiet := insertLocationSeen(ctx, t, dbClient, other, "quiet", dur(5*time.Minute))
	run()
	if locationOfflineAlerts(ctx, t, dbClient, quiet, "active") != 0 {
		t.Fatal("location under the tenant threshold alerted")
	}
	mustExec(ctx, t, dbClient, `UPDATE locations SET last_seen_at = NOW() - INTERVAL '11 minutes' WHERE id = $1`, quiet)
	run()
	if locationOfflineAlerts(ctx, t, dbClient, quiet, "active") != 1 {
		t.Fatal("location past the tenant threshold did not alert")
	}
	mustExec(ctx, t, dbClient, `UPDATE tenants SET location_offline_alerts_enabled = FALSE WHERE id = $1`, other)
	run()
	if locationOfflineAlerts(ctx, t, dbClient, quiet, "resolved") != 1 {
		t.Fatal("turning the tenant's offline alerts off must resolve open ones")
	}
	run()
	if locationOfflineAlerts(ctx, t, dbClient, quiet, "active") != 0 {
		t.Fatal("a tenant with offline alerts off opened one")
	}
}

// A mesh edge whose endpoint location is offline stays open but sends
// nothing: the location's own alert speaks for it.
func TestMeshEdgeOfOfflineLocationIsQuiet(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "mesh-offline")
	insertDefaultRoute(ctx, t, dbClient, tenantID, "ops", 0, false)
	alive := insertLocationSeen(ctx, t, dbClient, tenantID, "alive", dur(10*time.Second))
	dead := insertLocationSeen(ctx, t, dbClient, tenantID, "dead", dur(time.Hour))
	insertMeshEdgeState(ctx, t, dbClient, tenantID, alive, dead, "down", 3, "dial tcp: i/o timeout")

	a, spy := newMeshAlerter(dbClient)
	if err := a.evaluateMeshEdges(ctx); err != nil {
		t.Fatal(err)
	}
	if countMeshEdgeAlerts(ctx, t, dbClient, alive, dead, "active") != 1 {
		t.Fatal("the mesh edge alert must still open")
	}
	if got := len(spy.byType("created")); got != 0 {
		t.Fatalf("mesh edge to an offline location paged %d times", got)
	}

	// Once the location reports again (edge still down) the edge pages.
	mustExec(ctx, t, dbClient, `UPDATE locations SET last_seen_at = NOW() WHERE id = $1`, dead)
	if err := a.evaluateMeshEdges(ctx); err != nil {
		t.Fatal(err)
	}
	if got := len(spy.byType("created")); got != 1 {
		t.Fatalf("mesh edge after the location came back: created = %d, want 1", got)
	}
}

// LocationOfflinePredicate (SQL) and LocationOffline (Go) are twins.
func TestLocationOfflinePredicateMatchesGo(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "loc-twin")
	mustExec(ctx, t, dbClient, `UPDATE tenants SET location_offline_after_seconds = 120 WHERE id = $1`, tenantID)
	cases := map[string]*time.Duration{
		"never": nil, "fresh": dur(30 * time.Second), "under": dur(100 * time.Second),
		"over": dur(130 * time.Second), "long": dur(time.Hour),
	}
	for name, ago := range cases {
		insertLocationSeen(ctx, t, dbClient, tenantID, name, ago)
	}
	disabled := insertLocationSeen(ctx, t, dbClient, tenantID, "disabled", dur(time.Hour))
	mustExec(ctx, t, dbClient, `UPDATE locations SET enabled = FALSE WHERE id = $1`, disabled)

	rows, err := dbClient.QueryContext(ctx, `
		SELECT l.name, l.enabled, l.deleted_at IS NOT NULL, l.last_seen_at, te.location_offline_after_seconds,
			`+monitorstate.LocationOfflinePredicate("l", "te")+`, NOW()
		FROM locations l JOIN tenants te ON te.id = l.tenant_id
		WHERE l.tenant_id = $1`, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var name string
		var enabled, deleted, sqlOffline bool
		var lastSeen *time.Time
		var after int
		var now time.Time
		if err := rows.Scan(&name, &enabled, &deleted, &lastSeen, &after, &sqlOffline, &now); err != nil {
			t.Fatal(err)
		}
		if goOffline := monitorstate.LocationOffline(enabled, deleted, lastSeen, after, now); goOffline != sqlOffline {
			t.Fatalf("%s: SQL offline=%v, Go offline=%v", name, sqlOffline, goOffline)
		}
		seen++
	}
	if seen != len(cases)+1 {
		t.Fatalf("checked %d locations, want %d", seen, len(cases)+1)
	}
}
