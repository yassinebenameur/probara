package reportschedules

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	svc "github.com/yassinebenameur/probara/api/internal/services/reportschedules"
	"github.com/yassinebenameur/probara/shared/actionlink"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

const slaPath = "/api/v1/sla-report-schedules/c952c53e-d8c8-4e16-8e94-a9ec57690c11"

type keys struct{}

func (keys) CurrentKey() ([]byte, int, error)  { return bytes.Repeat([]byte{3}, 32), 1, nil }
func (keys) KeyForVersion(int) ([]byte, error) { return bytes.Repeat([]byte{3}, 32), nil }
func newHandlers(links *actionlink.Signer) *Handlers {
	return &Handlers{Service: svc.NewService(nil, nil, svc.Config{Links: links})}
}

func router(h *Handlers) chi.Router {
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		r.Route("/report-unsubscribe", h.PublicRoutes)
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireWrite)
			r.Route("/sla-report-schedules", h.Routes)
		})
	})
	return r
}

func TestReadOnlyCannotChangeSchedules(t *testing.T) {
	r := router(&Handlers{})
	for _, ctx := range []context.Context{ctxpkg.WithRole(context.Background(), "viewer"), ctxpkg.WithAuthScope(context.Background(), "read")} {
		for _, method := range []string{http.MethodPut, http.MethodDelete} {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(method, slaPath, strings.NewReader(`{"recipients":["a@example.com"]}`)).WithContext(ctx))
			require.Equal(t, http.StatusForbidden, w.Code, method)
		}
	}
}

func TestPutRejectsMalformedRequests(t *testing.T) {
	r := router(newHandlers(nil))
	ctx := ctxpkg.WithRole(ctxpkg.WithTenantID(context.Background(), "00000000-0000-0000-0000-000000000001"), "editor")
	for name, c := range map[string]struct{ path, body string }{
		"bad SLA ID":    {"/api/v1/sla-report-schedules/nope", `{"recipients":["a@example.com"]}`},
		"unknown field": {slaPath, `{"recipients":["a@example.com"],"formats":["pdf"]}`},
		"two objects":   {slaPath, `{"recipients":[]} {}`},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, c.path, strings.NewReader(c.body)).WithContext(ctx))
		require.Equal(t, http.StatusBadRequest, w.Code, name)
	}
}

// A browser that opened the one-click URL itself lands on the confirm page
// with the token in the fragment; the GET changes nothing.
func TestUnsubscribeGetWithQueryRedirectsToConfirmPage(t *testing.T) {
	w := httptest.NewRecorder()
	router(newHandlers(nil)).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/report-unsubscribe?token=AbC_-9", nil))
	require.Equal(t, http.StatusSeeOther, w.Code)
	require.Equal(t, "/report-unsubscribe#AbC_-9", w.Header().Get("Location"))
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}

// Every refusal answers the same, before any database read: no key, no
// token, a malformed or forged one, header or query.
func TestUnsubscribeRefusalsAreGeneric(t *testing.T) {
	signer := actionlink.New(keys{})
	other := actionlink.New(otherKeys{})
	forged, err := other.ActionToken(actionlink.ActionUnsubscribeReport,
		"00000000-0000-0000-0000-000000000001", "c952c53e-d8c8-4e16-8e94-a9ec57690c11", "3f2a91cc-11de-4b7a-9a2e-6c5f8d0e1234", testNow())
	require.NoError(t, err)
	for name, c := range map[string]struct {
		h      *Handlers
		method string
		target string
		header string
	}{
		"no key":           {newHandlers(nil), http.MethodPost, "/api/v1/report-unsubscribe", forged},
		"no token":         {newHandlers(signer), http.MethodPost, "/api/v1/report-unsubscribe", ""},
		"malformed":        {newHandlers(signer), http.MethodGet, "/api/v1/report-unsubscribe", "nonsense"},
		"forged header":    {newHandlers(signer), http.MethodPost, "/api/v1/report-unsubscribe", forged},
		"forged one-click": {newHandlers(signer), http.MethodPost, "/api/v1/report-unsubscribe?token=" + forged, ""},
	} {
		req := httptest.NewRequest(c.method, c.target, strings.NewReader("List-Unsubscribe=One-Click"))
		if c.header != "" {
			req.Header.Set(TokenHeader, c.header)
		}
		w := httptest.NewRecorder()
		router(c.h).ServeHTTP(w, req)
		require.Equal(t, http.StatusNotFound, w.Code, name)
		var resp models.ReportUnsubscribeResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), name)
		require.Equal(t, models.ReportUnsubscribeResponse{State: models.ReportUnsubscribeInvalid}, resp, name)
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"), name)
	}
}

type otherKeys struct{}

func (otherKeys) CurrentKey() ([]byte, int, error)  { return bytes.Repeat([]byte{4}, 32), 1, nil }
func (otherKeys) KeyForVersion(int) ([]byte, error) { return bytes.Repeat([]byte{4}, 32), nil }

func testNow() time.Time { return time.Now() }
