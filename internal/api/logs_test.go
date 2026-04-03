package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// --- RingBuffer unit tests ---

func TestRingBufferCapacityAndOrder(t *testing.T) {
	rb := NewRingBuffer(3)

	rb.Add(LogEntry{Level: "info", Message: "a"}, nil)
	rb.Add(LogEntry{Level: "info", Message: "b"}, nil)
	rb.Add(LogEntry{Level: "info", Message: "c"}, nil)

	entries := rb.Entries()
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	want := []string{"a", "b", "c"}
	for i, e := range entries {
		if e.Message != want[i] {
			t.Errorf("entry[%d].Message = %q, want %q", i, e.Message, want[i])
		}
	}
}

func TestRingBufferOverflow(t *testing.T) {
	rb := NewRingBuffer(3)

	for i := 0; i < 5; i++ {
		rb.Add(LogEntry{Level: "info", Message: fmt.Sprintf("msg-%d", i)}, nil)
	}

	entries := rb.Entries()
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries after overflow, got %d", len(entries))
	}
	// Oldest entry should be msg-2 (msg-0 and msg-1 were evicted)
	if entries[0].Message != "msg-2" {
		t.Errorf("oldest entry = %q, want msg-2", entries[0].Message)
	}
	if entries[2].Message != "msg-4" {
		t.Errorf("newest entry = %q, want msg-4", entries[2].Message)
	}
}

func TestRingBufferEmpty(t *testing.T) {
	rb := NewRingBuffer(10)
	entries := rb.Entries()
	if len(entries) != 0 {
		t.Errorf("expected 0 entries on empty buffer, got %d", len(entries))
	}
}

func TestRingBufferSingleCapacity(t *testing.T) {
	rb := NewRingBuffer(1)

	rb.Add(LogEntry{Level: "info", Message: "first"}, nil)
	rb.Add(LogEntry{Level: "warn", Message: "second"}, nil)

	entries := rb.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Message != "second" {
		t.Errorf("expected 'second', got %q", entries[0].Message)
	}
}

func TestRingBufferSSEBroadcast(t *testing.T) {
	hub := NewHub()
	ch := hub.Subscribe()
	defer hub.Unsubscribe(ch)

	rb := NewRingBuffer(10)
	entry := LogEntry{Level: "info", Message: "hello broadcast"}
	rb.Add(entry, hub)

	select {
	case ev := <-ch:
		if ev.Type != "log:line" {
			t.Errorf("event type = %q, want log:line", ev.Type)
		}
	default:
		t.Error("expected SSE event but channel was empty")
	}
}

// --- HTTP handler tests ---

func makeLogsHandler(t *testing.T, entries []LogEntry) *Handler {
	t.Helper()
	h, _ := testAPI(t)
	for _, e := range entries {
		h.RingBuf.Add(e, nil)
	}
	return h
}

func TestHandleLogsNoFilter(t *testing.T) {
	entries := []LogEntry{
		{Level: "info", Message: "startup complete"},
		{Level: "warn", Message: "disk space low"},
		{Level: "error", Message: "connection refused"},
	}
	h := makeLogsHandler(t, entries)

	req := httptest.NewRequest(http.MethodGet, "/api/logs?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got []LogEntry
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("expected 3 entries, got %d", len(got))
	}
}

func TestHandleLogsFilterByLevel(t *testing.T) {
	entries := []LogEntry{
		{Level: "info", Message: "ok"},
		{Level: "warn", Message: "watch out"},
		{Level: "error", Message: "bad"},
		{Level: "info", Message: "also ok"},
	}
	h := makeLogsHandler(t, entries)

	req := httptest.NewRequest(http.MethodGet, "/api/logs?apikey=test-api-key&level=info", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got []LogEntry
	json.NewDecoder(w.Body).Decode(&got)
	if len(got) != 2 {
		t.Errorf("expected 2 info entries, got %d", len(got))
	}
}

func TestHandleLogsFilterBySearchTerm(t *testing.T) {
	entries := []LogEntry{
		{Level: "info", Message: "download started for b039d07m"},
		{Level: "info", Message: "connection OK"},
		{Level: "error", Message: "download failed: timeout"},
	}
	h := makeLogsHandler(t, entries)

	req := httptest.NewRequest(http.MethodGet, "/api/logs?apikey=test-api-key&q=download", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got []LogEntry
	json.NewDecoder(w.Body).Decode(&got)
	if len(got) != 2 {
		t.Errorf("expected 2 entries matching 'download', got %d", len(got))
	}
}

func TestHandleLogsFilterCombined(t *testing.T) {
	entries := []LogEntry{
		{Level: "info", Message: "download started"},
		{Level: "error", Message: "download failed"},
		{Level: "info", Message: "unrelated"},
	}
	h := makeLogsHandler(t, entries)

	req := httptest.NewRequest(http.MethodGet, "/api/logs?apikey=test-api-key&level=error&q=download", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got []LogEntry
	json.NewDecoder(w.Body).Decode(&got)
	if len(got) != 1 {
		t.Errorf("expected 1 entry, got %d", len(got))
	}
	if got[0].Level != "error" {
		t.Errorf("level = %q, want error", got[0].Level)
	}
}

func TestHandleLogsEmpty(t *testing.T) {
	h, _ := testAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/api/logs?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got []LogEntry
	json.NewDecoder(w.Body).Decode(&got)
	if len(got) != 0 {
		t.Errorf("expected 0 entries, got %d", len(got))
	}
}
