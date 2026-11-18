//go:build !unit

package monitorstate

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/testutil"
)

// A result for a deleted workspace (a job dispatched just before the delete,
// a push or OTLP report racing it) stores nothing and moves no state: every
// result writer inserts through insertResult.
func TestRecordIgnoresDeletedWorkspace(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "deleted")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "target")
	if _, err := dbClient.ExecContext(ctx, `UPDATE tenants SET deleted_at = NOW() WHERE id = $1`, tenantID); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	transition, err := Record(ctx, dbClient.DB, Result{
		MonitorID: monitorID, TenantID: tenantID, JobID: uuid.New(),
		Status: "failure", ResultSource: "monitor", StartedAt: now, CompletedAt: now,
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if !transition.Duplicate || transition.Changed {
		t.Fatalf("transition = %+v, want a no-op", transition)
	}
	inserted, err := InsertOnly(ctx, dbClient.DB, Result{
		MonitorID: monitorID, TenantID: tenantID, JobID: uuid.New(),
		Status: "error", ResultSource: "platform", StartedAt: now, CompletedAt: now,
	})
	if err != nil || inserted {
		t.Fatalf("InsertOnly = %v, %v; want nothing inserted", inserted, err)
	}
	var rows, dirty int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM check_results WHERE monitor_id = $1),
		       (SELECT COUNT(*) FROM rollup_dirty WHERE monitor_id = $1)`, monitorID).Scan(&rows, &dirty); err != nil {
		t.Fatal(err)
	}
	if rows != 0 || dirty != 0 {
		t.Fatalf("check_results = %d, rollup_dirty = %d; want none", rows, dirty)
	}
}
