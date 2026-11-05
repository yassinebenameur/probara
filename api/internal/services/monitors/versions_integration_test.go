//go:build !unit

package monitors

import (
	"context"
	"encoding/json"
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

func storedPassword(t *testing.T, ctx context.Context, db *shareddb.Client, enc secrets.Encryptor, monitorID uuid.UUID) string {
	t.Helper()
	var raw json.RawMessage
	if err := db.QueryRowContext(ctx, `SELECT config FROM monitors WHERE id = $1`, monitorID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	plain, err := secrets.DecryptMonitorConfig(enc, "http", raw)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := decodeConfig(t, plain)["basic_auth_password"].(string)
	return password
}

func TestMonitorConfigVersions_Integration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
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
		Config: json.RawMessage(`{"url":"https://api.example","method":"GET","expected_status":200,"basic_auth_username":"u","basic_auth_password":"first-secret"}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	list, err := store.List(ctx, tenant, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("create recorded %d versions, want 1", len(list.Items))
	}
	v1 := list.Items[0]
	if v1.Version != 1 || v1.Source != VersionSourceUI || v1.ActorType != VersionActorUser || v1.ActorLabel != "alice" {
		t.Fatalf("v1 = %+v", v1)
	}
	if strings.Join(v1.ChangedSecretFields, ",") != "basic_auth_password" {
		t.Fatalf("v1 changed secrets = %v", v1.ChangedSecretFields)
	}
	if list.HistoryStartsAt == nil || list.Truncated {
		t.Fatalf("history start = %v truncated = %v", list.HistoryStartsAt, list.Truncated)
	}
	var rawSnapshot string
	if err := db.QueryRowContext(ctx, `SELECT snapshot::text FROM monitor_config_versions WHERE monitor_id = $1`, created.ID).Scan(&rawSnapshot); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rawSnapshot, "first-secret") || strings.Contains(rawSnapshot, `alg`) || !strings.Contains(rawSnapshot, `"***"`) {
		t.Fatalf("snapshot must be masked: %s", rawSnapshot)
	}

	// Round-tripping the masked config changes nothing: no version.
	if _, err := svc.UpdateMonitor(ctx, tenant, created.ID, &models.UpdateMonitorRequest{Config: created.Config}); err != nil {
		t.Fatal(err)
	}
	if n := versionCount(t, ctx, db, created.ID); n != 1 {
		t.Fatalf("identical write recorded a version (%d)", n)
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

	// A rotated secret is a version even though the masked snapshot is equal.
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
	if v3.SnapshotHash != v2.SnapshotHash || strings.Join(v3.ChangedSecretFields, ",") != "basic_auth_password" {
		t.Fatalf("v3 = %+v", v3.MonitorVersionSummary)
	}

	// Diff against the live monitor: restoring v1 would undo the rename.
	diff, err := store.Diff(ctx, tenant, created.ID, 1, AgainstCurrent)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Changes) != 1 || diff.Changes[0].Path != "name" || diff.Changes[0].Before != "api renamed" || diff.Changes[0].After != "api" {
		t.Fatalf("diff v1 against current = %+v", diff)
	}
	if _, err := store.Diff(ctx, tenant, created.ID, 1, "nope"); err != ErrInvalidAgainst {
		t.Fatalf("bad against = %v", err)
	}

	// Restore v1: the name comes back, the secret stays the current one.
	stored1, err := store.Get(ctx, tenant, created.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	req, err := RestoreRequest(stored1.Snapshot, models.MonitorTypeHTTP)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateMonitor(WithVersionRestore(session, 1), tenant, created.ID, req); err != nil {
		t.Fatal(err)
	}
	if got := storedPassword(t, ctx, db, enc, created.ID); got != "second-secret" {
		t.Fatalf("restore changed the secret to %q", got)
	}
	v4, err := store.Get(ctx, tenant, created.ID, 4)
	if err != nil {
		t.Fatal(err)
	}
	if v4.Source != VersionSourceRestore || v4.RestoredFrom == nil || *v4.RestoredFrom != 1 || v4.SnapshotHash != stored1.SnapshotHash || len(v4.ChangedSecretFields) != 0 {
		t.Fatalf("v4 = %+v", v4.MonitorVersionSummary)
	}

	// Tenant isolation.
	if foreign, err := store.List(ctx, other, created.ID); err != nil || len(foreign.Items) != 0 {
		t.Fatalf("other tenant saw %v (%v)", foreign, err)
	}
	if _, err := store.Get(ctx, other, created.ID, 1); err != ErrVersionNotFound {
		t.Fatalf("other tenant get = %v", err)
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

	// Retention keeps the last MaxVersionsPerMonitor versions.
	for i := 0; i < MaxVersionsPerMonitor+5; i++ {
		tag := []string{"t" + strings.Repeat("x", i%7), uuid.NewString()}
		if _, err := svc.UpdateMonitor(ctx, tenant, second.ID, &models.UpdateMonitorRequest{Tags: &tag}); err != nil {
			t.Fatal(err)
		}
	}
	if n := versionCount(t, ctx, db, second.ID); n != MaxVersionsPerMonitor {
		t.Fatalf("retained %d versions, want %d", n, MaxVersionsPerMonitor)
	}
	pruned, err := store.List(ctx, tenant, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !pruned.Truncated || pruned.Items[0].Version != MaxVersionsPerMonitor+6 || pruned.Items[len(pruned.Items)-1].Version != 7 {
		t.Fatalf("pruned list: truncated=%v newest=%d oldest=%d", pruned.Truncated, pruned.Items[0].Version, pruned.Items[len(pruned.Items)-1].Version)
	}
	if _, err := store.Get(ctx, tenant, second.ID, 1); err != ErrVersionNotFound {
		t.Fatalf("pruned version get = %v", err)
	}

	// Deleting the monitor row cascades its history away.
	if _, err := db.ExecContext(ctx, `DELETE FROM monitors WHERE id = $1`, second.ID); err != nil {
		t.Fatal(err)
	}
	if n := versionCount(t, ctx, db, second.ID); n != 0 {
		t.Fatalf("hard delete left %d versions", n)
	}
}
