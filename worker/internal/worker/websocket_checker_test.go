package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

// newWSEchoServer starts a WebSocket server that records handshake headers
// and echoes every received text frame back, prefixed with "echo: ".
func newWSEchoServer(t *testing.T, onHeader func(http.Header)) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if onHeader != nil {
			onHeader(r.Header)
		}
		conn, _, _, err := ws.UpgradeHTTP(r, w)
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			for {
				msg, err := wsutil.ReadClientText(conn)
				if err != nil {
					return
				}
				if err := wsutil.WriteServerText(conn, append([]byte("echo: "), msg...)); err != nil {
					return
				}
			}
		}()
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func TestWebSocketChecker_InvalidConfig(t *testing.T) {
	c := NewWebSocketChecker(false, nil)
	result := c.Check(context.Background(), json.RawMessage(`{`), 5)
	if result.Status != "error" {
		t.Fatalf("status = %s, want error", result.Status)
	}
}

func TestWebSocketChecker_InvalidURL(t *testing.T) {
	c := NewWebSocketChecker(false, nil)
	result := c.Check(context.Background(), json.RawMessage(`{"url":"http://example.com"}`), 5)
	if result.Status != "error" || !strings.Contains(*result.ErrorMessage, "ws://") {
		t.Fatalf("result = %+v, want scheme error", result)
	}
}

func TestWebSocketChecker_HandshakeSuccess(t *testing.T) {
	var gotAuth string
	url := newWSEchoServer(t, func(h http.Header) { gotAuth = h.Get("Authorization") })

	c := NewWebSocketChecker(false, nil)
	config := fmt.Sprintf(`{"url":"%s","headers":{"Authorization":"Bearer tok"}}`, url)
	result := c.Check(context.Background(), json.RawMessage(config), 5)
	if result.Status != "success" {
		t.Fatalf("result = %+v, want success", result)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("Authorization header = %q, want Bearer tok", gotAuth)
	}
	if result.LatencyMs == nil {
		t.Fatal("latency not recorded")
	}
}

func TestWebSocketChecker_SendExpect(t *testing.T) {
	url := newWSEchoServer(t, nil)
	c := NewWebSocketChecker(false, nil)

	config := fmt.Sprintf(`{"url":"%s","send_message":"ping","expected_substring":"echo: ping"}`, url)
	result := c.Check(context.Background(), json.RawMessage(config), 5)
	if result.Status != "success" {
		t.Fatalf("result = %+v, want success", result)
	}

	config = fmt.Sprintf(`{"url":"%s","send_message":"ping","expected_substring":"nope"}`, url)
	result = c.Check(context.Background(), json.RawMessage(config), 5)
	if result.Status != "failure" || !strings.Contains(*result.ErrorMessage, "expected_substring") {
		t.Fatalf("result = %+v, want expected_substring failure", result)
	}
}

func TestWebSocketChecker_ConnectionRefused(t *testing.T) {
	c := NewWebSocketChecker(false, nil)
	config := fmt.Sprintf(`{"url":"ws://127.0.0.1:%d"}`, closedPort(t))
	result := c.Check(context.Background(), json.RawMessage(config), 2)
	// Refused/timeout means the target is down (failure), matching the tcp checker.
	if result.Status != "failure" {
		t.Fatalf("status = %s, want failure", result.Status)
	}
}

func TestWebSocketChecker_UpgradeRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no websocket here", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	c := NewWebSocketChecker(false, nil)
	config := fmt.Sprintf(`{"url":"ws%s"}`, strings.TrimPrefix(srv.URL, "http"))
	result := c.Check(context.Background(), json.RawMessage(config), 5)
	if result.Status != "failure" {
		t.Fatalf("result = %+v, want failure", result)
	}
}

func TestWebSocketChecker_SSRFBlocked(t *testing.T) {
	c := NewWebSocketChecker(true, nil)
	result := c.Check(context.Background(), json.RawMessage(`{"url":"ws://127.0.0.1:8080"}`), 2)
	if result.Status != "error" || !strings.Contains(*result.ErrorMessage, "ssrf_blocked") {
		t.Fatalf("result = %+v, want ssrf_blocked error", result)
	}
}

func TestWebSocketChecker_MetricsEnvelope(t *testing.T) {
	url := newWSEchoServer(t, nil)
	c := NewWebSocketChecker(false, nil)
	config := fmt.Sprintf(`{"url":"%s"}`, url)
	result := c.Check(context.Background(), json.RawMessage(config), 5)
	if result.Status != "success" {
		t.Fatalf("result = %+v, want success", result)
	}
	if !strings.Contains(string(result.MetricsData), "websocket") {
		t.Fatalf("metrics = %s, want websocket envelope", result.MetricsData)
	}
}
