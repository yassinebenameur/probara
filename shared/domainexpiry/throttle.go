package domainexpiry

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Registries rate-limit by source address, and every check a worker runs
// shares that address: a burst of lookups (many monitors on one TLD, test
// lookups repeated from the form) could get the worker's egress blocked for
// everyone. So each worker spends lookups per registry host from a token
// bucket, and after a host says "slow down" (an HTTP 429, a WHOIS refusal)
// it leaves that host alone for a while. A throttled lookup is an unknown
// expiry with the reason, never a failure.
const (
	hostBurst        = 20          // lookups a host may take at once
	hostRefill       = time.Second // one more lookup per host per second
	coolDownDefault  = 5 * time.Minute
	coolDownMin      = time.Minute
	coolDownMax      = time.Hour
	maxTrackedHosts  = 1024
	maxCachedWHOISes = 2048
)

// errThrottled wraps every throttle refusal, so callers can tell it from a
// failed lookup.
var errThrottled = errors.New("throttled")

type hostBucket struct {
	tokens    float64
	refilled  time.Time
	coolUntil time.Time
}

// acquire spends one lookup to host, or says why it may not.
func (d *Directory) acquire(host string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	b, ok := d.hosts[host]
	if !ok {
		if len(d.hosts) >= maxTrackedHosts {
			d.evictHostLocked(now)
		}
		b = &hostBucket{tokens: hostBurst, refilled: now}
		d.hosts[host] = b
	}
	if now.Before(b.coolUntil) {
		return fmt.Errorf("%w: %s asked this worker to slow down; next lookup after %s", errThrottled, host, b.coolUntil.UTC().Format("15:04 UTC"))
	}
	b.tokens += float64(now.Sub(b.refilled)) / float64(hostRefill)
	if b.tokens > hostBurst {
		b.tokens = hostBurst
	}
	b.refilled = now
	if b.tokens < 1 {
		return fmt.Errorf("%w: too many lookups to %s from this worker", errThrottled, host)
	}
	b.tokens--
	return nil
}

// coolDown leaves host alone for d (bounded to coolDownMin..coolDownMax).
func (d *Directory) coolDown(host string, wait time.Duration) {
	if wait < coolDownMin {
		wait = coolDownMin
	}
	if wait > coolDownMax {
		wait = coolDownMax
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	b, ok := d.hosts[host]
	if !ok {
		if len(d.hosts) >= maxTrackedHosts {
			d.evictHostLocked(now)
		}
		b = &hostBucket{tokens: hostBurst, refilled: now}
		d.hosts[host] = b
	}
	if until := now.Add(wait); until.After(b.coolUntil) {
		b.coolUntil = until
	}
}

// evictHostLocked drops a host that is neither cooling down nor short of
// tokens (forgetting it loses nothing), else the least recently used one.
func (d *Directory) evictHostLocked(now time.Time) {
	var oldest string
	var oldestAt time.Time
	for host, b := range d.hosts {
		if !now.Before(b.coolUntil) && b.tokens+float64(now.Sub(b.refilled))/float64(hostRefill) >= hostBurst {
			delete(d.hosts, host)
			return
		}
		if oldest == "" || b.refilled.Before(oldestAt) {
			oldest, oldestAt = host, b.refilled
		}
	}
	delete(d.hosts, oldest)
}

// evictWHOISLocked makes room in the WHOIS server cache: expired entries
// first, else the one closest to expiry.
func (d *Directory) evictWHOISLocked(now time.Time) {
	var soonest string
	var soonestAt time.Time
	for tld, e := range d.whois {
		if now.After(e.expires) {
			delete(d.whois, tld)
			return
		}
		if soonest == "" || e.expires.Before(soonestAt) {
			soonest, soonestAt = tld, e.expires
		}
	}
	delete(d.whois, soonest)
}

// retryAfter reads a Retry-After header (seconds or an HTTP date).
func retryAfter(h http.Header, now time.Time) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return coolDownDefault
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(secs) * time.Second
	}
	if at, err := http.ParseTime(v); err == nil {
		return at.Sub(now)
	}
	return coolDownDefault
}
