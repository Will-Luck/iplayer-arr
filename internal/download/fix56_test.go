package download

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// #56: a watchdog stall must NOT count toward the CDN-failure counter
// that drives quality degradation. It must still be retryable with the
// standard exponential backoff, so the download goes back out at the
// originally requested height once the local throttling clears.
func TestFailDownload_StalledDoesNotCountCDNFailure(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	m := NewManager(st, filepath.Join(dir, "downloads"), 1, nil, nil, nil, nil)

	dl := &store.Download{ID: "s1", PID: "p1", Status: store.StatusPending, Quality: "1080p"}
	st.PutDownload(dl)

	m.failDownload(dl, store.FailCodeStalled, fmt.Errorf("stalled"))
	if dl.CDNFailures != 0 {
		t.Fatalf("Stalled bumped CDNFailures to %d, want 0", dl.CDNFailures)
	}
	if dl.FailureCode != store.FailCodeStalled {
		t.Fatalf("FailureCode = %q, want %q", dl.FailureCode, store.FailCodeStalled)
	}
	if !dl.Retryable {
		t.Fatal("stalled on first attempt should be retryable")
	}
	if dl.RetryAfter.IsZero() || !dl.RetryAfter.After(time.Now()) {
		t.Fatalf("RetryAfter = %v, want a non-zero time in the future", dl.RetryAfter)
	}
}

// #56: stalls share the standard 3-attempt budget by design. Three
// consecutive stalls exhaust the budget and the download fails
// permanently (moved to history); Sonarr's re-grab handles longer
// outages, so an unbounded stall-retry loop is deliberately avoided.
func TestFailDownload_StallBudgetExhaustion(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	m := NewManager(st, filepath.Join(dir, "downloads"), 1, nil, nil, nil, nil)

	dl := &store.Download{ID: "sb1", PID: "p1", Status: store.StatusPending, Quality: "1080p"}
	st.PutDownload(dl)

	m.failDownload(dl, store.FailCodeStalled, fmt.Errorf("stalled 1"))
	m.failDownload(dl, store.FailCodeStalled, fmt.Errorf("stalled 2"))
	if !dl.Retryable {
		t.Fatal("second stall should still be retryable")
	}
	m.failDownload(dl, store.FailCodeStalled, fmt.Errorf("stalled 3"))

	if dl.Retryable {
		t.Error("third stall should exhaust the retry budget (Retryable = true, want false)")
	}
	if got, _ := st.GetDownload(dl.ID); got != nil {
		t.Error("download still in the downloads bucket after the third stall, want moved to history")
	}
	hist, err := st.GetHistory(dl.ID)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if hist == nil {
		t.Fatal("download not found in history after budget exhaustion")
	}
}

// #56: the CDN-failure counter must survive an interleaved stall:
// neither reset nor incremented. A later genuine CDN-style failure
// then degrades by the full accumulated count.
func TestFailDownload_CDNFailuresPreservedAcrossStall(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	m := NewManager(st, filepath.Join(dir, "downloads"), 1, nil, nil, nil, nil)

	dl := &store.Download{ID: "cp1", PID: "p1", Status: store.StatusPending, Quality: "1080p"}
	st.PutDownload(dl)

	m.failDownload(dl, store.FailCodeFFmpeg, fmt.Errorf("ffmpeg"))
	if dl.CDNFailures != 1 {
		t.Fatalf("CDNFailures = %d after ffmpeg failure, want 1", dl.CDNFailures)
	}
	m.failDownload(dl, store.FailCodeStalled, fmt.Errorf("stalled"))
	if dl.CDNFailures != 1 {
		t.Fatalf("CDNFailures = %d after stall, want 1 (preserved, not reset or incremented)", dl.CDNFailures)
	}
	m.failDownload(dl, store.FailCodeTruncated, fmt.Errorf("trunc"))
	if dl.CDNFailures != 2 {
		t.Fatalf("CDNFailures = %d after truncated failure, want 2", dl.CDNFailures)
	}
	if !shouldDegrade(dl) {
		t.Error("shouldDegrade = false after truncated failure with accumulated count, want true")
	}
}

// #56: the degradation gate must ignore FailCodeStalled. This test
// exercises the extracted shouldDegrade helper only; the real
// processDownload wiring is covered by the runFFmpeg seam tests below.
func TestShouldDegrade_IgnoresStalled(t *testing.T) {
	cases := []struct {
		name string
		dl   store.Download
		want bool
	}{
		{
			name: "ffmpeg failure degrades",
			dl:   store.Download{Quality: "1080p", CDNFailures: 1, FailureCode: store.FailCodeFFmpeg},
			want: true,
		},
		{
			name: "truncated failure degrades",
			dl:   store.Download{Quality: "1080p", CDNFailures: 1, FailureCode: store.FailCodeTruncated},
			want: true,
		},
		{
			name: "stalled never degrades",
			dl:   store.Download{Quality: "1080p", CDNFailures: 0, FailureCode: store.FailCodeStalled},
			want: false,
		},
		{
			// A stall that follows earlier CDN-style failures keeps the
			// accumulated CDNFailures count, but the most recent failure
			// code is stalled, so THIS attempt does not degrade. The
			// count is only preserved for a later ffmpeg/truncated
			// failure, which will degrade by the full accumulated count.
			name: "stalled with earlier CDN failures does not degrade this attempt",
			dl:   store.Download{Quality: "1080p", CDNFailures: 2, FailureCode: store.FailCodeStalled},
			want: false,
		},
		{
			// Zero CDN failures means nothing to degrade for, whatever
			// the failure code says (first attempt, or counter never
			// bumped).
			name: "ffmpeg code without CDN failures does not degrade",
			dl:   store.Download{Quality: "1080p", CDNFailures: 0, FailureCode: store.FailCodeFFmpeg},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldDegrade(&tc.dl); got != tc.want {
				t.Errorf("shouldDegrade(CDNFailures=%d, FailureCode=%q) = %v, want %v",
					tc.dl.CDNFailures, tc.dl.FailureCode, got, tc.want)
			}
		})
	}
}

// newSeamHarness builds a Manager whose playlist and media-selector
// resolvers point at local mocks (same shape as TestWorkerLifecycle in
// manager_test.go), so processDownload runs its full pre-ffmpeg
// pipeline without the network. The caller overrides m.runFFmpeg to
// control the ffmpeg outcome.
func newSeamHarness(t *testing.T) (*Manager, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	playlistServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"defaultAvailableVersion": map[string]interface{}{
				"smpConfig": map[string]interface{}{
					"title":   "Seam Test Programme",
					"summary": "A test programme",
					"items": []map[string]interface{}{
						{"kind": "programme", "duration": 1800, "vpid": "p_seam_vpid"},
					},
				},
			},
			"allAvailableVersions": []interface{}{},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(playlistServer.Close)

	mediaSelectorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<?xml version="1.0"?>
<mediaSelection>
  <media kind="video" type="video/mp4" encoding="h264" bitrate="2500" width="1280" height="720">
    <connection supplier="akamai" transferFormat="hls" protocol="https" href="https://invalid.example.com/stream.m3u8"/>
  </media>
</mediaSelection>`)
	}))
	t.Cleanup(mediaSelectorServer.Close)

	bbcClient := bbc.NewClient()
	playlist := bbc.NewPlaylistResolver(bbcClient)
	playlist.BaseURL = playlistServer.URL

	ms := bbc.NewMediaSelector(bbcClient)
	ms.BaseURL = mediaSelectorServer.URL

	return NewManager(st, filepath.Join(dir, "downloads"), 1, bbcClient, playlist, ms, nil), st
}

// #56 (revert-proof): drive the REAL processDownload pipeline through
// the runFFmpeg seam and assert the stall routing end to end. A
// watchdog stall must land as FailCodeStalled, retryable, with the
// CDN-failure counter untouched. Guards against the errors.Is routing
// in processDownload being silently reverted.
func TestProcessDownload_StallRoutesToFailCodeStalled(t *testing.T) {
	m, st := newSeamHarness(t)
	m.runFFmpeg = func(ctx context.Context, job FFmpegJob) error {
		return fmt.Errorf("ffmpeg watchdog: %w", ErrStalled)
	}

	id, err := m.Enqueue("b099seam1", "1080p", "Seam.S01E01.1080p", "sonarr")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	dl, err := st.GetDownload(id)
	if err != nil || dl == nil {
		t.Fatalf("GetDownload(%q) = %v, %v", id, dl, err)
	}

	m.processDownload(context.Background(), dl)

	if dl.FailureCode != store.FailCodeStalled {
		t.Errorf("FailureCode = %q, want %q", dl.FailureCode, store.FailCodeStalled)
	}
	if dl.CDNFailures != 0 {
		t.Errorf("CDNFailures = %d, want 0", dl.CDNFailures)
	}
	if dl.Status != store.StatusFailed {
		t.Errorf("Status = %q, want %q", dl.Status, store.StatusFailed)
	}
	if !dl.Retryable {
		t.Error("Retryable = false, want true")
	}
}

// #56 sibling: a plain (non-stall) ffmpeg error through the same seam
// must keep the pre-#56 routing: FailCodeFFmpeg with a CDN-failure
// counter bump.
func TestProcessDownload_FFmpegErrorRoutesToFFmpegCode(t *testing.T) {
	m, st := newSeamHarness(t)
	m.runFFmpeg = func(ctx context.Context, job FFmpegJob) error {
		return fmt.Errorf("ffmpeg exited with code 1")
	}

	id, err := m.Enqueue("b099seam2", "1080p", "Seam.S01E02.1080p", "sonarr")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	dl, err := st.GetDownload(id)
	if err != nil || dl == nil {
		t.Fatalf("GetDownload(%q) = %v, %v", id, dl, err)
	}

	m.processDownload(context.Background(), dl)

	if dl.FailureCode != store.FailCodeFFmpeg {
		t.Errorf("FailureCode = %q, want %q", dl.FailureCode, store.FailCodeFFmpeg)
	}
	if dl.CDNFailures != 1 {
		t.Errorf("CDNFailures = %d, want 1", dl.CDNFailures)
	}
	if dl.Status != store.StatusFailed {
		t.Errorf("Status = %q, want %q", dl.Status, store.StatusFailed)
	}
}

// #56: wrapRunError must surface ErrStalled (via errors.Is) when the
// watchdog cancelled the run, and must keep the prior wrapping for
// genuine ffmpeg failures, including the stderr diagnostic tail.
func TestWrapRunError_StallSurfacesErrStalled(t *testing.T) {
	waitErr := errors.New("signal: terminated")

	t.Run("stalled without diagnostics", func(t *testing.T) {
		got := wrapRunError(waitErr, true, nil)
		if !errors.Is(got, ErrStalled) {
			t.Fatalf("errors.Is(got, ErrStalled) = false, got: %v", got)
		}
		if !strings.Contains(got.Error(), waitErr.Error()) {
			t.Errorf("stall error lost the wait error text: %v", got)
		}
	})

	t.Run("stalled with diagnostics", func(t *testing.T) {
		got := wrapRunError(waitErr, true, []string{"http 403", "EIO"})
		if !errors.Is(got, ErrStalled) {
			t.Fatalf("errors.Is(got, ErrStalled) = false, got: %v", got)
		}
		if !strings.Contains(got.Error(), "http 403 | EIO") {
			t.Errorf("stall error lost the diagnostic tail: %v", got)
		}
	})

	t.Run("genuine failure without diagnostics", func(t *testing.T) {
		got := wrapRunError(waitErr, false, nil)
		if errors.Is(got, ErrStalled) {
			t.Fatalf("genuine failure must not satisfy ErrStalled: %v", got)
		}
		if !errors.Is(got, waitErr) {
			t.Errorf("genuine failure must wrap the wait error: %v", got)
		}
	})

	t.Run("genuine failure with diagnostics", func(t *testing.T) {
		got := wrapRunError(waitErr, false, []string{"Permission denied"})
		if errors.Is(got, ErrStalled) {
			t.Fatalf("genuine failure must not satisfy ErrStalled: %v", got)
		}
		if !errors.Is(got, waitErr) {
			t.Errorf("genuine failure must wrap the wait error: %v", got)
		}
		if !strings.Contains(got.Error(), "Permission denied") {
			t.Errorf("genuine failure lost the diagnostic tail: %v", got)
		}
	})
}

// #56: the scanner-error return path must surface the stall too. The
// watchdog killing ffmpeg can tear down the stderr pipe mid-read, so a
// scan error during a stall is the stall, not an independent fault.
func TestWrapScanError_StallSurfacesErrStalled(t *testing.T) {
	scanErr := errors.New("read |0: file already closed")

	got := wrapScanError(scanErr, true)
	if !errors.Is(got, ErrStalled) {
		t.Fatalf("errors.Is(got, ErrStalled) = false, got: %v", got)
	}
	if !strings.Contains(got.Error(), scanErr.Error()) {
		t.Errorf("stall scan error lost the scan error text: %v", got)
	}

	got = wrapScanError(scanErr, false)
	if errors.Is(got, ErrStalled) {
		t.Fatalf("genuine scan error must not satisfy ErrStalled: %v", got)
	}
	if !errors.Is(got, scanErr) {
		t.Errorf("genuine scan error must wrap the scan error: %v", got)
	}
}

// #56: a genuine fast ffmpeg failure (input that does not exist) must
// NOT satisfy errors.Is(err, ErrStalled); only a watchdog cancellation
// may. Integration-level guard for the real RunFFmpeg wiring. The
// complementary hang test is TestRunFFmpeg_WatchdogStallReturnsErrStalled
// below, which shrinks progressWatchdogInterval so the watchdog can
// fire inside a test time budget.
func TestRunFFmpeg_GenuineFailureIsNotStalled(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}

	dir := t.TempDir()
	job := FFmpegJob{
		StreamURL:  filepath.Join(dir, "does-not-exist.mp4"),
		OutputPath: filepath.Join(dir, "out.mp4"),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := RunFFmpeg(ctx, job)
	if err == nil {
		t.Fatal("RunFFmpeg on a nonexistent input returned nil, want error")
	}
	if errors.Is(err, ErrStalled) {
		t.Fatalf("genuine ffmpeg failure must not satisfy ErrStalled: %v", err)
	}
}

// #56 positive watchdog plumbing test: a hung input must trip the
// progress watchdog inside the real RunFFmpeg and surface ErrStalled.
// Not parallel: it mutates the package-level progressWatchdogInterval,
// which RunFFmpeg reads once at ticker creation inside the watchdog
// goroutine; no other test touches it concurrently.
func TestRunFFmpeg_WatchdogStallReturnsErrStalled(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}

	oldInterval := progressWatchdogInterval
	progressWatchdogInterval = 100 * time.Millisecond
	t.Cleanup(func() { progressWatchdogInterval = oldInterval })

	// The handler sends headers then blocks, so ffmpeg hangs probing
	// the input and never produces a progress line. blockCh is closed
	// by the LAST-registered cleanup, which t.Cleanup's LIFO order runs
	// FIRST, unblocking the handler before server.Close waits on it.
	blockCh := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-blockCh
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(blockCh) })

	dir := t.TempDir()
	job := FFmpegJob{
		StreamURL:       server.URL,
		OutputPath:      filepath.Join(dir, "out.mp4"),
		WatchdogTimeout: 300 * time.Millisecond,
	}

	// Backstop only; the watchdog should fire at ~400ms and the whole
	// test should finish well inside a second or two.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := RunFFmpeg(ctx, job)
	if err == nil {
		t.Fatal("RunFFmpeg on a hung input returned nil, want ErrStalled")
	}
	if !errors.Is(err, ErrStalled) {
		t.Fatalf("errors.Is(err, ErrStalled) = false, got: %v", err)
	}
}
