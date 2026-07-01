package download

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeFakeFFmpeg installs a fake `ffmpeg` on PATH that emits a single -stats
// progress line at the given muxed time= position, then goes quiet for the
// given duration, then exits 0. RunFFmpeg resolves "ffmpeg" via PATH, so this
// drives the real watchdog goroutine (including the faststart finalize-grace
// wiring, which the m.runFFmpeg seam cannot reach) without a real ffmpeg or a
// real stream. GitHub #50.
func writeFakeFFmpeg(t *testing.T, timeField string, quiet time.Duration) {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf(
		"#!/usr/bin/env bash\n"+
			">&2 printf 'frame=100 fps=25 q=-1.0 size=1024kB time=%s bitrate=100kbits/s speed=1x\\n'\n"+
			"sleep %.2f\n"+
			"exit 0\n",
		timeField, quiet.Seconds(),
	)
	path := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func shrinkWatchdogForTest(t *testing.T, grace time.Duration) {
	t.Helper()
	oldInterval := progressWatchdogInterval
	progressWatchdogInterval = 50 * time.Millisecond
	oldGrace := faststartFinalizeGrace
	faststartFinalizeGrace = grace
	t.Cleanup(func() {
		progressWatchdogInterval = oldInterval
		faststartFinalizeGrace = oldGrace
	})
}

// TestRunFFmpeg_FaststartGraceSurvivesNearComplete: once muxed time= crosses
// the faststart threshold (99% of Duration), the watchdog relaxes to the
// finalize grace, so the silent moov relocation is not killed as a stall even
// though it exceeds the base watchdog window. GitHub #50.
func TestRunFFmpeg_FaststartGraceSurvivesNearComplete(t *testing.T) {
	shrinkWatchdogForTest(t, 5*time.Second)
	// 99s of a 100s programme = 99%, then quiet 1s (> base 200ms, < grace 5s).
	writeFakeFFmpeg(t, "00:01:39.00", 1*time.Second)

	dir := t.TempDir()
	job := FFmpegJob{
		StreamURL:       "http://127.0.0.1:1/stream.mp4",
		OutputPath:      filepath.Join(dir, "out.mp4"),
		WatchdogTimeout: 200 * time.Millisecond,
		Duration:        100 * time.Second,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := RunFFmpeg(ctx, job); err != nil {
		t.Fatalf("near-complete finalization must not be killed as a stall, got: %v", err)
	}
}

// TestRunFFmpeg_FaststartRelaxDoesNotMaskMidStreamStall: the relaxation is
// discriminating -- a stall at only 50% of Duration (mid-stream, not
// finalization) still trips the watchdog at the base window. Guards against
// setting the fraction too low and masking real trailing-segment CDN stalls.
// GitHub #50.
func TestRunFFmpeg_FaststartRelaxDoesNotMaskMidStreamStall(t *testing.T) {
	shrinkWatchdogForTest(t, 5*time.Second)
	// 50s of a 100s programme = 50%, then quiet 3s (>> base 200ms).
	writeFakeFFmpeg(t, "00:00:50.00", 3*time.Second)

	dir := t.TempDir()
	job := FFmpegJob{
		StreamURL:       "http://127.0.0.1:1/stream.mp4",
		OutputPath:      filepath.Join(dir, "out.mp4"),
		WatchdogTimeout: 200 * time.Millisecond,
		Duration:        100 * time.Second,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := RunFFmpeg(ctx, job)
	if !errors.Is(err, ErrStalled) {
		t.Fatalf("a mid-stream stall must still trip the watchdog, got: %v", err)
	}
}
