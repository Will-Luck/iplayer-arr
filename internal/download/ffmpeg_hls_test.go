package download

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeDownloaderProber struct {
	fhdURL    string
	found     bool
	err       error
	seenCtx   context.Context
	calls     int
	delayResp time.Duration
	onCall    func() // runs when the probe starts, before any delay
}

func (f *fakeDownloaderProber) ProbeHiddenFHD(ctx context.Context, hlsMasterURL string) (string, bool, error) {
	f.calls++
	f.seenCtx = ctx
	if f.onCall != nil {
		f.onCall()
	}
	if f.delayResp > 0 {
		select {
		case <-time.After(f.delayResp):
		case <-ctx.Done():
			return "", false, ctx.Err()
		}
	}
	return f.fhdURL, f.found, f.err
}

// minimalMasterPlaylist returns a master playlist body with a single
// highest-BW variant whose URL contains video=N (so the FHD rewrite
// applies).
func minimalMasterPlaylist(t *testing.T) (string, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=2700000\n%s/stream-audio=96000&video=2700000.m3u8\n", "https://example.com")
	}))
	return srv.URL + "/master.m3u8", srv
}

func TestResolveHLSVariant_FHDFound_ReturnsFHDURL(t *testing.T) {
	masterURL, srv := minimalMasterPlaylist(t)
	defer srv.Close()

	prober := &fakeDownloaderProber{fhdURL: "https://example.com/fhd-video=12000000.m3u8", found: true}
	got := resolveOK(t, context.Background(), prober, masterURL, 0)
	if got != prober.fhdURL {
		t.Errorf("expected FHD URL %q, got %q", prober.fhdURL, got)
	}
}

func TestResolveHLSVariant_FHDDefinitiveNo_ReturnsBestVariant(t *testing.T) {
	masterURL, srv := minimalMasterPlaylist(t)
	defer srv.Close()

	prober := &fakeDownloaderProber{found: false, err: nil}
	got := resolveOK(t, context.Background(), prober, masterURL, 0)
	if !strings.Contains(got, "video=2700000") {
		t.Errorf("expected best variant URL (video=2700000), got %q", got)
	}
}

func TestResolveHLSVariant_FHDProberError_ReturnsBestVariant(t *testing.T) {
	masterURL, srv := minimalMasterPlaylist(t)
	defer srv.Close()

	prober := &fakeDownloaderProber{err: errors.New("FHD HEAD 503")}
	got := resolveOK(t, context.Background(), prober, masterURL, 0)
	if !strings.Contains(got, "video=2700000") {
		t.Errorf("expected fallback to best variant on error, got %q", got)
	}
}

func TestResolveHLSVariant_NilProber_ReturnsBestVariant(t *testing.T) {
	masterURL, srv := minimalMasterPlaylist(t)
	defer srv.Close()

	got := resolveOK(t, context.Background(), nil, masterURL, 0)
	if !strings.Contains(got, "video=2700000") {
		t.Errorf("expected best variant on nil prober, got %q", got)
	}
}

func TestResolveHLSVariant_RespectsContextCancel(t *testing.T) {
	masterURL, srv := minimalMasterPlaylist(t)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Cancel once the probe starts. The master fetch honours ctx too, so a
	// ctx cancelled up front would stop before the probe ever ran.
	prober := &fakeDownloaderProber{delayResp: 5 * time.Second, onCall: cancel}
	got := resolveOK(t, ctx, prober, masterURL, 0)

	// The prober's ProbeHiddenFHD should have seen the cancelled ctx
	// and returned ctx.Err(), which resolveHLSVariant treats as an
	// error and falls back to bestURL.
	if !strings.Contains(got, "video=2700000") {
		t.Errorf("expected fallback to best variant on ctx cancel, got %q", got)
	}
	if prober.calls != 1 {
		t.Fatalf("expected one FHD probe, got %d", prober.calls)
	}
	if prober.seenCtx.Err() == nil {
		t.Errorf("expected prober to see cancelled ctx, but ctx.Err() == nil")
	}
}

// resolveOK calls resolveHLSVariant for a master playlist that is served
// normally, failing the test on any error.
func resolveOK(t *testing.T, ctx context.Context, prober downloaderFHDProber, masterURL string, targetHeight int) string {
	t.Helper()
	got, err := resolveHLSVariant(ctx, prober, masterURL, targetHeight)
	if err != nil {
		t.Fatalf("resolveHLSVariant(%q): %v", masterURL, err)
	}
	return got
}

// GitHub #55: a master playlist the supplier refuses must come back as a
// MasterPlaylistError carrying the status and host, not as the master URL
// (which ffmpeg would only fetch again and fail on the same way).
func TestResolveHLSVariant_MasterHTTPErrorReturnsTypedError(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// A refusal body that happens to look like a playlist must
				// still be refused on the status alone.
				w.WriteHeader(status)
				fmt.Fprint(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=2700000\nvideo=2700000.m3u8\n")
			}))
			defer srv.Close()
			masterURL := srv.URL + "/master.m3u8"

			prober := &fakeDownloaderProber{fhdURL: "https://example.com/fhd.m3u8", found: true}
			got, err := resolveHLSVariant(context.Background(), prober, masterURL, 0)

			var mpErr *MasterPlaylistError
			if !errors.As(err, &mpErr) {
				t.Fatalf("err = %v (%T), want *MasterPlaylistError", err, err)
			}
			if mpErr.Status != status {
				t.Errorf("Status = %d, want %d", mpErr.Status, status)
			}
			if want := hostOf(t, srv.URL); mpErr.Host != want {
				t.Errorf("Host = %q, want %q", mpErr.Host, want)
			}
			if got != "" {
				t.Errorf("returned URL %q alongside the error, want empty", got)
			}
			if prober.calls != 0 {
				t.Errorf("FHD probe ran %d time(s) after a refused master, want 0", prober.calls)
			}
		})
	}
}

// GitHub #55: a connection failure is a transport error: Status 0, the
// host, and the underlying cause.
func TestResolveHLSVariant_MasterConnectionErrorReturnsTypedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	masterURL := srv.URL + "/master.m3u8"
	host := hostOf(t, srv.URL)
	srv.Close() // nothing listens on the port any more

	got, err := resolveHLSVariant(context.Background(), nil, masterURL, 0)

	var mpErr *MasterPlaylistError
	if !errors.As(err, &mpErr) {
		t.Fatalf("err = %v (%T), want *MasterPlaylistError", err, err)
	}
	if mpErr.Status != 0 {
		t.Errorf("Status = %d, want 0 for a transport failure", mpErr.Status)
	}
	if mpErr.Host != host {
		t.Errorf("Host = %q, want %q", mpErr.Host, host)
	}
	if mpErr.Err == nil {
		t.Error("Err = nil, want the transport error")
	}
	if got != "" {
		t.Errorf("returned URL %q alongside the error, want empty", got)
	}
}

// A 200 master playlist with no variant lines keeps the old behaviour:
// hand the master URL to ffmpeg, no error.
func TestResolveHLSVariant_MasterWithoutVariantsReturnsMasterURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "#EXTM3U\n#EXT-X-VERSION:3\n")
	}))
	defer srv.Close()
	masterURL := srv.URL + "/master.m3u8"

	got, err := resolveHLSVariant(context.Background(), nil, masterURL, 720)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got != masterURL {
		t.Errorf("got %q, want the master URL %q", got, masterURL)
	}
}

// The master fetch honours ctx. A cancelled download is not a supplier
// refusal, so the error must not be a MasterPlaylistError.
func TestResolveHLSVariant_CancelledContextIsNotARefusal(t *testing.T) {
	masterURL, srv := minimalMasterPlaylist(t)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	prober := &fakeDownloaderProber{}
	_, err := resolveHLSVariant(ctx, prober, masterURL, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	var mpErr *MasterPlaylistError
	if errors.As(err, &mpErr) {
		t.Errorf("a cancelled fetch came back as a supplier refusal: %v", err)
	}
	if prober.calls != 0 {
		t.Errorf("FHD probe ran %d time(s) on a cancelled ctx, want 0", prober.calls)
	}
}

// GitHub #55: RunFFmpeg hands the *MasterPlaylistError back to the worker
// and never starts ffmpeg on the refused master URL. A fake ffmpeg on PATH
// leaves a marker if it is run at all.
func TestRunFFmpeg_RefusedMasterNeverReachesFFmpeg(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "Forbidden", http.StatusForbidden)
	}))
	defer srv.Close()

	binDir := t.TempDir()
	marker := filepath.Join(binDir, "ffmpeg-ran")
	script := fmt.Sprintf("#!/bin/sh\ntouch '%s'\nexit 1\n", marker)
	if err := os.WriteFile(filepath.Join(binDir, "ffmpeg"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	job := FFmpegJob{
		StreamURL:    srv.URL + "/vod/master.m3u8",
		OutputPath:   filepath.Join(t.TempDir(), "out.mp4"),
		TargetHeight: 720,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := RunFFmpeg(ctx, job)

	var mpErr *MasterPlaylistError
	if !errors.As(err, &mpErr) {
		t.Fatalf("RunFFmpeg err = %v (%T), want *MasterPlaylistError", err, err)
	}
	if mpErr.Status != http.StatusForbidden {
		t.Errorf("Status = %d, want %d", mpErr.Status, http.StatusForbidden)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("ffmpeg ran on the refused master URL")
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("master URL fetched %d time(s), want 1 (the variant lookup only)", n)
	}
}

func hostOf(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse %q: %v", rawURL, err)
	}
	return u.Hostname()
}
