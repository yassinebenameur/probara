package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

type headerRecorder struct {
	mu      sync.Mutex
	headers http.Header
}

func (r *headerRecorder) record(h http.Header) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.headers = h.Clone()
}

func (r *headerRecorder) get() http.Header {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.headers
}

// TestHTTPChecker_RedirectOffOriginDropsCredentials: an https monitor with a
// secret header and basic auth is redirected to plain http on another host;
// the second hop must carry neither, while plain headers still follow.
func TestHTTPChecker_RedirectOffOriginDropsCredentials(t *testing.T) {
	var target headerRecorder
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target.record(r.Header)
		w.WriteHeader(http.StatusOK)
	}))
	defer plain.Close()
	var origin headerRecorder
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin.record(r.Header)
		http.Redirect(w, r, plain.URL+"/landing", http.StatusFound)
	}))
	defer secure.Close()

	config, _ := json.Marshal(map[string]any{
		"url": secure.URL, "method": "GET", "tls_skip_verify": true,
		"headers":             map[string]string{"X-Trace": "t1"},
		"secret_headers":      map[string]string{"X-Custom-Credential": "key-1"},
		"basic_auth_username": "ops", "basic_auth_password": "pw",
	})
	result := NewHTTPChecker(1<<20, false, nil).Check(context.Background(), config, 5)
	if result.Status != "success" {
		t.Fatalf("status = %s (%v)", result.Status, result.ErrorMessage)
	}
	if o := origin.get(); o.Get("X-Custom-Credential") != "key-1" || o.Get("Authorization") == "" {
		t.Fatalf("the origin must still get the credentials: %v", o)
	}
	got := target.get()
	if got.Get("X-Custom-Credential") != "" || got.Get("Authorization") != "" {
		t.Fatalf("credentials followed the redirect off the origin: %v", got)
	}
	if got.Get("X-Trace") != "t1" {
		t.Fatalf("plain headers should still follow: %v", got)
	}
}

// A redirect within the origin keeps the credentials.
func TestHTTPChecker_RedirectWithinOriginKeepsCredentials(t *testing.T) {
	var final headerRecorder
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/next", http.StatusFound)
			return
		}
		final.record(r.Header)
	}))
	defer server.Close()

	config, _ := json.Marshal(map[string]any{
		"url": server.URL, "method": "GET",
		"secret_headers": map[string]string{"X-Custom-Credential": "key-1"},
	})
	if result := NewHTTPChecker(1<<20, false, nil).Check(context.Background(), config, 5); result.Status != "success" {
		t.Fatalf("status = %s (%v)", result.Status, result.ErrorMessage)
	}
	if final.get().Get("X-Custom-Credential") != "key-1" {
		t.Fatalf("same-origin redirect lost the secret header: %v", final.get())
	}
}

func TestSyntheticAPIChecker_RedirectOffOriginDropsSecretHeaders(t *testing.T) {
	var target headerRecorder
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target.record(r.Header)
	}))
	defer other.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	defer origin.Close()

	config, _ := json.Marshal(map[string]any{
		"secret_variables": map[string]string{"token": "tok-1"},
		"steps": []map[string]any{{
			"id": "s1",
			"request": map[string]any{
				"method": "GET", "url": origin.URL,
				"headers": map[string]string{
					"Authorization": "Bearer {{token}}",
					"X-Session":     "{{token}}",
					"X-Plain":       "v",
				},
			},
		}},
	})
	result := NewSyntheticAPIChecker(false, nil).Check(context.Background(), config, 5)
	if result.Status != "success" {
		t.Fatalf("status = %s (%v)", result.Status, result.ErrorMessage)
	}
	got := target.get()
	if got.Get("Authorization") != "" || got.Get("X-Session") != "" {
		t.Fatalf("secret-bearing headers followed the redirect: %v", got)
	}
	if got.Get("X-Plain") != "v" {
		t.Fatalf("plain header should still follow: %v", got)
	}
}

func TestCredentialsMayFollow(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{"https://h/a", "https://h/b", true},
		{"https://H/a", "https://h/b", true},
		{"https://h/a", "https://h:443/b", true},
		{"http://h:80/a", "http://h/b", true},
		// An http -> https upgrade on the same host keeps the credentials:
		// default ports 80 -> 443, or the same explicit port on both.
		{"http://h/a", "https://h/b", true},
		{"http://h:80/a", "https://h/b", true},
		{"http://h:8080/a", "https://h:8080/b", true},
		{"http://h:8080/a", "https://h/b", false},
		{"http://h/a", "https://h:8443/b", false},
		// A downgrade, another host (a subdomain too) or another port leaves.
		{"https://h/a", "http://h/b", false},
		{"https://h/a", "https://other/b", false},
		{"https://h/a", "https://sub.h/b", false},
		{"https://h/a", "https://h:8443/b", false},
		{"http://h/a", "https://other/b", false},
	}
	for _, tc := range cases {
		from, _ := url.Parse(tc.from)
		to, _ := url.Parse(tc.to)
		if got := credentialsMayFollow(from, to); got != tc.want {
			t.Errorf("credentialsMayFollow(%s, %s) = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}

func redirectHop(t *testing.T, method, target string, body string) *http.Request {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, target, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Basic b3BzOnB3")
	req.Header.Set("Cookie", "sid=1")
	req.Header.Set("X-Custom-Credential", "key-1")
	req.Header.Set("X-Trace", "t1")
	req.Header.Set("Referer", "https://api.test/start?key=k")
	return req
}

// TestRedirectGuard pins the per-hop rule: on the origin (or a same-host
// upgrade) every header stays; off it the credential-named and secret
// headers and Referer go, and the hop is refused when it would re-send the
// body or the request URL or body carries a secret.
func TestRedirectGuard(t *testing.T) {
	first := redirectHop(t, http.MethodGet, "http://api.test/start", "")
	guard := redirectGuard{secretNames: []string{"X-Custom-Credential"}}

	for _, target := range []string{"http://api.test/next", "https://api.test/next"} {
		hop := redirectHop(t, http.MethodGet, target, "")
		if err := guard.check(hop, []*http.Request{first}); err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		if hop.Header.Get("Authorization") == "" || hop.Header.Get("X-Custom-Credential") == "" || hop.Header.Get("Cookie") == "" || hop.Header.Get("Referer") == "" {
			t.Fatalf("%s lost credentials: %v", target, hop.Header)
		}
	}

	secureFirst := redirectHop(t, http.MethodGet, "https://api.test/start", "")
	downgrade := redirectHop(t, http.MethodGet, "http://api.test/next", "")
	if err := guard.check(downgrade, []*http.Request{secureFirst}); err != nil {
		t.Fatalf("a GET downgrade is followed without credentials: %v", err)
	}
	for _, name := range []string{"Authorization", "Cookie", "X-Custom-Credential", "Referer"} {
		if downgrade.Header.Get(name) != "" {
			t.Fatalf("downgrade kept %s: %v", name, downgrade.Header)
		}
	}
	if downgrade.Header.Get("X-Trace") != "t1" {
		t.Fatalf("plain header dropped: %v", downgrade.Header)
	}

	// A 307/308 re-sends the body: refused rather than sent on without its
	// credentials.
	resend := redirectHop(t, http.MethodPost, "https://other.test/next", `{"a":1}`)
	if err := guard.check(resend, []*http.Request{secureFirst}); !errors.Is(err, errRedirectRefused) {
		t.Fatalf("body re-send to another origin: err = %v, want a refusal", err)
	}

	// A secret in the URL or body: refused even for a GET.
	secretGuard := redirectGuard{carriesSecret: true}
	plainFirst, _ := http.NewRequest(http.MethodGet, "https://api.test/start?key=s3cret", nil)
	hop, _ := http.NewRequest(http.MethodGet, "https://other.test/next", nil)
	if err := secretGuard.check(hop, []*http.Request{plainFirst}); !errors.Is(err, errRedirectRefused) {
		t.Fatalf("secret-carrying request: err = %v, want a refusal", err)
	}

	// A request without any credential is followed as before, body included.
	bare, _ := http.NewRequest(http.MethodPost, "https://api.test/start", strings.NewReader("x"))
	bareHop, _ := http.NewRequest(http.MethodPost, "https://other.test/next", strings.NewReader("x"))
	if err := (redirectGuard{}).check(bareHop, []*http.Request{bare}); err != nil {
		t.Fatalf("a request without credentials must follow: %v", err)
	}
}

// TestHTTPChecker_RedirectResendingBodyOffOriginIsRefused: a 307 off the
// origin would re-send a POST body without the credentials it went with, so
// the check fails naming the redirect instead.
func TestHTTPChecker_RedirectResendingBodyOffOriginIsRefused(t *testing.T) {
	var reached headerRecorder
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.record(r.Header)
	}))
	defer other.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/login?from=origin", http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	config, _ := json.Marshal(map[string]any{
		"url": origin.URL + "/login?key=k", "method": "POST", "body": `{"user":"ops"}`,
		"secret_headers": map[string]string{"X-Api-Key": "key-1"},
	})
	result := NewHTTPChecker(1<<20, false, nil).Check(context.Background(), config, 5)
	if result.Status != "error" || result.ErrorMessage == nil || !strings.HasPrefix(*result.ErrorMessage, "redirect:") ||
		!strings.Contains(*result.ErrorMessage, "re-send the request body") {
		t.Fatalf("status %s, error %v; want a redirect refusal", result.Status, result.ErrorMessage)
	}
	if reached.get() != nil {
		t.Fatal("the other origin was contacted")
	}
}

// TestSyntheticAPIChecker_RedirectWithSecretInURLIsRefused: a step whose URL
// carries a secret variable is never followed to another origin, and the
// refusal is redacted like everything else.
func TestSyntheticAPIChecker_RedirectWithSecretInURLIsRefused(t *testing.T) {
	var referer string
	var reached bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		referer = r.Header.Get("Referer")
	}))
	defer other.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/next", http.StatusFound)
	}))
	defer origin.Close()

	config, _ := json.Marshal(map[string]any{
		"secret_variables": map[string]string{"token": "tok-secret-1"},
		"steps": []map[string]any{{
			"id":      "s1",
			"request": map[string]any{"method": "GET", "url": origin.URL + "/start?key={{token}}"},
		}},
	})
	result := NewSyntheticAPIChecker(false, nil).Check(context.Background(), config, 5)
	if result.Status != "error" || result.ErrorMessage == nil || !strings.Contains(*result.ErrorMessage, "carries a secret") {
		t.Fatalf("status %s, error %v; want a redirect refusal", result.Status, result.ErrorMessage)
	}
	if reached || referer != "" {
		t.Fatal("the other origin was contacted")
	}
	if strings.Contains(string(result.MetricsData)+*result.ErrorMessage, "tok-secret-1") {
		t.Fatalf("result leaks the secret: %s", result.MetricsData)
	}

	// The same step with the secret in a header only: followed, without it
	// and without the Referer that would carry the origin URL.
	config, _ = json.Marshal(map[string]any{
		"secret_variables": map[string]string{"token": "tok-secret-1"},
		"steps": []map[string]any{{
			"id":      "s1",
			"request": map[string]any{"method": "GET", "url": origin.URL + "/start?q=1", "headers": map[string]string{"X-Session": "{{token}}"}},
		}},
	})
	result = NewSyntheticAPIChecker(false, nil).Check(context.Background(), config, 5)
	if result.Status != "success" || !reached || referer != "" {
		t.Fatalf("status %s (%v), reached %v, referer %q", result.Status, result.ErrorMessage, reached, referer)
	}
}
