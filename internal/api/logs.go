package api

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// LogEntry is a single structured log line.
type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Message   string `json:"message"`
}

// RingBuffer is a fixed-capacity FIFO buffer for log entries.
type RingBuffer struct {
	mu      sync.Mutex
	entries []LogEntry
	cap     int
	start   int // index of oldest entry
	count   int // number of valid entries
}

// NewRingBuffer returns a RingBuffer with the given capacity.
func NewRingBuffer(capacity int) *RingBuffer {
	return &RingBuffer{
		entries: make([]LogEntry, capacity),
		cap:     capacity,
	}
}

// Add appends a log entry. When the buffer is full, the oldest entry is
// overwritten. After storing, the entry is broadcast as a log:line SSE event
// if a hub is provided.
func (rb *RingBuffer) Add(e LogEntry, hub *Hub) {
	rb.mu.Lock()
	idx := (rb.start + rb.count) % rb.cap
	rb.entries[idx] = e
	if rb.count < rb.cap {
		rb.count++
	} else {
		// overwrite oldest -- advance start pointer
		rb.start = (rb.start + 1) % rb.cap
	}
	rb.mu.Unlock()

	if hub != nil {
		hub.Broadcast("log:line", e)
	}
}

// Entries returns a copy of all stored log entries in insertion order (oldest
// first, newest last).
func (rb *RingBuffer) Entries() []LogEntry {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	out := make([]LogEntry, rb.count)
	for i := 0; i < rb.count; i++ {
		out[i] = rb.entries[(rb.start+i)%rb.cap]
	}
	return out
}

// handleLogs serves GET /api/logs.
//
// Optional query parameters:
//
//	?level=  -- filter by log level (case-insensitive prefix match)
//	?q=      -- filter entries whose message contains the search term
func (h *Handler) handleLogs(w http.ResponseWriter, r *http.Request) {
	level := strings.ToLower(r.URL.Query().Get("level"))
	q := strings.ToLower(r.URL.Query().Get("q"))

	all := h.RingBuf.Entries()

	// If no filters, return all entries directly.
	if level == "" && q == "" {
		writeJSON(w, http.StatusOK, all)
		return
	}

	filtered := make([]LogEntry, 0, len(all))
	for _, e := range all {
		if level != "" && !strings.EqualFold(e.Level, level) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(e.Message), q) {
			continue
		}
		filtered = append(filtered, e)
	}
	writeJSON(w, http.StatusOK, filtered)
}

// nowTimestamp returns the current time formatted as RFC3339.
func nowTimestamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}
