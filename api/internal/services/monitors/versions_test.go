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

func TestRestoreRequest_SendsOnlyDifferencesAndKeepsSecretKeys(t *testing.T) {
	version := snapshotFor(t, models.MonitorTypeHTTP, `{"url":"https://a","secret_headers":{"Authorization":"***"}}`)
	current := snapshotFor(t, models.MonitorTypeHTTP, `{"url":"https://b","secret_headers":{"Authorization":"***","X-Api-Key":"***"}}`)

	same, ok, err := RestoreRequest(current, &current)
	if err != nil || ok {
		t.Fatalf("restoring the live config = %+v, %v, %v", same, ok, err)
	}

	req, ok, err := RestoreRequest(version, &current)
	if err != nil || !ok {
		t.Fatalf("RestoreRequest = %v, %v", ok, err)
	}
	if req.Name != nil || req.IntervalSeconds != nil || req.LocationIDs != nil || req.NotificationMode != nil || req.Type != nil {
		t.Fatalf("unchanged fields were sent: %+v", req)
	}
	config := decodeConfigMap(req.Config)
	headers, _ := config["secret_headers"].(map[string]any)
	if config["url"] != "https://a" || headers["Authorization"] != "***" || headers["X-Api-Key"] != "***" {
		t.Fatalf("restored config = %s", req.Config)
	}

	// Group members ride in config.monitor_ids; locations carry the quorum.
	group := snapshotFor(t, models.MonitorTypeGroup, `{"monitor_ids":["a"]}`)
	group.MemberIDs = []string{"m1", "m2"}
	group.LocationIDs = []string{"l1", "l2"}
	group.LocationQuorum = 2
	liveGroup := snapshotFor(t, models.MonitorTypeGroup, `{"monitor_ids":["a"]}`)
	req, ok, err = RestoreRequest(group, &liveGroup)
	if err != nil || !ok {
		t.Fatalf("group restore = %v, %v", ok, err)
	}
	var groupConfig models.GroupConfig
	_ = json.Unmarshal(req.Config, &groupConfig)
	if strings.Join(groupConfig.MonitorIDs, ",") != "m1,m2" || req.LocationIDs == nil || *req.LocationQuorum != 2 || req.DependsOnIDs != nil {
		t.Fatalf("group request = %+v (%s)", req, req.Config)
	}

	// A custom route set is sent whole, non-nil even when empty.
	custom := current
	custom.NotificationMode = "custom"
	req, _, _ = RestoreRequest(custom, &current)
	if req.NotificationMode == nil || req.NotificationChannels == nil {
		t.Fatalf("custom routing request = %+v", req)
	}
}

func TestSecretsToReenter(t *testing.T) {
	version := snapshotFor(t, models.MonitorTypeSyntheticAPI, `{"secret_variables":{"token":"***","user":"***"},"steps":[]}`)
	got := SecretsToReenter(version, json.RawMessage(`{"secret_variables":{"USER":"***"}}`))
	if strings.Join(got, ",") != "secret_variables.token" {
		t.Fatalf("missing = %v", got)
	}
	db := snapshotFor(t, models.MonitorTypePostgres, `{"host":"h","password":"***"}`)
	if got := SecretsToReenter(db, json.RawMessage(`{"host":"h"}`)); strings.Join(got, ",") != "password" {
		t.Fatalf("missing = %v", got)
	}
	if got := SecretsToReenter(db, json.RawMessage(`{"host":"h","password":"env"}`)); len(got) != 0 {
		t.Fatalf("kept secret reported missing: %v", got)
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
