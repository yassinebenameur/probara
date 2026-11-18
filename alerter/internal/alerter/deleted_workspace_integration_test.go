//go:build !unit

package alerter

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/testutil"
)

// A deleted workspace is neither evaluated nor notified: a down monitor opens
// no alert, an open alert is neither resolved nor paged (it stays as it is
// until the purge removes it), and the notification claim refuses it even if
// a pre-filter loaded it before the delete committed.
func TestLifecycleIgnoresDeletedWorkspace(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	live := testutil.InsertTenant(ctx, t, dbClient, "live")
	deleted := testutil.InsertTenant(ctx, t, dbClient, "deleted")
	routed := func(tenant uuid.UUID) uuid.UUID {
		ch := insertTestChannel(ctx, t, dbClient, tenant, "hook-"+tenant.String()[:8])
		mustExec(ctx, t, dbClient, `INSERT INTO tenant_default_channels (tenant_id, channel_id, delay_seconds, position) VALUES ($1, $2, 0, 0)`, tenant, ch)
		return ch
	}
	routed(live)
	deletedChannel := routed(deleted)

	liveDown := testutil.InsertHTTPMonitor(ctx, t, dbClient, live, "live-down")
	newlyDown := testutil.InsertHTTPMonitor(ctx, t, dbClient, deleted, "newly-down")
	stillDown := testutil.InsertHTTPMonitor(ctx, t, dbClient, deleted, "still-down")
	recovered := testutil.InsertHTTPMonitor(ctx, t, dbClient, deleted, "recovered")
	now := time.Now().UTC()
	setStateForTest(ctx, t, dbClient, liveDown, "down", now)
	setStateForTest(ctx, t, dbClient, newlyDown, "down", now)
	setStateForTest(ctx, t, dbClient, stillDown, "down", now)
	setStateForTest(ctx, t, dbClient, recovered, "up", now)
	openAlert := func(monitor uuid.UUID) uuid.UUID {
		id := uuid.New()
		mustExec(ctx, t, dbClient, `
			INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, failure_count, triggered_at)
			VALUES ($1, $2, $3, 'availability', 'active', 3, NOW())`, id, deleted, monitor)
		return id
	}
	pageable := openAlert(stillDown)
	openAlert(recovered)
	mustExec(ctx, t, dbClient, `UPDATE tenants SET deleted_at = NOW() WHERE id = $1`, deleted)

	a := newIntegrationAlerter(dbClient)
	var mu sync.Mutex
	sentTo := map[uuid.UUID]int{}
	a.sendFunc = func(_ context.Context, _ alertChannel, _ string, binding alertSubject, _ *alertRecord, _ *groupDetail, _ time.Time) error {
		mu.Lock()
		defer mu.Unlock()
		sentTo[binding.TenantID]++
		return nil
	}
	for i := 0; i < 2; i++ {
		if err := a.runLifecycle(ctx); err != nil {
			t.Fatalf("runLifecycle: %v", err)
		}
	}

	if got := countAlerterAlertsByStatus(ctx, t, dbClient, liveDown, "active"); got != 1 {
		t.Fatalf("live workspace: active alerts = %d, want 1", got)
	}
	if sentTo[live] == 0 {
		t.Fatal("live workspace was not notified")
	}
	if got := countAlerterAlertsByStatus(ctx, t, dbClient, newlyDown, "active"); got != 0 {
		t.Fatalf("deleted workspace: a down monitor opened %d alerts", got)
	}
	if got := countAlerterAlertsByStatus(ctx, t, dbClient, recovered, "active"); got != 1 {
		t.Fatal("deleted workspace: an open alert of a recovered monitor must be left open, not resolved")
	}
	if got := countAlerterAlertsByStatus(ctx, t, dbClient, stillDown, "active"); got != 1 {
		t.Fatal("deleted workspace: the open alert must be left as it is")
	}
	if sentTo[deleted] != 0 || notificationStateExists(ctx, t, dbClient, stillDown, deletedChannel) {
		t.Fatalf("deleted workspace was notified (%d sends)", sentTo[deleted])
	}

	// The claim is the last gate: a send for this alert, whatever loaded
	// it, goes nowhere.
	sent, err := a.deliverNotification(ctx, "created",
		alertSubject{MonitorID: stillDown, TenantID: deleted, MonitorName: "still-down"},
		&alertRecord{ID: pageable, TenantID: deleted, MonitorID: stillDown, Kind: "availability", Status: "active"},
		nil, alertChannel{ID: deletedChannel, Name: "hook", Type: "webhook", IsActive: true}, time.Now(), time.Hour)
	if err != nil || sent {
		t.Fatalf("deliverNotification = %v, %v; want no send", sent, err)
	}
	if sentTo[deleted] != 0 {
		t.Fatal("the claim let a deleted workspace's notification through")
	}
}
