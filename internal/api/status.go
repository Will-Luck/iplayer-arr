package api

import (
	"net/http"
	"syscall"
)

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

	diskPath := h.DownloadDir
	if diskPath == "" {
		diskPath = "/downloads"
	}
	var diskTotal, diskFree int64
	var stat syscall.Statfs_t
	if err := syscall.Statfs(diskPath, &stat); err == nil {
		diskTotal = int64(stat.Blocks) * stat.Bsize
		diskFree = int64(stat.Bavail) * stat.Bsize
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ffmpeg":         h.status.FFmpegVersion,
		"geo_ok":         h.status.GeoOK,
		"active_workers": activeWorkers,
		"queue_depth":    queueDepth,
		"paused":         h.mgr != nil && h.mgr.IsPaused(),
		"disk_total":     diskTotal,
		"disk_free":      diskFree,
	})
}
