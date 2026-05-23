package push

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/api/internal/services/push"
	"github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

// Handler handles push monitor HTTP requests
type Handler struct {
	service       push.PushService
	log           *logger.Logger
	publicBaseURL string
}

// NewHandler creates a new push handler. publicBaseURL is the externally
// reachable URL of the API; when set, it is used in webhook URLs returned
// by HandleGetPushInfo instead of the request's Host header.
func NewHandler(service push.PushService, log *logger.Logger, publicBaseURL string) *Handler {
	return &Handler{
		service:       service,
		log:           log,
		publicBaseURL: publicBaseURL,
	}
}

// resolveBackendURL returns the URL to embed in push webhook responses.
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

// HandlePushGet handles GET /api/v1/push/{token}
// Simple heartbeat - marks as UP by default
func (h *Handler) HandlePushGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := chi.URLParam(r, "token")

	if token == "" {
		http.Error(w, "token is required", http.StatusBadRequest)
		return
	}

	// Build payload from query parameters
	payload := h.buildPayloadFromQuery(r)

	// Process the push
	if err := h.service.ProcessPush(ctx, token, payload); err != nil {
		h.log.WithError(err).Warn("failed to process push")
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, "push monitor not found", http.StatusNotFound)
			return
		}
		if strings.Contains(err.Error(), "disabled") {
			http.Error(w, "push monitor is disabled", http.StatusForbidden)
			return
		}
		http.Error(w, "failed to process push", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
	})
}

// HandlePushPost handles POST /api/v1/push/{token}
// Accepts JSON body or query parameters
func (h *Handler) HandlePushPost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := chi.URLParam(r, "token")

	if token == "" {
		http.Error(w, "token is required", http.StatusBadRequest)
		return
	}

	var payload push.PushPayload

	// Check if there's a JSON body
	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") && r.ContentLength > 0 {
		// Parse JSON body
		var bodyData map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&bodyData); err != nil {
			h.log.WithError(err).Warn("failed to decode push payload")
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}

		// Extract reserved fields
		if status, ok := bodyData["status"].(string); ok {
			payload.Status = status
			delete(bodyData, "status")
		}
		if errMsg, ok := bodyData["error"].(string); ok {
			payload.Error = errMsg
			delete(bodyData, "error")
		}

		// Remaining fields are metrics
		payload.Metrics = bodyData
	} else {
		// Build payload from query parameters
		payload = h.buildPayloadFromQuery(r)
	}

	// Process the push
	if err := h.service.ProcessPush(ctx, token, payload); err != nil {
		h.log.WithError(err).Warn("failed to process push")
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, "push monitor not found", http.StatusNotFound)
			return
		}
		if strings.Contains(err.Error(), "disabled") {
			http.Error(w, "push monitor is disabled", http.StatusForbidden)
			return
		}
		http.Error(w, "failed to process push", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
	})
}

// HandleGetPushInfo handles GET /api/v1/monitors/{id}/push/info
// Returns webhook URL and usage instructions (requires authentication)
func (h *Handler) HandleGetPushInfo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get tenant ID from context (set by auth middleware)
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

	// Get monitor ID from URL
	monitorIDStr := chi.URLParam(r, "id")
	monitorID, err := uuid.Parse(monitorIDStr)
	if err != nil {
		http.Error(w, "invalid monitor ID", http.StatusBadRequest)
		return
	}

	backendURL := h.resolveBackendURL(r)

	// Get push info
	info, err := h.service.GetPushInfo(ctx, monitorID, tenantID, backendURL)
	if err != nil {
		h.log.WithError(err).Error("failed to get push info")
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, "push monitor not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to get push info", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

// buildPayloadFromQuery extracts push payload from query parameters
func (h *Handler) buildPayloadFromQuery(r *http.Request) push.PushPayload {
	payload := push.PushPayload{
		Status:  "up", // Default to UP
		Metrics: make(map[string]interface{}),
	}

	for key, values := range r.URL.Query() {
		if len(values) == 0 {
			continue
		}
		value := values[0]

		switch key {
		case "status":
			payload.Status = value
		case "error":
			payload.Error = value
		default:
			// Auto-detect metric type: try int, then float, then string
			payload.Metrics[key] = parseValue(value)
		}
	}

	return payload
}

// parseValue attempts to parse a string value as int, float, or keeps it as string
func parseValue(value string) interface{} {
	// Try parsing as int first
	if intVal, err := strconv.ParseInt(value, 10, 64); err == nil {
		return intVal
	}
	// Try parsing as float
	if floatVal, err := strconv.ParseFloat(value, 64); err == nil {
		return floatVal
	}
	// Try parsing as bool
	if value == "true" {
		return true
	}
	if value == "false" {
		return false
	}
	// Keep as string
	return value
}
