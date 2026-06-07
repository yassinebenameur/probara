package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestAlertPoliciesGone verifies that every verb on /alert-policies returns 410.
func TestAlertPoliciesGone(t *testing.T) {
	gone := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte(`{"error":"alert policies were replaced by notification settings; see /notification-settings"}`))
	}

	r := chi.NewRouter()
	r.Route("/alert-policies", func(r chi.Router) {
		r.Post("/", gone)
		r.Get("/", gone)
		r.Get("/{id}", gone)
		r.Patch("/{id}", gone)
		r.Delete("/{id}", gone)
	})

	tests := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/alert-policies"},
		{http.MethodPost, "/alert-policies"},
		{http.MethodGet, "/alert-policies/some-id"},
		{http.MethodPatch, "/alert-policies/some-id"},
		{http.MethodDelete, "/alert-policies/some-id"},
	}

	for _, tc := range tests {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusGone {
			t.Errorf("%s %s: want 410, got %d", tc.method, tc.path, rec.Code)
		}
	}
}
