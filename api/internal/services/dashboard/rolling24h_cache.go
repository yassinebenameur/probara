package dashboard

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
)

// rolling24hCacheTTL bounds how long the shared rolling-24h aggregates may be
// served without recomputation. The dashboard frontend fires summary and
// problem-monitors in parallel on page load (and group sparklines shortly
// after), so a short TTL collapses that burst into one computation while
// keeping worst-case staleness small.
//
// Staleness note: response GeneratedAt fields (DashboardSummaryResponse,
// DashboardProblemMonitorsResponse, ...) are stamped with time.Now() when the
// response is assembled, NOT when the cached aggregates were computed, so the
// underlying numbers may lag generated_at by up to this TTL.
const rolling24hCacheTTL = 30 * time.Second

// rolling24hCacheMaxEntries bounds the entries map. One entry exists per
// distinct (tenant, tag-filter) — plus per group sparkline monitor set — so
// the bound is generous; when exceeded the map is reset wholesale (same
// strategy as the status-page render cache). The only cost of a reset is a
// recompute on the next request for each dropped key.
const rolling24hCacheMaxEntries = 512

// rolling24hBuildTimeout bounds a detached cache build. Builds run detached
// from the triggering request's cancellation because, with singleflight, one
// client disconnecting must not fail every concurrent waiter collapsed onto
// the same flight.
const rolling24hBuildTimeout = 15 * time.Second

// rolling24hCacheEntry is one cached computation: the heavy aggregates plus
// the monitor-ID set they were computed over.
type rolling24hCacheEntry struct {
	data       *rolling24hData
	monitorIDs []uuid.UUID
	expires    time.Time
}

// rolling24hCache caches the DB-heavy rolling-24h dashboard aggregates per
// (tenant, tag-filter) key so the parallel summary / problem-monitors /
// sparkline requests of one page load cost a single computation. Concurrent
// misses for the same key are collapsed with singleflight; build errors are
// returned to every waiter of the flight and are never cached.
//
// Cached values are shared between callers: the *rolling24hData maps and the
// monitorIDs slice must be treated as read-only.
type rolling24hCache struct {
	ttl   time.Duration
	now   func() time.Time // injectable for tests
	group singleflight.Group

	mu      sync.Mutex
	entries map[string]*rolling24hCacheEntry
}

func newRolling24hCache(ttl time.Duration) *rolling24hCache {
	if ttl <= 0 {
		ttl = rolling24hCacheTTL
	}
	return &rolling24hCache{
		ttl:     ttl,
		now:     time.Now,
		entries: make(map[string]*rolling24hCacheEntry),
	}
}

// Get returns the cached aggregates for key, invoking build at most once per
// TTL window across concurrent callers. A nil cache degrades to calling build
// directly.
func (c *rolling24hCache) Get(key string, build func() (*rolling24hData, []uuid.UUID, error)) (*rolling24hData, []uuid.UUID, error) {
	if c == nil {
		return build()
	}

	if entry, ok := c.lookup(key); ok {
		return entry.data, entry.monitorIDs, nil
	}

	v, err, _ := c.group.Do(key, func() (interface{}, error) {
		// A flight that completed while we waited may have stored a fresh entry.
		if entry, ok := c.lookup(key); ok {
			return entry, nil
		}
		data, monitorIDs, err := build()
		if err != nil {
			return nil, err
		}
		entry := &rolling24hCacheEntry{
			data:       data,
			monitorIDs: monitorIDs,
			expires:    c.now().Add(c.ttl),
		}
		c.store(key, entry)
		return entry, nil
	})
	if err != nil {
		return nil, nil, err
	}
	entry := v.(*rolling24hCacheEntry)
	return entry.data, entry.monitorIDs, nil
}

func (c *rolling24hCache) lookup(key string) (*rolling24hCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if c.now().After(entry.expires) {
		delete(c.entries, key)
		return nil, false
	}
	return entry, true
}

// store inserts entry, resetting the map wholesale if adding a new key would
// exceed the bound (drop-all eviction, same pattern as the status-page caches).
func (c *rolling24hCache) store(key string, entry *rolling24hCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; !exists && len(c.entries) >= rolling24hCacheMaxEntries {
		c.entries = make(map[string]*rolling24hCacheEntry)
	}
	c.entries[key] = entry
}

// rolling24hCacheKey derives the cache key for the tenant-wide aggregates from
// the tenant and the (already normalized) dashboard tag filter. Tags are
// sorted on a copy — the caller's slice is never mutated — so logically equal
// filters in different orders share an entry. Each tag is strconv.Quote'd
// before joining with '|': quoting escapes embedded quotes/backslashes, which
// makes the encoding uniquely decodable, so a tag containing '|' (e.g.
// ["a|b"]) cannot collide with the two-tag filter ["a","b"].
func rolling24hCacheKey(tenantID uuid.UUID, tags []string) string {
	if len(tags) == 0 {
		return tenantID.String()
	}
	sorted := make([]string, len(tags))
	copy(sorted, tags)
	sort.Strings(sorted)

	var b strings.Builder
	b.WriteString(tenantID.String())
	for _, tag := range sorted {
		b.WriteByte('|')
		b.WriteString(strconv.Quote(tag))
	}
	return b.String()
}

// sparkline24hCacheKey derives the cache key for a group sparkline's hourly
// series. A sparkline covers an arbitrary SUBSET of the tenant's monitors
// (one group), so the key is the hash of the sorted monitor-ID set rather
// than tag strings: identical sets share an entry regardless of which group
// tag / filter resolved them, and membership changes (including curated-tag
// edits affecting the ungrouped sentinel) take effect immediately because the
// cheap ID resolution still runs per request.
func sparkline24hCacheKey(tenantID uuid.UUID, monitorIDs []uuid.UUID) string {
	sorted := make([]uuid.UUID, len(monitorIDs))
	copy(sorted, monitorIDs)
	sort.Slice(sorted, func(i, j int) bool {
		return bytes.Compare(sorted[i][:], sorted[j][:]) < 0
	})
	h := sha256.New()
	for _, id := range sorted {
		h.Write(id[:])
	}
	return "sparkline|" + tenantID.String() + "|" + hex.EncodeToString(h.Sum(nil)[:16])
}
