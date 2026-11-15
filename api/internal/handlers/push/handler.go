package push

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/api/internal/services/push"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

// maxPushPayloadBytes caps both the JSON body and the raw query string of a
// push report. Whatever arrives is stored whole on every push (metrics as
// check_results.metrics_data, the error as error_message, raw rows kept 30
// days), and anyone holding the token can push at any rate. A real report —
// a status, an error line and a handful of metrics — is a few hundred bytes;
// 64 KiB leaves ample room without letting one request write megabytes. The
// query string needs its own bound because net/http alone admits ~1 MiB of
// request line.
const maxPushPayloadBytes = 64 << 10

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
	if !queryWithinLimit(w, r) {
		return
	}

	// Build payload from query parameters
	payload := h.buildPayloadFromQuery(r)

	// Process the push
	if err := push.ValidateStatus(payload.Status); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
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
// Accepts query parameters, a JSON body, or both (body keys override query keys)
func (h *Handler) HandlePushPost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := chi.URLParam(r, "token")

	if token == "" {
		http.Error(w, "token is required", http.StatusBadRequest)
		return
	}

	if !queryWithinLimit(w, r) {
		return
	}

	payload := h.buildPayloadFromQuery(r)

	// Check if there's a JSON body
	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		// Parse JSON body
		var bodyData map[string]interface{}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPushPayloadBytes))
		err := decoder.Decode(&bodyData)
		if err != nil && err != io.EOF {
			if bodyTooLarge(w, err) {
				return
			}
			h.log.WithError(err).Warn("failed to decode push payload")
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}

		if err == nil {
			if bodyData == nil {
				http.Error(w, "expected a single JSON object", http.StatusBadRequest)
				return
			}
			var trailing interface{}
			if terr := decoder.Decode(&trailing); terr != io.EOF {
				if bodyTooLarge(w, terr) {
					return
				}
				http.Error(w, "expected a single JSON object", http.StatusBadRequest)
				return
			}
			// The body overlays the query string field by field: a key the
			// body omits keeps its query value, so `POST ?status=down` with
			// a metrics-only body still records a failure. Body metrics win
			// over query metrics of the same name.
			if value, present := bodyData["status"]; present {
				status, ok := value.(string)
				if !ok {
					http.Error(w, "status must be a string", http.StatusBadRequest)
					return
				}
				payload.Status = status
				delete(bodyData, "status")
			}
			if value, present := bodyData["error"]; present {
				errMsg, ok := value.(string)
				if !ok {
					http.Error(w, "error must be a string", http.StatusBadRequest)
					return
				}
				payload.Error = errMsg
				delete(bodyData, "error")
			}
			for key, value := range bodyData {
				payload.Metrics[key] = value
			}
		}
	}

	// Process the push
	if err := push.ValidateStatus(payload.Status); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
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
	if !ctxpkg.CanWrite(ctx) {
		http.Error(w, "push credentials require write permission", http.StatusForbidden)
		return
	}

	// Get tenant ID from context (set by auth middleware)
	tenantIDStr, ok := ctxpkg.GetTenantID(ctx)
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

// queryWithinLimit answers 414 when the raw query string exceeds
// maxPushPayloadBytes, so query-string reports are bounded like bodies.
func queryWithinLimit(w http.ResponseWriter, r *http.Request) bool {
	if len(r.URL.RawQuery) > maxPushPayloadBytes {
		http.Error(w, "query string too large", http.StatusRequestURITooLong)
		return false
	}
	return true
}

// bodyTooLarge answers 413 when err came from the body cap.
func bodyTooLarge(w http.ResponseWriter, err error) bool {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return true
	}
	return false
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
