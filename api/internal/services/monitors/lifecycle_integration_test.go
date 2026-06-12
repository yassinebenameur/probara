package monitors_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	monitorsvc "github.com/yassinebenameur/probara/api/internal/services/monitors"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// TestMonitorLifecycle_SoftDeleteIsFastAndImmediatelyInvisible proves three
// of the four user-visible guarantees of the soft-delete design:
//  1. DeleteMonitor returns in under 500ms even with child rows present.
//  2. GetMonitor immediately returns "monitor not found".
//  3. A new monitor with the same push_token can be created before the purger
//     has run (partial unique index works).
//
// The fourth guarantee (purger drains the row entirely) is covered by
// TestPurger_RemovesChildRowsThenMonitor in scheduler/internal/scheduler/
// because the purger is an unexported type and Go's internal-package rule
// forbids importing it from api/.
func TestMonitorLifecycle_SoftDeleteIsFastAndImmediatelyInvisible(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")

	repo := monitorsvc.NewPostgresRepository(dbClient)
	svc := monitorsvc.NewService(repo)

	// 1. Create a push monitor with a stable token.
	const token = "tok-lifecycle"
	created, err := svc.CreateMonitor(ctx, tenantID, &models.CreateMonitorRequest{
		Name:            "lifecycle",
		Type:            models.MonitorTypePush,
		Config:          json.RawMessage(`{}`),
		IntervalSeconds: 60,
		TimeoutSeconds:  30,
	})
	require.NoError(t, err)
	_, err = dbClient.ExecContext(ctx,
		`UPDATE monitors SET push_token = $1 WHERE id = $2`, token, created.ID)
	require.NoError(t, err)

	// Seed child rows so the *would-be* synchronous cascade has real work to
	// do — this proves the soft-delete path doesn't touch them.
	for i := 0; i < 20; i++ {
		_, err := dbClient.ExecContext(ctx, `
			INSERT INTO check_results (id, tenant_id, monitor_id, job_id, status, latency_ms, created_at)
			VALUES (gen_random_uuid(), $1, $2, gen_random_uuid(), 'success', 10, NOW())
		`, tenantID, created.ID)
		require.NoError(t, err)
	}

	// 2. Soft delete and assert it's fast.
	start := time.Now()
	require.NoError(t, svc.DeleteMonitor(ctx, tenantID, created.ID))
	require.Less(t, time.Since(start), 500*time.Millisecond,
		"soft delete must return in under 500ms")

	// 3. The monitor is immediately invisible to GetMonitor.
	_, err = svc.GetMonitor(ctx, tenantID, created.ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "monitor not found")

	// 4. Reimport with the same push_token must succeed *before* any purge —
	// that's the whole point of the partial unique index.
	reimported, err := svc.CreateMonitor(ctx, tenantID, &models.CreateMonitorRequest{
		Name:            "lifecycle-reimport",
		Type:            models.MonitorTypePush,
		Config:          json.RawMessage(`{}`),
		IntervalSeconds: 60,
		TimeoutSeconds:  30,
	})
	require.NoError(t, err)
	_, err = dbClient.ExecContext(ctx,
		`UPDATE monitors SET push_token = $1 WHERE id = $2`, token, reimported.ID)
	require.NoError(t, err, "should be allowed to reuse push_token while old row is tombstoned")

	// Sanity check: the tombstoned row still exists in the DB (purger hasn't run).
	var deletedAt *time.Time
	require.NoError(t, dbClient.QueryRowContext(ctx,
		`SELECT deleted_at FROM monitors WHERE id = $1`, created.ID,
	).Scan(&deletedAt))
	require.NotNil(t, deletedAt, "original row should still be present, tombstoned")
}
