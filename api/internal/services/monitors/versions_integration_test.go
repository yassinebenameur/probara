//go:build !unit

package monitors

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/api/internal/models"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func versionCount(t *testing.T, ctx context.Context, db *shareddb.Client, monitorID uuid.UUID) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM monitor_config_versions WHERE monitor_id = $1`, monitorID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// rawVersionRows is every column of every version row of a monitor as text:
// what a secret leak would have to hide in.
func rawVersionRows(t *testing.T, ctx context.Context, db *shareddb.Client, monitorID uuid.UUID) string {
	t.Helper()
	var raw string
	if err := db.QueryRowContext(ctx, `
		SELECT COALESCE(string_agg(row_to_json(v)::text, E'\n'), '') FROM monitor_config_versions v WHERE monitor_id = $1
	`, monitorID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func storedConfig(t *testing.T, ctx context.Context, db *shareddb.Client, enc secrets.Encryptor, monitorID uuid.UUID, typ string) map[string]any {
	t.Helper()
	var raw json.RawMessage
	if err := db.QueryRowContext(ctx, `SELECT config FROM monitors WHERE id = $1`, monitorID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	plain, err := secrets.DecryptMonitorConfig(enc, typ, raw)
	if err != nil {
		t.Fatal(err)
	}
	return decodeConfig(t, plain)
}

func TestMonitorConfigVersions_Integration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenant := testutil.InsertTenant(ctx, t, db, "versions-owner")
	other := testutil.InsertTenant(ctx, t, db, "versions-other")
	var adminID uuid.UUID
	if err := db.QueryRowContext(ctx, `INSERT INTO admin_users (username, password_hash) VALUES ('alice', 'x') RETURNING id`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	session := ctxpkg.WithAdminID(ctx, adminID.String())

	enc := testEncryptor(t)
	svc := NewService(NewPostgresRepository(db))
	svc.ConfigureEncryption(enc)
	store := svc.Versions()
	if store == nil {
		t.Fatal("a Postgres-backed service must record versions without extra wiring")
	}

	created, err := svc.CreateMonitor(session, tenant, &models.CreateMonitorRequest{
		Name: "api", Type: models.MonitorTypeHTTP, IntervalSeconds: 60, TimeoutSeconds: 10,
		Config: json.RawMessage(`{"url":"https://api.example","method":"GET","expected_status":200,` +
			`"basic_auth_username":"u","basic_auth_password":"first-secret","secret_headers":{"X-Api-Key":"key-secret"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	list, err := store.List(ctx, tenant, created.ID, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Total != 1 {
		t.Fatalf("create recorded %d versions, want 1", len(list.Items))
	}
	v1 := list.Items[0]
	if v1.Version != 1 || v1.Source != VersionSourceUI || v1.ActorType != VersionActorUser || v1.ActorLabel != "alice" || v1.ActorID == nil || *v1.ActorID != adminID {
		t.Fatalf("v1 = %+v", v1)
	}
	if strings.Join(v1.ChangedSecretFields, ",") != "basic_auth_password,secret_headers.X-Api-Key" || len(v1.ChangedFields) != 0 {
		t.Fatalf("v1 changed = %v / secrets %v", v1.ChangedFields, v1.ChangedSecretFields)
	}
	if list.HistoryStartsAt == nil || list.Truncated || list.RetentionLimit != MaxVersionsPerMonitor {
		t.Fatalf("list meta = %+v", list)
	}

	// No-op save: round-tripping the masked config records nothing.
	if _, err := svc.UpdateMonitor(ctx, tenant, created.ID, &models.UpdateMonitorRequest{Config: created.Config}); err != nil {
		t.Fatal(err)
	}
	// Pausing is not configuration: no version either.
	paused := false
	if _, err := svc.UpdateMonitor(ctx, tenant, created.ID, &models.UpdateMonitorRequest{Enabled: &paused}); err != nil {
		t.Fatal(err)
	}
	if n := versionCount(t, ctx, db, created.ID); n != 1 {
		t.Fatalf("no-op saves recorded versions (%d)", n)
	}

	name := "api renamed"
	if _, err := svc.UpdateMonitor(ctx, tenant, created.ID, &models.UpdateMonitorRequest{Name: &name}); err != nil {
		t.Fatal(err)
	}
	v2, err := store.Get(ctx, tenant, created.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if v2.Source != VersionSourceAPI || v2.ActorType != VersionActorSystem || strings.Join(v2.ChangedFields, ",") != "name" {
		t.Fatalf("v2 = %+v", v2.MonitorVersionSummary)
	}

	// A rotated secret is a version although the masked snapshot is equal.
	cfg := decodeConfig(t, created.Config)
	cfg["basic_auth_password"] = "second-secret"
	rotated, _ := json.Marshal(cfg)
	if _, err := svc.UpdateMonitor(ctx, tenant, created.ID, &models.UpdateMonitorRequest{Config: rotated}); err != nil {
		t.Fatal(err)
	}
	v3, err := store.Get(ctx, tenant, created.ID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(v3.ChangedSecretFields, ",") != "basic_auth_password" || len(v3.ChangedFields) != 0 {
		t.Fatalf("v3 = %+v", v3.MonitorVersionSummary)
	}

	// No secret ever reaches a version row, in plain text or ciphertext.
	raw := rawVersionRows(t, ctx, db, created.ID)
	for _, leak := range []string{"first-secret", "second-secret", "key-secret", "alg"} {
		if strings.Contains(raw, leak) {
			t.Fatalf("version rows hold %q: %s", leak, raw)
		}
	}
	if !strings.Contains(raw, `***`) {
		t.Fatalf("version rows lost the masks: %s", raw)
	}

	// Diffs: previous (the rotation shows as a valueless secret change) and
	// against the live monitor.
	diff, err := store.Diff(ctx, tenant, created.ID, 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if diff.Against != "2" || len(diff.Changes) != 1 || !diff.Changes[0].Secret || diff.Changes[0].Path != "config.basic_auth_password" ||
		diff.Changes[0].Before != "***" || diff.Changes[0].After != "***" {
		t.Fatalf("diff v3 = %+v", diff)
	}
	diff, err = store.Diff(ctx, tenant, created.ID, 1, AgainstCurrent)
	if err != nil {
		t.Fatal(err)
	}
	if paths(diff.Changes) != "config.basic_auth_password*,name" {
		t.Fatalf("diff v1 against current = %+v", diff.Changes)
	}
	for _, change := range diff.Changes {
		if change.Path == "name" && (change.Before != "api renamed" || change.After != "api") {
			t.Fatalf("name change = %+v", change)
		}
	}
	if _, err := store.Diff(ctx, tenant, created.ID, 1, "nope"); !errors.Is(err, ErrInvalidAgainst) {
		t.Fatalf("bad against = %v", err)
	}

	// Restore v1 the way the handler does: the name comes back, the secret
	// stays the current one, and the new version names its origin.
	stored1, _ := store.Get(ctx, tenant, created.ID, 1)
	current, err := store.CurrentSnapshot(ctx, tenant, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := svc.PlanRestore(ctx, tenant, created.ID, stored1.Snapshot, current)
	if err != nil || !plan.Changed || plan.Request.Config != nil {
		t.Fatalf("restore plan = %+v, %v", plan, err)
	}
	var recorded map[uuid.UUID]int
	if err := svc.InTx(WithVersionRestore(session, 1), func(ctx context.Context) error {
		ctx, batch := DeferVersions(ctx)
		if _, err := svc.UpdateMonitor(ctx, tenant, created.ID, plan.Request); err != nil {
			return err
		}
		recorded = batch.Flush(ctx)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if recorded[created.ID] != 4 {
		t.Fatalf("restore recorded %v", recorded)
	}
	if got := storedConfig(t, ctx, db, enc, created.ID, "http")["basic_auth_password"]; got != "second-secret" {
		t.Fatalf("restore changed the secret to %v", got)
	}
	v4, _ := store.Get(ctx, tenant, created.ID, 4)
	if v4.Source != VersionSourceRestore || v4.RestoredFrom == nil || *v4.RestoredFrom != 1 || strings.Join(v4.ChangedFields, ",") != "name" {
		t.Fatalf("v4 = %+v", v4.MonitorVersionSummary)
	}

	// Tenant isolation: another tenant sees no history and no version.
	if foreign, err := store.List(ctx, other, created.ID, 1, 20); err != nil || len(foreign.Items) != 0 || foreign.Total != 0 {
		t.Fatalf("other tenant listed %+v (%v)", foreign, err)
	}
	if _, err := store.Get(ctx, other, created.ID, 1); !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("other tenant get = %v", err)
	}
	if _, err := store.Diff(ctx, other, created.ID, 1, AgainstCurrent); !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("other tenant diff = %v", err)
	}
	if _, err := store.CurrentSnapshot(ctx, other, created.ID); !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("other tenant snapshot = %v", err)
	}

	// Pagination, newest first.
	page2, err := store.List(ctx, tenant, created.ID, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if page2.Total != 4 || len(page2.Items) != 1 || page2.Items[0].Version != 1 {
		t.Fatalf("page 2 = %+v", page2)
	}

	// A deferred batch records one version for a create plus an update.
	deferred, batch := DeferVersions(ctx)
	second, err := svc.CreateMonitor(deferred, tenant, &models.CreateMonitorRequest{
		Name: "batched", Type: models.MonitorTypeHTTP, IntervalSeconds: 60, TimeoutSeconds: 10,
		Config: json.RawMessage(`{"url":"https://b.example","method":"GET","expected_status":200}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	tags := []string{"prod"}
	if _, err := svc.UpdateMonitor(deferred, tenant, second.ID, &models.UpdateMonitorRequest{Tags: &tags}); err != nil {
		t.Fatal(err)
	}
	if n := versionCount(t, ctx, db, second.ID); n != 0 {
		t.Fatalf("deferred writes recorded %d versions before Flush", n)
	}
	batch.Flush(deferred)
	if n := versionCount(t, ctx, db, second.ID); n != 1 {
		t.Fatalf("flush recorded %d versions, want 1", n)
	}
	only, _ := store.Get(ctx, tenant, second.ID, 1)
	if strings.Join(only.Snapshot.Tags, ",") != "prod" {
		t.Fatalf("flushed snapshot misses the update: %+v", only.Snapshot)
	}

	// Bulk edits record a version per monitor.
	threshold := 5
	if _, err := svc.BulkUpdateAlerting(ctx, tenant, []uuid.UUID{created.ID, second.ID}, &threshold, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if versionCount(t, ctx, db, created.ID) != 5 || versionCount(t, ctx, db, second.ID) != 2 {
		t.Fatalf("bulk edit versions: %d / %d", versionCount(t, ctx, db, created.ID), versionCount(t, ctx, db, second.ID))
	}

	// The cap keeps the newest MaxVersionsPerMonitor versions.
	for i := 0; i < MaxVersionsPerMonitor+5; i++ {
		tag := []string{uuid.NewString()}
		if _, err := svc.UpdateMonitor(ctx, tenant, second.ID, &models.UpdateMonitorRequest{Tags: &tag}); err != nil {
			t.Fatal(err)
		}
	}
	if n := versionCount(t, ctx, db, second.ID); n != MaxVersionsPerMonitor {
		t.Fatalf("kept %d versions, want %d", n, MaxVersionsPerMonitor)
	}
	pruned, err := store.List(ctx, tenant, second.ID, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	newest := MaxVersionsPerMonitor + 7
	if !pruned.Truncated || pruned.Items[0].Version != newest || pruned.Items[len(pruned.Items)-1].Version != newest-MaxVersionsPerMonitor+1 {
		t.Fatalf("pruned list: truncated=%v newest=%d oldest=%d", pruned.Truncated, pruned.Items[0].Version, pruned.Items[len(pruned.Items)-1].Version)
	}
	if _, err := store.Get(ctx, tenant, second.ID, 1); !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("pruned version get = %v", err)
	}

	// History goes with the monitor row (the purger's final delete).
	if _, err := db.ExecContext(ctx, `DELETE FROM monitors WHERE id = $1`, second.ID); err != nil {
		t.Fatal(err)
	}
	if n := versionCount(t, ctx, db, second.ID); n != 0 {
		t.Fatalf("hard delete left %d versions", n)
	}
}

// Restore keeps every current secret, including secret map keys the version
// never had, and reports the secrets the version had that are gone.
func TestMonitorConfigVersions_RestoreSecrets_Integration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenant := testutil.InsertTenant(ctx, t, db, "versions-secret-keys")
	enc := testEncryptor(t)
	svc := NewService(NewPostgresRepository(db))
	svc.ConfigureEncryption(enc)
	store := svc.Versions()

	restore := func(id uuid.UUID, version int) []string {
		t.Helper()
		stored, err := store.Get(ctx, tenant, id, version)
		if err != nil {
			t.Fatal(err)
		}
		current, err := store.CurrentSnapshot(ctx, tenant, id)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := svc.PlanRestore(ctx, tenant, id, stored.Snapshot, current)
		if err != nil || !plan.Changed {
			t.Fatalf("restore plan: %+v %v", plan, err)
		}
		if _, err := svc.UpdateMonitor(WithVersionRestore(ctx, version), tenant, id, plan.Request); err != nil {
			t.Fatal(err)
		}
		return plan.SecretsToReenter
	}

	created, err := svc.CreateMonitor(ctx, tenant, &models.CreateMonitorRequest{
		Name: "keys", Type: models.MonitorTypeHTTP, IntervalSeconds: 60, TimeoutSeconds: 10,
		Config: json.RawMessage(`{"url":"https://k.example","method":"GET","expected_status":200,"secret_headers":{"Authorization":"Bearer one"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateMonitor(ctx, tenant, created.ID, &models.UpdateMonitorRequest{
		Config: json.RawMessage(`{"url":"https://k2.example","method":"GET","expected_status":200,"secret_headers":{"Authorization":"***","X-Api-Key":"key-two"}}`),
	}); err != nil {
		t.Fatal(err)
	}
	if missing := restore(created.ID, 1); len(missing) != 0 {
		t.Fatalf("missing = %v", missing)
	}
	config := storedConfig(t, ctx, db, enc, created.ID, "http")
	headers, _ := config["secret_headers"].(map[string]any)
	if config["url"] != "https://k.example" || headers["Authorization"] != "Bearer one" || headers["X-Api-Key"] != "key-two" {
		t.Fatalf("restore lost the version or a current secret: %v", config)
	}

	// A synthetic journey whose secret variable was removed since: the
	// restore applies the rest and names the secret to enter again.
	journey, err := svc.CreateMonitor(ctx, tenant, &models.CreateMonitorRequest{
		Name: "journey", Type: models.MonitorTypeSyntheticAPI, IntervalSeconds: 60, TimeoutSeconds: 10,
		Config: json.RawMessage(`{"secret_variables":{"token":"t-1"},"steps":[{"id":"a","request":{"method":"GET",` +
			`"url":"https://j.example/me","headers":{"Authorization":"Bearer {{token}}"}}}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateMonitor(ctx, tenant, journey.ID, &models.UpdateMonitorRequest{
		Config: json.RawMessage(`{"secret_variables":{},"steps":[{"id":"a","request":{"method":"GET","url":"https://j.example/v2"}}]}`),
	}); err != nil {
		t.Fatal(err)
	}
	v2, _ := store.Get(ctx, tenant, journey.ID, 2)
	if strings.Join(v2.ChangedSecretFields, ",") != "secret_variables.token" {
		t.Fatalf("v2 secrets = %v", v2.ChangedSecretFields)
	}
	if missing := restore(journey.ID, 1); strings.Join(missing, ",") != "secret_variables.token" {
		t.Fatalf("missing = %v", missing)
	}
	config = storedConfig(t, ctx, db, enc, journey.ID, "synthetic_api")
	if _, present := config["secret_variables"]; present {
		t.Fatalf("restore brought a secret back: %v", config)
	}
	steps, _ := config["steps"].([]any)
	step, _ := steps[0].(map[string]any)
	request, _ := step["request"].(map[string]any)
	if request["url"] != "https://j.example/me" {
		t.Fatalf("restore did not apply the version: %v", config)
	}
}
