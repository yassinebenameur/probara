package statuspage

import (
	"errors"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRenderCacheCollapsesConcurrentBuilds(t *testing.T) {
	cache := newRenderCache(time.Minute)

	const concurrency = 8
	var builds atomic.Int64
	started := make(chan struct{})
	gate := make(chan struct{})
	startedOnce := sync.Once{}

	build := func() (string, error) {
		builds.Add(1)
		startedOnce.Do(func() { close(started) })
		<-gate
		return "<html>page</html>", nil
	}

	type result struct {
		html string
		etag string
		err  error
	}
	results := make(chan result, concurrency)
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			html, etag, err := cache.Get("edge", build)
			results <- result{html, etag, err}
		}()
	}

	<-started
	// Give the remaining goroutines a moment to queue on the in-flight build.
	// Even if some arrive late, the entry is cached by then, so the build
	// count assertion below holds regardless of scheduling.
	time.Sleep(50 * time.Millisecond)
	close(gate)
	wg.Wait()
	close(results)

	for got := range results {
		if got.err != nil {
			t.Fatalf("Get() error = %v", got.err)
		}
		if got.html != "<html>page</html>" {
			t.Fatalf("Get() html = %q, want %q", got.html, "<html>page</html>")
		}
		if got.etag == "" {
			t.Fatal("Get() returned empty etag")
		}
	}
	if n := builds.Load(); n != 1 {
		t.Fatalf("build invocations = %d, want 1 (concurrent gets must collapse)", n)
	}
}

func TestRenderCacheServesCachedEntryUntilTTL(t *testing.T) {
	cache := newRenderCache(10 * time.Second)
	base := time.Now()
	current := base
	cache.now = func() time.Time { return current }

	builds := 0
	build := func() (string, error) {
		builds++
		return "<html>v</html>", nil
	}

	for i := 0; i < 3; i++ {
		if _, _, err := cache.Get("edge", build); err != nil {
			t.Fatalf("Get() error = %v", err)
		}
	}
	if builds != 1 {
		t.Fatalf("builds = %d within TTL, want 1", builds)
	}

	current = base.Add(10*time.Second + time.Millisecond)
	if _, _, err := cache.Get("edge", build); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if builds != 2 {
		t.Fatalf("builds = %d after TTL expiry, want 2", builds)
	}
}

func TestRenderCacheInvalidateForcesRebuild(t *testing.T) {
	cache := newRenderCache(time.Minute)

	builds := 0
	build := func() (string, error) {
		builds++
		return "<html>v</html>", nil
	}

	if _, _, err := cache.Get("edge", build); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	cache.Invalidate("edge")
	if _, _, err := cache.Get("edge", build); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if builds != 2 {
		t.Fatalf("builds = %d after Invalidate, want 2", builds)
	}
}

func TestRenderCacheInvalidateDuringInflightBuildDiscardsStaleResult(t *testing.T) {
	cache := newRenderCache(time.Minute)

	var builds atomic.Int64
	started := make(chan struct{})
	gate := make(chan struct{})
	staleBuild := func() (string, error) {
		builds.Add(1)
		close(started)
		<-gate
		return "<html>stale</html>", nil
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		html, _, err := cache.Get("edge", staleBuild)
		if err != nil {
			t.Errorf("Get() error = %v", err)
		}
		// The waiter of the detached flight still receives its own result.
		if html != "<html>stale</html>" {
			t.Errorf("Get() html = %q, want stale build result", html)
		}
	}()

	<-started
	cache.Invalidate("edge") // invalidation lands while the build is in flight
	close(gate)
	<-done

	// The stale in-flight result must not have been stored: the next Get
	// rebuilds with fresh data.
	html, _, err := cache.Get("edge", func() (string, error) {
		builds.Add(1)
		return "<html>fresh</html>", nil
	})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if html != "<html>fresh</html>" {
		t.Fatalf("Get() html = %q, want fresh rebuild (stale in-flight result must be discarded)", html)
	}
	if n := builds.Load(); n != 2 {
		t.Fatalf("builds = %d, want 2", n)
	}
}

func TestRenderCacheDoesNotCacheBuildErrors(t *testing.T) {
	cache := newRenderCache(time.Minute)

	builds := 0
	boom := errors.New("status page not found")
	failing := func() (string, error) {
		builds++
		return "", boom
	}

	for i := 0; i < 2; i++ {
		if _, _, err := cache.Get("edge", failing); !errors.Is(err, boom) {
			t.Fatalf("Get() error = %v, want %v", err, boom)
		}
	}
	if builds != 2 {
		t.Fatalf("builds = %d, want 2 (errors must not be cached)", builds)
	}
	if cache.HasEntries() {
		t.Fatal("HasEntries() = true after failed builds, want false")
	}

	// A subsequent success is cached normally.
	if _, _, err := cache.Get("edge", func() (string, error) { return "<html>ok</html>", nil }); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !cache.HasEntries() {
		t.Fatal("HasEntries() = false after successful build, want true")
	}
}

func TestRenderCacheHasEntriesIsFreshnessAware(t *testing.T) {
	cache := newRenderCache(10 * time.Second)
	base := time.Now()
	current := base
	cache.now = func() time.Time { return current }

	if cache.HasEntries() {
		t.Fatal("HasEntries() = true on empty cache, want false")
	}
	if _, _, err := cache.Get("edge", func() (string, error) { return "<html>v</html>", nil }); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !cache.HasEntries() {
		t.Fatal("HasEntries() = false with a fresh entry, want true")
	}

	current = base.Add(10*time.Second + time.Millisecond)
	if cache.HasEntries() {
		t.Fatal("HasEntries() = true after TTL expiry, want false")
	}
	// The freshness scan must also have evicted the expired entry.
	cache.mu.Lock()
	n := len(cache.entries)
	cache.mu.Unlock()
	if n != 0 {
		t.Fatalf("entries after HasEntries freshness scan = %d, want 0 (expired entries must be evicted)", n)
	}
}

func TestRenderCacheLookupEvictsExpiredEntry(t *testing.T) {
	cache := newRenderCache(10 * time.Second)
	base := time.Now()
	current := base
	cache.now = func() time.Time { return current }

	builds := 0
	build := func() (string, error) {
		builds++
		return "<html>v</html>", nil
	}
	if _, _, err := cache.Get("edge", build); err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	current = base.Add(10*time.Second + time.Millisecond)
	if entry, ok := cache.lookup("edge"); ok {
		t.Fatalf("lookup() = (%+v, true) after TTL expiry, want miss", entry)
	}
	cache.mu.Lock()
	n := len(cache.entries)
	cache.mu.Unlock()
	if n != 0 {
		t.Fatalf("entries after expired lookup = %d, want 0 (lookup must evict the expired entry)", n)
	}

	// A follow-up Get rebuilds and re-populates the cache.
	if _, _, err := cache.Get("edge", build); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if builds != 2 {
		t.Fatalf("builds = %d, want 2 (expired entry must be rebuilt)", builds)
	}
	if !cache.HasEntries() {
		t.Fatal("HasEntries() = false after rebuild, want true")
	}
}

func TestComputeETagStableQuotedAndContentSensitive(t *testing.T) {
	a1 := computeETag("<html>a</html>")
	a2 := computeETag("<html>a</html>")
	b := computeETag("<html>b</html>")

	if a1 != a2 {
		t.Fatalf("computeETag not stable: %q != %q", a1, a2)
	}
	if a1 == b {
		t.Fatalf("computeETag collision for different HTML: %q", a1)
	}
	// Quoted, 16 bytes of SHA-256 hex-encoded => 32 hex chars in quotes.
	if !regexp.MustCompile(`^"[0-9a-f]{32}"$`).MatchString(a1) {
		t.Fatalf("computeETag format = %q, want quoted 32-char hex", a1)
	}
}

func TestRenderCacheNilReceiverFallsThroughToBuild(t *testing.T) {
	var cache *renderCache

	builds := 0
	build := func() (string, error) {
		builds++
		return "<html>direct</html>", nil
	}

	for i := 0; i < 2; i++ {
		html, etag, err := cache.Get("edge", build)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if html != "<html>direct</html>" {
			t.Fatalf("Get() html = %q", html)
		}
		if etag != computeETag(html) {
			t.Fatalf("Get() etag = %q, want %q", etag, computeETag(html))
		}
	}
	if builds != 2 {
		t.Fatalf("builds = %d, want 2 (nil cache must not cache)", builds)
	}

	boom := errors.New("nope")
	if _, _, err := cache.Get("edge", func() (string, error) { return "", boom }); !errors.Is(err, boom) {
		t.Fatalf("Get() error = %v, want %v", err, boom)
	}

	// Must not panic.
	cache.Invalidate("edge")
	if cache.HasEntries() {
		t.Fatal("HasEntries() on nil cache = true, want false")
	}
}

func TestRenderCacheTTLFromEnv(t *testing.T) {
	cases := map[string]time.Duration{
		"":        defaultRenderCacheTTL,
		"  ":      defaultRenderCacheTTL,
		"nope":    defaultRenderCacheTTL,
		"-5s":     defaultRenderCacheTTL,
		"0":       defaultRenderCacheTTL,
		"500ms":   500 * time.Millisecond,
		"30s":     30 * time.Second,
		" 1m ":    time.Minute,
		"1h30m0s": 90 * time.Minute,
	}
	for raw, want := range cases {
		t.Setenv("STATUS_PAGE_CACHE_TTL", raw)
		if got := renderCacheTTLFromEnv(); got != want {
			t.Errorf("renderCacheTTLFromEnv() with %q = %v, want %v", raw, got, want)
		}
	}
}
