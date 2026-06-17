package api

import (
	"net/http"
	"os/exec"
	"runtime"
	"syscall"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// SystemInfo is the response body for GET /api/system.
type SystemInfo struct {
	Version             string `json:"version"`
	GoVersion           string `json:"go_version"`
	UptimeSeconds       int64  `json:"uptime_seconds"`
	BuildDate           string `json:"build_date"`
	GeoOK               bool   `json:"geo_ok"`
	GeoStatus           string `json:"geo_status"`
	GeoDetail           string `json:"geo_detail"`
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
	// MaxWorkers and WatchdogTimeoutSeconds surface the active runtime
	// configuration so operators can verify which value the process is
	// actually using (rather than what they intended to configure).
	// Added v1.5.7 alongside #42 fix so the watchdog tuning is visible
	// without needing to read logs.
	MaxWorkers             int `json:"max_workers"`
	WatchdogTimeoutSeconds int `json:"watchdog_timeout_seconds"`
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
		s := h.status.Snapshot()
		info.FFmpegVersion = s.FFmpegVersion
		info.GeoOK = s.GeoOK
		info.GeoStatus = s.GeoStatus
		info.GeoDetail = s.GeoDetail
		info.GeoCheckedAt = s.GeoCheckedAt
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

	// Active runtime config from the download manager. WatchdogTimeout
	// of zero means the package default is in effect (60s); surface
	// that as 60 so operators see the effective number, not the override
	// presence/absence. Manager is nil in some unit-test wirings; tolerate.
	if h.mgr != nil {
		info.MaxWorkers = h.mgr.MaxWorkers()
		if wd := h.mgr.WatchdogTimeout(); wd > 0 {
			info.WatchdogTimeoutSeconds = int(wd.Seconds())
		} else {
			info.WatchdogTimeoutSeconds = ffmpegPackageDefaultWatchdogSeconds
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

	r2 := h.GeoProbe()
	checkedAt := time.Now().UTC().Format(time.RFC3339)

	if h.status != nil {
		h.status.SetGeo(string(r2.Status), r2.Detail, checkedAt)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"geo_ok":         r2.Status == bbc.GeoUKOK,
		"geo_status":     string(r2.Status),
		"geo_detail":     r2.Detail,
		"geo_checked_at": checkedAt,
	})
}

// appVersion and buildDate may be set via -ldflags at build time.
var (
	appVersion = "dev"
	buildDate  = "unknown"
)

// ffmpegPackageDefaultWatchdogSeconds mirrors the constant in
// internal/download/ffmpeg.go::progressWatchdogTimeout so /api/system
// can surface the effective value when no override is configured.
// Kept here as an int literal so we don't have to cross-package import
// just to compute the seconds; if the package default ever changes,
// update both sites (low risk: covered by manual test plus the v1.5.7
// CHANGELOG entry calls out 60s explicitly).
const ffmpegPackageDefaultWatchdogSeconds = 60
