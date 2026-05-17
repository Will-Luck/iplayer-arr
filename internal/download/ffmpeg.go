package download

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// progressWatchdogTimeout is the maximum gap allowed between ffmpeg
// progress emissions before the watchdog cancels the run. ffmpeg
// normally prints a progress line every 1-3 seconds while pulling HLS
// segments, so 60 s of silence is well past any healthy backoff.
// Audit item 11.
const progressWatchdogTimeout = 60 * time.Second

// progressWatchdogInterval is the watchdog's tick period. Short enough
// to detect a stall within ~progressWatchdogTimeout + this, long
// enough to keep the goroutine cheap.
const progressWatchdogInterval = 15 * time.Second

// ffmpegShutdownGrace is how long os/exec waits after sending SIGTERM
// before escalating to SIGKILL. ffmpeg uses this window to flush
// trailing frames + moov atom so the resulting MP4 is playable.
const ffmpegShutdownGrace = 5 * time.Second

// ffmpegStderrTail caps how many of the most recent non-progress
// stderr lines we keep around for failure diagnostics. ffmpeg's exit
// code alone (e.g. "exit status 251") rarely tells the user anything
// actionable; the tail captures the lines ffmpeg emitted right before
// it died (Permission denied, HTTP 403, EIO, etc.) and surfaces them
// in the returned error. GitHub issue #40.
const ffmpegStderrTail = 8

// appendDiagLine adds a non-empty, trimmed line to a ring-style
// buffer capped at max entries. Older lines are evicted when the cap
// is exceeded so callers always see the most recent context.
func appendDiagLine(diag []string, line string, max int) []string {
	line = strings.TrimSpace(line)
	if line == "" {
		return diag
	}
	diag = append(diag, line)
	if len(diag) > max {
		diag = diag[len(diag)-max:]
	}
	return diag
}

// min returns the smaller of a and b.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type FFmpegProgress struct {
	TimeSeconds float64
	SizeBytes   int64
}

var (
	reTime = regexp.MustCompile(`time=(\d+):(\d+):(\d+)\.(\d+)`)
	// ffmpeg 8.x renamed the size unit from kB to KiB. Accept both so
	// parseProgress keeps matching across the version bump; otherwise
	// the watchdog sees no progress and cancels at 60 s.
	reSize = regexp.MustCompile(`size=\s*(\d+)\s*(?:KiB|kB)`)
)

func parseProgress(line string) (FFmpegProgress, bool) {
	var p FFmpegProgress
	tm := reTime.FindStringSubmatch(line)
	sm := reSize.FindStringSubmatch(line)

	if tm == nil || sm == nil {
		return p, false
	}

	h, _ := strconv.ParseFloat(tm[1], 64)
	m, _ := strconv.ParseFloat(tm[2], 64)
	s, _ := strconv.ParseFloat(tm[3], 64)
	ms, _ := strconv.ParseFloat(tm[4], 64)
	p.TimeSeconds = h*3600 + m*60 + s + ms/100

	kb, _ := strconv.ParseInt(sm[1], 10, 64)
	p.SizeBytes = kb * 1024

	return p, true
}

// downloaderFHDProber is the single method resolveHLSVariant needs
// from bbc.Client. Kept as a local interface so ffmpeg_hls_test.go can
// inject a fake without importing bbc. *bbc.Client satisfies this
// automatically via Go's structural typing.
type downloaderFHDProber interface {
	ProbeHiddenFHD(ctx context.Context, hlsMasterURL string) (fhdURL string, found bool, err error)
}

type FFmpegJob struct {
	StreamURL  string
	OutputPath string
	OnProgress func(FFmpegProgress)
	FHDProber  downloaderFHDProber // NEW — satisfied by *bbc.Client
}

// resolveHLSVariant fetches the master playlist, finds the highest-
// bandwidth variant, and delegates FHD probing to the shared helper.
// Falls back to the highest listed variant if the FHD probe returns
// not-found OR any error. The ctx argument is the RunFFmpeg ctx and
// is forwarded to the prober so download cancellation propagates.
//
// NOTE: this function keeps its own master-playlist fetch and bestBW
// selection for v1.1.0 rather than delegating the entire pipeline to
// ProbeHiddenFHD. The duplication is documented in the spec's
// Non-Goals section as an intentional v1.1.0 trade-off; consolidation
// is a follow-up refactor for a later release.
func resolveHLSVariant(ctx context.Context, prober downloaderFHDProber, masterURL string) string {
	resp, err := http.Get(masterURL)
	if err != nil {
		log.Printf("failed to fetch master playlist: %v", err)
		return masterURL
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("failed to read master playlist: %v", err)
		return masterURL
	}
	log.Printf("master playlist fetched: %d bytes, %d lines", len(body), strings.Count(string(body), "\n"))
	log.Printf("master playlist content:\n%s", string(body))

	lines := strings.Split(string(body), "\n")
	bestBW := 0
	bestURL := ""
	bwRe := regexp.MustCompile(`BANDWIDTH=(\d+)`)
	for i, line := range lines {
		if !strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			continue
		}
		if m := bwRe.FindStringSubmatch(line); m != nil {
			bw, _ := strconv.Atoi(m[1])
			if bw > bestBW && i+1 < len(lines) {
				bestBW = bw
				bestURL = strings.TrimSpace(lines[i+1])
			}
		}
	}

	log.Printf("best variant: bw=%d url=%q", bestBW, bestURL)
	if bestURL == "" {
		log.Printf("no variant found in master playlist, returning master URL")
		return masterURL
	}

	// Resolve relative to master playlist base.
	if !strings.HasPrefix(bestURL, "http") {
		base := masterURL
		if idx := strings.LastIndex(base, "/"); idx >= 0 {
			base = base[:idx+1]
		}
		bestURL = base + bestURL
	}

	// Delegate the FHD probe to the shared helper. The prober may be
	// nil in tests or in any future caller that constructs a FFmpegJob
	// without wiring the prober; in that case fall straight through
	// to bestURL.
	if prober != nil && strings.Contains(bestURL, "video=") {
		fhdURL, found, err := prober.ProbeHiddenFHD(ctx, masterURL)
		switch {
		case err != nil:
			log.Printf("1080p probe error: %v", err)
		case found:
			log.Printf("HLS 1080p variant found (unlisted): %s", fhdURL[:min(len(fhdURL), 120)])
			return fhdURL
		}
	}

	log.Printf("HLS variant selected: bandwidth=%d", bestBW)
	return bestURL
}

func RunFFmpeg(ctx context.Context, job FFmpegJob) error {
	streamURL := job.StreamURL
	// For HLS master playlists, resolve the highest-bandwidth variant
	// and probe for unlisted 1080p. DASH manifests are handled by ffmpeg.
	if strings.Contains(streamURL, ".m3u8") {
		log.Printf("resolving HLS variant for: %s", streamURL[:min(len(streamURL), 80)])
		streamURL = resolveHLSVariant(ctx, job.FHDProber, streamURL)
		log.Printf("resolved stream URL: %s", streamURL[:min(len(streamURL), 80)])
	} else {
		log.Printf("not HLS, skipping variant resolution: %s", streamURL[:min(len(streamURL), 80)])
	}
	args := []string{
		// "error" is the loudest level that still excludes the per-segment
		// info chatter. We want HTTP failures, EIO writes, codec rejects,
		// and any other "ffmpeg gave up" messages to land in stderr so
		// the diagnostic tail can surface them. Audit-style fix for
		// GitHub issue #40.
		"-loglevel", "error",
		"-stats",
		"-y",
		"-i", streamURL,
		"-c:v", "copy",
		"-c:a", "copy",
		"-bsf:a", "aac_adtstoasc",
		"-movflags", "faststart",
		job.OutputPath,
	}

	// Derive a cancellable child context so the progress watchdog (and
	// the parent ctx) can both trigger cmd.Cancel via os/exec. Caller
	// cancellation still propagates through ctx.
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	cmd := exec.CommandContext(runCtx, "ffmpeg", args...)

	// Send SIGTERM on cancel rather than the os/exec default SIGKILL,
	// giving ffmpeg a brief grace window to flush its remaining input
	// buffers and write the MP4 moov atom. WaitDelay then escalates to
	// SIGKILL after ffmpegShutdownGrace so a process that ignores the
	// soft signal cannot block worker shutdown. Audit item 11.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = ffmpegShutdownGrace

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	// Progress watchdog. ffmpeg can hang on a segment fetch retry against
	// an unresponsive CDN without exiting; the os/exec context covers
	// caller cancel but not internal stalls. Track the wall time of the
	// last parsed progress line and cancel runCtx if it ages past the
	// threshold. atomic.Int64 keeps the scanner goroutine lock-free.
	var lastProgressNanos atomic.Int64
	lastProgressNanos.Store(time.Now().UnixNano())
	go func() {
		ticker := time.NewTicker(progressWatchdogInterval)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				since := time.Since(time.Unix(0, lastProgressNanos.Load()))
				if since > progressWatchdogTimeout {
					log.Printf("ffmpeg watchdog: no progress in %s; cancelling", since.Round(time.Second))
					cancelRun()
					return
				}
			}
		}
	}()

	// diagLines is a tail of the most recent non-progress stderr lines.
	// When ffmpeg exits non-zero we attach this tail to the error so
	// the user gets a real cause (e.g. "Permission denied" or an HTTP
	// 403 from the CDN) instead of a bare exit code. GitHub issue #40.
	var diagLines []string
	scanner := bufio.NewScanner(stderr)
	scanner.Split(scanFFmpegLines)
	for scanner.Scan() {
		line := scanner.Text()
		if prog, ok := parseProgress(line); ok {
			lastProgressNanos.Store(time.Now().UnixNano())
			if job.OnProgress != nil {
				job.OnProgress(prog)
			}
			continue
		}
		diagLines = appendDiagLine(diagLines, line, ffmpegStderrTail)
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return fmt.Errorf("reading ffmpeg stderr: %w", scanErr)
	}

	if err := cmd.Wait(); err != nil {
		if len(diagLines) > 0 {
			return fmt.Errorf("ffmpeg: %w | stderr: %s", err, strings.Join(diagLines, " | "))
		}
		return fmt.Errorf("ffmpeg: %w", err)
	}
	return nil
}

func scanFFmpegLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' || data[i] == '\r' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func CheckFFmpeg() (string, error) {
	out, err := exec.Command("ffmpeg", "-version").Output()
	if err != nil {
		return "", fmt.Errorf("ffmpeg not found: %w", err)
	}
	lines := strings.Split(string(out), "\n")
	if len(lines) > 0 {
		parts := strings.Fields(lines[0])
		if len(parts) >= 3 {
			return parts[2], nil
		}
		return strings.TrimSpace(lines[0]), nil
	}
	return "unknown", nil
}
