package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The operator UI's image is prebuilt, so it learns the status-page origin
// at runtime from /auth-context; unset, the field is left out and the UI
// falls back to its own resolution.
func TestAuthContextCarriesTheStatusPageOrigin(t *testing.T) {
	h := testHandlers()
	h.cfg.StatusPagePublicBaseURL = "https://status.example.com"

	rec := httptest.NewRecorder()
	h.AuthContext(rec, httptest.NewRequest(http.MethodGet, "/api/v1/auth-context", nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if got := body["status_page_base_url"]; got != "https://status.example.com" {
		t.Fatalf("status_page_base_url = %v, want the configured origin (%s)", got, rec.Body)
	}

	h.cfg.StatusPagePublicBaseURL = ""
	rec = httptest.NewRecorder()
	h.AuthContext(rec, httptest.NewRequest(http.MethodGet, "/api/v1/auth-context", nil))
	if strings.Contains(rec.Body.String(), "status_page_base_url") {
		t.Fatalf("unset origin rendered: %s", rec.Body)
	}
}
