package tenants

import (
	"encoding/json"
	"net/http"

	"github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/tenants"
	"github.com/yassinebenameur/probara/shared/logger"
)

// Handlers handles tenant requests.
type Handlers struct {
	service *tenants.Service
	logger  *logger.Logger
}

// NewHandlers creates a new tenants handler.
func NewHandlers(service *tenants.Service, log *logger.Logger) *Handlers {
	return &Handlers{service: service, logger: log}
}

// ListTenants handles GET /api/v1/tenants
func (h *Handlers) ListTenants(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListTenants(r.Context())
	if err != nil {
		h.logger.WithError(err).Error("Failed to list tenants")
		errors.WriteInternalError(w, "failed to list tenants")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.TenantListResponse{Items: items})
}
