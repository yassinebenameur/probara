package agent

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/api/internal/services/agent"
	"github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/models"
)

// legacyAgentSunset is the HTTP Sunset date advertised on the legacy
// /agent/metrics endpoint; after the window it becomes a 410 tombstone.
const legacyAgentSunset = "Wed, 18 Nov 2026 00:00:00 GMT"

// Handler handles agent-related HTTP requests
type Handler struct {
	service       agent.AgentService
	log           *logger.Logger
	publicBaseURL string
}

// NewHandler creates a new agent handler. publicBaseURL is the externally
// reachable URL of the API (e.g. "https://probara.example.com"); when set,
// it is used as the BACKEND_URL in install scripts instead of the request's
// Host header, which is unreliable behind reverse proxies.
func NewHandler(service agent.AgentService, log *logger.Logger, publicBaseURL string) *Handler {
	return &Handler{
		service:       service,
		log:           log,
		publicBaseURL: publicBaseURL,
	}
}

// resolveBackendURL returns the URL to bake into agent install scripts.
// Precedence: ?backend_url= query override → configured PublicBaseURL → r.Host fallback.
func (h *Handler) resolveBackendURL(r *http.Request) string {
	if q := r.URL.Query().Get("backend_url"); q != "" {
		return q
	}
	if h.publicBaseURL != "" {
		return h.publicBaseURL
	}
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	return scheme + "://" + r.Host
}

// HandleReceiveMetrics handles POST /api/v1/agent/metrics — the LEGACY
// custom-agent push protocol, kept through the deprecation window so
// already-deployed agents keep reporting. New installs run the OTel
// Collector against POST /api/v1/otlp/v1/metrics. The Deprecation/Sunset
// headers and the warn log below are the operator's straggler finder.
func (h *Handler) HandleReceiveMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	w.Header().Set("Deprecation", "true")
	w.Header().Set("Sunset", legacyAgentSunset)
	w.Header().Set("Link", "<https://probara.dev/docs/agents#migrating-from-the-legacy-agent>; rel=\"deprecation\"")

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

	// Per-report straggler visibility: which tenants/agents still run the
	// legacy binary (the OTLP path never hits this handler).
	h.log.WithFields(map[string]interface{}{
		"tenant_id": tenantIDStr,
		"agent_id":  payload.AgentID,
	}).Warn("legacy agent push received (deprecated endpoint)")

	// Process metrics
	if err := h.service.ProcessMetrics(ctx, payload, tenantID); err != nil {
		h.log.WithError(err).Error("failed to process metrics")
		if errors.Is(err, agent.ErrAgentUnavailable) {
			http.Error(w, "agent monitor deleted", http.StatusGone)
			return
		}
		if errors.Is(err, agent.ErrAgentDisabled) {
			http.Error(w, "agent monitor disabled", http.StatusForbidden)
			return
		}
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

	backendURL := h.resolveBackendURL(r)

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

	backendURL := h.resolveBackendURL(r)

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

// HandleGetWindowsInstallScript handles GET /api/v1/monitors/{id}/agent/install/script.ps1
func (h *Handler) HandleGetWindowsInstallScript(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	tenantIDStr, ok := context.GetTenantID(ctx)
	if !ok {
		h.log.Warn("tenant_id not found in context")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		h.log.WithError(err).Error("invalid tenant_id in context")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	monitorIDStr := chi.URLParam(r, "id")
	monitorID, err := uuid.Parse(monitorIDStr)
	if err != nil {
		http.Error(w, "invalid monitor ID", http.StatusBadRequest)
		return
	}

	backendURL := h.resolveBackendURL(r)

	apiKey := ""
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		apiKey = strings.TrimPrefix(authHeader, "Bearer ")
	}

	installCmd, err := h.service.GenerateInstallCommand(ctx, monitorID, tenantID, backendURL, apiKey)
	if err != nil {
		h.log.WithError(err).Error("failed to generate install command")
		http.Error(w, "failed to generate install command", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "inline; filename=install-probara-agent.ps1")
	w.Write([]byte(installCmd.WindowsInstallScript))
}

// HandleGetCollectorConfig handles GET /api/v1/monitors/{id}/agent/config.yaml
// (?platform=linux|darwin|windows, default linux) — the raw collector config
// for config-management users and stock otelcol-contrib installs.
func (h *Handler) HandleGetCollectorConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	tenantIDStr, ok := context.GetTenantID(ctx)
	if !ok {
		h.log.Warn("tenant_id not found in context")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		h.log.WithError(err).Error("invalid tenant_id in context")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	monitorID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid monitor ID", http.StatusBadRequest)
		return
	}

	cfg, err := h.service.GenerateCollectorConfig(ctx, monitorID, tenantID, h.resolveBackendURL(r), r.URL.Query().Get("platform"))
	if err != nil {
		h.log.WithError(err).Error("failed to generate collector config")
		http.Error(w, "failed to generate collector config", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/yaml")
	w.Header().Set("Content-Disposition", "inline; filename=config.yaml")
	w.Write([]byte(cfg))
}

// HandleGetUninstallScript handles GET /api/v1/monitors/{id}/agent/uninstall/script.sh
func (h *Handler) HandleGetUninstallScript(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	tenantIDStr, ok := context.GetTenantID(ctx)
	if !ok {
		h.log.Warn("tenant_id not found in context")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		h.log.WithError(err).Error("invalid tenant_id in context")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	monitorIDStr := chi.URLParam(r, "id")
	monitorID, err := uuid.Parse(monitorIDStr)
	if err != nil {
		http.Error(w, "invalid monitor ID", http.StatusBadRequest)
		return
	}

	backendURL := h.resolveBackendURL(r)

	apiKey := ""
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		apiKey = strings.TrimPrefix(authHeader, "Bearer ")
	}

	installCmd, err := h.service.GenerateInstallCommand(ctx, monitorID, tenantID, backendURL, apiKey)
	if err != nil {
		h.log.WithError(err).Error("failed to generate uninstall command")
		http.Error(w, "failed to generate uninstall command", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/x-shellscript")
	w.Header().Set("Content-Disposition", "inline; filename=uninstall-probara-agent.sh")
	w.Write([]byte(installCmd.UninstallScript))
}

// HandleGetWindowsUninstallScript handles GET /api/v1/monitors/{id}/agent/uninstall/script.ps1
func (h *Handler) HandleGetWindowsUninstallScript(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	tenantIDStr, ok := context.GetTenantID(ctx)
	if !ok {
		h.log.Warn("tenant_id not found in context")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		h.log.WithError(err).Error("invalid tenant_id in context")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	monitorIDStr := chi.URLParam(r, "id")
	monitorID, err := uuid.Parse(monitorIDStr)
	if err != nil {
		http.Error(w, "invalid monitor ID", http.StatusBadRequest)
		return
	}

	backendURL := h.resolveBackendURL(r)

	apiKey := ""
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		apiKey = strings.TrimPrefix(authHeader, "Bearer ")
	}

	installCmd, err := h.service.GenerateInstallCommand(ctx, monitorID, tenantID, backendURL, apiKey)
	if err != nil {
		h.log.WithError(err).Error("failed to generate uninstall command")
		http.Error(w, "failed to generate uninstall command", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "inline; filename=uninstall-probara-agent.ps1")
	w.Write([]byte(installCmd.WindowsUninstallScript))
}
