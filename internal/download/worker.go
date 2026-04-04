package download

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/store"
)

const maxRetries = 3

// worker polls for pending or retryable downloads every second.
func (m *Manager) worker(ctx context.Context, id int) {
	log.Printf("worker %d started", id)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("worker %d stopping", id)
			return
		case <-ticker.C:
			if !m.paused.Load() {
				m.processNext(ctx, id)
			}
		}
	}
}

// processNext finds the next pending or retryable-failed download and processes it.
func (m *Manager) processNext(ctx context.Context, workerID int) {
	downloads, err := m.store.ListDownloads()
	if err != nil {
		log.Printf("worker %d: list downloads: %v", workerID, err)
		return
	}

	for _, dl := range downloads {
		claimable := dl.Status == store.StatusPending ||
			(dl.Status == store.StatusFailed && dl.Retryable && dl.RetryCount < maxRetries)
		if !claimable {
			continue
		}
		dlCtx, dlCancel := context.WithCancel(ctx)
		if !m.claim(dl.ID, dlCancel) {
			dlCancel()
			continue
		}
		if dl.Status == store.StatusPending {
			log.Printf("worker %d: picking up pending download %s (%s)", workerID, dl.ID, dl.PID)
		} else {
			log.Printf("worker %d: retrying failed download %s (%s), attempt %d", workerID, dl.ID, dl.PID, dl.RetryCount+1)
		}
		m.processDownload(dlCtx, dl)
		dlCancel()
		m.release(dl.ID)
		return
	}
}

// processDownload runs the full pipeline: resolve -> media select -> ffmpeg -> subtitles -> history.
func (m *Manager) processDownload(ctx context.Context, dl *store.Download) {
	// 1. Resolve playlist
	m.setStatus(dl, store.StatusResolving, "")
	info, err := m.playlist.Resolve(dl.PID)
	if err != nil {
		m.failDownload(dl, store.FailCodeUnavailable, fmt.Errorf("playlist resolve: %w", err))
		return
	}

	dl.VPID = info.VPID
	dl.Duration = info.Duration
	if dl.Title == "" {
		dl.Title = info.Title
	}
	dl.Size = estimateSize(info.Duration, dl.Quality)
	if err := m.store.PutDownload(dl); err != nil {
		log.Printf("store update after playlist: %v", err)
	}
	m.broadcast("download:status", dl)

	// 2. Resolve media selector
	streams, err := m.ms.Resolve(info.VPID)
	if err != nil {
		if bbc.IsGeoBlocked(err) {
			m.failDownload(dl, store.FailCodeGeoBlocked, err)
		} else {
			m.failDownload(dl, store.FailCodeUnavailable, err)
		}
		return
	}

	if len(streams.Video) == 0 {
		m.failDownload(dl, store.FailCodeUnavailable, fmt.Errorf("no video streams for %s", info.VPID))
		return
	}

	// 3. Pick stream matching requested quality
	for _, s := range streams.Video {
		log.Printf("available stream: %dx%d %dkbps fmt=%s supplier=%s", s.Width, s.Height, s.Bitrate, s.Format, s.Supplier)
	}
	stream := pickStream(streams.Video, dl.Quality)
	log.Printf("picked stream: %dx%d %dkbps fmt=%s (requested %s)", stream.Width, stream.Height, stream.Bitrate, stream.Format, dl.Quality)
	dl.StreamURL = stream.URL
	if err := m.store.PutDownload(dl); err != nil {
		log.Printf("store update after stream pick: %v", err)
	}

	// 4. Download via ffmpeg
	m.setStatus(dl, store.StatusDownloading, "")
	dl.StartedAt = time.Now()

	if err := os.MkdirAll(dl.OutputDir, 0o755); err != nil {
		m.failDownload(dl, store.FailCodeFFmpeg, fmt.Errorf("create output dir: %w", err))
		return
	}

	outputFile := filepath.Join(dl.OutputDir, sanitiseFilename(dl.Title)+".mp4")
	dl.OutputFile = outputFile
	if err := m.store.PutDownload(dl); err != nil {
		log.Printf("store update before ffmpeg: %v", err)
	}

	lastBroadcast := time.Time{}
	job := FFmpegJob{
		StreamURL:  stream.URL,
		OutputPath: outputFile,
		OnProgress: func(p FFmpegProgress) {
			dl.Downloaded = p.SizeBytes
			if dl.Duration > 0 {
				dl.Progress = (p.TimeSeconds / float64(dl.Duration)) * 100
				if dl.Progress > 100 {
					dl.Progress = 100
				}
			}
			_ = m.store.PutDownload(dl)

			// Throttle broadcasts to every 2 seconds
			if time.Since(lastBroadcast) >= 2*time.Second {
				lastBroadcast = time.Now()
				m.broadcast("download:progress", dl)
			}
		},
	}

	ffErr := RunFFmpeg(ctx, job)
	if ffErr != nil {
		if ctx.Err() != nil {
			if m.IsCancelled(dl.ID) {
				m.clearCancelled(dl.ID)
				log.Printf("download %s cancelled by user, not returning to pending", dl.ID)
				return
			}
			m.setStatus(dl, store.StatusPending, "")
			log.Printf("download %s returned to pending (context cancelled)", dl.ID)
			return
		}
		m.failDownload(dl, store.FailCodeFFmpeg, ffErr)
		return
	}

	// 5. Download subtitles (best-effort)
	if streams.SubtitleURL != "" {
		m.downloadSubtitles(streams.SubtitleURL, dl.OutputDir, dl.Title)
	}

	// 6. Complete -- move straight to history. The SABnzbd history endpoint
	// returns these as Completed so Sonarr can see them and trigger import.
	// Previously we slept 90s in the downloads bucket, but Sonarr's delete
	// request would race and wipe the record before MoveToHistory ran.
	dl.Status = store.StatusCompleted
	dl.Progress = 100
	dl.CompletedAt = time.Now()
	if err := m.store.PutDownload(dl); err != nil {
		log.Printf("store update on complete: %v", err)
	}
	m.broadcast("download:complete", dl)

	if err := m.store.MoveToHistory(dl.ID); err != nil {
		log.Printf("move to history: %v", err)
	}

	log.Printf("download %s completed: %s", dl.ID, dl.Title)
}

// setStatus updates a download's status and persists + broadcasts.
func (m *Manager) setStatus(dl *store.Download, status, errMsg string) {
	dl.Status = status
	dl.Error = errMsg
	if err := m.store.PutDownload(dl); err != nil {
		log.Printf("store setStatus: %v", err)
	}
	m.broadcast("download:status", dl)
}

// failDownload marks a download as failed with the given failure code.
// GeoBlocked and Expired are not retryable; others are retryable up to maxRetries.
func (m *Manager) failDownload(dl *store.Download, code string, err error) {
	dl.Status = store.StatusFailed
	dl.FailureCode = code
	dl.Error = err.Error()
	dl.RetryCount++

	switch code {
	case store.FailCodeGeoBlocked, store.FailCodeExpired:
		dl.Retryable = false
	default:
		dl.Retryable = dl.RetryCount < maxRetries
	}

	if storeErr := m.store.PutDownload(dl); storeErr != nil {
		log.Printf("store failDownload: %v", storeErr)
	}

	// Non-retryable failures move to history so the frontend can display them
	if !dl.Retryable {
		dl.CompletedAt = time.Now()
		m.store.PutDownload(dl)
		m.store.MoveToHistory(dl.ID)
	}

	m.broadcast("download:failed", dl)
	log.Printf("download %s failed (%s): %v [retry=%v count=%d]", dl.ID, code, err, dl.Retryable, dl.RetryCount)
}

// downloadSubtitles fetches TTML subtitles from the BBC and converts to SRT.
// Failures are logged but do not fail the download.
func (m *Manager) downloadSubtitles(subURL, outputDir, title string) {
	body, err := m.client.Get(subURL)
	if err != nil {
		log.Printf("subtitle download failed (continuing): %v", err)
		return
	}

	srt, err := bbc.TTMLToSRT(body)
	if err != nil {
		log.Printf("subtitle conversion failed (continuing): %v", err)
		return
	}

	srtPath := filepath.Join(outputDir, sanitiseFilename(title)+".srt")
	if err := os.WriteFile(srtPath, srt, 0o644); err != nil {
		log.Printf("subtitle write failed (continuing): %v", err)
		return
	}

	log.Printf("subtitles saved: %s", srtPath)
}

// broadcast sends a typed event to SSE subscribers if a hub is connected.
func (m *Manager) broadcast(eventType string, dl *store.Download) {
	if m.hub == nil {
		return
	}
	m.hub.Broadcast(eventType, dl)
}

// claim attempts to reserve a download for this worker. Returns false if
// another worker has already claimed it, preventing duplicate processing.
func (m *Manager) claim(id string, cancel context.CancelFunc) bool {
	m.claimMu.Lock()
	defer m.claimMu.Unlock()
	if _, ok := m.claimed[id]; ok {
		return false
	}
	m.claimed[id] = cancel
	return true
}

// release removes a download from the claimed set after processing.
func (m *Manager) release(id string) {
	m.claimMu.Lock()
	defer m.claimMu.Unlock()
	delete(m.claimed, id)
}

// pickStream selects the stream matching the requested quality string.
// Quality strings are like "720p", "1080p", "480p". If no exact match,
// pick the closest available stream (preferring the best quality).
func pickStream(streams []bbc.VideoStream, quality string) bbc.VideoStream {
	targetHeight := qualityToHeight(quality)

	// Prefer HLS for exact height match -- our resolveHLSVariant can probe
	// for unlisted 1080p variants that BBC hides from manifests.
	for _, s := range streams {
		if s.Height == targetHeight && s.Format == "hls" {
			return s
		}
	}

	// Fall back to any format with exact height match
	for _, s := range streams {
		if s.Height == targetHeight {
			return s
		}
	}

	// Closest match, preferring HLS
	best := streams[0]
	bestDiff := abs(best.Height - targetHeight)
	for _, s := range streams[1:] {
		diff := abs(s.Height - targetHeight)
		if diff < bestDiff || (diff == bestDiff && s.Format == "hls") {
			best = s
			bestDiff = diff
		}
	}
	return best
}

// qualityToHeight converts a quality string like "720p" to a pixel height.
func qualityToHeight(q string) int {
	q = strings.TrimSuffix(strings.ToLower(q), "p")
	h, err := strconv.Atoi(q)
	if err != nil {
		return 720 // default
	}
	return h
}

// estimateSize estimates the download size in bytes based on duration and quality.
// Uses rough bitrate estimates: 1080p ~5Mbps, 720p ~2.5Mbps, 480p ~1.2Mbps.
func estimateSize(durationSecs int, quality string) int64 {
	height := qualityToHeight(quality)

	var bitsPerSecond int64
	switch {
	case height >= 1080:
		bitsPerSecond = 5_000_000
	case height >= 720:
		bitsPerSecond = 2_500_000
	case height >= 480:
		bitsPerSecond = 1_200_000
	default:
		bitsPerSecond = 800_000
	}

	return (bitsPerSecond * int64(durationSecs)) / 8
}

// sanitiseFilename replaces characters that are unsafe in filenames.
func sanitiseFilename(name string) string {
	replacer := strings.NewReplacer(
		"/", "-",
		"\\", "-",
		":", " -",
		"*", "",
		"?", "",
		"\"", "",
		"<", "",
		">", "",
		"|", "",
	)
	return replacer.Replace(name)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
