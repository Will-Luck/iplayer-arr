package bbc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// fhdVariantRewrite matches video=<digits> in a variant URL so we can
// swap it for video=12000000 (BBC's unlisted 1080p path). Compiled
// once to avoid paying the regex compile cost per call.
var fhdVariantRewrite = regexp.MustCompile(`video=\d+`)

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

	bestURL, err := pickHighestBandwidthVariant(hlsMasterURL, body)
	if err != nil {
		return "", false, err
	}
	if !strings.Contains(bestURL, "video=") {
		// Structural absence: the rewrite cannot apply. Cacheable.
		return "", false, nil
	}

	rewritten := fhdVariantRewrite.ReplaceAllString(bestURL, "video=12000000")

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rewritten, nil)
	if err != nil {
		return "", false, fmt.Errorf("build HEAD request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("HEAD %s: %w", rewritten, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return rewritten, true, nil
	case http.StatusNotFound, http.StatusGone:
		// Definitive-absence — cacheable as "no 1080p for this show".
		return "", false, nil
	default:
		// Transient (429, 5xx, 401/403, etc). Caller must not cache.
		return "", false, fmt.Errorf("FHD HEAD returned %d", resp.StatusCode)
	}
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
