//go:build !unit

package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/handlers/alertactions"
	"github.com/yassinebenameur/probara/api/internal/models"
	locationservice "github.com/yassinebenameur/probara/api/internal/services/locations"
	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/tenancy"
)

// login signs a user in (createUser's password) and returns a cookie client.
func (f *mcpFixture) login(username string) *http.Client {
	f.t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	postJSON(f.t, c, f.http.URL+"/api/v1/auth/login", nil,
		map[string]any{"username": username, "password": "correct-horse-battery"}, http.StatusOK, nil)
	return c
}

// call sends one request. tenant uuid.Nil sends no X-Tenant-ID; bearer, when
// set, authenticates instead of the client's cookies.
func (f *mcpFixture) call(c *http.Client, bearer, method, path string, tenant uuid.UUID, body any) (int, string) {
	f.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(f.t, err)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, f.http.URL+path, reader)
	require.NoError(f.t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if tenant != uuid.Nil {
		req.Header.Set("X-Tenant-ID", tenant.String())
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if c == nil {
		c = http.DefaultClient
	}
	res, err := c.Do(req)
	require.NoError(f.t, err)
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(data)
}

func TestWorkspaceManagement(t *testing.T) {
	f := newMCPFixture(t)
	f.createUser("root", auth.PlatformRoleSuperadmin, nil)
	f.createUser("member", auth.PlatformRoleMember, []models.MembershipInput{{TenantID: f.tenantA, Role: auth.RoleAdmin}})
	root, member := f.login("root"), f.login("member")
	key := f.apiKey(f.tenantA, auth.ScopeWrite)
	// Migration 000030 seeds the default workspace.
	_, err := f.db.ExecContext(f.ctx, `INSERT INTO tenants (id, name) VALUES ($1, 'Default') ON CONFLICT (id) DO UPDATE SET name = 'Default'`, tenancy.DefaultTenantID)
	require.NoError(t, err)

	t.Run("superadmins only", func(t *testing.T) {
		// RequireSuperadmin answers a member 403 and an API key (never an
		// admin) 401.
		for _, c := range []struct {
			client *http.Client
			bearer string
			want   int
		}{{member, "", http.StatusForbidden}, {nil, key, http.StatusUnauthorized}} {
			status, body := f.call(c.client, c.bearer, http.MethodPost, "/api/v1/tenants", uuid.Nil, map[string]string{"name": "Sneaky"})
			require.Equal(t, c.want, status, body)
			status, body = f.call(c.client, c.bearer, http.MethodGet, "/api/v1/tenants/overview", uuid.Nil, nil)
			require.Equal(t, c.want, status, body)
			status, body = f.call(c.client, c.bearer, http.MethodDelete, "/api/v1/tenants/"+f.tenantB.String(), uuid.Nil, map[string]string{"confirm_name": "Tenant B"})
			require.Equal(t, c.want, status, body)
		}
	})

	var ops models.Tenant
	t.Run("create and name rules", func(t *testing.T) {
		status, body := f.call(root, "", http.MethodPost, "/api/v1/tenants", uuid.Nil, map[string]string{"name": "  Ops  "})
		require.Equal(t, http.StatusCreated, status, body)
		require.NoError(t, json.Unmarshal([]byte(body), &ops))
		require.Equal(t, "Ops", ops.Name)
		for _, bad := range []string{"", "   ", strings.Repeat("x", 101), "tab\there", "line\nbreak"} {
			status, body = f.call(root, "", http.MethodPost, "/api/v1/tenants", uuid.Nil, map[string]string{"name": bad})
			require.Equalf(t, http.StatusBadRequest, status, "%q: %s", bad, body)
		}
		for _, taken := range []string{"Ops", "Tenant A"} {
			status, body = f.call(root, "", http.MethodPost, "/api/v1/tenants", uuid.Nil, map[string]string{"name": taken})
			require.Equalf(t, http.StatusConflict, status, "%q: %s", taken, body)
		}
		// The new workspace is usable at once.
		status, body = f.call(root, "", http.MethodGet, "/api/v1/monitors", ops.ID, nil)
		require.Equal(t, http.StatusOK, status, body)
	})

	t.Run("rename", func(t *testing.T) {
		status, body := f.call(root, "", http.MethodPatch, "/api/v1/tenants/"+ops.ID.String(), uuid.Nil, map[string]string{"name": "Operations"})
		require.Equal(t, http.StatusOK, status, body)
		require.Contains(t, body, `"name":"Operations"`)
		status, body = f.call(root, "", http.MethodPatch, "/api/v1/tenants/"+ops.ID.String(), uuid.Nil, map[string]string{"name": "Tenant B"})
		require.Equal(t, http.StatusConflict, status, body)
		status, body = f.call(root, "", http.MethodPatch, "/api/v1/tenants/"+uuid.NewString(), uuid.Nil, map[string]string{"name": "Nobody"})
		require.Equal(t, http.StatusNotFound, status, body)
	})

	t.Run("overview counts", func(t *testing.T) {
		_, err := f.db.ExecContext(f.ctx, `
			INSERT INTO monitors (tenant_id, name, type, config, interval_seconds, timeout_seconds)
			VALUES ($1, 'a', 'http', '{}', 60, 30), ($1, 'b', 'http', '{}', 60, 30)`, f.tenantA)
		require.NoError(t, err)
		status, body := f.call(root, "", http.MethodGet, "/api/v1/tenants/overview", uuid.Nil, nil)
		require.Equal(t, http.StatusOK, status, body)
		var resp models.WorkspaceOverviewResponse
		require.NoError(t, json.Unmarshal([]byte(body), &resp))
		byName := map[string]models.WorkspaceOverview{}
		for _, w := range resp.Items {
			byName[w.Name] = w
		}
		require.Equal(t, 1, byName["Tenant A"].MemberCount)
		require.Equal(t, 2, byName["Tenant A"].MonitorCount)
		require.True(t, byName["Default"].Protected)
		require.False(t, byName["Operations"].Protected)
	})

	t.Run("delete", func(t *testing.T) {
		status, body := f.call(root, "", http.MethodDelete, "/api/v1/tenants/"+tenancy.DefaultTenantID, uuid.Nil, map[string]string{"confirm_name": "Default"})
		require.Equal(t, http.StatusConflict, status, "the default workspace: %s", body)
		status, body = f.call(root, "", http.MethodDelete, "/api/v1/tenants/"+f.tenantB.String(), uuid.Nil, map[string]string{"confirm_name": "tenant b"})
		require.Equal(t, http.StatusBadRequest, status, "a wrong confirmation: %s", body)
		status, body = f.call(root, "", http.MethodDelete, "/api/v1/tenants/"+f.tenantB.String(), uuid.Nil, nil)
		require.Equal(t, http.StatusBadRequest, status, "no confirmation: %s", body)

		status, body = f.call(root, "", http.MethodDelete, "/api/v1/tenants/"+f.tenantB.String(), uuid.Nil, map[string]string{"confirm_name": "Tenant B"})
		require.Equal(t, http.StatusOK, status, body)
		status, body = f.call(root, "", http.MethodDelete, "/api/v1/tenants/"+f.tenantB.String(), uuid.Nil, map[string]string{"confirm_name": "Tenant B"})
		require.Equal(t, http.StatusNotFound, status, "already deleted: %s", body)

		status, body = f.call(root, "", http.MethodGet, "/api/v1/tenants", uuid.Nil, nil)
		require.Equal(t, http.StatusOK, status)
		require.NotContains(t, body, f.tenantB.String())
		// The name is free again at once.
		status, body = f.call(root, "", http.MethodPost, "/api/v1/tenants", uuid.Nil, map[string]string{"name": "Tenant B"})
		require.Equal(t, http.StatusCreated, status, body)
	})

	t.Run("audited", func(t *testing.T) {
		require.Eventually(t, func() bool {
			var actions []string
			rows, err := f.db.QueryContext(f.ctx, `SELECT DISTINCT action FROM audit_log WHERE action LIKE 'tenant.%' AND outcome = 'success'`)
			if err != nil {
				return false
			}
			defer rows.Close()
			for rows.Next() {
				var a string
				_ = rows.Scan(&a)
				actions = append(actions, a)
			}
			return len(actions) == 3
		}, 10*time.Second, 100*time.Millisecond, "tenant.create, tenant.rename and tenant.delete are recorded")
	})
}

// Without a default workspace (an install that removed it out of band) the
// last live workspace still cannot go.
func TestLastWorkspaceCannotBeDeleted(t *testing.T) {
	f := newMCPFixture(t)
	_, err := f.db.ExecContext(f.ctx, `DELETE FROM tenants WHERE id = $1`, tenancy.DefaultTenantID)
	require.NoError(t, err)
	f.createUser("root", auth.PlatformRoleSuperadmin, nil)
	root := f.login("root")
	status, body := f.call(root, "", http.MethodDelete, "/api/v1/tenants/"+f.tenantB.String(), uuid.Nil, map[string]string{"confirm_name": "Tenant B"})
	require.Equal(t, http.StatusOK, status, body)
	status, body = f.call(root, "", http.MethodDelete, "/api/v1/tenants/"+f.tenantA.String(), uuid.Nil, map[string]string{"confirm_name": "Tenant A"})
	require.Equal(t, http.StatusConflict, status, body)
	require.Contains(t, body, "last workspace")
}

// Every credential of a deleted workspace is refused at lookup, from the
// moment the delete commits: sessions and MCP tokens scoped to it, its API
// keys (OTLP ingest included), push tokens, discovery collector tokens,
// private-location worker credentials, acknowledge links and AI analysis.
func TestDeletedWorkspaceCredentialsAreRefused(t *testing.T) {
	f := newMCPFixture(t)
	doomed, live := f.tenantA, f.tenantB
	f.createUser("root", auth.PlatformRoleSuperadmin, nil)
	f.createUser("member", auth.PlatformRoleMember, []models.MembershipInput{
		{TenantID: doomed, Role: auth.RoleAdmin},
		{TenantID: live, Role: auth.RoleViewer},
	})
	root, member := f.login("root"), f.login("member")
	mcpToken := f.oauthSignIn("member", "correct-horse-battery", "read")["access_token"].(string)
	doomedKey, liveKey := f.apiKey(doomed, auth.ScopeWrite), f.apiKey(live, auth.ScopeWrite)

	pushToken := "push-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err := f.db.ExecContext(f.ctx, `
		INSERT INTO monitors (tenant_id, name, type, config, interval_seconds, timeout_seconds, push_token)
		VALUES ($1, 'cron', 'push', '{}', 60, 60, $2)`, doomed, pushToken)
	require.NoError(t, err)
	integration := uuid.New()
	collectorToken := strings.Repeat("t", 11) + strings.ReplaceAll(uuid.NewString(), "-", "")
	digest := sha256.Sum256([]byte(collectorToken))
	_, err = f.db.ExecContext(f.ctx, `
		INSERT INTO discovery_integrations (id, tenant_id, name, namespaces, home_namespace, token_hash)
		VALUES ($1, $2, 'cluster', ARRAY['default'], 'default', $3)`, integration, doomed, hex.EncodeToString(digest[:]))
	require.NoError(t, err)
	location := uuid.New()
	_, err = f.db.ExecContext(f.ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, worker_credential) VALUES ($1, $2, 'edge', 'edge', 'worker-secret')`, location, doomed)
	require.NoError(t, err)
	channel := uuid.New()
	_, err = f.db.ExecContext(f.ctx, `
		INSERT INTO alert_channels (id, tenant_id, name, type, config) VALUES ($1, $2, 'hook', 'webhook', '{}')`, channel, doomed)
	require.NoError(t, err)

	locations := locationservice.NewService(f.db)
	channels := alertactions.DBChannels(f.db)
	mcpCall := func() int {
		status, _ := f.call(nil, mcpToken, http.MethodPost, "/api/v1/mcp", uuid.Nil,
			map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"})
		return status
	}
	collector := func() int {
		status, _ := f.call(nil, collectorToken, http.MethodGet, "/api/v1/discovery-collectors/"+integration.String()+"/config", uuid.Nil, nil)
		return status
	}
	push := func() int {
		status, _ := f.call(nil, "", http.MethodGet, "/api/v1/push/"+pushToken, uuid.Nil, nil)
		return status
	}

	// Everything works while the workspace is live.
	for _, c := range []*http.Client{root, member} {
		status, body := f.call(c, "", http.MethodGet, "/api/v1/monitors", doomed, nil)
		require.Equal(t, http.StatusOK, status, body)
	}
	status, body := f.call(nil, doomedKey, http.MethodGet, "/api/v1/monitors", uuid.Nil, nil)
	require.Equal(t, http.StatusOK, status, body)
	require.NotEqual(t, http.StatusUnauthorized, mcpCall())
	require.Equal(t, http.StatusOK, collector())
	require.Equal(t, http.StatusOK, push())
	require.NoError(t, locations.AuthenticateWorker(f.ctx, location.String(), "worker-secret"))
	_, ok, err := channels.ChannelName(f.ctx, doomed, channel)
	require.NoError(t, err)
	require.True(t, ok)
	enabled, err := ai.EffectiveEnabled(f.ctx, f.db, doomed, false)
	require.NoError(t, err)
	require.True(t, enabled)

	status, body = f.call(root, "", http.MethodDelete, "/api/v1/tenants/"+doomed.String(), uuid.Nil, map[string]string{"confirm_name": "Tenant A"})
	require.Equal(t, http.StatusOK, status, body)

	for name, c := range map[string]*http.Client{"superadmin": root, "member": member} {
		status, body := f.call(c, "", http.MethodGet, "/api/v1/monitors", doomed, nil)
		require.Equalf(t, http.StatusNotFound, status, "%s session: %s", name, body)
		require.Contains(t, body, `"workspace_not_found"`)
		status, body = f.call(c, "", http.MethodGet, "/api/v1/monitors", live, nil)
		require.Equalf(t, http.StatusOK, status, "%s session, live workspace: %s", name, body)
	}
	status, body = f.call(member, "", http.MethodGet, "/api/v1/tenants", uuid.Nil, nil)
	require.Equal(t, http.StatusOK, status)
	require.NotContains(t, body, doomed.String())
	require.Contains(t, body, live.String())

	status, body = f.call(nil, doomedKey, http.MethodGet, "/api/v1/monitors", uuid.Nil, nil)
	require.Equal(t, http.StatusUnauthorized, status, "API key: %s", body)
	status, body = f.call(nil, doomedKey, http.MethodPost, "/api/v1/otlp/v1/metrics", uuid.Nil, map[string]any{})
	require.Equal(t, http.StatusUnauthorized, status, "OTLP ingest: %s", body)
	status, body = f.call(nil, liveKey, http.MethodGet, "/api/v1/monitors", uuid.Nil, nil)
	require.Equal(t, http.StatusOK, status, "a live workspace's key: %s", body)
	require.Equal(t, http.StatusUnauthorized, mcpCall(), "MCP token granted on the deleted workspace")
	require.Equal(t, http.StatusNotFound, collector())
	require.Equal(t, http.StatusNotFound, push())
	require.Error(t, locations.AuthenticateWorker(f.ctx, location.String(), "worker-secret"))
	_, ok, err = channels.ChannelName(f.ctx, doomed, channel)
	require.NoError(t, err)
	require.False(t, ok, "acknowledge links of the workspace's channels read as invalid")
	enabled, err = ai.EffectiveEnabled(f.ctx, f.db, doomed, false)
	require.NoError(t, err)
	require.False(t, enabled)

	var memberships, grants int
	require.NoError(t, f.db.QueryRowContext(f.ctx, `
		SELECT (SELECT COUNT(*) FROM tenant_memberships WHERE tenant_id = $1),
		       (SELECT COUNT(*) FROM mcp_oauth_grants WHERE tenant_id = $1 AND revoked_at IS NULL)`, doomed).Scan(&memberships, &grants))
	require.Zero(t, memberships, "memberships go with the delete")
	require.Zero(t, grants, "MCP connections are revoked with the delete")
}

// A tenant ID that does not name a workspace the caller can open gets one
// answer, 404 workspace_not_found, whether the workspace was deleted, is
// already purged, never existed, or (for a member) is not theirs: a member
// learns nothing about other workspaces, and a browser holding a stale ID
// always gets the code that makes it re-select. The workspace list itself
// must be fetched without the header (the web client never sends it there).
func TestWorkspaceNotFoundForEveryUnopenableTenant(t *testing.T) {
	f := newMCPFixture(t)
	f.createUser("root", auth.PlatformRoleSuperadmin, nil)
	f.createUser("member", auth.PlatformRoleMember, []models.MembershipInput{{TenantID: f.tenantA, Role: auth.RoleViewer}})
	root, member := f.login("root"), f.login("member")
	doomed := f.tenantB

	// Not a member of a live workspace: the same answer as for one that
	// does not exist.
	for _, id := range []uuid.UUID{doomed, uuid.New()} {
		status, body := f.call(member, "", http.MethodGet, "/api/v1/monitors", id, nil)
		require.Equal(t, http.StatusNotFound, status, body)
		require.Contains(t, body, `"workspace_not_found"`)
	}
	status, body := f.call(root, "", http.MethodGet, "/api/v1/monitors", uuid.New(), nil)
	require.Equal(t, http.StatusNotFound, status, "superadmin, unknown workspace: %s", body)
	require.Contains(t, body, `"workspace_not_found"`)

	status, body = f.call(root, "", http.MethodDelete, "/api/v1/tenants/"+doomed.String(), uuid.Nil, map[string]string{"confirm_name": "Tenant B"})
	require.Equal(t, http.StatusOK, status, body)

	// The list route with the deleted workspace still selected answers like
	// any other route scoped to it; without the header it lists the rest.
	status, body = f.call(root, "", http.MethodGet, "/api/v1/tenants", doomed, nil)
	require.Equal(t, http.StatusNotFound, status, body)
	require.Contains(t, body, `"workspace_not_found"`)
	status, body = f.call(root, "", http.MethodGet, "/api/v1/tenants", uuid.Nil, nil)
	require.Equal(t, http.StatusOK, status, body)
	require.NotContains(t, body, doomed.String())

	// Once purged (the row is gone), the answer is the same.
	_, err := f.db.ExecContext(f.ctx, `DELETE FROM tenants WHERE id = $1`, doomed)
	require.NoError(t, err)
	status, body = f.call(root, "", http.MethodGet, "/api/v1/monitors", doomed, nil)
	require.Equal(t, http.StatusNotFound, status, "superadmin, purged workspace: %s", body)
	require.Contains(t, body, `"workspace_not_found"`)
}

// Memberships and SSO group mappings only name live workspaces: a deleted
// one (row still there until the purge) and an unknown one are both refused
// with "workspace not found", never stored or turned into a 500.
func TestMembershipsAndSSOMappingsRefuseDeadWorkspaces(t *testing.T) {
	f := newMCPFixture(t)
	f.createUser("root", auth.PlatformRoleSuperadmin, nil)
	f.createUser("member", auth.PlatformRoleMember, nil)
	root := f.login("root")
	var memberID uuid.UUID
	require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT id FROM admin_users WHERE username = 'member'`).Scan(&memberID))
	status, body := f.call(root, "", http.MethodDelete, "/api/v1/tenants/"+f.tenantB.String(), uuid.Nil, map[string]string{"confirm_name": "Tenant B"})
	require.Equal(t, http.StatusOK, status, body)

	for _, dead := range []uuid.UUID{f.tenantB, uuid.New()} {
		memberships := []models.MembershipInput{{TenantID: f.tenantA, Role: auth.RoleViewer}, {TenantID: dead, Role: auth.RoleViewer}}
		status, body = f.call(root, "", http.MethodPatch, "/api/v1/users/"+memberID.String(), uuid.Nil,
			models.UpdateAdminUserRequest{Memberships: &memberships})
		require.Equal(t, http.StatusUnprocessableEntity, status, "membership edit: %s", body)
		require.Contains(t, body, "workspace not found")

		password := "correct-horse-battery"
		status, body = f.call(root, "", http.MethodPost, "/api/v1/users", uuid.Nil, models.CreateAdminUserRequest{
			Username: "new-" + dead.String()[:8], Password: &password, PlatformRole: auth.PlatformRoleMember, Memberships: memberships,
		})
		require.Equal(t, http.StatusUnprocessableEntity, status, "user create: %s", body)

		tenant := dead.String()
		status, body = f.call(root, "", http.MethodPost, "/api/v1/oidc-group-mappings", uuid.Nil, models.CreateOIDCGroupMappingRequest{
			GroupName: "ops-" + tenant[:8], TenantID: &tenant, Role: auth.RoleViewer,
		})
		require.Equal(t, http.StatusUnprocessableEntity, status, "SSO mapping: %s", body)
		require.Contains(t, body, "workspace not found")
	}
	var memberships, mappings int
	require.NoError(t, f.db.QueryRowContext(f.ctx, `
		SELECT (SELECT COUNT(*) FROM tenant_memberships WHERE admin_user_id = $1),
		       (SELECT COUNT(*) FROM oidc_group_mappings)`, memberID).Scan(&memberships, &mappings))
	require.Zero(t, memberships, "a refused edit changes nothing (the transaction rolls back)")
	require.Zero(t, mappings)
}
