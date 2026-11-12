package alerts

import (
	"fmt"
	"net/http"
	"time"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
)

// StreamAlerts handles GET /api/v1/alerts/stream - SSE endpoint for real-time alerts
func (h *Handlers) StreamAlerts(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	// Check if hub is initialized
	if h.hub == nil {
		h.logger.Error("SSE: hub is nil")
		apierrors.WriteInternalError(w, "SSE hub not initialized")
		return
	}

	// Check if the ResponseWriter supports flushing
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.logger.Error("SSE: streaming not supported")
		apierrors.WriteInternalError(w, "streaming not supported")
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering
	if origin := r.Header.Get("Origin"); origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Vary", "Origin")
	}

	// Register client for this tenant
	clientChan := h.hub.Register(tenantUUID)
	defer h.hub.Unregister(tenantUUID, clientChan)

	h.logger.WithFields(map[string]interface{}{
		"tenant_id":    tenantID,
		"client_count": h.hub.ClientCount(tenantUUID),
	}).Info("SSE client connected")

	// Send initial connection message
	fmt.Fprintf(w, "event: connected\ndata: {\"status\":\"connected\"}\n\n")
	flusher.Flush()

	// Heartbeat ticker to keep connection alive (every 15 seconds)
	heartbeatTicker := time.NewTicker(15 * time.Second)
	defer heartbeatTicker.Stop()

	// Stream events
	for {
		select {
		case event, ok := <-clientChan:
			if !ok {
				// Channel closed
				return
			}
			// Send SSE formatted event
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, event.Data)
			flusher.Flush()

		case <-heartbeatTicker.C:
			// Send heartbeat to keep connection alive
			fmt.Fprintf(w, "event: heartbeat\ndata: {\"time\":\"%s\"}\n\n", time.Now().UTC().Format(time.RFC3339))
			flusher.Flush()

		case <-r.Context().Done():
			// Client disconnected
			h.logger.WithFields(map[string]interface{}{
				"tenant_id": tenantID,
			}).Info("SSE client disconnected")
			return
		}
	}
}
