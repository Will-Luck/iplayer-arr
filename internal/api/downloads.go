package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/Will-Luck/iplayer-arr/internal/store"
)

func (h *Handler) handleListDownloads(w http.ResponseWriter, r *http.Request) {
	downloads, err := h.store.ListDownloads()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if downloads == nil {
		downloads = []*store.Download{}
	}
	writeJSON(w, http.StatusOK, downloads)
}

// handleListHistory serves GET /api/history with optional filtering, sorting,
// and pagination.
//
// Query params:
//
//	?status=   -- "completed" or "failed" (default: all)
//	?since=    -- ISO date or RFC3339 timestamp (default: all time)
//	?page=     -- 1-based page number (default: 1)
//	?per_page= -- entries per page (default: 20)
//	?sort=     -- "completed_at" or "title" (default: "completed_at")
//	?order=    -- "asc" or "desc" (default: "desc")
func (h *Handler) handleListHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	f := store.HistoryFilter{
		Status:  q.Get("status"),
		Since:   q.Get("since"),
		Sort:    q.Get("sort"),
		Order:   q.Get("order"),
	}

	if v := q.Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Page = n
		}
	}
	if v := q.Get("per_page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.PerPage = n
		}
	}

	page, err := h.store.ListHistoryFiltered(f)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Ensure Items is never null in JSON.
	if page.Items == nil {
		page.Items = []*store.Download{}
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) handleClearHistory(w http.ResponseWriter, r *http.Request) {
	n, err := h.store.ClearHistory()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"deleted": n})
}

func (h *Handler) handleDeleteHistory(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/history/")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing id"})
		return
	}
	if err := h.store.DeleteHistory(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleHistoryStats serves GET /api/history/stats.
//
// Optional query param:
//
//	?since= -- ISO date or RFC3339 timestamp
//
// Response: {"completed": N, "failed": N, "total_bytes": N}
func (h *Handler) handleHistoryStats(w http.ResponseWriter, r *http.Request) {
	since := r.URL.Query().Get("since")

	// Reuse ListHistoryFiltered for since-filtered access.
	// Request a large page to get all entries; total is accurate regardless.
	page, err := h.store.ListHistoryFiltered(store.HistoryFilter{
		Since:   since,
		Page:    1,
		PerPage: 1<<31 - 1,
		Sort:    "completed_at",
		Order:   "desc",
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	var completed, failed int
	var totalBytes int64
	for _, dl := range page.Items {
		switch dl.Status {
		case store.StatusCompleted:
			completed++
			totalBytes += dl.Size
		case store.StatusFailed:
			failed++
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"completed":   completed,
		"failed":      failed,
		"total_bytes": totalBytes,
	})
}

func (h *Handler) handleManualDownload(w http.ResponseWriter, r *http.Request) {
	if h.mgr == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "downloads disabled"})
		return
	}

	var req struct {
		PID      string `json:"pid"`
		Quality  string `json:"quality"`
		Title    string `json:"title"`
		Category string `json:"category"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.PID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pid is required"})
		return
	}
	if req.Quality == "" {
		req.Quality = "720p"
	}
	if req.Category == "" {
		req.Category = "manual"
	}

	id, err := h.mgr.Enqueue(req.PID, req.Quality, req.Title, req.Category)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}
