package domainexpiry

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHostBucketBurstRefillAndCoolDown(t *testing.T) {
	d := newDirectoryWith(nil)
	clock := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return clock }

	for i := 0; i < hostBurst; i++ {
		if err := d.acquire("rdap.example"); err != nil {
			t.Fatalf("lookup %d refused: %v", i, err)
		}
	}
	err := d.acquire("rdap.example")
	if !errors.Is(err, errThrottled) || !strings.Contains(err.Error(), "too many lookups to rdap.example") {
		t.Fatalf("over the burst: %v", err)
	}
	// Another host has its own bucket.
	if err := d.acquire("whois.example"); err != nil {
		t.Fatalf("other host: %v", err)
	}
	clock = clock.Add(2 * hostRefill)
	if err := d.acquire("rdap.example"); err != nil {
		t.Fatalf("after refill: %v", err)
	}

	// A cool-down refuses every lookup to that host until it ends, and is
	// bounded both ways.
	d.coolDown("whois.example", time.Second)
	if err := d.acquire("whois.example"); !errors.Is(err, errThrottled) || !strings.Contains(err.Error(), "asked this worker to slow down") {
		t.Fatalf("cooling down: %v", err)
	}
	clock = clock.Add(coolDownMin)
	if err := d.acquire("whois.example"); err != nil {
		t.Fatalf("after the minimum cool-down: %v", err)
	}
	d.coolDown("whois.example", 48*time.Hour)
	clock = clock.Add(coolDownMax)
	if err := d.acquire("whois.example"); err != nil {
		t.Fatalf("cool-down not capped: %v", err)
	}
}

func TestDirectoryCachesAreBounded(t *testing.T) {
	d := newDirectoryWith(nil)
	for i := 0; i < maxTrackedHosts+50; i++ {
		_ = d.acquire(fmt.Sprintf("host%d.example", i))
	}
	if len(d.hosts) > maxTrackedHosts {
		t.Fatalf("tracked hosts = %d", len(d.hosts))
	}
	for i := 0; i < maxCachedWHOISes+50; i++ {
		d.storeWHOISServer(fmt.Sprintf("tld%d", i), "")
	}
	if len(d.whois) > maxCachedWHOISes {
		t.Fatalf("cached WHOIS servers = %d", len(d.whois))
	}
	// The newest entry survives eviction.
	if _, ok := d.cachedWHOISServer(fmt.Sprintf("tld%d", maxCachedWHOISes+49)); !ok {
		t.Fatal("newest entry evicted")
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	h := http.Header{}
	if got := retryAfter(h, now); got != coolDownDefault {
		t.Errorf("absent = %v", got)
	}
	h.Set("Retry-After", "120")
	if got := retryAfter(h, now); got != 2*time.Minute {
		t.Errorf("seconds = %v", got)
	}
	h.Set("Retry-After", now.Add(10*time.Minute).Format(http.TimeFormat))
	if got := retryAfter(h, now); got != 10*time.Minute {
		t.Errorf("date = %v", got)
	}
}

// A 429 cools the host down: the next lookup does not reach it at all, and
// reads as throttled (an unknown expiry), never as "not registered".
func TestLookupBacksOffAfterRateLimit(t *testing.T) {
	n := newFakeNet(t)
	var hits atomic.Int32
	n.rdap(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "600")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	client := n.client(rdapDir(), true)
	for i := 0; i < 3; i++ {
		info := client.Lookup(lookupCtx(t), "example.com")
		if info.Registered != nil || !strings.HasPrefix(info.UnknownReason, "rdap: throttled: ") {
			t.Fatalf("lookup %d: %+v", i, info)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("RDAP server hit %d times; a cool-down must hold back the rest", hits.Load())
	}
}

// A burst of lookups beyond the bucket never reaches the registry.
func TestLookupThrottlesBursts(t *testing.T) {
	n := newFakeNet(t)
	var hits atomic.Int32
	body := fixture(t, "rdap-example-com.json")
	n.rdap(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write(body)
	})
	dir := rdapDir()
	frozen := time.Now()
	dir.now = func() time.Time { return frozen }
	client := n.client(dir, true)
	var throttled int
	for i := 0; i < hostBurst+5; i++ {
		info := client.Lookup(lookupCtx(t), "example.com")
		if strings.Contains(info.UnknownReason, "too many lookups to "+rdapHost) {
			throttled++
		}
	}
	if hits.Load() != hostBurst || throttled != 5 {
		t.Fatalf("hits = %d, throttled = %d", hits.Load(), throttled)
	}
}

// A WHOIS refusal cools that server down too.
func TestLookupWHOISRefusalCoolsDown(t *testing.T) {
	n := newFakeNet(t)
	n.whois(whoisRoot, func(string) string { return "whois: " + whoisRegistry + "\n" })
	n.whois(whoisRegistry, func(string) string { return "%% Maximum query rate reached\n%% not found\n" })
	client := n.client(newDirectoryWith(nil), true)
	for i := 0; i < 3; i++ {
		info := client.Lookup(lookupCtx(t), "example.se")
		if info.Registered != nil || !strings.Contains(info.UnknownReason, "whois: throttled: ") {
			t.Fatalf("lookup %d: %+v", i, info)
		}
	}
	if q := n.queriesTo(whoisRegistry); len(q) != 1 {
		t.Fatalf("registry queried %d times after refusing", len(q))
	}
}
