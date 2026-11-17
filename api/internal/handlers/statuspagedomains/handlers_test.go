package statuspagedomains

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	svc "github.com/yassinebenameur/probara/api/internal/services/statuspagedomains"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

func withTenant(r *http.Request) *http.Request {
	return r.WithContext(ctxpkg.WithTenantID(r.Context(), uuid.NewString()))
}

// Hostnames are validated before anything is stored: wildcards, IPs, URLs
// and internal names are refused with the reason.
func TestCreateRefusesBadHostnames(t *testing.T) {
	h := &Handlers{}
	for _, host := range []string{"*.example.com", "192.0.2.1", "https://status.example.com", "status.local", ""} {
		body := `{"status_page_id":"` + uuid.NewString() + `","hostname":"` + host + `"}`
		rec := httptest.NewRecorder()
		h.create(rec, withTenant(httptest.NewRequest(http.MethodPost, "/api/v1/status-page-domains", strings.NewReader(body))))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "hostname") {
			t.Errorf("%q: %d %s", host, rec.Code, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	h.create(rec, withTenant(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"status_page_id":"x","hostname":"status.example.com"}`))))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad page id: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.create(rec, withTenant(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"status_page_id":"`+uuid.NewString()+`","hostname":"status.example.com","extra":1}`))))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown field: %d", rec.Code)
	}
}

func TestErrorMapping(t *testing.T) {
	h := &Handlers{}
	cases := []struct {
		err  error
		code int
	}{
		{svc.ErrNotFound, http.StatusNotFound},
		{svc.ErrStatusPageNotFound, http.StatusNotFound},
		{svc.ErrHostnameTaken, http.StatusConflict},
		{svc.ErrHostnameReserved, http.StatusConflict},
		{svc.ErrDisabled, http.StatusBadRequest},
		{svc.ErrTooManyDomains, http.StatusBadRequest},
		{&svc.CheckedRecentlyError{RetryAfter: 12500 * time.Millisecond}, http.StatusTooManyRequests},
		{errors.New("boom"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.fail(rec, c.err, "test")
		if rec.Code != c.code {
			t.Errorf("%v: %d, want %d", c.err, rec.Code, c.code)
		}
		if c.code == http.StatusTooManyRequests && rec.Header().Get("Retry-After") != "13" {
			t.Errorf("Retry-After = %q", rec.Header().Get("Retry-After"))
		}
		if c.code == http.StatusInternalServerError && strings.Contains(rec.Body.String(), "boom") {
			t.Error("an internal error leaked its text")
		}
	}
}
