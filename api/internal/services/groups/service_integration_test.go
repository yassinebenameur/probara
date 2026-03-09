package groups

import (
	"context"
	"errors"
	"testing"
	"time"

	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestService_AddMonitorsToGroup_AllowsNestingAndRejectsCycles(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	svc := NewService(dbClient)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "groups-cycles")
	parentGroupID := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "parent")
	childGroupID := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "child")
	leafMonitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "leaf")

	if err := svc.AddMonitorsToGroup(ctx, tenantID, childGroupID, []uuid.UUID{leafMonitorID}); err != nil {
		t.Fatalf("AddMonitorsToGroup(child <- leaf) error = %v", err)
	}
	if err := svc.AddMonitorsToGroup(ctx, tenantID, parentGroupID, []uuid.UUID{childGroupID}); err != nil {
		t.Fatalf("AddMonitorsToGroup(parent <- child) error = %v", err)
	}

	err := svc.AddMonitorsToGroup(ctx, tenantID, childGroupID, []uuid.UUID{parentGroupID})
	if !errors.Is(err, ErrGroupCycleDetected) {
		t.Fatalf("AddMonitorsToGroup(child <- parent) error = %v, want ErrGroupCycleDetected", err)
	}
}

func TestService_GetGroupMembersAndLeafMembers(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	svc := NewService(dbClient)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "groups-members")
	parentGroupID := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "parent")
	childGroupID := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "child")
	leafA := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "leaf-a")
	leafB := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "leaf-b")

	if err := svc.AddMonitorsToGroup(ctx, tenantID, childGroupID, []uuid.UUID{leafA}); err != nil {
		t.Fatalf("AddMonitorsToGroup(child <- leafA) error = %v", err)
	}
	if err := svc.AddMonitorsToGroup(ctx, tenantID, parentGroupID, []uuid.UUID{childGroupID, leafB}); err != nil {
		t.Fatalf("AddMonitorsToGroup(parent <- child,leafB) error = %v", err)
	}

	directMembers, err := svc.GetGroupMembers(ctx, tenantID, parentGroupID)
	if err != nil {
		t.Fatalf("GetGroupMembers() error = %v", err)
	}
	if len(directMembers) != 2 {
		t.Fatalf("GetGroupMembers() length = %d, want 2", len(directMembers))
	}

	leafMembers, err := svc.GetGroupLeafMembers(ctx, tenantID, parentGroupID)
	if err != nil {
		t.Fatalf("GetGroupLeafMembers() error = %v", err)
	}
	if len(leafMembers) != 2 {
		t.Fatalf("GetGroupLeafMembers() length = %d, want 2", len(leafMembers))
	}
	for _, member := range leafMembers {
		if member.Type == "group" {
			t.Fatalf("GetGroupLeafMembers() returned nested group %s", member.ID)
		}
	}
}

func TestService_GetGroupStatus_EmptyIsUnknownAndMixedIsDegraded(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	svc := NewService(dbClient)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "groups-status")
	emptyGroupID := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "empty")
	groupID := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "status")
	monitorA := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-a")
	monitorB := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-b")

	status, err := svc.GetGroupStatus(ctx, tenantID, emptyGroupID)
	if err != nil {
		t.Fatalf("GetGroupStatus(empty) error = %v", err)
	}
	if status != "unknown" {
		t.Fatalf("GetGroupStatus(empty) = %s, want unknown", status)
	}

	if err := svc.AddMonitorsToGroup(ctx, tenantID, groupID, []uuid.UUID{monitorA, monitorB}); err != nil {
		t.Fatalf("AddMonitorsToGroup(status <- leaves) error = %v", err)
	}

	now := time.Now().UTC()
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorA, now.Add(-5*time.Minute), "success", "monitor", testutil.IntPtr(120))
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorB, now.Add(-2*time.Minute), "failure", "monitor", nil)

	status, err = svc.GetGroupStatus(ctx, tenantID, groupID)
	if err != nil {
		t.Fatalf("GetGroupStatus(mixed) error = %v", err)
	}
	if status != "degraded" {
		t.Fatalf("GetGroupStatus(mixed) = %s, want degraded", status)
	}
}
