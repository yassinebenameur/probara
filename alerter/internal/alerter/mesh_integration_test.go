package alerter

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/config"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// meshSentEvent captures one sendFunc invocation for assertions.
type meshSentEvent struct {
	eventType   string
	channelID   uuid.UUID
	monitorName string
}

// meshSendSpy is a thread-safe recording replacement for Alerter.sendFunc.
type meshSendSpy struct {
	mu     sync.Mutex
	events []meshSentEvent
}

func (s *meshSendSpy) record(eventType string, channelID uuid.UUID, monitorName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, meshSentEvent{eventType: eventType, channelID: channelID, monitorName: monitorName})
}

func (s *meshSendSpy) byType(eventType string) []meshSentEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []meshSentEvent
	for _, e := range s.events {
		if e.eventType == eventType {
			out = append(out, e)
		}
	}
	return out
}

func newMeshAlerter(dbClient *shareddb.Client) (*Alerter, *meshSendSpy) {
	spy := &meshSendSpy{}
	a := &Alerter{
		config: &config.AlerterConfig{},
		logger: logger.New("alerter-test", "error"),
		db:     dbClient,
	}
	a.sendFunc = func(_ context.Context, channel alertChannel, eventType string, binding policyBinding, _ *alertRecord, _ *groupDetail, _ time.Time) error {
		spy.record(eventType, channel.ID, binding.MonitorName)
		return nil
	}
	return a, spy
}

// insertMeshLocation seeds a mesh-participating location with raw SQL.
func insertMeshLocation(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID uuid.UUID, name, slug string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, mesh_endpoint, enabled, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, TRUE, NOW(), NOW())
	`, id, tenantID, name, slug, slug+".mesh.internal:8080"); err != nil {
		t.Fatalf("insert location %s: %v", name, err)
	}
	return id
}

// insertMeshEdgeState seeds one directed location_mesh_state row.
func insertMeshEdgeState(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID, sourceID, targetID uuid.UUID, state string, failures int, lastError string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO location_mesh_state (
			tenant_id, source_location_id, target_location_id, current_state,
			consecutive_failures, last_error, last_check_at, last_state_change_at, next_run_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), NOW(), NOW(), NOW(), NOW())
	`, tenantID, sourceID, targetID, state, failures, lastError); err != nil {
		t.Fatalf("insert mesh edge state: %v", err)
	}
}

func setMeshEdgeState(ctx context.Context, t *testing.T, db *shareddb.Client, sourceID, targetID uuid.UUID, state string) {
	t.Helper()
	mustExec(ctx, t, db, `
		UPDATE location_mesh_state SET current_state = $3, updated_at = NOW()
		WHERE source_location_id = $1 AND target_location_id = $2
	`, sourceID, targetID, state)
}

func countMeshEdgeAlerts(ctx context.Context, t *testing.T, db *shareddb.Client, sourceID, targetID uuid.UUID, status string) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM alerts
		WHERE kind = 'mesh_edge' AND source_location_id = $1 AND target_location_id = $2 AND status = $3
	`, sourceID, targetID, status).Scan(&count); err != nil {
		t.Fatalf("count mesh edge alerts: %v", err)
	}
	return count
}

func TestMeshEdgeAlertOpensOnceForDownEdge(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "mesh")
	sourceID := insertMeshLocation(ctx, t, dbClient, tenantID, "Paris", "paris")
	targetID := insertMeshLocation(ctx, t, dbClient, tenantID, "Frankfurt", "frankfurt")
	insertMeshEdgeState(ctx, t, dbClient, tenantID, sourceID, targetID, "down", 3, "dial tcp: i/o timeout")

	a, _ := newMeshAlerter(dbClient)
	if err := a.evaluateMeshEdges(ctx); err != nil {
		t.Fatalf("evaluateMeshEdges(open) error = %v", err)
	}
	if got := countMeshEdgeAlerts(ctx, t, dbClient, sourceID, targetID, "active"); got != 1 {
		t.Fatalf("active mesh alerts = %d, want 1", got)
	}

	// The alert row must carry the directed edge as its subject, no monitor.
	var monitorID *uuid.UUID
	var gotSource, gotTarget uuid.UUID
	var failureCount int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT monitor_id, source_location_id, target_location_id, failure_count FROM alerts
		WHERE kind = 'mesh_edge' AND status = 'active'
	`).Scan(&monitorID, &gotSource, &gotTarget, &failureCount); err != nil {
		t.Fatalf("read mesh alert: %v", err)
	}
	if monitorID != nil {
		t.Errorf("monitor_id = %v, want NULL", *monitorID)
	}
	if gotSource != sourceID {
		t.Errorf("source_location_id = %v, want %v", gotSource, sourceID)
	}
	if gotTarget != targetID {
		t.Errorf("target_location_id = %v, want %v", gotTarget, targetID)
	}
	if failureCount != 3 {
		t.Errorf("failure_count = %d, want 3", failureCount)
	}

	// Idempotent: a second evaluation while the edge is still down must not duplicate.
	if err := a.evaluateMeshEdges(ctx); err != nil {
		t.Fatalf("evaluateMeshEdges(idempotent) error = %v", err)
	}
	if got := countMeshEdgeAlerts(ctx, t, dbClient, sourceID, targetID, "active"); got != 1 {
		t.Fatalf("active mesh alerts after second pass = %d, want 1", got)
	}
}

func TestMeshEdgeAlertResolvesOnRecovery(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "mesh")
	sourceID := insertMeshLocation(ctx, t, dbClient, tenantID, "Paris", "paris")
	targetID := insertMeshLocation(ctx, t, dbClient, tenantID, "Frankfurt", "frankfurt")
	insertMeshEdgeState(ctx, t, dbClient, tenantID, sourceID, targetID, "down", 3, "timeout")

	// Tenant default channel with zero delay so 'created' fires on the first tick.
	channelID := insertTestChannel(ctx, t, dbClient, tenantID, "mesh-webhook")
	mustExec(ctx, t, dbClient, `
		INSERT INTO tenant_default_channels (tenant_id, channel_id, delay_seconds, position)
		VALUES ($1, $2, 0, 0)
	`, tenantID, channelID)

	a, spy := newMeshAlerter(dbClient)
	if err := a.evaluateMeshEdges(ctx); err != nil {
		t.Fatalf("evaluateMeshEdges(open) error = %v", err)
	}
	created := spy.byType("created")
	if len(created) != 1 || created[0].channelID != channelID {
		t.Fatalf("created sends = %+v, want exactly one to channel %v", created, channelID)
	}

	// Edge recovers: alert must resolve and the fired channel must be notified.
	setMeshEdgeState(ctx, t, dbClient, sourceID, targetID, "up")
	if err := a.evaluateMeshEdges(ctx); err != nil {
		t.Fatalf("evaluateMeshEdges(recover) error = %v", err)
	}
	if got := countMeshEdgeAlerts(ctx, t, dbClient, sourceID, targetID, "active"); got != 0 {
		t.Fatalf("active mesh alerts after recovery = %d, want 0", got)
	}
	if got := countMeshEdgeAlerts(ctx, t, dbClient, sourceID, targetID, "resolved"); got != 1 {
		t.Fatalf("resolved mesh alerts after recovery = %d, want 1", got)
	}
	resolved := spy.byType("resolved")
	if len(resolved) != 1 || resolved[0].channelID != channelID {
		t.Fatalf("resolved sends = %+v, want exactly one to channel %v", resolved, channelID)
	}
	if want := "mesh: Paris → Frankfurt"; resolved[0].monitorName != want {
		t.Errorf("resolved monitor name = %q, want %q", resolved[0].monitorName, want)
	}
}

func TestMeshEdgeAlertResolvesWhenEdgeVanishes(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "mesh")
	sourceID := insertMeshLocation(ctx, t, dbClient, tenantID, "Paris", "paris")
	targetID := insertMeshLocation(ctx, t, dbClient, tenantID, "Frankfurt", "frankfurt")
	insertMeshEdgeState(ctx, t, dbClient, tenantID, sourceID, targetID, "down", 3, "timeout")

	a, _ := newMeshAlerter(dbClient)
	if err := a.evaluateMeshEdges(ctx); err != nil {
		t.Fatalf("evaluateMeshEdges(open) error = %v", err)
	}
	if got := countMeshEdgeAlerts(ctx, t, dbClient, sourceID, targetID, "active"); got != 1 {
		t.Fatalf("active mesh alerts = %d, want 1", got)
	}

	// The scheduler's edge sync removed the row (endpoint cleared / location
	// disabled). The orphaned alert must resolve, not stay open forever.
	mustExec(ctx, t, dbClient, `
		DELETE FROM location_mesh_state WHERE source_location_id = $1 AND target_location_id = $2
	`, sourceID, targetID)
	if err := a.evaluateMeshEdges(ctx); err != nil {
		t.Fatalf("evaluateMeshEdges(orphan) error = %v", err)
	}
	if got := countMeshEdgeAlerts(ctx, t, dbClient, sourceID, targetID, "active"); got != 0 {
		t.Fatalf("active mesh alerts after edge removal = %d, want 0", got)
	}
	if got := countMeshEdgeAlerts(ctx, t, dbClient, sourceID, targetID, "resolved"); got != 1 {
		t.Fatalf("resolved mesh alerts after edge removal = %d, want 1", got)
	}
}

func TestMeshEdgeDispatchFiresCreatedOnceWithMeshLabel(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "mesh")
	sourceID := insertMeshLocation(ctx, t, dbClient, tenantID, "Paris", "paris")
	targetID := insertMeshLocation(ctx, t, dbClient, tenantID, "Frankfurt", "frankfurt")
	insertMeshEdgeState(ctx, t, dbClient, tenantID, sourceID, targetID, "down", 5, "timeout")

	channelID := insertTestChannel(ctx, t, dbClient, tenantID, "mesh-webhook")
	mustExec(ctx, t, dbClient, `
		INSERT INTO tenant_default_channels (tenant_id, channel_id, delay_seconds, position)
		VALUES ($1, $2, 0, 0)
	`, tenantID, channelID)

	a, spy := newMeshAlerter(dbClient)
	if err := a.evaluateMeshEdges(ctx); err != nil {
		t.Fatalf("evaluateMeshEdges(first tick) error = %v", err)
	}
	// A second dispatch tick while still down must not re-fire 'created'
	// (and the default 3600s reminder interval has not elapsed).
	if err := a.evaluateMeshEdges(ctx); err != nil {
		t.Fatalf("evaluateMeshEdges(second tick) error = %v", err)
	}

	created := spy.byType("created")
	if len(created) != 1 {
		t.Fatalf("created sends = %d, want exactly 1 (got %+v)", len(created), created)
	}
	if created[0].channelID != channelID {
		t.Errorf("created channel = %v, want %v", created[0].channelID, channelID)
	}
	if want := "mesh: Paris → Frankfurt"; created[0].monitorName != want {
		t.Errorf("binding MonitorName = %q, want %q", created[0].monitorName, want)
	}
	if reminders := spy.byType("reminder"); len(reminders) != 0 {
		t.Errorf("reminder sends = %d, want 0 before reminder interval", len(reminders))
	}
}

func TestOpenMeshEdgeAlertRaceNeverDuplicates(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "mesh")
	sourceID := insertMeshLocation(ctx, t, dbClient, tenantID, "Paris", "paris")
	targetID := insertMeshLocation(ctx, t, dbClient, tenantID, "Frankfurt", "frankfurt")
	insertMeshEdgeState(ctx, t, dbClient, tenantID, sourceID, targetID, "down", 3, "timeout")

	a, _ := newMeshAlerter(dbClient)
	edge := meshEdgeSubject{
		tenantID:   tenantID,
		sourceID:   sourceID,
		targetID:   targetID,
		sourceName: "Paris",
		targetName: "Frankfurt",
		failCount:  3,
		lastError:  sql.NullString{String: "timeout", Valid: true},
	}

	// Two replicas racing on the same down edge: the loser's INSERT hits
	// idx_alerts_one_open_mesh_edge via ON CONFLICT DO NOTHING and returns nil.
	const replicas = 4
	errs := make([]error, replicas)
	var wg sync.WaitGroup
	for i := 0; i < replicas; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = a.openMeshEdgeAlert(ctx, edge)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("openMeshEdgeAlert(replica %d) error = %v", i, err)
		}
	}

	// And a sequential retry after the race is equally a no-op.
	if err := a.openMeshEdgeAlert(ctx, edge); err != nil {
		t.Fatalf("openMeshEdgeAlert(sequential retry) error = %v", err)
	}

	if got := countMeshEdgeAlerts(ctx, t, dbClient, sourceID, targetID, "active"); got != 1 {
		t.Fatalf("active mesh alerts after race = %d, want 1", got)
	}
}
