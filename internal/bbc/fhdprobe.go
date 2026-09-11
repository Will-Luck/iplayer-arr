package bbc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// fhdVariantRewrite matches video=<digits> in a variant URL so we can
// swap it for video=12000000 (BBC's unlisted 1080p path). Compiled
// once to avoid paying the regex compile cost per call.
var fhdVariantRewrite = regexp.MustCompile(`video=\d+`)

// resHeightRe extracts the height from RESOLUTION=WxH in #EXT-X-STREAM-INF.
var resHeightRe = regexp.MustCompile(`RESOLUTION=\d+x(\d+)`)

// maxPlaylistHeight scans an HLS master playlist body for RESOLUTION
// tags and returns the largest height value found. Returns 0 if no
// RESOLUTION tags are present (older playlists or test fixtures may
// omit them), which callers should treat as "unknown, skip the guard".
func maxPlaylistHeight(body []byte) int {
	maxH := 0
	for _, m := range resHeightRe.FindAllSubmatch(body, -1) {
		if h, err := strconv.Atoi(string(m[1])); err == nil && h > maxH {
			maxH = h
		}
	}
	return maxH
}

// ProbeHiddenFHD reports whether BBC hosts an unlisted video=12000000
// 1080p variant for the given HLS master playlist URL. Fetches the
// master playlist, picks the highest-BANDWIDTH variant URL (matching
// the downloader's resolveHLSVariant selection rule byte-for-byte),
// rewrites its video=N segment to video=12000000, and HEAD-probes
// the result. Honours ctx throughout.
//
// Returns:
//   - (fhdURL, true,  nil) on HEAD HTTP 200 — caller gets the concrete 1080p URL.
//   - ("",     false, nil) for the definitive-absence cases:
//     (a) HEAD returns 404 or 410 — BBC has no 1080p for this programme.
//     (b) Master playlist parses but has no video=N variants (DASH-only
//     or weird format) — the rewrite cannot apply, and never will.
//     Both are safe to cache forever. See the spec for the caching contract.
//   - ("",     false, err) for every other failure mode: 429, 5xx, 401/403,
//     transport errors, context cancellation, master-playlist fetch failures,
//     or parse failures. Callers that cache must NOT cache this branch.
//
// The selection rule MUST match internal/download/ffmpeg.go:80-93 exactly,
// otherwise the downloader's existing 1080p detection can silently regress.
// The downloader code walks every #EXT-X-STREAM-INF line, tracks bestBW
// and bestURL, and rewrites ONLY the highest-BANDWIDTH variant. Do not
// shortcut this.
func (c *Client) ProbeHiddenFHD(ctx context.Context, hlsMasterURL string) (fhdURL string, found bool, err error) {
	body, err := c.GetCtx(ctx, hlsMasterURL)
	if err != nil {
		return "", false, fmt.Errorf("fetch master playlist: %w", err)
	}
	return c.probeHiddenFHDIn(ctx, hlsMasterURL, body)
}

// VerifyFHD reports whether BBC can actually serve a 1080p rendition for
// the given HLS master playlist, and the highest RESOLUTION height the
// master lists (0 when it carries no RESOLUTION tags).
//
// It exists because mediaselector's height attribute cannot be trusted
// for this: on the iptv-all mediaset the single video element reports
// the packaging profile (1920x1080 for piff_abr_full_hd), not an
// encoded rendition. Waterloo Road S06E02 reports 1080 there while its
// master tops out at 1280x720 and the video=12000000 rewrite serves
// audio only. GitHub #54.
//
// has1080 is true when the master lists a variant of 1080 or more
// (no rewrite probe issued) or when ProbeHiddenFHD's rewrite serves a
// genuine video track. err carries the same transient-vs-definitive
// contract as ProbeHiddenFHD: a non-nil err must not be cached.
func (c *Client) VerifyFHD(ctx context.Context, hlsMasterURL string) (has1080 bool, maxListed int, err error) {
	body, err := c.GetCtx(ctx, hlsMasterURL)
	if err != nil {
		return false, 0, fmt.Errorf("fetch master playlist: %w", err)
	}
	maxListed = maxPlaylistHeight(body)
	if maxListed >= 1080 {
		return true, maxListed, nil
	}
	_, found, err := c.probeHiddenFHDIn(ctx, hlsMasterURL, body)
	if err != nil {
		return false, maxListed, err
	}
	return found, maxListed, nil
}

// maxVariantPlaylistBytes bounds the media playlist read in
// probeHiddenFHDIn. A 60-minute episode's playlist is ~55 KB; a
// three-hour film is under 200 KB.
const maxVariantPlaylistBytes = 4 << 20

// probeHiddenFHDIn is ProbeHiddenFHD after the master playlist fetch,
// shared with VerifyFHD so the master is fetched once.
func (c *Client) probeHiddenFHDIn(ctx context.Context, hlsMasterURL string, body []byte) (fhdURL string, found bool, err error) {
	bestURL, err := pickHighestBandwidthVariant(hlsMasterURL, body)
	if err != nil {
		return "", false, err
	}
	if !strings.Contains(bestURL, "video=") {
		// Structural absence: the rewrite cannot apply. Cacheable.
		return "", false, nil
	}

	// SD-only guard: if every variant in the master playlist is below
	// 720p, this is genuinely SD-only content (e.g. pre-2010 BBC
	// programmes). BBC only hides 1080p on content that already has
	// 720p+ in the manifest, so there is nothing to probe for.
	// maxPlaylistHeight returns 0 if no RESOLUTION tags are present,
	// in which case we skip the guard and proceed with the probe.
	if maxH := maxPlaylistHeight(body); maxH > 0 && maxH < 720 {
		log.Printf("FHD probe skipped: max playlist resolution %dp (below 720p threshold)", maxH)
		return "", false, nil
	}

	rewritten := fhdVariantRewrite.ReplaceAllString(bestURL, "video=12000000")

	// GET, not HEAD. USP answers 200 on the rewrite whenever the
	// audio track matches, and if the requested video track does not
	// exist it drops that filter and serves the audio track alone. A
	// 200 therefore proves nothing on its own; the media playlist has
	// to be read and its segments checked for a video track. Skipping
	// this check produced a 16-second audio-only "1080p" download that
	// ffprobe reported as having no video stream. GitHub #54.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rewritten, nil)
	if err != nil {
		return "", false, fmt.Errorf("build FHD probe request: %w", err)
	}
	req.Header.Set("User-Agent", RandomUserAgent())

	resp, err := c.http.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("GET %s: %w", rewritten, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		media, err := io.ReadAll(io.LimitReader(resp.Body, maxVariantPlaylistBytes))
		if err != nil {
			// Transport failure mid-body. Caller must not cache.
			return "", false, fmt.Errorf("read FHD variant playlist: %w", err)
		}
		if !hasVideoSegments(media) {
			// Definitive-absence: BBC has no 1080p track for this
			// programme and the rewrite serves audio only. Cacheable.
			log.Printf("FHD probe: rewrite answered 200 but serves no video track (audio-only rendition); no 1080p for %s", hlsMasterURL)
			return "", false, nil
		}
		return rewritten, true, nil
	case http.StatusNotFound, http.StatusGone:
		// Definitive-absence — cacheable as "no 1080p for this show".
		return "", false, nil
	default:
		// Transient (429, 5xx, 401/403, etc). Caller must not cache.
		return "", false, fmt.Errorf("FHD probe returned %d", resp.StatusCode)
	}
}

// hasVideoSegments reports whether an HLS media playlist carries at
// least one segment URI with a video track. USP names each segment
// after the tracks it muxes, so an audio-only rendition's segments read
// `...-audio_eng_1=128000-N.ts` while a real one reads
// `...-audio_eng_1=128000-video=12000000-N.ts`. Only URIs that follow
// an #EXTINF tag count, so a master playlist (whose variant URIs also
// contain `video=`) never passes.
func hasVideoSegments(body []byte) bool {
	pendingSegment := false
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "#EXTINF"):
			pendingSegment = true
		case strings.HasPrefix(line, "#"):
			continue
		default:
			if pendingSegment && strings.Contains(line, "video=") {
				return true
			}
			pendingSegment = false
		}
	}
	return false
}

// pickHighestBandwidthVariant walks an HLS master playlist body and
// returns the variant URL with the highest BANDWIDTH attribute,
// resolved against the master playlist base if the variant URL is
// relative. Mirrors internal/download/ffmpeg.go:80-110. Returns an
// error if no #EXT-X-STREAM-INF line is found.
func pickHighestBandwidthVariant(masterURL string, body []byte) (string, error) {
	var bwRe = regexp.MustCompile(`BANDWIDTH=(\d+)`)

	lines := strings.Split(string(body), "\n")
	bestBW := 0
	bestURL := ""
	for i, line := range lines {
		if !strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			continue
		}
		m := bwRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		bw, convErr := strconv.Atoi(m[1])
		if convErr != nil || i+1 >= len(lines) {
			continue
		}
		if bw > bestBW {
			bestBW = bw
			bestURL = strings.TrimSpace(lines[i+1])
		}
	}

	if bestURL == "" {
		return "", errors.New("no variant found in master playlist")
	}

	// Resolve relative to master playlist base directory.
	if !strings.HasPrefix(bestURL, "http") {
		base := masterURL
		if idx := strings.LastIndex(base, "/"); idx >= 0 {
			base = base[:idx+1]
		}
		bestURL = base + bestURL
	}
	return bestURL, nil
}
