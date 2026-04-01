package api

import "sync"

// Event represents a server-sent event with a type and payload.
type Event struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

// Hub is a pub/sub broadcaster for SSE events. Clients subscribe to
// receive events on a buffered channel and the download worker (or any
// other producer) calls Broadcast to fan out to all subscribers.
type Hub struct {
	mu      sync.RWMutex
	clients map[chan Event]struct{}
}

// NewHub returns a ready-to-use Hub.
func NewHub() *Hub {
	return &Hub{clients: make(map[chan Event]struct{})}
}

// Subscribe registers a new client and returns a buffered event channel.
func (h *Hub) Subscribe() chan Event {
	ch := make(chan Event, 64)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

// Unsubscribe removes a client and closes its channel.
func (h *Hub) Unsubscribe(ch chan Event) {
	h.mu.Lock()
	delete(h.clients, ch)
	close(ch)
	h.mu.Unlock()
}

// Broadcast sends an event to every subscriber. Slow clients that have
// a full buffer are silently skipped (non-blocking send).
func (h *Hub) Broadcast(eventType string, data interface{}) {
	evt := Event{Type: eventType, Data: data}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.clients {
		select {
		case ch <- evt:
		default:
			// drop if client is slow
		}
	}
}
