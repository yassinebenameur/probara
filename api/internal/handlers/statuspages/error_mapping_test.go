package statuspages

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
	statuspageservice "github.com/yassinebenameur/probara/api/internal/services/statuspages"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

type fakeService struct {
	statuspageservice.StatusPageService
	err      error
	pageSize int
}

func (f *fakeService) GetStatusPage(context.Context, uuid.UUID, uuid.UUID) (*models.StatusPage, error) {
	return nil, f.err
}

func (f *fakeService) CreateStatusPage(context.Context, uuid.UUID, *models.CreateStatusPageRequest) (*models.StatusPage, error) {
	return nil, f.err
}

func (f *fakeService) DeleteStatusPage(context.Context, uuid.UUID, uuid.UUID) error {
	return f.err
}

func (f *fakeService) ListStatusPages(_ context.Context, _ uuid.UUID, page, pageSize int) (*models.StatusPageListResponse, error) {
	f.pageSize = pageSize
	return &models.StatusPageListResponse{Page: page, PageSize: pageSize}, nil
}

func serve(h http.HandlerFunc, method, path, body string) *httptest.ResponseRecorder {
	router := chi.NewRouter()
	router.Method(method, "/", h)
	router.Method(method, "/{id}", h)
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r = r.WithContext(ctxpkg.WithTenantID(r.Context(), uuid.NewString()))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

func TestStatusPageErrorsMapToStatus(t *testing.T) {
	log := logger.New("test", "error")
	page := "/" + uuid.NewString()
	create := `{"slug":"acme","title":"Acme"}`
	for _, tc := range []struct {
		name    string
		method  string
		path    string
		body    string
		handler func(*Handlers) http.HandlerFunc
		err     error
		want    int
		message string
	}{
		{"get not found", http.MethodGet, page, "", func(h *Handlers) http.HandlerFunc { return h.GetStatusPage }, statuspageservice.ErrStatusPageNotFound, http.StatusNotFound, "status page not found"},
		{"get wrapped not found", http.MethodGet, page, "", func(h *Handlers) http.HandlerFunc { return h.GetStatusPage }, fmt.Errorf("load: %w", statuspageservice.ErrStatusPageNotFound), http.StatusNotFound, "status page not found"},
		{"delete not found", http.MethodDelete, page, "", func(h *Handlers) http.HandlerFunc { return h.DeleteStatusPage }, statuspageservice.ErrStatusPageNotFound, http.StatusNotFound, "status page not found"},
		{"create duplicate slug", http.MethodPost, "/", create, func(h *Handlers) http.HandlerFunc { return h.CreateStatusPage }, statuspageservice.ErrSlugExists, http.StatusBadRequest, "slug already exists"},
		{"create other error", http.MethodPost, "/", create, func(h *Handlers) http.HandlerFunc { return h.CreateStatusPage }, fmt.Errorf("database is down"), http.StatusInternalServerError, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHandlers(&fakeService{err: tc.err}, log)
			w := serve(tc.handler(h), tc.method, tc.path, tc.body)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tc.message) {
				t.Fatalf("body = %s, want message %q", w.Body.String(), tc.message)
			}
		})
	}
}

// The template and library error writers echo the service's message, so the
// wrapped "version N is already published" must still be a 400 with that text.
func TestTemplateErrorsMapToStatus(t *testing.T) {
	h := NewHandlers(&fakeService{}, logger.New("test", "error"))
	for _, tc := range []struct {
		err     error
		want    int
		message string
	}{
		{statuspageservice.ErrStatusPageNotFound, http.StatusNotFound, "status page not found"},
		{statuspageservice.ErrTemplateVersionNotFound, http.StatusNotFound, "template version not found"},
		{statuspageservice.ErrNoDraftTemplate, http.StatusBadRequest, "no draft template to publish"},
		{fmt.Errorf("version %d %w", 3, statuspageservice.ErrAlreadyPublished), http.StatusBadRequest, "version 3 is already published"},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		h.writeTemplateError(w, r, tc.err, "publish template", uuid.New(), uuid.New())
		if w.Code != tc.want || !strings.Contains(w.Body.String(), `"message":"`+tc.message+`"`) {
			t.Errorf("%v: status = %d body = %s; want %d %q", tc.err, w.Code, w.Body.String(), tc.want, tc.message)
		}
	}
	for _, tc := range []struct {
		err     error
		want    int
		message string
	}{
		{statuspageservice.ErrLibraryTemplateNotFound, http.StatusNotFound, "library template not found"},
		{statuspageservice.ErrTemplateNameExists, http.StatusBadRequest, "template name already exists"},
	} {
		w := httptest.NewRecorder()
		h.writeLibraryError(w, tc.err, "save library template", uuid.New())
		if w.Code != tc.want || !strings.Contains(w.Body.String(), `"message":"`+tc.message+`"`) {
			t.Errorf("%v: status = %d body = %s; want %d %q", tc.err, w.Code, w.Body.String(), tc.want, tc.message)
		}
	}
}

func TestListStatusPages_PageSizeIsClamped(t *testing.T) {
	svc := &fakeService{}
	h := NewHandlers(svc, logger.New("test", "error"))
	w := serve(h.ListStatusPages, http.MethodGet, "/?page_size=5000", "")
	if w.Code != http.StatusOK || svc.pageSize != 100 {
		t.Fatalf("status = %d, page size = %d; want 200 and 100", w.Code, svc.pageSize)
	}
}
