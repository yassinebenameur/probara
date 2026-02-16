package agent

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/api/internal/services/agent"
	"github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/models"
)

// Handler handles agent-related HTTP requests
type Handler struct {
	service agent.AgentService
	log     *logger.Logger
}

// NewHandler creates a new agent handler
func NewHandler(service agent.AgentService, log *logger.Logger) *Handler {
	return &Handler{
		service: service,
		log:     log,
	}
}

// HandleReceiveMetrics handles POST /api/v1/agent/metrics
func (h *Handler) HandleReceiveMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get tenant ID from context (set by auth middleware)
	tenantIDStr, ok := context.GetTenantID(ctx)
	if !ok {
		h.log.Warn("tenant_id not found in context")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Parse tenant ID to UUID
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		h.log.WithError(err).Error("invalid tenant_id in context")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Parse request body
	var payload models.AgentMetricsPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		h.log.WithError(err).Warn("failed to decode metrics payload")
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Validate payload
	if payload.AgentID == "" {
		http.Error(w, "agent_id is required", http.StatusBadRequest)
		return
	}

	// Process metrics
	if err := h.service.ProcessMetrics(ctx, payload, tenantID); err != nil {
		h.log.WithError(err).Error("failed to process metrics")
		http.Error(w, "failed to process metrics", http.StatusInternalServerError)
		return
	}

	// Return success
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
	})
}

// HandleGetInstallCommand handles GET /api/v1/monitors/{id}/agent/install
func (h *Handler) HandleGetInstallCommand(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get tenant ID from context
	tenantIDStr, ok := context.GetTenantID(ctx)
	if !ok {
		h.log.Warn("tenant_id not found in context")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Parse tenant ID to UUID
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		h.log.WithError(err).Error("invalid tenant_id in context")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Get monitor ID from URL
	monitorIDStr := chi.URLParam(r, "id")
	monitorID, err := uuid.Parse(monitorIDStr)
	if err != nil {
		http.Error(w, "invalid monitor ID", http.StatusBadRequest)
		return
	}

	// Get backend URL from request or config
	backendURL := r.URL.Query().Get("backend_url")
	if backendURL == "" {
		// Default to the host from the request
		scheme := "https"
		if r.TLS == nil {
			scheme = "http"
		}
		backendURL = scheme + "://" + r.Host
	}

	// Extract API key from Authorization header
	apiKey := ""
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		apiKey = strings.TrimPrefix(authHeader, "Bearer ")
	}

	// Generate install command
	installCmd, err := h.service.GenerateInstallCommand(ctx, monitorID, tenantID, backendURL, apiKey)
	if err != nil {
		h.log.WithError(err).Error("failed to generate install command")
		http.Error(w, "failed to generate install command", http.StatusInternalServerError)
		return
	}

	// Return install command
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(installCmd)
}

// HandleGetInstallScript handles GET /api/v1/monitors/{id}/agent/install/script.sh
func (h *Handler) HandleGetInstallScript(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get tenant ID from context
	tenantIDStr, ok := context.GetTenantID(ctx)
	if !ok {
		h.log.Warn("tenant_id not found in context")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Parse tenant ID to UUID
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		h.log.WithError(err).Error("invalid tenant_id in context")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Get monitor ID from URL
	monitorIDStr := chi.URLParam(r, "id")
	monitorID, err := uuid.Parse(monitorIDStr)
	if err != nil {
		http.Error(w, "invalid monitor ID", http.StatusBadRequest)
		return
	}

	// Get backend URL from request or config
	backendURL := r.URL.Query().Get("backend_url")
	if backendURL == "" {
		// Default to the host from the request
		scheme := "https"
		if r.TLS == nil {
			scheme = "http"
		}
		backendURL = scheme + "://" + r.Host
	}

	// Extract API key from Authorization header
	apiKey := ""
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		apiKey = strings.TrimPrefix(authHeader, "Bearer ")
	}

	// Generate install command
	installCmd, err := h.service.GenerateInstallCommand(ctx, monitorID, tenantID, backendURL, apiKey)
	if err != nil {
		h.log.WithError(err).Error("failed to generate install command")
		http.Error(w, "failed to generate install command", http.StatusInternalServerError)
		return
	}

	// Return install script as shell script
	w.Header().Set("Content-Type", "text/x-shellscript")
	w.Header().Set("Content-Disposition", "inline; filename=install-probara-agent.sh")
	w.Write([]byte(installCmd.InstallScript))
}
