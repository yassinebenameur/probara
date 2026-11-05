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

	"github.com/yassinebenameur/probara/api/internal/models"
	depservice "github.com/yassinebenameur/probara/api/internal/services/dependencies"
	groupservice "github.com/yassinebenameur/probara/api/internal/services/groups"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// TestMonitorVersionHistoryHTTP_Integration drives the history routes the
// way the UI does: a create that also syncs members or dependencies records
// one version holding them, edits record more, and a restore puts members
// and dependencies back (dropping references that no longer exist).
func TestMonitorVersionHistoryHTTP_Integration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, db, "history-http")
	memberA := testutil.InsertHTTPMonitor(ctx, t, db, tenant, "member-a")
	memberB := testutil.InsertHTTPMonitor(ctx, t, db, tenant, "member-b")
	memberC := testutil.InsertHTTPMonitor(ctx, t, db, tenant, "member-c")

	svc := monitorservice.NewService(monitorservice.NewPostgresRepository(db))
	h := NewHandlers(svc, groupservice.NewService(db), nil, logger.New("test", "error"), t.TempDir())
	h.ConfigureDependencies(depservice.NewService(db))
	h.ConfigureVersions(svc.Versions())
	r := chi.NewRouter()
	r.Post("/monitors", h.CreateMonitor)
	r.Patch("/monitors/{id}", h.UpdateMonitor)
	r.Get("/monitors/{id}/versions", h.ListMonitorVersions)
	r.Get("/monitors/{id}/versions/{version}", h.GetMonitorVersion)
	r.Get("/monitors/{id}/versions/{version}/diff", h.DiffMonitorVersion)
	r.Post("/monitors/{id}/versions/{version}/restore", h.RestoreMonitorVersion)

	requestCtx := ctxpkg.WithRole(ctxpkg.WithTenantID(ctx, tenant.String()), "admin")
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(method, path, &buf).WithContext(requestCtx))
		if w.Code != want {
			t.Fatalf("%s %s: status %d, want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w.Body.Bytes()
	}
	versions := func(id uuid.UUID) models.MonitorVersionListResponse {
		t.Helper()
		var out models.MonitorVersionListResponse
		if err := json.Unmarshal(call("GET", "/monitors/"+id.String()+"/versions", nil, http.StatusOK), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	members := func(groupID uuid.UUID) string {
		t.Helper()
		rows, err := db.QueryContext(ctx, `SELECT monitor_id::text FROM monitor_groups WHERE group_id = $1`, groupID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var ids []string
		for rows.Next() {
			var id string
			_ = rows.Scan(&id)
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return strings.Join(ids, ",")
	}
	sorted := func(ids ...uuid.UUID) string {
		out := make([]string, len(ids))
		for i, id := range ids {
			out[i] = id.String()
		}
		sort.Strings(out)
		return strings.Join(out, ",")
	}

	// Group create: one version, with its members.
	var group models.Monitor
	_ = json.Unmarshal(call("POST", "/monitors", map[string]any{
		"name": "edge", "type": "group", "interval_seconds": 60, "timeout_seconds": 10,
		"config": map[string]any{"monitor_ids": []string{memberA.String(), memberB.String()}},
	}, http.StatusCreated), &group)
	list := versions(group.ID)
	if len(list.Items) != 1 {
		t.Fatalf("group create recorded %d versions", len(list.Items))
	}
	var v1 models.MonitorVersion
	_ = json.Unmarshal(call("GET", "/monitors/"+group.ID.String()+"/versions/1", nil, http.StatusOK), &v1)
	if strings.Join(v1.Snapshot.MemberIDs, ",") != sorted(memberA, memberB) {
		t.Fatalf("v1 members = %v", v1.Snapshot.MemberIDs)
	}

	// Membership edit: v2.
	call("PATCH", "/monitors/"+group.ID.String(), map[string]any{
		"config": map[string]any{"monitor_ids": []string{memberC.String()}},
	}, http.StatusOK)
	list = versions(group.ID)
	if len(list.Items) != 2 || strings.Join(list.Items[0].ChangedFields, ",") != "config.monitor_ids,member_ids" {
		t.Fatalf("after edit: %+v", list.Items)
	}

	// Diff v1 against current, then restore v1 with member-b gone.
	var diff models.MonitorVersionDiffResponse
	_ = json.Unmarshal(call("GET", "/monitors/"+group.ID.String()+"/versions/1/diff?against=current", nil, http.StatusOK), &diff)
	if diff.Against != "current" || len(diff.Changes) == 0 {
		t.Fatalf("diff = %+v", diff)
	}
	if _, err := db.ExecContext(ctx, `UPDATE monitors SET deleted_at = NOW() WHERE id = $1`, memberB); err != nil {
		t.Fatal(err)
	}
	var restored models.RestoreMonitorVersionResponse
	_ = json.Unmarshal(call("POST", "/monitors/"+group.ID.String()+"/versions/1/restore", nil, http.StatusOK), &restored)
	if restored.RestoredFrom != 1 || strings.Join(restored.DroppedReferences, ",") != "member:"+memberB.String() {
		t.Fatalf("restore response = %+v", restored)
	}
	if got := members(group.ID); got != memberA.String() {
		t.Fatalf("restored members = %s, want %s", got, memberA)
	}
	list = versions(group.ID)
	if len(list.Items) != 3 || list.Items[0].Source != monitorservice.VersionSourceRestore || list.Items[0].RestoredFrom == nil {
		t.Fatalf("restore version: %+v", list.Items[0])
	}

	// A monitor created with dependencies: one version holding them; a
	// restore of an older version puts the old dependency set back.
	var dependent models.Monitor
	_ = json.Unmarshal(call("POST", "/monitors", map[string]any{
		"name": "dependent", "type": "http", "interval_seconds": 60, "timeout_seconds": 10,
		"config":         map[string]any{"url": "https://d.example", "method": "GET", "expected_status": 200},
		"depends_on_ids": []string{memberA.String()},
	}, http.StatusCreated), &dependent)
	list = versions(dependent.ID)
	if len(list.Items) != 1 {
		t.Fatalf("create with dependencies recorded %d versions", len(list.Items))
	}
	call("PATCH", "/monitors/"+dependent.ID.String(), map[string]any{"depends_on_ids": []string{memberC.String()}}, http.StatusOK)
	list = versions(dependent.ID)
	if len(list.Items) != 2 || strings.Join(list.Items[0].ChangedFields, ",") != "depends_on_ids" {
		t.Fatalf("dependency-only edit: %+v", list.Items)
	}
	call("POST", "/monitors/"+dependent.ID.String()+"/versions/1/restore", nil, http.StatusOK)
	var dep string
	if err := db.QueryRowContext(ctx, `SELECT string_agg(depends_on_id::text, ',') FROM monitor_dependencies WHERE monitor_id = $1`, dependent.ID).Scan(&dep); err != nil {
		t.Fatal(err)
	}
	if dep != memberA.String() {
		t.Fatalf("restored dependencies = %s", dep)
	}

	call("GET", "/monitors/"+dependent.ID.String()+"/versions/99", nil, http.StatusNotFound)
	call("GET", "/monitors/"+dependent.ID.String()+"/versions/0", nil, http.StatusBadRequest)
	call("GET", "/monitors/"+uuid.NewString()+"/versions", nil, http.StatusNotFound)
}

// TestRestoreMonitorVersionPolicyAndSecretsHTTP_Integration: a restore over
// HTTP re-applies a version's escalation policy, keeps the secret map keys
// added since the version, and answers 400 (not 500) for a version the
// escalation rules refuse.
func TestRestoreMonitorVersionPolicyAndSecretsHTTP_Integration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, db, "history-policy-http")
	var policyID string
	if err := db.QueryRowContext(ctx, `INSERT INTO escalation_policies (tenant_id, name) VALUES ($1, 'oncall') RETURNING id::text`, tenant).Scan(&policyID); err != nil {
		t.Fatal(err)
	}

	svc := monitorservice.NewService(monitorservice.NewPostgresRepository(db))
	h := NewHandlers(svc, groupservice.NewService(db), nil, logger.New("test", "error"), t.TempDir())
	h.ConfigureDependencies(depservice.NewService(db))
	h.ConfigureVersions(svc.Versions())
	r := chi.NewRouter()
	r.Post("/monitors", h.CreateMonitor)
	r.Patch("/monitors/{id}", h.UpdateMonitor)
	r.Post("/monitors/{id}/versions/{version}/restore", h.RestoreMonitorVersion)
	requestCtx := ctxpkg.WithRole(ctxpkg.WithTenantID(ctx, tenant.String()), "admin")
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(method, path, &buf).WithContext(requestCtx))
		if w.Code != want {
			t.Fatalf("%s %s: status %d, want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w.Body.Bytes()
	}
	state := func(id uuid.UUID) (string, string, map[string]any) {
		t.Helper()
		var mode, policy string
		var raw json.RawMessage
		if err := db.QueryRowContext(ctx, `SELECT notification_mode, COALESCE(escalation_policy_id::text, ''), config FROM monitors WHERE id = $1`, id).Scan(&mode, &policy, &raw); err != nil {
			t.Fatal(err)
		}
		var config map[string]any
		_ = json.Unmarshal(raw, &config)
		headers, _ := config["secret_headers"].(map[string]any)
		return mode, policy, headers
	}

	// v1: policy mode, one secret header.
	var monitor models.Monitor
	_ = json.Unmarshal(call("POST", "/monitors", map[string]any{
		"name": "paged", "type": "http", "interval_seconds": 60, "timeout_seconds": 10,
		"notification_mode": "policy", "escalation_policy_id": policyID,
		"config": map[string]any{"url": "https://p.example", "method": "GET", "expected_status": 200,
			"secret_headers": map[string]any{"Authorization": "Bearer one"}},
	}, http.StatusCreated), &monitor)
	base := "/monitors/" + monitor.ID.String()

	// v2: default mode, a second secret header added.
	call("PATCH", base, map[string]any{
		"notification_mode": "default",
		"config": map[string]any{"url": "https://p2.example", "method": "GET", "expected_status": 200,
			"secret_headers": map[string]any{"Authorization": "***", "X-Api-Key": "key-two"}},
	}, http.StatusOK)

	var restored models.RestoreMonitorVersionResponse
	_ = json.Unmarshal(call("POST", base+"/versions/1/restore", nil, http.StatusOK), &restored)
	if len(restored.DroppedReferences) != 0 {
		t.Fatalf("dropped = %v", restored.DroppedReferences)
	}
	mode, policy, headers := state(monitor.ID)
	if mode != "policy" || policy != policyID {
		t.Fatalf("after restore: %s/%s, want policy/%s", mode, policy, policyID)
	}
	if headers["Authorization"] == nil || headers["X-Api-Key"] == nil {
		t.Fatalf("restore removed a current secret header: %v", headers)
	}

	// A policy-mode version without its policy id (recorded before
	// snapshots carried it) cannot be re-applied to a default-mode monitor:
	// 400, not 500.
	call("PATCH", base, map[string]any{"notification_mode": "default"}, http.StatusOK)
	if _, err := db.ExecContext(ctx, `UPDATE monitor_config_versions SET snapshot = snapshot - 'escalation_policy_id' WHERE monitor_id = $1 AND version = 1`, monitor.ID); err != nil {
		t.Fatal(err)
	}
	call("POST", base+"/versions/1/restore", nil, http.StatusBadRequest)
}
