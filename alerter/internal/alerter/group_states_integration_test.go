//go:build !unit

package alerter

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/monitorstate"
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

// S-G2: the SQL that stores a group's state and monitorstate's AlertState are
// one rule. Every combination of two member states (plus a paused member and
// an empty group) must give the same answer through both.
func TestGroupAlertStateSQLMatchesSummary(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "group parity")
	a := newIntegrationAlerter(db)

	states := []monitorstate.State{
		monitorstate.StateUp, monitorstate.StateSuspect, monitorstate.StateDegraded,
		monitorstate.StateDown, monitorstate.StateUnknown,
	}
	type fixture struct {
		group   uuid.UUID
		members []monitorstate.State
	}
	var fixtures []fixture
	add := func(name string, members ...monitorstate.State) {
		group := testutil.InsertGroupMonitor(ctx, t, db, tenantID, name)
		for i, state := range members {
			leaf := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, fmt.Sprintf("%s leaf %d", name, i))
			testutil.AddMonitorToGroup(ctx, t, db, leaf, group)
			setStateForTest(ctx, t, db, leaf, string(state), time.Now())
		}
		fixtures = append(fixtures, fixture{group: group, members: members})
	}
	add("empty")
	for _, first := range states {
		add("one "+string(first), first)
		for _, second := range states {
			add(string(first)+"+"+string(second), first, second)
		}
	}

	// A paused member takes no part: a down member that is disabled must not
	// take the group down.
	paused := testutil.InsertGroupMonitor(ctx, t, db, tenantID, "paused member")
	upLeaf := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "paused member up leaf")
	downLeaf := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "paused member down leaf")
	testutil.AddMonitorToGroup(ctx, t, db, upLeaf, paused)
	testutil.AddMonitorToGroup(ctx, t, db, downLeaf, paused)
	setStateForTest(ctx, t, db, upLeaf, "up", time.Now())
	setStateForTest(ctx, t, db, downLeaf, "down", time.Now())
	if _, err := db.ExecContext(ctx, `UPDATE monitors SET enabled = FALSE WHERE id = $1`, downLeaf); err != nil {
		t.Fatal(err)
	}
	fixtures = append(fixtures, fixture{group: paused, members: []monitorstate.State{monitorstate.StateUp}})

	if err := a.refreshGroupStates(ctx); err != nil {
		t.Fatal(err)
	}
	for _, f := range fixtures {
		var got string
		if err := db.QueryRowContext(ctx, `SELECT COALESCE(current_state, 'unknown') FROM monitors WHERE id = $1`, f.group).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if want := monitorstate.SummarizeGroup(f.members).AlertState(); got != string(want) {
			t.Errorf("members %v: SQL stored %q, AlertState is %q", f.members, got, want)
		}

		// The shared loader must see the same members the alerter aggregated.
		summaries, err := monitorstate.LoadGroupSummaries(ctx, db, tenantID, []uuid.UUID{f.group})
		if err != nil {
			t.Fatal(err)
		}
		if got, want := summaries[f.group], monitorstate.SummarizeGroup(f.members); got != want {
			t.Errorf("members %v: LoadGroupSummaries gave %+v, want %+v", f.members, got, want)
		}
	}
}
