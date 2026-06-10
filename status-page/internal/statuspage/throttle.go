package statuspage

import (
	"sync"
	"time"
)

// slugThrottler coalesces routine broadcasts per status page slug.
//
// Routine fires for a slug happen at most once per interval: the first event
// in a window fires immediately (leading edge) and the last suppressed event
// fires once at the end of the window (trailing edge), so a burst never loses
// its final update. Urgent fires bypass the throttle entirely and reset the
// window.
type slugThrottler struct {
	mu       sync.Mutex
	interval time.Duration
	now      func() time.Time
	last     map[string]time.Time
	pending  map[string]*pendingFire
}

type pendingFire struct {
	timer *time.Timer
	fn    func()
}

func newSlugThrottler(interval time.Duration) *slugThrottler {
	return &slugThrottler{
		interval: interval,
		now:      time.Now,
		last:     make(map[string]time.Time),
		pending:  make(map[string]*pendingFire),
	}
}

// Fire invokes fn for slug, subject to per-slug coalescing.
//
// Urgent fires run immediately, cancel any pending trailing fire (it would be
// stale) and reset the window. Routine fires run immediately when outside the
// window; inside the window the latest fn is kept and scheduled to run once at
// the end of the window. A nil throttler never throttles.
func (t *slugThrottler) Fire(slug string, urgent bool, fn func()) {
	if fn == nil {
		return
	}
	if t == nil {
		fn()
		return
	}

	t.mu.Lock()
	now := t.now()

	if urgent {
		if p, ok := t.pending[slug]; ok {
			p.timer.Stop()
			delete(t.pending, slug)
		}
		t.last[slug] = now
		t.mu.Unlock()
		fn()
		return
	}

	if p, ok := t.pending[slug]; ok {
		// A trailing fire is already scheduled; keep only the latest payload.
		p.fn = fn
		t.mu.Unlock()
		return
	}

	last, seen := t.last[slug]
	if !seen || now.Sub(last) >= t.interval {
		t.last[slug] = now
		t.mu.Unlock()
		fn()
		return
	}

	// Suppressed: schedule one trailing fire at the end of the current window.
	delay := t.interval - now.Sub(last)
	p := &pendingFire{fn: fn}
	p.timer = time.AfterFunc(delay, func() { t.flush(slug) })
	t.pending[slug] = p
	t.mu.Unlock()
}

func (t *slugThrottler) flush(slug string) {
	t.mu.Lock()
	p, ok := t.pending[slug]
	if !ok {
		// Cancelled by an urgent fire racing the timer.
		t.mu.Unlock()
		return
	}
	delete(t.pending, slug)
	t.last[slug] = t.now()
	fn := p.fn
	t.mu.Unlock()
	fn()
}
