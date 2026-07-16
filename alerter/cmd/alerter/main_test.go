package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yassinebenameur/probara/shared/queue"
)

// Liveness must fail once the NATS connection is permanently closed so
// Kubernetes restarts the pod instead of leaving a zombie alerter.
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

// NATS is optional for the alerter: a nil client must not fail liveness.
func TestHealthzHandlerNilClient(t *testing.T) {
	handler := healthzHandler(nil)

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz with nil NATS client = %d, want %d", rec.Code, http.StatusOK)
	}
}
