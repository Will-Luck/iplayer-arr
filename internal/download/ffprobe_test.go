package download

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestProbeActualHeight_Mp4Fixture(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH; cannot generate fixture")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not on PATH; cannot probe fixture")
	}

	dir := t.TempDir()
	fixture := filepath.Join(dir, "fixture.mp4")
	// Generate a 1-second 540p mp4 (BBC's lowest-typical encode height).
	gen := exec.Command(
		"ffmpeg", "-y",
		"-f", "lavfi",
		"-i", "testsrc=size=960x540:duration=1",
		"-c:v", "libx264", "-pix_fmt", "yuv420p",
		fixture,
	)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate fixture: %v\noutput:\n%s", err, out)
	}

	got, err := probeActualHeight(context.Background(), fixture)
	if err != nil {
		t.Fatalf("probeActualHeight: %v", err)
	}
	if got != 540 {
		t.Errorf("probeActualHeight(540p fixture) = %d, want 540", got)
	}
}

func TestProbeActualHeight_MissingFile(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not on PATH")
	}
	_, err := probeActualHeight(context.Background(), "/nonexistent/path/should/never/exist.mp4")
	if err == nil {
		t.Fatal("probeActualHeight on missing file: want non-nil error, got nil")
	}
}
