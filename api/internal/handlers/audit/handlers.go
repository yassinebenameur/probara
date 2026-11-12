package audit

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/pagination"
	auditservice "github.com/yassinebenameur/probara/api/internal/services/audit"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

// Handlers serves the audit log API.
type Handlers struct {
	service *auditservice.Service
	logger  *logger.Logger
}

// NewHandlers creates audit handlers.
func NewHandlers(service *auditservice.Service, log *logger.Logger) *Handlers {
	return &Handlers{service: service, logger: log}
}

// List handles GET /api/v1/audit-log.
func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}

	filters := auditservice.Filters{
		Action:  r.URL.Query().Get("action"),
		Outcome: r.URL.Query().Get("outcome"),
	}
	if v := r.URL.Query().Get("actor_id"); v != "" {
		actorID, err := uuid.Parse(v)
		if err != nil {
			apierrors.WriteValidationError(w, "invalid actor_id")
			return
		}
		filters.ActorID = &actorID
	}
	if v := r.URL.Query().Get("from"); v != "" {
		from, err := time.Parse(time.RFC3339, v)
		if err != nil {
			apierrors.WriteValidationError(w, "invalid from timestamp (RFC3339)")
			return
		}
		filters.From = &from
	}
	if v := r.URL.Query().Get("to"); v != "" {
		to, err := time.Parse(time.RFC3339, v)
		if err != nil {
			apierrors.WriteValidationError(w, "invalid to timestamp (RFC3339)")
			return
		}
		filters.To = &to
	}
	filters.Page, filters.PageSize = httpx.PageWithLimits(r, pagination.LargeDefaultSize, pagination.LargeMaxSize)

	// sort=occurred_at|action|resource|outcome, dir=asc|desc (whitelisted).
	sortColumn, sortDesc, err := auditservice.ParseSort(r.URL.Query().Get("sort"), r.URL.Query().Get("dir"))
	if err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}
	filters.Sort = sortColumn
	filters.Desc = sortDesc

	resp, err := h.service.List(r.Context(), tenantUUID, ctxpkg.IsSuperadmin(r.Context()), filters)
	if err != nil {
		h.logger.WithError(err).Error("Failed to list audit log")
		apierrors.WriteInternalError(w, "failed to list audit log")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, resp)
}

// Actions handles GET /api/v1/audit-log/actions.
func (h *Handlers) Actions(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}

	actions, err := h.service.DistinctActions(r.Context(), tenantUUID, ctxpkg.IsSuperadmin(r.Context()))
	if err != nil {
		h.logger.WithError(err).Error("Failed to list audit actions")
		apierrors.WriteInternalError(w, "failed to list audit actions")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string][]string{"actions": actions})
}
