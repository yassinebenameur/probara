package alerter

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func insertDependencyForTest(ctx context.Context, t *testing.T, db *shareddb.Client, monitorID, dependsOnID uuid.UUID) {
	t.Helper()
	mustExec(ctx, t, db, `
		INSERT INTO monitor_dependencies (monitor_id, depends_on_id) VALUES ($1, $2)
	`, monitorID, dependsOnID)
}

func loadAlertRootCause(ctx context.Context, t *testing.T, db *shareddb.Client, monitorID uuid.UUID) (uuid.NullUUID, sql.NullTime) {
	t.Helper()
	var rcID uuid.NullUUID
	var rcDownSince sql.NullTime
	if err := db.QueryRowContext(ctx, `
		SELECT root_cause_monitor_id, root_cause_down_since
		FROM alerts
		WHERE monitor_id = $1 AND status IN ('active', 'acknowledged')
	`, monitorID).Scan(&rcID, &rcDownSince); err != nil {
		t.Fatalf("load alert root cause: %v", err)
	}
	return rcID, rcDownSince
}

func TestLifecycleAnnotatesRootCauseAtCreation(t *testing.T) {
	// Common case: the upstream (DB) dies first, the downstream (API) follows.
	// The downstream alert must carry the annotation from its very first
	// dispatch, and the upstream's own alert must stay un-annotated.
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "rc")
	upstreamID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "Postgres prod")
	downstreamID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "Backend API")
	insertDependencyForTest(ctx, t, dbClient, downstreamID, upstreamID)

	channelID := insertTestChannel(ctx, t, dbClient, tenantID, "hook")
	mustExec(ctx, t, dbClient,
		`INSERT INTO tenant_default_channels (tenant_id, channel_id) VALUES ($1, $2)`, tenantID, channelID)

	a := newIntegrationAlerter(dbClient)
	dispatched := map[uuid.UUID]alertRecord{}
	a.sendFunc = func(_ context.Context, _ alertChannel, _ string, _ policyBinding, alert *alertRecord, _ *groupDetail, _ time.Time) error {
		dispatched[alert.MonitorID] = *alert
		return nil
	}

	downSince := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Millisecond)
	setStateForTest(ctx, t, dbClient, upstreamID, "down", downSince)
	setStateForTest(ctx, t, dbClient, downstreamID, "down", time.Now().UTC())
	if err := a.runLifecycle(ctx); err != nil {
		t.Fatalf("runLifecycle error = %v", err)
	}

	rcID, rcDownSince := loadAlertRootCause(ctx, t, dbClient, downstreamID)
	if !rcID.Valid || rcID.UUID != upstreamID {
		t.Fatalf("downstream root_cause_monitor_id = %v, want %s", rcID, upstreamID)
	}
	if !rcDownSince.Valid || !rcDownSince.Time.UTC().Truncate(time.Millisecond).Equal(downSince) {
		t.Fatalf("downstream root_cause_down_since = %v, want %s", rcDownSince, downSince)
	}

	upRcID, _ := loadAlertRootCause(ctx, t, dbClient, upstreamID)
	if upRcID.Valid {
		t.Fatalf("upstream alert must not have a root cause, got %s", upRcID.UUID)
	}

	record, ok := dispatched[downstreamID]
	if !ok {
		t.Fatalf("downstream alert was not dispatched")
	}
	if record.RootCauseMonitorID == nil || *record.RootCauseMonitorID != upstreamID {
		t.Fatalf("dispatched record root cause = %v, want %s", record.RootCauseMonitorID, upstreamID)
	}
	if record.RootCauseMonitorName == nil || *record.RootCauseMonitorName != "Postgres prod" {
		t.Fatalf("dispatched record root cause name = %v, want Postgres prod", record.RootCauseMonitorName)
	}
}

func TestLifecycleAnnotatesRootCauseDetectedLater(t *testing.T) {
	// Out-of-order detection: the downstream alert opens before the upstream
	// flips down. The next tick must back-fill the annotation.
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "rc")
	upstreamID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "Redis prod")
	downstreamID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "Backend API")
	insertDependencyForTest(ctx, t, dbClient, downstreamID, upstreamID)
	a := newIntegrationAlerter(dbClient)

	setStateForTest(ctx, t, dbClient, downstreamID, "down", time.Now().UTC())
	if err := a.runLifecycle(ctx); err != nil {
		t.Fatalf("runLifecycle(first) error = %v", err)
	}
	rcID, _ := loadAlertRootCause(ctx, t, dbClient, downstreamID)
	if rcID.Valid {
		t.Fatalf("root cause set before upstream went down: %s", rcID.UUID)
	}

	setStateForTest(ctx, t, dbClient, upstreamID, "down", time.Now().UTC())
	if err := a.runLifecycle(ctx); err != nil {
		t.Fatalf("runLifecycle(second) error = %v", err)
	}
	rcID, _ = loadAlertRootCause(ctx, t, dbClient, downstreamID)
	if !rcID.Valid || rcID.UUID != upstreamID {
		t.Fatalf("root cause after upstream down = %v, want %s", rcID, upstreamID)
	}
}

func TestLifecycleClearsRootCauseWhenUpstreamRecovers(t *testing.T) {
	// A stale "likely caused by" claim must not survive the upstream's
	// recovery while the downstream stays down.
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "rc")
	upstreamID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "Postgres prod")
	downstreamID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "Backend API")
	insertDependencyForTest(ctx, t, dbClient, downstreamID, upstreamID)
	a := newIntegrationAlerter(dbClient)

	setStateForTest(ctx, t, dbClient, upstreamID, "down", time.Now().UTC())
	setStateForTest(ctx, t, dbClient, downstreamID, "down", time.Now().UTC())
	if err := a.runLifecycle(ctx); err != nil {
		t.Fatalf("runLifecycle(both down) error = %v", err)
	}
	rcID, _ := loadAlertRootCause(ctx, t, dbClient, downstreamID)
	if !rcID.Valid {
		t.Fatalf("root cause not set while upstream down")
	}

	setStateForTest(ctx, t, dbClient, upstreamID, "up", time.Now().UTC())
	if err := a.runLifecycle(ctx); err != nil {
		t.Fatalf("runLifecycle(upstream recovered) error = %v", err)
	}
	rcID, rcDownSince := loadAlertRootCause(ctx, t, dbClient, downstreamID)
	if rcID.Valid || rcDownSince.Valid {
		t.Fatalf("root cause not cleared after upstream recovery: id=%v down_since=%v", rcID, rcDownSince)
	}
}

func TestLifecycleTransitiveRootCausePointsAtDeepestUpstream(t *testing.T) {
	// Chain C -> B -> A with everything down: C's alert must blame A (the
	// deepest down upstream), not its direct dependency B.
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "rc")
	aID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "Postgres")
	bID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "Auth service")
	cID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "Frontend API")
	insertDependencyForTest(ctx, t, dbClient, bID, aID)
	insertDependencyForTest(ctx, t, dbClient, cID, bID)
	alerter := newIntegrationAlerter(dbClient)

	now := time.Now().UTC()
	setStateForTest(ctx, t, dbClient, aID, "down", now.Add(-3*time.Minute))
	setStateForTest(ctx, t, dbClient, bID, "down", now.Add(-2*time.Minute))
	setStateForTest(ctx, t, dbClient, cID, "down", now)
	if err := alerter.runLifecycle(ctx); err != nil {
		t.Fatalf("runLifecycle error = %v", err)
	}

	rcID, _ := loadAlertRootCause(ctx, t, dbClient, cID)
	if !rcID.Valid || rcID.UUID != aID {
		t.Fatalf("C's root cause = %v, want deepest upstream %s", rcID, aID)
	}
	rcID, _ = loadAlertRootCause(ctx, t, dbClient, bID)
	if !rcID.Valid || rcID.UUID != aID {
		t.Fatalf("B's root cause = %v, want %s", rcID, aID)
	}
}

func TestLifecycleIgnoresDisabledUpstream(t *testing.T) {
	// A paused upstream has stale state — it must never be blamed.
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "rc")
	upstreamID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "Postgres prod")
	downstreamID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "Backend API")
	insertDependencyForTest(ctx, t, dbClient, downstreamID, upstreamID)
	a := newIntegrationAlerter(dbClient)

	setStateForTest(ctx, t, dbClient, upstreamID, "down", time.Now().UTC())
	mustExec(ctx, t, dbClient, `UPDATE monitors SET enabled = FALSE WHERE id = $1`, upstreamID)
	setStateForTest(ctx, t, dbClient, downstreamID, "down", time.Now().UTC())
	if err := a.runLifecycle(ctx); err != nil {
		t.Fatalf("runLifecycle error = %v", err)
	}

	rcID, _ := loadAlertRootCause(ctx, t, dbClient, downstreamID)
	if rcID.Valid {
		t.Fatalf("disabled upstream blamed as root cause: %s", rcID.UUID)
	}
}
