package alerts

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/middleware"
	alertservice "github.com/yassinebenameur/probara/api/internal/services/alerts"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

func streamRequest(ctx context.Context) *http.Request {
	tenant := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/alerts/stream", nil)
	return req.WithContext(ctxpkg.WithTenantID(ctx, tenant.String()))
}

// serveStream runs the stream until it ends on its own; it fails the test if
// the stream outlives limit.
func serveStream(t *testing.T, req *http.Request, limit time.Duration) string {
	t.Helper()
	h := NewHandlers(nil, alertservice.NewHub(), logger.New("test", "error"))
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.StreamAlerts(rec, req)
		close(done)
	}()
	select {
	case <-done:
		return rec.Body.String()
	case <-time.After(limit):
		t.Fatal("stream still open")
		return ""
	}
}

func shortHeartbeat(t *testing.T) {
	prev := streamHeartbeat
	streamHeartbeat = 20 * time.Millisecond
	t.Cleanup(func() { streamHeartbeat = prev })
}

// A credential revoked while the stream is open closes it at the next
// heartbeat instead of streaming the tenant's alerts to it indefinitely.
func TestAlertStreamClosesWhenCredentialIsRevoked(t *testing.T) {
	shortHeartbeat(t)
	var revoked atomic.Bool
	var checks atomic.Int32
	ctx := middleware.WithCredentialCheck(context.Background(), func(context.Context) error {
		checks.Add(1)
		if revoked.Load() {
			return middleware.ErrCredentialRevoked
		}
		return nil
	}, time.Time{})
	go func() {
		time.Sleep(100 * time.Millisecond)
		revoked.Store(true)
	}()
	body := serveStream(t, streamRequest(ctx), 5*time.Second)
	if checks.Load() < 2 {
		t.Fatalf("credential checked %d times, want once per heartbeat", checks.Load())
	}
	if !strings.Contains(body, "event: heartbeat") || strings.Contains(body, "event: reauth") {
		t.Fatalf("body = %q, want heartbeats then a close without reauth", body)
	}
}

// The stream ends at the credential's expiry with a reauth event, so the
// client reconnects with its refreshed session.
func TestAlertStreamEndsAtCredentialExpiry(t *testing.T) {
	ctx := middleware.WithCredentialCheck(context.Background(), func(context.Context) error { return nil },
		time.Now().Add(150*time.Millisecond))
	body := serveStream(t, streamRequest(ctx), 5*time.Second)
	if !strings.HasSuffix(body, "event: reauth\ndata: {}\n\n") {
		t.Fatalf("body = %q, want it to end with a reauth event", body)
	}
}
