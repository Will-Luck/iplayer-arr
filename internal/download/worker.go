package download

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/store"
	"golang.org/x/sys/unix"
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
			(dl.Status == store.StatusFailed && dl.Retryable && dl.RetryCount < maxRetries &&
				(dl.RetryAfter.IsZero() || time.Now().After(dl.RetryAfter)))
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

	if err := EnsureDownloadDir(dl.OutputDir); err != nil {
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
		FHDProber: m.client, // NEW — *bbc.Client satisfies downloaderFHDProber
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

	// 5. Stat the output file once; both the truncation gate and
	// the post-completion size update consume actualSize.
	statInfo, statErr := os.Stat(outputFile)
	if statErr != nil {
		m.failDownload(dl, store.FailCodeFFmpeg, fmt.Errorf("stat output: %w", statErr))
		return
	}
	actualSize := statInfo.Size()

	// 6. Probe the actual encoded height. Failure is non-fatal; the
	// truncation gate will fall back to the requested-quality
	// estimate, preserving v1.1.7 behaviour (including its
	// false-positive risk on quality downgrades).
	actualHeight, probeErr := probeActualHeight(ctx, outputFile)
	actualQual := ""
	if probeErr != nil {
		log.Printf("download %s: ffprobe failed: %v (truncation gate will use requested quality)", dl.ID, probeErr)
	} else {
		actualQual = heightToQualityTag(actualHeight)
	}

	// 7. Truncation gate using the actual-quality threshold (or
	// requested-quality fallback when actualQual is empty).
	if threshold := truncationThreshold(dl.Duration, dl.Quality, actualQual); threshold > 0 && actualSize < threshold {
		thresholdQ := actualQual
		if thresholdQ == "" {
			thresholdQ = dl.Quality
		}
		log.Printf("download %s truncated: %d bytes actual, threshold %d (%s)",
			dl.ID, actualSize, threshold, thresholdQ)
		m.failDownload(dl, store.FailCodeTruncated, fmt.Errorf(
			"output file truncated: %d bytes, expected at least %d (30%% of estimate at %s)",
			actualSize, threshold, thresholdQ,
		))
		os.Remove(outputFile)
		return
	}

	// 8. Reconciliation block: when ffprobe disagrees with the
	// requested quality, atomically relocate the file/dir to a
	// truthful name and update dl.ActualQuality. On any failure,
	// dl.ActualQuality is still set so the frontend shows truth.
	m.reconcileDownload(dl, actualQual)

	// 9. Download subtitles (best-effort)
	if streams.SubtitleURL != "" {
		m.downloadSubtitles(streams.SubtitleURL, dl.OutputDir, dl.Title)
	}

	// 10. Atomic-rename the staging directory out of incomplete/ so the
	// final folder appears only when everything succeeded. Failure leaves
	// the file in incomplete/ and is logged; the SAB history slot will
	// then point at the staging path rather than fail import. Issue #29.
	if err := m.finaliseDownload(dl); err != nil {
		log.Printf("download %s: finalise failed, leaving file in incomplete/: %v", dl.ID, err)
	}

	// 11. Complete -- move straight to history. The SABnzbd history endpoint
	// returns these as Completed so Sonarr can see them and trigger import.
	// Previously we slept 90s in the downloads bucket, but Sonarr's delete
	// request would race and wipe the record before MoveToHistory ran.
	dl.Size = actualSize
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
// GeoBlocked and Expired are permanent; everything else retries with
// exponential backoff (30s, 90s, 270s) to avoid hammering the BBC CDN.
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

	if dl.Retryable {
		backoff := 30 * time.Second
		for i := 1; i < dl.RetryCount; i++ {
			backoff *= 3
		}
		dl.RetryAfter = time.Now().Add(backoff)
	}

	if storeErr := m.store.PutDownload(dl); storeErr != nil {
		log.Printf("store failDownload: %v", storeErr)
	}

	if !dl.Retryable {
		dl.CompletedAt = time.Now()
		m.store.PutDownload(dl)
		m.store.MoveToHistory(dl.ID)
	}

	m.broadcast("download:failed", dl)
	if dl.Retryable {
		log.Printf("download %s failed (%s): %v [retry %d/%d, backoff %v]", dl.ID, code, err, dl.RetryCount, maxRetries, time.Until(dl.RetryAfter).Round(time.Second))
	} else {
		log.Printf("download %s failed (%s): %v [permanent, count=%d]", dl.ID, code, err, dl.RetryCount)
	}
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

	// Tag as English so Plex/Jellyfin label the track correctly. BBC iPlayer
	// only ships English captions on this CDN, so a fixed code is safe.
	srtPath := filepath.Join(outputDir, sanitiseFilename(title)+".en.srt")
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

// heightToQualityTag converts an encoded video height to one of the
// project's Newznab quality tags using the same cutoff ladder as
// internal/newznab/search.go::heightsToTags. Returns "" for heights
// below the lowest tier (396), so callers can distinguish "no tag" from
// any real bucket. The 480 -> 396p mapping is intentional: the
// codebase taxonomy is ["1080p", "720p", "540p", "396p"] with no
// literal 480p tier.
func heightToQualityTag(h int) string {
	switch {
	case h >= 2160:
		return "2160p"
	case h >= 1080:
		return "1080p"
	case h >= 720:
		return "720p"
	case h >= 540:
		return "540p"
	case h >= 396:
		return "396p"
	default:
		return ""
	}
}

// reconcileTitle swaps the requested-quality tag for the actual-quality
// tag in a release-style title. Anchored on the ".WEB-DL." marker so
// stray occurrences of the old quality string elsewhere in the title
// (e.g. as part of the show name) are left alone. Empty oldQ or a
// title without ".WEB-DL." returns the input unchanged so the caller
// can use the result == input check as a "no rename needed" signal.
func reconcileTitle(title, oldQ, newQ string) string {
	if oldQ == "" || newQ == "" || oldQ == newQ {
		return title
	}
	oldToken := "." + oldQ + ".WEB-DL."
	newToken := "." + newQ + ".WEB-DL."
	if !strings.Contains(title, oldToken) {
		return title
	}
	return strings.Replace(title, oldToken, newToken, 1)
}

// truncationThreshold returns the bytes-on-disk threshold below which
// a download is considered truncated. Prefers actualQuality (post-
// ffprobe truth) and falls back to requestedQuality when actualQuality
// is empty (ffprobe failed). Uses the existing 30% slack.
func truncationThreshold(durationSecs int, requestedQuality, actualQuality string) int64 {
	q := actualQuality
	if q == "" {
		q = requestedQuality
	}
	expected := estimateSize(durationSecs, q)
	if expected <= 0 {
		return 0
	}
	return expected * 30 / 100
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

// reconcileDownload mutates dl in place to reflect post-ffprobe truth.
// When actualQual disagrees with dl.Quality and the title contains a
// reconcilable WEB-DL token, it atomically relocates the file into a
// correctly-named sibling directory using relocateNoReplace. On any
// rename failure (including a pre-existing target) it logs and leaves
// the file in place; dl.ActualQuality is set unconditionally so the
// frontend shows truth even when the rename was skipped.
func (m *Manager) reconcileDownload(dl *store.Download, actualQual string) {
	if actualQual == "" {
		return // ffprobe failed earlier; nothing to reconcile
	}
	if actualQual == dl.Quality {
		dl.ActualQuality = actualQual // record truth for uniform frontend display
		return
	}
	dl.ActualQuality = actualQual

	newTitle := reconcileTitle(dl.Title, dl.Quality, actualQual)
	if newTitle == dl.Title {
		log.Printf("download %s: title %q does not contain reconcilable WEB-DL token; ActualQuality=%s, file kept in place",
			dl.ID, dl.Title, actualQual)
		return
	}

	newDirBase := sanitiseFilename(newTitle)
	newDir := filepath.Join(filepath.Dir(dl.OutputDir), newDirBase)
	newFilePath := filepath.Join(newDir, newDirBase+".mp4")

	createdNewDir := false
	if _, err := os.Stat(newDir); os.IsNotExist(err) {
		if mkErr := EnsureDownloadDir(newDir); mkErr != nil {
			log.Printf("download %s: EnsureDownloadDir %s failed: %v (skipping rename)", dl.ID, newDir, mkErr)
			return
		}
		createdNewDir = true
	} else if err != nil {
		log.Printf("download %s: cannot stat target dir %s: %v (skipping rename)", dl.ID, newDir, err)
		return
	}

	err := relocateNoReplace(dl.OutputFile, newFilePath)
	switch {
	case err == nil:
		_ = os.Remove(dl.OutputDir) // best-effort cleanup of now-empty old dir
		log.Printf("download %s reconciled: requested %s, actual %s, relocated to %s",
			dl.ID, dl.Quality, actualQual, newDir)
		dl.Title = newTitle
		dl.OutputDir = newDir
		dl.OutputFile = newFilePath
	case errors.Is(err, unix.EEXIST):
		log.Printf("download %s: cannot relocate, %s already exists (skipping rename)", dl.ID, newFilePath)
		if createdNewDir {
			_ = os.Remove(newDir)
		}
	default:
		log.Printf("download %s: relocate to %s failed: %v", dl.ID, newFilePath, err)
		if createdNewDir {
			_ = os.Remove(newDir)
		}
	}
}

// relocateDeps bundles the syscall-and-side-effect primitives used by
// relocateNoReplaceWith so tests can inject mocks for each branch.
// Production callers use unix.Renameat2 / os.Stat / os.Rename / log.Printf
// via the relocateNoReplace wrapper.
type relocateDeps struct {
	renameat2 func(srcDirFD int, src string, dstDirFD int, dst string, flags uint) error
	stat      func(string) (os.FileInfo, error)
	rename    func(src, dst string) error
	logf      func(format string, args ...any)
}

// relocateNoReplaceWith attempts an atomic move that fails (EEXIST) if
// dst already exists. Tries unix.Renameat2(RENAME_NOREPLACE) first;
// falls back to os.Stat + os.Rename when the underlying filesystem
// doesn't support RENAME_NOREPLACE (the kernel returns EINVAL on
// unsupported filesystems, ENOSYS on kernels < 3.15).
//
// The fast path is kernel-atomic and race-free against concurrent
// racers. The fallback is best-effort: it offers "no overwrite under
// happy-path Stat observation" but has a residual Stat -> Rename TOCTOU
// window during which a concurrent racer could create dst. We accept
// this because (a) the only realistic racer in iplayer-arr is another
// worker thread reconciling the same (PID, actualQual) to the same
// dst, where the racing files are byte-equivalent (benign-overwrite-
// equivalence), and (b) the fallback path emits a distinguishing log
// line so operators on exotic filesystems can detect the degraded
// mode via log monitoring.
//
// The Linux man page (renameat2(2), current as of 2026-05) explicitly
// lists ext4 (>=3.15), btrfs/tmpfs/cifs (>=3.17), xfs (>=4.0), and
// "many other filesystems" added in 4.9 (ext2, minix, reiserfs, jfs,
// vfat, bpf). NFS and overlayfs are NOT in the explicit list - support
// there is empirically determined per kernel/server combination and
// must be confirmed by smoke-test, not assumed.
func relocateNoReplaceWith(deps relocateDeps, src, dst string) error {
	err := deps.renameat2(unix.AT_FDCWD, src, unix.AT_FDCWD, dst, unix.RENAME_NOREPLACE)
	if err == nil || errors.Is(err, unix.EEXIST) {
		return err
	}
	if !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.ENOSYS) {
		return err
	}
	deps.logf("relocateNoReplace: Renameat2 returned %v, falling back to Stat+Rename for %s", err, dst)
	if _, statErr := deps.stat(dst); statErr == nil {
		return unix.EEXIST
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	return deps.rename(src, dst)
}

// relocateNoReplace is the production wrapper: binds unix.Renameat2 /
// os.Stat / os.Rename / log.Printf into relocateNoReplaceWith.
func relocateNoReplace(src, dst string) error {
	return relocateNoReplaceWith(relocateDeps{
		renameat2: unix.Renameat2,
		stat:      os.Stat,
		rename:    os.Rename,
		logf:      log.Printf,
	}, src, dst)
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
