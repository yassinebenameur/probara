package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestWaitForAlertEventsTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := time.Now()
		h := &Handler{router: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/alerts/stream" {
				t.Fatalf("unexpected stream path: %s", r.URL.Path)
			}
			deadline, ok := r.Context().Deadline()
			if !ok || deadline.Sub(started) != time.Second {
				t.Fatalf("stream deadline = %v, want one second after start", deadline)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: connected\ndata: {\"status\":\"connected\"}\n\n"))
			<-r.Context().Done()
			if err := r.Context().Err(); err != context.DeadlineExceeded {
				t.Fatalf("stream ended with %v, want deadline exceeded", err)
			}
		})}
		result, err := h.waitForAlertEvents(t.Context(), &mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: json.RawMessage(`{"timeout_seconds":1}`)},
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError {
			t.Fatalf("unexpected tool error: %+v", result.Content)
		}
		if got := result.Content[0].(*mcp.TextContent).Text; got != `{"events":[],"listened_seconds":1}` {
			t.Fatalf("unexpected result: %s", got)
		}
		if elapsed := time.Since(started); elapsed != time.Second {
			t.Fatalf("waited %s, want one second", elapsed)
		}
	})
}

// describe_monitor_type shows agents a test call's call.media fields.
func TestDescribeSIPMonitorTypeShowsMedia(t *testing.T) {
	info, err := typeInfo("sip", true)
	if err != nil {
		t.Fatal(err)
	}
	call := info.ConfigSchema.Properties["call"]
	if call == nil || call.Properties["media"] == nil {
		t.Fatalf("schema = %+v, want call.media", info.ConfigSchema)
	}
	media := call.Properties["media"]
	for _, field := range []string{"expect", "max_loss_pct", "min_mos", "max_jitter_ms", "max_rtt_ms"} {
		if media.Properties[field] == nil {
			t.Errorf("call.media has no %q in %+v", field, media.Properties)
		}
	}
}
