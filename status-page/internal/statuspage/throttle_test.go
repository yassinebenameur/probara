package statuspage

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitForCount(t *testing.T, count *atomic.Int32, want int32) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if count.Load() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("count = %d, want %d", count.Load(), want)
}

func TestSlugThrottlerFirstRoutineFiresImmediately(t *testing.T) {
	throttler := newSlugThrottler(50 * time.Millisecond)
	var count atomic.Int32

	throttler.Fire("alpha", false, func() { count.Add(1) })

	if got := count.Load(); got != 1 {
		t.Fatalf("count = %d, want 1 (leading fire must be synchronous)", got)
	}
}

func TestSlugThrottlerCoalescesBurstToLeadingAndTrailing(t *testing.T) {
	throttler := newSlugThrottler(40 * time.Millisecond)
	var count atomic.Int32

	for i := 0; i < 10; i++ {
		throttler.Fire("alpha", false, func() { count.Add(1) })
	}

	if got := count.Load(); got != 1 {
		t.Fatalf("count = %d after burst, want exactly 1 leading fire", got)
	}

	// The trailing fire delivers after the interval window closes.
	waitForCount(t, &count, 2)

	// No further fires after the trailing one.
	time.Sleep(80 * time.Millisecond)
	if got := count.Load(); got != 2 {
		t.Fatalf("count = %d after settle, want 2 (leading + trailing only)", got)
	}
}

func TestSlugThrottlerTrailingFireKeepsLatestPayload(t *testing.T) {
	throttler := newSlugThrottler(30 * time.Millisecond)

	var mu sync.Mutex
	var fired []string
	var count atomic.Int32
	record := func(name string) func() {
		return func() {
			mu.Lock()
			fired = append(fired, name)
			mu.Unlock()
			count.Add(1)
		}
	}

	throttler.Fire("alpha", false, record("first"))
	throttler.Fire("alpha", false, record("second"))
	throttler.Fire("alpha", false, record("third"))

	waitForCount(t, &count, 2)

	mu.Lock()
	defer mu.Unlock()
	if len(fired) != 2 || fired[0] != "first" || fired[1] != "third" {
		t.Fatalf("fired = %v, want [first third] (last event in burst must win)", fired)
	}
}

func TestSlugThrottlerUrgentBypassesThrottleAndCancelsPending(t *testing.T) {
	throttler := newSlugThrottler(50 * time.Millisecond)
	var routine, urgent atomic.Int32

	throttler.Fire("alpha", false, func() { routine.Add(1) }) // leading
	throttler.Fire("alpha", false, func() { routine.Add(1) }) // suppressed, pending
	throttler.Fire("alpha", true, func() { urgent.Add(1) })   // bypasses, cancels pending

	if got := urgent.Load(); got != 1 {
		t.Fatalf("urgent count = %d, want 1 (urgent must fire synchronously)", got)
	}
	if got := routine.Load(); got != 1 {
		t.Fatalf("routine count = %d, want 1", got)
	}

	// The pending trailing fire was cancelled by the urgent fire.
	time.Sleep(100 * time.Millisecond)
	if got := routine.Load(); got != 1 {
		t.Fatalf("routine count = %d after settle, want 1 (urgent cancels pending)", got)
	}
}

func TestSlugThrottlerUrgentResetsWindow(t *testing.T) {
	throttler := newSlugThrottler(50 * time.Millisecond)
	var count atomic.Int32

	throttler.Fire("alpha", true, func() { count.Add(1) })
	// Urgent updated the last-fire time, so an immediate routine fire is
	// suppressed into a trailing fire.
	throttler.Fire("alpha", false, func() { count.Add(1) })

	if got := count.Load(); got != 1 {
		t.Fatalf("count = %d, want 1 (routine right after urgent must be suppressed)", got)
	}
	waitForCount(t, &count, 2)
}

func TestSlugThrottlerSlugsAreIndependent(t *testing.T) {
	throttler := newSlugThrottler(50 * time.Millisecond)
	var alpha, beta atomic.Int32

	throttler.Fire("alpha", false, func() { alpha.Add(1) })
	throttler.Fire("beta", false, func() { beta.Add(1) })

	if alpha.Load() != 1 || beta.Load() != 1 {
		t.Fatalf("alpha = %d, beta = %d, want 1 and 1 (per-slug windows)", alpha.Load(), beta.Load())
	}
}

func TestSlugThrottlerRoutineFiresAgainAfterInterval(t *testing.T) {
	throttler := newSlugThrottler(20 * time.Millisecond)
	base := time.Now()
	current := base
	throttler.now = func() time.Time { return current }

	var count atomic.Int32
	throttler.Fire("alpha", false, func() { count.Add(1) })

	current = base.Add(25 * time.Millisecond)
	throttler.Fire("alpha", false, func() { count.Add(1) })

	if got := count.Load(); got != 2 {
		t.Fatalf("count = %d, want 2 (window elapsed, no suppression)", got)
	}
}

func TestNilSlugThrottlerFiresImmediately(t *testing.T) {
	var throttler *slugThrottler
	var count atomic.Int32

	throttler.Fire("alpha", false, func() { count.Add(1) })
	throttler.Fire("alpha", false, func() { count.Add(1) })

	if got := count.Load(); got != 2 {
		t.Fatalf("count = %d, want 2 (nil throttler never throttles)", got)
	}
}
