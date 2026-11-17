package statuspage

import (
	"container/list"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"
)

// Failed-credential throttling on gated pages: wrong passwords on /unlock and
// wrong kiosk tokens on the page URL spend one budget.
//
//   - Only failures are charged. A correct credential costs nothing, so a
//     visitor who types the password right is never pushed into a 429 by the
//     attempt itself.
//   - Per client (clientip's BucketKey: an attributable address, IPv6
//     grouped by /64) and per page.
//   - An empty page budget refuses only clients that already failed in the
//     window: a fresh visitor still gets a check, so a distributed guess
//     cannot lock out everyone who knows the password.
//   - A client that is not attributable (an untrusted private peer, i.e.
//     usually the ingress itself when TRUSTED_PROXY_CIDRS is unset) has no
//     bucket of its own -- keying on it would pool every visitor -- and is
//     limited by the page budget alone.
//   - Both maps are LRUs with a hard size cap; nothing scans them.
const (
	failuresPerClientPerMinute = 5
	failuresPerClientBurst     = 5
	failuresPerPagePerMinute   = 30
	failuresPerPageBurst       = 10
	// recentFailureWindow is how long a client counts as "already failed"
	// for the empty-page-budget rule.
	recentFailureWindow    = 10 * time.Minute
	failureThrottleMaxKeys = 10000
)

type failureThrottle struct {
	mu      sync.Mutex
	clients *lruBuckets
	pages   *lruBuckets
	now     func() time.Time
}

func newFailureThrottle() *failureThrottle {
	return &failureThrottle{
		clients: newLRUBuckets(failuresPerClientPerMinute, failuresPerClientBurst, failureThrottleMaxKeys),
		pages:   newLRUBuckets(failuresPerPagePerMinute, failuresPerPageBurst, failureThrottleMaxKeys),
		now:     time.Now,
	}
}

// allowed reports whether a credential attempt from client on page may be
// checked at all. It charges nothing.
func (t *failureThrottle) allowed(client string, page uuid.UUID) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if client != "" {
		if e := t.clients.peek(client); e != nil && e.lim.TokensAt(now) < 1 {
			return false
		}
	}
	if e := t.pages.peek(page.String()); e != nil && e.lim.TokensAt(now) < 1 {
		if client == "" {
			return false
		}
		c := t.clients.peek(client)
		return c == nil || now.Sub(c.lastFailure) >= recentFailureWindow
	}
	return true
}

// fail charges one failed attempt to client (when attributable) and page.
func (t *failureThrottle) fail(client string, page uuid.UUID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if client != "" {
		e := t.clients.get(client, now)
		e.lim.AllowN(now, 1)
		e.lastFailure = now
	}
	e := t.pages.get(page.String(), now)
	e.lim.AllowN(now, 1)
	e.lastFailure = now
}

// lruBuckets is a size-capped map of token buckets, least recently used
// evicted first. An evicted bucket comes back full, which is what an idle
// one would be anyway.
type lruBuckets struct {
	perMinute float64
	burst     int
	max       int
	order     *list.List // front = most recently used; values are *bucketEntry
	index     map[string]*list.Element
}

type bucketEntry struct {
	key         string
	lim         *rate.Limiter
	lastFailure time.Time
}

func newLRUBuckets(perMinute float64, burst, max int) *lruBuckets {
	return &lruBuckets{perMinute: perMinute, burst: burst, max: max, order: list.New(), index: map[string]*list.Element{}}
}

// peek returns the key's bucket without creating it.
func (b *lruBuckets) peek(key string) *bucketEntry {
	if el, ok := b.index[key]; ok {
		b.order.MoveToFront(el)
		return el.Value.(*bucketEntry)
	}
	return nil
}

// get returns the key's bucket, creating it (and evicting the least recently
// used one at the cap).
func (b *lruBuckets) get(key string, now time.Time) *bucketEntry {
	if e := b.peek(key); e != nil {
		return e
	}
	if b.order.Len() >= b.max {
		oldest := b.order.Back()
		b.order.Remove(oldest)
		delete(b.index, oldest.Value.(*bucketEntry).key)
	}
	e := &bucketEntry{key: key, lim: rate.NewLimiter(rate.Limit(b.perMinute/60.0), b.burst)}
	b.index[key] = b.order.PushFront(e)
	return e
}

func (b *lruBuckets) len() int { return b.order.Len() }
