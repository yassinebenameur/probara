package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

func requestWithTenant(tenant string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if tenant == "" {
		return r
	}
	return r.WithContext(ctxpkg.WithTenantID(r.Context(), tenant))
}

func TestTenantID(t *testing.T) {
	want := uuid.New()
	rec := httptest.NewRecorder()
	got, ok := TenantID(rec, requestWithTenant(want.String()))
	if !ok || got != want {
		t.Fatalf("TenantID = %s, %v; want %s, true", got, ok, want)
	}

	rec = httptest.NewRecorder()
	if _, ok := TenantID(rec, requestWithTenant("")); ok || rec.Code != http.StatusUnauthorized {
		t.Fatalf("no tenant: ok=%v status=%d, want false and 401", ok, rec.Code)
	}

	rec = httptest.NewRecorder()
	if _, ok := TenantID(rec, requestWithTenant("not-a-uuid")); ok || rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed tenant: ok=%v status=%d, want false and 400", ok, rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"error":"validation_error"`) {
		t.Fatalf("malformed tenant body = %s, want the standard error shape", rec.Body.String())
	}
}

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSON(rec, http.StatusCreated, map[string]int{"n": 1})
	if rec.Code != http.StatusCreated || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status=%d content-type=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if strings.TrimSpace(rec.Body.String()) != `{"n":1}` {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestPage(t *testing.T) {
	for query, want := range map[string][2]int{
		"":                      {1, 20},
		"page=3&page_size=50":   {3, 50},
		"page=0&page_size=0":    {1, 20},
		"page=x&page_size=y":    {1, 20},
		"page=2&page_size=5000": {2, 100},
		"page=-1&page_size=-10": {1, 20},
	} {
		page, size := Page(httptest.NewRequest(http.MethodGet, "/?"+query, nil))
		if page != want[0] || size != want[1] {
			t.Errorf("Page(%q) = %d, %d; want %d, %d", query, page, size, want[0], want[1])
		}
	}
	if page, size := PageWithLimits(httptest.NewRequest(http.MethodGet, "/?page_size=500", nil), 50, 200); page != 1 || size != 200 {
		t.Errorf("PageWithLimits = %d, %d; want 1, 200", page, size)
	}
}
