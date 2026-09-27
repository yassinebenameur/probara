package monitors_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	testcontainers "github.com/testcontainers/testcontainers-go"

	monitorsvc "github.com/yassinebenameur/probara/api/internal/services/monitors"
	"github.com/yassinebenameur/probara/shared/alertrouting"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// insertChannel creates an alert channel, active or disabled.
func insertChannel(ctx context.Context, t testing.TB, dbClient *shareddb.Client, tenantID uuid.UUID, name string, active bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := dbClient.ExecContext(ctx, `
		INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, 'email', '{"to":["ops@example.com"]}'::jsonb, $4, NOW(), NOW())
	`, id, tenantID, name, active)
	require.NoError(t, err, "insert alert channel")
	return id
}

func setNotificationMode(ctx context.Context, t testing.TB, dbClient *shareddb.Client, monitorID uuid.UUID, mode string) {
	t.Helper()
	_, err := dbClient.ExecContext(ctx,
		`UPDATE monitors SET notification_mode = $2 WHERE id = $1`, monitorID, mode)
	require.NoError(t, err, "set notification mode")
}

func assignMonitorChannel(ctx context.Context, t testing.TB, dbClient *shareddb.Client, monitorID, channelID uuid.UUID) {
	t.Helper()
	_, err := dbClient.ExecContext(ctx, `
		INSERT INTO monitor_channels (monitor_id, channel_id, delay_seconds)
		VALUES ($1, $2, 0)
	`, monitorID, channelID)
	require.NoError(t, err, "assign monitor channel")
}

func addTenantDefaultChannel(ctx context.Context, t testing.TB, dbClient *shareddb.Client, tenantID, channelID uuid.UUID, position int) {
	t.Helper()
	_, err := dbClient.ExecContext(ctx, `
		INSERT INTO tenant_default_channels (tenant_id, channel_id, delay_seconds, position)
		VALUES ($1, $2, 0, $3)
	`, tenantID, channelID, position)
	require.NoError(t, err, "add tenant default channel")
}

func setEnabled(ctx context.Context, t testing.TB, dbClient *shareddb.Client, monitorID uuid.UUID, enabled bool) {
	t.Helper()
	_, err := dbClient.ExecContext(ctx,
		`UPDATE monitors SET enabled = $2 WHERE id = $1`, monitorID, enabled)
	require.NoError(t, err, "set enabled")
}

func addGroupMember(ctx context.Context, t testing.TB, dbClient *shareddb.Client, groupID, monitorID uuid.UUID, rollup string) {
	t.Helper()
	_, err := dbClient.ExecContext(ctx, `
		INSERT INTO monitor_groups (monitor_id, group_id, created_at) VALUES ($1, $2, NOW())
	`, monitorID, groupID)
	require.NoError(t, err, "add group member")
	_, err = dbClient.ExecContext(ctx,
		`UPDATE monitors SET member_alert_rollup = $2 WHERE id = $1`, groupID, rollup)
	require.NoError(t, err, "set group rollup mode")
}

// TestAlertRoutingReachability covers each routing shape end to end: what the
// repository reports must match what the alerter would actually deliver.
func TestAlertRoutingReachability(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")
	activeChannel := insertChannel(ctx, t, dbClient, tenantID, "active-email", true)
	disabledChannel := insertChannel(ctx, t, dbClient, tenantID, "disabled-email", false)
	repo := monitorsvc.NewPostgresRepository(dbClient)

	// Tenant defaults are empty for now, so 'default' mode routes nowhere.
	defaultNoRoutes := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "default-no-routes")

	customRouted := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "custom-routed")
	setNotificationMode(ctx, t, dbClient, customRouted, "custom")
	assignMonitorChannel(ctx, t, dbClient, customRouted, activeChannel)

	customEmpty := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "custom-empty")
	setNotificationMode(ctx, t, dbClient, customEmpty, "custom")

	customDisabled := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "custom-disabled")
	setNotificationMode(ctx, t, dbClient, customDisabled, "custom")
	assignMonitorChannel(ctx, t, dbClient, customDisabled, disabledChannel)

	// A member rolled up into a group that itself routes nowhere: its own
	// channels never fire, so it is unreachable despite having one.
	rolledUpMember := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "rolled-up-member")
	setNotificationMode(ctx, t, dbClient, rolledUpMember, "custom")
	assignMonitorChannel(ctx, t, dbClient, rolledUpMember, activeChannel)
	silentGroup := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "silent-group")
	setNotificationMode(ctx, t, dbClient, silentGroup, "custom")
	addGroupMember(ctx, t, dbClient, silentGroup, rolledUpMember, "group")

	// A member of a routed roll-up group inherits the group's reachability.
	coveredMember := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "covered-member")
	setNotificationMode(ctx, t, dbClient, coveredMember, "custom")
	routedGroup := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "routed-group")
	setNotificationMode(ctx, t, dbClient, routedGroup, "custom")
	assignMonitorChannel(ctx, t, dbClient, routedGroup, activeChannel)
	addGroupMember(ctx, t, dbClient, routedGroup, coveredMember, "group")

	// A per_monitor group never alerts itself.
	perMonitorGroup := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "per-monitor-group")
	perMonitorMember := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "per-monitor-member")
	addGroupMember(ctx, t, dbClient, perMonitorGroup, perMonitorMember, "per_monitor")

	// Roll-up suppression ignores the group's enabled flag, so a paused group
	// silences its members while never alerting itself.
	pausedGroupMember := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "paused-group-member")
	pausedGroup := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "paused-group")
	setNotificationMode(ctx, t, dbClient, pausedGroup, "custom")
	assignMonitorChannel(ctx, t, dbClient, pausedGroup, activeChannel)
	addGroupMember(ctx, t, dbClient, pausedGroup, pausedGroupMember, "group")
	setEnabled(ctx, t, dbClient, pausedGroup, false)

	// Two roll-up groups cover this member and both alert, so reaching either
	// is enough — the unrouted one must not mask the routed one.
	multiGroupMember := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "multi-group-member")
	addGroupMember(ctx, t, dbClient, silentGroup, multiGroupMember, "group")
	addGroupMember(ctx, t, dbClient, routedGroup, multiGroupMember, "group")

	all := []uuid.UUID{
		defaultNoRoutes, customRouted, customEmpty, customDisabled,
		rolledUpMember, silentGroup, coveredMember, routedGroup,
		perMonitorGroup, perMonitorMember,
		pausedGroupMember, multiGroupMember,
	}
	routing, err := repo.GetAlertRoutingForMonitors(ctx, all)
	require.NoError(t, err)
	require.Len(t, routing, len(all), "every monitor should get a routing verdict")

	expect := map[uuid.UUID]struct {
		reachable bool
		source    string
		reason    string
	}{
		defaultNoRoutes:  {false, alertrouting.SourceTenantDefault, alertrouting.ReasonNoTenantDefaults},
		customRouted:     {true, alertrouting.SourceCustom, ""},
		customEmpty:      {false, alertrouting.SourceCustom, alertrouting.ReasonNoCustomChannels},
		customDisabled:   {false, alertrouting.SourceCustom, alertrouting.ReasonCustomChannelsDisabled},
		rolledUpMember:   {false, alertrouting.SourceGroupRollup, alertrouting.ReasonGroupRollupUnrouted},
		silentGroup:      {false, alertrouting.SourceCustom, alertrouting.ReasonNoCustomChannels},
		coveredMember:    {true, alertrouting.SourceGroupRollup, ""},
		routedGroup:      {true, alertrouting.SourceCustom, ""},
		perMonitorGroup:  {true, alertrouting.SourceMembers, ""},
		perMonitorMember: {false, alertrouting.SourceTenantDefault, alertrouting.ReasonNoTenantDefaults},

		pausedGroupMember: {false, alertrouting.SourceGroupRollup, alertrouting.ReasonGroupRollupPaused},
		multiGroupMember:  {true, alertrouting.SourceGroupRollup, ""},
	}
	names := map[uuid.UUID]string{
		defaultNoRoutes: "defaultNoRoutes", customRouted: "customRouted",
		customEmpty: "customEmpty", customDisabled: "customDisabled",
		rolledUpMember: "rolledUpMember", silentGroup: "silentGroup",
		coveredMember: "coveredMember", routedGroup: "routedGroup",
		perMonitorGroup: "perMonitorGroup", perMonitorMember: "perMonitorMember",
		pausedGroupMember: "pausedGroupMember", multiGroupMember: "multiGroupMember",
	}
	for id, want := range expect {
		got := routing[id]
		require.Equalf(t, want.reachable, got.Reachable, "%s reachable", names[id])
		require.Equalf(t, want.source, got.Source, "%s source", names[id])
		require.Equalf(t, want.reason, got.Reason, "%s reason", names[id])
	}
	require.Equal(t, silentGroup, *routing[rolledUpMember].RollupGroupID, "member should name its roll-up group")
	require.Equal(t, "silent-group", routing[rolledUpMember].RollupGroupName)
	require.Equal(t, routedGroup, *routing[multiGroupMember].RollupGroupID,
		"with several covering groups, the one that actually delivers must be reported")
	require.Equal(t, pausedGroup, *routing[pausedGroupMember].RollupGroupID)

	// Adding an active tenant default fixes every 'default' mode monitor.
	addTenantDefaultChannel(ctx, t, dbClient, tenantID, activeChannel, 0)
	routing, err = repo.GetAlertRoutingForMonitors(ctx, []uuid.UUID{defaultNoRoutes, perMonitorMember})
	require.NoError(t, err)
	require.True(t, routing[defaultNoRoutes].Reachable, "tenant default should make it reachable")
	require.Equal(t, 1, routing[defaultNoRoutes].ActiveChannels)
	require.True(t, routing[perMonitorMember].Reachable)
}

// TestUnreachablePredicateMatchesClassify pins the SQL twin of Classify: the
// dashboard's unrouted count and the per-monitor verdict must never disagree.
func TestUnreachablePredicateMatchesClassify(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")
	activeChannel := insertChannel(ctx, t, dbClient, tenantID, "active-email", true)
	disabledChannel := insertChannel(ctx, t, dbClient, tenantID, "disabled-email", false)
	repo := monitorsvc.NewPostgresRepository(dbClient)

	// One monitor per routing shape, including both group roll-up modes.
	var ids []uuid.UUID
	newHTTP := func(name, mode string, channels ...uuid.UUID) uuid.UUID {
		id := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, name)
		setNotificationMode(ctx, t, dbClient, id, mode)
		for _, ch := range channels {
			assignMonitorChannel(ctx, t, dbClient, id, ch)
		}
		ids = append(ids, id)
		return id
	}

	newHTTP("d-inherits", "default")
	newHTTP("c-active", "custom", activeChannel)
	newHTTP("c-none", "custom")
	newHTTP("c-disabled", "custom", disabledChannel)

	unroutedGroup := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "g-unrouted")
	setNotificationMode(ctx, t, dbClient, unroutedGroup, "custom")
	ids = append(ids, unroutedGroup)
	addGroupMember(ctx, t, dbClient, unroutedGroup, newHTTP("g-unrouted-member", "custom", activeChannel), "group")

	routedGroup := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "g-routed")
	setNotificationMode(ctx, t, dbClient, routedGroup, "custom")
	assignMonitorChannel(ctx, t, dbClient, routedGroup, activeChannel)
	ids = append(ids, routedGroup)
	addGroupMember(ctx, t, dbClient, routedGroup, newHTTP("g-routed-member", "custom"), "group")

	perMonitorGroup := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "g-per-monitor")
	setNotificationMode(ctx, t, dbClient, perMonitorGroup, "custom")
	ids = append(ids, perMonitorGroup)
	addGroupMember(ctx, t, dbClient, perMonitorGroup, newHTTP("g-per-monitor-member", "custom"), "per_monitor")

	// A paused roll-up group with channels, and a member covered by both an
	// unrouted and a routed group.
	pausedGroup := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "g-paused")
	setNotificationMode(ctx, t, dbClient, pausedGroup, "custom")
	assignMonitorChannel(ctx, t, dbClient, pausedGroup, activeChannel)
	ids = append(ids, pausedGroup)
	addGroupMember(ctx, t, dbClient, pausedGroup, newHTTP("g-paused-member", "custom"), "group")
	setEnabled(ctx, t, dbClient, pausedGroup, false)

	multiGroupMember := newHTTP("g-multi-member", "custom")
	addGroupMember(ctx, t, dbClient, unroutedGroup, multiGroupMember, "group")
	addGroupMember(ctx, t, dbClient, routedGroup, multiGroupMember, "group")

	// Run the matrix twice: with and without tenant defaults, so 'default'
	// mode monitors are exercised on both sides of the boundary.
	for _, withDefaults := range []bool{false, true} {
		if withDefaults {
			addTenantDefaultChannel(ctx, t, dbClient, tenantID, activeChannel, 0)
		}

		routing, err := repo.GetAlertRoutingForMonitors(ctx, ids)
		require.NoError(t, err)

		rows, err := dbClient.QueryContext(ctx, `
			SELECT m.id FROM monitors m
			WHERE m.tenant_id = $1 AND m.deleted_at IS NULL AND m.enabled = TRUE
			  AND `+alertrouting.UnreachablePredicate("m"), tenantID)
		require.NoError(t, err)
		sqlUnreachable := map[uuid.UUID]bool{}
		for rows.Next() {
			var id uuid.UUID
			require.NoError(t, rows.Scan(&id))
			sqlUnreachable[id] = true
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())

		for _, id := range ids {
			status, ok := routing[id]
			require.True(t, ok, "missing routing verdict")
			require.Equalf(t, !status.Reachable, sqlUnreachable[id],
				"SQL predicate and Classify disagree on %s (source %s, defaults=%v)",
				id, status.Source, withDefaults)
		}
	}
}
