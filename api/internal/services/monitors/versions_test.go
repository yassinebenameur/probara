package monitors

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/yassinebenameur/probara/api/internal/models"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

func TestSecretFieldChanges(t *testing.T) {
	stored := json.RawMessage(`{"url":"https://a","basic_auth_password":"env:1","secret_headers":{"Authorization":"env:2","X-Key":"env:3"}}`)
	cases := []struct {
		name   string
		before json.RawMessage
		after  json.RawMessage
		want   []string
	}{
		{"create sets every present secret", nil, stored, []string{"basic_auth_password", "secret_headers.Authorization", "secret_headers.X-Key"}},
		{"kept envelopes are no change", stored, json.RawMessage(`{"url":"https://b","basic_auth_password":"env:1","secret_headers":{"X-Key":"env:3","Authorization":"env:2"}}`), []string{}},
		{"replace and clear", stored, json.RawMessage(`{"url":"https://a","basic_auth_password":"env:9","secret_headers":{"Authorization":"env:2"}}`), []string{"basic_auth_password", "secret_headers.X-Key"}},
		{"cleared scalar", stored, json.RawMessage(`{"url":"https://a","secret_headers":{"Authorization":"env:2","X-Key":"env:3"}}`), []string{"basic_auth_password"}},
		{"added map key", stored, json.RawMessage(`{"url":"https://a","basic_auth_password":"env:1","secret_headers":{"Authorization":"env:2","X-Key":"env:3","Cookie":"env:4"}}`), []string{"secret_headers.Cookie"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			beforeType := models.MonitorType("")
			if tc.before != nil {
				beforeType = models.MonitorTypeHTTP
			}
			got := SecretFieldChanges(beforeType, tc.before, models.MonitorTypeHTTP, tc.after)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("SecretFieldChanges = %v, want %v", got, tc.want)
			}
		})
	}
	if got := SecretFieldChanges(models.MonitorTypePing, nil, models.MonitorTypePing, json.RawMessage(`{"host":"x"}`)); got != nil {
		t.Fatalf("type without secrets reported %v", got)
	}
}

func snapshot(config string) *models.MonitorConfigSnapshot {
	s := &models.MonitorConfigSnapshot{
		Name: "api", Type: models.MonitorTypeHTTP, Config: json.RawMessage(config),
		IntervalSeconds: 60, TimeoutSeconds: 10, NotificationMode: "default",
		MemberAlertRollup: "per_monitor", DependencySuppression: "inherit", LocationQuorum: 1, RecoveryThreshold: 1,
	}
	canonical, err := canonicalJSON(s.Config)
	if err != nil {
		panic(err)
	}
	s.Config = canonical
	normalizeSnapshot(s)
	return s
}

func TestSnapshotHashIgnoresKeyOrderAndFormatting(t *testing.T) {
	a := snapshot(`{"url":"https://a","method":"GET","expected_status":200}`)
	b := snapshot(`{ "method": "GET", "expected_status": 200, "url": "https://a" }`)
	ha, err := snapshotHash(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, _ := snapshotHash(b)
	if ha != hb {
		t.Fatalf("equal configs hash differently: %s vs %s", ha, hb)
	}
	c := snapshot(`{"url":"https://a","method":"GET","expected_status":201}`)
	if hc, _ := snapshotHash(c); hc == ha {
		t.Fatal("a config change did not change the hash")
	}
}

func TestDiffSnapshots(t *testing.T) {
	before := snapshot(`{"url":"https://a","method":"GET","headers":{"Accept":"json"}}`)
	after := snapshot(`{"url":"https://b","method":"GET","headers":{"Accept":"json","X":"1"},"follow_redirects":true}`)
	after.Tags = []string{"prod"}
	after.LocationIDs = []string{"loc-1"}

	changes, err := DiffSnapshots(before, after)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, change := range changes {
		paths = append(paths, change.Path)
	}
	want := []string{"config.follow_redirects", "config.headers.X", "config.url", "location_ids", "tags"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	for _, change := range changes {
		switch change.Path {
		case "config.url":
			if change.Before != "https://a" || change.After != "https://b" {
				t.Fatalf("config.url change = %+v", change)
			}
		case "config.follow_redirects":
			if change.Before != nil || change.After != true {
				t.Fatalf("added field = %+v", change)
			}
		}
	}

	same, err := DiffSnapshots(before, before)
	if err != nil || len(same) != 0 {
		t.Fatalf("identical snapshots differ: %v %v", same, err)
	}
	all, err := diffAll(nil, before)
	if err != nil || len(all) == 0 {
		t.Fatalf("diff against nothing should list every field: %v %v", all, err)
	}
	for _, change := range all {
		if change.Before != nil {
			t.Fatalf("diff against nothing has a before: %+v", change)
		}
	}
}

func TestRestoreRequest(t *testing.T) {
	s := snapshot(`{"url":"https://a","basic_auth_username":"u","basic_auth_password":"***"}`)
	s.NotificationMode = "custom"
	s.NotificationChannels = []models.MonitorSnapshotChannel{}
	s.DependsOnIDs = []string{"dep-1"}
	req, err := RestoreRequest(*s, models.MonitorTypeHTTP)
	if err != nil {
		t.Fatal(err)
	}
	if req.Type != nil {
		t.Fatal("unchanged type must not be sent")
	}
	if req.NotificationChannels == nil {
		t.Fatal("custom mode must send the (empty) route set, not leave it unchanged")
	}
	if req.DependsOnIDs == nil || !reflect.DeepEqual(*req.DependsOnIDs, []string{"dep-1"}) {
		t.Fatalf("dependencies = %v", req.DependsOnIDs)
	}
	if decodeConfigMap(req.Config)["basic_auth_password"] != "***" {
		t.Fatal("restore must submit the masked secret so the stored one is kept")
	}
	if req.Enabled != nil {
		t.Fatal("restore must not touch pause state")
	}

	s.NotificationMode = "default"
	req, _ = RestoreRequest(*s, models.MonitorTypeTCP)
	if req.NotificationChannels != nil {
		t.Fatal("default mode must leave channels to the mode switch")
	}
	if req.Type == nil || *req.Type != models.MonitorTypeHTTP {
		t.Fatal("a changed type must be sent")
	}

	group := snapshot(`{"monitor_ids":["stale"]}`)
	group.Type = models.MonitorTypeGroup
	group.MemberIDs = []string{"m-1", "m-2"}
	req, err = RestoreRequest(*group, models.MonitorTypeGroup)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeConfigMap(req.Config)["monitor_ids"]; !reflect.DeepEqual(got, []any{"m-1", "m-2"}) {
		t.Fatalf("group members travel as config.monitor_ids, got %v", got)
	}
	if req.DependsOnIDs != nil {
		t.Fatal("groups have no dependencies")
	}
}

func TestVersionSourceFromContext(t *testing.T) {
	ctx := context.Background()
	if got := versionSourceFromContext(ctx); got != VersionSourceAPI {
		t.Fatalf("bare context source = %q", got)
	}
	session := ctxpkg.WithAdminID(ctx, "00000000-0000-0000-0000-0000000000aa")
	if got := versionSourceFromContext(session); got != VersionSourceUI {
		t.Fatalf("session source = %q", got)
	}
	if got := versionSourceFromContext(ctxpkg.WithVia(session, ctxpkg.ViaMCP)); got != VersionSourceMCP {
		t.Fatalf("mcp source = %q", got)
	}
	if got := versionSourceFromContext(WithVersionSource(session, VersionSourceImport)); got != VersionSourceImport {
		t.Fatalf("explicit source = %q", got)
	}
	meta := versionMetaFromContext(WithVersionRestore(session, 3), nil)
	if meta.source != VersionSourceRestore || meta.restoredFrom == nil || *meta.restoredFrom != 3 {
		t.Fatalf("restore meta = %+v", meta)
	}
	if meta.actor.typ != VersionActorUser || meta.actor.id == nil {
		t.Fatalf("session actor = %+v", meta.actor)
	}
	if actor := versionActorFromContext(ctx); actor.typ != VersionActorSystem {
		t.Fatalf("bare actor = %+v", actor)
	}
}

func TestDeferVersionsNests(t *testing.T) {
	ctx, outer := DeferVersions(context.Background())
	if outer == nil {
		t.Fatal("outer batch missing")
	}
	inner, batch := DeferVersions(ctx)
	if batch != nil || inner != ctx {
		t.Fatal("a nested DeferVersions must defer to the outer batch")
	}
	batch.Flush(inner) // nil batch: no-op, no panic
}
