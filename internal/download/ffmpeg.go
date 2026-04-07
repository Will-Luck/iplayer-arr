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
)

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
	reSize = regexp.MustCompile(`size=\s*(\d+)kB`)
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
		"-loglevel", "fatal",
		"-stats",
		"-y",
		"-i", streamURL,
		"-c:v", "copy",
		"-c:a", "copy",
		"-bsf:a", "aac_adtstoasc",
		"-movflags", "faststart",
		job.OutputPath,
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	scanner := bufio.NewScanner(stderr)
	scanner.Split(scanFFmpegLines)
	for scanner.Scan() {
		line := scanner.Text()
		if prog, ok := parseProgress(line); ok && job.OnProgress != nil {
			job.OnProgress(prog)
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return fmt.Errorf("reading ffmpeg stderr: %w", scanErr)
	}

	if err := cmd.Wait(); err != nil {
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
