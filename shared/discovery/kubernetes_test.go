package discovery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	require.NoError(t, good.Validate([]string{"prod"}))
	require.NotEmpty(t, good.Resources[0].Suggestions[0].Key)
	require.Error(t, good.Validate([]string{"prod", "staging"}))
	for _, target := range []string{"file:///etc/passwd", "https://user:pass@example.com/", "https://example.com/?token=secret", "//example.com"} {
		good.Resources[0].Suggestions[0].Target = target
		require.Error(t, good.Validate([]string{"prod"}))
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
			require.NoError(t, snapshot.Validate([]string{"prod"}))
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
	require.NoError(t, snap.Validate([]string{"prod"}))
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
			require.Error(t, snap.Validate([]string{"prod"}))
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
	require.NoError(t, snap.Validate([]string{"prod"}))
}
