package download

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
}

func (f *fakeDownloaderProber) ProbeHiddenFHD(ctx context.Context, hlsMasterURL string) (string, bool, error) {
	f.calls++
	f.seenCtx = ctx
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
	got := resolveHLSVariant(context.Background(), prober, masterURL)
	if got != prober.fhdURL {
		t.Errorf("expected FHD URL %q, got %q", prober.fhdURL, got)
	}
}

func TestResolveHLSVariant_FHDDefinitiveNo_ReturnsBestVariant(t *testing.T) {
	masterURL, srv := minimalMasterPlaylist(t)
	defer srv.Close()

	prober := &fakeDownloaderProber{found: false, err: nil}
	got := resolveHLSVariant(context.Background(), prober, masterURL)
	if !strings.Contains(got, "video=2700000") {
		t.Errorf("expected best variant URL (video=2700000), got %q", got)
	}
}

func TestResolveHLSVariant_FHDProberError_ReturnsBestVariant(t *testing.T) {
	masterURL, srv := minimalMasterPlaylist(t)
	defer srv.Close()

	prober := &fakeDownloaderProber{err: errors.New("FHD HEAD 503")}
	got := resolveHLSVariant(context.Background(), prober, masterURL)
	if !strings.Contains(got, "video=2700000") {
		t.Errorf("expected fallback to best variant on error, got %q", got)
	}
}

func TestResolveHLSVariant_NilProber_ReturnsBestVariant(t *testing.T) {
	masterURL, srv := minimalMasterPlaylist(t)
	defer srv.Close()

	got := resolveHLSVariant(context.Background(), nil, masterURL)
	if !strings.Contains(got, "video=2700000") {
		t.Errorf("expected best variant on nil prober, got %q", got)
	}
}

func TestResolveHLSVariant_RespectsContextCancel(t *testing.T) {
	masterURL, srv := minimalMasterPlaylist(t)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled

	prober := &fakeDownloaderProber{delayResp: 5 * time.Second}
	got := resolveHLSVariant(ctx, prober, masterURL)

	// The prober's ProbeHiddenFHD should have seen the cancelled ctx
	// and returned ctx.Err(), which resolveHLSVariant treats as an
	// error and falls back to bestURL.
	if !strings.Contains(got, "video=2700000") {
		t.Errorf("expected fallback to best variant on ctx cancel, got %q", got)
	}
	if prober.calls == 1 && prober.seenCtx.Err() == nil {
		t.Errorf("expected prober to see cancelled ctx, but ctx.Err() == nil")
	}
}
