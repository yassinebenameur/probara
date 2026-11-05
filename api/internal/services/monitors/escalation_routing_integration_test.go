//go:build !unit

package monitors_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/api/internal/models"
	monitorsvc "github.com/yassinebenameur/probara/api/internal/services/monitors"
	"github.com/yassinebenameur/probara/shared/alertrouting"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// newPolicyFixtures creates escalation policies covering every reachability
// shape, keyed by name: person (a pageable user), channel (an active
// channel), admins (all_admins with a pageable admin), schedule (whoever a
// schedule has on call, a pageable member), nobody (a user without a
// contact, a disabled channel, an empty schedule).
func newPolicyFixtures(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID, activeChannel, disabledChannel uuid.UUID) map[string]uuid.UUID {
	t.Helper()
	exec := func(q string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, q, args...)
		require.NoError(t, err)
	}
	user := func(name, role string, contact bool) uuid.UUID {
		id := uuid.New()
		exec(`INSERT INTO admin_users (id, username, password_hash) VALUES ($1, $2, 'x')`, id, name+"-"+id.String()[:6])
		exec(`INSERT INTO tenant_memberships (admin_user_id, tenant_id, role) VALUES ($1, $2, $3)`, id, tenantID, role)
		if contact {
			exec(`INSERT INTO user_contacts (user_id, kind, address, verified_at) VALUES ($1, 'email', 'x@example.com', NOW())`, id)
		}
		return id
	}
	pageable := user("pageable", "admin", true)
	silent := user("silent", "editor", false)
	schedule := func(member *uuid.UUID) uuid.UUID {
		id := uuid.New()
		exec(`INSERT INTO oncall_schedules (id, tenant_id, name) VALUES ($1, $2, $3)`, id, tenantID, "s-"+id.String()[:8])
		if member != nil {
			rot := uuid.New()
			exec(`INSERT INTO oncall_rotations (id, schedule_id, position, kind, handoff_minute, start_at)
				VALUES ($1, $2, 0, 'daily', 0, NOW() - INTERVAL '1 day')`, rot, id)
			exec(`INSERT INTO oncall_rotation_members (rotation_id, position, user_id) VALUES ($1, 0, $2)`, rot, *member)
		}
		return id
	}
	policy := func(name string, targets ...[3]any) uuid.UUID {
		id, step := uuid.New(), uuid.New()
		exec(`INSERT INTO escalation_policies (id, tenant_id, name) VALUES ($1, $2, $3)`, id, tenantID, name)
		exec(`INSERT INTO escalation_policy_steps (id, policy_id, position, delay_seconds) VALUES ($1, $2, 0, 0)`, step, id)
		for i, tg := range targets {
			exec(`INSERT INTO escalation_policy_targets (step_id, position, kind, user_id, schedule_id, channel_id)
				VALUES ($1, $2, $3, $4, $5, $6)`, step, i, tg[0], tg[1], tg[2], nil)
		}
		return id
	}
	channelPolicy := func(name string, channel uuid.UUID) uuid.UUID {
		id, step := uuid.New(), uuid.New()
		exec(`INSERT INTO escalation_policies (id, tenant_id, name) VALUES ($1, $2, $3)`, id, tenantID, name)
		exec(`INSERT INTO escalation_policy_steps (id, policy_id, position, delay_seconds) VALUES ($1, $2, 0, 0)`, step, id)
		exec(`INSERT INTO escalation_policy_targets (step_id, kind, channel_id) VALUES ($1, 'channel', $2)`, step, channel)
		return id
	}
	nobody := policy("nobody", [3]any{"user", silent, nil}, [3]any{"schedule", nil, schedule(nil)})
	nobodyStep := uuid.UUID{}
	require.NoError(t, db.QueryRowContext(ctx, `SELECT id FROM escalation_policy_steps WHERE policy_id = $1`, nobody).Scan(&nobodyStep))
	exec(`INSERT INTO escalation_policy_targets (step_id, position, kind, channel_id) VALUES ($1, 9, 'channel', $2)`, nobodyStep, disabledChannel)
	return map[string]uuid.UUID{
		"person":   policy("person", [3]any{"user", pageable, nil}),
		"admins":   policy("admins", [3]any{"all_admins", nil, nil}),
		"schedule": policy("schedule", [3]any{"schedule", nil, schedule(&pageable)}),
		"channel":  channelPolicy("channel", activeChannel),
		"nobody":   nobody,
	}
}

func setEscalationPolicy(ctx context.Context, t *testing.T, db *shareddb.Client, monitorID, policyID uuid.UUID) {
	t.Helper()
	_, err := db.ExecContext(ctx, `UPDATE monitors SET notification_mode = 'policy', escalation_policy_id = $2 WHERE id = $1`, monitorID, policyID)
	require.NoError(t, err)
}

func setTenantDefaultPolicy(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID, policyID uuid.UUID) {
	t.Helper()
	_, err := db.ExecContext(ctx, `UPDATE tenants SET default_escalation_policy_id = $2 WHERE id = $1`, tenantID, policyID)
	require.NoError(t, err)
}

// Each policy shape gets the verdict its targets earn, with the policy named
// on the status; a default-mode monitor under a workspace default policy
// follows that policy instead of the default channels.
func TestEscalationPolicyReachability(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")
	active := insertChannel(ctx, t, dbClient, tenantID, "active", true)
	disabled := insertChannel(ctx, t, dbClient, tenantID, "disabled", false)
	policies := newPolicyFixtures(ctx, t, dbClient, tenantID, active, disabled)
	repo := monitorsvc.NewPostgresRepository(dbClient)

	want := map[string]bool{"person": true, "admins": true, "schedule": true, "channel": true, "nobody": false}
	monitors := map[string]uuid.UUID{}
	var ids []uuid.UUID
	for name, policy := range policies {
		id := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, name)
		setEscalationPolicy(ctx, t, dbClient, id, policy)
		monitors[name] = id
		ids = append(ids, id)
	}
	inherits := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "inherits")
	addTenantDefaultChannel(ctx, t, dbClient, tenantID, active, 0)
	setTenantDefaultPolicy(ctx, t, dbClient, tenantID, policies["nobody"])
	ids = append(ids, inherits)

	routing, err := repo.GetAlertRoutingForMonitors(ctx, ids)
	require.NoError(t, err)
	for name, id := range monitors {
		st := routing[id]
		require.Equalf(t, want[name], st.Reachable, "policy %s", name)
		require.Equal(t, alertrouting.SourcePolicy, st.Source)
		require.NotNil(t, st.EscalationPolicyID)
		require.Equal(t, policies[name], *st.EscalationPolicyID)
		if !want[name] {
			require.Equal(t, alertrouting.ReasonPolicyNoTargets, st.Reason)
		}
	}
	st := routing[inherits]
	require.False(t, st.Reachable, "the default policy replaces the (reachable) default channels")
	require.Equal(t, alertrouting.SourceTenantDefault, st.Source)
	require.Equal(t, alertrouting.ReasonPolicyNoTargets, st.Reason)
	require.Equal(t, policies["nobody"], *st.EscalationPolicyID)
}

// A monitor enters 'policy' mode with its policy (create and update),
// switches policy alone, refuses another workspace's policy, and leaving the
// mode drops the reference.
func TestMonitorEscalationPolicyAssignment(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")
	other := testutil.InsertTenant(ctx, t, dbClient, "")
	active := insertChannel(ctx, t, dbClient, tenantID, "active", true)
	disabled := insertChannel(ctx, t, dbClient, tenantID, "disabled", false)
	policies := newPolicyFixtures(ctx, t, dbClient, tenantID, active, disabled)
	foreign := newPolicyFixtures(ctx, t, dbClient, other, insertChannel(ctx, t, dbClient, other, "a", true), insertChannel(ctx, t, dbClient, other, "d", false))
	service := monitorsvc.NewService(monitorsvc.NewPostgresRepository(dbClient))

	str := func(s string) *string { return &s }
	created, err := service.CreateMonitor(ctx, tenantID, &models.CreateMonitorRequest{
		Name: "api", Type: models.MonitorTypeHTTP, Config: []byte(`{"url":"https://example.com","method":"GET"}`),
		IntervalSeconds: 60, TimeoutSeconds: 10,
		NotificationMode: str("policy"), EscalationPolicyID: str(policies["person"].String()),
	})
	require.NoError(t, err)
	require.Equal(t, "policy", created.NotificationMode)
	require.Equal(t, policies["person"], *created.EscalationPolicyID)

	got, err := service.GetMonitor(ctx, tenantID, created.ID)
	require.NoError(t, err)
	require.Equal(t, policies["person"], *got.EscalationPolicyID)
	require.Equal(t, alertrouting.SourcePolicy, got.AlertRouting.Source)
	require.True(t, got.AlertRouting.Reachable)

	updated, err := service.UpdateMonitor(ctx, tenantID, created.ID, &models.UpdateMonitorRequest{EscalationPolicyID: str(policies["nobody"].String())})
	require.NoError(t, err)
	require.Equal(t, policies["nobody"], *updated.EscalationPolicyID)

	_, err = service.UpdateMonitor(ctx, tenantID, created.ID, &models.UpdateMonitorRequest{EscalationPolicyID: str(foreign["person"].String())})
	require.ErrorIs(t, err, monitorsvc.ErrEscalationPolicyNotFound)

	updated, err = service.UpdateMonitor(ctx, tenantID, created.ID, &models.UpdateMonitorRequest{NotificationMode: str("default")})
	require.NoError(t, err)
	require.Equal(t, "default", updated.NotificationMode)
	require.Nil(t, updated.EscalationPolicyID, "leaving 'policy' drops the reference")

	_, err = service.UpdateMonitor(ctx, tenantID, created.ID, &models.UpdateMonitorRequest{NotificationMode: str("policy")})
	require.ErrorIs(t, err, monitorsvc.ErrEscalationPolicyNotFound, "entering 'policy' needs a policy")
	updated, err = service.UpdateMonitor(ctx, tenantID, created.ID, &models.UpdateMonitorRequest{
		NotificationMode: str("policy"), EscalationPolicyID: str(policies["channel"].String())})
	require.NoError(t, err)
	require.Equal(t, policies["channel"], *updated.EscalationPolicyID)
}
