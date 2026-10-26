package bbc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
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

	if len(streams.SubtitleURLs) == 0 {
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

// selectionUnavailableXML is mediaselector's own "nothing for this vpid"
// answer, in the same shape as the geolocation error above.
const selectionUnavailableXML = `<?xml version="1.0" encoding="UTF-8"?><mediaSelection xmlns="http://bbc.co.uk/2008/mp/mediaselection"><error id="selectionunavailable"/></mediaSelection>`

// Gitea #94: when no attempt gets a mediaSelection document back, the BBC
// did not answer, and the error must say so instead of reading as "no
// streams".
func TestResolveCtx_NoAnswerIsMediaSelectorUnavailable(t *testing.T) {
	page, err := os.ReadFile("testdata/ibl/bbc_error_page.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"503 on every attempt", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}},
		{"redirect to the HTML error page", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/error.html" {
				http.Redirect(w, r, "/error.html", http.StatusFound)
				return
			}
			w.Header().Set("Content-Type", "text/html")
			w.Write(page)
		}},
		{"empty 200", func(w http.ResponseWriter, r *http.Request) {}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			ms := NewMediaSelector(NewClient())
			ms.BaseURL = srv.URL

			_, err := ms.ResolveCtx(context.Background(), "m0033jgj")
			if !errors.Is(err, ErrMediaSelectorUnavailable) {
				t.Fatalf("err = %v, want it to match ErrMediaSelectorUnavailable", err)
			}
			if IsGeoBlocked(err) {
				t.Errorf("err = %v reads as a geo-block", err)
			}
			if !strings.Contains(err.Error(), "m0033jgj") {
				t.Errorf("err = %q does not name the vpid", err)
			}
		})
	}
}

// A parsed mediaselector answer without usable video is an answer about
// the programme, even when another mediaset is down at the same time.
func TestResolveCtx_BBCAnswerWithoutVideoIsNotAnOutage(t *testing.T) {
	captionsOnly := `<?xml version="1.0" encoding="UTF-8"?><mediaSelection xmlns="http://bbc.co.uk/2008/mp/mediaselection"><media kind="captions" type="application/ttaf+xml"><connection supplier="mf_akamai" transferFormat="plain" protocol="https" href="https://subs.example/captions.xml"/></media></mediaSelection>`
	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"selectionunavailable", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/xml;charset=UTF-8")
			w.Write([]byte(selectionUnavailableXML))
		}},
		{"document with no video", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/xml;charset=UTF-8")
			w.Write([]byte(captionsOnly))
		}},
		{"answer on one mediaset, outage on the other", func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/mediaset/pc/") {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "text/xml;charset=UTF-8")
			w.Write([]byte(selectionUnavailableXML))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			ms := NewMediaSelector(NewClient())
			ms.BaseURL = srv.URL

			_, err := ms.ResolveCtx(context.Background(), "m0033jgj")
			if err == nil {
				t.Fatal("want an error: the answer lists no usable video")
			}
			if errors.Is(err, ErrMediaSelectorUnavailable) {
				t.Errorf("err = %v; the BBC answered, so this is not an outage", err)
			}
			if IsGeoBlocked(err) {
				t.Errorf("err = %v reads as a geo-block", err)
			}
		})
	}
}

// realShapeCaptions lists captions the way mediaselector lists every
// media element: per supplier, in priority order, an http and then an
// https connection (the layout of testdata/ibl/mediaselector_m0033jgj.xml).
const realShapeCaptions = `<?xml version="1.0" encoding="UTF-8"?>
<mediaSelection xmlns="http://bbc.co.uk/2008/mp/mediaselection">
  <media kind="video" type="video/mp4" encoding="h264" bitrate="5070" width="1280" height="720">
    <connection priority="10" supplier="mf_akamai" transferFormat="hls" protocol="https" href="https://vod-a.example/720.m3u8"/>
  </media>
  <media kind="captions" type="application/ttaf+xml">
    <connection priority="10" supplier="mf_akamai" transferFormat="plain" protocol="http" href="http://subs-a.example/captions.xml"/>
    <connection priority="10" supplier="mf_akamai" transferFormat="plain" protocol="https" href="https://subs-a.example/captions.xml"/>
    <connection priority="30" supplier="mf_cloudfront" transferFormat="plain" protocol="http" href="http://subs-b.example/captions.xml"/>
    <connection priority="30" supplier="mf_cloudfront" transferFormat="plain" protocol="https" href="https://subs-b.example/captions.xml"/>
  </media>
</mediaSelection>`

// Gitea #91: the subtitle URL is the https connection of the FIRST
// captions supplier. The old loop overwrote it on every https
// connection, so the last supplier won.
func TestParseResponseSubtitleURLIsFirstHTTPSSupplier(t *testing.T) {
	ms := NewMediaSelector(NewClient())
	got, err := ms.parseResponse([]byte(realShapeCaptions))
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	if want := "https://subs-a.example/captions.xml"; len(got.SubtitleURLs) == 0 || got.SubtitleURLs[0] != want {
		t.Errorf("SubtitleURLs = %q, want first %q", got.SubtitleURLs, want)
	}
}

// Every captions URL is kept, https first, each group in supplier order,
// so the downloader can fail over. Gitea #91.
func TestParseResponseKeepsEveryCaptionsURLHTTPSFirst(t *testing.T) {
	ms := NewMediaSelector(NewClient())
	got, err := ms.parseResponse([]byte(realShapeCaptions))
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	want := []string{
		"https://subs-a.example/captions.xml",
		"https://subs-b.example/captions.xml",
		"http://subs-a.example/captions.xml",
		"http://subs-b.example/captions.xml",
	}
	if !slices.Equal(got.SubtitleURLs, want) {
		t.Errorf("SubtitleURLs = %v, want %v", got.SubtitleURLs, want)
	}
}

// A captions URL listed twice is kept once, and an empty href is
// skipped, so the downloader never asks the same supplier twice. Gitea #91.
func TestParseResponseCaptionsURLsDeduplicated(t *testing.T) {
	body := `<?xml version="1.0" encoding="UTF-8"?><mediaSelection xmlns="http://bbc.co.uk/2008/mp/mediaselection"><media kind="captions" type="application/ttaf+xml"><connection supplier="mf_akamai" transferFormat="plain" protocol="https" href="https://subs-a.example/captions.xml"/></media><media kind="captions" type="application/ttaf+xml"><connection supplier="mf_akamai" transferFormat="plain" protocol="https" href="https://subs-a.example/captions.xml"/><connection supplier="mf_cloudfront" transferFormat="plain" protocol="https" href=""/></media></mediaSelection>`
	ms := NewMediaSelector(NewClient())
	got, err := ms.parseResponse([]byte(body))
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	if want := []string{"https://subs-a.example/captions.xml"}; !slices.Equal(got.SubtitleURLs, want) {
		t.Errorf("SubtitleURLs = %v, want %v", got.SubtitleURLs, want)
	}
}

// The mediaselector sends selectionunavailable with a 404 for a
// programme with no streams. client.go drops 4xx bodies, so the status
// itself has to count as the BBC's answer. 429, 5xx and transport
// failures stay an outage.
func TestResolveCtx_Status404IsAnAnswerNotAnOutage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml;charset=UTF-8")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(selectionUnavailableXML))
	}))
	defer srv.Close()
	ms := NewMediaSelector(NewClient())
	ms.BaseURL = srv.URL

	_, err := ms.ResolveCtx(context.Background(), "m0033jgj")
	if err == nil {
		t.Fatal("want an error: no streams")
	}
	if errors.Is(err, ErrMediaSelectorUnavailable) {
		t.Errorf("err = %v; a 404 answer is not an outage", err)
	}
}

func TestResolveCtx_Status429IsStillAnOutage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	ms := NewMediaSelector(NewClient())
	ms.BaseURL = srv.URL

	_, err := ms.ResolveCtx(context.Background(), "m0033jgj")
	if !errors.Is(err, ErrMediaSelectorUnavailable) {
		t.Errorf("err = %v, want ErrMediaSelectorUnavailable", err)
	}
}
