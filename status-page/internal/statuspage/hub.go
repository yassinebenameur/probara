package statuspage

import "sync"

// SSEEvent represents a server-sent event for status pages.
type SSEEvent struct {
	Type string
	Data string
}

// Hub manages SSE clients grouped by status page slug.
type Hub struct {
	mu      sync.RWMutex
	clients map[string]map[chan SSEEvent]struct{}
}

// NewHub creates a new SSE hub.
func NewHub() *Hub {
	return &Hub{
		clients: make(map[string]map[chan SSEEvent]struct{}),
	}
}

// Register registers a client for a slug and returns its channel.
func (h *Hub) Register(slug string) chan SSEEvent {
	h.mu.Lock()
	defer h.mu.Unlock()

	ch := make(chan SSEEvent, 10)
	if _, ok := h.clients[slug]; !ok {
		h.clients[slug] = make(map[chan SSEEvent]struct{})
	}
	h.clients[slug][ch] = struct{}{}
	return ch
}

// Unregister removes a client channel.
func (h *Hub) Unregister(slug string, ch chan SSEEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.clients[slug]; !ok {
		return
	}
	delete(h.clients[slug], ch)
	close(ch)
	if len(h.clients[slug]) == 0 {
		delete(h.clients, slug)
	}
}

// Broadcast sends an event to clients subscribed to a specific slug.
func (h *Hub) Broadcast(slug string, event SSEEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for ch := range h.clients[slug] {
		select {
		case ch <- event:
		default:
			// Drop if client is slow.
		}
	}
}

// BroadcastAll sends an event to all clients.
func (h *Hub) BroadcastAll(event SSEEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, set := range h.clients {
		for ch := range set {
			select {
			case ch <- event:
			default:
				// Drop if client is slow.
			}
		}
	}
}

// ClientCount returns the number of clients for a slug.
func (h *Hub) ClientCount(slug string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients[slug])
}
