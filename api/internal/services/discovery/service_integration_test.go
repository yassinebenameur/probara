//go:build !unit

package discovery

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
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
	errs := make([]error, 2)
	for n := range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); ids[n], errs[n] = s.Approve(ctx, tenant, service.ID, request) }()
	}
	wg.Wait()
	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	require.Equal(t, ids[0], ids[1])
	monitor := ids[0]
	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM monitor_state_intervals WHERE monitor_id=$1`, monitor).Scan(&count))
	require.Equal(t, 1, count)
	overview, err = s.Overview(ctx, tenant, i.ID, OverviewQuery{Page: 1})
	require.NoError(t, err)
	require.Contains(t, overview.Items[0].Gaps, "stale_checks")
	require.Contains(t, overview.Items[0].Gaps, "unrouted")
	require.NotContains(t, overview.Items[0].Gaps, "no_enabled_monitor")
	require.Len(t, overview.Items[0].Candidates[key], 1)
	require.ErrorIs(t, s.Link(ctx, other, service.ID, monitor, nil), ErrNotFound)
	_, err = s.Approve(ctx, other, service.ID, request)
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, s.Unlink(ctx, tenant, service.ID, monitor))
	_, err = s.Approve(ctx, tenant, service.ID, request)
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
	_, err = s.Approve(ctx, tenant, service, req)
	require.ErrorContains(t, err, "private location")
	req.LocationID = &location
	_, err = s.Approve(ctx, tenant, service, req)
	require.ErrorContains(t, err, "workspace")
	_, err = db.ExecContext(ctx, `UPDATE locations SET tenant_id=$1 WHERE id=$2`, tenant, location)
	require.NoError(t, err)
	monitor, err := s.Approve(ctx, tenant, service, req)
	require.NoError(t, err)
	var saved uuid.UUID
	require.NoError(t, db.QueryRowContext(ctx, `SELECT location_id FROM monitor_locations WHERE monitor_id=$1`, monitor).Scan(&saved))
	require.Equal(t, location, saved)
	// Soft-deleted monitors do not count as coverage and can be replaced.
	_, err = db.ExecContext(ctx, `UPDATE monitors SET deleted_at=NOW() WHERE id=$1`, monitor)
	require.NoError(t, err)
	replacement, err := s.Approve(ctx, tenant, service, req)
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
	_, err = s.Approve(ctx, tenant, o.Items[0].ID, req)
	require.ErrorContains(t, err, "workspace")
	var count int
	require.NoError(t, database.QueryRowContext(ctx, `SELECT count(*) FROM monitors WHERE tenant_id=$1`, tenant).Scan(&count))
	require.Zero(t, count)
	_, err = database.ExecContext(ctx, `UPDATE alert_channels SET tenant_id=$1 WHERE id=$2`, tenant, channel)
	require.NoError(t, err)
	monitor, err := s.Approve(ctx, tenant, o.Items[0].ID, req)
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
	_, err = s.Approve(ctx, tenant, web.ID, ApproveRequest{Key: webKey, Name: "dup", IntervalSeconds: 60, ExpectedStatus: 200})
	require.ErrorContains(t, err, "link it instead", "a trailing-slash spelling is still a duplicate")

	stored := redis.Resources[0].Suggestions[0]
	require.True(t, stored.Internal, "the server derives reachability")
	_, err = s.Approve(ctx, tenant, redis.ID, ApproveRequest{Key: forged.Identity(), Name: "forged", IntervalSeconds: 60})
	require.ErrorIs(t, err, ErrNotFound, "the collector-computed key no longer exists")
	_, err = s.Approve(ctx, tenant, redis.ID, ApproveRequest{Key: stored.Key, Name: "redis", IntervalSeconds: 60})
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
