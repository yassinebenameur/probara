package importhandlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/yassinebenameur/probara/api/internal/dbtx"
	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	importservice "github.com/yassinebenameur/probara/api/internal/services/import"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
)

// auditRecorder is the slice of *audit.Recorder the apply handlers use.
type auditRecorder interface {
	Record(event audit.Event)
}

// ConfigureAudit wires the recorder that audits plans and applies with
// their counts and the keys they touched (never configuration). The audit
// middleware leaves their successes to these events (auditItemizedPrefixes).
func (h *Handlers) ConfigureAudit(recorder auditRecorder) {
	h.audit = recorder
}

func (h *Handlers) decodeApplyRequest(w http.ResponseWriter, r *http.Request) (*models.MonitorApplyRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	var req models.MonitorApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return nil, false
	}
	return &req, true
}

// PlanApply handles POST /api/v1/monitors/apply/plan: what applying the
// document would do, writing nothing. Read access is enough (it is on the
// read-only POST allowlist, middleware/readonly.go); a document that cannot
// be applied still answers 200, with valid false and its errors.
func (h *Handlers) PlanApply(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	req, ok := h.decodeApplyRequest(w, r)
	if !ok {
		return
	}
	plan, err := h.service.PlanApply(r.Context(), tenantID, req)
	if err != nil {
		h.writeApplyError(w, r, err, nil)
		return
	}
	h.auditApply(r, "monitor.plan", plan, nil)
	httpx.WriteJSON(w, http.StatusOK, plan)
}

// Apply handles POST /api/v1/monitors/apply. All or nothing: on 200 every
// change of the plan it returns was made; any refusal writes nothing and
// answers with the plan and the reason (422 invalid document; 409 changed
// since the fingerprinted plan, prune or a probable key rename refused
// without force, or a monitor saved meanwhile, which a retry resolves).
func (h *Handlers) Apply(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	req, ok := h.decodeApplyRequest(w, r)
	if !ok {
		return
	}
	result, err := h.service.Apply(r.Context(), tenantID, req)
	if err != nil {
		h.writeApplyError(w, r, err, result)
		return
	}
	h.auditApply(r, "monitor.apply", &result.MonitorApplyPlan, result)
	h.logger.WithFields(map[string]interface{}{
		"tenant_id": tenantID,
		"create":    result.Summary.Create,
		"update":    result.Summary.Update,
		"unchanged": result.Summary.Unchanged,
		"pruned":    result.Pruned,
		"deletes":   result.Summary.Delete,
	}).Info("Monitors applied")
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handlers) writeApplyError(w http.ResponseWriter, r *http.Request, err error, result *models.MonitorApplyResult) {
	refusal := func(status int, code string) {
		if result == nil {
			apierrors.WriteError(w, status, code, err.Error())
			return
		}
		result.Error, result.Message = code, err.Error()
		httpx.WriteJSON(w, status, result)
	}
	switch {
	case errors.Is(err, importservice.ErrApplyDocument):
		apierrors.WriteValidationError(w, err.Error())
	case errors.Is(err, importservice.ErrApplyInvalid):
		refusal(http.StatusUnprocessableEntity, "invalid_document")
	case errors.Is(err, importservice.ErrApplyPlanChanged):
		refusal(http.StatusConflict, "plan_changed")
	case errors.Is(err, importservice.ErrApplyPruneRefused):
		refusal(http.StatusConflict, "prune_refused")
	case errors.Is(err, importservice.ErrApplyRenameRefused):
		refusal(http.StatusConflict, "probable_rename")
	case errors.Is(err, importservice.ErrApplyStateChanged):
		refusal(http.StatusConflict, "apply_conflict")
	case errors.Is(err, monitorservice.ErrCallMonitorForbidden):
		apierrors.WriteForbiddenError(w, err.Error())
	case errors.Is(err, monitorservice.ErrMonitorKeyTaken):
		apierrors.WriteError(w, http.StatusConflict, "key_taken", err.Error())
	case dbtx.IsConflict(err):
		apierrors.WriteError(w, http.StatusServiceUnavailable, "conflict", dbtx.ErrConflict.Error())
	default:
		h.logger.WithFields(map[string]interface{}{
			"error": err.Error(),
			"path":  r.URL.Path,
		}).Error("Monitor apply failed")
		apierrors.WriteInternalError(w, "monitor apply failed; nothing was written")
	}
}

// auditApply records a successful plan or apply: the counts and the keys of
// the monitors it created, changed or deleted.
func (h *Handlers) auditApply(r *http.Request, action string, plan *models.MonitorApplyPlan, result *models.MonitorApplyResult) {
	if h.audit == nil {
		return
	}
	details := map[string]any{
		"valid":     plan.Valid,
		"create":    plan.Summary.Create,
		"update":    plan.Summary.Update,
		"unchanged": plan.Summary.Unchanged,
		"delete":    plan.Summary.Delete,
	}
	if result != nil {
		keys := map[string][]string{}
		for _, item := range plan.Monitors {
			if item.Action == models.ApplyActionDelete && !result.Pruned {
				continue
			}
			if item.Action != models.ApplyActionUnchanged {
				keys[item.Action] = append(keys[item.Action], item.Key)
			}
		}
		details["pruned"] = result.Pruned
		if result.Pruned && len(plan.Prune.Affected) > 0 {
			affected := make([]string, 0, len(plan.Prune.Affected))
			for _, m := range plan.Prune.Affected {
				affected = append(affected, m.MonitorID.String())
			}
			details["prune_affected_ids"] = affected
		}
		details["created_keys"] = keys[models.ApplyActionCreate]
		details["updated_keys"] = keys[models.ApplyActionUpdate]
		details["deleted_keys"] = keys[models.ApplyActionDelete]
	}
	event := audit.FromRequest(r)
	event.Action = action
	event.ResourceType = "monitor"
	event.StatusCode = http.StatusOK
	event.Outcome = audit.OutcomeSuccess
	event.Details = details
	h.audit.Record(event)
}
