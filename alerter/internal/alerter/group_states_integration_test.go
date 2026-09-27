package alerter

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestGroupStatesRefreshNestedLeavesInOneTick(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "nested groups")
	parent := testutil.InsertGroupMonitor(ctx, t, db, tenantID, "parent")
	child := testutil.InsertGroupMonitor(ctx, t, db, tenantID, "child")
	leaf := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "leaf")
	testutil.AddMonitorToGroup(ctx, t, db, child, parent)
	testutil.AddMonitorToGroup(ctx, t, db, leaf, child)
	// A shared leaf must not change aggregation, and an accidentally raced
	// cycle must neither hang evaluation nor depend on cached group states.
	testutil.AddMonitorToGroup(ctx, t, db, leaf, parent)
	testutil.AddMonitorToGroup(ctx, t, db, parent, child)
	a := newIntegrationAlerter(db)
	for _, state := range []string{"down", "up", "unknown", "degraded", "suspect"} {
		setStateForTest(ctx, t, db, leaf, state, time.Now())
		if err := a.refreshGroupStates(ctx); err != nil {
			t.Fatal(err)
		}
		want := state
		if state == "degraded" || state == "suspect" {
			want = "up"
		}
		for _, id := range []uuid.UUID{parent, child} {
			var got string
			if err := db.QueryRowContext(ctx, `SELECT current_state FROM monitors WHERE id=$1`, id).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("leaf %s: group state=%s, want %s", state, got, want)
			}
		}
	}
}

func TestGroupStatesClearEmptyGroupOutages(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "empty groups")
	a := newIntegrationAlerter(db)
	for _, operation := range []string{"remove", "pause", "delete", "pause nested group"} {
		t.Run(operation, func(t *testing.T) {
			group := testutil.InsertGroupMonitor(ctx, t, db, tenantID, operation)
			leaf := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, operation+" leaf")
			child := testutil.InsertGroupMonitor(ctx, t, db, tenantID, operation+" child")
			testutil.AddMonitorToGroup(ctx, t, db, child, group)
			testutil.AddMonitorToGroup(ctx, t, db, leaf, child)
			mustExec(ctx, t, db, `UPDATE monitors SET member_alert_rollup='group' WHERE id=$1`, group)
			setStateForTest(ctx, t, db, leaf, "down", time.Now())
			if err := a.runLifecycle(ctx); err != nil {
				t.Fatal(err)
			}
			if got := countAlerterAlertsByStatus(ctx, t, db, group, "active"); got != 1 {
				t.Fatalf("active alerts=%d, want 1", got)
			}
			switch operation {
			case "remove":
				mustExec(ctx, t, db, `DELETE FROM monitor_groups WHERE monitor_id=$1`, leaf)
			case "pause":
				mustExec(ctx, t, db, `UPDATE monitors SET enabled=FALSE WHERE id=$1`, leaf)
			case "delete":
				mustExec(ctx, t, db, `UPDATE monitors SET deleted_at=NOW() WHERE id=$1`, leaf)
			case "pause nested group":
				mustExec(ctx, t, db, `UPDATE monitors SET enabled=FALSE WHERE id=$1`, child)
			}
			if err := a.runLifecycle(ctx); err != nil {
				t.Fatal(err)
			}
			var state string
			if err := db.QueryRowContext(ctx, `SELECT current_state FROM monitors WHERE id=$1`, group).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if state != "unknown" {
				t.Fatalf("state=%s, want unknown", state)
			}
			if got := countAlerterAlertsByStatus(ctx, t, db, group, "active"); got != 0 {
				t.Fatalf("empty group retains %d active alerts", got)
			}
		})
	}
}
