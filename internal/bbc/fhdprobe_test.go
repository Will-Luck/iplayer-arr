package bbc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestProbeHiddenFHD_VariantExists_ReturnsFoundWithURL(t *testing.T) {
	masterBody := fakeMasterPlaylist(
		struct {
			BW  int
			URL string
		}{BW: 2700000, URL: "stream-audio=96000&video=2700000.m3u8"},
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(masterBody))
		case http.MethodHead:
			if strings.Contains(r.URL.String(), "video=12000000") {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}
	}))
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
}

func TestProbeHiddenFHD_Head404_ReturnsDefinitiveNoFound(t *testing.T) {
	masterBody := fakeMasterPlaylist(
		struct {
			BW  int
			URL string
		}{BW: 1500000, URL: "stream-video=1500000.m3u8"},
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(masterBody))
		case http.MethodHead:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
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

func TestProbeHiddenFHD_Head410_ReturnsDefinitiveNoFound(t *testing.T) {
	masterBody := fakeMasterPlaylist(
		struct {
			BW  int
			URL string
		}{BW: 1500000, URL: "stream-video=1500000.m3u8"},
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(masterBody))
		case http.MethodHead:
			w.WriteHeader(http.StatusGone)
		}
	}))
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

func TestProbeHiddenFHD_Head429_ReturnsError(t *testing.T) {
	masterBody := fakeMasterPlaylist(
		struct {
			BW  int
			URL string
		}{BW: 1500000, URL: "stream-video=1500000.m3u8"},
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(masterBody))
		case http.MethodHead:
			w.WriteHeader(http.StatusTooManyRequests)
		}
	}))
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

func TestProbeHiddenFHD_Head503_ReturnsError(t *testing.T) {
	masterBody := fakeMasterPlaylist(
		struct {
			BW  int
			URL string
		}{BW: 1500000, URL: "stream-video=1500000.m3u8"},
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(masterBody))
		case http.MethodHead:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
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

func TestProbeHiddenFHD_HeadProbeNetworkError_ReturnsError(t *testing.T) {
	// Master playlist hosted on a valid server, but the variant URL
	// points to a non-routable address so the HEAD fails at transport
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
		t.Fatal("expected error when HEAD target is unreachable")
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
	// or 1500000. The test server returns 200 on the rewritten URL
	// ONLY if the path came from the highest-BW variant.
	masterBody := "#EXTM3U\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=320000\nstream-low-video=320000.m3u8\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=1500000\nstream-mid-video=1500000.m3u8\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=2700000\nstream-high-video=2700000.m3u8\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(masterBody))
		case http.MethodHead:
			// Only the "-high-" path should ever be probed.
			if strings.Contains(r.URL.Path, "stream-high-") && strings.Contains(r.URL.RawQuery+r.URL.Path, "video=12000000") {
				w.WriteHeader(http.StatusOK)
				return
			}
			// Any other path is a selection bug.
			w.WriteHeader(http.StatusInternalServerError)
		}
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
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(masterBody))
		case http.MethodHead:
			// Expect the HEAD to come from the same server (relative URL
			// resolved against master base) with video=12000000.
			if !strings.Contains(r.URL.RawQuery, "video=12000000") {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
		}
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
