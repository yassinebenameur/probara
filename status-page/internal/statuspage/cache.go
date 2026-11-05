package statuspage

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// defaultRenderCacheTTL bounds how long a rendered status page may be served
// without recomputation. SSE-driven invalidation usually refreshes a page
// sooner; the TTL is the worst-case staleness when no events arrive (or the
// NATS subscriber is down).
const defaultRenderCacheTTL = 10 * time.Second

// renderCacheGenerationCap bounds the per-slug generation map. When exceeded,
// the map is reset wholesale (same strategy as the slug cache eviction). The
// only cost of a reset is that a build in flight across the reset may store a
// result that an Invalidate tried to drop; the TTL bounds that staleness.
const renderCacheGenerationCap = 4096

// renderCacheTTLFromEnv returns the render cache TTL. STATUS_PAGE_CACHE_TTL is
// parsed with time.ParseDuration (e.g. "10s", "1m", "500ms"); unset, invalid,
// or non-positive values fall back to defaultRenderCacheTTL.
func renderCacheTTLFromEnv() time.Duration {
	raw := strings.TrimSpace(os.Getenv("STATUS_PAGE_CACHE_TTL"))
	if raw == "" {
		return defaultRenderCacheTTL
	}
	ttl, err := time.ParseDuration(raw)
	if err != nil || ttl <= 0 {
		return defaultRenderCacheTTL
	}
	return ttl
}

type cacheEntry struct {
	page    renderedPage
	etag    string
	expires time.Time
}

// renderedPage is one cached render: the HTML (with the CSP nonce still a
// placeholder) and the page's CSP inputs, so every response can mint its own
// nonce without re-rendering.
type renderedPage struct {
	html string
	csp  pageCSP
}

// renderCache caches the rendered HTML of public status pages per slug so N
// concurrent viewers cost one render per TTL window. Concurrent misses for
// the same slug are collapsed with singleflight. Invalidate (called by the
// SSE subscriber before broadcasting) drops the entry and bumps the slug's
// generation so a build that started before the invalidation can never store
// pre-invalidation HTML; the SSE-triggered refetch therefore always rebuilds.
type renderCache struct {
	ttl   time.Duration
	now   func() time.Time
	group singleflight.Group

	mu      sync.Mutex
	entries map[string]*cacheEntry
	gens    map[string]uint64
}

func newRenderCache(ttl time.Duration) *renderCache {
	if ttl <= 0 {
		ttl = defaultRenderCacheTTL
	}
	return &renderCache{
		ttl:     ttl,
		now:     time.Now,
		entries: make(map[string]*cacheEntry),
		gens:    make(map[string]uint64),
	}
}

// Get returns the cached rendered page and its ETag for slug, invoking build
// at most once per TTL window across concurrent callers. Build errors are
// returned to every waiter of the flight and are never cached. A nil cache
// degrades to calling build directly.
func (c *renderCache) Get(slug string, build func() (string, error)) (string, string, error) {
	page, etag, err := c.GetPage(slug, func() (renderedPage, error) {
		html, err := build()
		return renderedPage{html: html}, err
	})
	return page.html, etag, err
}

// GetPage is Get for a full renderedPage. The ETag covers the CSP inputs as
// well as the HTML: a 304 makes the browser keep its stored headers, so a
// changed policy (csp_mode, embed origins) must change the ETag even when
// the markup did not.
func (c *renderCache) GetPage(slug string, build func() (renderedPage, error)) (renderedPage, string, error) {
	if c == nil {
		page, err := build()
		if err != nil {
			return renderedPage{}, "", err
		}
		return page, page.etag(), nil
	}

	if entry, ok := c.lookup(slug); ok {
		return entry.page, entry.etag, nil
	}

	v, err, _ := c.group.Do(slug, func() (interface{}, error) {
		// A flight that completed while we waited may have stored a fresh entry.
		if entry, ok := c.lookup(slug); ok {
			return entry, nil
		}
		gen := c.generation(slug)
		page, err := build()
		if err != nil {
			return nil, err
		}
		entry := &cacheEntry{
			page:    page,
			etag:    page.etag(),
			expires: c.now().Add(c.ttl),
		}
		c.storeIfCurrent(slug, gen, entry)
		return entry, nil
	})
	if err != nil {
		return renderedPage{}, "", err
	}
	entry := v.(*cacheEntry)
	return entry.page, entry.etag, nil
}

func (p renderedPage) etag() string {
	if p.csp.mode == "" {
		return computeETag(p.html)
	}
	return computeETag(p.html + "\x00" + p.csp.mode + "\x00" + strings.Join(p.csp.frameAncestors, " "))
}

// Invalidate drops the cached entry for slug so the next Get rebuilds it.
// In-flight builds for the slug are detached (singleflight Forget) and their
// result is discarded, since it was computed from pre-invalidation data.
func (c *renderCache) Invalidate(slug string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(c.entries, slug)
	if _, seen := c.gens[slug]; !seen && len(c.gens) >= renderCacheGenerationCap {
		c.gens = make(map[string]uint64)
	}
	c.gens[slug]++
	c.mu.Unlock()
	c.group.Forget(slug)
}

// HasEntries reports whether any unexpired rendered page is currently cached.
// Expired entries encountered during the scan are evicted, so a viewer-less
// deployment converges back to an empty map after one TTL. The SSE subscriber
// uses it to skip slug resolution when nothing could be stale.
func (c *renderCache) HasEntries() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	fresh := false
	for slug, entry := range c.entries {
		if now.After(entry.expires) {
			delete(c.entries, slug)
			continue
		}
		fresh = true
	}
	return fresh
}

func (c *renderCache) lookup(slug string) (*cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[slug]
	if !ok {
		return nil, false
	}
	if c.now().After(entry.expires) {
		delete(c.entries, slug)
		return nil, false
	}
	return entry, true
}

func (c *renderCache) generation(slug string) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gens[slug]
}

// storeIfCurrent stores entry unless the slug was invalidated after gen was
// captured, in which case the (possibly stale) result is discarded.
func (c *renderCache) storeIfCurrent(slug string, gen uint64, entry *cacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gens[slug] != gen {
		return
	}
	c.entries[slug] = entry
}

// computeETag returns a strong, quoted ETag derived from the page bytes:
// the first 16 bytes of the SHA-256 digest, hex-encoded.
func computeETag(html string) string {
	sum := sha256.Sum256([]byte(html))
	return fmt.Sprintf("\"%x\"", sum[:16])
}
