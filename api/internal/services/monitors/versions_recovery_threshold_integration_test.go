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
	"github.com/yassinebenameur/probara/shared/testutil"
)

// recovery_threshold (docs/state-semantics.md S-T7) is a versioned setting:
// a change records it, the bulk edit records it, a restore brings it back,
// and the runtime counters beside it are never versioned. A version written
// before the setting existed reads as 1.
func TestMonitorConfigVersions_RecoveryThreshold_Integration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenant := testutil.InsertTenant(ctx, t, db, "versions-recovery")
	svc := NewService(NewPostgresRepository(db))
	store := svc.Versions()
	n := func(v int) *int { return &v }

	created, err := svc.CreateMonitor(ctx, tenant, &models.CreateMonitorRequest{
		Name: "api", Type: models.MonitorTypeHTTP, IntervalSeconds: 60, TimeoutSeconds: 10,
		Config: json.RawMessage(`{"url":"https://api.example","method":"GET","expected_status":200}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateMonitor(ctx, tenant, created.ID, &models.UpdateMonitorRequest{RecoveryThreshold: n(3)}); err != nil {
		t.Fatal(err)
	}
	v2, err := store.Get(ctx, tenant, created.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(v2.ChangedFields, ",") != "recovery_threshold" || v2.Snapshot.RecoveryThreshold != 3 {
		t.Fatalf("v2 changed %v, snapshot threshold %d, want recovery_threshold / 3", v2.ChangedFields, v2.Snapshot.RecoveryThreshold)
	}

	// Runtime state is not configuration: moving the counters records nothing.
	if _, err := db.ExecContext(ctx, `
		UPDATE monitors SET current_state = 'down', consecutive_successes = 2, recovery_pending = TRUE WHERE id = $1
	`, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateMonitor(ctx, tenant, created.ID, &models.UpdateMonitorRequest{RecoveryThreshold: n(3)}); err != nil {
		t.Fatal(err)
	}
	if count := versionCount(t, ctx, db, created.ID); count != 2 {
		t.Fatalf("a no-op save over changed counters recorded a version (%d versions)", count)
	}
	raw := rawVersionRows(t, ctx, db, created.ID)
	if strings.Contains(raw, "consecutive_successes") || strings.Contains(raw, "recovery_pending") {
		t.Fatalf("runtime counters leaked into a snapshot: %s", raw)
	}

	// The bulk edit carries the threshold and records it.
	if _, err := svc.BulkUpdateAlerting(ctx, tenant, []uuid.UUID{created.ID}, nil, n(5), nil, nil); err != nil {
		t.Fatal(err)
	}
	v3, err := store.Get(ctx, tenant, created.ID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(v3.ChangedFields, ",") != "recovery_threshold" || v3.Snapshot.RecoveryThreshold != 5 {
		t.Fatalf("bulk v3 changed %v, threshold %d", v3.ChangedFields, v3.Snapshot.RecoveryThreshold)
	}

	// Restoring v2 brings 3 back.
	current, err := store.CurrentSnapshot(ctx, tenant, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := svc.PlanRestore(ctx, tenant, created.ID, v2.Snapshot, current)
	if err != nil || !plan.Changed || plan.Request.RecoveryThreshold == nil || *plan.Request.RecoveryThreshold != 3 {
		t.Fatalf("restore plan = %+v, %v", plan, err)
	}
	if err := svc.InTx(WithVersionRestore(ctx, 2), func(ctx context.Context) error {
		_, err := svc.UpdateMonitor(ctx, tenant, created.ID, plan.Request)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	restored, err := svc.GetMonitor(ctx, tenant, created.ID)
	if err != nil || restored.RecoveryThreshold != 3 {
		t.Fatalf("restored threshold = %v (%v), want 3", restored, err)
	}

	// A version recorded before the setting existed (no key) reads as 1, so
	// restoring it sets 1, never an invalid 0.
	if _, err := db.ExecContext(ctx, `
		UPDATE monitor_config_versions SET snapshot = snapshot - 'recovery_threshold'
		WHERE monitor_id = $1 AND version = 1
	`, created.ID); err != nil {
		t.Fatal(err)
	}
	v1, err := store.Get(ctx, tenant, created.ID, 1)
	if err != nil || v1.Snapshot.RecoveryThreshold != 1 {
		t.Fatalf("legacy v1 threshold = %+v (%v), want 1", v1, err)
	}
	current, err = store.CurrentSnapshot(ctx, tenant, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = svc.PlanRestore(ctx, tenant, created.ID, v1.Snapshot, current)
	if err != nil || plan.Request.RecoveryThreshold == nil || *plan.Request.RecoveryThreshold != 1 {
		t.Fatalf("legacy restore plan = %+v, %v", plan, err)
	}
}
