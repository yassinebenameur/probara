package alertchannels

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
	alertchannelservice "github.com/yassinebenameur/probara/api/internal/services/alertchannels"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

type fakeService struct {
	alertchannelservice.AlertChannelService
	err error
}

func (f *fakeService) GetAlertChannel(context.Context, uuid.UUID, uuid.UUID) (*models.AlertChannel, error) {
	return nil, f.err
}

func (f *fakeService) DeleteAlertChannel(context.Context, uuid.UUID, uuid.UUID) error {
	return f.err
}

func serve(h http.HandlerFunc, method, tenant string) *httptest.ResponseRecorder {
	router := chi.NewRouter()
	router.Method(method, "/{id}", h)
	r := httptest.NewRequest(method, "/"+uuid.NewString(), nil)
	r = r.WithContext(ctxpkg.WithTenantID(r.Context(), tenant))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

func TestAlertChannelErrorsMapToStatus(t *testing.T) {
	log := logger.New("test", "error")
	for _, tc := range []struct {
		name    string
		method  string
		handler func(*Handlers) http.HandlerFunc
		err     error
		want    int
	}{
		{"get not found", http.MethodGet, func(h *Handlers) http.HandlerFunc { return h.GetAlertChannel }, alertchannelservice.ErrAlertChannelNotFound, http.StatusNotFound},
		{"get wrapped not found", http.MethodGet, func(h *Handlers) http.HandlerFunc { return h.GetAlertChannel }, fmt.Errorf("load: %w", alertchannelservice.ErrAlertChannelNotFound), http.StatusNotFound},
		{"delete not found", http.MethodDelete, func(h *Handlers) http.HandlerFunc { return h.DeleteAlertChannel }, alertchannelservice.ErrAlertChannelNotFound, http.StatusNotFound},
		{"get other error", http.MethodGet, func(h *Handlers) http.HandlerFunc { return h.GetAlertChannel }, fmt.Errorf("database is down"), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := serve(tc.handler(NewHandlers(&fakeService{err: tc.err}, log)), tc.method, uuid.NewString())
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if tc.want == http.StatusNotFound && !strings.Contains(w.Body.String(), `"message":"alert channel not found"`) {
				t.Fatalf("body = %s, want the not-found message", w.Body.String())
			}
		})
	}
}

func TestMalformedTenantIsValidationError(t *testing.T) {
	w := serve(NewHandlers(&fakeService{}, logger.New("test", "error")).GetAlertChannel, http.MethodGet, "not-a-uuid")
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"error":"validation_error"`) {
		t.Fatalf("status = %d body = %s; want 400 validation_error", w.Code, w.Body.String())
	}
}
