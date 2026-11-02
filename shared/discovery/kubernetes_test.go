package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCollectPaginationAndAnnotations(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer scoped-token", r.Header.Get("Authorization"))
		requests++
		switch r.URL.Path {
		case "/api/v1/namespaces/prod/services":
			if r.URL.Query().Get("continue") == "next" {
				_, _ = w.Write([]byte(`{"metadata":{},"items":[{"metadata":{"uid":"udp","name":"dns","namespace":"prod"},"spec":{"ports":[{"port":53,"protocol":"UDP"}]}}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"metadata":{"continue":"next"},"items":[{"metadata":{"uid":"api","name":"payments","namespace":"prod","annotations":{"probara.io/health-path":"/readyz","probara.io/service":"payments"}},"spec":{"ports":[{"port":8080,"protocol":"TCP"}]}}]}`))
		case "/apis/networking.k8s.io/v1/namespaces/prod/ingresses":
			_, _ = w.Write([]byte(`{"metadata":{},"items":[{"metadata":{"uid":"ingress","name":"payments","namespace":"prod","annotations":{"probara.io/service":"payments"}},"spec":{"rules":[{"host":"payments.example.com"},{"host":"*.example.com"}],"tls":[{"hosts":["payments.example.com"]}]}}]}`))
		default:
			t.Errorf("unexpected URL %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	k := Kubernetes{BaseURL: server.URL, Client: server.Client(), Token: func() (string, error) { return "scoped-token", nil }}
	snap, err := k.Collect(context.Background(), Config{Namespaces: []string{"prod"}, Revision: 4})
	require.NoError(t, err)
	require.Equal(t, 3, requests)
	require.Len(t, snap.Resources, 3)
	require.Equal(t, "http://payments.prod.svc:8080/readyz", snap.Resources[0].Suggestions[0].Target)
	require.True(t, snap.Resources[0].Suggestions[0].Internal)
	require.Empty(t, snap.Resources[1].Suggestions)
	require.Equal(t, "https://payments.example.com/", snap.Resources[2].Suggestions[0].Target)
	require.Equal(t, snap.Resources[0].ServiceKey(), snap.Resources[2].ServiceKey())
}
func TestCollectFailsWholeScan(t *testing.T) {
	for _, status := range []int{403, 410, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/namespaces/prod/services" {
					_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
					return
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			_, err := (Kubernetes{BaseURL: server.URL, Client: server.Client(), Token: func() (string, error) { return "token", nil }}).Collect(context.Background(), Config{Namespaces: []string{"prod"}})
			require.Error(t, err)
		})
	}
}
func TestSnapshotRejectsPartialOrUnsafeTargets(t *testing.T) {
	good := Snapshot{Namespaces: []string{"prod"}, Resources: []Resource{{UID: "1", Kind: "Ingress", Name: "web", Namespace: "prod", Suggestions: []Suggestion{{Type: "http", Target: "https://example.com/"}}}}}
	require.NoError(t, good.Validate([]string{"prod"}, nil))
	require.NotEmpty(t, good.Resources[0].Suggestions[0].Key)
	require.Error(t, good.Validate([]string{"prod", "staging"}, nil))
	for _, target := range []string{"file:///etc/passwd", "https://user:pass@example.com/", "https://example.com/?token=secret", "//example.com"} {
		good.Resources[0].Suggestions[0].Target = target
		require.Error(t, good.Validate([]string{"prod"}, nil))
	}
}

func TestMalformedAnnotationsDoNotPoisonInventory(t *testing.T) {
	for _, path := range []string{"/bad%escape", "/" + strings.Repeat("x", 2048), "/ready\n", "//other.example/", "/ready?secret=x"} {
		t.Run(path[:min(len(path), 30)], func(t *testing.T) {
			var obj kubeObject
			require.NoError(t, json.Unmarshal([]byte(`{"metadata":{"uid":"api","name":"api","namespace":"prod"},"spec":{"ports":[{"port":8080}]}}`), &obj))
			obj.Metadata.Annotations = map[string]string{"probara.io/health-path": path, "probara.io/service": strings.Repeat("x", 101)}
			resource := resourceFromObject("Service", obj)
			require.Empty(t, resource.Service)
			require.Len(t, resource.Suggestions, 1)
			require.Equal(t, "tcp", resource.Suggestions[0].Type)
			snapshot := Snapshot{Namespaces: []string{"prod"}, Resources: []Resource{resource}}
			require.NoError(t, snapshot.Validate([]string{"prod"}, nil))
		})
	}
}

func TestSnapshotDerivesInternalReachability(t *testing.T) {
	snap := Snapshot{Namespaces: []string{"prod"}, Resources: []Resource{
		{UID: "1", Kind: "Service", Name: "redis", Namespace: "prod", Suggestions: []Suggestion{{Type: "tcp", Target: "redis.prod.svc", Port: 6379, Internal: false}}},
		{UID: "2", Kind: "Ingress", Name: "web", Namespace: "prod", Suggestions: []Suggestion{
			{Type: "http", Target: "https://pay.example.com/", Internal: true},
			{Type: "http", Target: "http://intranet/"},
			{Type: "http", Target: "http://10.0.0.5/"},
		}},
	}}
	require.NoError(t, snap.Validate([]string{"prod"}, nil))
	require.True(t, snap.Resources[0].Suggestions[0].Internal, "a Service target is always internal, whatever the collector claims")
	require.False(t, snap.Resources[1].Suggestions[0].Internal)
	require.True(t, snap.Resources[1].Suggestions[1].Internal)
	require.True(t, snap.Resources[1].Suggestions[2].Internal)
	forged := Suggestion{Type: "tcp", Target: "redis.prod.svc", Port: 6379}
	require.NotEqual(t, forged.Identity(), snap.Resources[0].Suggestions[0].Key, "the key is recomputed from the derived flag")
}

func TestSnapshotRejectsForeignOrMalformedTargets(t *testing.T) {
	for name, r := range map[string]Resource{
		"service targets another host": {Kind: "Service", Name: "redis", Suggestions: []Suggestion{{Type: "tcp", Target: "postgres.kube-system.svc", Port: 5432}}},
		"service http to another host": {Kind: "Service", Name: "web", Suggestions: []Suggestion{{Type: "http", Target: "http://db.prod.svc:80/"}}},
		"ingress tcp suggestion":       {Kind: "Ingress", Name: "web", Suggestions: []Suggestion{{Type: "tcp", Target: "web.prod.svc", Port: 80}}},
		"ingress target with a path":   {Kind: "Ingress", Name: "web", Suggestions: []Suggestion{{Type: "http", Target: "https://pay.example.com/admin"}}},
		"ingress target with a port":   {Kind: "Ingress", Name: "web", Suggestions: []Suggestion{{Type: "http", Target: "https://pay.example.com:8443/"}}},
	} {
		t.Run(name, func(t *testing.T) {
			r.UID, r.Namespace = "1", "prod"
			snap := Snapshot{Namespaces: []string{"prod"}, Resources: []Resource{r}}
			require.Error(t, snap.Validate([]string{"prod"}, nil))
		})
	}
}

func TestIngressWildcardTLSSelectsHTTPS(t *testing.T) {
	var obj kubeObject
	require.NoError(t, json.Unmarshal([]byte(`{"metadata":{"uid":"i","name":"web","namespace":"prod"},"spec":{"rules":[{"host":"api.example.com"},{"host":"example.com"},{"host":"a.b.example.com"},{"host":"API.other.com"}],"tls":[{"hosts":["*.example.com","api.other.com"]}]}}`), &obj))
	r := resourceFromObject("Ingress", obj)
	targets := []string{}
	for _, s := range r.Suggestions {
		targets = append(targets, s.Target)
	}
	require.Equal(t, []string{"https://api.example.com/", "http://example.com/", "http://a.b.example.com/"}, targets[:3])
	snap := Snapshot{Namespaces: []string{"prod"}, Resources: []Resource{r}}
	require.NoError(t, snap.Validate([]string{"prod"}, nil))
}

// fakeCluster serves Services and Ingresses per namespace. A namespace in
// forbid (kind "" = both) answers 403; big serves n Services in it.
type fakeCluster struct {
	t        *testing.T
	forbid   map[string]string
	big      map[string]int
	slow     map[string]time.Duration
	requests []string
}

func (f *fakeCluster) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests = append(f.requests, r.URL.Path)
	var ns, kind string
	if rest, ok := strings.CutPrefix(r.URL.Path, "/api/v1/namespaces/"); ok {
		ns, kind = strings.TrimSuffix(rest, "/services"), "Service"
	} else if rest, ok := strings.CutPrefix(r.URL.Path, "/apis/networking.k8s.io/v1/namespaces/"); ok {
		ns, kind = strings.TrimSuffix(rest, "/ingresses"), "Ingress"
	}
	if d := f.slow[ns]; d > 0 {
		select {
		case <-time.After(d):
		case <-r.Context().Done():
			return
		}
	}
	if k, ok := f.forbid[ns]; ok && (k == "" || k == kind) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	items := []map[string]any{}
	n := 1
	if kind == "Service" && f.big[ns] > 0 {
		n = f.big[ns]
	}
	for i := range n {
		name := fmt.Sprintf("%s-%d", strings.ToLower(kind), i)
		items = append(items, map[string]any{"metadata": map[string]any{"uid": ns + "/" + kind + "/" + name, "name": name, "namespace": ns}, "spec": map[string]any{}})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{}, "items": items})
}

func collectFrom(t *testing.T, f *fakeCluster, ctx context.Context, cfg Config, reserve time.Duration) (Snapshot, error) {
	t.Helper()
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	k := Kubernetes{BaseURL: server.URL, Client: server.Client(), Token: func() (string, error) { return "token", nil }, Reserve: reserve}
	return k.Collect(ctx, cfg)
}

func namespacesOf(s Snapshot) map[string]int {
	out := map[string]int{}
	for _, r := range s.Resources {
		out[r.Namespace]++
	}
	return out
}

func TestCollectPendingFailuresAreSkippedNotFatal(t *testing.T) {
	f := &fakeCluster{t: t,
		forbid: map[string]string{"locked": "", "half": "Ingress"},
		big:    map[string]int{"huge": MaxResources},
	}
	snap, err := collectFrom(t, f, context.Background(), Config{Namespaces: []string{"prod"}, Pending: []string{"locked", "half", "huge", "fresh"}, Revision: 2}, 0)
	require.NoError(t, err)
	require.Equal(t, []string{"prod", "fresh"}, snap.Namespaces)
	require.Equal(t, map[string]string{"locked": SkipForbidden, "half": SkipForbidden, "huge": SkipTooLarge}, snap.Skipped)
	require.Equal(t, map[string]int{"prod": 2, "fresh": 2}, namespacesOf(snap), "a skipped namespace leaves no resources, even the Services listed before its Ingresses were refused")
	require.Equal(t, "/api/v1/namespaces/prod/services", f.requests[0], "active namespaces are scanned first")
}

func TestCollectActiveFailureStillAbortsWithPending(t *testing.T) {
	f := &fakeCluster{t: t, forbid: map[string]string{"prod": "Ingress"}}
	_, err := collectFrom(t, f, context.Background(), Config{Namespaces: []string{"prod"}, Pending: []string{"fresh"}}, 0)
	require.ErrorContains(t, err, "HTTP 403")
	for _, p := range f.requests {
		require.NotContains(t, p, "fresh", "nothing pending is scanned once an active namespace failed")
	}
}

func TestCollectPendingKeepsReserveForUpload(t *testing.T) {
	// Less than the reserve left: pending namespaces are not even listed.
	f := &fakeCluster{t: t}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	snap, err := collectFrom(t, f, ctx, Config{Namespaces: []string{"prod"}, Pending: []string{"a", "b"}}, 0)
	require.NoError(t, err)
	require.Equal(t, []string{"prod"}, snap.Namespaces)
	require.Equal(t, map[string]string{"a": SkipDeadline, "b": SkipDeadline}, snap.Skipped)
	require.Len(t, f.requests, 2)

	// A pending namespace that runs into the reserve is dropped, and so is
	// every one after it; the active inventory is kept.
	f = &fakeCluster{t: t, slow: map[string]time.Duration{"slow": 5 * time.Second}}
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snap, err = collectFrom(t, f, ctx, Config{Namespaces: []string{"prod"}, Pending: []string{"slow", "after"}}, 1500*time.Millisecond)
	require.NoError(t, err)
	require.Equal(t, []string{"prod"}, snap.Namespaces)
	require.Equal(t, map[string]string{"slow": SkipDeadline, "after": SkipDeadline}, snap.Skipped)
	require.Equal(t, map[string]int{"prod": 2}, namespacesOf(snap))
}

func TestSnapshotValidateActiveAndPending(t *testing.T) {
	res := func(ns string) Resource {
		return Resource{UID: ns, Kind: "Ingress", Name: "web", Namespace: ns, Suggestions: []Suggestion{}}
	}
	active, pending := []string{"prod"}, []string{"new", "other"}
	for name, tc := range map[string]struct {
		snap Snapshot
		ok   bool
	}{
		"active only":                 {Snapshot{Namespaces: []string{"prod"}, Resources: []Resource{res("prod")}}, true},
		"pending scanned":             {Snapshot{Namespaces: []string{"prod", "new"}, Skipped: map[string]string{"other": SkipForbidden}, Resources: []Resource{res("prod"), res("new")}}, true},
		"active missing":              {Snapshot{Namespaces: []string{"new"}, Resources: []Resource{}}, false},
		"unconfigured namespace":      {Snapshot{Namespaces: []string{"prod", "gone"}, Resources: []Resource{}}, false},
		"duplicate namespace":         {Snapshot{Namespaces: []string{"prod", "prod"}, Resources: []Resource{}}, false},
		"active skipped":              {Snapshot{Namespaces: []string{"prod"}, Skipped: map[string]string{"prod": SkipForbidden}, Resources: []Resource{}}, false},
		"scanned and skipped":         {Snapshot{Namespaces: []string{"prod", "new"}, Skipped: map[string]string{"new": SkipForbidden}, Resources: []Resource{}}, false},
		"unconfigured skipped":        {Snapshot{Namespaces: []string{"prod"}, Skipped: map[string]string{"gone": SkipForbidden}, Resources: []Resource{}}, false},
		"resource in skipped":         {Snapshot{Namespaces: []string{"prod"}, Skipped: map[string]string{"new": SkipForbidden}, Resources: []Resource{res("new")}}, false},
		"resource in unscanned":       {Snapshot{Namespaces: []string{"prod"}, Resources: []Resource{res("other")}}, false},
		"pending neither (older cfg)": {Snapshot{Namespaces: []string{"prod"}, Resources: []Resource{}}, true},
	} {
		t.Run(name, func(t *testing.T) {
			err := tc.snap.Validate(active, pending)
			if tc.ok {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
	snap := Snapshot{Namespaces: []string{"prod"}, Skipped: map[string]string{"new": "quota_exceeded", "other": SkipInventoryLimit}, Resources: []Resource{}}
	require.NoError(t, snap.Validate(active, pending))
	require.Equal(t, map[string]string{"new": SkipError, "other": SkipError}, snap.Skipped, "unknown reasons, and the API-only inventory_limit, are stored as error")
}
