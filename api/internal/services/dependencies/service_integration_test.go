package dependencies_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	depsvc "github.com/yassinebenameur/probara/api/internal/services/dependencies"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestSetDependenciesRoundTripAndReplace(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "deps")
	api := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	postgres := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "Postgres")
	redis := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "Redis")
	svc := depsvc.NewService(dbClient)

	// Duplicates in the request collapse to one edge.
	require.NoError(t, svc.SetDependencies(ctx, tenantID, api, []uuid.UUID{postgres, redis, postgres}))
	deps, err := svc.GetDependencies(ctx, tenantID, api)
	require.NoError(t, err)
	require.Len(t, deps, 2)

	dependents, err := svc.GetDependents(ctx, tenantID, postgres)
	require.NoError(t, err)
	require.Len(t, dependents, 1)
	require.Equal(t, api, dependents[0].ID)

	// Full replace: dropping redis keeps only postgres.
	require.NoError(t, svc.SetDependencies(ctx, tenantID, api, []uuid.UUID{postgres}))
	deps, err = svc.GetDependencies(ctx, tenantID, api)
	require.NoError(t, err)
	require.Len(t, deps, 1)
	require.Equal(t, postgres, deps[0].ID)

	// Empty replace clears everything.
	require.NoError(t, svc.SetDependencies(ctx, tenantID, api, nil))
	deps, err = svc.GetDependencies(ctx, tenantID, api)
	require.NoError(t, err)
	require.Empty(t, deps)
}

func TestSetDependenciesRejectsCycles(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "deps")
	a := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "A")
	b := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "B")
	c := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "C")
	svc := depsvc.NewService(dbClient)

	require.ErrorIs(t, svc.SetDependencies(ctx, tenantID, a, []uuid.UUID{a}),
		depsvc.ErrDependencyCycle, "self-dependency")

	// B depends on A; adding A -> B is a direct cycle.
	require.NoError(t, svc.SetDependencies(ctx, tenantID, b, []uuid.UUID{a}))
	require.ErrorIs(t, svc.SetDependencies(ctx, tenantID, a, []uuid.UUID{b}),
		depsvc.ErrDependencyCycle, "direct cycle")

	// C depends on B (which depends on A); adding A -> C is a transitive cycle.
	require.NoError(t, svc.SetDependencies(ctx, tenantID, c, []uuid.UUID{b}))
	require.ErrorIs(t, svc.SetDependencies(ctx, tenantID, a, []uuid.UUID{c}),
		depsvc.ErrDependencyCycle, "transitive cycle")

	// A failed set must not leave partial edges behind.
	deps, err := svc.GetDependencies(ctx, tenantID, a)
	require.NoError(t, err)
	require.Empty(t, deps)

	// Replacing a set with reordered-but-acyclic edges must not false-positive.
	require.NoError(t, svc.SetDependencies(ctx, tenantID, c, []uuid.UUID{a, b}))
}

func TestAddAndRemoveSingleDependency(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "deps")
	otherTenant := testutil.InsertTenant(ctx, t, dbClient, "other")
	api := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	postgres := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "Postgres")
	foreign := testutil.InsertHTTPMonitor(ctx, t, dbClient, otherTenant, "Foreign")
	svc := depsvc.NewService(dbClient)

	// Add, then idempotent re-add.
	require.NoError(t, svc.AddDependency(ctx, tenantID, api, postgres))
	require.NoError(t, svc.AddDependency(ctx, tenantID, api, postgres))
	deps, err := svc.GetDependencies(ctx, tenantID, api)
	require.NoError(t, err)
	require.Len(t, deps, 1)

	// Cycle and tenant guards.
	require.ErrorIs(t, svc.AddDependency(ctx, tenantID, postgres, api), depsvc.ErrDependencyCycle)
	require.ErrorIs(t, svc.AddDependency(ctx, tenantID, api, api), depsvc.ErrDependencyCycle)
	require.ErrorIs(t, svc.AddDependency(ctx, tenantID, api, foreign), depsvc.ErrMonitorNotFound)

	// Remove, then idempotent re-remove.
	require.NoError(t, svc.RemoveDependency(ctx, tenantID, api, postgres))
	require.NoError(t, svc.RemoveDependency(ctx, tenantID, api, postgres))
	deps, err = svc.GetDependencies(ctx, tenantID, api)
	require.NoError(t, err)
	require.Empty(t, deps)

	// Cross-tenant remove must not touch the edge.
	require.NoError(t, svc.AddDependency(ctx, tenantID, api, postgres))
	require.NoError(t, svc.RemoveDependency(ctx, otherTenant, api, postgres))
	deps, err = svc.GetDependencies(ctx, tenantID, api)
	require.NoError(t, err)
	require.Len(t, deps, 1, "edge must survive a cross-tenant delete attempt")
}

func TestSetDependenciesRejectsCrossTenantAndUnknownTargets(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantA := testutil.InsertTenant(ctx, t, dbClient, "tenant-a")
	tenantB := testutil.InsertTenant(ctx, t, dbClient, "tenant-b")
	monitorA := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantA, "A")
	monitorB := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantB, "B")
	svc := depsvc.NewService(dbClient)

	require.ErrorIs(t, svc.SetDependencies(ctx, tenantA, monitorA, []uuid.UUID{monitorB}),
		depsvc.ErrMonitorNotFound, "cross-tenant dependency")
	require.ErrorIs(t, svc.SetDependencies(ctx, tenantA, monitorA, []uuid.UUID{uuid.New()}),
		depsvc.ErrMonitorNotFound, "unknown dependency target")
	require.ErrorIs(t, svc.SetDependencies(ctx, tenantA, monitorB, []uuid.UUID{monitorA}),
		depsvc.ErrMonitorNotFound, "monitor from another tenant")
}

func TestGetDependencyGraph(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "deps")
	otherTenant := testutil.InsertTenant(ctx, t, dbClient, "other")
	api := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	postgres := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "Postgres")
	lonely := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "Lonely")
	foreignAPI := testutil.InsertHTTPMonitor(ctx, t, dbClient, otherTenant, "Foreign API")
	foreignDB := testutil.InsertHTTPMonitor(ctx, t, dbClient, otherTenant, "Foreign DB")
	svc := depsvc.NewService(dbClient)

	require.NoError(t, svc.SetDependencies(ctx, tenantID, api, []uuid.UUID{postgres}))
	require.NoError(t, svc.SetDependencies(ctx, otherTenant, foreignAPI, []uuid.UUID{foreignDB}))

	graph, err := svc.GetDependencyGraph(ctx, tenantID)
	require.NoError(t, err)
	require.Len(t, graph.Edges, 1, "only this tenant's edges")
	require.Equal(t, api, graph.Edges[0].From)
	require.Equal(t, postgres, graph.Edges[0].To)

	require.Len(t, graph.Nodes, 2, "only monitors participating in an edge")
	nodeIDs := map[uuid.UUID]bool{}
	for _, n := range graph.Nodes {
		nodeIDs[n.ID] = true
	}
	require.True(t, nodeIDs[api])
	require.True(t, nodeIDs[postgres])
	require.False(t, nodeIDs[lonely])
}
