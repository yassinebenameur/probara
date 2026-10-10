package otlp

import (
	"sync"

	"github.com/google/uuid"
)

// seriesCache maps (monitor, attr_hash) → metric_series.id so steady-state
// ingest resolves series without touching the registry (freshness is kept by
// one batched TouchSeries per report instead). Eviction is wholesale at
// capacity — series populations are stable per host, so a full flush is a
// once-in-a-blue-moon re-resolve, not churn worth an LRU list.
type seriesCache struct {
	mu    sync.Mutex
	max   int
	items map[seriesCacheKey]int64
}

type seriesCacheKey struct {
	monitorID uuid.UUID
	attrHash  [32]byte
}

func newSeriesCache(max int) *seriesCache {
	if max <= 0 {
		max = 100000
	}
	return &seriesCache{max: max, items: make(map[seriesCacheKey]int64)}
}

func (c *seriesCache) get(monitorID uuid.UUID, attrHash []byte) (int64, bool) {
	key := seriesCacheKey{monitorID: monitorID}
	copy(key.attrHash[:], attrHash)
	c.mu.Lock()
	defer c.mu.Unlock()
	id, ok := c.items[key]
	return id, ok
}

func (c *seriesCache) put(monitorID uuid.UUID, attrHash []byte, id int64) {
	key := seriesCacheKey{monitorID: monitorID}
	copy(key.attrHash[:], attrHash)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) >= c.max {
		c.items = make(map[seriesCacheKey]int64)
	}
	c.items[key] = id
}

// forgetMonitor drops a monitor's entries (after its series rows were purged
// or its cardinality state changed server-side).
func (c *seriesCache) forgetMonitor(monitorID uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.items {
		if k.monitorID == monitorID {
			delete(c.items, k)
		}
	}
}
