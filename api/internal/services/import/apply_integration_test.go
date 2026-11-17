//go:build !unit

package importservice

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	groupservice "github.com/yassinebenameur/probara/api/internal/services/groups"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/shared/testutil"
)

type applyHarness struct {
	t        *testing.T
	db       *shareddb.Client
	tenantID uuid.UUID
	admin    context.Context // tenant admin session (write access)
	monitors *monitorservice.Service
	svc      *Service
	enc      secrets.Encryptor
}

func newApplyHarness(t *testing.T) *applyHarness {
	t.Helper()
	ctx := context.Background()
	dbClient, teardown := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(teardown)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "apply-"+uuid.NewString()[:8])
	var adminID uuid.UUID
	if err := dbClient.QueryRowContext(ctx, `INSERT INTO admin_users (username, password_hash) VALUES ('applier', 'x') RETURNING id`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PROBARA_SECRETS_KEY", base64.StdEncoding.EncodeToString(key))
	kp, err := secrets.NewEnvKeyProvider()
	if err != nil {
		t.Fatal(err)
	}
	enc := secrets.NewAESGCMEncryptor(kp)

	monitorSvc := monitorservice.NewService(monitorservice.NewPostgresRepository(dbClient))
	monitorSvc.ConfigureEncryption(enc)
	admin := ctxpkg.WithRole(ctxpkg.WithAdminID(ctxpkg.WithTenantID(ctx, tenantID.String()), adminID.String()), "admin")
	return &applyHarness{
		t: t, db: dbClient, tenantID: tenantID, admin: admin, monitors: monitorSvc, enc: enc,
		svc: NewService(dbClient, monitorSvc, groupservice.NewService(dbClient)),
	}
}

func applyRequest(doc string) *models.MonitorApplyRequest {
	raw, _ := json.Marshal(doc)
	return &models.MonitorApplyRequest{Document: raw}
}

func (h *applyHarness) plan(ctx context.Context, doc string) *models.MonitorApplyPlan {
	h.t.Helper()
	plan, err := h.svc.PlanApply(ctx, h.tenantID, applyRequest(doc))
	if err != nil {
		h.t.Fatalf("PlanApply() = %v", err)
	}
	return plan
}

func (h *applyHarness) apply(req *models.MonitorApplyRequest) *models.MonitorApplyResult {
	h.t.Helper()
	result, err := h.svc.Apply(h.admin, h.tenantID, req)
	if err != nil {
		h.t.Fatalf("Apply() = %v (%+v)", err, result)
	}
	return result
}

func (h *applyHarness) monitorByKey(key string) *models.Monitor {
	h.t.Helper()
	var id uuid.UUID
	if err := h.db.QueryRowContext(context.Background(), `SELECT id FROM monitors WHERE tenant_id = $1 AND key = $2 AND deleted_at IS NULL`, h.tenantID, key).Scan(&id); err != nil {
		h.t.Fatalf("monitor %q: %v", key, err)
	}
	monitor, err := h.monitors.GetMonitor(context.Background(), h.tenantID, id)
	if err != nil {
		h.t.Fatal(err)
	}
	return monitor
}

func (h *applyHarness) count(query string, args ...any) int {
	h.t.Helper()
	var n int
	if err := h.db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

func (h *applyHarness) versionCount() int {
	return h.count(`SELECT count(*) FROM monitor_config_versions WHERE tenant_id = $1`, h.tenantID)
}

func (h *applyHarness) liveMonitors() int {
	return h.count(`SELECT count(*) FROM monitors WHERE tenant_id = $1 AND deleted_at IS NULL`, h.tenantID)
}

func actions(plan *models.MonitorApplyPlan) map[string]string {
	out := map[string]string{}
	for _, item := range plan.Monitors {
		out[item.Key] = item.Action
	}
	return out
}

const baseDoc = `
kind: monitor_export
version: 2
monitors:
  - key: db
    name: Database
    type: http
    interval_seconds: 60
    timeout_seconds: 10
    enabled: true
    config: {url: "https://db.example.com/health", method: GET, expected_status: 200}
  - key: web
    name: Web
    type: http
    interval_seconds: 60
    timeout_seconds: 10
    enabled: true
    tags: [prod]
    depends_on: [db]
    config: {url: "https://web.example.com", method: GET, expected_status: 200}
  - key: edge
    name: Edge
    type: group
    interval_seconds: 60
    timeout_seconds: 0
    enabled: true
    member_keys: [web, db]
    config: {}
`

// Create, then idempotency: the second apply of the same document changes
// nothing and records no version; versions carry source apply and the actor.
func TestApply_CreateThenIdempotent(t *testing.T) {
	h := newApplyHarness(t)

	plan := h.plan(h.admin, baseDoc)
	if !plan.Valid || plan.Summary.Create != 3 || plan.Summary.Delete != 0 {
		t.Fatalf("plan = %+v", plan)
	}
	result := h.apply(applyRequest(baseDoc))
	if !result.Applied || result.Summary.Create != 3 {
		t.Fatalf("apply = %+v", result)
	}
	for _, item := range result.Monitors {
		if item.MonitorID == nil {
			t.Fatalf("created %q without id", item.Key)
		}
	}

	web, db, edge := h.monitorByKey("web"), h.monitorByKey("db"), h.monitorByKey("edge")
	if len(web.DependsOnIDs) != 1 || web.DependsOnIDs[0] != db.ID {
		t.Fatalf("web depends on %v, want db", web.DependsOnIDs)
	}
	if len(edge.MemberIDs) != 2 {
		t.Fatalf("edge members = %v", edge.MemberIDs)
	}
	for _, monitor := range []*models.Monitor{web, db, edge} {
		list, err := h.monitors.Versions().List(context.Background(), h.tenantID, monitor.ID, 1, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(list.Items) != 1 || list.Items[0].Source != monitorservice.VersionSourceApply || list.Items[0].ActorLabel != "applier" {
			t.Fatalf("%s versions = %+v", monitor.Name, list.Items)
		}
	}

	versions := h.versionCount()
	again := h.plan(h.admin, baseDoc)
	if again.Summary.Unchanged != 3 || again.Summary.Create+again.Summary.Update != 0 {
		t.Fatalf("second plan = %+v", again)
	}
	second := h.apply(applyRequest(baseDoc))
	if second.Applied || second.Summary.Unchanged != 3 {
		t.Fatalf("second apply = %+v", second)
	}
	if got := h.versionCount(); got != versions {
		t.Fatalf("second apply recorded versions: %d -> %d", versions, got)
	}
}

// An export of the keyed monitors plans as unchanged, secrets included
// (they export as "***"), and a monitor without a key is not in it.
func TestApply_ExportRoundTripIsUnchanged(t *testing.T) {
	h := newApplyHarness(t)
	h.apply(applyRequest(baseDoc))
	webKey := "api"
	if _, err := h.monitors.CreateMonitor(h.admin, h.tenantID, &models.CreateMonitorRequest{
		Name: "API", Key: &webKey, Type: models.MonitorTypeHTTP, IntervalSeconds: 60, TimeoutSeconds: 10,
		Config: json.RawMessage(`{"url":"https://api.example.com","method":"GET","basic_auth_username":"ops","basic_auth_password":"hunter2","secret_headers":{"X-Api-Key":"k-123"}}`),
	}); err != nil {
		t.Fatal(err)
	}
	testutil.InsertHTTPMonitor(context.Background(), t, h.db, h.tenantID, "Unmanaged")

	data, err := h.svc.ExportMonitors(h.admin, h.tenantID, ExportOptions{KeyedOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Unmanaged") || strings.Contains(string(data), "hunter2") || strings.Contains(string(data), "k-123") {
		t.Fatalf("keyed export leaked an unkeyed monitor or a secret:\n%s", data)
	}
	plan := h.plan(h.admin, string(data))
	if !plan.Valid || plan.Summary.Unchanged != 4 || plan.Summary.Create+plan.Summary.Update+plan.Summary.Delete != 0 {
		t.Fatalf("round-trip plan = %+v", plan)
	}

	// The full export carries the unkeyed monitor, which apply refuses.
	all, err := h.svc.ExportMonitors(h.admin, h.tenantID, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if full := h.plan(h.admin, string(all)); full.Valid {
		t.Fatalf("a document with an unkeyed monitor planned as valid: %+v", full)
	}
	// The importer still reads a version 2 file.
	preview, err := h.svc.ParseFile(all, "monitors.yaml")
	if err != nil || preview.Schema != models.ImportSchemaPortableMonitorExport || preview.TotalRows != 5 {
		t.Fatalf("ParseFile(v2) = %+v, %v", preview, err)
	}
}

// Update with a field diff, rename through the key, delete only with prune.
func TestApply_UpdateRenameAndPrune(t *testing.T) {
	h := newApplyHarness(t)
	h.apply(applyRequest(baseDoc))
	webID := h.monitorByKey("web").ID

	edited := strings.NewReplacer(
		"name: Web\n", "name: Website\n",
		"    depends_on: [db]\n", "",
		"member_keys: [web, db]", "member_keys: [web]",
		"https://web.example.com", "https://www.example.com",
	).Replace(baseDoc)
	// db leaves the document.
	edited = strings.Replace(edited, `  - key: db
    name: Database
    type: http
    interval_seconds: 60
    timeout_seconds: 10
    enabled: true
    config: {url: "https://db.example.com/health", method: GET, expected_status: 200}
`, "", 1)

	plan := h.plan(h.admin, edited)
	got := actions(plan)
	if !plan.Valid || got["web"] != "update" || got["edge"] != "update" || got["db"] != "delete" {
		t.Fatalf("plan = %+v", plan)
	}
	paths := map[string]bool{}
	for _, item := range plan.Monitors {
		if item.Key == "web" {
			for _, change := range item.Changes {
				paths[change.Path] = true
			}
		}
	}
	for _, want := range []string{"name", "config.url", "depends_on"} {
		if !paths[want] {
			t.Fatalf("web changes lack %s: %+v", want, paths)
		}
	}

	// Without prune: updated, db kept. 1 of 3 keyed is not more than half.
	result := h.apply(&models.MonitorApplyRequest{Document: applyRequest(edited).Document, Fingerprint: plan.Fingerprint})
	if result.Pruned {
		t.Fatal("pruned without prune")
	}
	if web := h.monitorByKey("web"); web.ID != webID || web.Name != "Website" || len(web.DependsOnIDs) != 0 {
		t.Fatalf("web after apply = %+v", web)
	}
	h.monitorByKey("db")

	result = h.apply(&models.MonitorApplyRequest{Document: applyRequest(edited).Document, Prune: true})
	if !result.Pruned || h.count(`SELECT count(*) FROM monitors WHERE tenant_id = $1 AND key = 'db' AND deleted_at IS NULL`, h.tenantID) != 0 {
		t.Fatalf("prune = %+v", result)
	}
	if edge := h.monitorByKey("edge"); len(edge.MemberIDs) != 1 || edge.MemberIDs[0] != webID {
		t.Fatalf("edge members after prune = %v", edge.MemberIDs)
	}
}

// Prune deletes keyed monitors only, and its guard refuses an empty document
// or deleting more than half of the keyed monitors without force.
func TestApply_PruneGuardAndUnkeyed(t *testing.T) {
	h := newApplyHarness(t)
	h.apply(applyRequest(baseDoc))
	unkeyed := testutil.InsertHTTPMonitor(context.Background(), t, h.db, h.tenantID, "Hand-made")

	empty := "kind: monitor_export\nversion: 2\nmonitors: []\n"
	plan := h.plan(h.admin, empty)
	if plan.Summary.Delete != 3 || !plan.Prune.NeedsForce {
		t.Fatalf("empty plan = %+v", plan)
	}
	for _, item := range plan.Monitors {
		if item.MonitorID != nil && *item.MonitorID == unkeyed {
			t.Fatal("plan deletes an unkeyed monitor")
		}
	}
	before := h.liveMonitors()
	_, err := h.svc.Apply(h.admin, h.tenantID, &models.MonitorApplyRequest{Document: applyRequest(empty).Document, Prune: true})
	if !errors.Is(err, ErrApplyPruneRefused) || h.liveMonitors() != before {
		t.Fatalf("empty prune without force = %v", err)
	}

	// Two of three keyed monitors: more than half.
	onlyDB := strings.Split(baseDoc, "  - key: web")[0]
	_, err = h.svc.Apply(h.admin, h.tenantID, &models.MonitorApplyRequest{Document: applyRequest(onlyDB).Document, Prune: true})
	if !errors.Is(err, ErrApplyPruneRefused) {
		t.Fatalf("majority prune without force = %v", err)
	}

	result := h.apply(&models.MonitorApplyRequest{Document: applyRequest(empty).Document, Prune: true, Force: true})
	if !result.Pruned || h.liveMonitors() != 1 {
		t.Fatalf("forced prune = %+v, live %d", result, h.liveMonitors())
	}
	if _, err := h.monitors.GetMonitor(context.Background(), h.tenantID, unkeyed); err != nil {
		t.Fatalf("unkeyed monitor gone: %v", err)
	}
}

// Secrets: "***" and absent keep, a literal replaces, "" clears; a value
// equal to the stored one is no change; no output carries a value; a read-only
// plan does not compare; a monitor whose secret is masked but not held is
// created disabled and stays so.
func TestApply_Secrets(t *testing.T) {
	h := newApplyHarness(t)
	doc := func(password string) string {
		return `
kind: monitor_export
version: 2
monitors:
  - key: api
    name: API
    type: http
    interval_seconds: 60
    timeout_seconds: 10
    config: {url: "https://api.example.com", method: GET, basic_auth_username: ops` + password + `}
`
	}
	stored := func() string {
		var raw []byte
		if err := h.db.QueryRowContext(context.Background(), `SELECT config FROM monitors WHERE tenant_id = $1 AND key = 'api'`, h.tenantID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		plain, err := secrets.DecryptMonitorConfig(h.enc, "http", raw)
		if err != nil {
			t.Fatal(err)
		}
		var cfg map[string]any
		_ = json.Unmarshal(plain, &cfg)
		value, _ := cfg["basic_auth_password"].(string)
		return value
	}

	created := h.apply(applyRequest(doc(`, basic_auth_password: "s3cret-one"`)))
	if stored() != "s3cret-one" {
		t.Fatalf("stored password = %q", stored())
	}
	encoded, _ := json.Marshal(created)
	if strings.Contains(string(encoded), "s3cret") {
		t.Fatalf("apply output carries the secret: %s", encoded)
	}

	versions := h.versionCount()
	for name, spelling := range map[string]string{
		"same literal": `, basic_auth_password: "s3cret-one"`,
		"masked":       `, basic_auth_password: "***"`,
		"absent":       ``,
	} {
		plan := h.plan(h.admin, doc(spelling))
		if actions(plan)["api"] != "unchanged" {
			t.Fatalf("%s: plan = %+v", name, plan)
		}
		h.apply(applyRequest(doc(spelling)))
	}
	if h.versionCount() != versions || stored() != "s3cret-one" {
		t.Fatalf("kept secret wrote: versions %d -> %d, stored %q", versions, h.versionCount(), stored())
	}

	// A read-only caller cannot learn whether a literal matches.
	viewer := ctxpkg.WithRole(ctxpkg.WithAdminID(context.Background(), uuid.NewString()), "viewer")
	readOnly := h.plan(viewer, doc(`, basic_auth_password: "s3cret-one"`))
	if actions(readOnly)["api"] != "update" || len(readOnly.Monitors[0].Notes) == 0 {
		t.Fatalf("read-only plan compared a secret: %+v", readOnly)
	}

	replaced := h.plan(h.admin, doc(`, basic_auth_password: "s3cret-two"`))
	item := replaced.Monitors[0]
	if item.Action != "update" || len(item.Changes) != 1 || item.Changes[0].Path != "config.basic_auth_password" || !item.Changes[0].Secret {
		t.Fatalf("replace plan = %+v", item)
	}
	encoded, _ = json.Marshal(replaced)
	if strings.Contains(string(encoded), "s3cret") {
		t.Fatalf("plan output carries a secret: %s", encoded)
	}
	h.apply(applyRequest(doc(`, basic_auth_password: "s3cret-two"`)))
	if stored() != "s3cret-two" {
		t.Fatalf("replaced password = %q", stored())
	}
	h.apply(applyRequest(doc(`, basic_auth_password: ""`)))
	if stored() != "" {
		t.Fatalf("cleared password = %q", stored())
	}

	// Masked but not held: created disabled, never enabled by a re-apply.
	masked := strings.Replace(doc(`, basic_auth_password: "***"`), "key: api", "key: api-copy", 1)
	result := h.apply(applyRequest(masked))
	if len(result.Monitors) == 0 || result.Monitors[0].Action != "create" || h.monitorByKey("api-copy").Enabled {
		t.Fatalf("masked create = %+v", result)
	}
	h.apply(applyRequest(masked))
	if h.monitorByKey("api-copy").Enabled {
		t.Fatal("a re-apply enabled a monitor missing its secret")
	}
}

// A plan's fingerprint no longer matches once a keyed monitor changed; apply
// refuses it and writes nothing.
func TestApply_FingerprintMismatch(t *testing.T) {
	h := newApplyHarness(t)
	h.apply(applyRequest(baseDoc))
	edited := strings.Replace(baseDoc, "name: Web\n", "name: Web 2\n", 1)
	plan := h.plan(h.admin, edited)

	name := "Changed in the UI"
	if _, err := h.monitors.UpdateMonitor(h.admin, h.tenantID, h.monitorByKey("db").ID, &models.UpdateMonitorRequest{Name: &name}); err != nil {
		t.Fatal(err)
	}
	versions := h.versionCount()
	result, err := h.svc.Apply(h.admin, h.tenantID, &models.MonitorApplyRequest{Document: applyRequest(edited).Document, Fingerprint: plan.Fingerprint})
	if !errors.Is(err, ErrApplyPlanChanged) || result == nil || result.Applied {
		t.Fatalf("stale fingerprint = %+v, %v", result, err)
	}
	if h.monitorByKey("web").Name != "Web" || h.versionCount() != versions {
		t.Fatal("a refused apply wrote")
	}
	// A different document also no longer matches.
	if again := h.plan(h.admin, edited); again.Fingerprint == plan.Fingerprint {
		t.Fatal("fingerprint did not change with the workspace")
	}
	if other := h.plan(h.admin, baseDoc); other.Fingerprint == h.plan(h.admin, edited).Fingerprint {
		t.Fatal("fingerprint does not cover the document")
	}
}

// One invalid monitor fails the whole document: nothing is written.
func TestApply_ValidationFailureWritesNothing(t *testing.T) {
	h := newApplyHarness(t)
	bad := baseDoc + `
  - key: BAD KEY
    name: Bad
    type: http
    interval_seconds: 60
    timeout_seconds: 10
    config: {url: "https://bad.example.com"}
  - key: broken
    name: Broken
    type: http
    interval_seconds: 5
    timeout_seconds: 10
    depends_on: [missing]
    config: {url: "https://x.example.com"}
  - key: web
    name: Duplicate
    type: http
    interval_seconds: 60
    timeout_seconds: 10
    config: {url: "https://dup.example.com"}
`
	plan := h.plan(h.admin, bad)
	if plan.Valid || len(plan.Errors) < 3 {
		t.Fatalf("plan = %+v", plan)
	}
	result, err := h.svc.Apply(h.admin, h.tenantID, applyRequest(bad))
	if !errors.Is(err, ErrApplyInvalid) || result == nil || len(result.Errors) == 0 {
		t.Fatalf("apply = %+v, %v", result, err)
	}
	if h.liveMonitors() != 0 || h.versionCount() != 0 {
		t.Fatal("an invalid document wrote")
	}
	if _, err := h.svc.PlanApply(h.admin, h.tenantID, applyRequest("kind: monitor_export\nversion: 1\nmonitors: []\n")); !errors.Is(err, ErrApplyDocument) {
		t.Fatalf("version 1 document = %v", err)
	}
	if _, err := h.svc.PlanApply(h.admin, h.tenantID, applyRequest("kind: monitor_export\nversion: 2\nmonitors:\n  - key: a\n    nmae: typo\n")); !errors.Is(err, ErrApplyDocument) {
		t.Fatalf("unknown field = %v", err)
	}
}

// Dependency and group cycles are refused before anything is written,
// including a cycle that only closes through the workspace's current edges.
func TestApply_Cycles(t *testing.T) {
	h := newApplyHarness(t)
	depCycle := strings.Replace(baseDoc, `    config: {url: "https://db.example.com/health", method: GET, expected_status: 200}`,
		"    depends_on: [web]\n    config: {url: \"https://db.example.com/health\", method: GET, expected_status: 200}", 1)
	if plan := h.plan(h.admin, depCycle); plan.Valid || !strings.Contains(plan.Errors[0].Message, "cycle") {
		t.Fatalf("dependency cycle plan = %+v", plan)
	}
	groupCycle := baseDoc + `
  - key: outer
    name: Outer
    type: group
    interval_seconds: 60
    member_keys: [edge]
    config: {}
`
	groupCycle = strings.Replace(groupCycle, "member_keys: [web, db]", "member_keys: [web, db, outer]", 1)
	if plan := h.plan(h.admin, groupCycle); plan.Valid {
		t.Fatalf("group cycle plan = %+v", plan)
	}
	if h.liveMonitors() != 0 {
		t.Fatal("cycle plan wrote")
	}

	// Swapping a dependency's direction in one apply is not a cycle.
	h.apply(applyRequest(baseDoc))
	swapped := strings.Replace(baseDoc, "    depends_on: [db]\n", "", 1)
	swapped = strings.Replace(swapped, `    config: {url: "https://db.example.com/health", method: GET, expected_status: 200}`,
		"    depends_on: [web]\n    config: {url: \"https://db.example.com/health\", method: GET, expected_status: 200}", 1)
	h.apply(applyRequest(swapped))
	if db := h.monitorByKey("db"); len(db.DependsOnIDs) != 1 || db.DependsOnIDs[0] != h.monitorByKey("web").ID {
		t.Fatalf("swapped dependency = %v", db.DependsOnIDs)
	}
}

// Two applies of one workspace run one after the other: both creating the
// same key end with one monitor, the second apply updating it.
func TestApply_ConcurrentAppliesSerialize(t *testing.T) {
	h := newApplyHarness(t)
	doc := func(name string) string {
		return "kind: monitor_export\nversion: 2\nmonitors:\n  - key: same\n    name: " + name +
			"\n    type: http\n    interval_seconds: 60\n    timeout_seconds: 10\n    config: {url: \"https://same.example.com\", method: GET, expected_status: 200}\n"
	}
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = h.svc.Apply(h.admin, h.tenantID, applyRequest(doc("Same "+string(rune('A'+i)))))
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("apply %d = %v", i, err)
		}
	}
	if n := h.count(`SELECT count(*) FROM monitors WHERE tenant_id = $1 AND key = 'same' AND deleted_at IS NULL`, h.tenantID); n != 1 {
		t.Fatalf("%d monitors with key same, want 1", n)
	}
}

// A SIP test call (a billed call per check) is a tenant admin's to create or
// change, through apply as everywhere; an unchanged one needs nothing.
func TestApply_SIPCallNeedsTenantAdmin(t *testing.T) {
	h := newApplyHarness(t)
	doc := `
kind: monitor_export
version: 2
monitors:
  - key: call
    name: Call
    type: sip
    interval_seconds: 300
    timeout_seconds: 15
    config: {host: trunk.example.com, method: invite, username: trunk, password: secret, call: {number: "+33123456789"}}
`
	apiKey := ctxpkg.WithAuthScope(ctxpkg.WithActorType(ctxpkg.WithAPIKeyID(context.Background(), uuid.NewString()), ctxpkg.ActorTypeAPIKey), "write")
	if plan := h.plan(apiKey, doc); plan.Valid {
		t.Fatalf("API key planned a test-call create: %+v", plan)
	}
	if _, err := h.svc.Apply(apiKey, h.tenantID, applyRequest(doc)); !errors.Is(err, ErrApplyInvalid) {
		t.Fatalf("API key apply = %v", err)
	}
	h.apply(applyRequest(doc))
	if plan := h.plan(apiKey, doc); !plan.Valid || actions(plan)["call"] != "unchanged" {
		t.Fatalf("unchanged test call refused for an API key: %+v", plan)
	}
	// Nor can it create another one.
	if _, err := h.svc.Apply(apiKey, h.tenantID, applyRequest(strings.Replace(doc, "key: call", "key: other", 1))); err == nil {
		t.Fatal("API key created a second test call")
	}
}

// Keys: unique among live monitors, freed by a delete, settable and
// removable through the monitors service.
func TestMonitorKey_UniqueAmongLiveMonitors(t *testing.T) {
	h := newApplyHarness(t)
	key := "dup"
	req := &models.CreateMonitorRequest{Name: "A", Key: &key, Type: models.MonitorTypeHTTP, IntervalSeconds: 60, TimeoutSeconds: 10,
		Config: json.RawMessage(`{"url":"https://a.example.com"}`)}
	first, err := h.monitors.CreateMonitor(h.admin, h.tenantID, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.monitors.CreateMonitor(h.admin, h.tenantID, req); !errors.Is(err, monitorservice.ErrMonitorKeyTaken) {
		t.Fatalf("duplicate key = %v", err)
	}
	if err := h.monitors.DeleteMonitor(h.admin, h.tenantID, first.ID); err != nil {
		t.Fatal(err)
	}
	second, err := h.monitors.CreateMonitor(h.admin, h.tenantID, req)
	if err != nil {
		t.Fatalf("key of a deleted monitor not freed: %v", err)
	}
	empty := ""
	updated, err := h.monitors.UpdateMonitor(h.admin, h.tenantID, second.ID, &models.UpdateMonitorRequest{Key: &empty})
	if err != nil || updated.Key != nil {
		t.Fatalf("remove key = %+v, %v", updated, err)
	}
}
