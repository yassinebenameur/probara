//go:build !unit

package monitors

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	auditservice "github.com/yassinebenameur/probara/api/internal/services/audit"
	depservice "github.com/yassinebenameur/probara/api/internal/services/dependencies"
	groupservice "github.com/yassinebenameur/probara/api/internal/services/groups"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// TestMonitorConfigHistoryHTTP_Integration drives the history routes through
// the write gate and the audit middleware the way the UI and MCP reach them:
// a create that also syncs members or dependencies records one version
// holding them, member and dependency edge routes record versions, reads are
// open to viewers, and a restore re-applies a version through the update
// path, drops references that no longer exist, keeps and reports secrets,
// and is audited.
func TestMonitorConfigHistoryHTTP_Integration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, db, "history-http")
	other := testutil.InsertTenant(ctx, t, db, "history-http-other")
	memberA := testutil.InsertHTTPMonitor(ctx, t, db, tenant, "member-a")
	memberB := testutil.InsertHTTPMonitor(ctx, t, db, tenant, "member-b")
	memberC := testutil.InsertHTTPMonitor(ctx, t, db, tenant, "member-c")
	var adminID uuid.UUID
	if err := db.QueryRowContext(ctx, `INSERT INTO admin_users (username, password_hash) VALUES ('editor', 'x') RETURNING id`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}

	log := logger.New("test", "error")
	recorder := auditservice.NewRecorder(db, log)
	recorder.Start()
	svc := monitorservice.NewService(monitorservice.NewPostgresRepository(db))
	h := NewHandlers(svc, groupservice.NewService(db), nil, log, t.TempDir())
	h.ConfigureDependencies(depservice.NewService(db))
	r := chi.NewRouter()
	r.Use(middleware.AuditMutations(recorder))
	r.Use(middleware.RequireWrite)
	r.Route("/api/v1/monitors", func(r chi.Router) {
		r.Post("/", h.CreateMonitor)
		r.Patch("/{id}", h.UpdateMonitor)
		r.Post("/{id}/members", h.AddMonitorsToGroup)
		r.Post("/{id}/dependencies", h.AddMonitorDependency)
		r.Delete("/{id}/dependencies/{dependsOnId}", h.RemoveMonitorDependency)
		r.Get("/{id}/versions", h.ListMonitorVersions)
		r.Get("/{id}/versions/{version}", h.GetMonitorVersion)
		r.Get("/{id}/versions/{version}/diff", h.DiffMonitorVersion)
		r.Post("/{id}/versions/{version}/restore", h.RestoreMonitorVersion)
	})

	as := func(tenantID uuid.UUID, role string) context.Context {
		return ctxpkg.WithRole(ctxpkg.WithAdminID(ctxpkg.WithTenantID(ctx, tenantID.String()), adminID.String()), role)
	}
	editor, viewer, foreign := as(tenant, "editor"), as(tenant, "viewer"), as(other, "admin")
	call := func(reqCtx context.Context, method, path string, body any, want int) []byte {
		t.Helper()
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(method, "/api/v1/monitors"+path, &buf).WithContext(reqCtx))
		if w.Code != want {
			t.Fatalf("%s %s: status %d, want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w.Body.Bytes()
	}
	versions := func(id uuid.UUID) models.MonitorVersionListResponse {
		t.Helper()
		var out models.MonitorVersionListResponse
		if err := json.Unmarshal(call(editor, "GET", "/"+id.String()+"/versions", nil, http.StatusOK), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	sorted := func(ids ...uuid.UUID) string {
		out := make([]string, len(ids))
		for i, id := range ids {
			out[i] = id.String()
		}
		sort.Strings(out)
		return strings.Join(out, ",")
	}
	members := func(groupID uuid.UUID) string {
		t.Helper()
		var got string
		if err := db.QueryRowContext(ctx, `SELECT COALESCE(string_agg(monitor_id::text, ',' ORDER BY monitor_id::text), '') FROM monitor_groups WHERE group_id = $1`, groupID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	// Group create: one version, holding its members, by the signed-in user.
	var group models.Monitor
	_ = json.Unmarshal(call(editor, "POST", "/", map[string]any{
		"name": "edge", "type": "group", "interval_seconds": 60, "timeout_seconds": 10,
		"config": map[string]any{"monitor_ids": []string{memberA.String(), memberB.String()}},
	}, http.StatusCreated), &group)
	list := versions(group.ID)
	if len(list.Items) != 1 || list.Items[0].Source != monitorservice.VersionSourceUI || list.Items[0].ActorLabel != "editor" {
		t.Fatalf("group create: %+v", list.Items)
	}
	var v1 models.MonitorVersion
	_ = json.Unmarshal(call(viewer, "GET", "/"+group.ID.String()+"/versions/1", nil, http.StatusOK), &v1)
	if strings.Join(v1.Snapshot.MemberIDs, ",") != sorted(memberA, memberB) {
		t.Fatalf("v1 members = %v", v1.Snapshot.MemberIDs)
	}

	// Membership edit through PATCH, then through the member route.
	call(editor, "PATCH", "/"+group.ID.String(), map[string]any{
		"config": map[string]any{"monitor_ids": []string{memberC.String()}},
	}, http.StatusOK)
	list = versions(group.ID)
	if len(list.Items) != 2 || strings.Join(list.Items[0].ChangedFields, ",") != "config.monitor_ids,member_ids" {
		t.Fatalf("after edit: %+v", list.Items)
	}
	call(editor, "POST", "/"+group.ID.String()+"/members", map[string]any{"monitor_ids": []string{memberA.String()}}, http.StatusNoContent)
	if list = versions(group.ID); len(list.Items) != 3 || strings.Join(list.Items[0].ChangedFields, ",") != "member_ids" {
		t.Fatalf("after member route: %+v", list.Items)
	}

	// Viewers read history but cannot restore; another tenant sees nothing.
	var diff models.MonitorVersionDiffResponse
	_ = json.Unmarshal(call(viewer, "GET", "/"+group.ID.String()+"/versions/1/diff?against=current", nil, http.StatusOK), &diff)
	if diff.Against != "current" || len(diff.Changes) == 0 {
		t.Fatalf("diff = %+v", diff)
	}
	call(viewer, "POST", "/"+group.ID.String()+"/versions/1/restore", nil, http.StatusForbidden)
	call(foreign, "GET", "/"+group.ID.String()+"/versions", nil, http.StatusNotFound)
	call(foreign, "POST", "/"+group.ID.String()+"/versions/1/restore", nil, http.StatusNotFound)

	// Restore v1 with member-b deleted since: dropped and reported.
	if _, err := db.ExecContext(ctx, `UPDATE monitors SET deleted_at = NOW() WHERE id = $1`, memberB); err != nil {
		t.Fatal(err)
	}
	var restored models.RestoreMonitorVersionResponse
	_ = json.Unmarshal(call(editor, "POST", "/"+group.ID.String()+"/versions/1/restore", nil, http.StatusOK), &restored)
	if restored.RestoredFrom != 1 || strings.Join(restored.DroppedReferences, ",") != "member:"+memberB.String() ||
		restored.Version == nil || *restored.Version != 4 || restored.Monitor == nil {
		t.Fatalf("restore response = %+v", restored)
	}
	if got := members(group.ID); got != memberA.String() {
		t.Fatalf("restored members = %s, want %s", got, memberA)
	}
	list = versions(group.ID)
	if len(list.Items) != 4 || list.Items[0].Source != monitorservice.VersionSourceRestore || list.Items[0].RestoredFrom == nil || *list.Items[0].RestoredFrom != 1 {
		t.Fatalf("restore version: %+v", list.Items[0])
	}
	// Restoring what is already live writes nothing.
	restored = models.RestoreMonitorVersionResponse{}
	_ = json.Unmarshal(call(editor, "POST", "/"+group.ID.String()+"/versions/4/restore", nil, http.StatusOK), &restored)
	if restored.Version != nil || len(versions(group.ID).Items) != 4 {
		t.Fatalf("no-op restore recorded a version: %+v", restored)
	}

	// A monitor created with dependencies: one version holding them; edge
	// routes and a dependency-only PATCH each record one.
	var dependent models.Monitor
	_ = json.Unmarshal(call(editor, "POST", "/", map[string]any{
		"name": "dependent", "type": "http", "interval_seconds": 60, "timeout_seconds": 10,
		"config":         map[string]any{"url": "https://d.example", "method": "GET", "expected_status": 200},
		"depends_on_ids": []string{memberA.String()},
	}, http.StatusCreated), &dependent)
	base := "/" + dependent.ID.String()
	if list = versions(dependent.ID); len(list.Items) != 1 {
		t.Fatalf("create with dependencies recorded %d versions", len(list.Items))
	}
	call(editor, "PATCH", base, map[string]any{"depends_on_ids": []string{memberC.String()}}, http.StatusOK)
	call(editor, "POST", base+"/dependencies", map[string]any{"depends_on_id": memberA.String()}, http.StatusNoContent)
	call(editor, "DELETE", base+"/dependencies/"+memberA.String(), nil, http.StatusNoContent)
	list = versions(dependent.ID)
	if len(list.Items) != 4 || strings.Join(list.Items[2].ChangedFields, ",") != "depends_on_ids" {
		t.Fatalf("dependency edits: %+v", list.Items)
	}
	call(editor, "POST", base+"/versions/1/restore", nil, http.StatusOK)
	var deps string
	if err := db.QueryRowContext(ctx, `SELECT string_agg(depends_on_id::text, ',') FROM monitor_dependencies WHERE monitor_id = $1`, dependent.ID).Scan(&deps); err != nil {
		t.Fatal(err)
	}
	if deps != memberA.String() {
		t.Fatalf("restored dependencies = %s", deps)
	}

	// A version re-applied goes through the save path: a credential header in
	// its plain headers is moved into the secret map like on any save, and a
	// secret the version had that is gone is reported, not invented.
	var api models.Monitor
	_ = json.Unmarshal(call(editor, "POST", "/", map[string]any{
		"name": "api", "type": "http", "interval_seconds": 60, "timeout_seconds": 10,
		"config": map[string]any{"url": "https://a.example", "method": "GET", "expected_status": 200,
			"basic_auth_username": "ops", "basic_auth_password": "pw-1"},
	}, http.StatusCreated), &api)
	apiBase := "/" + api.ID.String()
	call(editor, "PATCH", apiBase, map[string]any{
		"config": map[string]any{"url": "https://a2.example", "method": "GET", "expected_status": 200,
			"basic_auth_username": "", "basic_auth_password": ""},
	}, http.StatusOK)
	if _, err := db.ExecContext(ctx, `
		UPDATE monitor_config_versions SET snapshot = jsonb_set(snapshot, '{config,headers}', '{"X-Api-Key":"plain-key"}')
		WHERE monitor_id = $1 AND version = 1`, api.ID); err != nil {
		t.Fatal(err)
	}
	restored = models.RestoreMonitorVersionResponse{}
	_ = json.Unmarshal(call(editor, "POST", apiBase+"/versions/1/restore", nil, http.StatusOK), &restored)
	if strings.Join(restored.SecretsToReenter, ",") != "basic_auth_password" {
		t.Fatalf("secrets to re-enter = %v", restored.SecretsToReenter)
	}
	var storedConfig map[string]any
	var raw []byte
	if err := db.QueryRowContext(ctx, `SELECT config FROM monitors WHERE id = $1`, api.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(raw, &storedConfig)
	secretHeaders, _ := storedConfig["secret_headers"].(map[string]any)
	if storedConfig["url"] != "https://a.example" || storedConfig["basic_auth_username"] != "ops" ||
		secretHeaders["X-Api-Key"] != "plain-key" || storedConfig["headers"] != nil || storedConfig["basic_auth_password"] != nil {
		t.Fatalf("restored config = %s", raw)
	}

	call(editor, "GET", base+"/versions/99", nil, http.StatusNotFound)
	call(editor, "GET", base+"/versions/0", nil, http.StatusBadRequest)
	call(editor, "GET", base+"/versions/1/diff?against=nope", nil, http.StatusBadRequest)
	call(editor, "GET", "/"+uuid.NewString()+"/versions", nil, http.StatusNotFound)

	// Restores are audited, denied ones included.
	recorder.Stop()
	var succeeded, denied int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FILTER (WHERE outcome = 'success'), count(*) FILTER (WHERE outcome = 'denied')
		FROM audit_log WHERE action = 'monitor.restore' AND resource_type = 'monitor' AND resource_id = $1
	`, group.ID.String()).Scan(&succeeded, &denied); err != nil {
		t.Fatal(err)
	}
	if succeeded != 2 || denied != 1 {
		t.Fatalf("audited restores: %d succeeded, %d denied", succeeded, denied)
	}
}
