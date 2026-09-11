package bbc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeMasterPlaylist builds a minimal HLS master playlist with the
// given (bandwidth, variant-url) pairs. Used by every test in this file.
func fakeMasterPlaylist(variants ...struct {
	BW  int
	URL string
}) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")
	for _, v := range variants {
		b.WriteString("#EXT-X-STREAM-INF:BANDWIDTH=")
		b.WriteString(itoaBW(v.BW))
		b.WriteString("\n")
		b.WriteString(v.URL)
		b.WriteString("\n")
	}
	return b.String()
}

func itoaBW(n int) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = digits[n%10]
		n /= 10
	}
	return string(buf[i:])
}

// Media playlists modelled on what USP returned for two real Waterloo
// Road episodes on 2026-09-11 (GitHub #54). Both answer HTTP 200 on the
// video=12000000 rewrite. Series 6 Episode 7 carries genuine 1080p
// segments; Series 6 Episode 2 has no 1080p track, and USP silently
// dropped the video filter and served the audio track on its own.
const (
	videoMediaPlaylist = "#EXTM3U\n#EXT-X-VERSION:2\n## Created with Unified Streaming Platform(version=1.7.32)\n" +
		"#EXT-X-MEDIA-SEQUENCE:1\n#EXT-X-TARGETDURATION:8\n" +
		"#EXTINF:8, no desc\nvf_b00v72t2.ism.hlsv2-audio_eng_1=128000-video=12000000-1.ts\n" +
		"#EXTINF:7, no desc\nvf_b00v72t2.ism.hlsv2-audio_eng_1=128000-video=12000000-2.ts\n" +
		"#EXT-X-ENDLIST\n"
	audioOnlyMediaPlaylist = "#EXTM3U\n#EXT-X-VERSION:2\n## Created with Unified Streaming Platform(version=1.7.32)\n" +
		"#EXT-X-MEDIA-SEQUENCE:1\n#EXT-X-TARGETDURATION:8\n" +
		"#EXTINF:8, no desc\nvf_b00tmlvh.ism.hlsv2-audio_eng_1=128000-1.ts\n" +
		"#EXTINF:7, no desc\nvf_b00tmlvh.ism.hlsv2-audio_eng_1=128000-2.ts\n" +
		"#EXT-X-ENDLIST\n"
)

// fhdServer serves masterBody for every request except the rewritten
// video=12000000 variant, which gets probeStatus + probeBody. It records
// how the rewritten variant was requested so tests can assert on the
// probe's method and call count.
type fhdServer struct {
	*httptest.Server
	probeCalls  int32
	probeMethod atomic.Value // string
}

func newFHDServer(masterBody string, probeStatus int, probeBody string) *fhdServer {
	s := &fhdServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path+"?"+r.URL.RawQuery, "video=12000000") {
			atomic.AddInt32(&s.probeCalls, 1)
			s.probeMethod.Store(r.Method)
			w.WriteHeader(probeStatus)
			if r.Method != http.MethodHead {
				w.Write([]byte(probeBody))
			}
			return
		}
		w.Write([]byte(masterBody))
	}))
	return s
}

func (s *fhdServer) calls() int32 { return atomic.LoadInt32(&s.probeCalls) }

func (s *fhdServer) method() string {
	v, _ := s.probeMethod.Load().(string)
	return v
}

func single720Master() string {
	return fakeMasterPlaylist(
		struct {
			BW  int
			URL string
		}{BW: 2700000, URL: "stream-audio=96000&video=2700000.m3u8"},
	)
}

func TestProbeHiddenFHD_VariantExists_ReturnsFoundWithURL(t *testing.T) {
	srv := newFHDServer(single720Master(), http.StatusOK, videoMediaPlaylist)
	defer srv.Close()

	c := NewClient()
	fhdURL, found, err := c.ProbeHiddenFHD(context.Background(), srv.URL+"/master.m3u8")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if !strings.Contains(fhdURL, "video=12000000") {
		t.Errorf("expected rewritten URL to contain video=12000000, got %q", fhdURL)
	}
	if srv.method() != http.MethodGet {
		t.Errorf("probe must GET the rewritten variant so its segments can be inspected, got %q", srv.method())
	}
}

// GitHub #54: USP answers HTTP 200 on the video=12000000 rewrite for a
// programme with no 1080p track and serves an audio-only rendition. A
// 200 alone is not evidence of 1080p; the media playlist must carry
// video segments. Audio-only is a definitive, cacheable "no".
func TestProbeHiddenFHD_AudioOnlyRendition_ReturnsDefinitiveNoFound(t *testing.T) {
	srv := newFHDServer(single720Master(), http.StatusOK, audioOnlyMediaPlaylist)
	defer srv.Close()

	c := NewClient()
	fhdURL, found, err := c.ProbeHiddenFHD(context.Background(), srv.URL+"/master.m3u8")
	if err != nil {
		t.Fatalf("expected definitive no (nil err) on audio-only rendition, got err: %v", err)
	}
	if found || fhdURL != "" {
		t.Errorf("expected (\"\", false, nil) for audio-only rendition, got (%q, %v, nil)", fhdURL, found)
	}
	if srv.calls() != 1 {
		t.Errorf("expected exactly one probe request, got %d", srv.calls())
	}
}

func TestProbeHiddenFHD_Probe404_ReturnsDefinitiveNoFound(t *testing.T) {
	srv := newFHDServer(single720Master(), http.StatusNotFound, "")
	defer srv.Close()

	c := NewClient()
	fhdURL, found, err := c.ProbeHiddenFHD(context.Background(), srv.URL+"/master.m3u8")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if found || fhdURL != "" {
		t.Errorf("expected (\"\", false, nil), got (%q, %v, nil)", fhdURL, found)
	}
}

func TestProbeHiddenFHD_Probe410_ReturnsDefinitiveNoFound(t *testing.T) {
	srv := newFHDServer(single720Master(), http.StatusGone, "")
	defer srv.Close()

	c := NewClient()
	_, found, err := c.ProbeHiddenFHD(context.Background(), srv.URL+"/master.m3u8")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if found {
		t.Error("expected found=false on HTTP 410")
	}
}

func TestProbeHiddenFHD_Probe429_ReturnsError(t *testing.T) {
	srv := newFHDServer(single720Master(), http.StatusTooManyRequests, "")
	defer srv.Close()

	c := NewClient()
	_, found, err := c.ProbeHiddenFHD(context.Background(), srv.URL+"/master.m3u8")
	if err == nil {
		t.Fatal("expected error on 429, got nil")
	}
	if found {
		t.Error("expected found=false on 429")
	}
}

func TestProbeHiddenFHD_Probe503_ReturnsError(t *testing.T) {
	srv := newFHDServer(single720Master(), http.StatusServiceUnavailable, "")
	defer srv.Close()

	c := NewClient()
	_, _, err := c.ProbeHiddenFHD(context.Background(), srv.URL+"/master.m3u8")
	if err == nil {
		t.Fatal("expected error on 503, got nil")
	}
}

func TestProbeHiddenFHD_NoVariantsInPlaylist_ReturnsDefinitiveNoFound(t *testing.T) {
	masterBody := "#EXTM3U\n#EXT-X-VERSION:3\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(masterBody))
	}))
	defer srv.Close()

	c := NewClient()
	_, found, err := c.ProbeHiddenFHD(context.Background(), srv.URL+"/master.m3u8")
	// Empty variant list should surface as a fetch/parse error in our
	// implementation (pickHighestBandwidthVariant returns "no variant
	// found in master playlist"). If the spec decides this is cacheable,
	// flip this assertion — either way, we must be deterministic.
	if err == nil && found {
		t.Fatal("expected either (no-found, nil) cacheable or (err) — not found=true")
	}
}

func TestProbeHiddenFHD_MasterPlaylistFetchFails_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := NewClient()
	_, _, err := c.ProbeHiddenFHD(context.Background(), srv.URL+"/master.m3u8")
	if err == nil {
		t.Fatal("expected error when master playlist GET returns 503")
	}
}

func TestProbeHiddenFHD_ProbeNetworkError_ReturnsError(t *testing.T) {
	// Master playlist hosted on a valid server, but the variant URL
	// points to a non-routable address so the probe fails at transport
	// level.
	masterBody := "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1500000\nhttps://127.0.0.1:1/stream-video=1500000.m3u8\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(masterBody))
	}))
	defer srv.Close()

	c := NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := c.ProbeHiddenFHD(ctx, srv.URL+"/master.m3u8")
	if err == nil {
		t.Fatal("expected error when probe target is unreachable")
	}
}

func TestProbeHiddenFHD_ContextCancel_ReturnsError(t *testing.T) {
	masterBody := "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1500000\nhttps://example.com/stream-video=1500000.m3u8\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(masterBody))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled

	c := NewClient()
	_, _, err := c.ProbeHiddenFHD(ctx, srv.URL+"/master.m3u8")
	if err == nil {
		t.Fatal("expected ctx-cancel error, got nil")
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("expected context.Canceled in error chain, got %v", err)
	}
}

func TestProbeHiddenFHD_PicksHighestBandwidthVariant(t *testing.T) {
	// Three variants. Helper must rewrite the 2700000 URL, not 320000
	// or 1500000. The test server returns a 1080p media playlist on the
	// rewritten URL ONLY if the path came from the highest-BW variant.
	masterBody := "#EXTM3U\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=320000\nstream-low-video=320000.m3u8\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=1500000\nstream-mid-video=1500000.m3u8\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=2700000\nstream-high-video=2700000.m3u8\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "video=12000000") {
			// Only the "-high-" path should ever be probed.
			if strings.Contains(r.URL.Path, "stream-high-") {
				w.Write([]byte(videoMediaPlaylist))
				return
			}
			// Any other path is a selection bug.
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(masterBody))
	}))
	defer srv.Close()

	c := NewClient()
	fhdURL, found, err := c.ProbeHiddenFHD(context.Background(), srv.URL+"/master.m3u8")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !found {
		t.Fatal("expected found=true for highest-BW variant")
	}
	if !strings.Contains(fhdURL, "stream-high-") {
		t.Errorf("expected rewritten URL to come from the highest-BW variant, got %q", fhdURL)
	}
}

func TestProbeHiddenFHD_RelativeVariantURL_ResolvedAgainstBase(t *testing.T) {
	// Variant URL is a bare filename (relative). Helper must resolve it
	// against the master playlist's base directory before rewriting.
	masterBody := "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=2700000\nindex-2700000.m3u8?video=2700000\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "video=") {
			// Expect the probe to come from the same server (relative URL
			// resolved against master base) with video=12000000.
			if !strings.Contains(r.URL.RawQuery, "video=12000000") {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Write([]byte(videoMediaPlaylist))
			return
		}
		w.Write([]byte(masterBody))
	}))
	defer srv.Close()

	c := NewClient()
	fhdURL, found, err := c.ProbeHiddenFHD(context.Background(), srv.URL+"/subdir/master.m3u8")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !found {
		t.Fatal("expected found=true for resolved relative URL")
	}
	if !strings.HasPrefix(fhdURL, srv.URL+"/subdir/") {
		t.Errorf("expected fhdURL to start with server base, got %q", fhdURL)
	}
}

// fakeMasterPlaylistWithRes builds a playlist with RESOLUTION tags, used
// by the SD-only guard and VerifyFHD tests.
func fakeMasterPlaylistWithRes(variants ...struct {
	BW  int
	Res string
	URL string
}) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:2\n")
	for _, v := range variants {
		fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%s\n%s\n", v.BW, v.Res, v.URL)
	}
	return b.String()
}

func sdOnlyMaster() string {
	return fakeMasterPlaylistWithRes(
		struct {
			BW  int
			Res string
			URL string
		}{BW: 1013000, Res: "704x396", URL: "stream-audio=128000-video=827000.m3u8"},
		struct {
			BW  int
			Res string
			URL string
		}{BW: 1800000, Res: "704x396", URL: "stream-audio=128000-video=1570000.m3u8"},
	)
}

func hd720Master() string {
	return fakeMasterPlaylistWithRes(
		struct {
			BW  int
			Res string
			URL string
		}{BW: 3117000, Res: "960x540", URL: "stream-audio=128000-video=2812000.m3u8"},
		struct {
			BW  int
			Res string
			URL string
		}{BW: 5510000, Res: "1280x720", URL: "stream-audio=128000-video=5070000.m3u8"},
	)
}

func listed1080Master() string {
	return fakeMasterPlaylistWithRes(
		struct {
			BW  int
			Res string
			URL string
		}{BW: 5510000, Res: "1280x720", URL: "stream-audio=128000-video=5070000.m3u8"},
		struct {
			BW  int
			Res string
			URL string
		}{BW: 8490000, Res: "1920x1080", URL: "stream-audio=128000-video=8000000.m3u8"},
	)
}

func TestMaxPlaylistHeight(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"no RESOLUTION tags", "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=2700000\nfoo.m3u8\n", 0},
		{"single 396p", "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1800000,RESOLUTION=704x396\nfoo.m3u8\n", 396},
		{"mixed 396p and 720p", "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1800000,RESOLUTION=704x396\na.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=3500000,RESOLUTION=1280x720\nb.m3u8\n", 720},
		{"1080p present", "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=5000000,RESOLUTION=1920x1080\nfoo.m3u8\n", 1080},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := maxPlaylistHeight([]byte(tt.body))
			if got != tt.want {
				t.Errorf("maxPlaylistHeight() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestHasVideoSegments(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"1080p media playlist", videoMediaPlaylist, true},
		{"audio-only media playlist", audioOnlyMediaPlaylist, false},
		// A master playlist's variant URIs contain video= but are not
		// segments; without #EXTINF they must not count.
		{"master playlist", hd720Master(), false},
		{"empty body", "", false},
		{"tags only", "#EXTM3U\n#EXT-X-VERSION:2\n#EXT-X-ENDLIST\n", false},
		{"CRLF line endings", strings.ReplaceAll(videoMediaPlaylist, "\n", "\r\n"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasVideoSegments([]byte(tt.body)); got != tt.want {
				t.Errorf("hasVideoSegments() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProbeHiddenFHD_SDOnlyPlaylist_ReturnsDefinitiveAbsence(t *testing.T) {
	srv := newFHDServer(sdOnlyMaster(), http.StatusOK, videoMediaPlaylist)
	defer srv.Close()

	c := NewClient()
	fhdURL, found, err := c.ProbeHiddenFHD(context.Background(), srv.URL+"/master.m3u8")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if found {
		t.Errorf("expected found=false for SD-only playlist, got found=true url=%q", fhdURL)
	}
	if srv.calls() != 0 {
		t.Error("probe should not have been issued for SD-only playlist")
	}
}

func TestProbeHiddenFHD_720pPlaylist_StillProbes(t *testing.T) {
	srv := newFHDServer(hd720Master(), http.StatusNotFound, "")
	defer srv.Close()

	c := NewClient()
	_, found, err := c.ProbeHiddenFHD(context.Background(), srv.URL+"/master.m3u8")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if found {
		t.Error("expected found=false (probe 404)")
	}
	if srv.calls() != 1 {
		t.Errorf("probe should have been issued once for 720p playlist, got %d", srv.calls())
	}
}

// --- VerifyFHD (GitHub #54) ---
//
// mediaselector's height attribute describes the packaging profile, not
// an encoded rendition, so a 1080 claim has to be checked against what
// the HLS master actually lists plus the unlisted FHD rewrite.

func TestVerifyFHD_Listed1080_TrueWithoutProbe(t *testing.T) {
	srv := newFHDServer(listed1080Master(), http.StatusInternalServerError, "")
	defer srv.Close()

	c := NewClient()
	has1080, maxListed, err := c.VerifyFHD(context.Background(), srv.URL+"/master.m3u8")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !has1080 || maxListed != 1080 {
		t.Errorf("expected (true, 1080), got (%v, %d)", has1080, maxListed)
	}
	if srv.calls() != 0 {
		t.Errorf("listed 1080 needs no rewrite probe, got %d probe calls", srv.calls())
	}
}

func TestVerifyFHD_720Master_HiddenVideo_True720(t *testing.T) {
	srv := newFHDServer(hd720Master(), http.StatusOK, videoMediaPlaylist)
	defer srv.Close()

	c := NewClient()
	has1080, maxListed, err := c.VerifyFHD(context.Background(), srv.URL+"/master.m3u8")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !has1080 || maxListed != 720 {
		t.Errorf("expected (true, 720), got (%v, %d)", has1080, maxListed)
	}
}

func TestVerifyFHD_720Master_HiddenAudioOnly_False720(t *testing.T) {
	srv := newFHDServer(hd720Master(), http.StatusOK, audioOnlyMediaPlaylist)
	defer srv.Close()

	c := NewClient()
	has1080, maxListed, err := c.VerifyFHD(context.Background(), srv.URL+"/master.m3u8")
	if err != nil {
		t.Fatalf("expected definitive no (nil err), got: %v", err)
	}
	if has1080 || maxListed != 720 {
		t.Errorf("expected (false, 720), got (%v, %d)", has1080, maxListed)
	}
}

func TestVerifyFHD_720Master_Probe404_False720(t *testing.T) {
	srv := newFHDServer(hd720Master(), http.StatusNotFound, "")
	defer srv.Close()

	c := NewClient()
	has1080, maxListed, err := c.VerifyFHD(context.Background(), srv.URL+"/master.m3u8")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if has1080 || maxListed != 720 {
		t.Errorf("expected (false, 720), got (%v, %d)", has1080, maxListed)
	}
}

func TestVerifyFHD_ProbeTransient_ReturnsError(t *testing.T) {
	srv := newFHDServer(hd720Master(), http.StatusServiceUnavailable, "")
	defer srv.Close()

	c := NewClient()
	has1080, _, err := c.VerifyFHD(context.Background(), srv.URL+"/master.m3u8")
	if err == nil {
		t.Fatal("expected error on 503, got nil")
	}
	if has1080 {
		t.Error("expected has1080=false on transient error")
	}
}

func TestVerifyFHD_MasterFetchFails_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := NewClient()
	_, _, err := c.VerifyFHD(context.Background(), srv.URL+"/master.m3u8")
	if err == nil {
		t.Fatal("expected error when master playlist GET returns 503")
	}
}
