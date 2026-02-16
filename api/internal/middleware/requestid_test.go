package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestIDMiddleware_UsesExistingHeader(t *testing.T) {
	var gotRequestID string

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequestID = GetRequestID(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "req-123")
	recorder := httptest.NewRecorder()

	RequestIDMiddleware(handler).ServeHTTP(recorder, req)

	if gotRequestID != "req-123" {
		t.Fatalf("expected request id req-123, got %q", gotRequestID)
	}
	if recorder.Header().Get("X-Request-ID") != "req-123" {
		t.Fatalf("expected response header to include request id")
	}
}

func TestRequestIDMiddleware_GeneratesHeader(t *testing.T) {
	var gotRequestID string

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequestID = GetRequestID(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	recorder := httptest.NewRecorder()

	RequestIDMiddleware(handler).ServeHTTP(recorder, req)

	if gotRequestID == "" {
		t.Fatalf("expected generated request id")
	}
	if recorder.Header().Get("X-Request-ID") != gotRequestID {
		t.Fatalf("expected response header to match generated request id")
	}
}
