// Package mesh exposes the inter-location connectivity matrix endpoints.
package mesh

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	locationservice "github.com/yassinebenameur/probara/api/internal/services/locations"
	meshservice "github.com/yassinebenameur/probara/api/internal/services/mesh"
	"github.com/yassinebenameur/probara/shared/logger"
	sharedmodels "github.com/yassinebenameur/probara/shared/models"
)

// probeRequester is the NATS request-reply surface for probe-now (same shape
// as the monitors handlers' checkRequester; satisfied by *queue.Client).
type probeRequester interface {
	Request(ctx context.Context, subject string, data []byte) ([]byte, error)
}

// Handlers handles mesh HTTP requests.
type Handlers struct {
	service             *meshservice.Service
	requester           probeRequester
	probeTimeoutSeconds int
	logger              *logger.Logger
}

// NewHandlers creates mesh handlers. requester may be nil (probe-now then
// returns an error response).
func NewHandlers(service *meshservice.Service, requester probeRequester, probeTimeoutSeconds int, log *logger.Logger) *Handlers {
	if probeTimeoutSeconds <= 0 {
		probeTimeoutSeconds = 5
	}
	return &Handlers{service: service, requester: requester, probeTimeoutSeconds: probeTimeoutSeconds, logger: log}
}

func (h *Handlers) tenantUUID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return uuid.Nil, false
	}
	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return uuid.Nil, false
	}
	return tenantUUID, true
}

// GetMesh handles GET /api/v1/mesh.
func (h *Handlers) GetMesh(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := h.tenantUUID(w, r)
	if !ok {
		return
	}

	mesh, err := h.service.GetMesh(r.Context(), tenantUUID)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{"error": err.Error(), "tenant_id": tenantUUID}).Error("Failed to load mesh")
		errors.WriteInternalError(w, "failed to load mesh")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(mesh)
}

// GetEdgeHistory handles GET /api/v1/mesh/history?source=<uuid>&target=<uuid>&hours=<n>.
func (h *Handlers) GetEdgeHistory(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := h.tenantUUID(w, r)
	if !ok {
		return
	}

	sourceID, err := uuid.Parse(r.URL.Query().Get("source"))
	if err != nil {
		errors.WriteValidationError(w, "invalid source location ID")
		return
	}
	targetID, err := uuid.Parse(r.URL.Query().Get("target"))
	if err != nil {
		errors.WriteValidationError(w, "invalid target location ID")
		return
	}
	hours := 24
	if raw := r.URL.Query().Get("hours"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 24*30 {
			hours = n
		}
	}

	points, err := h.service.GetEdgeHistory(r.Context(), tenantUUID, sourceID, targetID, time.Duration(hours)*time.Hour, 0)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{"error": err.Error(), "tenant_id": tenantUUID}).Error("Failed to load mesh edge history")
		errors.WriteInternalError(w, "failed to load edge history")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"points": points})
}

// ProbeNow handles POST /api/v1/mesh/probe — one ephemeral probe of a
// directed edge via the source location's workers (request-reply; nothing
// persisted), mirroring the monitors test-check flow.
func (h *Handlers) ProbeNow(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := h.tenantUUID(w, r)
	if !ok {
		return
	}

	if h.requester == nil {
		errors.WriteInternalError(w, "mesh probes are not available (queue not configured)")
		return
	}

	var req models.MeshProbeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}
	if req.SourceLocationID == req.TargetLocationID {
		errors.WriteValidationError(w, "source and target must differ")
		return
	}

	endpoint, err := h.service.GetProbeTarget(r.Context(), tenantUUID, req.SourceLocationID, req.TargetLocationID)
	if err != nil {
		if stderrors.Is(err, locationservice.ErrNotFound) {
			errors.WriteNotFoundError(w, "both locations must exist and participate in the mesh")
			return
		}
		h.logger.WithFields(map[string]interface{}{"error": err.Error(), "tenant_id": tenantUUID}).Error("Failed to resolve mesh probe target")
		errors.WriteInternalError(w, "failed to resolve probe target")
		return
	}

	config, err := json.Marshal(sharedmodels.MeshProbeConfig{
		TargetLocationID: req.TargetLocationID.String(),
		Endpoint:         endpoint,
	})
	if err != nil {
		errors.WriteInternalError(w, "failed to encode probe config")
		return
	}
	payload, err := json.Marshal(sharedmodels.CheckJobPayload{
		Type:           sharedmodels.MonitorTypeMeshProbe,
		Config:         config,
		TimeoutSeconds: h.probeTimeoutSeconds,
	})
	if err != nil {
		errors.WriteInternalError(w, "failed to encode probe payload")
		return
	}

	subject := sharedmodels.TestCheckSubjectForLocation(req.SourceLocationID.String())
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(h.probeTimeoutSeconds+10)*time.Second)
	defer cancel()
	reply, err := h.requester.Request(ctx, subject, payload)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{"error": err.Error(), "subject": subject}).Warn("Mesh probe request failed")
		errors.WriteInternalError(w, "no worker answered at the source location — is its worker running and connected?")
		return
	}

	var response sharedmodels.TestCheckResponse
	if err := json.Unmarshal(reply, &response); err != nil {
		errors.WriteInternalError(w, "invalid worker response")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
