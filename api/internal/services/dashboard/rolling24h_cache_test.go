package dashboard

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRolling24hCache_TTLExpiryWithInjectedClock(t *testing.T) {
	c := newRolling24hCache(30 * time.Second)
	current := time.Date(2026, time.June, 10, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return current }

	builds := 0
	build := func() (*rolling24hData, []uuid.UUID, error) {
		builds++
		return &rolling24hData{}, []uuid.UUID{uuid.New()}, nil
	}

	d1, ids1, err := c.Get("k", build)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	d2, ids2, err := c.Get("k", build)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if builds != 1 {
		t.Fatalf("builds = %d, want 1 (second Get within TTL must hit cache)", builds)
	}
	if d1 != d2 || len(ids1) != len(ids2) || ids1[0] != ids2[0] {
		t.Fatalf("cached Get returned different values")
	}

	// Advance past the TTL: the entry must expire and be rebuilt.
	current = current.Add(31 * time.Second)
	d3, _, err := c.Get("k", build)
	if err != nil {
		t.Fatalf("Get() after expiry error = %v", err)
	}
	if builds != 2 {
		t.Fatalf("builds = %d, want 2 (expired entry must rebuild)", builds)
	}
	if d3 == d1 {
		t.Fatalf("rebuilt entry returned the stale pointer")
	}
}

func TestRolling24hCacheKey_TagOrderInsensitiveAndNonMutating(t *testing.T) {
	tenantID := uuid.New()
	tags := []string{"b", "a"}

	k1 := rolling24hCacheKey(tenantID, tags)
	k2 := rolling24hCacheKey(tenantID, []string{"a", "b"})
	if k1 != k2 {
		t.Fatalf("keys differ for same tag set: %q vs %q", k1, k2)
	}
	if tags[0] != "b" || tags[1] != "a" {
		t.Fatalf("caller's tag slice was mutated: %v", tags)
	}

	if rolling24hCacheKey(uuid.New(), []string{"a", "b"}) == k1 {
		t.Fatalf("different tenants produced the same key")
	}
	if rolling24hCacheKey(tenantID, nil) == k1 {
		t.Fatalf("empty filter collided with tagged filter")
	}
}

func TestRolling24hCacheKey_PipeInTagDoesNotCollide(t *testing.T) {
	tenantID := uuid.New()
	single := rolling24hCacheKey(tenantID, []string{"a|b"})
	double := rolling24hCacheKey(tenantID, []string{"a", "b"})
	if single == double {
		t.Fatalf("tag containing '|' collided with two-tag filter: %q", single)
	}
	// Quote-escaping must also keep quote characters unambiguous.
	if rolling24hCacheKey(tenantID, []string{`a"|"b`}) == double {
		t.Fatalf("tag containing quoted pipe collided with two-tag filter")
	}
}

func TestSparkline24hCacheKey_OrderInsensitiveAndPrefixed(t *testing.T) {
	tenantID := uuid.New()
	a, b := uuid.New(), uuid.New()
	k1 := sparkline24hCacheKey(tenantID, []uuid.UUID{a, b})
	k2 := sparkline24hCacheKey(tenantID, []uuid.UUID{b, a})
	if k1 != k2 {
		t.Fatalf("sparkline keys differ for same monitor set")
	}
	if k1 == sparkline24hCacheKey(tenantID, []uuid.UUID{a}) {
		t.Fatalf("different monitor sets produced the same key")
	}
	if k1[:10] != "sparkline|" {
		t.Fatalf("sparkline key missing prefix: %q", k1)
	}
}

func TestRolling24hCache_SingleflightDedupesConcurrentBuilds(t *testing.T) {
	c := newRolling24hCache(time.Minute)
	gate := make(chan struct{})
	var builds int32

	const n = 8
	var wg sync.WaitGroup
	results := make([]*rolling24hData, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			data, _, err := c.Get("k", func() (*rolling24hData, []uuid.UUID, error) {
				atomic.AddInt32(&builds, 1)
				<-gate
				return &rolling24hData{}, nil, nil
			})
			if err != nil {
				t.Errorf("Get() error = %v", err)
				return
			}
			results[i] = data
		}(i)
	}

	// Let the goroutines converge on the in-flight build, then release it.
	// Late arrivals (after the flight completes) hit the stored entry, so a
	// single build is guaranteed either way.
	time.Sleep(20 * time.Millisecond)
	close(gate)
	wg.Wait()

	if got := atomic.LoadInt32(&builds); got != 1 {
		t.Fatalf("builds = %d, want 1", got)
	}
	for i := 1; i < n; i++ {
		if results[i] != results[0] {
			t.Fatalf("waiter %d got a different value", i)
		}
	}
}

func TestRolling24hCache_BuildErrorNotCached(t *testing.T) {
	c := newRolling24hCache(time.Minute)
	builds := 0
	boom := errors.New("boom")

	_, _, err := c.Get("k", func() (*rolling24hData, []uuid.UUID, error) {
		builds++
		return nil, nil, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Get() error = %v, want boom", err)
	}

	c.mu.Lock()
	stored := len(c.entries)
	c.mu.Unlock()
	if stored != 0 {
		t.Fatalf("error result was cached: %d entries", stored)
	}

	data, _, err := c.Get("k", func() (*rolling24hData, []uuid.UUID, error) {
		builds++
		return &rolling24hData{}, nil, nil
	})
	if err != nil {
		t.Fatalf("Get() after error = %v", err)
	}
	if data == nil || builds != 2 {
		t.Fatalf("builds = %d, want 2 (error must not be cached)", builds)
	}
}

func TestRolling24hCache_BoundedDropAllEviction(t *testing.T) {
	c := newRolling24hCache(time.Minute)
	build := func() (*rolling24hData, []uuid.UUID, error) {
		return &rolling24hData{}, nil, nil
	}

	for i := 0; i < rolling24hCacheMaxEntries; i++ {
		if _, _, err := c.Get(fmt.Sprintf("k%d", i), build); err != nil {
			t.Fatalf("Get() error = %v", err)
		}
	}
	c.mu.Lock()
	full := len(c.entries)
	c.mu.Unlock()
	if full != rolling24hCacheMaxEntries {
		t.Fatalf("entries = %d, want %d", full, rolling24hCacheMaxEntries)
	}

	// One key over the bound resets the map wholesale and stores only the new key.
	if _, _, err := c.Get("overflow", build); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	c.mu.Lock()
	after := len(c.entries)
	_, hasOverflow := c.entries["overflow"]
	c.mu.Unlock()
	if after != 1 || !hasOverflow {
		t.Fatalf("after overflow: entries = %d (hasOverflow=%v), want drop-all to 1", after, hasOverflow)
	}

	// Existing keys can still be refreshed without triggering a reset.
	if _, _, err := c.Get("overflow", build); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	c.mu.Lock()
	final := len(c.entries)
	c.mu.Unlock()
	if final != 1 {
		t.Fatalf("entries = %d, want 1", final)
	}
}

func TestRolling24hCache_NilCacheCallsBuildDirectly(t *testing.T) {
	var c *rolling24hCache
	builds := 0
	for i := 0; i < 2; i++ {
		if _, _, err := c.Get("k", func() (*rolling24hData, []uuid.UUID, error) {
			builds++
			return &rolling24hData{}, nil, nil
		}); err != nil {
			t.Fatalf("Get() error = %v", err)
		}
	}
	if builds != 2 {
		t.Fatalf("builds = %d, want 2 (nil cache must not cache)", builds)
	}
}
