package download

import (
	"strings"
	"testing"
	"time"
)

// TestEffectiveWatchdogTimeout_FallsBackToPackageDefault: a job with
// zero WatchdogTimeout uses progressWatchdogTimeout (60s). Locks the
// backward-compatible behaviour: any caller that didn't set the new
// field still sees the historical timeout.
func TestEffectiveWatchdogTimeout_FallsBackToPackageDefault(t *testing.T) {
	job := FFmpegJob{} // WatchdogTimeout is zero
	if got := effectiveWatchdogTimeout(job); got != progressWatchdogTimeout {
		t.Errorf("effectiveWatchdogTimeout(zero) = %v, want %v (package default)", got, progressWatchdogTimeout)
	}
}

// TestEffectiveWatchdogTimeout_HonoursPositiveOverride: a job with a
// positive WatchdogTimeout uses that value instead of the default.
func TestEffectiveWatchdogTimeout_HonoursPositiveOverride(t *testing.T) {
	job := FFmpegJob{WatchdogTimeout: 120 * time.Second}
	if got := effectiveWatchdogTimeout(job); got != 120*time.Second {
		t.Errorf("effectiveWatchdogTimeout(120s) = %v, want 120s", got)
	}
}

// TestEffectiveWatchdogTimeout_NegativeFallsBack: a negative override
// (shouldn't happen but defensive) falls back to the package default
// rather than producing a negative-timeout watchdog (which would fire
// on every tick).
func TestEffectiveWatchdogTimeout_NegativeFallsBack(t *testing.T) {
	job := FFmpegJob{WatchdogTimeout: -1 * time.Second}
	if got := effectiveWatchdogTimeout(job); got != progressWatchdogTimeout {
		t.Errorf("effectiveWatchdogTimeout(negative) = %v, want %v (package default)", got, progressWatchdogTimeout)
	}
}

// TestEscalateWatchdog locks the escalation ladder: base, 2x, 4x, then
// held at the shift cap; clamped to the max ceiling but never below base;
// a negative count is treated as zero (a negative Go shift panics).
func TestEscalateWatchdog(t *testing.T) {
	cases := []struct {
		base       time.Duration
		stallCount int
		want       time.Duration
	}{
		{60 * time.Second, 0, 60 * time.Second},
		{60 * time.Second, 1, 120 * time.Second},
		{60 * time.Second, 2, 240 * time.Second},
		{60 * time.Second, 3, 240 * time.Second},           // shift capped at 2
		{60 * time.Second, 9, 240 * time.Second},           // shift capped at 2
		{60 * time.Second, -1, 60 * time.Second},           // negative guarded to 0
		{5 * time.Minute, 2, watchdogEscalationMaxTimeout}, // 20m clamped to 10m
		{15 * time.Minute, 0, 15 * time.Minute},            // large base preserved, never clamped below base
	}
	for _, c := range cases {
		if got := escalateWatchdog(c.base, c.stallCount); got != c.want {
			t.Errorf("escalateWatchdog(%v, %d) = %v, want %v", c.base, c.stallCount, got, c.want)
		}
	}
}

// TestEffectiveWatchdogTimeout_EscalatesWithStallCount is the discriminating
// test: with the DEFAULT (unset) base, a StallCount of 1 must widen the
// window to 120s, NOT stay at 0 or 60s. A version that computed base<<n with
// base==0 would return 0 here and ship the bug for exactly the users on the
// package default. GitHub #50.
func TestEffectiveWatchdogTimeout_EscalatesWithStallCount(t *testing.T) {
	if got := effectiveWatchdogTimeout(FFmpegJob{StallCount: 1}); got != 120*time.Second {
		t.Errorf("effectiveWatchdogTimeout(default base, StallCount 1) = %v, want 120s", got)
	}
	if got := effectiveWatchdogTimeout(FFmpegJob{StallCount: 2}); got != 240*time.Second {
		t.Errorf("effectiveWatchdogTimeout(default base, StallCount 2) = %v, want 240s", got)
	}
	// An explicit base still escalates from that base.
	if got := effectiveWatchdogTimeout(FFmpegJob{WatchdogTimeout: 120 * time.Second, StallCount: 1}); got != 240*time.Second {
		t.Errorf("effectiveWatchdogTimeout(120s base, StallCount 1) = %v, want 240s", got)
	}
}

// TestCrossedFaststartThreshold: muxed time within 99% of duration is the
// finalization window; an unknown (<=0) duration disables the relaxation.
func TestCrossedFaststartThreshold(t *testing.T) {
	cases := []struct {
		muxed, duration float64
		want            bool
	}{
		{100, 100, true},
		{99, 100, true},
		{98.9, 100, false},
		{50, 100, false},
		{10, 0, false}, // unknown duration -> no relaxation
		{0, 0, false},
	}
	for _, c := range cases {
		if got := crossedFaststartThreshold(c.muxed, c.duration); got != c.want {
			t.Errorf("crossedFaststartThreshold(%v, %v) = %v, want %v", c.muxed, c.duration, got, c.want)
		}
	}
}

// TestFinalizeAwareThreshold: the finalize grace applies only when near
// completion and only when it is larger than the (escalated) base.
func TestFinalizeAwareThreshold(t *testing.T) {
	grace := 3 * time.Minute
	cases := []struct {
		base time.Duration
		near bool
		want time.Duration
	}{
		{60 * time.Second, false, 60 * time.Second},
		{60 * time.Second, true, grace},              // grace 3m > 60s
		{240 * time.Second, true, 240 * time.Second}, // escalated base 4m > grace, never below base
		{grace, true, grace},
	}
	for _, c := range cases {
		if got := finalizeAwareThreshold(c.base, grace, c.near); got != c.want {
			t.Errorf("finalizeAwareThreshold(%v, %v, %v) = %v, want %v", c.base, grace, c.near, got, c.want)
		}
	}
}

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
