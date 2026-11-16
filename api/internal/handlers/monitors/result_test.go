package monitors

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

func TestHandlers_GetMonitorResult(t *testing.T) {
	resultID := uuid.New()
	capture := json.RawMessage(`{"http":{"failure_response":{"status_line":"HTTP/1.1 503 Service Unavailable"}}}`)
	resultSvc := &MockResultsService{result: &models.CheckResult{ID: resultID, Status: "failure", MetricsData: capture}}
	handlers := NewHandlers(NewMockMonitorService(), &MockGroupService{}, resultSvc, logger.New("test", "debug"), t.TempDir())
	r := chi.NewRouter()
	r.Get("/{id}/results/{resultId}", handlers.GetMonitorResult)

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = req.WithContext(ctxpkg.WithTenantID(req.Context(), uuid.New().String()))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	monitorID := uuid.New().String()

	w := get("/" + monitorID + "/results/" + resultID.String())
	var got models.CheckResult
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.ID != resultID || string(got.MetricsData) != string(capture) {
		t.Fatalf("GET result = %d %s", w.Code, w.Body.String())
	}
	if w := get("/" + monitorID + "/results/" + uuid.New().String()); w.Code != http.StatusNotFound {
		t.Fatalf("unknown result = %d, want 404", w.Code)
	}
	if w := get("/" + monitorID + "/results/nope"); w.Code != http.StatusBadRequest {
		t.Fatalf("bad result id = %d, want 400", w.Code)
	}
}
