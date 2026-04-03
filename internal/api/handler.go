package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/GiteaLN/iplayer-arr/internal/bbc"
	"github.com/GiteaLN/iplayer-arr/internal/download"
	"github.com/GiteaLN/iplayer-arr/internal/store"
)

// RuntimeStatus holds startup health check results for the status endpoint.
type RuntimeStatus struct {
	FFmpegVersion string
	GeoOK         bool
	GeoCheckedAt  string
}

// Handler is the REST API router for the frontend dashboard.
type Handler struct {
	store  *store.Store
	hub    *Hub
	mgr    *download.Manager
	ibl    *bbc.IBL
	status *RuntimeStatus

	// Fields set after construction (exported so main.go can populate them).
	RingBuf     *RingBuffer
	StartedAt   time.Time
	DownloadDir string
	// GeoProbe, when non-nil, re-runs the BBC geo check and returns true when
	// UK access is confirmed.
	GeoProbe func() bool
}

// NewHandler creates a new API handler.
func NewHandler(st *store.Store, hub *Hub, mgr *download.Manager, ibl *bbc.IBL, status *RuntimeStatus) *Handler {
	return &Handler{
		store:     st,
		hub:       hub,
		mgr:       mgr,
		ibl:       ibl,
		status:    status,
		RingBuf:   NewRingBuffer(1000),
		StartedAt: time.Now(),
	}
}

// ServeHTTP routes requests to the appropriate handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")

	switch {
	case path == "/api/status" && r.Method == "GET":
		h.handleStatus(w, r)
		return
	case path == "/api/events" && r.Method == "GET":
		h.handleEvents(w, r)
		return
	case path == "/api/downloads" && r.Method == "GET":
		h.handleListDownloads(w, r)
	case path == "/api/download" && r.Method == "POST":
		h.handleManualDownload(w, r)
	case path == "/api/history" && r.Method == "GET":
		h.handleListHistory(w, r)
	case path == "/api/history/stats" && r.Method == "GET":
		h.handleHistoryStats(w, r)
	case strings.HasPrefix(path, "/api/history/") && r.Method == "DELETE":
		h.handleDeleteHistory(w, r)
	case path == "/api/config" && r.Method == "GET":
		h.handleGetConfig(w, r)
	case path == "/api/config" && r.Method == "PUT":
		h.handlePutConfig(w, r)
	case path == "/api/overrides" && r.Method == "GET":
		h.handleListOverrides(w, r)
	case strings.HasPrefix(path, "/api/overrides/") && r.Method == "PUT":
		h.handlePutOverride(w, r)
	case strings.HasPrefix(path, "/api/overrides/") && r.Method == "DELETE":
		h.handleDeleteOverride(w, r)
	case path == "/api/search" && r.Method == "GET":
		h.handleSearch(w, r)
	case path == "/api/downloads/directory" && r.Method == "GET":
		h.handleListDirectory(w, r)
	case strings.HasPrefix(path, "/api/downloads/directory/") && r.Method == "DELETE":
		h.handleDeleteDirectory(w, r)
	case path == "/api/pause" && r.Method == "POST":
		h.mgr.Pause()
		writeJSON(w, http.StatusOK, map[string]bool{"paused": true})
	case path == "/api/resume" && r.Method == "POST":
		h.mgr.Resume()
		writeJSON(w, http.StatusOK, map[string]bool{"paused": false})
	case path == "/api/logs" && r.Method == "GET":
		if !h.authenticate(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		h.handleLogs(w, r)
	case path == "/api/system" && r.Method == "GET":
		if !h.authenticate(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		h.handleSystem(w, r)
	case path == "/api/system/geo-check" && r.Method == "POST":
		if !h.authenticate(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		h.handleGeoCheck(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

// authenticate checks the apikey query param or Authorization: Bearer header.
func (h *Handler) authenticate(r *http.Request) bool {
	storedKey, _ := h.store.GetConfig("api_key")
	if storedKey == "" {
		return false
	}

	// Check query param
	if key := r.URL.Query().Get("apikey"); key == storedKey {
		return true
	}

	// Check Authorization header
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		token := strings.TrimPrefix(auth, "Bearer ")
		return token == storedKey
	}

	return false
}

// writeJSON encodes v as JSON and writes it to the response with the given
// status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
