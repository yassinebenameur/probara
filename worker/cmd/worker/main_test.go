package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yassinebenameur/probara/shared/queue"
)

// Liveness must fail once the NATS connection is permanently closed so
// Kubernetes restarts the pod instead of leaving a zombie that can never
// consume jobs again.
func TestHealthzHandler(t *testing.T) {
	client, err := queue.NewClient("nats://127.0.0.1:59999")
	if err != nil {
		t.Fatalf("queue.NewClient: %v", err)
	}

	handler := healthzHandler(client)

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz while reconnecting = %d, want %d", rec.Code, http.StatusOK)
	}

	client.Close()

	rec = httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("healthz after permanent close = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}
