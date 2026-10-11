package airca

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/queue"
)

func TestRetryDelayGrowsWithDeliveries(t *testing.T) {
	for n, want := range map[uint64]time.Duration{0: 10 * time.Second, 1: 10 * time.Second, 2: 30 * time.Second, 3: 30 * time.Second} {
		if got := retryDelay(n); got != want {
			t.Fatalf("retryDelay(%d) = %s, want %s", n, got, want)
		}
	}
}

func TestHeartbeatTicksUntilStoppedAndNeverAfter(t *testing.T) {
	var calls atomic.Int32
	msg := &queue.Message{InProgress: func() error { calls.Add(1); return errors.New("ignored") }}
	stop := startHeartbeat(context.Background(), msg, 5*time.Millisecond, logger.New("test", "error").WithField("t", 1))
	deadline := time.Now().Add(5 * time.Second)
	for calls.Load() < 3 {
		if time.Now().After(deadline) {
			t.Fatal("heartbeat never fired")
		}
		time.Sleep(time.Millisecond)
	}
	stop() // waits for the goroutine, so the count is final from here on
	after := calls.Load()
	time.Sleep(30 * time.Millisecond)
	if calls.Load() != after {
		t.Fatal("InProgress called after stop")
	}
	// A message without InProgress (tests, replays) is a no-op.
	startHeartbeat(context.Background(), &queue.Message{}, time.Millisecond, nil)()
}

// The heartbeat ends with the delivery deadline even if the handler is still
// stuck, so JetStream can redeliver after AckWait.
func TestHeartbeatStopsAtContextDeadline(t *testing.T) {
	var calls atomic.Int32
	msg := &queue.Message{InProgress: func() error { calls.Add(1); return nil }}
	ctx, cancel := context.WithCancel(context.Background())
	stop := startHeartbeat(ctx, msg, 5*time.Millisecond, logger.New("test", "error").WithField("t", 1))
	defer stop()
	for deadline := time.Now().Add(5 * time.Second); calls.Load() == 0; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("heartbeat never fired")
		}
	}
	cancel()
	time.Sleep(20 * time.Millisecond) // let an in-flight tick finish
	after := calls.Load()
	time.Sleep(30 * time.Millisecond)
	if calls.Load() != after {
		t.Fatal("InProgress kept firing after the delivery deadline")
	}
}

// A shutdown is not a failure: even the final delivery must not record one
// (settle would need the database for that; nil db panics if it tries).
func TestSettleOnShutdownRetriesAndNeverRecordsFailure(t *testing.T) {
	c := New(&config.WorkerConfig{}, logger.New("test", "error"), nil, nil, nil)
	entry := c.logger.WithField("t", 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, delivery := range []uint64{1, maxDeliver} {
		err := c.settle(ctx, entry, &queue.Message{NumDelivered: delivery}, ai.AnalysisJob{AnalysisID: uuid.New()}, context.Canceled)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("delivery %d during shutdown must NAK, got %v", delivery, err)
		}
	}
}

// settle never touches the database on these paths, so a nil db is fine.
func TestSettleBusyAndNonFinalErrorsRetryWithDelay(t *testing.T) {
	c := New(&config.WorkerConfig{}, logger.New("test", "error"), nil, nil, nil)
	entry := c.logger.WithField("t", 1)
	job := ai.AnalysisJob{AnalysisID: uuid.New()}
	ctx := context.Background()

	if err := c.settle(ctx, entry, &queue.Message{NumDelivered: 1}, job, errAnalysisBusy); !errors.Is(err, errAnalysisBusy) {
		t.Fatalf("busy non-final delivery must retry, got %v", err)
	}
	if err := c.settle(ctx, entry, &queue.Message{NumDelivered: maxDeliver}, job, errAnalysisBusy); err != nil {
		t.Fatalf("busy final delivery must ack (the lock holder finalizes), got %v", err)
	}
	transient := errors.New("db down")
	if err := c.settle(ctx, entry, &queue.Message{NumDelivered: 2}, job, transient); !errors.Is(err, transient) {
		t.Fatalf("transient non-final error must retry, got %v", err)
	}
	if err := c.settle(ctx, entry, &queue.Message{NumDelivered: 1}, job, nil); err != nil {
		t.Fatal(err)
	}
}

func TestSweepWindowExceedsLongestDelivery(t *testing.T) {
	if err := ValidateSweepWindow(maxLocalAITimeout); err != nil {
		t.Fatalf("max configurable timeout must fit the sweep window: %v", err)
	}
	if err := ValidateSweepWindow(staleAfter); err == nil {
		t.Fatal("a timeout as long as the sweep window must be rejected")
	}
	if heartbeatInterval >= time.Second+ackWaitHeadroom {
		t.Fatal("heartbeat must fire before the shortest AckWait expires")
	}
}

// TestLockCeilingCoversLongestDeliveryChain pins lockCeiling above both the
// longest a single run can legitimately hold a row past staleAfter and a whole
// chain of maximal deliveries with their retry delays.
func TestLockCeilingCoversLongestDeliveryChain(t *testing.T) {
	delivery := maxLocalAITimeout + ackWaitHeadroom
	if late := staleAfter + SweepInterval + delivery; lockCeiling <= late {
		t.Fatalf("lockCeiling %s must exceed a delivery started at the sweep's latest (%s)", lockCeiling, late)
	}
	chain := time.Duration(maxDeliver) * delivery
	for n := uint64(1); n < maxDeliver; n++ {
		chain += retryDelay(n)
	}
	if lockCeiling <= chain {
		t.Fatalf("lockCeiling %s must exceed a full delivery chain (%s)", lockCeiling, chain)
	}
}

// TestLocalAITimeoutBound pins maxLocalAITimeout to the bound the config
// loader enforces; if the loader accepts more, the sweep invariant breaks.
func TestLocalAITimeoutBound(t *testing.T) {
	t.Setenv("HTTP_PORT", "8083")
	t.Setenv("METRICS_PORT", "9092")
	maxSeconds := int(maxLocalAITimeout / time.Second)
	t.Setenv("LOCAL_AI_TIMEOUT_SECONDS", strconv.Itoa(maxSeconds))
	cfg, err := config.LoadWorkerConfig()
	if err != nil {
		t.Fatalf("max timeout rejected: %v", err)
	}
	if cfg.LocalAI.Timeout != maxLocalAITimeout {
		t.Fatalf("timeout = %s", cfg.LocalAI.Timeout)
	}
	t.Setenv("LOCAL_AI_TIMEOUT_SECONDS", strconv.Itoa(maxSeconds+1))
	if _, err := config.LoadWorkerConfig(); err == nil || !strings.Contains(err.Error(), "LOCAL_AI_TIMEOUT_SECONDS") {
		t.Fatalf("timeout above maxLocalAITimeout accepted: %v", err)
	}
}
