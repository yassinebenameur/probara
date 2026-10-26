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
)

// The legacy /agent/metrics tombstone: the successor endpoint, and the
// migration guide as the RFC 8594 "sunset" link.
const (
	legacyMetricsLink     = `</api/v1/otlp/v1/metrics>; rel="successor-version", <https://probara-uptime.com/docs/agents/#migrating-legacy>; rel="sunset"`
	legacyMetricsGoneBody = `{"error":"POST /api/v1/agent/metrics was retired on 2026-11-18; send OTLP to POST /api/v1/otlp/v1/metrics (reinstall with the OpenTelemetry Collector)"}` + "\n"
)

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

// HandleLegacyMetricsGone answers POST /api/v1/agent/metrics, the retired
// custom-agent push protocol (sunset 2026-11-18), with 410 Gone. The route
// stays registered so a straggler gets a pointer to the OTLP endpoint rather
// than a bare 404; the handler never reads the body or touches the database.
// A legacy agent started with --allow-remote-disable reads any 410 as
// "monitor deleted" and runs its uninstall command, which is the cleanup it
// needs anyway. The warn log is the operator's straggler finder.
func (h *Handler) HandleLegacyMetricsGone(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := context.GetTenantID(r.Context())
	h.log.WithFields(map[string]interface{}{
		"tenant_id":  tenantID,
		"user_agent": r.UserAgent(),
	}).Warn("legacy agent push rejected: endpoint retired, reinstall with the OpenTelemetry Collector")

	w.Header().Set("Link", legacyMetricsLink)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusGone)
	_, _ = w.Write([]byte(legacyMetricsGoneBody))
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

	apiKey := embeddableAPIKey(r)

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

	apiKey := embeddableAPIKey(r)

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

	apiKey := embeddableAPIKey(r)

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

	apiKey := embeddableAPIKey(r)

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

	apiKey := embeddableAPIKey(r)

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

// embeddableAPIKey returns the caller's bearer credential for baking into an
// install script, but only when it is a tenant API key. A cookie session has
// no bearer, and an MCP OAuth access token is a short-lived credential of a
// person: written onto a host it would leak that user's session and stop
// working within the hour. A read-scope key cannot submit metrics, so the
// collector it installed would have every export rejected. Those callers get
// a script with an empty key, which the installer refuses up front.
func embeddableAPIKey(r *http.Request) string {
	if actor, ok := context.GetActorType(r.Context()); !ok || actor != context.ActorTypeAPIKey {
		return ""
	}
	if scope, ok := context.GetAuthScope(r.Context()); !ok || scope != "write" {
		return ""
	}
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(authHeader, "Bearer ")
}
