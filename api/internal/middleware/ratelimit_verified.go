package middleware

import (
	"container/list"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Recently verified credentials let a valid key or token keep working from an
// address that someone else has run out of auth-failure budget (an office
// NAT, a shared egress): the limiter's pre-check lets a credential in this set
// through and refuses only ones it has not seen succeed. A credential never
// seen before still waits for the address's budget -- telling it from a guess
// would take the lookup the pre-check exists to avoid.
const (
	// verifiedCredentialTTL is how long one success vouches for a credential;
	// every further success restarts it.
	verifiedCredentialTTL = 10 * time.Minute
	// verifiedCredentialsMax bounds the set per API replica; past it the
	// least recently verified entry goes.
	verifiedCredentialsMax = 20_000
)

// verifiedCredentials is a bounded, expiring LRU set of credentials that
// recently passed verification. It stores only an HMAC of each credential
// under a key generated per process, never the credential itself, so a memory
// dump cannot replay one and entries cannot be precomputed.
type verifiedCredentials struct {
	key []byte
	ttl time.Duration
	max int

	mu      sync.Mutex
	order   *list.List // front = most recently verified
	entries map[[sha256.Size]byte]*list.Element
}

type verifiedEntry struct {
	sum     [sha256.Size]byte
	expires time.Time
}

func newVerifiedCredentials(ttl time.Duration, max int) *verifiedCredentials {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		// crypto/rand does not fail on supported platforms; without a key
		// the set would still be correct, only predictable.
		panic("ratelimit: cannot generate the verified-credential key: " + err.Error())
	}
	return &verifiedCredentials{
		key:     key,
		ttl:     ttl,
		max:     max,
		order:   list.New(),
		entries: make(map[[sha256.Size]byte]*list.Element),
	}
}

func (v *verifiedCredentials) digest(credential string) [sha256.Size]byte {
	mac := hmac.New(sha256.New, v.key)
	mac.Write([]byte(credential))
	var sum [sha256.Size]byte
	copy(sum[:], mac.Sum(nil))
	return sum
}

// remember records a successful verification of credential at now.
func (v *verifiedCredentials) remember(credential string, now time.Time) {
	v.rememberFor(credential, now, v.ttl)
}

// rememberFor is remember with its own TTL (never shorter than the set's):
// an issued refresh cookie is first presented an access-token lifetime later
// (RateLimiter.issuedRefreshCookieTTL).
func (v *verifiedCredentials) rememberFor(credential string, now time.Time, ttl time.Duration) {
	if credential == "" {
		return
	}
	if ttl < v.ttl {
		ttl = v.ttl
	}
	sum := v.digest(credential)
	v.mu.Lock()
	defer v.mu.Unlock()
	if el, ok := v.entries[sum]; ok {
		if e := el.Value.(*verifiedEntry); now.Add(ttl).After(e.expires) {
			e.expires = now.Add(ttl)
		}
		v.order.MoveToFront(el)
		return
	}
	v.entries[sum] = v.order.PushFront(&verifiedEntry{sum: sum, expires: now.Add(ttl)})
	for v.order.Len() > v.max {
		oldest := v.order.Back()
		v.order.Remove(oldest)
		delete(v.entries, oldest.Value.(*verifiedEntry).sum)
	}
}

// known reports whether credential verified successfully within the TTL.
func (v *verifiedCredentials) known(credential string, now time.Time) bool {
	if credential == "" {
		return false
	}
	sum := v.digest(credential)
	v.mu.Lock()
	defer v.mu.Unlock()
	el, ok := v.entries[sum]
	if !ok {
		return false
	}
	if !now.Before(el.Value.(*verifiedEntry).expires) {
		v.order.Remove(el)
		delete(v.entries, sum)
		return false
	}
	return true
}

// forget drops credential after it failed verification (revoked, expired),
// so it is judged like any unknown credential from then on.
func (v *verifiedCredentials) forget(credential string) {
	if credential == "" {
		return
	}
	sum := v.digest(credential)
	v.mu.Lock()
	defer v.mu.Unlock()
	if el, ok := v.entries[sum]; ok {
		v.order.Remove(el)
		delete(v.entries, sum)
	}
}

func (v *verifiedCredentials) len() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.order.Len()
}

// The credential identities the limiter records. Each is prefixed with its
// carrier so one value presented two ways is two entries.

// bearerCredential is what AuthMiddleware verifies when no live session
// cookie is present: the Authorization header (API key or MCP token).
func bearerCredential(r *http.Request) string {
	if v := r.Header.Get("Authorization"); v != "" {
		return "bearer:" + v
	}
	return ""
}

// tokenPathCredential is the credential a token-path route checks: the push
// token in the path, or the collector's Authorization header.
func tokenPathCredential(r *http.Request) string {
	if token, ok := strings.CutPrefix(r.URL.Path, "/api/v1/push/"); ok {
		if token == "" {
			return ""
		}
		return "push:" + token
	}
	if v := r.Header.Get("Authorization"); v != "" {
		return "collector:" + v
	}
	return ""
}

func refreshCredential(value string) string {
	if value == "" {
		return ""
	}
	return "refresh:" + value
}

// issuedRefreshCookie returns the refresh cookie a response sets (refresh
// rotates it, sign-in issues it), or "".
func issuedRefreshCookie(h http.Header) string {
	for _, c := range (&http.Response{Header: h}).Cookies() {
		if c.Name == refreshCookieName && c.Value != "" && c.MaxAge >= 0 {
			return c.Value
		}
	}
	return ""
}
