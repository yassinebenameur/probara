package alerts

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	alertservice "github.com/yassinebenameur/probara/api/internal/services/alerts"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

// fakeErrService fails every lookup with err.
type fakeErrService struct {
	alertservice.AlertService
	err error
}

func (f *fakeErrService) GetAlert(context.Context, uuid.UUID, uuid.UUID) (*models.AlertWithDetails, error) {
	return nil, f.err
}

func (f *fakeErrService) AcknowledgeAlert(context.Context, uuid.UUID, uuid.UUID) (*models.AlertWithDetails, error) {
	return nil, f.err
}

func (f *fakeErrService) ResolveAlert(context.Context, uuid.UUID, uuid.UUID) (*models.AlertWithDetails, error) {
	return nil, f.err
}

func serveAlert(h http.HandlerFunc, method, tenant string) *httptest.ResponseRecorder {
	router := chi.NewRouter()
	router.Method(method, "/{id}", h)
	r := httptest.NewRequest(method, "/"+uuid.NewString(), nil)
	r = r.WithContext(ctxpkg.WithTenantID(r.Context(), tenant))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

// The handlers match service errors with errors.Is, so a sentinel maps to its
// status whether or not something wrapped it on the way up.
func TestAlertErrorsMapToStatus(t *testing.T) {
	log := logger.New("test", "error")
	for _, tc := range []struct {
		name    string
		method  string
		handler func(*Handlers) http.HandlerFunc
		err     error
		want    int
		message string
	}{
		{"get not found", http.MethodGet, func(h *Handlers) http.HandlerFunc { return h.GetAlert }, alertservice.ErrAlertNotFound, http.StatusNotFound, "alert not found"},
		{"get wrapped not found", http.MethodGet, func(h *Handlers) http.HandlerFunc { return h.GetAlert }, fmt.Errorf("load: %w", alertservice.ErrAlertNotFound), http.StatusNotFound, "alert not found"},
		{"get other error", http.MethodGet, func(h *Handlers) http.HandlerFunc { return h.GetAlert }, fmt.Errorf("database is down"), http.StatusInternalServerError, ""},
		{"acknowledge not active", http.MethodPost, func(h *Handlers) http.HandlerFunc { return h.AcknowledgeAlert }, alertservice.ErrAlertNotActive, http.StatusNotFound, "alert not found or not active"},
		{"resolve already resolved", http.MethodPost, func(h *Handlers) http.HandlerFunc { return h.ResolveAlert }, alertservice.ErrAlertAlreadyResolved, http.StatusNotFound, "alert not found or already resolved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHandlers(&fakeErrService{err: tc.err}, alertservice.NewHub(), log)
			w := serveAlert(tc.handler(h), tc.method, uuid.NewString())
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tc.message) {
				t.Fatalf("body = %s, want message %q", w.Body.String(), tc.message)
			}
		})
	}
}

func TestMalformedTenantIsValidationError(t *testing.T) {
	h := NewHandlers(&fakeErrService{}, alertservice.NewHub(), logger.New("test", "error"))
	w := serveAlert(h.GetAlert, http.MethodGet, "not-a-uuid")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, `"error":"validation_error"`) || !strings.Contains(body, `"message":"invalid tenant ID"`) {
		t.Fatalf("body = %s, want the standard error shape", body)
	}
}

func TestListAlerts_PageSizeIsClamped(t *testing.T) {
	for query, want := range map[string][2]int{
		"":                      {1, 20},
		"page=3&page_size=50":   {3, 50},
		"page=2&page_size=5000": {2, 100},
		"page=0&page_size=abc":  {1, 20},
	} {
		svc := &fakeListService{}
		h := NewHandlers(svc, alertservice.NewHub(), logger.New("test", "error"))
		r := httptest.NewRequest(http.MethodGet, "/api/v1/alerts?"+query, nil)
		r = r.WithContext(ctxpkg.WithTenantID(r.Context(), uuid.NewString()))
		w := httptest.NewRecorder()
		h.ListAlerts(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%q: status = %d: %s", query, w.Code, w.Body.String())
		}
		if svc.got.Page != want[0] || svc.got.PageSize != want[1] {
			t.Errorf("%q: page, size = %d, %d; want %d, %d", query, svc.got.Page, svc.got.PageSize, want[0], want[1])
		}
	}
}
