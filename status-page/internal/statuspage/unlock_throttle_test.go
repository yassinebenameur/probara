package statuspage

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Failed credentials are throttled per client and per page; only failures
// are charged, and the page budget never locks out a fresh visitor.
func TestAccess_FailureThrottle(t *testing.T) {
	setup := func(t *testing.T) *accessFixture {
		f := newAccessFixture(t, testSessionSecret)
		// A frozen clock: no bucket refills while the bcrypt checks run.
		frozen := time.Now()
		f.h.access.failures.now = func() time.Time { return frozen }
		return f
	}
	unlock := func(f *accessFixture, slug, remote, password string) int {
		req := httptest.NewRequest(http.MethodPost, "/public/status/"+slug+"/unlock", strings.NewReader("password="+url.QueryEscape(password)))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = remote
		return f.do(req).Code
	}

	t.Run("per client", func(t *testing.T) {
		f := setup(t)
		for i := 0; i < failuresPerClientBurst; i++ {
			if code := unlock(f, "locked", "203.0.113.7:1", "wrong-guess"); code != http.StatusUnauthorized {
				t.Fatalf("attempt %d = %d", i, code)
			}
		}
		if code := unlock(f, "locked", "203.0.113.7:1", "wrong-guess"); code != http.StatusTooManyRequests {
			t.Fatalf("over the client budget = %d, want 429", code)
		}
		if code := unlock(f, "locked", "203.0.113.8:1", f.password); code != http.StatusSeeOther {
			t.Fatalf("another client's right password = %d", code)
		}
	})

	t.Run("a correct password costs nothing", func(t *testing.T) {
		f := setup(t)
		for i := 0; i < 3*failuresPerPageBurst; i++ {
			if code := unlock(f, "locked", "203.0.113.9:1", f.password); code != http.StatusSeeOther {
				t.Fatalf("right password %d = %d", i, code)
			}
		}
	})

	t.Run("empty page budget refuses only clients that failed", func(t *testing.T) {
		f := setup(t)
		for i := 0; i < failuresPerPageBurst; i++ {
			unlock(f, "locked", "198.51.100."+strconv.Itoa(i)+":1", "wrong-guess")
		}
		if code := unlock(f, "locked", "198.51.100.0:1", "wrong-guess"); code != http.StatusTooManyRequests {
			t.Fatalf("a failed client on an empty page budget = %d, want 429", code)
		}
		if code := unlock(f, "locked", "192.0.2.50:1", f.password); code != http.StatusSeeOther {
			t.Fatalf("a fresh client with the right password = %d, want 303", code)
		}
		if code := unlock(f, "locked", "192.0.2.51:1", "wrong-guess"); code != http.StatusUnauthorized {
			t.Fatalf("a fresh client's first wrong guess = %d, want 401", code)
		}
		if code := unlock(f, "locked", "192.0.2.51:1", f.password); code != http.StatusTooManyRequests {
			t.Fatalf("that client after failing = %d, want 429", code)
		}
	})

	t.Run("an unattributable peer has no bucket of its own", func(t *testing.T) {
		f := setup(t)
		// 172.16.0.5 is a private peer that is not a trusted proxy: every
		// visitor behind it would share its address.
		for i := 0; i < failuresPerPageBurst; i++ {
			if code := unlock(f, "locked", "172.16.0.5:1", "wrong-guess"); code != http.StatusUnauthorized {
				t.Fatalf("attempt %d = %d (pooled into a client bucket?)", i, code)
			}
		}
		if n := f.h.access.failures.clients.len(); n != 0 {
			t.Fatalf("%d client buckets for an unattributable peer", n)
		}
		if code := unlock(f, "locked", "172.16.0.5:1", f.password); code != http.StatusTooManyRequests {
			t.Fatalf("unattributable peer on an empty page budget = %d, want 429", code)
		}
		if code := unlock(f, "locked", "203.0.113.20:1", f.password); code != http.StatusSeeOther {
			t.Fatalf("an attributable fresh client = %d, want 303", code)
		}
	})

	t.Run("a trusted proxy's forwarded client is attributable", func(t *testing.T) {
		f := setup(t)
		post := func(client string) int {
			req := httptest.NewRequest(http.MethodPost, "/public/status/locked/unlock", strings.NewReader("password=wrong-guess"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.RemoteAddr = "10.1.2.3:1" // inside the fixture's TRUSTED_PROXY_CIDRS
			req.Header.Set("X-Forwarded-For", client)
			return f.do(req).Code
		}
		for i := 0; i < failuresPerClientBurst; i++ {
			post("203.0.113.40")
		}
		if code := post("203.0.113.40"); code != http.StatusTooManyRequests {
			t.Fatalf("forwarded client over budget = %d", code)
		}
		if code := post("203.0.113.41"); code != http.StatusUnauthorized {
			t.Fatalf("another forwarded client = %d, want its own budget", code)
		}
	})

	t.Run("IPv6 is grouped by /64", func(t *testing.T) {
		f := setup(t)
		for i := 0; i < failuresPerClientBurst; i++ {
			unlock(f, "locked", "[2001:db8:1:2::"+strconv.Itoa(i+1)+"]:1", "wrong-guess")
		}
		if code := unlock(f, "locked", "[2001:db8:1:2::ffff]:1", "wrong-guess"); code != http.StatusTooManyRequests {
			t.Fatalf("another address in the /64 = %d, want 429", code)
		}
		if code := unlock(f, "locked", "[2001:db8:1:3::1]:1", f.password); code != http.StatusSeeOther {
			t.Fatalf("a different /64 = %d", code)
		}
	})

	t.Run("wrong kiosk tokens spend the same budget", func(t *testing.T) {
		f := setup(t)
		get := func(remote, token string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodGet, "/public/status/internal?token="+url.QueryEscape(token), nil)
			req.RemoteAddr = remote
			return f.do(req)
		}
		for i := 0; i < failuresPerClientBurst; i++ {
			if rec := get("203.0.113.30:1", "spk_wrong"); rec.Code != http.StatusNotFound {
				t.Fatalf("wrong token %d = %d", i, rec.Code)
			}
		}
		if rec := get("203.0.113.30:1", f.token); rec.Code != http.StatusNotFound || len(rec.Result().Cookies()) != 0 {
			t.Fatalf("right token from a throttled client = %d, want refused like any denial", rec.Code)
		}
		if rec := get("203.0.113.31:1", f.token); rec.Code != http.StatusSeeOther {
			t.Fatalf("right token from a fresh client = %d", rec.Code)
		}
		// The token failures count against password attempts too.
		if code := unlock(f, "locked", "203.0.113.30:1", f.password); code != http.StatusTooManyRequests {
			t.Fatalf("password after token failures = %d, want 429", code)
		}
	})

	t.Run("missing slugs create no buckets", func(t *testing.T) {
		f := setup(t)
		for i := 0; i < 20; i++ {
			unlock(f, "no-such-page-"+strconv.Itoa(i), "192.0.2.1:1", "x")
		}
		if f.h.access.failures.pages.len() != 0 || f.h.access.failures.clients.len() != 0 {
			t.Fatal("missing slugs created buckets")
		}
	})
}

func TestLRUBucketsCap(t *testing.T) {
	b := newLRUBuckets(60, 1, 3)
	now := time.Now()
	for _, k := range []string{"a", "b", "c"} {
		b.get(k, now).lim.AllowN(now, 1)
	}
	b.peek("a") // a is now the most recently used
	b.get("d", now)
	if b.len() != 3 || b.peek("b") != nil {
		t.Fatalf("len %d, least recently used kept: %v", b.len(), b.peek("b") != nil)
	}
	if b.peek("a") == nil || b.peek("c") == nil || b.peek("d") == nil {
		t.Fatal("a recently used bucket was evicted")
	}
}
