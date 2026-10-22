//go:build !unit

package scheduler

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestScheduler_FetchDueMonitors_ExcludesSoftDeleted(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")
	live := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "live")
	gone := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "gone")

	if _, err := dbClient.ExecContext(ctx,
		`UPDATE monitors SET deleted_at = NOW() WHERE id = $1`, gone,
	); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	s := &Scheduler{db: dbClient}
	tx, err := dbClient.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()

	monitors, err := s.fetchDueMonitors(ctx, tx, 10)
	if err != nil {
		t.Fatalf("fetchDueMonitors: %v", err)
	}

	var sawLive, sawGone bool
	for _, m := range monitors {
		if m.ID == live {
			sawLive = true
		}
		if m.ID == gone {
			sawGone = true
		}
	}
	if !sawLive {
		t.Error("expected live monitor in batch; not found")
	}
	if sawGone {
		t.Error("soft-deleted monitor must NOT appear in batch")
	}
}

// Group, push and agent monitors are never executed by a worker, so the
// dispatch batch must not carry them (a push job used to reach the worker
// every suspect recheck and fail as "Unknown monitor type").
func TestScheduler_FetchDueMonitors_ExcludesPassiveTypes(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")
	active := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "active")
	passive := map[uuid.UUID]string{}
	for _, typ := range models.PassiveMonitorTypes {
		id := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, typ)
		if _, err := dbClient.ExecContext(ctx,
			`UPDATE monitors SET type = $2, config = '{}' WHERE id = $1`, id, typ,
		); err != nil {
			t.Fatalf("set type %s: %v", typ, err)
		}
		passive[id] = typ
	}

	s := &Scheduler{db: dbClient}
	tx, err := dbClient.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()

	monitors, err := s.fetchDueMonitors(ctx, tx, 10)
	if err != nil {
		t.Fatalf("fetchDueMonitors: %v", err)
	}

	sawActive := false
	for _, m := range monitors {
		if m.ID == active {
			sawActive = true
		}
		if typ, ok := passive[m.ID]; ok {
			t.Errorf("passive %s monitor must NOT appear in the dispatch batch", typ)
		}
	}
	if !sawActive {
		t.Error("expected the active monitor in batch; not found")
	}
}
