package download

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// multiVariantMasterPlaylist serves a master playlist with three
// RESOLUTION-tagged variants (396p / 540p / 720p). Each variant URL
// carries video=N so the FHD rewrite path is exercised when probed.
func multiVariantMasterPlaylist(t *testing.T) (string, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "#EXTM3U\n"+
			"#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=704x396\nhttps://example.com/audio=96000&video=800000.m3u8\n"+
			"#EXT-X-STREAM-INF:BANDWIDTH=1500000,RESOLUTION=960x540\nhttps://example.com/audio=96000&video=1500000.m3u8\n"+
			"#EXT-X-STREAM-INF:BANDWIDTH=2700000,RESOLUTION=1280x720\nhttps://example.com/audio=96000&video=2700000.m3u8\n")
	}))
	return srv.URL + "/master.m3u8", srv
}

// #45: a sub-1080p request must select the matching listed variant and
// must NOT trigger the unlisted-1080p FHD upgrade.
func TestResolveHLSVariant_HonoursRequestedHeight(t *testing.T) {
	masterURL, srv := multiVariantMasterPlaylist(t)
	defer srv.Close()

	// prober would return a 1080p URL if asked; it must not be asked.
	prober := &fakeDownloaderProber{fhdURL: "https://example.com/fhd-video=12000000.m3u8", found: true}
	got := resolveHLSVariant(context.Background(), prober, masterURL, 396)
	if !strings.Contains(got, "video=800000") {
		t.Errorf("expected 396p variant (video=800000), got %q", got)
	}
	if prober.calls != 0 {
		t.Errorf("FHD probe must not run for a sub-1080p request; calls=%d", prober.calls)
	}
}

// #45: a mid-ladder request resolves to the exact listed variant.
func TestResolveHLSVariant_MidLadderHeight(t *testing.T) {
	masterURL, srv := multiVariantMasterPlaylist(t)
	defer srv.Close()

	got := resolveHLSVariant(context.Background(), nil, masterURL, 540)
	if !strings.Contains(got, "video=1500000") {
		t.Errorf("expected 540p variant (video=1500000), got %q", got)
	}
}

// #45: a 1080p request still runs the FHD probe (preserving the feature)
// and returns the unlisted 1080p URL when found.
func TestResolveHLSVariant_1080Request_RunsFHDProbe(t *testing.T) {
	masterURL, srv := multiVariantMasterPlaylist(t)
	defer srv.Close()

	prober := &fakeDownloaderProber{fhdURL: "https://example.com/fhd-video=12000000.m3u8", found: true}
	got := resolveHLSVariant(context.Background(), prober, masterURL, 1080)
	if got != prober.fhdURL {
		t.Errorf("expected FHD URL for 1080p request, got %q", got)
	}
	if prober.calls != 1 {
		t.Errorf("expected exactly one FHD probe for a 1080p request, got %d", prober.calls)
	}
}

// #45: when no variant carries a RESOLUTION tag, height selection can't
// apply, so it falls back to the highest-bandwidth variant.
func TestResolveHLSVariant_NoResolutionTags_FallsBackToBandwidth(t *testing.T) {
	masterURL, srv := minimalMasterPlaylist(t)
	defer srv.Close()

	got := resolveHLSVariant(context.Background(), nil, masterURL, 396)
	if !strings.Contains(got, "video=2700000") {
		t.Errorf("expected bandwidth fallback (video=2700000), got %q", got)
	}
}

func TestSelectVariantByHeight(t *testing.T) {
	v := func(bw, h int, url string) hlsVariant { return hlsVariant{bandwidth: bw, height: h, url: url} }
	ladder := []hlsVariant{v(800000, 396, "396"), v(1500000, 540, "540"), v(2700000, 720, "720")}

	cases := []struct {
		name    string
		in      []hlsVariant
		target  int
		wantURL string
		wantOK  bool
	}{
		{"exact-396", ladder, 396, "396", true},
		{"exact-540", ladder, 540, "540", true},
		{"between-prefers-below", ladder, 480, "396", true}, // largest at/below
		{"above-all-picks-highest-below", ladder, 1080, "720", true},
		{"below-all-picks-smallest-above", ladder, 200, "396", true},
		{"tie-height-prefers-bandwidth", []hlsVariant{v(900000, 396, "lo"), v(1100000, 396, "hi")}, 396, "hi", true},
		{"no-resolution-tags", []hlsVariant{v(2700000, 0, "x")}, 396, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := selectVariantByHeight(c.in, c.target)
			if ok != c.wantOK {
				t.Fatalf("ok=%v, want %v", ok, c.wantOK)
			}
			if ok && got.url != c.wantURL {
				t.Errorf("url=%q, want %q", got.url, c.wantURL)
			}
		})
	}
}

// #46: reconnect options must be present and precede -i (input options),
// and the output path must remain the final argument.
func TestBuildFFmpegArgs_ReconnectBeforeInput(t *testing.T) {
	args := buildFFmpegArgs("https://cdn.example/stream.m3u8", "/out/file.mp4")

	for _, want := range []string{"-reconnect", "-reconnect_streamed", "-reconnect_on_network_error", "-reconnect_delay_max"} {
		if argIndex(args, want) < 0 {
			t.Errorf("buildFFmpegArgs missing %q: %v", want, args)
		}
	}
	rIdx, iIdx := argIndex(args, "-reconnect"), argIndex(args, "-i")
	if rIdx < 0 || iIdx < 0 || rIdx > iIdx {
		t.Errorf("-reconnect (%d) must precede -i (%d): %v", rIdx, iIdx, args)
	}
	if args[len(args)-1] != "/out/file.mp4" {
		t.Errorf("output path must be the last arg, got %q", args[len(args)-1])
	}
}

func TestDegradeHeight(t *testing.T) {
	cases := []struct {
		requested, steps, want int
	}{
		{1080, 1, 720},
		{1080, 2, 540},
		{1080, 3, 396},
		{1080, 5, 396}, // clamped to lowest rung
		{720, 1, 540},
		{540, 1, 396},
		{396, 1, 396}, // already lowest
		{2160, 1, 1080},
		{1080, 0, 1080}, // no degrade for steps < 1
		{480, 1, 396},   // between rungs -> snaps below, then steps down
	}
	for _, c := range cases {
		if got := degradeHeight(c.requested, c.steps); got != c.want {
			t.Errorf("degradeHeight(%d, %d) = %d, want %d", c.requested, c.steps, got, c.want)
		}
	}
}

// #46/F1: degrade must key off a dedicated CDN-failure counter so that
// not-yet-available retries do not inflate the quality step-down.
func TestFailDownload_CDNFailureCounter(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	m := NewManager(st, filepath.Join(dir, "downloads"), 1, nil, nil, nil, nil)

	dl := &store.Download{ID: "c1", PID: "p1", Status: store.StatusPending, Quality: "1080p"}
	st.PutDownload(dl)

	// not-yet-available must NOT bump the CDN counter
	m.failDownload(dl, store.FailCodeNotYetAvailable, fmt.Errorf("nya"))
	if dl.CDNFailures != 0 {
		t.Fatalf("NotYetAvailable bumped CDNFailures to %d, want 0", dl.CDNFailures)
	}
	// ffmpeg + truncated must bump it
	m.failDownload(dl, store.FailCodeFFmpeg, fmt.Errorf("ffmpeg"))
	m.failDownload(dl, store.FailCodeTruncated, fmt.Errorf("trunc"))
	if dl.CDNFailures != 2 {
		t.Fatalf("CDNFailures = %d, want 2 after ffmpeg+truncated", dl.CDNFailures)
	}
	// geo-blocked must NOT bump it
	m.failDownload(dl, store.FailCodeGeoBlocked, fmt.Errorf("geo"))
	if dl.CDNFailures != 2 {
		t.Fatalf("GeoBlocked bumped CDNFailures to %d, want 2", dl.CDNFailures)
	}
}

func argIndex(ss []string, s string) int {
	for i, v := range ss {
		if v == s {
			return i
		}
	}
	return -1
}
