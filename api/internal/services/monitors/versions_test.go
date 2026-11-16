package monitors

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

func snapshotFor(t *testing.T, typ models.MonitorType, config string) models.MonitorConfigSnapshot {
	t.Helper()
	canonical, err := canonicalJSON(json.RawMessage(config))
	if err != nil {
		t.Fatal(err)
	}
	s := models.MonitorConfigSnapshot{
		Name: "api", Type: typ, Config: canonical, IntervalSeconds: 60, TimeoutSeconds: 10,
		ConsecutiveFailuresThreshold: 2, NotificationMode: "default", MemberAlertRollup: "per_monitor",
		DependencySuppression: "inherit", LocationQuorum: 1,
	}
	normalizeSnapshot(&s)
	return s
}

func paths(changes []models.MonitorVersionChange) string {
	out := make([]string, len(changes))
	for i, change := range changes {
		out[i] = change.Path
		if change.Secret {
			out[i] += "*"
		}
	}
	return strings.Join(out, ",")
}

func TestSecretFieldChanges(t *testing.T) {
	before := json.RawMessage(`{"url":"u","basic_auth_password":"env-1","secret_headers":{"Authorization":"env-a","X-Old":"env-o"}}`)
	after := json.RawMessage(`{"url":"u2","basic_auth_password":"env-1","secret_headers":{"Authorization":"env-b","X-New":"env-n"}}`)
	got := strings.Join(SecretFieldChanges(models.MonitorTypeHTTP, before, models.MonitorTypeHTTP, after), ",")
	if got != "secret_headers.Authorization,secret_headers.X-New,secret_headers.X-Old" {
		t.Fatalf("changes = %s", got)
	}
	// A kept secret (same envelope) is no change; a cleared one is.
	cleared := json.RawMessage(`{"url":"u"}`)
	if got := strings.Join(SecretFieldChanges(models.MonitorTypeHTTP, before, models.MonitorTypeHTTP, cleared), ","); got != "basic_auth_password,secret_headers.Authorization,secret_headers.X-Old" {
		t.Fatalf("cleared = %s", got)
	}
	// Create: everything set is a change; types without secrets have none.
	if got := strings.Join(SecretFieldChanges("", nil, models.MonitorTypePostgres, json.RawMessage(`{"password":"env"}`)), ","); got != "password" {
		t.Fatalf("create = %s", got)
	}
	if got := SecretFieldChanges(models.MonitorTypePing, nil, models.MonitorTypePing, json.RawMessage(`{"host":"h"}`)); got != nil {
		t.Fatalf("ping = %v", got)
	}
}

func TestDiffSnapshots_LeavesAndSecrets(t *testing.T) {
	before := snapshotFor(t, models.MonitorTypeHTTP, `{"url":"https://a","method":"GET","headers":{"Accept":"json"}}`)
	after := snapshotFor(t, models.MonitorTypeHTTP, `{"url":"https://b","method":"GET","secret_headers":{"Authorization":"***"},"basic_auth_password":"***"}`)
	after.Tags = []string{"prod"}
	changes, err := diffSnapshots(&before, &after)
	if err != nil {
		t.Fatal(err)
	}
	want := "config.basic_auth_password*,config.headers.Accept,config.secret_headers.Authorization*,config.url,tags"
	if got := paths(changes); got != want {
		t.Fatalf("paths = %s, want %s", got, want)
	}
	for _, change := range changes {
		if change.Secret && (change.Before != nil || change.After != "***") {
			t.Fatalf("secret change carries a value: %+v", change)
		}
	}
	// No base: everything reads as added.
	all, err := diffSnapshots(nil, &before)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) == 0 || all[0].Before != nil {
		t.Fatalf("diff against nothing = %+v", all)
	}
}

func TestWithSecretChanges_ShowsRotationWithoutValue(t *testing.T) {
	before := snapshotFor(t, models.MonitorTypePostgres, `{"host":"db","password":"***"}`)
	after := snapshotFor(t, models.MonitorTypePostgres, `{"host":"db","password":"***","connection_string":""}`)
	changes, err := diffSnapshots(&before, &after)
	if err != nil {
		t.Fatal(err)
	}
	changes = withSecretChanges(changes, &before, &after, []string{"password", "tls_client_key_pem"})
	if got := paths(changes); got != "config.connection_string*,config.password*" {
		t.Fatalf("paths = %s", got)
	}
	for _, change := range changes {
		if change.Path == "config.password" && (change.Before != "***" || change.After != "***") {
			t.Fatalf("rotation = %+v", change)
		}
	}
}

func TestPlanRestore_SendsOnlyDifferencesAndKeepsSecretKeys(t *testing.T) {
	version := snapshotFor(t, models.MonitorTypeHTTP, `{"url":"https://a","secret_headers":{"Authorization":"***"}}`)
	current := snapshotFor(t, models.MonitorTypeHTTP, `{"url":"https://b","secret_headers":{"Authorization":"***","X-Api-Key":"***"}}`)
	stored := json.RawMessage(`{"url":"https://b","secret_headers":{"Authorization":"env-a","X-Api-Key":"env-k"}}`)

	same, err := planRestore(current, &current, stored)
	if err != nil || same.Changed {
		t.Fatalf("restoring the live config = %+v, %v", same, err)
	}

	plan, err := planRestore(version, &current, stored)
	if err != nil || !plan.Changed {
		t.Fatalf("planRestore = %+v, %v", plan, err)
	}
	req := plan.Request
	if req.Name != nil || req.IntervalSeconds != nil || req.LocationIDs != nil || req.NotificationMode != nil || req.Type != nil {
		t.Fatalf("unchanged fields were sent: %+v", req)
	}
	config := decodeConfigMap(req.Config)
	headers, _ := config["secret_headers"].(map[string]any)
	if config["url"] != "https://a" || headers["Authorization"] != "***" || headers["X-Api-Key"] != "***" || len(plan.SecretsToReenter) != 0 {
		t.Fatalf("restored config = %s, reenter %v", req.Config, plan.SecretsToReenter)
	}

	// The field list drives the plain columns.
	renamed := current
	renamed.Name = "other"
	renamed.ConsecutiveFailuresThreshold = 7
	plan, _ = planRestore(renamed, &current, stored)
	if plan.Request.Name == nil || *plan.Request.Name != "other" || plan.Request.ConsecutiveFailuresThreshold == nil || plan.Request.Config != nil {
		t.Fatalf("column restore = %+v", plan.Request)
	}

	// Group members ride in config.monitor_ids; locations carry the quorum.
	group := snapshotFor(t, models.MonitorTypeGroup, `{"monitor_ids":["a"]}`)
	group.MemberIDs = []string{"m1", "m2"}
	group.LocationIDs = []string{"l1", "l2"}
	group.LocationQuorum = 2
	liveGroup := snapshotFor(t, models.MonitorTypeGroup, `{"monitor_ids":["a"]}`)
	plan, err = planRestore(group, &liveGroup, nil)
	if err != nil || !plan.Changed {
		t.Fatalf("group restore = %+v, %v", plan, err)
	}
	var groupConfig models.GroupConfig
	_ = json.Unmarshal(plan.Request.Config, &groupConfig)
	if strings.Join(groupConfig.MonitorIDs, ",") != "m1,m2" || plan.Request.LocationIDs == nil || *plan.Request.LocationQuorum != 2 || plan.Request.DependsOnIDs != nil {
		t.Fatalf("group request = %+v (%s)", plan.Request, plan.Request.Config)
	}

	// A custom route set is sent whole, non-nil even when empty.
	custom := current
	custom.NotificationMode = "custom"
	plan, _ = planRestore(custom, &current, stored)
	if plan.Request.NotificationMode == nil || plan.Request.NotificationChannels == nil {
		t.Fatalf("custom routing request = %+v", plan.Request)
	}
}

// Whatever a version holds, a restore writes no secret value from it: a
// literal secret (a version recorded before snapshots were scrubbed), an
// emptied secret ("" would clear the stored one) or a plain credential
// header (the save would move it over the stored secret).
func TestRestoreConfig_NeverWritesASecretFromAVersion(t *testing.T) {
	versionConfig := json.RawMessage(`{"url":"https://a","basic_auth_username":"ops","basic_auth_password":"leaked",` +
		`"headers":{"Authorization":"Bearer leaked","Accept":"json"},"Basic_Auth_Password":"leaked-2",` +
		`"secret_headers":{"X-Old":"leaked-3"}}`)
	stored := json.RawMessage(`{"url":"https://b","basic_auth_username":"ops","basic_auth_password":"env-pw","secret_headers":{"Authorization":"env-auth"}}`)
	out, reenter, err := restoreConfig("http", versionConfig, stored)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "leaked") {
		t.Fatalf("restore would write a version's secret: %s", out)
	}
	cfg := decodeConfigMap(out)
	headers, _ := cfg["headers"].(map[string]any)
	secretHeaders, _ := cfg["secret_headers"].(map[string]any)
	if cfg["basic_auth_password"] != "***" || headers["Accept"] != "json" || headers["Authorization"] != nil ||
		secretHeaders["Authorization"] != "***" || secretHeaders["X-Old"] != "***" {
		t.Fatalf("restore config = %s", out)
	}
	if strings.Join(reenter, ",") != "secret_headers.X-Old" {
		t.Fatalf("reenter = %v", reenter)
	}

	// An emptied secret keeps the stored one; a removed secret variable and
	// a plain credential nothing stored backs are reported.
	out, reenter, _ = restoreConfig("postgres", json.RawMessage(`{"host":"db","connection_string":"","password":"***"}`), json.RawMessage(`{"host":"db","connection_string":"env"}`))
	if decodeConfigMap(out)["connection_string"] != "***" || strings.Join(reenter, ",") != "password" {
		t.Fatalf("postgres restore = %s, %v", out, reenter)
	}
	// A masked URL credential keeps the stored one when the URL is the same,
	// and is left out (and reported) when it is not.
	out, reenter, _ = restoreConfig("http", json.RawMessage(`{"url":"https://ops:***@a.example/x?token=***"}`),
		json.RawMessage(`{"url":"https://ops:pw@a.example/x?token=tk"}`))
	if decodeConfigMap(out)["url"] != "https://ops:pw@a.example/x?token=tk" || len(reenter) != 0 {
		t.Fatalf("same URL restore = %s, %v", out, reenter)
	}
	out, reenter, _ = restoreConfig("http", json.RawMessage(`{"url":"https://ops:***@a.example/x?token=***"}`),
		json.RawMessage(`{"url":"https://b.example/"}`))
	if decodeConfigMap(out)["url"] != "https://ops@a.example/x" || strings.Join(reenter, ",") != "url (URL credentials)" {
		t.Fatalf("changed URL restore = %s, %v", out, reenter)
	}
	_, reenter, _ = restoreConfig("synthetic_api", json.RawMessage(`{"secret_variables":{"token":"***","user":"***"},"variables":{"api_key":"***"},"steps":[]}`),
		json.RawMessage(`{"secret_variables":{"USER":"env"},"steps":[]}`))
	if strings.Join(reenter, ",") != "secret_variables.token,variables.api_key" {
		t.Fatalf("synthetic reenter = %v", reenter)
	}
}

func TestVersionMetaFromContext(t *testing.T) {
	admin := uuid.New()
	key := uuid.New()
	ctx := context.Background()
	cases := []struct {
		name   string
		ctx    context.Context
		source string
		actor  string
	}{
		{"nothing", ctx, VersionSourceAPI, VersionActorSystem},
		{"session", ctxpkg.WithAdminID(ctx, admin.String()), VersionSourceUI, VersionActorUser},
		{"api key", ctxpkg.WithAPIKeyID(ctx, key.String()), VersionSourceAPI, VersionActorAPIKey},
		{"mcp", ctxpkg.WithVia(ctxpkg.WithAPIKeyID(ctx, key.String()), ctxpkg.ViaMCP), VersionSourceMCP, VersionActorAPIKey},
		{"import", WithVersionSource(ctxpkg.WithAdminID(ctx, admin.String()), VersionSourceImport), VersionSourceImport, VersionActorUser},
		{"restore", WithVersionRestore(ctx, 3), VersionSourceRestore, VersionActorSystem},
	}
	for _, tc := range cases {
		meta := versionMetaFromContext(tc.ctx, nil)
		if meta.source != tc.source || meta.actor.typ != tc.actor {
			t.Fatalf("%s: source %s actor %s", tc.name, meta.source, meta.actor.typ)
		}
	}
	if meta := versionMetaFromContext(WithVersionRestore(ctx, 3), nil); meta.restoredFrom == nil || *meta.restoredFrom != 3 {
		t.Fatalf("restore meta = %+v", meta)
	}
}

func TestDeferVersions_NestedAndNilBatch(t *testing.T) {
	ctx, batch := DeferVersions(context.Background())
	if batch == nil {
		t.Fatal("outer batch is nil")
	}
	if _, inner := DeferVersions(ctx); inner != nil {
		t.Fatal("a nested DeferVersions must leave recording to the outer batch")
	}
	var none *VersionBatch
	if got := none.Flush(ctx); len(got) != 0 {
		t.Fatalf("nil batch flushed %v", got)
	}
}

func TestCanonicalJSON(t *testing.T) {
	a, _ := canonicalJSON(json.RawMessage(`{"b": 1.50, "a": {"y": 2, "x": 1}}`))
	b, _ := canonicalJSON(json.RawMessage(`{"a":{"x":1,"y":2},"b":1.50}`))
	if string(a) != string(b) || string(a) != `{"a":{"x":1,"y":2},"b":1.50}` {
		t.Fatalf("canonical = %s / %s", a, b)
	}
	if empty, _ := canonicalJSON(nil); string(empty) != "{}" {
		t.Fatalf("empty = %s", empty)
	}
}
