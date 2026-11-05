package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	result := NewSyntheticAPIChecker().Check(context.Background(), config, 5)
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

func TestRedirectLeavesOrigin(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{"https://h/a", "https://h/b", false},
		{"http://h/a", "https://h/b", false},
		{"https://h/a", "http://h/b", true}, // basic auth over https must not downgrade
		{"https://h/a", "https://other/b", true},
		{"https://h/a", "https://h:8443/b", true},
		{"https://H/a", "https://h/b", false},
	}
	for _, tc := range cases {
		from, _ := url.Parse(tc.from)
		to, _ := url.Parse(tc.to)
		if got := redirectLeavesOrigin(from, to); got != tc.want {
			t.Errorf("redirectLeavesOrigin(%s, %s) = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}
