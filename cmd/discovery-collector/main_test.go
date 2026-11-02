package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yassinebenameur/probara/shared/discovery"
)

func TestPlatformBaseAcceptsOnlyHTTPSOrigins(t *testing.T) {
	for raw, want := range map[string]string{
		"https://probara.example.com":       "https://probara.example.com",
		"https://probara.example.com/":      "https://probara.example.com",
		"https://probara.example.com:8443/": "https://probara.example.com:8443",
	} {
		got, err := platformBase(raw)
		require.NoError(t, err, raw)
		require.Equal(t, want, got)
	}
	for _, raw := range []string{
		"",
		"http://probara.example.com",
		"https://",
		"https://probara.example.com/api",
		"https://user:pass@probara.example.com",
		"https://probara.example.com?x=1",
		"https://probara.example.com#frag",
		"ftp://probara.example.com",
		"probara.example.com",
	} {
		_, err := platformBase(raw)
		require.Error(t, err, raw)
	}
}

func client(srv *httptest.Server) *http.Client {
	c := srv.Client()
	c.CheckRedirect = noRedirect
	return c
}

func TestPlatformRequestSendsTokenAndReturnsBody(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer secret", r.Header.Get("Authorization"))
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		body, _ := io.ReadAll(r.Body)
		_, _ = w.Write(append([]byte("echo:"), body...))
	}))
	defer srv.Close()
	b, err := platformRequest(context.Background(), client(srv), http.MethodPost, srv.URL+"/snapshot", "secret", []byte(`{}`))
	require.NoError(t, err)
	require.Equal(t, "echo:{}", string(b))
}

func TestPlatformRequestRefusesRedirects(t *testing.T) {
	var leaked bool
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("Authorization") != ""
	}))
	defer other.Close()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	_, err := platformRequest(context.Background(), client(srv), http.MethodGet, srv.URL+"/config", "secret", nil)
	var he *httpError
	require.ErrorAs(t, err, &he)
	require.Equal(t, http.StatusTemporaryRedirect, he.Status)
	require.False(t, leaked, "the token must not follow a redirect")
}

func TestPlatformRequestErrorCarriesBoundedBody(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"validation_error","message":"invalid resource metadata"}` + strings.Repeat("x", 2000)))
	}))
	defer srv.Close()
	_, err := platformRequest(context.Background(), client(srv), http.MethodPost, srv.URL+"/snapshot", "secret", []byte(`{}`))
	var he *httpError
	require.ErrorAs(t, err, &he)
	require.Equal(t, http.StatusBadRequest, he.Status)
	require.Len(t, he.Body, maxErrorBody)
	require.Contains(t, err.Error(), "HTTP 400: ")
	require.Contains(t, err.Error(), "invalid resource metadata")
	require.Zero(t, he.RetryAfter, "only 409/429/503 carry a retry delay")
	require.Equal(t, scanInterval, nextDelay(err))
}

func TestPlatformRequestHonorsRetryAfter(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "42")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	_, err := platformRequest(context.Background(), client(srv), http.MethodPost, srv.URL+"/snapshot", "secret", []byte(`{}`))
	require.Equal(t, 42*time.Second, nextDelay(err))
	require.Equal(t, "platform returned HTTP 429", err.Error())
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	for h, want := range map[string]time.Duration{
		"30":                            30 * time.Second,
		" 5 ":                           5 * time.Second,
		"0":                             time.Second,
		"999999":                        maxRetryAfter,
		"Mon, 02 Nov 2026 12:01:30 GMT": 90 * time.Second,
		"Mon, 02 Nov 2026 11:00:00 GMT": time.Second,
	} {
		got, ok := parseRetryAfter(h, now)
		require.True(t, ok, h)
		require.Equal(t, want, got, h)
	}
	for _, h := range []string{"", "-5", "soon", "1.5"} {
		_, ok := parseRetryAfter(h, now)
		require.False(t, ok, h)
	}
}

func TestNextDelayDefaultsToScanInterval(t *testing.T) {
	require.Equal(t, scanInterval, nextDelay(nil))
	require.Equal(t, scanInterval, nextDelay(errors.New("dial tcp: refused")))
	require.Less(t, time.Minute, scanInterval, "the API's one-minute snapshot floor sits below the scan interval")
}

func TestEncodeSnapshotEnforcesPayloadCap(t *testing.T) {
	small := discovery.Snapshot{Namespaces: []string{"prod"}, Resources: []discovery.Resource{}}
	b, err := encodeSnapshot(small)
	require.NoError(t, err)
	require.JSONEq(t, `{"revision":0,"namespaces":["prod"],"resources":[]}`, string(b))
	big := discovery.Snapshot{Namespaces: []string{"prod"}, Resources: []discovery.Resource{}}
	for i := range 2000 {
		big.Resources = append(big.Resources, discovery.Resource{UID: strings.Repeat("u", 100) + string(rune('a'+i%26)), Kind: "Ingress", Name: strings.Repeat("n", 253), Namespace: "prod",
			Suggestions: []discovery.Suggestion{{Type: "http", Target: "https://" + strings.Repeat("h", 2000) + "/", Reason: "Ingress hostname"}}})
	}
	_, err = encodeSnapshot(big)
	require.ErrorContains(t, err, "exceeds 4 MiB")
}

func TestPlatformRequestHonorsRetryAfterOnConflict(t *testing.T) {
	// The API answers a snapshot that raced a namespace removal with 409 and
	// Retry-After: 10, so the next cycle re-reads /config within seconds.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "10")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"conflict"}`))
	}))
	defer srv.Close()
	_, err := platformRequest(context.Background(), client(srv), http.MethodPost, srv.URL+"/snapshot", "secret", []byte(`{}`))
	var he *httpError
	require.ErrorAs(t, err, &he)
	require.Equal(t, http.StatusConflict, he.Status)
	require.Equal(t, 10*time.Second, nextDelay(err))

	// A 409 without the header (a revision conflict) waits the scan interval.
	plain := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer plain.Close()
	_, err = platformRequest(context.Background(), client(plain), http.MethodPost, plain.URL+"/snapshot", "secret", []byte(`{}`))
	require.Equal(t, scanInterval, nextDelay(err))
}

func TestEncodeSnapshotCarriesSkippedNamespaces(t *testing.T) {
	b, err := encodeSnapshot(discovery.Snapshot{Namespaces: []string{"prod"}, Skipped: map[string]string{"new": discovery.SkipForbidden}, Resources: []discovery.Resource{}})
	require.NoError(t, err)
	require.JSONEq(t, `{"revision":0,"namespaces":["prod"],"skipped":{"new":"forbidden"},"resources":[]}`, string(b))
}

// heavyResources builds n Services in ns whose suggestions make each one
// about 20 KB of JSON, so a few hundred of them exceed the upload cap while
// staying far under the 2,000-resource budget.
func heavyResources(ns string, n int) []discovery.Resource {
	out := make([]discovery.Resource, 0, n)
	for i := range n {
		r := discovery.Resource{UID: ns + "-" + strconv.Itoa(i), Kind: "Service", Name: "svc" + strconv.Itoa(i), Namespace: ns}
		for p := range 10 {
			r.Suggestions = append(r.Suggestions, discovery.Suggestion{Type: "http", Target: "http://" + r.Name + "." + ns + ".svc:" + strconv.Itoa(8000+p) + "/" + strings.Repeat("p", 1990), Reason: "Explicit probara.io/health-path annotation"})
		}
		out = append(out, r)
	}
	return out
}

func TestFitSnapshotSkipsPendingNamespaceOverUploadCap(t *testing.T) {
	// A pending namespace under the resource budget can still push the JSON
	// past 4 MiB. It is dropped as too_large, in scan order, and the active
	// namespaces and a smaller pending one after it still upload.
	s := discovery.Snapshot{Revision: 3, Namespaces: []string{"prod", "big", "small"}, Skipped: map[string]string{"locked": discovery.SkipForbidden}}
	s.Resources = append(s.Resources, heavyResources("prod", 5)...)
	s.Resources = append(s.Resources, heavyResources("big", 250)...)
	s.Resources = append(s.Resources, heavyResources("small", 2)...)
	_, err := encodeSnapshot(s)
	require.ErrorIs(t, err, errPayloadTooLarge, "fixture must exceed the cap")

	b, fitted, err := fitSnapshot(s, []string{"prod"})
	require.NoError(t, err)
	require.LessOrEqual(t, len(b), maxPayload)
	require.Equal(t, []string{"prod", "small"}, fitted.Namespaces)
	require.Equal(t, map[string]string{"locked": discovery.SkipForbidden, "big": discovery.SkipTooLarge}, fitted.Skipped)
	require.Len(t, fitted.Resources, 7)
	for _, r := range fitted.Resources {
		require.NotEqual(t, "big", r.Namespace)
	}
	require.NoError(t, fitted.Validate([]string{"prod"}, []string{"big", "small", "locked"}))
	var sent discovery.Snapshot
	require.NoError(t, json.Unmarshal(b, &sent))
	require.Equal(t, fitted.Namespaces, sent.Namespaces)
	require.Equal(t, fitted.Skipped, sent.Skipped, "the uploaded bytes carry the final skip list")
	require.Len(t, sent.Resources, 7)
}

func TestFitSnapshotPassesSnapshotsUnderTheCapThrough(t *testing.T) {
	s := discovery.Snapshot{Namespaces: []string{"prod", "new"}, Resources: append(heavyResources("prod", 2), heavyResources("new", 2)...)}
	b, fitted, err := fitSnapshot(s, []string{"prod"})
	require.NoError(t, err)
	require.Equal(t, []string{"prod", "new"}, fitted.Namespaces)
	require.Nil(t, fitted.Skipped)
	want, err := encodeSnapshot(s)
	require.NoError(t, err)
	require.Equal(t, want, b)
}

func TestFitSnapshotFailsWhenActiveNamespacesAloneExceedTheCap(t *testing.T) {
	s := discovery.Snapshot{Namespaces: []string{"prod", "new"}, Resources: append(heavyResources("prod", 250), heavyResources("new", 1)...)}
	_, _, err := fitSnapshot(s, []string{"prod"})
	require.ErrorIs(t, err, errPayloadTooLarge)
}
