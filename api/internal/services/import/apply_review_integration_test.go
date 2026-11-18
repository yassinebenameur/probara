//go:build !unit

package importservice

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/yassinebenameur/probara/api/internal/models"
	groupservice "github.com/yassinebenameur/probara/api/internal/services/groups"
	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func (h *applyHarness) storedConfig(key string) map[string]any {
	h.t.Helper()
	var raw []byte
	if err := h.db.QueryRowContext(context.Background(), `SELECT config FROM monitors WHERE tenant_id = $1 AND key = $2 AND deleted_at IS NULL`, h.tenantID, key).Scan(&raw); err != nil {
		h.t.Fatal(err)
	}
	var cfg map[string]any
	_ = json.Unmarshal(raw, &cfg)
	return cfg
}

func (h *applyHarness) decryptedConfig(key, monitorType string) map[string]any {
	h.t.Helper()
	raw, _ := json.Marshal(h.storedConfig(key))
	plain, err := secrets.DecryptMonitorConfig(h.enc, monitorType, raw)
	if err != nil {
		h.t.Fatal(err)
	}
	var cfg map[string]any
	_ = json.Unmarshal(plain, &cfg)
	return cfg
}

// The same document converges whatever the write path moves: a literal
// credential in a synthetic API step header (moved into a generated secret
// variable), plain credential headers, gRPC metadata and synthetic browser
// variables (moved into their secret maps), and basic auth. Three applies:
// one create, then nothing — no update, no version, no new stored secret.
func TestApply_CredentialsConverge(t *testing.T) {
	h := newApplyHarness(t)
	doc := func(token string) string {
		return `
kind: monitor_export
version: 2
monitors:
  - key: journey
    name: Journey
    type: synthetic_api
    interval_seconds: 60
    timeout_seconds: 30
    config:
      steps:
        - id: login
          request: {method: GET, url: "https://example.com/me", headers: {Authorization: "Bearer ` + token + `"}}
  - key: web
    name: Web
    type: http
    interval_seconds: 60
    timeout_seconds: 10
    config: {url: "https://example.com", method: GET, headers: {X-Api-Token: "` + token + `", Accept: "text/html"}, basic_auth_username: ops, basic_auth_password: "pw-` + token + `"}
  - key: rpc
    name: RPC
    type: grpc
    interval_seconds: 60
    timeout_seconds: 10
    config: {host: grpc.example.com, metadata: {authorization: "` + token + `", x-tenant: t1}}
  - key: login
    name: Login
    type: synthetic_browser
    interval_seconds: 60
    timeout_seconds: 30
    config:
      start_url: "https://example.com/login"
      variables: {username: robot, password: "` + token + `"}
      steps: [{id: p, action: fill, selector: "#p", value: "{{password}}"}]
`
	}

	first := h.apply(applyRequest(doc("one")))
	if first.Summary.Create != 4 {
		t.Fatalf("first apply = %+v", first)
	}
	versions := h.versionCount()
	for run := 2; run <= 3; run++ {
		result := h.apply(applyRequest(doc("one")))
		if result.Applied || result.Summary.Unchanged != 4 {
			t.Fatalf("apply %d = %+v", run, result.Summary)
		}
	}
	if got := h.versionCount(); got != versions {
		t.Fatalf("re-applies recorded versions: %d -> %d", versions, got)
	}
	journey := h.decryptedConfig("journey", "synthetic_api")
	secretVars, _ := journey["secret_variables"].(map[string]any)
	if len(secretVars) != 1 || secretVars["login_authorization"] != "Bearer one" {
		t.Fatalf("journey secret_variables = %v", secretVars)
	}

	// A rotated value replaces the stored one in place.
	rotated := h.apply(applyRequest(doc("two")))
	if rotated.Summary.Update != 4 {
		t.Fatalf("rotation = %+v", rotated.Summary)
	}
	journey = h.decryptedConfig("journey", "synthetic_api")
	secretVars, _ = journey["secret_variables"].(map[string]any)
	if len(secretVars) != 1 || secretVars["login_authorization"] != "Bearer two" {
		t.Fatalf("rotated journey secret_variables = %v", secretVars)
	}
	versions = h.versionCount()
	if again := h.apply(applyRequest(doc("two"))); again.Applied || h.versionCount() != versions {
		t.Fatalf("rotated document did not converge: %+v", again.Summary)
	}
	if web := h.decryptedConfig("web", "http"); web["basic_auth_password"] != "pw-two" {
		t.Fatalf("web = %v", web)
	}
}

// Generated secret variables left behind by earlier saves are dropped once
// no step references them.
func TestApply_DropsOrphanedGeneratedVariables(t *testing.T) {
	h := newApplyHarness(t)
	key := "journey"
	config := `{"secret_variables":{"login_authorization":"old","login_authorization_2":"older","api_token":"keep"},` +
		`"steps":[{"id":"login","request":{"method":"GET","url":"https://example.com/me?t={{api_token}}","headers":{"Authorization":"{{login_authorization}}"}}}]}`
	if _, err := h.monitors.CreateMonitor(h.admin, h.tenantID, &models.CreateMonitorRequest{
		Name: "Journey", Key: &key, Type: models.MonitorTypeSyntheticAPI, IntervalSeconds: 60, TimeoutSeconds: 30,
		Config: json.RawMessage(config),
	}); err != nil {
		t.Fatal(err)
	}
	doc := `
kind: monitor_export
version: 2
monitors:
  - key: journey
    name: Journey
    type: synthetic_api
    interval_seconds: 60
    timeout_seconds: 30
    config:
      steps:
        - id: login
          request: {method: GET, url: "https://example.com/me?t={{api_token}}", headers: {Authorization: "Bearer new"}}
`
	h.apply(applyRequest(doc))
	vars, _ := h.decryptedConfig("journey", "synthetic_api")["secret_variables"].(map[string]any)
	if len(vars) != 2 || vars["login_authorization"] != "Bearer new" || vars["api_token"] != "keep" {
		t.Fatalf("secret_variables = %v", vars)
	}
}

// A changed key with the same name and type is flagged as a probable
// rename on both items, and apply needs force for it.
func TestApply_ProbableRenameNeedsForce(t *testing.T) {
	h := newApplyHarness(t)
	h.apply(applyRequest(baseDoc))
	renamed := strings.Replace(baseDoc, "key: db\n", "key: database\n", 1)
	renamed = strings.ReplaceAll(renamed, "[db]", "[database]")
	renamed = strings.Replace(renamed, "[web, db]", "[web, database]", 1)

	plan := h.plan(h.admin, renamed)
	if len(plan.ProbableRenames) != 1 || plan.ProbableRenames[0].FromKey != "db" || plan.ProbableRenames[0].ToKey != "database" {
		t.Fatalf("renames = %+v", plan.ProbableRenames)
	}
	for _, item := range plan.Monitors {
		if (item.Key == "db" || item.Key == "database") && (item.ProbableRename == "" || len(item.Notes) == 0) {
			t.Fatalf("rename not marked on %+v", item)
		}
	}
	for _, prune := range []bool{false, true} {
		_, err := h.svc.Apply(h.admin, h.tenantID, &models.MonitorApplyRequest{Document: applyRequest(renamed).Document, Prune: prune})
		if !errors.Is(err, ErrApplyRenameRefused) {
			t.Fatalf("rename without force (prune %v) = %v", prune, err)
		}
	}
	result := h.apply(&models.MonitorApplyRequest{Document: applyRequest(renamed).Document, Prune: true, Force: true})
	if !result.Pruned || result.Summary.Create != 1 {
		t.Fatalf("forced rename = %+v", result.Summary)
	}
}

// Exports mask URL credentials; apply keeps the stored URL when the masked
// forms match and otherwise strips them and creates the monitor paused;
// import strips them and warns.
func TestApply_URLCredentials(t *testing.T) {
	h := newApplyHarness(t)
	key := "api"
	if _, err := h.monitors.CreateMonitor(h.admin, h.tenantID, &models.CreateMonitorRequest{
		Name: "API", Key: &key, Type: models.MonitorTypeHTTP, IntervalSeconds: 60, TimeoutSeconds: 10,
		Config: json.RawMessage(`{"url":"https://u:pw-secret@api.example.com/x?api_key=k-secret&page=1","method":"GET"}`),
	}); err != nil {
		t.Fatal(err)
	}
	data, err := h.svc.ExportMonitors(h.admin, h.tenantID, ExportOptions{KeyedOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "pw-secret") || strings.Contains(string(data), "k-secret") {
		t.Fatalf("export leaked URL credentials:\n%s", data)
	}
	if plan := h.plan(h.admin, string(data)); plan.Summary.Unchanged != 1 {
		t.Fatalf("masked URL round trip = %+v", plan)
	}
	h.apply(applyRequest(string(data)))
	if url := h.storedConfig("api")["url"]; url != "https://u:pw-secret@api.example.com/x?api_key=k-secret&page=1" {
		t.Fatalf("stored URL after round trip = %v", url)
	}

	copied := strings.Replace(string(data), "key: api", "key: api-copy", 1)
	copied = strings.Replace(copied, "name: API", "name: API copy", 1)
	result := h.apply(applyRequest(copied))
	var created models.MonitorApplyItem
	for _, item := range result.Monitors {
		if item.Key == "api-copy" {
			created = item
		}
	}
	if created.Action != "create" || len(created.Notes) == 0 || h.monitorByKey("api-copy").Enabled {
		t.Fatalf("masked URL create = %+v", created)
	}
	if url, _ := h.storedConfig("api-copy")["url"].(string); strings.Contains(url, "***") {
		t.Fatalf("a masked credential was stored: %s", url)
	}

	// Import of the full export: stripped, with a preview warning.
	other := testutil.InsertTenant(context.Background(), t, h.db, "url-import")
	all, err := h.svc.ExportMonitors(h.admin, h.tenantID, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := h.svc.ParseFile(all, "monitors.yaml")
	if err != nil || len(preview.Rows) == 0 || len(preview.Rows[0].Warnings) == 0 {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	if _, err := h.svc.ExecuteImport(h.admin, other, &models.ImportExecuteRequest{Rows: preview.Rows, Mapping: preview.SuggestedMapping}); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := h.db.QueryRowContext(context.Background(), `SELECT config->>'url' FROM monitors WHERE tenant_id = $1 AND name = 'API'`, other).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "***") {
		t.Fatalf("import stored a masked credential: %s", stored)
	}
}

// A save committing between the plan and the row locks is detected after
// the locks: the apply refuses, writes nothing, and the save is kept.
func TestApply_SaveBetweenPlanAndLocks(t *testing.T) {
	h := newApplyHarness(t)
	h.apply(applyRequest(baseDoc))
	edge := h.monitorByKey("edge").ID
	unkeyed := testutil.InsertHTTPMonitor(context.Background(), t, h.db, h.tenantID, "Hand-made")
	edited := strings.Replace(baseDoc, "member_keys: [web, db]", "member_keys: [web]", 1)

	applyTestHookAfterPlan = func() {
		// A UI save on its own connection, committed at once.
		if err := groupservice.NewService(h.db).AddMonitorsToGroup(context.Background(), h.tenantID, edge, []uuid.UUID{unkeyed}); err != nil {
			t.Errorf("concurrent save: %v", err)
		}
		h.monitors.RecordVersion(context.Background(), h.tenantID, edge)
	}
	defer func() { applyTestHookAfterPlan = nil }()
	_, err := h.svc.Apply(h.admin, h.tenantID, applyRequest(edited))
	if !errors.Is(err, ErrApplyStateChanged) {
		t.Fatalf("apply over a concurrent save = %v", err)
	}
	applyTestHookAfterPlan = nil
	if members := h.monitorByKey("edge").MemberIDs; len(members) != 3 {
		t.Fatalf("edge members after refused apply = %v", members)
	}
	// A retry plans against the save and keeps the unmanaged member.
	h.apply(applyRequest(edited))
	members := h.monitorByKey("edge").MemberIDs
	if len(members) != 2 {
		t.Fatalf("edge members after retry = %v", members)
	}
}

// group_members must agree with member_keys; a keyed export carries
// member_keys only, and the importer reads members from them.
func TestApply_GroupMembersByName(t *testing.T) {
	h := newApplyHarness(t)
	h.apply(applyRequest(baseDoc))
	disagree := strings.Replace(baseDoc, "    member_keys: [web, db]\n", "    member_keys: [web, db]\n    group_members: [Web]\n", 1)
	if plan := h.plan(h.admin, disagree); plan.Valid {
		t.Fatalf("disagreeing group_members planned as valid: %+v", plan)
	}
	agree := strings.Replace(baseDoc, "    member_keys: [web, db]\n", "    member_keys: [web, db]\n    group_members: [Database, Web]\n", 1)
	if plan := h.plan(h.admin, agree); !plan.Valid || plan.Summary.Unchanged != 3 {
		t.Fatalf("agreeing group_members = %+v", plan)
	}
	onlyNames := strings.Replace(baseDoc, "    member_keys: [web, db]\n", "    group_members: [Database, Web]\n", 1)
	if plan := h.plan(h.admin, onlyNames); plan.Valid {
		t.Fatal("a group with names only planned as valid")
	}

	data, err := h.svc.ExportMonitors(h.admin, h.tenantID, ExportOptions{KeyedOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "group_members") {
		t.Fatalf("keyed export writes group_members:\n%s", data)
	}
	preview, err := h.svc.ParseFile(data, "monitors.yaml")
	if err != nil {
		t.Fatal(err)
	}
	other := testutil.InsertTenant(context.Background(), t, h.db, "keyed-import")
	result, err := h.svc.ExecuteImport(h.admin, other, &models.ImportExecuteRequest{Rows: preview.Rows, Mapping: preview.SuggestedMapping})
	if err != nil || result.SuccessCount != 3 {
		t.Fatalf("import of a keyed export = %+v, %v", result, err)
	}
}

// A prune scope limits deletes and the guard to its keys, and refuses a
// document with keys outside it; unmanaged monitors a prune changes are
// listed.
func TestApply_PruneScopeAndAffected(t *testing.T) {
	h := newApplyHarness(t)
	doc := func(keys ...string) string {
		var b strings.Builder
		b.WriteString("kind: monitor_export\nversion: 2\nmonitors:\n")
		for _, key := range keys {
			b.WriteString("  - key: " + key + "\n    name: " + key + "\n    type: http\n    interval_seconds: 60\n    timeout_seconds: 10\n" +
				"    config: {url: \"https://" + key + ".example.com\", method: GET, expected_status: 200}\n")
		}
		return b.String()
	}
	h.apply(applyRequest(doc("pay.a", "pay.b", "ops.c")))
	hand := testutil.InsertGroupMonitor(context.Background(), t, h.db, h.tenantID, "Hand group")
	testutil.AddMonitorToGroup(context.Background(), t, h.db, h.monitorByKey("pay.b").ID, hand)

	req := &models.MonitorApplyRequest{Document: applyRequest(doc("pay.a")).Document, PruneScope: "pay.", Prune: true}
	plan, err := h.svc.PlanApply(h.admin, h.tenantID, req)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Summary.Delete != 1 || plan.Prune.KeyedMonitors != 2 || plan.Prune.NeedsForce {
		t.Fatalf("scoped plan = %+v", plan.Prune)
	}
	if len(plan.Prune.Affected) != 1 || plan.Prune.Affected[0].MonitorID != hand || plan.Prune.Affected[0].Loses[0] != "member:pay.b" {
		t.Fatalf("affected = %+v", plan.Prune.Affected)
	}
	result, err := h.svc.Apply(h.admin, h.tenantID, req)
	if err != nil || !result.Pruned || len(result.Prune.Affected) != 1 {
		t.Fatalf("scoped apply = %+v, %v", result, err)
	}
	h.monitorByKey("ops.c")

	outside, err := h.svc.PlanApply(h.admin, h.tenantID, &models.MonitorApplyRequest{Document: applyRequest(doc("pay.a", "ops.c")).Document, PruneScope: "pay."})
	if err != nil || outside.Valid {
		t.Fatalf("key outside the scope = %+v, %v", outside, err)
	}
	if _, err := h.svc.PlanApply(h.admin, h.tenantID, &models.MonitorApplyRequest{Document: applyRequest(doc("pay.a")).Document, PruneScope: "Pay "}); !errors.Is(err, ErrApplyDocument) {
		t.Fatalf("malformed scope = %v", err)
	}
}

// The fingerprint is an HMAC over the document with secrets masked: the
// same with another secret value, different with any other change.
func TestApply_FingerprintIsKeyedAndMasked(t *testing.T) {
	h := newApplyHarness(t)
	doc := func(password, url string) string {
		return "kind: monitor_export\nversion: 2\nmonitors:\n  - key: api\n    name: API\n    type: http\n    interval_seconds: 60\n    timeout_seconds: 10\n" +
			"    config: {url: \"" + url + "\", method: GET, basic_auth_username: ops, basic_auth_password: \"" + password + "\"}\n"
	}
	a := h.plan(h.admin, doc("one", "https://a.example.com")).Fingerprint
	b := h.plan(h.admin, doc("two", "https://a.example.com")).Fingerprint
	c := h.plan(h.admin, doc("one", "https://b.example.com")).Fingerprint
	if !strings.HasPrefix(a, "hmac-sha256:") || a != b || a == c {
		t.Fatalf("fingerprints = %s %s %s", a, b, c)
	}
}

// An export of monitors none of which has a key stays version 1.
func TestExport_VersionOneWithoutKeys(t *testing.T) {
	h := newApplyHarness(t)
	testutil.InsertHTTPMonitor(context.Background(), t, h.db, h.tenantID, "Plain")
	data, err := h.svc.ExportMonitors(h.admin, h.tenantID, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var bundle models.PortableMonitorExport
	if err := yaml.Unmarshal(data, &bundle); err != nil || bundle.Version != 1 {
		t.Fatalf("export without keys = version %d, %v", bundle.Version, err)
	}
}
