package audit

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
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
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}
	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteValidationError(w, "invalid tenant ID")
		return
	}

	filters := auditservice.Filters{
		Action:  r.URL.Query().Get("action"),
		Outcome: r.URL.Query().Get("outcome"),
	}
	if v := r.URL.Query().Get("actor_id"); v != "" {
		actorID, err := uuid.Parse(v)
		if err != nil {
			errors.WriteValidationError(w, "invalid actor_id")
			return
		}
		filters.ActorID = &actorID
	}
	if v := r.URL.Query().Get("from"); v != "" {
		from, err := time.Parse(time.RFC3339, v)
		if err != nil {
			errors.WriteValidationError(w, "invalid from timestamp (RFC3339)")
			return
		}
		filters.From = &from
	}
	if v := r.URL.Query().Get("to"); v != "" {
		to, err := time.Parse(time.RFC3339, v)
		if err != nil {
			errors.WriteValidationError(w, "invalid to timestamp (RFC3339)")
			return
		}
		filters.To = &to
	}
	if v := r.URL.Query().Get("page"); v != "" {
		if page, err := strconv.Atoi(v); err == nil && page > 0 {
			filters.Page = page
		}
	}
	if v := r.URL.Query().Get("page_size"); v != "" {
		if pageSize, err := strconv.Atoi(v); err == nil && pageSize > 0 {
			filters.PageSize = pageSize
		}
	}

	resp, err := h.service.List(r.Context(), tenantUUID, ctxpkg.IsSuperadmin(r.Context()), filters)
	if err != nil {
		h.logger.WithError(err).Error("Failed to list audit log")
		errors.WriteInternalError(w, "failed to list audit log")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// Actions handles GET /api/v1/audit-log/actions.
func (h *Handlers) Actions(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}
	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteValidationError(w, "invalid tenant ID")
		return
	}

	actions, err := h.service.DistinctActions(r.Context(), tenantUUID, ctxpkg.IsSuperadmin(r.Context()))
	if err != nil {
		h.logger.WithError(err).Error("Failed to list audit actions")
		errors.WriteInternalError(w, "failed to list audit actions")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string][]string{"actions": actions})
}
