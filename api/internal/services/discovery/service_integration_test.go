//go:build !unit

package discovery

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/yassinebenameur/probara/shared/db"
	contract "github.com/yassinebenameur/probara/shared/discovery"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestDiscoveryLifecycle(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, db, "discovery")
	other := testutil.InsertTenant(ctx, t, db, "other")
	s := New(db)
	s.minSnapshotInterval = 0
	i, err := s.Create(ctx, tenant, CreateRequest{Name: "production", Namespaces: []string{"prod"}})
	require.NoError(t, err)
	require.Len(t, i.Token, 43)
	_, err = s.CollectorConfig(ctx, i.ID, "wrong")
	require.ErrorIs(t, err, ErrNotFound)
	cfg, err := s.CollectorConfig(ctx, i.ID, i.Token)
	require.NoError(t, err)
	resource := contract.Resource{UID: "web-1", Kind: "Ingress", Name: "web", Namespace: "prod", Suggestions: []contract.Suggestion{{Type: "http", Target: "https://example.com/", Reason: "Ingress hostname"}}}
	snapshot := contract.Snapshot{Revision: cfg.Revision, Namespaces: cfg.Namespaces, Resources: []contract.Resource{resource}}
	require.NoError(t, s.Snapshot(ctx, i.ID, i.Token, snapshot))
	require.ErrorIs(t, s.Snapshot(ctx, i.ID, i.Token, snapshot), ErrConflict)
	overview, err := s.Overview(ctx, tenant, i.ID, OverviewQuery{Page: 1})
	require.NoError(t, err)
	require.Len(t, overview.Items, 1)
	require.False(t, overview.InventoryStale)
	require.NotNil(t, overview.LastSeenAt)
	service := overview.Items[0]
	require.Contains(t, service.Gaps, "no_enabled_monitor")
	key := service.Resources[0].Suggestions[0].Key
	_, err = s.Overview(ctx, other, i.ID, OverviewQuery{Page: 1})
	require.ErrorIs(t, err, ErrNotFound)
	request := ApproveRequest{Key: key, Name: "Web", IntervalSeconds: 60, ExpectedStatus: 200}
	// Two simultaneous approvals create one monitor and one binding.
	var wg sync.WaitGroup
	ids := make([]uuid.UUID, 2)
	created := make([]bool, 2)
	errs := make([]error, 2)
	for n := range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); ids[n], created[n], errs[n] = s.Approve(ctx, tenant, service.ID, request) }()
	}
	wg.Wait()
	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	require.Equal(t, ids[0], ids[1])
	require.ElementsMatch(t, []bool{true, false}, created, "exactly one approval creates; the replay reports created=false")
	monitor := ids[0]
	// A later replay also returns the linked monitor without creating one.
	replayed, again, err := s.Approve(ctx, tenant, service.ID, request)
	require.NoError(t, err)
	require.False(t, again)
	require.Equal(t, monitor, replayed)
	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM monitor_state_intervals WHERE monitor_id=$1`, monitor).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM monitors WHERE tenant_id=$1`, tenant).Scan(&count))
	require.Equal(t, 1, count)
	overview, err = s.Overview(ctx, tenant, i.ID, OverviewQuery{Page: 1})
	require.NoError(t, err)
	// No result yet: aged from created_at like the watchdog, so a monitor
	// approved a moment ago is not stale_checks.
	require.NotContains(t, overview.Items[0].Gaps, "stale_checks")
	require.True(t, overview.Items[0].Monitors[0].Fresh)
	require.Contains(t, overview.Items[0].Gaps, "unrouted")
	_, err = db.ExecContext(ctx, `UPDATE monitors SET created_at=NOW()-interval '1 hour' WHERE id=$1`, monitor)
	require.NoError(t, err)
	overview, err = s.Overview(ctx, tenant, i.ID, OverviewQuery{Page: 1})
	require.NoError(t, err)
	require.Contains(t, overview.Items[0].Gaps, "stale_checks", "never ran within a window of its creation")
	require.NotContains(t, overview.Items[0].Gaps, "no_enabled_monitor")
	require.Len(t, overview.Items[0].Candidates[key], 1)
	require.ErrorIs(t, s.Link(ctx, other, service.ID, monitor, nil), ErrNotFound)
	_, _, err = s.Approve(ctx, other, service.ID, request)
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, s.Unlink(ctx, tenant, service.ID, monitor))
	_, _, err = s.Approve(ctx, tenant, service.ID, request)
	require.Error(t, err)
	require.Contains(t, err.Error(), "link it instead")
	require.NoError(t, s.Link(ctx, tenant, service.ID, monitor, &key))
	// An invalid complete-snapshot claim must not mark inventory missing.
	invalidSnap := contract.Snapshot{Revision: 1, Namespaces: []string{"other"}, Resources: []contract.Resource{}}
	require.Error(t, s.Snapshot(ctx, i.ID, i.Token, invalidSnap))
	overview, err = s.Overview(ctx, tenant, i.ID, OverviewQuery{Page: 1})
	require.NoError(t, err)
	require.Nil(t, overview.Items[0].Resources[0].MissingSince)
	require.NoError(t, s.Snapshot(ctx, i.ID, i.Token, contract.Snapshot{Revision: 1, Namespaces: cfg.Namespaces, Resources: []contract.Resource{}}))
	overview, err = s.Overview(ctx, tenant, i.ID, OverviewQuery{Page: 1})
	require.NoError(t, err)
	require.Contains(t, overview.Items[0].Gaps, "resource_missing_grace")
	require.Len(t, overview.Items[0].Monitors, 1)
	snapshot.Revision = 2
	require.NoError(t, s.Snapshot(ctx, i.ID, i.Token, snapshot))
	overview, err = s.Overview(ctx, tenant, i.ID, OverviewQuery{Page: 1})
	require.NoError(t, err)
	require.Nil(t, overview.Items[0].Resources[0].MissingSince)
	require.Equal(t, service.ID, overview.Items[0].ID)
	rotated, err := s.Rotate(ctx, tenant, i.ID)
	require.NoError(t, err)
	_, err = s.CollectorConfig(ctx, i.ID, i.Token)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = s.CollectorConfig(ctx, i.ID, rotated)
	require.NoError(t, err)
	require.ErrorIs(t, s.Delete(ctx, other, i.ID), ErrNotFound)
	require.NoError(t, s.Delete(ctx, tenant, i.ID))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM monitors WHERE id=$1 AND deleted_at IS NULL`, monitor).Scan(&count))
	require.Equal(t, 1, count)
}
func TestPrivateLocationAndTenantBoundaries(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, db, "one")
	other := testutil.InsertTenant(ctx, t, db, "two")
	s := New(db)
	location := uuid.New()
	_, err := db.ExecContext(ctx, `INSERT INTO locations(id,tenant_id,name,slug) VALUES($1,$2,'private','private')`, location, other)
	require.NoError(t, err)
	_, err = s.Create(ctx, tenant, CreateRequest{Name: "bad", Namespaces: []string{"prod"}, LocationID: &location})
	require.Error(t, err)
	i, err := s.Create(ctx, tenant, CreateRequest{Name: "cluster", Namespaces: []string{"prod"}})
	require.NoError(t, err)
	p := contract.Suggestion{Type: "tcp", Target: "database.prod.svc", Port: 5432, Internal: true}
	require.NoError(t, s.Snapshot(ctx, i.ID, i.Token, contract.Snapshot{Namespaces: []string{"prod"}, Resources: []contract.Resource{{UID: "db", Kind: "Service", Name: "database", Namespace: "prod", Suggestions: []contract.Suggestion{p}}}}))
	o, err := s.Overview(ctx, tenant, i.ID, OverviewQuery{Page: 1})
	require.NoError(t, err)
	service := o.Items[0].ID
	req := ApproveRequest{Key: p.Identity(), Name: "Database", IntervalSeconds: 60}
	_, _, err = s.Approve(ctx, tenant, service, req)
	require.ErrorContains(t, err, "private location")
	req.LocationID = &location
	_, _, err = s.Approve(ctx, tenant, service, req)
	require.ErrorContains(t, err, "workspace")
	_, err = db.ExecContext(ctx, `UPDATE locations SET tenant_id=$1 WHERE id=$2`, tenant, location)
	require.NoError(t, err)
	monitor, _, err := s.Approve(ctx, tenant, service, req)
	require.NoError(t, err)
	var saved uuid.UUID
	require.NoError(t, db.QueryRowContext(ctx, `SELECT location_id FROM monitor_locations WHERE monitor_id=$1`, monitor).Scan(&saved))
	require.Equal(t, location, saved)
	// Soft-deleted monitors do not count as coverage and can be replaced.
	_, err = db.ExecContext(ctx, `UPDATE monitors SET deleted_at=NOW() WHERE id=$1`, monitor)
	require.NoError(t, err)
	replacement, created, err := s.Approve(ctx, tenant, service, req)
	require.True(t, created)
	require.NoError(t, err)
	require.NotEqual(t, monitor, replacement)
}

func TestApprovalRoutingIsAtomicAndTenantScoped(t *testing.T) {
	ctx := context.Background()
	database, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, database, "routing")
	other := testutil.InsertTenant(ctx, t, database, "other-routing")
	s := New(database)
	integration, err := s.Create(ctx, tenant, CreateRequest{Name: "cluster", Namespaces: []string{"prod"}})
	require.NoError(t, err)
	suggestion := contract.Suggestion{Type: "http", Target: "https://routing.example.com/"}
	require.NoError(t, s.Snapshot(ctx, integration.ID, integration.Token, contract.Snapshot{Namespaces: []string{"prod"}, Resources: []contract.Resource{{UID: "route", Kind: "Ingress", Name: "route", Namespace: "prod", Suggestions: []contract.Suggestion{suggestion}}}}))
	o, err := s.Overview(ctx, tenant, integration.ID, OverviewQuery{Page: 1})
	require.NoError(t, err)
	channel := uuid.New()
	_, err = database.ExecContext(ctx, `INSERT INTO alert_channels(id,tenant_id,name,type,config,is_active) VALUES($1,$2,'email','email','{"to":["test@example.com"]}',true)`, channel, other)
	require.NoError(t, err)
	req := ApproveRequest{Key: suggestion.Identity(), Name: "Routed", IntervalSeconds: 60, ExpectedStatus: 200, NotificationMode: "custom", ChannelIDs: []uuid.UUID{channel}}
	_, _, err = s.Approve(ctx, tenant, o.Items[0].ID, req)
	require.ErrorContains(t, err, "workspace")
	var count int
	require.NoError(t, database.QueryRowContext(ctx, `SELECT count(*) FROM monitors WHERE tenant_id=$1`, tenant).Scan(&count))
	require.Zero(t, count)
	_, err = database.ExecContext(ctx, `UPDATE alert_channels SET tenant_id=$1 WHERE id=$2`, tenant, channel)
	require.NoError(t, err)
	monitor, _, err := s.Approve(ctx, tenant, o.Items[0].ID, req)
	require.NoError(t, err)
	o, err = s.Overview(ctx, tenant, integration.ID, OverviewQuery{Page: 1})
	require.NoError(t, err)
	require.NotContains(t, o.Items[0].Gaps, "unrouted")
	require.NoError(t, database.QueryRowContext(ctx, `SELECT count(*) FROM monitor_channels WHERE monitor_id=$1 AND channel_id=$2`, monitor, channel).Scan(&count))
	require.Equal(t, 1, count)
}

func TestTargetMatchingForgedReachabilityAndFiltering(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, db, "matching")
	s := New(db)
	i, err := s.Create(ctx, tenant, CreateRequest{Name: "cluster", Namespaces: []string{"prod"}})
	require.NoError(t, err)
	// An existing monitor spells the Ingress target without the root slash.
	existing := testutil.InsertHTTPMonitor(ctx, t, db, tenant, "existing")
	// Collector claims internal:false for a cluster Service target.
	forged := contract.Suggestion{Type: "tcp", Target: "redis.prod.svc", Port: 6379, Internal: false}
	require.NoError(t, s.Snapshot(ctx, i.ID, i.Token, contract.Snapshot{Namespaces: []string{"prod"}, Resources: []contract.Resource{
		{UID: "web", Kind: "Ingress", Name: "web", Namespace: "prod", Suggestions: []contract.Suggestion{{Type: "http", Target: "https://example.com/"}}},
		{UID: "redis", Kind: "Service", Name: "redis", Namespace: "prod", Suggestions: []contract.Suggestion{forged}},
	}}))
	o, err := s.Overview(ctx, tenant, i.ID, OverviewQuery{Page: 1})
	require.NoError(t, err)
	require.Equal(t, Summary{Services: 2, WithGaps: 2, Unmonitored: 2}, o.Summary)
	var web, redis Coverage
	for _, c := range o.Items {
		if c.Name == "web" {
			web = c
		} else {
			redis = c
		}
	}
	webKey := web.Resources[0].Suggestions[0].Key
	require.Len(t, web.Candidates[webKey], 1)
	require.Equal(t, existing, web.Candidates[webKey][0].ID)
	_, _, err = s.Approve(ctx, tenant, web.ID, ApproveRequest{Key: webKey, Name: "dup", IntervalSeconds: 60, ExpectedStatus: 200})
	require.ErrorContains(t, err, "link it instead", "a trailing-slash spelling is still a duplicate")

	stored := redis.Resources[0].Suggestions[0]
	require.True(t, stored.Internal, "the server derives reachability")
	_, _, err = s.Approve(ctx, tenant, redis.ID, ApproveRequest{Key: forged.Identity(), Name: "forged", IntervalSeconds: 60})
	require.ErrorIs(t, err, ErrNotFound, "the collector-computed key no longer exists")
	_, _, err = s.Approve(ctx, tenant, redis.ID, ApproveRequest{Key: stored.Key, Name: "redis", IntervalSeconds: 60})
	require.ErrorContains(t, err, "private location")

	// Filters apply across the whole integration; summary stays unfiltered.
	require.NoError(t, s.Link(ctx, tenant, web.ID, existing, &webKey))
	_, err = db.ExecContext(ctx, `UPDATE monitors SET last_result_at=NOW() - interval '100 seconds', last_result_started_at=NOW() WHERE id=$1`, existing)
	require.NoError(t, err)
	o, err = s.Overview(ctx, tenant, i.ID, OverviewQuery{Page: 1, Query: "RED"})
	require.NoError(t, err)
	require.Equal(t, 1, o.Total)
	require.Equal(t, "redis", o.Items[0].Name)
	require.Equal(t, 2, o.Summary.Services)
	// S-F1: 100s since platform receipt is within 3 × 60s, whatever the
	// worker-reported start time says.
	o, err = s.Overview(ctx, tenant, i.ID, OverviewQuery{Page: 1, Query: "web"})
	require.NoError(t, err)
	require.True(t, o.Items[0].Monitors[0].Fresh)
	_, err = db.ExecContext(ctx, `UPDATE monitors SET last_result_at=NOW() - interval '200 seconds' WHERE id=$1`, existing)
	require.NoError(t, err)
	o, err = s.Overview(ctx, tenant, i.ID, OverviewQuery{Page: 1, GapsOnly: true, SortGaps: true})
	require.NoError(t, err)
	require.Equal(t, 2, o.Total)
	// web now has stale_checks + unrouted, redis only no_enabled_monitor.
	require.Equal(t, "web", o.Items[0].Name, "most gaps first")
	require.False(t, o.Items[0].Monitors[0].Fresh)
	require.Contains(t, o.Items[0].Gaps, "stale_checks")
}

// snapshotOf submits a complete snapshot at the integration's current revision.
func snapshotOf(ctx context.Context, t *testing.T, s *Service, i *Integration, resources ...contract.Resource) error {
	t.Helper()
	cfg, err := s.CollectorConfig(ctx, i.ID, i.Token)
	require.NoError(t, err)
	if resources == nil {
		resources = []contract.Resource{}
	}
	return s.Snapshot(ctx, i.ID, i.Token, contract.Snapshot{Revision: cfg.Revision, Namespaces: cfg.Namespaces, Resources: resources})
}

func ingress(uid, service string) contract.Resource {
	return contract.Resource{UID: uid, Kind: "Ingress", Name: uid, Namespace: "prod", Service: service,
		Suggestions: []contract.Suggestion{{Type: "http", Target: "https://" + uid + ".example.com/"}}}
}

func serviceIDOf(ctx context.Context, t *testing.T, database *db.Client, integration uuid.UUID, uid string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, database.QueryRowContext(ctx, `SELECT service_id FROM discovered_resources WHERE integration_id=$1 AND uid=$2`, integration, uid).Scan(&id))
	return id
}

func countRows(ctx context.Context, t *testing.T, database *db.Client, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, database.QueryRowContext(ctx, query, args...).Scan(&n))
	return n
}

func TestSnapshotRetentionPrunesMissingInventory(t *testing.T) {
	ctx := context.Background()
	database, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, database, "retention")
	s := New(database)
	s.minSnapshotInterval = 0
	i, err := s.Create(ctx, tenant, CreateRequest{Name: "cluster", Namespaces: []string{"prod"}})
	require.NoError(t, err)
	require.NoError(t, snapshotOf(ctx, t, s, i, ingress("keep", ""), ingress("gone", ""), ingress("linked", "")))
	linkedService := serviceIDOf(ctx, t, database, i.ID, "linked")
	monitor := testutil.InsertHTTPMonitor(ctx, t, database, tenant, "linked")
	require.NoError(t, s.Link(ctx, tenant, linkedService, monitor, nil))

	require.NoError(t, snapshotOf(ctx, t, s, i, ingress("keep", "")))
	var first time.Time
	require.NoError(t, database.QueryRowContext(ctx, `SELECT missing_since FROM discovered_resources WHERE integration_id=$1 AND uid='gone'`, i.ID).Scan(&first))
	require.NoError(t, snapshotOf(ctx, t, s, i, ingress("keep", "")))
	var second time.Time
	require.NoError(t, database.QueryRowContext(ctx, `SELECT missing_since FROM discovered_resources WHERE integration_id=$1 AND uid='gone'`, i.ID).Scan(&second))
	require.True(t, first.Equal(second), "an already-missing resource keeps its missing_since")

	// Within retention both stay; past it, the next snapshot deletes them and
	// their now-empty services, including the linked one with its link.
	_, err = database.ExecContext(ctx, `UPDATE discovered_resources SET missing_since=NOW()-interval '6 days' WHERE integration_id=$1 AND uid<>'keep'`, i.ID)
	require.NoError(t, err)
	require.NoError(t, snapshotOf(ctx, t, s, i, ingress("keep", "")))
	require.Equal(t, 3, countRows(ctx, t, database, `SELECT count(*) FROM discovered_resources WHERE integration_id=$1`, i.ID))
	_, err = database.ExecContext(ctx, `UPDATE discovered_resources SET missing_since=NOW()-interval '8 days' WHERE integration_id=$1 AND uid<>'keep'`, i.ID)
	require.NoError(t, err)
	// Their services went absent in the same snapshot the resources went missing.
	_, err = database.ExecContext(ctx, `UPDATE discovered_services SET absent_since=NOW()-interval '8 days' WHERE integration_id=$1 AND absent_since IS NOT NULL`, i.ID)
	require.NoError(t, err)
	require.NoError(t, snapshotOf(ctx, t, s, i, ingress("keep", "")))
	require.Equal(t, 1, countRows(ctx, t, database, `SELECT count(*) FROM discovered_resources WHERE integration_id=$1`, i.ID))
	require.Equal(t, 1, countRows(ctx, t, database, `SELECT count(*) FROM discovered_services WHERE integration_id=$1`, i.ID))
	require.Zero(t, countRows(ctx, t, database, `SELECT count(*) FROM discovery_monitor_links WHERE service_id=$1`, linkedService))
	require.Equal(t, 1, countRows(ctx, t, database, `SELECT count(*) FROM monitors WHERE id=$1 AND deleted_at IS NULL`, monitor), "monitors are never pruned")
	o, err := s.Overview(ctx, tenant, i.ID, OverviewQuery{Page: 1})
	require.NoError(t, err)
	require.Equal(t, 1, o.Summary.Services)
}

func TestGroupingChangeLeavesLinkedServiceInGraceThenPrunes(t *testing.T) {
	ctx := context.Background()
	database, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, database, "grouping")
	s := New(database)
	s.minSnapshotInterval = 0
	i, err := s.Create(ctx, tenant, CreateRequest{Name: "cluster", Namespaces: []string{"prod"}})
	require.NoError(t, err)
	require.NoError(t, snapshotOf(ctx, t, s, i, ingress("web", ""), ingress("api", "")))
	web := serviceIDOf(ctx, t, database, i.ID, "web")
	api := serviceIDOf(ctx, t, database, i.ID, "api")
	monitor := testutil.InsertHTTPMonitor(ctx, t, database, tenant, "web")
	require.NoError(t, s.Link(ctx, tenant, web, monitor, nil))

	// probara.io/service now groups both under "shop": the unlinked old
	// service goes at once, the linked one stays without resources.
	require.NoError(t, snapshotOf(ctx, t, s, i, ingress("web", "shop"), ingress("api", "shop")))
	require.Zero(t, countRows(ctx, t, database, `SELECT count(*) FROM discovered_services WHERE id=$1`, api))
	require.Equal(t, 1, countRows(ctx, t, database, `SELECT count(*) FROM discovered_services WHERE id=$1 AND absent_since IS NOT NULL`, web))
	byID := func(o Overview, id uuid.UUID) Coverage {
		for _, c := range o.Items {
			if c.ID == id {
				return c
			}
		}
		t.Fatalf("service %s not in overview", id)
		return Coverage{}
	}
	o, err := s.Overview(ctx, tenant, i.ID, OverviewQuery{Page: 1})
	require.NoError(t, err)
	require.Equal(t, 2, o.Summary.Services)
	ghost := byID(o, web)
	require.Empty(t, ghost.Resources)
	require.Len(t, ghost.Monitors, 1)
	require.Contains(t, ghost.Gaps, "resource_missing_grace", "a service that just lost its resources gets the same grace")
	require.Equal(t, 1, o.Summary.MissingGrace)
	require.Zero(t, o.Summary.Missing)

	_, err = database.ExecContext(ctx, `UPDATE discovered_services SET absent_since=NOW()-interval '20 minutes' WHERE id=$1`, web)
	require.NoError(t, err)
	o, err = s.Overview(ctx, tenant, i.ID, OverviewQuery{Page: 1})
	require.NoError(t, err)
	require.Contains(t, byID(o, web).Gaps, "resource_missing")
	require.Equal(t, 1, o.Summary.Missing)
	require.Zero(t, o.Summary.MissingGrace)

	// Reverting the grouping brings the resource back to the same service.
	require.NoError(t, snapshotOf(ctx, t, s, i, ingress("web", ""), ingress("api", "shop")))
	require.Equal(t, web, serviceIDOf(ctx, t, database, i.ID, "web"))
	require.Equal(t, 1, countRows(ctx, t, database, `SELECT count(*) FROM discovered_services WHERE id=$1 AND absent_since IS NULL`, web))
	o, err = s.Overview(ctx, tenant, i.ID, OverviewQuery{Page: 1})
	require.NoError(t, err)
	require.NotContains(t, byID(o, web).Gaps, "resource_missing")

	// Grouped again and left past retention: deleted with its link.
	require.NoError(t, snapshotOf(ctx, t, s, i, ingress("web", "shop"), ingress("api", "shop")))
	_, err = database.ExecContext(ctx, `UPDATE discovered_services SET absent_since=NOW()-interval '8 days' WHERE id=$1`, web)
	require.NoError(t, err)
	require.NoError(t, snapshotOf(ctx, t, s, i, ingress("web", "shop"), ingress("api", "shop")))
	require.Zero(t, countRows(ctx, t, database, `SELECT count(*) FROM discovered_services WHERE id=$1`, web))
	require.Zero(t, countRows(ctx, t, database, `SELECT count(*) FROM discovery_monitor_links WHERE monitor_id=$1`, monitor))
	require.Equal(t, 1, countRows(ctx, t, database, `SELECT count(*) FROM monitors WHERE id=$1 AND deleted_at IS NULL`, monitor))
}

// A service whose last present resource moves away in the same snapshot that
// prunes a sibling is absent from that snapshot: it keeps its links through
// the grace and the retention, rather than going with the pruned resource.
func TestGroupingChangeWithSiblingPrunedKeepsLinkedService(t *testing.T) {
	ctx := context.Background()
	database, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, database, "grouping-prune")
	s := New(database)
	s.minSnapshotInterval = 0
	i, err := s.Create(ctx, tenant, CreateRequest{Name: "cluster", Namespaces: []string{"prod"}})
	require.NoError(t, err)
	require.NoError(t, snapshotOf(ctx, t, s, i, ingress("web", "shop"), ingress("old", "shop")))
	shop := serviceIDOf(ctx, t, database, i.ID, "web")
	monitor := testutil.InsertHTTPMonitor(ctx, t, database, tenant, "shop")
	require.NoError(t, s.Link(ctx, tenant, shop, monitor, nil))
	// "old" went missing more than the retention ago; "web" is still present.
	require.NoError(t, snapshotOf(ctx, t, s, i, ingress("web", "shop")))
	_, err = database.ExecContext(ctx, `UPDATE discovered_resources SET missing_since=NOW()-interval '8 days' WHERE integration_id=$1 AND uid='old'`, i.ID)
	require.NoError(t, err)
	require.Equal(t, 1, countRows(ctx, t, database, `SELECT count(*) FROM discovered_services WHERE id=$1 AND absent_since IS NULL`, shop))

	// "web" moves to "store" while "old" is pruned.
	require.NoError(t, snapshotOf(ctx, t, s, i, ingress("web", "store")))
	require.Zero(t, countRows(ctx, t, database, `SELECT count(*) FROM discovered_resources WHERE integration_id=$1 AND uid='old'`, i.ID))
	require.Equal(t, 1, countRows(ctx, t, database, `SELECT count(*) FROM discovered_services WHERE id=$1 AND absent_since > NOW()-interval '1 minute'`, shop))
	require.Equal(t, 1, countRows(ctx, t, database, `SELECT count(*) FROM discovery_monitor_links WHERE service_id=$1`, shop))
	o, err := s.Overview(ctx, tenant, i.ID, OverviewQuery{Page: 1})
	require.NoError(t, err)
	require.Equal(t, 2, o.Summary.Services)
	require.Equal(t, 1, o.Summary.MissingGrace)

	// Absent past the retention: deleted with its link, monitor kept.
	_, err = database.ExecContext(ctx, `UPDATE discovered_services SET absent_since=NOW()-interval '8 days' WHERE id=$1`, shop)
	require.NoError(t, err)
	require.NoError(t, snapshotOf(ctx, t, s, i, ingress("web", "store")))
	require.Zero(t, countRows(ctx, t, database, `SELECT count(*) FROM discovered_services WHERE id=$1`, shop))
	require.Zero(t, countRows(ctx, t, database, `SELECT count(*) FROM discovery_monitor_links WHERE monitor_id=$1`, monitor))
	require.Equal(t, 1, countRows(ctx, t, database, `SELECT count(*) FROM monitors WHERE id=$1 AND deleted_at IS NULL`, monitor))
}

func TestSnapshotRefusedAboveStoredResourceCap(t *testing.T) {
	ctx := context.Background()
	database, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, database, "cap")
	s := New(database)
	s.minSnapshotInterval = 0
	i, err := s.Create(ctx, tenant, CreateRequest{Name: "cluster", Namespaces: []string{"prod"}})
	require.NoError(t, err)
	require.NoError(t, snapshotOf(ctx, t, s, i, ingress("keep", "")))
	service := serviceIDOf(ctx, t, database, i.ID, "keep")
	// Churn within retention: fill the integration to exactly the cap.
	_, err = database.ExecContext(ctx, `INSERT INTO discovered_resources(integration_id,uid,service_id,kind,name,namespace,missing_since)
	 SELECT $1,'old-'||g,$2,'Ingress','old','prod',NOW()-interval '1 day' FROM generate_series(1,$3::int) g`, i.ID, service, MaxStoredResources-1)
	require.NoError(t, err)
	cfg, err := s.CollectorConfig(ctx, i.ID, i.Token)
	require.NoError(t, err)
	err = snapshotOf(ctx, t, s, i, ingress("keep", ""), ingress("new", ""))
	var full InventoryFull
	require.ErrorAs(t, err, &full)
	require.Equal(t, MaxStoredResources+1, full.Stored)
	require.Equal(t, MaxStoredResources, full.Limit)
	require.Zero(t, countRows(ctx, t, database, `SELECT count(*) FROM discovered_resources WHERE integration_id=$1 AND uid='new'`, i.ID), "the refused snapshot is rolled back")
	after, err := s.CollectorConfig(ctx, i.ID, i.Token)
	require.NoError(t, err)
	require.Equal(t, cfg.Revision, after.Revision)
	// The refusal did the reconciliation work, so it counts against the rate
	// limit although nothing was applied.
	require.Equal(t, 1, countRows(ctx, t, database, `SELECT count(*) FROM discovery_integrations WHERE id=$1 AND last_attempt_at > last_seen_at`, i.ID))
	s.minSnapshotInterval = MinSnapshotInterval
	var limited RateLimited
	require.ErrorAs(t, s.AuthorizeSnapshot(ctx, i.ID, i.Token), &limited, "a refused snapshot cannot be repeated back to back")
	require.ErrorAs(t, snapshotOf(ctx, t, s, i, ingress("keep", ""), ingress("new", "")), &limited)
	s.minSnapshotInterval = 0
	require.NoError(t, snapshotOf(ctx, t, s, i, ingress("keep", "")), "staying at the cap is accepted")
}

func TestSnapshotRateLimit(t *testing.T) {
	ctx := context.Background()
	database, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, database, "rate")
	s := New(database)
	i, err := s.Create(ctx, tenant, CreateRequest{Name: "cluster", Namespaces: []string{"prod"}})
	require.NoError(t, err)
	require.NoError(t, s.AuthorizeSnapshot(ctx, i.ID, i.Token), "a never-synced integration may submit")
	require.ErrorIs(t, s.AuthorizeSnapshot(ctx, i.ID, "wrong"), ErrNotFound)
	require.NoError(t, snapshotOf(ctx, t, s, i, ingress("web", "")))
	var limited RateLimited
	require.ErrorAs(t, s.AuthorizeSnapshot(ctx, i.ID, i.Token), &limited)
	require.Greater(t, limited.RetryAfter, time.Duration(0))
	require.LessOrEqual(t, limited.RetryAfter, MinSnapshotInterval)
	require.ErrorAs(t, snapshotOf(ctx, t, s, i, ingress("web", "")), &limited, "Snapshot re-checks under the row lock")
	_, err = database.ExecContext(ctx, `UPDATE discovery_integrations SET last_attempt_at=NOW()-interval '61 seconds' WHERE id=$1`, i.ID)
	require.NoError(t, err)
	require.NoError(t, s.AuthorizeSnapshot(ctx, i.ID, i.Token))
	require.NoError(t, snapshotOf(ctx, t, s, i, ingress("web", "")))
}

func TestStaleInventoryIsPageLevelAndBlocksApproval(t *testing.T) {
	ctx := context.Background()
	database, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, database, "stale")
	s := New(database)
	i, err := s.Create(ctx, tenant, CreateRequest{Name: "cluster", Namespaces: []string{"prod"}})
	require.NoError(t, err)
	require.NoError(t, snapshotOf(ctx, t, s, i, ingress("web", "")))
	web := serviceIDOf(ctx, t, database, i.ID, "web")
	monitor := testutil.InsertHTTPMonitor(ctx, t, database, tenant, "web")
	require.NoError(t, s.Link(ctx, tenant, web, monitor, nil))
	// A fully covered service: fresh evidence and a routed channel.
	channel := uuid.New()
	_, err = database.ExecContext(ctx, `INSERT INTO alert_channels(id,tenant_id,name,type,config,is_active) VALUES($1,$2,'email','email','{"to":["test@example.com"]}',true)`, channel, tenant)
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `INSERT INTO monitor_channels(monitor_id,channel_id) VALUES($1,$2)`, monitor, channel)
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `UPDATE monitors SET last_result_at=NOW(),notification_mode='custom' WHERE id=$1`, monitor)
	require.NoError(t, err)
	o, err := s.Overview(ctx, tenant, i.ID, OverviewQuery{Page: 1})
	require.NoError(t, err)
	require.Empty(t, o.Items[0].Gaps)
	_, err = database.ExecContext(ctx, `UPDATE discovery_integrations SET last_seen_at=NOW()-interval '16 minutes' WHERE id=$1`, i.ID)
	require.NoError(t, err)
	o, err = s.Overview(ctx, tenant, i.ID, OverviewQuery{Page: 1, GapsOnly: true})
	require.NoError(t, err)
	require.True(t, o.InventoryStale)
	require.Equal(t, Summary{Services: 1, Covered: 1}, o.Summary, "inventory_stale does not count as a service gap")
	require.Zero(t, o.Total, "the gaps filter ignores inventory_stale")
	o, err = s.Overview(ctx, tenant, i.ID, OverviewQuery{Page: 1})
	require.NoError(t, err)
	require.Equal(t, []string{"inventory_stale"}, o.Items[0].Gaps, "still reported on the row")
	key := o.Items[0].Resources[0].Suggestions[0].Key
	_, _, err = s.Approve(ctx, tenant, web, ApproveRequest{Key: key, Name: "Web", IntervalSeconds: 60, ExpectedStatus: 200})
	require.ErrorContains(t, err, "stale")
}
