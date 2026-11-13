package alerts

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	alertservice "github.com/yassinebenameur/probara/api/internal/services/alerts"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

// fakeListService records the params ListAlerts was called with.
type fakeListService struct {
	alertservice.AlertService
	got *models.AlertListParams
}

func (f *fakeListService) ListAlerts(_ context.Context, _ uuid.UUID, params *models.AlertListParams, _ alertservice.ListSort) (*models.AlertListResponse, error) {
	f.got = params
	return &models.AlertListResponse{Page: params.Page, PageSize: params.PageSize}, nil
}

func TestListAlerts_SearchTerm(t *testing.T) {
	cases := []struct {
		name      string
		q         string
		wantCode  int
		wantQuery string
	}{
		{name: "trimmed", q: "  checkout api ", wantCode: http.StatusOK, wantQuery: "checkout api"},
		{name: "blank is no filter", q: "   ", wantCode: http.StatusOK, wantQuery: ""},
		{name: "at the cap", q: strings.Repeat("é", maxAlertQueryLength), wantCode: http.StatusOK, wantQuery: strings.Repeat("é", maxAlertQueryLength)},
		{name: "over the cap", q: strings.Repeat("a", maxAlertQueryLength+1), wantCode: http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeListService{}
			h := NewHandlers(svc, alertservice.NewHub(), logger.New("test", "error"))
			r := httptest.NewRequest(http.MethodGet, "/api/v1/alerts?q="+url.QueryEscape(tc.q), nil)
			r = r.WithContext(ctxpkg.WithTenantID(r.Context(), uuid.NewString()))
			w := httptest.NewRecorder()
			h.ListAlerts(w, r)
			if w.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d", w.Code, tc.wantCode)
			}
			if tc.wantCode != http.StatusOK {
				if svc.got != nil {
					t.Fatal("service called for a rejected term")
				}
				return
			}
			if svc.got.Query != tc.wantQuery {
				t.Fatalf("Query = %q, want %q", svc.got.Query, tc.wantQuery)
			}
		})
	}
}
