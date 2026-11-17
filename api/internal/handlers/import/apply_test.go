package importhandlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	importservice "github.com/yassinebenameur/probara/api/internal/services/import"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

type recordedAudit struct{ events []audit.Event }

func (r *recordedAudit) Record(event audit.Event) { r.events = append(r.events, event) }

func applyCall(t *testing.T, handler func(http.ResponseWriter, *http.Request), path string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(models.MonitorApplyRequest{Document: json.RawMessage(`"kind: monitor_export"`)})
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), uuid.NewString()))
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

// Refusals answer with the plan and a stable error code; nothing else
// reaches the client as a 500.
func TestApply_StatusMapping(t *testing.T) {
	plan := models.MonitorApplyPlan{Fingerprint: "sha256:x", Errors: []models.MonitorApplyError{{Index: 0, Message: "bad"}}}
	for name, tc := range map[string]struct {
		err    error
		status int
		code   string
	}{
		"invalid":     {importservice.ErrApplyInvalid, http.StatusUnprocessableEntity, "invalid_document"},
		"changed":     {importservice.ErrApplyPlanChanged, http.StatusConflict, "plan_changed"},
		"prune":       {fmt.Errorf("%w: reason", importservice.ErrApplyPruneRefused), http.StatusConflict, "prune_refused"},
		"bad doc":     {fmt.Errorf("%w: yaml", importservice.ErrApplyDocument), http.StatusBadRequest, "validation_error"},
		"unavailable": {errors.New("boom"), http.StatusInternalServerError, "internal_error"},
	} {
		t.Run(name, func(t *testing.T) {
			service := &importServiceMock{result: &models.MonitorApplyResult{MonitorApplyPlan: plan}, err: tc.err}
			h := NewHandlers(service, logger.New("test", "error"))
			w := applyCall(t, h.Apply, "/api/v1/monitors/apply")
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
			var body struct {
				Error       string `json:"error"`
				Fingerprint string `json:"fingerprint"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &body)
			if body.Error != tc.code {
				t.Fatalf("error = %q, want %q", body.Error, tc.code)
			}
			if (tc.status == http.StatusConflict || tc.status == http.StatusUnprocessableEntity) && body.Fingerprint != "sha256:x" {
				t.Fatalf("refusal without the plan: %s", w.Body.String())
			}
		})
	}
}

// A plan and an apply each record one audit event with counts and keys.
func TestApply_Audit(t *testing.T) {
	recorder := &recordedAudit{}
	id := uuid.New()
	plan := models.MonitorApplyPlan{
		Valid:   true,
		Summary: models.MonitorApplySummary{Create: 1, Delete: 1},
		Monitors: []models.MonitorApplyItem{
			{Key: "web", Action: models.ApplyActionCreate, MonitorID: &id},
			{Key: "old", Action: models.ApplyActionDelete},
		},
	}
	service := &importServiceMock{plan: &plan, result: &models.MonitorApplyResult{MonitorApplyPlan: plan, Applied: true, Pruned: true}}
	h := NewHandlers(service, logger.New("test", "error"))
	h.ConfigureAudit(recorder)

	if w := applyCall(t, h.PlanApply, "/api/v1/monitors/apply/plan"); w.Code != http.StatusOK {
		t.Fatalf("plan status = %d", w.Code)
	}
	if w := applyCall(t, h.Apply, "/api/v1/monitors/apply"); w.Code != http.StatusOK {
		t.Fatalf("apply status = %d", w.Code)
	}
	if len(recorder.events) != 2 || recorder.events[0].Action != "monitor.plan" || recorder.events[1].Action != "monitor.apply" {
		t.Fatalf("audit events = %+v", recorder.events)
	}
	details := recorder.events[1].Details
	if keys, _ := details["deleted_keys"].([]string); len(keys) != 1 || keys[0] != "old" || details["pruned"] != true {
		t.Fatalf("apply audit details = %+v", details)
	}
}

// Plan needs read access only; apply needs write access.
func TestApply_ReadOnlyAllowlist(t *testing.T) {
	if !middleware.IsReadOnlyRequest(http.MethodPost, "/api/v1/monitors/apply/plan") {
		t.Fatal("plan is not on the read-only allowlist")
	}
	if middleware.IsReadOnlyRequest(http.MethodPost, "/api/v1/monitors/apply") {
		t.Fatal("apply is on the read-only allowlist")
	}
}
