package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORSAllowlist(t *testing.T) {
	h := CORS([]string{"https://ops.example.com/"})(statusHandler(http.StatusOK))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil)
	req.Header.Set("Origin", "https://ops.example.com")
	rec := do(h, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://ops.example.com" {
		t.Fatalf("allowed origin: ACAO = %q", got)
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "true" || rec.Header().Get("Vary") != "Origin" {
		t.Fatalf("allowed origin headers: %v", rec.Header())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec = do(h, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("denied origin still reaches the handler (the browser blocks it): %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("denied origin: ACAO = %q, want none", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("denied origin: ACAC = %q, want none", got)
	}
}

func TestCORSPreflight(t *testing.T) {
	h := CORS([]string{"https://ops.example.com"})(statusHandler(http.StatusTeapot))

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/monitors", nil)
	req.Header.Set("Origin", "https://ops.example.com")
	req.Header.Set("Access-Control-Request-Method", "PATCH")
	rec := do(h, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight code = %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Methods") == "" || rec.Header().Get("Access-Control-Allow-Headers") == "" {
		t.Fatalf("preflight headers: %v", rec.Header())
	}

	// A preflight from an origin outside the list is not answered.
	req.Header.Set("Origin", "https://evil.example")
	rec = do(h, req)
	if rec.Code != http.StatusTeapot || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("denied preflight: %d %v", rec.Code, rec.Header())
	}
}

func TestCORSEmptyAllowlistNeverReflects(t *testing.T) {
	h := CORS(nil)(statusHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/alerts/stream", nil)
	req.Header.Set("Origin", "https://anything.example")
	rec := do(h, req)
	if len(rec.Header().Values("Access-Control-Allow-Origin")) != 0 {
		t.Fatalf("empty allowlist reflected an origin: %v", rec.Header())
	}
}
