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
//
// Lines that LOOK like ffmpeg progress (start with `frame=`, `size=`,
// `time=`, `bitrate=`, `out_time=`) are dropped even if parseProgress
// rejected them. Pre-v1.5.6 a progress line that the regex didn't
// recognise (e.g. ffmpeg-8.x emitting `time=` without `size=` for an
// audio-only segment, or a partial flush before the size field
// materialises) would fall through into diagLines and drown the
// real error context in the tail. The diagnostic only matters when
// ffmpeg actually died, so noise that looks like progress is more
// dangerous here than missing one rare warning.
func appendDiagLine(diag []string, line string, max int) []string {
	line = strings.TrimSpace(line)
	if line == "" {
		return diag
	}
	if looksLikeProgressLine(line) {
		return diag
	}
	diag = append(diag, line)
	if len(diag) > max {
		diag = diag[len(diag)-max:]
	}
	return diag
}

// looksLikeProgressLine reports whether line begins with one of
// ffmpeg's progress field prefixes. The check is intentionally
// generous: any line whose first token is a known progress field is
// treated as progress regardless of whether parseProgress could pull
// a full set of values from it. Used by appendDiagLine to keep
// partial-progress noise out of the diagnostic tail. Audit follow-up
// to GitHub issue #40.
var progressFieldPrefixes = []string{
	"frame=",
	"fps=",
	"size=",
	"time=",
	"bitrate=",
	"speed=",
	"out_time=",
	"out_time_ms=",
	"out_time_us=",
	"dup=",
	"drop=",
}

func looksLikeProgressLine(line string) bool {
	for _, prefix := range progressFieldPrefixes {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
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

// ParseProgress is the exported wrapper around parseProgress. Exposed
// in v1.5.7 so the /api/diag/ffmpeg endpoint can assert the
// production regex still matches the current ffmpeg progress shape
// without duplicating the regex into the diag package and inviting
// drift. Returns the parsed progress + a boolean indicating whether
// the line matched both the time= and size= regex groups.
func ParseProgress(line string) (FFmpegProgress, bool) {
	return parseProgress(line)
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

	// TargetHeight is the requested pixel height (e.g. 396, 720, 1080),
	// forwarded to resolveHLSVariant so HLS variant selection honours the
	// requested quality and the unlisted-1080p FHD probe only runs for
	// 1080p requests. Zero means "no constraint" (highest bandwidth + FHD
	// probe), preserving pre-#45 behaviour for callers that don't set it.
	TargetHeight int

	// WatchdogTimeout overrides progressWatchdogTimeout for this run.
	// Zero (the default) falls back to the package constant. Manager
	// sets this from the process-wide configured value (env var
	// IPLAYER_ARR_WATCHDOG_TIMEOUT_SECONDS or store key
	// watchdog_timeout_seconds) so users on busy hosts can extend the
	// gap before the watchdog cancels a slow-but-not-stuck download.
	// Resolves #42.
	WatchdogTimeout time.Duration
}

// effectiveWatchdogTimeout returns the per-job override if set, or
// the package default. Centralised so the watchdog goroutine logic
// stays readable.
func effectiveWatchdogTimeout(job FFmpegJob) time.Duration {
	if job.WatchdogTimeout > 0 {
		return job.WatchdogTimeout
	}
	return progressWatchdogTimeout
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
func resolveHLSVariant(ctx context.Context, prober downloaderFHDProber, masterURL string, targetHeight int) string {
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
	bwRe := regexp.MustCompile(`BANDWIDTH=(\d+)`)
	var variants []hlsVariant
	bestBW := 0
	bestURL := ""
	for i, line := range lines {
		if !strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			continue
		}
		m := bwRe.FindStringSubmatch(line)
		if m == nil || i+1 >= len(lines) {
			continue
		}
		bw, _ := strconv.Atoi(m[1])
		url := strings.TrimSpace(lines[i+1])
		height := 0
		if rm := streamInfResRe.FindStringSubmatch(line); rm != nil {
			height, _ = strconv.Atoi(rm[1])
		}
		variants = append(variants, hlsVariant{bandwidth: bw, height: height, url: url})
		if bw > bestBW {
			bestBW = bw
			bestURL = url
		}
	}

	if bestURL == "" {
		log.Printf("no variant found in master playlist, returning master URL")
		return masterURL
	}

	// Default to the highest-bandwidth variant (historical behaviour),
	// then narrow to the requested height when one was asked for. #45.
	selectedURL := bestURL
	selectedBW := bestBW
	selectedHeight := 0
	if targetHeight > 0 {
		if v, ok := selectVariantByHeight(variants, targetHeight); ok {
			selectedURL = v.url
			selectedBW = v.bandwidth
			selectedHeight = v.height
		}
	}

	resolveBase := func(u string) string {
		if strings.HasPrefix(u, "http") {
			return u
		}
		base := masterURL
		if idx := strings.LastIndex(base, "/"); idx >= 0 {
			base = base[:idx+1]
		}
		return base + u
	}
	selectedURL = resolveBase(selectedURL)
	bestURL = resolveBase(bestURL)

	// Unlisted-1080p upgrade: only when the caller wants 1080p
	// (targetHeight <= 0 means "no constraint / best") AND the height-
	// matched variant is below 1080p, so a listed 1080p-or-higher
	// selection is never downgraded to the hidden 1080p stream. Probe on
	// the highest-bandwidth variant to match ProbeHiddenFHD's selection
	// rule. A sub-1080p request is never silently upgraded. #45.
	wantFHD := (targetHeight <= 0 || targetHeight >= 1080) && selectedHeight < 1080
	if wantFHD && prober != nil && strings.Contains(bestURL, "video=") {
		fhdURL, found, err := prober.ProbeHiddenFHD(ctx, masterURL)
		switch {
		case err != nil:
			log.Printf("1080p probe error: %v", err)
		case found:
			log.Printf("HLS 1080p variant found (unlisted): %s", fhdURL[:min(len(fhdURL), 120)])
			return fhdURL
		}
	}

	log.Printf("HLS variant selected: target=%dp height=%dp bandwidth=%d", targetHeight, selectedHeight, selectedBW)
	return selectedURL
}

// hlsVariant is one #EXT-X-STREAM-INF entry parsed from a master
// playlist: its advertised bandwidth, its RESOLUTION height (0 when the
// variant carries no RESOLUTION tag), and the media-playlist URL.
type hlsVariant struct {
	bandwidth int
	height    int
	url       string
}

// streamInfResRe extracts the height from RESOLUTION=WxH on an
// #EXT-X-STREAM-INF line. #45.
var streamInfResRe = regexp.MustCompile(`RESOLUTION=\d+x(\d+)`)

// selectVariantByHeight returns the variant whose RESOLUTION height best
// matches targetHeight: an exact match (highest bandwidth among ties),
// else the largest height at or below the target, else the smallest
// height above it. Returns ok=false when no variant carries a RESOLUTION
// tag, so the caller keeps the highest-bandwidth default. #45.
func selectVariantByHeight(variants []hlsVariant, targetHeight int) (hlsVariant, bool) {
	var exact, below, above *hlsVariant
	for i := range variants {
		v := &variants[i]
		if v.height <= 0 {
			continue
		}
		switch {
		case v.height == targetHeight:
			if exact == nil || v.bandwidth > exact.bandwidth {
				exact = v
			}
		case v.height < targetHeight:
			if below == nil || v.height > below.height ||
				(v.height == below.height && v.bandwidth > below.bandwidth) {
				below = v
			}
		default:
			if above == nil || v.height < above.height ||
				(v.height == above.height && v.bandwidth > above.bandwidth) {
				above = v
			}
		}
	}
	switch {
	case exact != nil:
		return *exact, true
	case below != nil:
		return *below, true
	case above != nil:
		return *above, true
	default:
		return hlsVariant{}, false
	}
}

// ffmpegReconnectArgs make ffmpeg ride out transient CDN drops (TLS
// "IO error: End of file", "Stream ends prematurely") by reconnecting
// the underlying HTTP(S) transfer instead of aborting the run.
// reconnect_on_network_error needs ffmpeg >= 5.1; the container ships a
// current build. GitHub #46.
var ffmpegReconnectArgs = []string{
	"-reconnect", "1",
	"-reconnect_streamed", "1",
	"-reconnect_on_network_error", "1",
	"-reconnect_delay_max", "30",
}

// buildFFmpegArgs assembles the ffmpeg command line. The reconnect
// options are INPUT options and must precede -i. Split out from
// RunFFmpeg so the arg shape is unit-testable without exec'ing ffmpeg.
func buildFFmpegArgs(streamURL, outputPath string) []string {
	args := []string{
		// "error" is the loudest level that still excludes the per-segment
		// info chatter. HTTP failures, EIO writes, codec rejects, and any
		// other "ffmpeg gave up" messages land in stderr so the diagnostic
		// tail can surface them. GitHub issue #40.
		"-loglevel", "error",
		"-stats",
	}
	args = append(args, ffmpegReconnectArgs...)
	args = append(args,
		"-y",
		"-i", streamURL,
		"-c:v", "copy",
		"-c:a", "copy",
		"-bsf:a", "aac_adtstoasc",
		"-movflags", "faststart",
		outputPath,
	)
	return args
}

func RunFFmpeg(ctx context.Context, job FFmpegJob) error {
	streamURL := job.StreamURL
	// For HLS master playlists, resolve the variant matching the
	// requested height and (for 1080p requests) probe for unlisted
	// 1080p. DASH manifests are handled by ffmpeg.
	if strings.Contains(streamURL, ".m3u8") {
		log.Printf("resolving HLS variant for: %s", streamURL[:min(len(streamURL), 80)])
		streamURL = resolveHLSVariant(ctx, job.FHDProber, streamURL, job.TargetHeight)
		log.Printf("resolved stream URL: %s", streamURL[:min(len(streamURL), 80)])
	} else {
		log.Printf("not HLS, skipping variant resolution: %s", streamURL[:min(len(streamURL), 80)])
	}
	args := buildFFmpegArgs(streamURL, job.OutputPath)

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
	watchdogTimeout := effectiveWatchdogTimeout(job)
	go func() {
		ticker := time.NewTicker(progressWatchdogInterval)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				since := time.Since(time.Unix(0, lastProgressNanos.Load()))
				if since > watchdogTimeout {
					log.Printf("ffmpeg watchdog: no progress in %s (threshold %s); cancelling", since.Round(time.Second), watchdogTimeout)
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
