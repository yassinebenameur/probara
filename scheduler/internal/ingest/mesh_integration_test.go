package ingest

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/locationauth"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/testutil"
)

var meshTestCredential = base64.RawURLEncoding.EncodeToString(make([]byte, 32))

// newMeshTestIngest builds an Ingest with an explicit mesh failure threshold
// (the knob under test), mirroring newTestIngest otherwise.
func newMeshTestIngest(t testing.TB, dbClient *db.Client) *Ingest {
	t.Helper()
	cfg := &config.SchedulerConfig{
		CheckResultStream:        models.CheckResultStream,
		CheckResultSubject:       models.CheckResultSubject,
		ResultIngestConsumerName: "result-ingest-test",
		ResultIngestConcurrency:  1,
		MeshFailureThreshold:     3,
	}
	return New(cfg, logger.New("mesh-ingest-test", "error"), metrics.NewRegistry("mesh_ingest_test"), dbClient, nil, nil)
}

func insertMeshEdge(ctx context.Context, t testing.TB, dbClient *db.Client, tenantID, sourceID, targetID uuid.UUID) {
	t.Helper()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO location_mesh_state (tenant_id, source_location_id, target_location_id)
		VALUES ($1, $2, $3)
	`, tenantID, sourceID, targetID); err != nil {
		t.Fatalf("insert mesh edge: %v", err)
	}
}

func authorizeMeshSource(ctx context.Context, t testing.TB, dbClient *db.Client, sourceID uuid.UUID) {
	t.Helper()
	if _, err := dbClient.ExecContext(ctx, `UPDATE locations SET worker_credential = $1 WHERE id = $2`, meshTestCredential, sourceID); err != nil {
		t.Fatalf("set mesh source credential: %v", err)
	}
}

// meshMessage builds one mesh probe result: LocationID is the probing source,
// Mesh carries the target. Every call gets a fresh JobID.
func meshMessage(tenantID, sourceID, targetID uuid.UUID, status string) models.CheckResultMessage {
	now := time.Now()
	latency := int64(42)
	m := models.CheckResultMessage{
		Version:      "v1",
		JobID:        uuid.New().String(),
		TenantID:     tenantID.String(),
		LocationID:   sourceID.String(),
		Status:       status,
		ResultSource: string(models.ResultSourceMonitor),
		LatencyMs:    &latency,
		StartedAt:    now,
		CompletedAt:  now,
		Mesh:         &models.MeshResultInfo{TargetLocationID: targetID.String()},
	}
	if status != "success" {
		errMsg := "mesh probe failed: connection refused"
		m.ErrorMessage = &errMsg
	}
	return m
}

// ingestMeshMessage routes the message through handleMessage, the same entry
// point the NATS consumer uses (exercising the Mesh != nil routing).
func ingestMeshMessage(t *testing.T, i *Ingest, m models.CheckResultMessage) {
	t.Helper()
	if err := i.handleMessage(context.Background(), signedMeshQueueMessage(t, m)); err != nil {
		t.Fatalf("handleMessage %s: %v", m.Status, err)
	}
}

func signedMeshQueueMessage(t testing.TB, m models.CheckResultMessage) *queue.Message {
	t.Helper()
	m.LocationSignature = ""
	signature, err := locationauth.SignJSON(meshTestCredential, m)
	if err != nil {
		t.Fatalf("sign mesh message: %v", err)
	}
	m.LocationSignature = signature
	msg := testMessage(mustMarshal(t, m))
	msg.Subject = models.CheckResultSubjectForLocation(models.CheckResultSubject, m.LocationID)
	return msg
}

func meshEdgeState(ctx context.Context, t testing.TB, dbClient *db.Client, sourceID, targetID uuid.UUID) (string, int) {
	t.Helper()
	var state string
	var fails int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT current_state, consecutive_failures
		FROM location_mesh_state
		WHERE source_location_id = $1 AND target_location_id = $2
	`, sourceID, targetID).Scan(&state, &fails); err != nil {
		t.Fatalf("query mesh edge state: %v", err)
	}
	return state, fails
}

func countMeshResults(ctx context.Context, t testing.TB, dbClient *db.Client, sourceID, targetID uuid.UUID) int {
	t.Helper()
	var count int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM mesh_probe_results
		WHERE source_location_id = $1 AND target_location_id = $2
	`, sourceID, targetID).Scan(&count); err != nil {
		t.Fatalf("count mesh probe results: %v", err)
	}
	return count
}

func TestMeshIngestStateProgression(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "mesh-ingest")
	source := insertLocation(ctx, t, dbClient, tenantID, "mesh-src")
	authorizeMeshSource(ctx, t, dbClient, source)
	target := insertLocation(ctx, t, dbClient, tenantID, "mesh-tgt")
	insertMeshEdge(ctx, t, dbClient, tenantID, source, target)

	i := newMeshTestIngest(t, dbClient) // MeshFailureThreshold = 3

	assertEdge := func(wantState string, wantFails int) {
		t.Helper()
		state, fails := meshEdgeState(ctx, t, dbClient, source, target)
		if state != wantState || fails != wantFails {
			t.Fatalf("edge = %s/%d, want %s/%d", state, fails, wantState, wantFails)
		}
	}

	ingestMeshMessage(t, i, meshMessage(tenantID, source, target, "failure")) // 1st failure
	assertEdge("suspect", 1)
	ingestMeshMessage(t, i, meshMessage(tenantID, source, target, "failure")) // 2nd: below threshold
	assertEdge("suspect", 2)
	ingestMeshMessage(t, i, meshMessage(tenantID, source, target, "failure")) // 3rd: threshold reached
	assertEdge("down", 3)
	ingestMeshMessage(t, i, meshMessage(tenantID, source, target, "success")) // recovery
	assertEdge("up", 0)

	var lastCheckAt, lastStateChangeAt *time.Time
	if err := dbClient.QueryRowContext(ctx, `
		SELECT last_check_at, last_state_change_at FROM location_mesh_state
		WHERE source_location_id = $1 AND target_location_id = $2
	`, source, target).Scan(&lastCheckAt, &lastStateChangeAt); err != nil {
		t.Fatalf("query edge timestamps: %v", err)
	}
	if lastCheckAt == nil {
		t.Error("last_check_at should be set after ingesting results")
	}
	if lastStateChangeAt == nil {
		t.Error("last_state_change_at should be set after state transitions")
	}

	if got := countMeshResults(ctx, t, dbClient, source, target); got != 4 {
		t.Fatalf("mesh_probe_results rows = %d, want 4 (one per probe)", got)
	}
}

func TestMeshIngestRedeliveryIsNoOp(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "mesh-dedupe")
	source := insertLocation(ctx, t, dbClient, tenantID, "mesh-src")
	authorizeMeshSource(ctx, t, dbClient, source)
	target := insertLocation(ctx, t, dbClient, tenantID, "mesh-tgt")
	insertMeshEdge(ctx, t, dbClient, tenantID, source, target)

	i := newMeshTestIngest(t, dbClient)

	// The same message (same JobID) delivered twice: one sample row, one
	// state-machine step.
	m := meshMessage(tenantID, source, target, "failure")
	for n := 0; n < 2; n++ {
		if err := i.handleMessage(ctx, signedMeshQueueMessage(t, m)); err != nil {
			t.Fatalf("handleMessage delivery %d: %v", n+1, err)
		}
	}

	state, fails := meshEdgeState(ctx, t, dbClient, source, target)
	if state != "suspect" || fails != 1 {
		t.Fatalf("edge = %s/%d, want suspect/1 (redelivery double-applied)", state, fails)
	}
	if got := countMeshResults(ctx, t, dbClient, source, target); got != 1 {
		t.Fatalf("mesh_probe_results rows = %d, want 1", got)
	}
}

func TestMeshIngestDropsResultForMissingEdge(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "mesh-gone")
	source := insertLocation(ctx, t, dbClient, tenantID, "mesh-src")
	authorizeMeshSource(ctx, t, dbClient, source)
	target := insertLocation(ctx, t, dbClient, tenantID, "mesh-tgt")
	// No location_mesh_state row: the edge was removed while the probe was in
	// flight. The result must be acked (nil) and insert nothing.

	i := newMeshTestIngest(t, dbClient)

	m := meshMessage(tenantID, source, target, "failure")
	if err := i.handleMessage(ctx, signedMeshQueueMessage(t, m)); err != nil {
		t.Fatalf("handleMessage should drop results for missing edges, got: %v", err)
	}

	if got := countMeshResults(ctx, t, dbClient, source, target); got != 0 {
		t.Fatalf("mesh_probe_results rows = %d, want 0 (dropped result must not persist)", got)
	}
}

func TestMeshIngestPersistsSample(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "mesh-sample")
	source := insertLocation(ctx, t, dbClient, tenantID, "mesh-src")
	authorizeMeshSource(ctx, t, dbClient, source)
	target := insertLocation(ctx, t, dbClient, tenantID, "mesh-tgt")
	insertMeshEdge(ctx, t, dbClient, tenantID, source, target)

	i := newMeshTestIngest(t, dbClient)

	m := meshMessage(tenantID, source, target, "failure")
	ingestMeshMessage(t, i, m)

	var (
		gotTenant  uuid.UUID
		status     string
		latencyMs  *int64
		errMessage *string
	)
	if err := dbClient.QueryRowContext(ctx, `
		SELECT tenant_id, status, latency_ms, error_message
		FROM mesh_probe_results
		WHERE job_id = $1 AND source_location_id = $2 AND target_location_id = $3
	`, m.JobID, source, target).Scan(&gotTenant, &status, &latencyMs, &errMessage); err != nil {
		t.Fatalf("query sample row: %v", err)
	}
	if gotTenant != tenantID {
		t.Errorf("sample tenant = %s, want %s", gotTenant, tenantID)
	}
	if status != "failure" {
		t.Errorf("sample status = %q, want failure", status)
	}
	if latencyMs == nil || *latencyMs != 42 {
		t.Errorf("sample latency = %v, want 42", latencyMs)
	}
	if errMessage == nil || *errMessage != *m.ErrorMessage {
		t.Errorf("sample error = %v, want %q", errMessage, *m.ErrorMessage)
	}

	// The edge row mirrors the last sample.
	var lastLatency *int64
	var lastError *string
	if err := dbClient.QueryRowContext(ctx, `
		SELECT last_latency_ms, last_error FROM location_mesh_state
		WHERE source_location_id = $1 AND target_location_id = $2
	`, source, target).Scan(&lastLatency, &lastError); err != nil {
		t.Fatalf("query edge row: %v", err)
	}
	if lastLatency == nil || *lastLatency != 42 {
		t.Errorf("edge last_latency_ms = %v, want 42", lastLatency)
	}
	if lastError == nil || *lastError != *m.ErrorMessage {
		t.Errorf("edge last_error = %v, want %q", lastError, *m.ErrorMessage)
	}
}
