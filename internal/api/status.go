package api

import "net/http"

func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	downloads, _ := h.store.ListDownloads()

	activeWorkers := 0
	queueDepth := 0
	for _, dl := range downloads {
		switch dl.Status {
		case "downloading", "resolving", "converting":
			activeWorkers++
		case "pending":
			queueDepth++
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ffmpeg":         h.status.FFmpegVersion,
		"geo_ok":         h.status.GeoOK,
		"active_workers": activeWorkers,
		"queue_depth":    queueDepth,
		"paused":         h.mgr != nil && h.mgr.IsPaused(),
	})
}
