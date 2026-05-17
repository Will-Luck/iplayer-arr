package download

import (
	"strings"
	"testing"
)

func TestParseFFmpegProgress(t *testing.T) {
	tests := []struct {
		line     string
		wantTime float64
		wantSize int64
		wantOK   bool
	}{
		{
			"frame=  720 fps= 25 q=-1.0 size=  456789kB time=00:12:34.56 bitrate=5000.0kbits/s speed=2.5x",
			754.56, 456789 * 1024, true,
		},
		{
			"size=  123456kB time=00:05:00.00 bitrate=3300.0kbits/s speed=1.2x",
			300.0, 123456 * 1024, true,
		},
		// ffmpeg 8.0.1 stderr (KiB unit), captured from prod.
		{
			"frame=37630 fps=684 q=-1.0 size=  680448KiB time=00:12:32.60 bitrate=7406.6kbits/s speed=13.7x",
			752.60, 680448 * 1024, true,
		},
		{
			"size= 680448 KiB time=00:12:32.60 bitrate=7406.6kbits/s",
			752.60, 680448 * 1024, true,
		},
		{
			"some random line",
			0, 0, false,
		},
	}

	for _, tt := range tests {
		prog, ok := parseProgress(tt.line)
		if ok != tt.wantOK {
			t.Errorf("parseProgress(%q): ok = %v, want %v", tt.line, ok, tt.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if prog.TimeSeconds != tt.wantTime {
			t.Errorf("time = %f, want %f", prog.TimeSeconds, tt.wantTime)
		}
		if prog.SizeBytes != tt.wantSize {
			t.Errorf("size = %d, want %d", prog.SizeBytes, tt.wantSize)
		}
	}
}

// TestAppendDiagLine_TailsAndTrims exercises GitHub issue #40: when
// ffmpeg exits non-zero we want the last few non-progress stderr
// lines surfaced in the error. appendDiagLine implements the ring
// behaviour so the diagnostic always reflects the *latest* state
// ffmpeg printed before dying.
func TestAppendDiagLine_TailsAndTrims(t *testing.T) {
	const max = 3
	var diag []string

	// Whitespace-only and empty lines must be dropped (parseProgress
	// already filtered progress; this is the "should not pollute the
	// tail" guard for the non-progress path).
	for _, line := range []string{"   ", "", "\t"} {
		diag = appendDiagLine(diag, line, max)
	}
	if len(diag) != 0 {
		t.Fatalf("empty-only path produced entries: %v", diag)
	}

	// Real diagnostic content tails to the cap.
	for _, line := range []string{
		"  [hls @ 0x55] HTTP error 403 Forbidden  ",
		"[https @ 0x55] Failed to open segment",
		"Output #0, mp4, to '/downloads/x.mp4':",
		"Conversion failed!",
	} {
		diag = appendDiagLine(diag, line, max)
	}

	if len(diag) != max {
		t.Errorf("diag len = %d, want %d", len(diag), max)
	}
	if diag[0] != "[https @ 0x55] Failed to open segment" {
		t.Errorf("oldest survivor = %q, want the second emitted line", diag[0])
	}
	if diag[len(diag)-1] != "Conversion failed!" {
		t.Errorf("newest = %q, want \"Conversion failed!\"", diag[len(diag)-1])
	}

	// Lines must be trimmed so the user-facing error is tidy.
	for _, line := range diag {
		if strings.HasPrefix(line, " ") || strings.HasSuffix(line, " ") {
			t.Errorf("diag entry %q still has surrounding whitespace", line)
		}
	}
}

// TestAppendDiagLine_SkipsPartialProgressLines pins the v1.5.6 fix:
// progress-looking lines that parseProgress rejected (because they
// lack one of the required fields) must NOT pollute the diagnostic
// tail. Pre-v1.5.6 a `time=` line with no `size=` (audio-only
// segment, partial-flush before the size field materialises) would
// fall through into diagLines and drown the real error context.
func TestAppendDiagLine_SkipsPartialProgressLines(t *testing.T) {
	const max = 3
	var diag []string

	// Eight progress-looking lines that the regex would reject because
	// no single line has both `time=` AND `size=` together. None of
	// these should land in the diagnostic tail.
	noiseLines := []string{
		"time=00:00:01.50 bitrate=128.0kbits/s",
		"frame=  120 fps=30",
		"size=N/A time=00:00:02.00",
		"out_time_us=3000000",
		"bitrate=  256.0kbits/s",
		"speed=1.02x",
		"dup=0 drop=2",
		"out_time=00:00:04.00",
	}
	for _, line := range noiseLines {
		diag = appendDiagLine(diag, line, max)
	}
	if len(diag) != 0 {
		t.Fatalf("progress-shaped lines polluted tail: %v", diag)
	}

	// A real error sandwiched between progress noise must survive.
	for _, line := range []string{
		"time=00:00:05.00 bitrate=128.0kbits/s",
		"[hls @ 0x55] HTTP error 403 Forbidden",
		"size=  512KiB time=00:00:06.00",
	} {
		diag = appendDiagLine(diag, line, max)
	}
	if len(diag) != 1 || diag[0] != "[hls @ 0x55] HTTP error 403 Forbidden" {
		t.Errorf("real error lost in progress noise: %v", diag)
	}
}

// TestLooksLikeProgressLine_KnownPrefixes locks the prefix list so a
// future field rename (e.g. ffmpeg 9 adding `out_time_ns=`) is caught
// here rather than by silently letting noise back into the diag tail.
func TestLooksLikeProgressLine_KnownPrefixes(t *testing.T) {
	progress := []string{
		"frame=120",
		"fps=29.97",
		"size= 1024KiB",
		"time=00:00:01.00",
		"bitrate=128kbits/s",
		"speed=1.0x",
		"out_time=00:00:01.00",
		"out_time_ms=1000",
		"out_time_us=1000000",
		"dup=0",
		"drop=0",
	}
	for _, line := range progress {
		if !looksLikeProgressLine(line) {
			t.Errorf("looksLikeProgressLine(%q) = false, want true", line)
		}
	}

	notProgress := []string{
		"[hls @ 0x55] HTTP error 403 Forbidden",
		"Conversion failed!",
		"Error opening input file",
		"Output #0, mp4, to '/downloads/x.mp4':",
		"",
		"random text",
	}
	for _, line := range notProgress {
		if looksLikeProgressLine(line) {
			t.Errorf("looksLikeProgressLine(%q) = true, want false", line)
		}
	}
}
