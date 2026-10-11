package airca

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/logger"
)

func TestSweepStaleFailsOnlyOldUnclaimedPendingRows(t *testing.T) {
	f := newAnalysisFixture(t)
	insert := func(status string, age time.Duration) uuid.UUID {
		t.Helper()
		id := uuid.New()
		f.exec(`INSERT INTO incident_ai_analyses(id,tenant_id,incident_id,status,created_at) VALUES($1,$2,$3,$4,NOW()-make_interval(secs=>$5))`,
			id, f.tenant, f.incident, status, age.Seconds())
		return id
	}
	var stale []uuid.UUID
	for i := 0; i < 40; i++ {
		stale = append(stale, insert("pending", staleAfter+time.Duration(i+1)*time.Minute))
	}
	fresh := insert("pending", time.Minute)
	nearlyStale := insert("pending", staleAfter-30*time.Second)
	oldReady := insert("ready", time.Hour)
	held := insert("pending", staleAfter+5*time.Minute)
	heldPastCeiling := insert("pending", lockCeiling+time.Minute)

	// A consumer holding the analysis lock (same acquisition as process) is
	// still working on `held`: the sweep must skip it. A lock still held past
	// lockCeiling belongs to a session that outlived its worker, so that row
	// is failed regardless.
	conn, err := f.dbc.DB.Conn(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var claimed bool
	for _, id := range []uuid.UUID{held, heldPastCeiling} {
		if err := conn.QueryRowContext(f.ctx, `SELECT pg_try_advisory_lock(`+analysisLockKeySQL("$1")+`)`, id.String()).Scan(&claimed); err != nil || !claimed {
			t.Fatalf("lock held row: %t %v", claimed, err)
		}
	}
	stale = append(stale, heldPastCeiling)

	// Two replicas sweeping at once split the work without double updates.
	var wg sync.WaitGroup
	counts := make([]int64, 2)
	errs := make([]error, 2)
	start := make(chan struct{})
	for i := range counts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			counts[i], errs[i] = SweepStale(f.ctx, f.dbc)
		}(i)
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if total := counts[0] + counts[1]; total != int64(len(stale)) {
		t.Fatalf("concurrent sweeps failed %d rows (%v), want %d", total, counts, len(stale))
	}
	for _, id := range stale {
		if status, message := f.status(id); status != "failed" || message != staleFailureMessage {
			t.Fatalf("stale row %s: %s %q", id, status, message)
		}
	}
	for name, id := range map[string]uuid.UUID{"fresh": fresh, "nearly stale": nearlyStale, "held": held} {
		if status, _ := f.status(id); status != "pending" {
			t.Fatalf("%s row was swept: %s", name, status)
		}
	}
	if status, _ := f.status(oldReady); status != "ready" {
		t.Fatalf("ready row changed: %s", status)
	}
	var actions, metadata string
	if err := f.dbc.QueryRowContext(f.ctx, `SELECT recommended_actions::text, metadata->>'outcome' FROM incident_ai_analyses WHERE id=$1`, stale[0]).Scan(&actions, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata != "failed" || actions == "" {
		t.Fatalf("swept row lacks failure metadata: %s %s", actions, metadata)
	}

	// The consumer's own lock attempt is not blocked by a finished sweep, and
	// once it lets go, the next sweep picks the row up.
	var unlocked bool
	if err := conn.QueryRowContext(f.ctx, `SELECT pg_advisory_unlock(`+analysisLockKeySQL("$1")+`)`, held.String()).Scan(&unlocked); err != nil || !unlocked {
		t.Fatalf("unlock: %t %v", unlocked, err)
	}
	if err := conn.QueryRowContext(f.ctx, `SELECT pg_advisory_unlock(`+analysisLockKeySQL("$1")+`)`, heldPastCeiling.String()).Scan(&unlocked); err != nil || !unlocked {
		t.Fatalf("unlock past-ceiling row: %t %v", unlocked, err)
	}
	n, err := SweepStale(f.ctx, f.dbc)
	if err != nil || n != 1 {
		t.Fatalf("sweep after release: %d %v", n, err)
	}
	if status, _ := f.status(held); status != "failed" {
		t.Fatalf("released row not swept: %s", status)
	}
	// Idempotent: nothing left.
	if n, err := SweepStale(f.ctx, f.dbc); err != nil || n != 0 {
		t.Fatalf("repeat sweep: %d %v", n, err)
	}
	// A finished sweep leaves no advisory locks behind (they are xact-scoped).
	var locks int
	if err := f.dbc.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM pg_locks WHERE locktype='advisory' AND database=(SELECT oid FROM pg_database WHERE datname=current_database())`).Scan(&locks); err != nil || locks != 0 {
		t.Fatalf("advisory locks left: %d %v", locks, err)
	}
}

func TestRunStaleSweepSweepsImmediatelyAndStopsOnCancel(t *testing.T) {
	f := newAnalysisFixture(t)
	id := uuid.New()
	f.exec(`INSERT INTO incident_ai_analyses(id,tenant_id,incident_id,status,created_at) VALUES($1,$2,$3,'pending',NOW()-interval '1 hour')`, id, f.tenant, f.incident)
	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan error, 1)
	go func() { done <- RunStaleSweep(ctx, logger.New("test", "error"), f.dbc, time.Hour) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if status, _ := f.status(id); status == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first sweep did not run at startup")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunStaleSweep ignored cancellation")
	}
}
