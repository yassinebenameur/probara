package airca

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/testutil"
)

type analysisFixture struct {
	t        *testing.T
	ctx      context.Context
	dbc      *db.Client
	tenant   uuid.UUID
	incident uuid.UUID
}

func newAnalysisFixture(t *testing.T) *analysisFixture {
	t.Helper()
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	f := &analysisFixture{t: t, ctx: ctx, dbc: dbc, tenant: testutil.InsertTenant(ctx, t, dbc, "rca hardening"), incident: uuid.New()}
	f.exec(`INSERT INTO incidents(id,tenant_id,title) VALUES($1,$2,'outage')`, f.incident, f.tenant)
	return f
}

func (f *analysisFixture) exec(q string, args ...any) {
	f.t.Helper()
	if _, err := f.dbc.ExecContext(f.ctx, q, args...); err != nil {
		f.t.Fatal(err)
	}
}

func (f *analysisFixture) pending() ai.AnalysisJob {
	f.t.Helper()
	id := uuid.New()
	f.exec(`INSERT INTO incident_ai_analyses(id,tenant_id,incident_id,status) VALUES($1,$2,$3,'pending')`, id, f.tenant, f.incident)
	return ai.AnalysisJob{V: ai.AnalysisJobVersion, AnalysisID: id, IncidentID: f.incident, TenantID: f.tenant}
}

func (f *analysisFixture) status(id uuid.UUID) (string, string) {
	f.t.Helper()
	var status string
	var message *string
	if err := f.dbc.QueryRowContext(f.ctx, `SELECT status,error_message FROM incident_ai_analyses WHERE id=$1`, id).Scan(&status, &message); err != nil {
		f.t.Fatal(err)
	}
	if message == nil {
		return status, ""
	}
	return status, *message
}

func payload(t *testing.T, job ai.AnalysisJob) []byte {
	t.Helper()
	b, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type slowAnalyzer struct {
	delay  time.Duration
	during func() // runs inside Analyze, before returning
}

func (a *slowAnalyzer) Analyze(ctx context.Context, _ ai.AnalysisInput) (ai.AnalysisResult, error) {
	if a.during != nil {
		a.during()
	}
	select {
	case <-time.After(a.delay):
	case <-ctx.Done():
		return ai.AnalysisResult{}, ctx.Err()
	}
	return ai.AnalysisResult{Summary: "ok", Confidence: "low", Metadata: ai.Metadata{Engine: "laya-cpu", Outcome: "insufficient_evidence"}}, nil
}

func TestConsumerHeartbeatsDuringSlowAnalysis(t *testing.T) {
	f := newAnalysisFixture(t)
	job := f.pending()
	var inAnalyze atomic.Bool
	var duringCalls, outsideCalls atomic.Int32
	a := &slowAnalyzer{delay: 200 * time.Millisecond, during: func() { inAnalyze.Store(true) }}
	c := New(&config.WorkerConfig{LocalAI: ai.Config{Timeout: 10 * time.Second}}, logger.New("test", "error"), f.dbc, nil, a)
	c.heartbeatEvery = 20 * time.Millisecond
	msg := &queue.Message{Data: payload(t, job), NumDelivered: 1, InProgress: func() error {
		if inAnalyze.Load() {
			duringCalls.Add(1)
		} else {
			outsideCalls.Add(1)
		}
		return nil
	}}
	if err := c.handle(f.ctx, msg); err != nil {
		t.Fatal(err)
	}
	inAnalyze.Store(false)
	if n := duringCalls.Load(); n < 3 {
		t.Fatalf("expected InProgress heartbeats during a 200ms analysis at 20ms, got %d", n)
	}
	before := outsideCalls.Load()
	time.Sleep(100 * time.Millisecond)
	if outsideCalls.Load() != before {
		t.Fatal("heartbeat kept running after handle returned")
	}
	if status, _ := f.status(job.AnalysisID); status != "ready" {
		t.Fatalf("status = %s", status)
	}
}

// breakSettings makes resolveAnalyzer fail like a transient database error.
func breakSettings(f *analysisFixture) func() {
	f.exec(`ALTER TABLE ai_settings RENAME TO ai_settings_broken`)
	return func() { f.exec(`ALTER TABLE ai_settings_broken RENAME TO ai_settings`) }
}

func TestConsumerTransientErrorRetriesThenFailsOnFinalDelivery(t *testing.T) {
	f := newAnalysisFixture(t)
	job := f.pending()
	c := New(&config.WorkerConfig{LocalAI: ai.Config{Timeout: 10 * time.Second}}, logger.New("test", "error"), f.dbc, nil, &countingAnalyzer{})
	restore := breakSettings(f)
	defer restore()

	for delivery := uint64(1); delivery < maxDeliver; delivery++ {
		err := c.handle(f.ctx, &queue.Message{Data: payload(t, job), NumDelivered: delivery})
		if err == nil {
			t.Fatalf("delivery %d: transient error must be retried", delivery)
		}
		// RetryAfter keeps the cause reachable for Consume's errors.As.
		if errors.Unwrap(err) == nil {
			t.Fatalf("delivery %d: expected a RetryAfter wrapper, got %T", delivery, err)
		}
		if status, _ := f.status(job.AnalysisID); status != "pending" {
			t.Fatalf("delivery %d left status %s", delivery, status)
		}
	}
	if err := c.handle(f.ctx, &queue.Message{Data: payload(t, job), NumDelivered: maxDeliver}); err != nil {
		t.Fatalf("final delivery must ack after recording the failure, got %v", err)
	}
	status, message := f.status(job.AnalysisID)
	if status != "failed" || message != finalDeliveryFailureMessage {
		t.Fatalf("final delivery: %s %q", status, message)
	}
}

// An unavailable local AI is retried without an operator click; on the
// final delivery the analysis records the unavailable message.
func TestConsumerUnavailableLocalAIRetries(t *testing.T) {
	f := newAnalysisFixture(t)
	a := &recordingAnalyzer{err: fmt.Errorf("decide: %w", ai.ErrUnavailable)}
	c := New(&config.WorkerConfig{LocalAI: ai.Config{Timeout: 10 * time.Second}}, logger.New("test", "error"), f.dbc, nil, a)

	job := f.pending()
	err := c.handle(f.ctx, &queue.Message{Data: payload(t, job), NumDelivered: 1})
	if !errors.Is(err, ai.ErrUnavailable) || errors.Unwrap(err) == nil {
		t.Fatalf("delivery 1: want a RetryAfter around ErrUnavailable, got %v", err)
	}
	if status, _ := f.status(job.AnalysisID); status != "pending" {
		t.Fatalf("delivery 1 left %s", status)
	}
	if err := c.handle(f.ctx, &queue.Message{Data: payload(t, job), NumDelivered: maxDeliver}); err != nil {
		t.Fatalf("final delivery must ack, got %v", err)
	}
	if status, message := f.status(job.AnalysisID); status != "failed" || message != analysisUnavailableMessage {
		t.Fatalf("final delivery: %s %q", status, message)
	}

	// A non-transient analyzer error stays terminal on the first delivery.
	a.err = errors.New("invalid local AI model identity or batch size")
	bad := f.pending()
	if err := c.handle(f.ctx, &queue.Message{Data: payload(t, bad), NumDelivered: 1}); err != nil {
		t.Fatalf("terminal analyzer error must ack, got %v", err)
	}
	if status, message := f.status(bad.AnalysisID); status != "failed" || message != analysisUnavailableMessage {
		t.Fatalf("terminal failure: %s %q", status, message)
	}
}

func TestConsumerLostRaceIsNotAnError(t *testing.T) {
	f := newAnalysisFixture(t)
	job := f.pending()
	// The row is finalized elsewhere while the model runs — e.g. a stale sweep
	// on a replica that lost track of the lock, or a manual fix-up.
	a := &slowAnalyzer{delay: 10 * time.Millisecond, during: func() {
		f.exec(`UPDATE incident_ai_analyses SET status='failed',error_message='finalized elsewhere' WHERE id=$1`, job.AnalysisID)
	}}
	c := New(&config.WorkerConfig{LocalAI: ai.Config{Timeout: 10 * time.Second}}, logger.New("test", "error"), f.dbc, nil, a)
	if err := c.handle(f.ctx, &queue.Message{Data: payload(t, job), NumDelivered: 1}); err != nil {
		t.Fatalf("lost race must ack, got %v", err)
	}
	if status, message := f.status(job.AnalysisID); status != "failed" || message != "finalized elsewhere" {
		t.Fatalf("lost race overwrote the row: %s %q", status, message)
	}

	updated, err := c.markReady(f.ctx, job, ai.AnalysisResult{Summary: "late"})
	if err != nil || updated {
		t.Fatalf("markReady on a finalized row: updated=%t err=%v", updated, err)
	}
	updated, err = c.markFailed(f.ctx, job, "late")
	if err != nil || updated {
		t.Fatalf("markFailed on a finalized row: updated=%t err=%v", updated, err)
	}
	fresh := f.pending()
	updated, err = c.markFailed(f.ctx, fresh, "first")
	if err != nil || !updated {
		t.Fatalf("markFailed on a pending row: updated=%t err=%v", updated, err)
	}
	// Tenant guard: a forged tenant never matches.
	forged := f.pending()
	forged.TenantID = uuid.New()
	updated, err = c.markFailed(f.ctx, forged, "forged")
	if err != nil || updated {
		t.Fatalf("markFailed crossed tenants: updated=%t err=%v", updated, err)
	}
}

// A delivery whose database step stalls (here: markReady waiting on a row
// lock) must give up at the delivery deadline, stop heartbeating, and release
// the analysis lock, so the row is neither stuck in progress nor shielded from
// the stale-pending sweep.
func TestConsumerStalledStepEndsAtDeliveryDeadline(t *testing.T) {
	f := newAnalysisFixture(t)
	job := f.pending()
	tx, err := f.dbc.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(f.ctx, `SELECT 1 FROM incident_ai_analyses WHERE id=$1 FOR UPDATE`, job.AnalysisID); err != nil {
		t.Fatal(err)
	}

	c := New(&config.WorkerConfig{LocalAI: ai.Config{Timeout: 500 * time.Millisecond}}, logger.New("test", "error"), f.dbc, nil, &countingAnalyzer{})
	c.deliveryHeadroom = 300 * time.Millisecond
	c.heartbeatEvery = 10 * time.Millisecond
	var beats atomic.Int32
	msg := &queue.Message{Data: payload(t, job), NumDelivered: 1, InProgress: func() error { beats.Add(1); return nil }}

	start := time.Now()
	err = c.handle(f.ctx, msg)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("handle ran %s past an 800ms delivery deadline", elapsed)
	}
	if err == nil {
		t.Fatal("a delivery cut off by its deadline must be retried, not acked")
	}
	if beats.Load() == 0 {
		t.Fatal("expected heartbeats while the delivery was running")
	}
	after := beats.Load()
	time.Sleep(50 * time.Millisecond)
	if beats.Load() != after {
		t.Fatal("heartbeat outlived the delivery")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if status, _ := f.status(job.AnalysisID); status != "pending" {
		t.Fatalf("status = %s", status)
	}
	// The run's advisory lock is gone (released, or its session discarded).
	deadline := time.Now().Add(5 * time.Second)
	for {
		var locks int
		if err := f.dbc.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM pg_locks WHERE locktype='advisory' AND database=(SELECT oid FROM pg_database WHERE datname=current_database())`).Scan(&locks); err != nil {
			t.Fatal(err)
		}
		if locks == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("analysis lock still held after the delivery ended (%d)", locks)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
