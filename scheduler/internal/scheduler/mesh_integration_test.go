package scheduler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// publishedJob is one captured publish: the subject and the job sent to it.
type publishedJob struct {
	subject string
	job     *models.Job
}

// spyJobPublisher plugs into the Scheduler's publish seam and records every
// job instead of talking to NATS.
type spyJobPublisher struct {
	published []publishedJob
}

func (p *spyJobPublisher) publish(_ context.Context, subject string, job *models.Job) error {
	p.published = append(p.published, publishedJob{subject: subject, job: job})
	return nil
}

func newMeshTestScheduler(t testing.TB, dbClient *db.Client) (*Scheduler, *spyJobPublisher) {
	t.Helper()
	cfg := &config.SchedulerConfig{
		CheckJobSubject:          "check.jobs",
		MeshScheduleBatchSize:    100,
		MeshProbeIntervalSeconds: 30,
		MeshProbeTimeoutSeconds:  5,
	}
	s := NewScheduler(cfg, logger.New("mesh-sched-test", "error"), metrics.NewRegistry("mesh_sched_test"), dbClient, nil)
	spy := &spyJobPublisher{}
	s.publish = spy.publish
	return s, spy
}

// insertMeshLocation inserts a location; meshEndpoint == "" means the location
// does not participate in the mesh.
func insertMeshLocation(ctx context.Context, t testing.TB, dbClient *db.Client, tenantID uuid.UUID, name, meshEndpoint string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var endpoint interface{}
	if meshEndpoint != "" {
		endpoint = meshEndpoint
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug, mesh_endpoint)
		VALUES ($1, $2, $3, $4, $5)
	`, id, tenantID, name, name, endpoint); err != nil {
		t.Fatalf("insert location %s: %v", name, err)
	}
	return id
}

// meshEdges returns the current directed edge set as "source->target" keys.
func meshEdges(ctx context.Context, t testing.TB, dbClient *db.Client) map[string]bool {
	t.Helper()
	rows, err := dbClient.QueryContext(ctx,
		`SELECT source_location_id, target_location_id FROM location_mesh_state`)
	if err != nil {
		t.Fatalf("query mesh edges: %v", err)
	}
	defer rows.Close()

	edges := make(map[string]bool)
	for rows.Next() {
		var source, target uuid.UUID
		if err := rows.Scan(&source, &target); err != nil {
			t.Fatalf("scan mesh edge: %v", err)
		}
		edges[source.String()+"->"+target.String()] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate mesh edges: %v", err)
	}
	return edges
}

func edgeKey(source, target uuid.UUID) string {
	return source.String() + "->" + target.String()
}

func TestMeshSyncEdgeLifecycle(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "mesh-sync")
	locA := insertMeshLocation(ctx, t, dbClient, tenantID, "mesh-a", "10.0.0.1:8080")
	locB := insertMeshLocation(ctx, t, dbClient, tenantID, "mesh-b", "10.0.0.2:8080")
	locC := insertMeshLocation(ctx, t, dbClient, tenantID, "no-mesh-c", "") // no endpoint: never in the mesh

	s, _ := newMeshTestScheduler(t, dbClient)

	assertEdges := func(step string, want ...string) {
		t.Helper()
		edges := meshEdges(ctx, t, dbClient)
		if len(edges) != len(want) {
			t.Fatalf("%s: edges = %v, want exactly %v", step, edges, want)
		}
		for _, key := range want {
			if !edges[key] {
				t.Fatalf("%s: missing edge %s in %v", step, key, edges)
			}
		}
		for key := range edges {
			if key == edgeKey(locC, locA) || key == edgeKey(locC, locB) ||
				key == edgeKey(locA, locC) || key == edgeKey(locB, locC) {
				t.Fatalf("%s: edge %s touches the endpointless location", step, key)
			}
		}
	}

	// Two mesh locations -> exactly the two directed edges, none touching C.
	s.runMeshBatch(ctx)
	assertEdges("initial sync", edgeKey(locA, locB), edgeKey(locB, locA))

	// Clearing an endpoint deletes every edge whose source or target is B.
	if _, err := dbClient.ExecContext(ctx,
		`UPDATE locations SET mesh_endpoint = NULL WHERE id = $1`, locB); err != nil {
		t.Fatalf("clear endpoint: %v", err)
	}
	s.runMeshBatch(ctx)
	assertEdges("after endpoint cleared")

	// Restoring the endpoint recreates the pair.
	if _, err := dbClient.ExecContext(ctx,
		`UPDATE locations SET mesh_endpoint = '10.0.0.2:8080' WHERE id = $1`, locB); err != nil {
		t.Fatalf("restore endpoint: %v", err)
	}
	s.runMeshBatch(ctx)
	assertEdges("after endpoint restored", edgeKey(locA, locB), edgeKey(locB, locA))

	// Disabling a location removes its edges.
	if _, err := dbClient.ExecContext(ctx,
		`UPDATE locations SET enabled = FALSE WHERE id = $1`, locB); err != nil {
		t.Fatalf("disable location: %v", err)
	}
	s.runMeshBatch(ctx)
	assertEdges("after disable")

	if _, err := dbClient.ExecContext(ctx,
		`UPDATE locations SET enabled = TRUE WHERE id = $1`, locB); err != nil {
		t.Fatalf("re-enable location: %v", err)
	}
	s.runMeshBatch(ctx)
	assertEdges("after re-enable", edgeKey(locA, locB), edgeKey(locB, locA))

	// Soft-deleting a location removes its edges too.
	if _, err := dbClient.ExecContext(ctx,
		`UPDATE locations SET deleted_at = NOW() WHERE id = $1`, locB); err != nil {
		t.Fatalf("soft delete location: %v", err)
	}
	s.runMeshBatch(ctx)
	assertEdges("after soft delete")
}

func TestMeshPublishesDueEdgesAndReschedules(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "mesh-publish")
	endpointA := "10.0.0.1:8080"
	endpointB := "10.0.0.2:8080"
	locA := insertMeshLocation(ctx, t, dbClient, tenantID, "mesh-a", endpointA)
	locB := insertMeshLocation(ctx, t, dbClient, tenantID, "mesh-b", endpointB)

	s, spy := newMeshTestScheduler(t, dbClient)

	// First run: sync creates both edges due immediately -> one job per edge.
	s.runMeshBatch(ctx)
	if len(spy.published) != 2 {
		t.Fatalf("published jobs = %d, want 2 (%+v)", len(spy.published), spy.published)
	}

	assertMeshJob := func(p publishedJob, source, target uuid.UUID, targetEndpoint string) {
		t.Helper()
		wantSubject := "check.jobs.loc." + source.String()
		if p.subject != wantSubject {
			t.Fatalf("subject = %q, want %q", p.subject, wantSubject)
		}
		if p.job.TenantID != tenantID.String() {
			t.Fatalf("job tenant = %q, want %q", p.job.TenantID, tenantID)
		}
		if p.job.Type != models.JobTypeCheck {
			t.Fatalf("job type = %q, want %q", p.job.Type, models.JobTypeCheck)
		}
		if p.job.Deadline == nil {
			t.Fatal("job deadline should be set")
		}

		var payload models.CheckJobPayload
		if err := json.Unmarshal(p.job.Payload, &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if payload.Type != models.MonitorTypeMeshProbe {
			t.Fatalf("payload type = %q, want %q", payload.Type, models.MonitorTypeMeshProbe)
		}
		if payload.MonitorID != "" {
			t.Fatalf("payload monitor_id = %q, want empty (mesh jobs carry no monitor)", payload.MonitorID)
		}
		if payload.LocationID != source.String() {
			t.Fatalf("payload location_id = %q, want source %q", payload.LocationID, source)
		}
		var probeCfg models.MeshProbeConfig
		if err := json.Unmarshal(payload.Config, &probeCfg); err != nil {
			t.Fatalf("unmarshal mesh probe config: %v", err)
		}
		if probeCfg.TargetLocationID != target.String() {
			t.Fatalf("config target = %q, want %q", probeCfg.TargetLocationID, target)
		}
		if probeCfg.Endpoint != targetEndpoint {
			t.Fatalf("config endpoint = %q, want %q", probeCfg.Endpoint, targetEndpoint)
		}
	}

	bySubject := make(map[string]publishedJob, len(spy.published))
	for _, p := range spy.published {
		bySubject[p.subject] = p
	}
	fromA, ok := bySubject["check.jobs.loc."+locA.String()]
	if !ok {
		t.Fatalf("no job published to A's subject (%+v)", bySubject)
	}
	assertMeshJob(fromA, locA, locB, endpointB)
	fromB, ok := bySubject["check.jobs.loc."+locB.String()]
	if !ok {
		t.Fatalf("no job published to B's subject (%+v)", bySubject)
	}
	assertMeshJob(fromB, locB, locA, endpointA)

	// Both edges rescheduled into the future.
	var stillDue int
	if err := dbClient.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM location_mesh_state WHERE next_run_at <= NOW()`).Scan(&stillDue); err != nil {
		t.Fatalf("count due edges: %v", err)
	}
	if stillDue != 0 {
		t.Fatalf("due edges after batch = %d, want 0 (next_run_at must advance)", stillDue)
	}

	// Not-due edges are not republished.
	s.runMeshBatch(ctx)
	if len(spy.published) != 2 {
		t.Fatalf("published jobs after no-op batch = %d, want 2 (not-due edges must not publish)", len(spy.published))
	}

	// Make only A->B due again: exactly one more publish, on A's subject.
	if _, err := dbClient.ExecContext(ctx, `
		UPDATE location_mesh_state SET next_run_at = NOW() - INTERVAL '1 second'
		WHERE source_location_id = $1 AND target_location_id = $2
	`, locA, locB); err != nil {
		t.Fatalf("make edge due: %v", err)
	}
	s.runMeshBatch(ctx)
	if len(spy.published) != 3 {
		t.Fatalf("published jobs = %d, want 3 (only the due edge republishes)", len(spy.published))
	}
	assertMeshJob(spy.published[2], locA, locB, endpointB)
}

func insertMeshResult(ctx context.Context, t testing.TB, dbClient *db.Client, tenantID, sourceID, targetID uuid.UUID, createdAt time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO mesh_probe_results (
			id, tenant_id, source_location_id, target_location_id,
			job_id, status, latency_ms, error_message, created_at
		) VALUES ($1, $2, $3, $4, $5, 'success', 12, NULL, $6)
	`, id, tenantID, sourceID, targetID, uuid.New(), createdAt.UTC()); err != nil {
		t.Fatalf("insert mesh probe result: %v", err)
	}
	return id
}

func TestMeshPruneTenantMeshResults(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "mesh-prune")
	locA := insertMeshLocation(ctx, t, dbClient, tenantID, "mesh-a", "10.0.0.1:8080")
	locB := insertMeshLocation(ctx, t, dbClient, tenantID, "mesh-b", "10.0.0.2:8080")

	otherTenantID := testutil.InsertTenant(ctx, t, dbClient, "mesh-prune-other")
	otherA := insertMeshLocation(ctx, t, dbClient, otherTenantID, "mesh-a", "10.1.0.1:8080")
	otherB := insertMeshLocation(ctx, t, dbClient, otherTenantID, "mesh-b", "10.1.0.2:8080")

	now := time.Now().UTC()
	staleID := insertMeshResult(ctx, t, dbClient, tenantID, locA, locB, now.AddDate(0, 0, -10))
	freshID := insertMeshResult(ctx, t, dbClient, tenantID, locA, locB, now.Add(-time.Hour))
	// Another tenant's stale row: out of scope for this tenant's prune.
	otherStaleID := insertMeshResult(ctx, t, dbClient, otherTenantID, otherA, otherB, now.AddDate(0, 0, -10))

	s, _ := newMeshTestScheduler(t, dbClient)

	deleted, err := s.pruneTenantMeshResults(ctx, tenantID, 7)
	if err != nil {
		t.Fatalf("pruneTenantMeshResults: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}

	remaining := make(map[uuid.UUID]bool)
	rows, err := dbClient.QueryContext(ctx, `SELECT id FROM mesh_probe_results`)
	if err != nil {
		t.Fatalf("query remaining results: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan result id: %v", err)
		}
		remaining[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate results: %v", err)
	}

	if remaining[staleID] {
		t.Error("stale row survived prune")
	}
	if !remaining[freshID] {
		t.Error("fresh row was pruned")
	}
	if !remaining[otherStaleID] {
		t.Error("other tenant's row was pruned")
	}

	// Retention disabled (<= 0) prunes nothing.
	deleted, err = s.pruneTenantMeshResults(ctx, tenantID, 0)
	if err != nil {
		t.Fatalf("pruneTenantMeshResults(0): %v", err)
	}
	if deleted != 0 {
		t.Fatalf("deleted with retention 0 = %d, want 0", deleted)
	}
}
