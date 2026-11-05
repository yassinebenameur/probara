//go:build !unit

package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/yassinebenameur/probara/api/internal/models"
	adminusersservice "github.com/yassinebenameur/probara/api/internal/services/adminusers"
	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	"github.com/yassinebenameur/probara/shared/tenancy"
	"github.com/yassinebenameur/probara/shared/testutil"
)

type workspaceFixture struct {
	t      *testing.T
	ctx    context.Context
	db     *db.Client
	http   *httptest.Server
	secret string
}

// newWorkspaceFixture runs the real router (auth middleware, audit, role
// gates) against a scratch database. jitTenant is the configured OIDC JIT
// default workspace.
func newWorkspaceFixture(t *testing.T, jitTenant uuid.UUID) *workspaceFixture {
	t.Helper()
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	cfg := &config.APIConfig{}
	cfg.ServiceName = "api-test"
	cfg.NATSURL = "nats://127.0.0.1:1"
	cfg.AdminJWTSecret = "workspace-test-secret-workspace-32b"
	cfg.AdminBcryptCost = 4
	cfg.OIDC.JITDefaultTenantID = jitTenant.String()
	s := NewServer(cfg, logger.New("api-test", "error"), metrics.NewRegistry("api-test"), dbClient)
	s.auditRecorder.Start()
	t.Cleanup(s.auditRecorder.Stop)
	srv := httptest.NewServer(s.http.Handler)
	t.Cleanup(srv.Close)
	return &workspaceFixture{t: t, ctx: ctx, db: dbClient, http: srv, secret: cfg.AdminJWTSecret}
}

// session creates a user and returns an access-token cookie for it.
func (f *workspaceFixture) session(username, platformRole string, memberships []models.MembershipInput) *http.Cookie {
	f.t.Helper()
	password := "correct-horse-battery"
	user, err := adminusersservice.NewService(f.db, 4).CreateUser(f.ctx, &models.CreateAdminUserRequest{
		Username: username, Password: &password, PlatformRole: platformRole, Memberships: memberships,
	})
	require.NoError(f.t, err)
	token, err := auth.CreateAdminToken(user.ID.String(), f.secret, time.Hour)
	require.NoError(f.t, err)
	return &http.Cookie{Name: "admin_access", Value: token}
}

// do sends a request as cookie (or, when cookie is nil, as bearer API key
// apiKey) scoped to tenant (uuid.Nil: no tenant) and returns the status and
// body.
func (f *workspaceFixture) do(cookie *http.Cookie, apiKey, method, path string, tenant uuid.UUID, body any) (int, string) {
	f.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(f.t, err)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(f.ctx, method, f.http.URL+path, reader)
	require.NoError(f.t, err)
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	} else {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	if tenant != uuid.Nil {
		req.Header.Set("X-Tenant-ID", tenant.String())
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(f.t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// apiKey inserts a write-scope API key for tenant (cheap bcrypt cost).
func (f *workspaceFixture) apiKey(tenant uuid.UUID) string {
	f.t.Helper()
	key := "pk_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	hash, err := bcrypt.GenerateFromPassword([]byte(key), bcrypt.MinCost)
	require.NoError(f.t, err)
	prefix := sha256.Sum256([]byte(key))
	_, err = f.db.ExecContext(f.ctx, `
		INSERT INTO api_keys (tenant_id, name, key_hash, key_prefix, scope) VALUES ($1, 'workspace-test', $2, $3, $4)`,
		tenant, string(hash), hex.EncodeToString(prefix[:]), auth.ScopeWrite)
	require.NoError(f.t, err)
	return key
}

func (f *workspaceFixture) liveTenantNames(cookie *http.Cookie) []string {
	f.t.Helper()
	status, body := f.do(cookie, "", http.MethodGet, "/api/v1/tenants", uuid.Nil, nil)
	require.Equal(f.t, http.StatusOK, status, body)
	var list models.TenantListResponse
	require.NoError(f.t, json.Unmarshal([]byte(body), &list))
	names := []string{}
	for _, tenant := range list.Items {
		names = append(names, tenant.Name)
	}
	return names
}

func TestWorkspaceManagement(t *testing.T) {
	jit := uuid.New()
	f := newWorkspaceFixture(t, jit)
	ctx := f.ctx

	tenantA := testutil.InsertTenant(ctx, t, f.db, "Tenant A")
	testutil.InsertTenant(ctx, t, f.db, "Tenant B")
	_, err := f.db.ExecContext(ctx, `INSERT INTO tenants (id, name) VALUES ($1, 'SSO landing')`, jit)
	require.NoError(t, err)
	monitorA := testutil.InsertHTTPMonitor(ctx, t, f.db, tenantA, "")

	root := f.session("root", auth.PlatformRoleSuperadmin, nil)
	member := f.session("owner", auth.PlatformRoleMember, []models.MembershipInput{{TenantID: tenantA, Role: auth.RoleAdmin}})
	key := f.apiKey(tenantA)

	t.Run("only superadmins manage workspaces", func(t *testing.T) {
		for _, req := range []struct {
			method, path string
			body         any
		}{
			{http.MethodPost, "/api/v1/tenants", models.CreateTenantRequest{Name: "Sneaky"}},
			{http.MethodPatch, "/api/v1/tenants/" + tenantA.String(), models.RenameTenantRequest{Name: "Sneaky"}},
			{http.MethodDelete, "/api/v1/tenants/" + tenantA.String(), models.DeleteTenantRequest{ConfirmName: "Tenant A"}},
		} {
			status, body := f.do(member, "", req.method, req.path, tenantA, req.body)
			require.Equalf(t, http.StatusForbidden, status, "tenant admin %s %s: %s", req.method, req.path, body)
			status, body = f.do(nil, key, req.method, req.path, uuid.Nil, req.body)
			require.Equalf(t, http.StatusUnauthorized, status, "API key %s %s: %s", req.method, req.path, body)
		}
		require.ElementsMatch(t, []string{"Default", "Tenant A", "Tenant B", "SSO landing"}, f.liveTenantNames(root))
	})

	var acme models.Tenant
	t.Run("create and rename", func(t *testing.T) {
		status, body := f.do(root, "", http.MethodPost, "/api/v1/tenants", uuid.Nil, models.CreateTenantRequest{Name: "  "})
		require.Equal(t, http.StatusBadRequest, status, body)
		status, body = f.do(root, "", http.MethodPost, "/api/v1/tenants", uuid.Nil, models.CreateTenantRequest{Name: "Tenant A"})
		require.Equal(t, http.StatusConflict, status, body)

		status, body = f.do(root, "", http.MethodPost, "/api/v1/tenants", uuid.Nil, models.CreateTenantRequest{Name: " Acme "})
		require.Equal(t, http.StatusCreated, status, body)
		require.NoError(t, json.Unmarshal([]byte(body), &acme))
		require.Equal(t, "Acme", acme.Name)

		status, body = f.do(root, "", http.MethodPatch, "/api/v1/tenants/"+acme.ID.String(), uuid.Nil, models.RenameTenantRequest{Name: "Tenant B"})
		require.Equal(t, http.StatusConflict, status, body)
		status, body = f.do(root, "", http.MethodPatch, "/api/v1/tenants/"+acme.ID.String(), uuid.Nil, models.RenameTenantRequest{Name: "Acme Corp"})
		require.Equal(t, http.StatusOK, status, body)
		status, body = f.do(root, "", http.MethodPatch, "/api/v1/tenants/"+uuid.NewString(), uuid.Nil, models.RenameTenantRequest{Name: "Ghost"})
		require.Equal(t, http.StatusNotFound, status, body)
	})

	t.Run("protected workspaces and typed confirmation", func(t *testing.T) {
		for _, id := range []string{tenancy.DefaultTenantID, jit.String()} {
			status, body := f.do(root, "", http.MethodDelete, "/api/v1/tenants/"+id, uuid.Nil, models.DeleteTenantRequest{ConfirmName: "Default"})
			require.Equalf(t, http.StatusConflict, status, "delete %s: %s", id, body)
		}
		status, body := f.do(root, "", http.MethodDelete, "/api/v1/tenants/"+tenantA.String(), uuid.Nil, models.DeleteTenantRequest{ConfirmName: "tenant a"})
		require.Equal(t, http.StatusBadRequest, status, body)
		status, body = f.do(root, "", http.MethodDelete, "/api/v1/tenants/"+tenantA.String(), uuid.Nil, nil)
		require.Equal(t, http.StatusBadRequest, status, body)
	})

	t.Run("delete cuts off access at once", func(t *testing.T) {
		// Both work before the delete.
		status, body := f.do(member, "", http.MethodGet, "/api/v1/monitors", tenantA, nil)
		require.Equal(t, http.StatusOK, status, body)
		status, body = f.do(nil, key, http.MethodGet, "/api/v1/monitors", uuid.Nil, nil)
		require.Equal(t, http.StatusOK, status, body)

		status, body = f.do(root, "", http.MethodDelete, "/api/v1/tenants/"+tenantA.String(), tenantA, models.DeleteTenantRequest{ConfirmName: "Tenant A"})
		require.Equal(t, http.StatusOK, status, body)
		var result models.DeleteTenantResponse
		require.NoError(t, json.Unmarshal([]byte(body), &result))
		require.Equal(t, int64(1), result.Monitors)

		for name, cookie := range map[string]*http.Cookie{"superadmin": root, "former member": member} {
			status, body := f.do(cookie, "", http.MethodGet, "/api/v1/monitors", tenantA, nil)
			require.Equalf(t, http.StatusForbidden, status, "%s scoped to the deleted workspace: %s", name, body)
		}
		status, body = f.do(nil, key, http.MethodGet, "/api/v1/monitors", uuid.Nil, nil)
		require.Equal(t, http.StatusUnauthorized, status, "revoked API key: %s", body)

		require.NotContains(t, f.liveTenantNames(root), "Tenant A")
		require.Empty(t, f.liveTenantNames(member), "the member's only workspace is gone")

		var deletedAt *time.Time
		require.NoError(t, f.db.QueryRowContext(ctx, `SELECT deleted_at FROM monitors WHERE id = $1`, monitorA).Scan(&deletedAt))
		require.NotNil(t, deletedAt, "the workspace's monitors are tombstoned for the purger")
		var rows int
		require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tenants WHERE id = $1`, tenantA).Scan(&rows))
		require.Equal(t, 1, rows, "the API never hard-deletes the tenant row")

		// Deleting it again is a not-found: it is gone for every reader.
		status, body = f.do(root, "", http.MethodDelete, "/api/v1/tenants/"+tenantA.String(), uuid.Nil, models.DeleteTenantRequest{ConfirmName: "Tenant A"})
		require.Equal(t, http.StatusNotFound, status, body)
	})

	t.Run("a deleted workspace's name is free again", func(t *testing.T) {
		status, body := f.do(root, "", http.MethodPost, "/api/v1/tenants", uuid.Nil, models.CreateTenantRequest{Name: "Tenant A"})
		require.Equal(t, http.StatusCreated, status, body)
	})

	t.Run("the last live workspace cannot be deleted", func(t *testing.T) {
		// Without the protected workspaces, Acme Corp and the new Tenant A
		// and Tenant B are what is left.
		_, err := f.db.ExecContext(ctx, `UPDATE tenants SET deleted_at = NOW() WHERE id IN ($1, $2)`, tenancy.DefaultTenantID, jit)
		require.NoError(t, err)
		var live []struct {
			id   uuid.UUID
			name string
		}
		rows, err := f.db.QueryContext(ctx, `SELECT id, name FROM tenants WHERE deleted_at IS NULL ORDER BY name`)
		require.NoError(t, err)
		for rows.Next() {
			var item struct {
				id   uuid.UUID
				name string
			}
			require.NoError(t, rows.Scan(&item.id, &item.name))
			live = append(live, item)
		}
		require.NoError(t, rows.Close())
		require.Len(t, live, 3)
		for i, tenant := range live {
			status, body := f.do(root, "", http.MethodDelete, "/api/v1/tenants/"+tenant.id.String(), uuid.Nil, models.DeleteTenantRequest{ConfirmName: tenant.name})
			if i < len(live)-1 {
				require.Equalf(t, http.StatusOK, status, "delete %s: %s", tenant.name, body)
			} else {
				require.Equalf(t, http.StatusConflict, status, "delete the last workspace %s: %s", tenant.name, body)
			}
		}
	})

	t.Run("workspace events are audited with a name snapshot", func(t *testing.T) {
		require.Eventually(t, func() bool {
			var n int
			err := f.db.QueryRowContext(ctx, `
				SELECT COUNT(*) FROM audit_log
				WHERE action = 'tenant.delete' AND resource_id = $1 AND tenant_id IS NULL
				  AND details->>'name' = 'Tenant A' AND outcome = 'success'`, tenantA.String()).Scan(&n)
			return err == nil && n == 1
		}, 10*time.Second, 200*time.Millisecond)
		var actions []string
		rows, err := f.db.QueryContext(ctx, `SELECT DISTINCT action FROM audit_log WHERE resource_type = 'tenant' ORDER BY action`)
		require.NoError(t, err)
		for rows.Next() {
			var action string
			require.NoError(t, rows.Scan(&action))
			actions = append(actions, action)
		}
		require.NoError(t, rows.Close())
		require.Subset(t, actions, []string{"tenant.create", "tenant.rename", "tenant.delete"})
	})
}
