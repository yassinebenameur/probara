package airca

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/logger"
)

// fakeClock advances only when the supervised run or the fake sleep say so.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.mu.Unlock()
}

func TestRunWithRestartBacksOffAndStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clock := &fakeClock{now: time.Unix(0, 0)}
	var mu sync.Mutex
	var sleeps []time.Duration
	runs := 0
	policy := RestartPolicy{
		InitialBackoff: time.Second,
		MaxBackoff:     8 * time.Second,
		HealthyAfter:   time.Minute,
		Now:            clock.Now,
		Sleep: func(_ context.Context, d time.Duration) error {
			mu.Lock()
			sleeps = append(sleeps, d)
			mu.Unlock()
			clock.advance(d)
			return nil
		},
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunWithRestart(ctx, logger.New("test", "error"), "test", policy, func(ctx context.Context) error {
			runs++
			if runs <= 6 {
				return errors.New("consume failed")
			}
			// Seventh run is healthy until shutdown.
			<-ctx.Done()
			return ctx.Err()
		})
	}()
	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		n := len(sleeps)
		mu.Unlock()
		if n == 6 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("restarts did not happen, sleeps=%v", sleeps)
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not stop the supervisor")
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second, 8 * time.Second}
	if !reflect.DeepEqual(sleeps, want) {
		t.Fatalf("backoff = %v, want %v", sleeps, want)
	}
	if runs != 7 {
		t.Fatalf("runs = %d, want 7", runs)
	}
}

func TestRunWithRestartResetsBackoffAfterHealthyRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clock := &fakeClock{now: time.Unix(0, 0)}
	var sleeps []time.Duration
	runs := 0
	policy := RestartPolicy{
		InitialBackoff: time.Second,
		MaxBackoff:     time.Minute,
		HealthyAfter:   time.Minute,
		Now:            clock.Now,
		Sleep: func(_ context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			if len(sleeps) == 4 {
				cancel()
				return context.Canceled
			}
			return nil
		},
	}
	RunWithRestart(ctx, logger.New("test", "error"), "test", policy, func(context.Context) error {
		runs++
		if runs == 3 {
			clock.advance(2 * time.Minute) // a healthy run before failing
		}
		return errors.New("boom")
	})
	want := []time.Duration{time.Second, 2 * time.Second, time.Second, 2 * time.Second}
	if !reflect.DeepEqual(sleeps, want) {
		t.Fatalf("backoff = %v, want %v", sleeps, want)
	}
}

func TestRunWithRestartRestartsCleanExitAndHonoursCancelledSleep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runs := 0
	RunWithRestart(ctx, logger.New("test", "error"), "test", RestartPolicy{InitialBackoff: time.Millisecond}, func(context.Context) error {
		runs++
		if runs == 2 {
			cancel() // the real sleepCtx must return on cancellation
		}
		return nil // a clean return while ctx is live still restarts
	})
	if runs != 2 {
		t.Fatalf("runs = %d, want 2", runs)
	}
}

func TestDefaultRestartPolicy(t *testing.T) {
	p := DefaultRestartPolicy()
	if p.InitialBackoff != time.Second || p.MaxBackoff != 60*time.Second || p.HealthyAfter != time.Minute {
		t.Fatalf("unexpected defaults %+v", p)
	}
}
