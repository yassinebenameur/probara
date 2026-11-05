//go:build !unit

package escalation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	svc "github.com/yassinebenameur/probara/api/internal/services/escalation"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	shared "github.com/yassinebenameur/probara/shared/escalation"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func exec(ctx context.Context, t *testing.T, db *shareddb.Client, q string, args ...any) {
	t.Helper()
	_, err := db.ExecContext(ctx, q, args...)
	require.NoError(t, err)
}

func channel(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	exec(ctx, t, db, `INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active)
		VALUES ($1, $2, $3, 'webhook', '{"url":"https://example.com/h"}'::jsonb, TRUE)`, id, tenantID, name)
	return id
}

func member(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID uuid.UUID, name string, contact bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	exec(ctx, t, db, `INSERT INTO admin_users (id, username, password_hash) VALUES ($1, $2, 'x')`, id, name)
	exec(ctx, t, db, `INSERT INTO tenant_memberships (admin_user_id, tenant_id, role) VALUES ($1, $2, 'editor')`, id, tenantID)
	if contact {
		exec(ctx, t, db, `INSERT INTO user_contacts (user_id, kind, address, verified_at) VALUES ($1, 'email', 'x@example.com', NOW())`, id)
	}
	return id
}

func idPtr(id uuid.UUID) *uuid.UUID { return &id }

func TestEscalationPolicyCRUD(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	tenantID := testutil.InsertTenant(ctx, t, db, "policies")
	other := testutil.InsertTenant(ctx, t, db, "other")
	s := svc.NewService(db)

	alice := member(ctx, t, db, tenantID, "alice", true)
	quiet := member(ctx, t, db, tenantID, "quiet", false)
	room := channel(ctx, t, db, tenantID, "room")
	foreign := channel(ctx, t, db, other, "foreign")

	req := svc.WriteRequest{Name: "Primary", Steps: []svc.Step{
		{DelaySeconds: 0, Targets: []svc.Target{{Kind: shared.TargetUser, UserID: &alice}, {Kind: shared.TargetUser, UserID: &quiet}}},
		{DelaySeconds: 600, Targets: []svc.Target{{Kind: shared.TargetChannel, ChannelID: &room, NotifyAfterAck: true}, {Kind: shared.TargetAllAdmins}}},
	}}
	p, err := s.Create(ctx, tenantID, req)
	require.NoError(t, err)
	require.Equal(t, 1800, p.RepeatAfterSeconds, "repeat_after defaults to 30 minutes")
	require.Len(t, p.Steps, 2)
	require.Equal(t, "alice", p.Steps[0].Targets[0].Label)
	require.True(t, p.Steps[0].Targets[0].Reachable)
	require.False(t, p.Steps[0].Targets[1].Reachable, "a user without a verified contact pages nobody")
	require.True(t, p.Steps[1].Targets[0].NotifyAfterAck)
	require.Equal(t, "All workspace admins", p.Steps[1].Targets[1].Label)

	_, err = s.Create(ctx, tenantID, req)
	var conflict svc.Conflict
	require.True(t, errors.As(err, &conflict), "duplicate name: %v", err)

	bad := req
	bad.Name = "Foreign"
	bad.Steps = []svc.Step{{Targets: []svc.Target{{Kind: shared.TargetChannel, ChannelID: &foreign}}}}
	_, err = s.Create(ctx, tenantID, bad)
	var invalid svc.Invalid
	require.True(t, errors.As(err, &invalid), "another workspace's channel: %v", err)

	bad.Steps = []svc.Step{{DelaySeconds: 600, Targets: []svc.Target{{Kind: shared.TargetAllAdmins}}}, {DelaySeconds: 60, Targets: []svc.Target{{Kind: shared.TargetUser, UserID: &alice}}}}
	_, err = s.Create(ctx, tenantID, bad)
	require.True(t, errors.As(err, &invalid), "decreasing delays: %v", err)

	req.Name = "Primary v2"
	req.RepeatCount = 2
	req.Steps = req.Steps[:1]
	p, err = s.Replace(ctx, tenantID, p.ID, req)
	require.NoError(t, err)
	require.Equal(t, "Primary v2", p.Name)
	require.Equal(t, 2, p.RepeatCount)
	require.Len(t, p.Steps, 1)

	_, err = s.Get(ctx, other, p.ID)
	require.ErrorIs(t, err, svc.ErrNotFound, "another workspace cannot read it")

	// In use: refused until the monitor and the workspace default move off it.
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "api")
	exec(ctx, t, db, `UPDATE monitors SET notification_mode = 'policy', escalation_policy_id = $2 WHERE id = $1`, monitorID, p.ID)
	exec(ctx, t, db, `UPDATE tenants SET default_escalation_policy_id = $2 WHERE id = $1`, tenantID, p.ID)
	list, err := s.List(ctx, tenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, 1, list[0].MonitorCount)
	require.True(t, list[0].TenantDefault)
	require.True(t, errors.As(s.Delete(ctx, tenantID, p.ID), &conflict))
	exec(ctx, t, db, `UPDATE tenants SET default_escalation_policy_id = NULL WHERE id = $1`, tenantID)
	require.True(t, errors.As(s.Delete(ctx, tenantID, p.ID), &conflict))
	// A deleted monitor no longer holds it; the trigger clears the reference.
	exec(ctx, t, db, `UPDATE monitors SET deleted_at = NOW() WHERE id = $1`, monitorID)
	require.NoError(t, s.Delete(ctx, tenantID, p.ID))
	var mode string
	var ref *uuid.UUID
	require.NoError(t, db.QueryRowContext(ctx, `SELECT notification_mode, escalation_policy_id FROM monitors WHERE id = $1`, monitorID).Scan(&mode, &ref))
	require.Equal(t, "default", mode)
	require.Nil(t, ref)
}

func TestConvertRoutesToPolicy(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	tenantID := testutil.InsertTenant(ctx, t, db, "convert")
	s := svc.NewService(db)
	first := channel(ctx, t, db, tenantID, "first")
	second := channel(ctx, t, db, tenantID, "second")
	late := channel(ctx, t, db, tenantID, "late")

	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "Checkout")
	exec(ctx, t, db, `UPDATE monitors SET notification_mode = 'custom' WHERE id = $1`, monitorID)
	exec(ctx, t, db, `INSERT INTO monitor_channels (monitor_id, channel_id, delay_seconds, notify_after_ack) VALUES
		($1, $2, 0, FALSE), ($1, $3, 0, TRUE), ($1, $4, 900, FALSE)`, monitorID, first, second, late)

	editor := member(ctx, t, db, tenantID, "converter", false)
	sessionCtx := ctxpkg.WithAdminID(ctx, editor.String())
	p, err := s.ConvertRoutes(sessionCtx, tenantID, svc.ConvertRequest{MonitorID: idPtr(monitorID), Assign: true})
	require.NoError(t, err)
	require.Equal(t, "Checkout routes", p.Name)
	require.Len(t, p.Steps, 2, "routes sharing a delay become one step")
	require.Equal(t, 0, p.Steps[0].DelaySeconds)
	require.Len(t, p.Steps[0].Targets, 2)
	require.Equal(t, 900, p.Steps[1].DelaySeconds)
	require.Equal(t, 1, p.MonitorCount)
	var mode string
	var ref uuid.UUID
	require.NoError(t, db.QueryRowContext(ctx, `SELECT notification_mode, escalation_policy_id FROM monitors WHERE id = $1`, monitorID).Scan(&mode, &ref))
	require.Equal(t, "policy", mode)
	require.Equal(t, p.ID, ref)

	// Assigning the policy is a routing change: it records a config version
	// under the caller's session, so the next unrelated edit's diff does not
	// pick it up under someone else's name.
	var versions int
	var source, actorType, snapMode string
	var actorID uuid.UUID
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT COUNT(*) OVER (), source, actor_type, actor_id, snapshot->>'notification_mode'
		FROM monitor_config_versions WHERE monitor_id = $1 ORDER BY version DESC LIMIT 1`, monitorID).
		Scan(&versions, &source, &actorType, &actorID, &snapMode))
	require.Equal(t, 1, versions)
	require.Equal(t, "ui", source)
	require.Equal(t, "user", actorType)
	require.Equal(t, editor, actorID)
	require.Equal(t, "policy", snapMode)

	_, err = s.ConvertRoutes(ctx, tenantID, svc.ConvertRequest{MonitorID: idPtr(monitorID)})
	var invalid svc.Invalid
	require.True(t, errors.As(err, &invalid), "a policy-mode monitor has no custom routes to convert: %v", err)

	_, err = s.ConvertRoutes(ctx, tenantID, svc.ConvertRequest{})
	require.True(t, errors.As(err, &invalid), "no workspace default routes: %v", err)
	exec(ctx, t, db, `INSERT INTO tenant_default_channels (tenant_id, channel_id, delay_seconds, position) VALUES ($1, $2, 300, 0)`, tenantID, late)
	p, err = s.ConvertRoutes(ctx, tenantID, svc.ConvertRequest{})
	require.NoError(t, err)
	require.Equal(t, "Workspace default routes", p.Name)
	require.False(t, p.TenantDefault, "converting the defaults does not assign them")
}
