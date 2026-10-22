package bbc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// iblEpisodeServer serves real iBL captures from testdata/ibl at
// /episodes/{pid} and answers 404 for any other path, so a resolver that
// asks the wrong endpoint fails loudly. It records each request.
type iblEpisodeServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []*http.Request
}

func newIBLEpisodeServer(t *testing.T, fixtures map[string]string) *iblEpisodeServer {
	t.Helper()
	bodies := make(map[string][]byte, len(fixtures))
	for pid, file := range fixtures {
		b, err := os.ReadFile("testdata/ibl/" + file)
		if err != nil {
			t.Fatalf("read fixture %s: %v", file, err)
		}
		bodies["/episodes/"+pid] = b
	}
	s := &iblEpisodeServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, r)
		s.mu.Unlock()
		body, ok := bodies[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write(body)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *iblEpisodeServer) requestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

// newIBLBodyServer answers /episodes/{pid} with body, for shapes no real
// capture has.
func newIBLBodyServer(t *testing.T, pid, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/episodes/"+pid {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// reorderedVersions returns a real capture with episodes[0].versions put
// in the given index order. The file on disk stays a byte-for-byte copy.
func reorderedVersions(t *testing.T, file string, order ...int) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/ibl/" + file)
	if err != nil {
		t.Fatalf("read fixture %s: %v", file, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode %s: %v", file, err)
	}
	ep := doc["episodes"].([]any)[0].(map[string]any)
	versions := ep["versions"].([]any)
	reordered := make([]any, 0, len(order))
	for _, i := range order {
		reordered = append(reordered, versions[i])
	}
	ep["versions"] = reordered
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return string(out)
}

// fixtureNow is a moment at which every captured version except the
// deliberately future ones has started.
var fixtureNow = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

func newFixtureResolver(baseURL string, now time.Time) *PlaylistResolver {
	r := NewPlaylistResolver(NewClient())
	r.BaseURL = baseURL
	r.now = func() time.Time { return now }
	return r
}

// GitHub #57: with BBC /programmes redirecting to its error page the
// resolver reported "parse playlist: invalid character '<' looking for
// beginning of value". Whatever endpoint serves the HTML, the error must
// say what happened.
func TestResolve_GH57_HTMLErrorPageIsReported(t *testing.T) {
	srv, _ := newErrorPageServer(t)
	r := newFixtureResolver(srv.URL, fixtureNow)

	_, err := r.ResolveCtx(context.Background(), "m0033jgh")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"HTML", "Service Temporarily Unavailable"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}
	if strings.Contains(msg, "invalid character") {
		t.Errorf("error %q still reads as a JSON parse failure", msg)
	}
	if !errors.Is(err, ErrNotJSON) {
		t.Errorf("errors.Is(err, ErrNotJSON) = false")
	}
	if errors.Is(err, ErrNotYetAvailable) {
		t.Errorf("an HTML page must not read as not-yet-available: %v", err)
	}
}

// GitHub #57: the reporter's PID resolves through iBL to the version id
// mediaselector accepts.
func TestResolve_GH57_IssuePIDResolvesViaIBL(t *testing.T) {
	srv := newIBLEpisodeServer(t, map[string]string{"m0033jgh": "episode_m0033jgh.json"})
	r := newFixtureResolver(srv.URL, fixtureNow)

	info, err := r.ResolveCtx(context.Background(), "m0033jgh")
	if err != nil {
		t.Fatalf("ResolveCtx: %v", err)
	}
	if info.VPID != "m0033jgj" {
		t.Errorf("VPID = %q, want m0033jgj", info.VPID)
	}
	if info.Duration != 184 {
		t.Errorf("Duration = %d, want 184", info.Duration)
	}
	if info.Title != "UEFA Champions League: Club Brugge v RC Lens Highlights" {
		t.Errorf("Title = %q", info.Title)
	}
	if info.Type != "tv" {
		t.Errorf("Type = %q, want tv", info.Type)
	}
	if info.Thumbnail != "https://ichef.bbci.co.uk/images/ic/960x540/p0phxgld.jpg" {
		t.Errorf("Thumbnail = %q", info.Thumbnail)
	}
	if info.AccessibilityOnly {
		t.Error("AccessibilityOnly = true for an original version")
	}
	if n := srv.requestCount(); n != 1 {
		t.Fatalf("requests = %d, want 1", n)
	}
	req := srv.requests[0]
	if req.URL.Path != "/episodes/m0033jgh" || req.URL.RawQuery != "" {
		t.Errorf("request = %s?%s, want /episodes/m0033jgh with no query", req.URL.Path, req.URL.RawQuery)
	}
}

func TestResolve_VersionSelection(t *testing.T) {
	cases := []struct {
		pid, file         string
		now               time.Time
		vpid              string
		duration          int
		title             string
		accessibilityOnly bool
		versionTypes      []string
	}{
		{pid: "m00336jr", file: "episode_m00336jr.json", vpid: "m00336jq", duration: 4164},
		// audio-described listed second; the original wins
		{pid: "m00336w8", file: "episode_m00336w8.json", vpid: "m00336w7", duration: 1704},
		{pid: "b00tn579", file: "episode_b00tn579.json", vpid: "b00tmlvh", duration: 3524,
			title: "Waterloo Road: Series 6: Episode 2", versionTypes: []string{"original", "audiodescribed", "signed"}},
		// no "original" at all: editorial-only film
		{pid: "b0074t6w", file: "episode_b0074t6w.json", vpid: "b0bjd402", duration: 4949, title: "The 39 Steps"},
		// "shortened" listed first
		{pid: "m001wsyw", file: "episode_m001wsyw.json", vpid: "m0028vk7", duration: 2626},
		// film without a subtitle: no trailing ": "
		{pid: "b007xh8v", file: "episode_b007xh8v.json", vpid: "b007xh8n", duration: 5430, title: "A Cock and Bull Story"},
		// editorial (from 11:45Z) listed before original (from 21:45Z):
		// iBL order is what iPlayer plays, before and after the original starts
		{pid: "m0033d93", file: "episode_m0033d93.json", now: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC), vpid: "m0033dbj", duration: 2873},
		{pid: "m0033d93", file: "episode_m0033d93.json", vpid: "m0033dbj", duration: 2873},
		// a signed-only programme is downloadable as signed
		{pid: "m0032bdk", file: "episode_m0032bdk.json", vpid: "m0032bdj", duration: 9311, accessibilityOnly: true},
		// audio-described only
		{pid: "m002pppv", file: "episode_m002pppv.json", vpid: "p0mqjkqs", duration: 4438, accessibilityOnly: true},
	}
	for _, tc := range cases {
		t.Run(tc.pid+"_"+tc.vpid, func(t *testing.T) {
			now := tc.now
			if now.IsZero() {
				now = fixtureNow
			}
			srv := newIBLEpisodeServer(t, map[string]string{tc.pid: tc.file})
			info, err := newFixtureResolver(srv.URL, now).ResolveCtx(context.Background(), tc.pid)
			if err != nil {
				t.Fatalf("ResolveCtx: %v", err)
			}
			if info.VPID != tc.vpid {
				t.Errorf("VPID = %q, want %q", info.VPID, tc.vpid)
			}
			if info.Duration != tc.duration {
				t.Errorf("Duration = %d, want %d", info.Duration, tc.duration)
			}
			if tc.title != "" && info.Title != tc.title {
				t.Errorf("Title = %q, want %q", info.Title, tc.title)
			}
			if info.AccessibilityOnly != tc.accessibilityOnly {
				t.Errorf("AccessibilityOnly = %v, want %v", info.AccessibilityOnly, tc.accessibilityOnly)
			}
			if tc.versionTypes != nil {
				var got []string
				for _, v := range info.Versions {
					got = append(got, v.Type)
					if v.PID == "" || v.VPID != v.PID {
						t.Errorf("version %+v: want PID == VPID, non-empty", v)
					}
				}
				if strings.Join(got, ",") != strings.Join(tc.versionTypes, ",") {
					t.Errorf("Versions types = %v, want %v", got, tc.versionTypes)
				}
			}
		})
	}
}

// The version rule against orders no capture happens to have: an
// audio-described version listed before the original, and an original
// that has not started listed before an editorial version that has.
func TestResolve_VersionSelection_Reordered(t *testing.T) {
	cases := []struct {
		name, pid, body string
		now             time.Time
		vpid            string
	}{
		{name: "audio-described first", pid: "m00336w8",
			body: reorderedVersions(t, "episode_m00336w8.json", 1, 0), vpid: "m00336w7"},
		{name: "unstarted original first", pid: "m0033d93",
			body: reorderedVersions(t, "episode_m0033d93.json", 1, 0),
			now:  time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC), vpid: "m0033dbj"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := tc.now
			if now.IsZero() {
				now = fixtureNow
			}
			srv := newIBLBodyServer(t, tc.pid, tc.body)
			info, err := newFixtureResolver(srv.URL, now).ResolveCtx(context.Background(), tc.pid)
			if err != nil {
				t.Fatalf("ResolveCtx: %v", err)
			}
			if info.VPID != tc.vpid || info.AccessibilityOnly {
				t.Errorf("VPID = %q, AccessibilityOnly = %v; want %q, false", info.VPID, info.AccessibilityOnly, tc.vpid)
			}
		})
	}
}

func TestResolve_NotYetAvailableShapes(t *testing.T) {
	cases := []struct {
		name, pid, file string
		now             time.Time
		wantInMsg       []string
	}{
		{name: "live simulcast", pid: "m00334lv", file: "episode_m00334lv.json", wantInMsg: []string{"m00334lv", "live"}},
		// a 24-hour pop-up channel whose only version is a webcast
		{name: "live webcast", pid: "p0p77xg7", file: "episode_p0p77xg7.json", wantInMsg: []string{"p0p77xg7", "live"}},
		{name: "not listed", pid: "zzzzzzzz", file: "episode_empty.json", wantInMsg: []string{"zzzzzzzz", "not listed on BBC iPlayer"}},
		// both versions start after now
		{name: "future start", pid: "m0033d93", file: "episode_m0033d93.json",
			now: time.Date(2026, 10, 21, 11, 0, 0, 0, time.UTC), wantInMsg: []string{"available on iPlayer from 2026-10-21T11:45:00Z"}},
		// the signed version has started but the original has not: wait for
		// the original rather than download the signed one
		{name: "original not started, signed has", pid: "b00tn579", file: "episode_b00tn579.json",
			now: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), wantInMsg: []string{"available on iPlayer from 2025-09-19T04:45:00Z"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := tc.now
			if now.IsZero() {
				now = fixtureNow
			}
			srv := newIBLEpisodeServer(t, map[string]string{tc.pid: tc.file})
			info, err := newFixtureResolver(srv.URL, now).ResolveCtx(context.Background(), tc.pid)
			if !errors.Is(err, ErrNotYetAvailable) {
				t.Fatalf("errors.Is(err, ErrNotYetAvailable) = false, info = %+v, err = %v", info, err)
			}
			if info != nil {
				t.Errorf("info = %+v, want nil", info)
			}
			for _, want := range tc.wantInMsg {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

// Shapes that say nothing about availability must not read as
// not-yet-available: that would quietly hide the item from the feed.
func TestResolve_RejectsUnexpectedShapes(t *testing.T) {
	t.Run("no episodes key", func(t *testing.T) {
		srv := newIBLEpisodeServer(t, map[string]string{"b007t9y1": "programmes_b007t9y1.json"})
		_, err := newFixtureResolver(srv.URL, fixtureNow).ResolveCtx(context.Background(), "b007t9y1")
		if err == nil || errors.Is(err, ErrNotYetAvailable) {
			t.Fatalf("err = %v, want a non-NYA error", err)
		}
	})
	for name, body := range map[string]string{
		"no versions key":    `{"episodes":[{"id":"m0033jgh","live":false,"title":"T"}]}`,
		"null versions":      `{"episodes":[{"id":"m0033jgh","live":false,"title":"T","versions":null}]}`,
		"version without id": `{"episodes":[{"id":"m0033jgh","live":false,"title":"T","versions":[{"kind":"original","duration":{"value":"PT3M"}}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := newIBLBodyServer(t, "m0033jgh", body)
			info, err := newFixtureResolver(srv.URL, fixtureNow).ResolveCtx(context.Background(), "m0033jgh")
			if err == nil || errors.Is(err, ErrNotYetAvailable) {
				t.Fatalf("info = %+v, err = %v; want a non-NYA error", info, err)
			}
		})
	}
	t.Run("different episode returned", func(t *testing.T) {
		srv := newIBLEpisodeServer(t, map[string]string{"m0033jgh": "episode_b00tn579.json"})
		_, err := newFixtureResolver(srv.URL, fixtureNow).ResolveCtx(context.Background(), "m0033jgh")
		if err == nil || errors.Is(err, ErrNotYetAvailable) {
			t.Fatalf("err = %v, want a non-NYA error", err)
		}
	})
	// iBL batches comma-separated PIDs, so a crafted PID could resolve a
	// different episode; uppercase is never a BBC PID.
	for _, pid := range []string{"b00tn579,b00v73q9", "B00TN579", "../x", ""} {
		t.Run("invalid pid "+pid, func(t *testing.T) {
			srv := newIBLEpisodeServer(t, map[string]string{"b00tn579": "episode_b00tn579.json"})
			_, err := newFixtureResolver(srv.URL, fixtureNow).ResolveCtx(context.Background(), pid)
			if err == nil || errors.Is(err, ErrNotYetAvailable) {
				t.Fatalf("err = %v, want a non-NYA error", err)
			}
			if n := srv.requestCount(); n != 0 {
				t.Errorf("requests = %d, want 0 for an invalid PID", n)
			}
		})
	}
}

func TestResolve_DefaultsToIBL(t *testing.T) {
	if got := NewPlaylistResolver(NewClient()).BaseURL; got != defaultIBLBase {
		t.Errorf("BaseURL = %q, want %q", got, defaultIBLBase)
	}
}

func TestNormaliseVersionType(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Original", "original"},
		{"Audio Described", "audiodescribed"},
		{"Audio Description", "audiodescribed"},
		{"Signed", "signed"},
		{"Signed and Audio Described", "combined"},
		{"Open Subtitles", "opensubtitles"},
		{"Dubbbed", "dubbbed"},
		{"", "unknown"},
		{"  ", "unknown"},
		// iBL version kinds
		{"original", "original"},
		{"audio-described", "audiodescribed"},
		{"signed", "signed"},
		{"simulcast", "simulcast"},
		{"webcast", "webcast"},
		{"editorial", "editorial"},
		{"shortened", "shortened"},
		{"iplayer-version", "iplayer-version"},
	}
	for _, tc := range cases {
		if got := normaliseVersionType(tc.in); got != tc.want {
			t.Errorf("normaliseVersionType(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
