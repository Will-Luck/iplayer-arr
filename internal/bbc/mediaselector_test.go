package bbc

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestMediaSelectorResolve(t *testing.T) {
	fixture, err := os.ReadFile("testdata/mediaselector.xml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write(fixture)
	}))
	defer srv.Close()

	ms := NewMediaSelector(NewClient())
	ms.BaseURL = srv.URL

	streams, err := ms.Resolve("b039d080")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if len(streams.Video) < 2 {
		t.Fatalf("expected >= 2 video streams, got %d", len(streams.Video))
	}

	// should be sorted best first
	best := streams.Video[0]
	if best.Height != 720 {
		t.Errorf("best height = %d, want 720", best.Height)
	}
	if best.URL == "" {
		t.Error("best URL is empty")
	}

	if streams.SubtitleURL == "" {
		t.Error("expected subtitle URL")
	}
}

// Streams of equal height and bitrate must keep mediaselector's order, so
// that "the first HLS entry" in the downloader's pickStream means the first
// supplier the BBC listed. sort.Slice only happens to keep that order for
// 12 or fewer entries (insertion sort); above that it may reorder ties.
func TestParseResponseKeepsSupplierOrderForEqualStreams(t *testing.T) {
	const perHeight = 10 // 2 heights x 10 suppliers = 20 entries, above 12
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><mediaSelection>`)
	// 720 is listed before 1080, so the sort has real work to do.
	for _, h := range []int{720, 1080} {
		fmt.Fprintf(&b, `<media kind="video" bitrate="5000" width="%d" height="%d">`, h*16/9, h)
		for i := 0; i < perHeight; i++ {
			fmt.Fprintf(&b, `<connection supplier="sup%02d" transferFormat="hls" protocol="https" href="https://cdn%02d.example/%d.m3u8"/>`, i, i, h)
		}
		b.WriteString(`</media>`)
	}
	b.WriteString(`</mediaSelection>`)

	ms := NewMediaSelector(NewClient())
	got, err := ms.parseResponse([]byte(b.String()))
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	if len(got.Video) != 2*perHeight {
		t.Fatalf("got %d streams, want %d", len(got.Video), 2*perHeight)
	}

	var gotOrder, wantOrder []string
	for _, h := range []int{1080, 720} {
		for i := 0; i < perHeight; i++ {
			wantOrder = append(wantOrder, fmt.Sprintf("%d/sup%02d", h, i))
		}
	}
	for _, s := range got.Video {
		gotOrder = append(gotOrder, fmt.Sprintf("%d/%s", s.Height, s.Supplier))
	}
	if strings.Join(gotOrder, " ") != strings.Join(wantOrder, " ") {
		t.Errorf("stream order changed:\n got  %v\n want %v", gotOrder, wantOrder)
	}
}

func TestMediaSelectorGeoBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<mediaSelection><error id="geolocation"/></mediaSelection>`))
	}))
	defer srv.Close()

	ms := NewMediaSelector(NewClient())
	ms.BaseURL = srv.URL

	_, err := ms.Resolve("b039d080")
	if err == nil {
		t.Fatal("expected geo-block error")
	}
	if !IsGeoBlocked(err) {
		t.Errorf("expected geo-block error, got: %v", err)
	}
}
