package api

import (
	"net/http"
	"os/exec"
	"runtime"
	"syscall"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// SystemInfo is the response body for GET /api/system.
type SystemInfo struct {
	Version             string `json:"version"`
	GoVersion           string `json:"go_version"`
	UptimeSeconds       int64  `json:"uptime_seconds"`
	BuildDate           string `json:"build_date"`
	GeoOK               bool   `json:"geo_ok"`
	GeoCheckedAt        string `json:"geo_checked_at,omitempty"`
	FFmpegVersion       string `json:"ffmpeg_version"`
	FFmpegPath          string `json:"ffmpeg_path"`
	DiskTotal           int64  `json:"disk_total"`
	DiskFree            int64  `json:"disk_free"`
	DiskPath            string `json:"disk_path"`
	DownloadsCompleted  int    `json:"downloads_completed"`
	DownloadsFailed     int    `json:"downloads_failed"`
	DownloadsTotalBytes int64  `json:"downloads_total_bytes"`
	LastIndexerRequest  string `json:"last_indexer_request,omitempty"`
}

// handleSystem serves GET /api/system.
func (h *Handler) handleSystem(w http.ResponseWriter, r *http.Request) {
	info := SystemInfo{
		Version:   appVersion,
		GoVersion: runtime.Version(),
		BuildDate: buildDate,
	}

	info.UptimeSeconds = int64(time.Since(h.StartedAt).Seconds())

	// Geo status from runtime status.
	if h.status != nil {
		ffmpeg, geoOK, geoCheckedAt := h.status.Snapshot()
		info.FFmpegVersion = ffmpeg
		info.GeoOK = geoOK
		info.GeoCheckedAt = geoCheckedAt
	}

	// FFmpeg binary path.
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		info.FFmpegPath = p
	}

	// Disk stats for the download directory.
	diskPath := h.DownloadDir
	if diskPath == "" {
		diskPath = "/downloads"
	}
	info.DiskPath = diskPath
	var stat syscall.Statfs_t
	if err := syscall.Statfs(diskPath, &stat); err == nil {
		info.DiskTotal = int64(stat.Blocks) * stat.Bsize
		info.DiskFree = int64(stat.Bavail) * stat.Bsize
	}

	// History stats.
	if history, err := h.store.ListHistory(); err == nil {
		for _, dl := range history {
			switch dl.Status {
			case store.StatusCompleted:
				info.DownloadsCompleted++
				info.DownloadsTotalBytes += dl.Size
			case store.StatusFailed:
				info.DownloadsFailed++
			}
		}
	}

	// Last time Sonarr (or any Newznab client) queried the indexer.
	if v := h.lastIndexerRequest.Load(); v != nil {
		if t, ok := v.(time.Time); ok && !t.IsZero() {
			info.LastIndexerRequest = t.Format(time.RFC3339)
		}
	}

	writeJSON(w, http.StatusOK, info)
}

// handleGeoCheck serves POST /api/system/geo-check.
// It re-runs the BBC geo-probe via the stored geoProbe function and updates h.status.
func (h *Handler) handleGeoCheck(w http.ResponseWriter, r *http.Request) {
	if h.GeoProbe == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "geo probe not available"})
		return
	}

	geoOK := h.GeoProbe()
	checkedAt := time.Now().UTC().Format(time.RFC3339)

	if h.status != nil {
		h.status.SetGeo(geoOK, checkedAt)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"geo_ok":         geoOK,
		"geo_checked_at": checkedAt,
	})
}

// appVersion and buildDate may be set via -ldflags at build time.
var (
	appVersion = "dev"
	buildDate  = "unknown"
)
