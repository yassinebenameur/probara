package statuspages

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	statuspageservice "github.com/yassinebenameur/probara/api/internal/services/statuspages"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

type accessFakeService struct {
	statuspageservice.StatusPageService
	err       error
	createdBy statuspageservice.TokenCreator
	label     string
	called    bool
}

func (f *accessFakeService) CreateStatusPage(context.Context, uuid.UUID, *models.CreateStatusPageRequest) (*models.StatusPage, error) {
	f.called = true
	return &models.StatusPage{}, f.err
}

func (f *accessFakeService) UpdateStatusPage(context.Context, uuid.UUID, uuid.UUID, *models.UpdateStatusPageRequest) (*models.StatusPage, error) {
	f.called = true
	return &models.StatusPage{}, f.err
}

func (f *accessFakeService) CreateAccessToken(_ context.Context, _, _ uuid.UUID, by statuspageservice.TokenCreator, label string) (*models.CreatedStatusPageAccessToken, error) {
	f.called = true
	f.createdBy, f.label = by, label
	if f.err != nil {
		return nil, f.err
	}
	return &models.CreatedStatusPageAccessToken{
		StatusPageAccessToken: models.StatusPageAccessToken{ID: uuid.New(), Label: label, CreatedAt: time.Now()},
		Token:                 "spk_secret-value",
	}, nil
}

func (f *accessFakeService) RevokeAccessToken(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	f.called = true
	return f.err
}

func (f *accessFakeService) ListAccessTokens(context.Context, uuid.UUID, uuid.UUID) (*models.StatusPageAccessTokenListResponse, error) {
	f.called = true
	return &models.StatusPageAccessTokenListResponse{Items: []models.StatusPageAccessToken{}}, f.err
}

func serveAccess(h http.HandlerFunc, method, pattern, path, body string, ctxs ...func(context.Context) context.Context) *httptest.ResponseRecorder {
	router := chi.NewRouter()
	router.Method(method, pattern, h)
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	ctx := ctxpkg.WithTenantID(r.Context(), uuid.NewString())
	for _, f := range ctxs {
		ctx = f(ctx)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r.WithContext(ctx))
	return w
}

func TestStatusPageAccessFieldsValidatedAndMapped(t *testing.T) {
	log := logger.New("test", "error")
	page := "/" + uuid.NewString()
	for _, tc := range []struct {
		name        string
		method      string
		path        string
		body        string
		err         error
		want        int
		message     string
		serviceHits bool
	}{
		{"create unknown visibility", http.MethodPost, "/", `{"slug":"a","title":"A","visibility":"secret"}`, nil, 400, "visibility must be one of", false},
		{"create short password", http.MethodPost, "/", `{"slug":"a","title":"A","visibility":"password","password":"short"}`, nil, 400, "at least 8 characters", false},
		{"create password page without password", http.MethodPost, "/", `{"slug":"a","title":"A","visibility":"password"}`, statuspageservice.ErrPasswordRequired, 400, "a password is required", true},
		{"update short password", http.MethodPatch, page, `{"password":"1234567"}`, nil, 400, "at least 8 characters", false},
		{"update clear on password page", http.MethodPatch, page, `{"password":""}`, statuspageservice.ErrPasswordRequired, 400, "a password is required", true},
		{"update visibility ok", http.MethodPatch, page, `{"visibility":"unlisted"}`, nil, 200, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &accessFakeService{err: tc.err}
			h := NewHandlers(svc, log)
			handler, pattern := h.CreateStatusPage, "/"
			if tc.method == http.MethodPatch {
				handler, pattern = h.UpdateStatusPage, "/{id}"
			}
			w := serveAccess(handler, tc.method, pattern, tc.path, tc.body)
			if w.Code != tc.want || !strings.Contains(w.Body.String(), tc.message) {
				t.Fatalf("= %d %s, want %d %q", w.Code, w.Body.String(), tc.want, tc.message)
			}
			if svc.called != tc.serviceHits {
				t.Fatalf("service called = %v", svc.called)
			}
		})
	}
}

func TestAccessTokenHandlers(t *testing.T) {
	log := logger.New("test", "error")
	pattern := "/{id}/access-tokens"
	path := "/" + uuid.NewString() + "/access-tokens"
	userID, keyID := uuid.New(), uuid.New()

	svc := &accessFakeService{}
	h := NewHandlers(svc, log)
	w := serveAccess(h.CreateAccessToken, http.MethodPost, pattern, path, `{"label":"Lobby TV"}`,
		func(ctx context.Context) context.Context { return ctxpkg.WithAdminID(ctx, userID.String()) },
		func(ctx context.Context) context.Context { return ctxpkg.WithAPIKeyID(ctx, keyID.String()) },
	)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"token":"spk_secret-value"`) {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("the response carrying the token is cacheable")
	}
	if svc.createdBy.UserID != userID || svc.createdBy.APIKeyID != keyID || svc.label != "Lobby TV" {
		t.Fatalf("creator = %+v label %q", svc.createdBy, svc.label)
	}

	for _, tc := range []struct {
		name, body string
		err        error
		want       int
		message    string
	}{
		{"empty label", `{"label":"  "}`, nil, 400, "label is required"},
		{"long label", `{"label":"` + strings.Repeat("x", 101) + `"}`, nil, 400, "at most 100"},
		{"cap", `{"label":"x"}`, statuspageservice.ErrTooManyAccessTokens, 400, "at most 50 active kiosk tokens"},
		{"no page", `{"label":"x"}`, statuspageservice.ErrStatusPageNotFound, 404, "status page not found"},
	} {
		w := serveAccess(NewHandlers(&accessFakeService{err: tc.err}, log).CreateAccessToken, http.MethodPost, pattern, path, tc.body)
		if w.Code != tc.want || !strings.Contains(w.Body.String(), tc.message) {
			t.Errorf("%s: = %d %s", tc.name, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "spk_") {
			t.Errorf("%s: token in an error response", tc.name)
		}
	}

	revoke := "/{id}/access-tokens/{accessTokenId}/revoke"
	if w := serveAccess(NewHandlers(&accessFakeService{}, log).RevokeAccessToken, http.MethodPost, revoke, path+"/"+uuid.NewString()+"/revoke", ""); w.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d", w.Code)
	}
	if w := serveAccess(NewHandlers(&accessFakeService{err: statuspageservice.ErrAccessTokenNotFound}, log).RevokeAccessToken, http.MethodPost, revoke, path+"/"+uuid.NewString()+"/revoke", ""); w.Code != http.StatusNotFound {
		t.Fatalf("revoke unknown = %d", w.Code)
	}
	if w := serveAccess(NewHandlers(&accessFakeService{}, log).RevokeAccessToken, http.MethodPost, revoke, path+"/nope/revoke", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("revoke bad id = %d", w.Code)
	}
	if w := serveAccess(NewHandlers(&accessFakeService{}, log).ListAccessTokens, http.MethodGet, pattern, path, ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Fatalf("list = %d %s", w.Code, w.Body.String())
	}
}
