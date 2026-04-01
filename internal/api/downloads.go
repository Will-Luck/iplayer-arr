package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/GiteaLN/iplayer-arr/internal/store"
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

func (h *Handler) handleListHistory(w http.ResponseWriter, r *http.Request) {
	history, err := h.store.ListHistory()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if history == nil {
		history = []*store.Download{}
	}
	writeJSON(w, http.StatusOK, history)
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
