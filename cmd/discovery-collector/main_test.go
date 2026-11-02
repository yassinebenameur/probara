package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
	require.Zero(t, he.RetryAfter, "only 429/503 carry a retry delay")
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
