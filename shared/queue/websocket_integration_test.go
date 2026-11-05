//go:build !unit

package queue

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
)

// A location worker reaches the broker's websocket listener through a reverse
// proxy that routes only one path, as a path-based ingress on the shared app
// host does: the client must send the URL's path in the upgrade request.
func TestNewClientOverWebsocketBehindPathProxy(t *testing.T) {
	srv, err := server.NewServer(&server.Options{
		Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoLog: true, NoSigs: true,
		Websocket: server.WebsocketOpts{Host: "127.0.0.1", Port: -1, NoTLS: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats server not ready")
	}
	t.Cleanup(srv.Shutdown)

	backend, err := url.Parse(strings.Replace(srv.WebsocketURL(), "ws://", "http://", 1))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/nats", httputil.NewSingleHostReverseProxy(backend))
	proxy := httptest.NewServer(mux)
	t.Cleanup(proxy.Close)

	c, err := NewClient(strings.Replace(proxy.URL, "http://", "ws://", 1) + "/nats")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for c.HealthCheck(ctx) != nil {
		if ctx.Err() != nil {
			t.Fatal("client never connected through the path-routed websocket proxy")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := c.EnsureStream(ctx, "WS_TEST", []string{"ws.test"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Publish(ctx, "ws.test", []byte(`{}`), nil); err != nil {
		t.Fatalf("publish over websocket: %v", err)
	}
}
