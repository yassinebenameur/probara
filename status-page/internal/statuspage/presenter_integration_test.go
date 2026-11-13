//go:build !unit

package statuspage

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestService_GetStatusPageMonitors_GroupPresenterAggregatesNestedGroups(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	analyticsRepo := sharedanalytics.NewRepository(dbClient)
	svc := NewService(dbClient, analyticsRepo)
	now := time.Now().UTC()
	svc.now = func() time.Time { return now }

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "status-group")
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "group-status", "Group Status")
	parentGroupID := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "parent")
	childGroupID := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "child")
	monitorA := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "leaf-a")
	monitorB := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "leaf-b")

	testutil.AddMonitorToGroup(ctx, t, dbClient, monitorA, childGroupID)
	testutil.AddMonitorToGroup(ctx, t, dbClient, monitorB, parentGroupID)
	testutil.AddMonitorToGroup(ctx, t, dbClient, childGroupID, parentGroupID)
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, parentGroupID, 0)

	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorA, now.Add(-5*time.Minute), "success", "monitor", testutil.IntPtr(120))
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorB, now.Add(-2*time.Minute), "failure", "monitor", nil)
	// The group's status comes from its members' monitor-level states (S-G3),
	// not from their newest check rows.
	setMonitorStateForTest(ctx, t, dbClient, monitorA, "up")
	setMonitorStateForTest(ctx, t, dbClient, monitorB, "down")

	monitors, err := svc.GetStatusPageMonitors(ctx, statusPageID, tenantID)
	if err != nil {
		t.Fatalf("GetStatusPageMonitors() error = %v", err)
	}
	if len(monitors) != 1 {
		t.Fatalf("monitor count = %d, want 1", len(monitors))
	}
	if monitors[0].URL != "Group Monitor" {
		t.Fatalf("group URL = %q, want Group Monitor", monitors[0].URL)
	}
	if monitors[0].Status != "degraded" {
		t.Fatalf("group status = %q, want degraded", monitors[0].Status)
	}
}

func TestService_ResolveStatusPageOperationalMonitorIDs_GroupPresenterReturnsLeaves(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient))
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "status-operational")
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "operational-status", "Operational Status")
	parentGroupID := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "parent")
	childGroupID := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "child")
	leafA := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "leaf-a")
	leafB := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "leaf-b")

	testutil.AddMonitorToGroup(ctx, t, dbClient, leafA, childGroupID)
	testutil.AddMonitorToGroup(ctx, t, dbClient, childGroupID, parentGroupID)
	testutil.AddMonitorToGroup(ctx, t, dbClient, leafB, parentGroupID)
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, parentGroupID, 0)

	ids, err := svc.resolveStatusPageOperationalMonitorIDs(ctx, statusPageID, tenantID)
	if err != nil {
		t.Fatalf("resolveStatusPageOperationalMonitorIDs() error = %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("operational monitor count = %d, want 2", len(ids))
	}
	want := map[uuid.UUID]bool{leafA: true, leafB: true}
	for _, id := range ids {
		if !want[id] {
			t.Fatalf("unexpected operational ID %s", id)
		}
	}
}

func setMonitorStateForTest(ctx context.Context, t *testing.T, dbClient *shareddb.Client, monitorID uuid.UUID, state string) {
	t.Helper()
	if _, err := dbClient.ExecContext(ctx, `UPDATE monitors SET current_state = $2 WHERE id = $1`, monitorID, state); err != nil {
		t.Fatalf("set monitor state: %v", err)
	}
}

// S-G3 on the public page: a group shows the display view of its members'
// states. A failed check row that did not change the member's state (below
// the failure threshold, or one location below quorum) must not show, and a
// paused member takes no part.
func TestGroupStatusFollowsMemberStatesNotCheckRows(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient))
	now := time.Now().UTC()
	svc.now = func() time.Time { return now }

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "status-group-states")
	groupID := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "group")
	leafA := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "leaf-a")
	leafB := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "leaf-b")
	testutil.AddMonitorToGroup(ctx, t, dbClient, leafA, groupID)
	testutil.AddMonitorToGroup(ctx, t, dbClient, leafB, groupID)
	members := []uuid.UUID{leafA, leafB}

	status := func() string {
		t.Helper()
		got, err := svc.GetGroupAggregatedStatus(ctx, groupID.String(), members, tenantID)
		if err != nil {
			t.Fatalf("GetGroupAggregatedStatus: %v", err)
		}
		return got.Status
	}
	set := func(a, b string) {
		setMonitorStateForTest(ctx, t, dbClient, leafA, a)
		setMonitorStateForTest(ctx, t, dbClient, leafB, b)
	}

	// Newest row of B is a failure, but B's state machine still says up.
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, leafA, now.Add(-2*time.Minute), "success", "monitor", testutil.IntPtr(100))
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, leafB, now.Add(-time.Minute), "failure", "monitor", nil)
	set("up", "up")
	if got := status(); got != "up" {
		t.Fatalf("both members up with a stray failed row: status = %q, want up", got)
	}

	for _, tc := range []struct{ a, b, want string }{
		{"up", "suspect", "up"},
		{"up", "degraded", "degraded"},
		{"up", "down", "degraded"},
		{"down", "down", "down"},
		{"up", "unknown", "up"},
		{"unknown", "unknown", "unknown"},
	} {
		set(tc.a, tc.b)
		if got := status(); got != tc.want {
			t.Errorf("members %s + %s: status = %q, want %q", tc.a, tc.b, got, tc.want)
		}
	}

	// Pausing the down member leaves only the up one.
	set("up", "down")
	if _, err := dbClient.ExecContext(ctx, `UPDATE monitors SET enabled = FALSE WHERE id = $1`, leafB); err != nil {
		t.Fatal(err)
	}
	if got := status(); got != "up" {
		t.Errorf("down member paused: status = %q, want up", got)
	}
}
