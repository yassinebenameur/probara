package alerts

import (
	"encoding/json"
	"sync"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// SSEEvent represents an event to be sent to SSE clients
type SSEEvent struct {
	Type string `json:"type"` // "alert_created", "alert_acknowledged", "alert_resolved"
	Data string `json:"data"` // JSON-encoded alert data
}

// Hub manages SSE client connections grouped by tenant
type Hub struct {
	mu      sync.RWMutex
	clients map[uuid.UUID]map[chan SSEEvent]struct{} // tenant_id -> set of client channels
}

// NewHub creates a new SSE hub
func NewHub() *Hub {
	return &Hub{
		clients: make(map[uuid.UUID]map[chan SSEEvent]struct{}),
	}
}

// Register adds a client channel for a tenant and returns the channel
func (h *Hub) Register(tenantID uuid.UUID) chan SSEEvent {
	h.mu.Lock()
	defer h.mu.Unlock()

	ch := make(chan SSEEvent, 10) // Buffered channel

	if h.clients[tenantID] == nil {
		h.clients[tenantID] = make(map[chan SSEEvent]struct{})
	}
	h.clients[tenantID][ch] = struct{}{}

	return ch
}

// Unregister removes a client channel for a tenant
func (h *Hub) Unregister(tenantID uuid.UUID, ch chan SSEEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if tenantClients, ok := h.clients[tenantID]; ok {
		if _, exists := tenantClients[ch]; exists {
			delete(tenantClients, ch)
			close(ch)
		}
		// Clean up empty tenant map
		if len(tenantClients) == 0 {
			delete(h.clients, tenantID)
		}
	}
}

// Broadcast sends an event to all clients of a specific tenant
func (h *Hub) Broadcast(tenantID uuid.UUID, eventType string, alert *models.AlertWithDetails) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	tenantClients, ok := h.clients[tenantID]
	if !ok || len(tenantClients) == 0 {
		return
	}

	// Serialize alert to JSON
	alertJSON, err := json.Marshal(alert)
	if err != nil {
		return
	}

	event := SSEEvent{
		Type: eventType,
		Data: string(alertJSON),
	}

	// Send to all clients (non-blocking)
	for ch := range tenantClients {
		select {
		case ch <- event:
		default:
			// Channel full, skip this client
		}
	}
}

// BroadcastAlertCreated broadcasts an alert creation event
func (h *Hub) BroadcastAlertCreated(tenantID uuid.UUID, alert *models.AlertWithDetails) {
	h.Broadcast(tenantID, "alert_created", alert)
}

// BroadcastAlertAcknowledged broadcasts an alert acknowledgement event
func (h *Hub) BroadcastAlertAcknowledged(tenantID uuid.UUID, alert *models.AlertWithDetails) {
	h.Broadcast(tenantID, "alert_acknowledged", alert)
}

// BroadcastAlertResolved broadcasts an alert resolution event
func (h *Hub) BroadcastAlertResolved(tenantID uuid.UUID, alert *models.AlertWithDetails) {
	h.Broadcast(tenantID, "alert_resolved", alert)
}

// ClientCount returns the number of connected clients for a tenant
func (h *Hub) ClientCount(tenantID uuid.UUID) int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if tenantClients, ok := h.clients[tenantID]; ok {
		return len(tenantClients)
	}
	return 0
}

// TotalClientCount returns the total number of connected clients across all tenants
func (h *Hub) TotalClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	total := 0
	for _, tenantClients := range h.clients {
		total += len(tenantClients)
	}
	return total
}
