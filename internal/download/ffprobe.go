package download

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// probeActualHeight shells out to ffprobe and returns the encoded video
// height in pixels for the first video stream. Returns a non-nil error
// when ffprobe is unavailable, the file is missing, or the output
// cannot be parsed as a positive integer. The caller is responsible
// for treating any error as "no truth available; fall back to
// requested-quality estimate".
func probeActualHeight(ctx context.Context, path string) (int, error) {
	cmd := exec.CommandContext(
		ctx,
		"ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=height",
		"-of", "csv=p=0",
		path,
	)
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe %s: %w", path, err)
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return 0, fmt.Errorf("ffprobe %s: empty output", path)
	}
	h, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("ffprobe %s: parse %q: %w", path, raw, err)
	}
	if h <= 0 {
		return 0, fmt.Errorf("ffprobe %s: non-positive height %d", path, h)
	}
	return h, nil
}
