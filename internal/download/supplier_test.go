package download

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// issue55Streams is the stream list from GitHub #55: one height from three
// suppliers, HLS first, then the same three as DASH.
func issue55Streams() []bbc.VideoStream {
	var out []bbc.VideoStream
	for _, format := range []string{"hls", "dash"} {
		for _, sup := range []string{"mf_akamai", "mf_cloudfront", "mf_bidi"} {
			out = append(out, bbc.VideoStream{
				URL:      strings.TrimPrefix(sup, "mf_") + "-" + format,
				Height:   1080,
				Width:    1920,
				Bitrate:  8490,
				Format:   format,
				Supplier: sup,
			})
		}
	}
	return out
}

func TestPickStream_ExcludesFailedSuppliers(t *testing.T) {
	streams := issue55Streams()
	cases := []struct {
		excluded []string
		want     string
	}{
		{nil, "akamai-hls"},
		{[]string{"mf_akamai"}, "cloudfront-hls"},
		{[]string{"mf_bidi"}, "akamai-hls"},
		{[]string{"mf_akamai", "mf_cloudfront"}, "bidi-hls"},
		{[]string{"mf_cloudfront", "mf_akamai"}, "bidi-hls"},
	}
	for _, q := range []string{"1080p", "720p", "396p"} {
		for _, c := range cases {
			if got := pickStream(streams, q, c.excluded...); got.URL != c.want {
				t.Errorf("pickStream(%q, excluded=%v) = %s, want %s", q, c.excluded, got.URL, c.want)
			}
		}
	}
}

// When every candidate's supplier has refused, the exclusion is ignored and
// the pick is today's: the first HLS entry.
func TestPickStream_AllCandidatesExcludedFallsBack(t *testing.T) {
	streams := issue55Streams()
	for _, q := range []string{"1080p", "720p"} {
		got := pickStream(streams, q, "mf_akamai", "mf_cloudfront", "mf_bidi")
		if got.URL != "akamai-hls" {
			t.Errorf("pickStream(%q, all excluded) = %s, want akamai-hls", q, got.URL)
		}
	}
}

// The exclusion only chooses between candidates at the best height
// distance; it never trades height for a different supplier.
func TestPickStream_ExclusionNeverCostsHeight(t *testing.T) {
	streams := []bbc.VideoStream{
		{URL: "bidi-1080", Height: 1080, Format: "hls", Supplier: "mf_bidi"},
		{URL: "akamai-720", Height: 720, Format: "hls", Supplier: "mf_akamai"},
	}
	if got := pickStream(streams, "1080p", "mf_bidi"); got.URL != "bidi-1080" {
		t.Errorf("pickStream(1080p, excluded mf_bidi) = %s, want bidi-1080", got.URL)
	}
	if got := pickStream(streams, "720p", "mf_akamai"); got.URL != "akamai-720" {
		t.Errorf("pickStream(720p, excluded mf_akamai) = %s, want akamai-720", got.URL)
	}
}

// A supplier that refused is avoided whatever the format: another
// supplier's DASH entry beats the refusing supplier's HLS entry.
func TestPickStream_AllowedDASHBeatsExcludedHLS(t *testing.T) {
	streams := []bbc.VideoStream{
		{URL: "bidi-hls", Height: 1080, Format: "hls", Supplier: "mf_bidi"},
		{URL: "akamai-dash", Height: 1080, Format: "dash", Supplier: "mf_akamai"},
	}
	if got := pickStream(streams, "720p", "mf_bidi"); got.URL != "akamai-dash" {
		t.Errorf("pickStream(720p, excluded mf_bidi) = %s, want akamai-dash", got.URL)
	}
}

// pickStreamPR56 is pickStream exactly as PR #56 (007e0ab) left it. With
// no exclusions the current pickStream must choose the same stream for
// every input.
func pickStreamPR56(streams []bbc.VideoStream, quality string) bbc.VideoStream {
	targetHeight := qualityToHeight(quality)
	for _, s := range streams {
		if s.Height == targetHeight && s.Format == "hls" {
			return s
		}
	}
	for _, s := range streams {
		if s.Height == targetHeight {
			return s
		}
	}
	best := streams[0]
	bestDiff := abs(best.Height - targetHeight)
	for _, s := range streams[1:] {
		diff := abs(s.Height - targetHeight)
		if diff < bestDiff || (diff == bestDiff && s.Format == "hls" && best.Format != "hls") {
			best = s
			bestDiff = diff
		}
	}
	return best
}

func TestPickStream_NoExclusionMatchesPR56(t *testing.T) {
	heights := []int{1080, 720, 540}
	formats := []string{"hls", "dash"}
	options := len(heights) * len(formats)
	qualities := []string{"2160p", "1080p", "720p", "630p", "540p", "396p"}

	checked := 0
	for n := 1; n <= 5; n++ {
		total := 1
		for i := 0; i < n; i++ {
			total *= options
		}
		for combo := 0; combo < total; combo++ {
			streams := make([]bbc.VideoStream, n)
			c := combo
			for i := range streams {
				o := c % options
				c /= options
				streams[i] = bbc.VideoStream{
					URL:      fmt.Sprintf("s%d", i),
					Height:   heights[o/len(formats)],
					Format:   formats[o%len(formats)],
					Supplier: fmt.Sprintf("sup%d", i%2),
				}
			}
			for _, q := range qualities {
				want := pickStreamPR56(streams, q)
				if got := pickStream(streams, q); got.URL != want.URL {
					t.Fatalf("pickStream(%v, %q) = %s, PR #56 picks %s", streams, q, got.URL, want.URL)
				}
				checked++
			}
		}
	}
	if checked == 0 {
		t.Fatal("no cases checked")
	}
}

func TestSupplierRefusal(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantReason string
		wantOK     bool
	}{
		{
			name:       "ffmpeg 403 on open",
			err:        errors.New("ffmpeg: exit status 8 | stderr: [in#0 @ 0x55d1] Error opening input: Server returned 403 Forbidden (access denied) | Error opening input file https://mm.bidi.example/master.m3u8. | Error opening input files: Server returned 403 Forbidden (access denied)"),
			wantReason: "HTTP 403",
			wantOK:     true,
		},
		{
			name:       "ffmpeg 401",
			err:        errors.New("ffmpeg: exit status 8 | stderr: https://cdn.example/a.m3u8: Server returned 401 Unauthorized (authorization failed)"),
			wantReason: "HTTP 401",
			wantOK:     true,
		},
		{
			name:       "ffmpeg 404",
			err:        errors.New("ffmpeg: exit status 8 | stderr: Error opening input files: Server returned 404 Not Found"),
			wantReason: "HTTP 404",
			wantOK:     true,
		},
		{
			name:       "ffmpeg 410 has no name of its own",
			err:        errors.New("ffmpeg: exit status 8 | stderr: Error opening input files: Server returned 4XX Client Error, but not one of 40{0,1,3,4}"),
			wantReason: "HTTP 4xx",
			wantOK:     true,
		},
		{
			// Any 4xx counts, not only the four codes ffmpeg names.
			name:       "ffmpeg 429",
			err:        errors.New("ffmpeg: exit status 8 | stderr: Error opening input files: Server returned 429 Too Many Requests"),
			wantReason: "HTTP 429",
			wantOK:     true,
		},
		{
			name:       "http protocol warning line",
			err:        errors.New("ffmpeg: exit status 251 | stderr: [hls @ 0x55] HTTP error 403 Forbidden"),
			wantReason: "HTTP 403",
			wantOK:     true,
		},
		{
			name:       "connection refused",
			err:        errors.New("ffmpeg: exit status 8 | stderr: [tcp @ 0x55] Connection to tcp://cdn.example:443 failed: Connection refused | Error opening input files: Connection refused"),
			wantReason: "connection refused",
			wantOK:     true,
		},
		{
			name:       "DNS failure",
			err:        errors.New("ffmpeg: exit status 8 | stderr: [tcp @ 0x55] Failed to resolve hostname mm.bidi.example: Name or service not known | Error opening input files: Input/output error"),
			wantReason: "DNS failure",
			wantOK:     true,
		},
		{
			name:       "master playlist 403",
			err:        &MasterPlaylistError{Host: "mm.bidi.example", Status: 403},
			wantReason: "HTTP 403",
			wantOK:     true,
		},
		{
			name:       "master playlist 503",
			err:        &MasterPlaylistError{Host: "mm.bidi.example", Status: 503},
			wantReason: "HTTP 503",
			wantOK:     true,
		},
		{
			name:       "master playlist transport failure",
			err:        &MasterPlaylistError{Host: "mm.bidi.example", Err: errors.New("dial tcp: i/o timeout")},
			wantReason: "unreachable",
			wantOK:     true,
		},
		{
			name:   "ffmpeg 5xx is not a refusal",
			err:    errors.New("ffmpeg: exit status 8 | stderr: Error opening input files: Server returned 5XX Server Error reply"),
			wantOK: false,
		},
		{
			name:   "invalid data",
			err:    errors.New("ffmpeg: exit status 1 | stderr: Invalid data found when processing input"),
			wantOK: false,
		},
		{
			name:   "TLS end of file",
			err:    errors.New("ffmpeg: exit status 1 | stderr: [tls @ 0x55] IO error: End of file"),
			wantOK: false,
		},
		{
			name:   "bare exit status",
			err:    errors.New("ffmpeg: exit status 251"),
			wantOK: false,
		},
		{
			name:   "watchdog stall with a 403 in the tail",
			err:    fmt.Errorf("%w (ffmpeg: signal: terminated | stderr: Server returned 403 Forbidden (access denied))", ErrStalled),
			wantOK: false,
		},
		{
			name:   "nil",
			err:    nil,
			wantOK: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reason, ok := supplierRefusal(c.err)
			if ok != c.wantOK || reason != c.wantReason {
				t.Errorf("supplierRefusal() = (%q, %v), want (%q, %v)", reason, ok, c.wantReason, c.wantOK)
			}
		})
	}
}

// A refusal is recorded against the supplier and keeps the existing
// failure code and retry budget, but must not count toward quality
// degradation.
func TestFailDownload_SupplierRefusalLeavesCDNFailures(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	m := NewManager(st, filepath.Join(dir, "downloads"), 1, nil, nil, nil, nil)

	dl := &store.Download{ID: "r1", PID: "p1", Status: store.StatusPending, Quality: "720p"}
	st.PutDownload(dl)

	refusal := func() error {
		return &supplierRefusedError{Supplier: "mf_bidi", Reason: "HTTP 403", Err: errors.New("ffmpeg: exit status 8")}
	}
	m.failDownload(dl, store.FailCodeFFmpeg, refusal())

	if dl.CDNFailures != 0 {
		t.Errorf("CDNFailures = %d after a refusal, want 0", dl.CDNFailures)
	}
	if dl.FailureCode != store.FailCodeFFmpeg {
		t.Errorf("FailureCode = %q, want %q", dl.FailureCode, store.FailCodeFFmpeg)
	}
	if dl.RetryCount != 1 || !dl.Retryable {
		t.Errorf("RetryCount = %d, Retryable = %v; want 1, true", dl.RetryCount, dl.Retryable)
	}
	if !reflect.DeepEqual(dl.FailedSuppliers, []string{"mf_bidi"}) {
		t.Errorf("FailedSuppliers = %v, want [mf_bidi]", dl.FailedSuppliers)
	}
	if shouldDegrade(dl) {
		t.Error("shouldDegrade = true after a refusal, want false")
	}
	if !strings.Contains(dl.Error, "mf_bidi") || !strings.Contains(dl.Error, "HTTP 403") {
		t.Errorf("Error = %q, want it to name the supplier and the reason", dl.Error)
	}

	// The same supplier refusing again is not listed twice, and the
	// shared retry budget still runs out on the third failure.
	m.failDownload(dl, store.FailCodeFFmpeg, refusal())
	m.failDownload(dl, store.FailCodeFFmpeg, refusal())
	if !reflect.DeepEqual(dl.FailedSuppliers, []string{"mf_bidi"}) {
		t.Errorf("FailedSuppliers = %v after repeat refusals, want [mf_bidi]", dl.FailedSuppliers)
	}
	if dl.CDNFailures != 0 {
		t.Errorf("CDNFailures = %d after repeat refusals, want 0", dl.CDNFailures)
	}
	if dl.Retryable {
		t.Error("third failure should exhaust maxRetries, Retryable = true")
	}
}

// newSupplierHarness builds a Manager whose playlist and media-selector
// resolvers are local mocks, serving mediaXML as the media-selector reply.
// The caller overrides m.runFFmpeg to control the ffmpeg outcome.
func newSupplierHarness(t *testing.T, mediaXML string) (*Manager, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	playlistServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"defaultAvailableVersion": map[string]interface{}{
				"smpConfig": map[string]interface{}{
					"title": "Supplier Test Programme",
					"items": []map[string]interface{}{
						{"kind": "programme", "duration": 1800, "vpid": "p_sup_vpid"},
					},
				},
			},
			"allAvailableVersions": []interface{}{},
		})
	}))
	t.Cleanup(playlistServer.Close)

	mediaSelectorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, mediaXML)
	}))
	t.Cleanup(mediaSelectorServer.Close)

	bbcClient := bbc.NewClient()
	playlist := bbc.NewPlaylistResolver(bbcClient)
	playlist.BaseURL = playlistServer.URL
	ms := bbc.NewMediaSelector(bbcClient)
	ms.BaseURL = mediaSelectorServer.URL

	return NewManager(st, filepath.Join(dir, "downloads"), 1, bbcClient, playlist, ms, nil), st
}

// mediaXML builds a media-selector reply with one 1080p media element
// listing an HLS connection per supplier, in order.
func mediaXML(suppliers ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><mediaSelection>`)
	b.WriteString(`<media kind="video" type="video/mp4" encoding="h264" bitrate="8490" width="1920" height="1080">`)
	for _, s := range suppliers {
		fmt.Fprintf(&b, `<connection supplier="%s" transferFormat="hls" protocol="https" href="https://%s.invalid/master.m3u8"/>`, s, s)
	}
	b.WriteString(`</media></mediaSelection>`)
	return b.String()
}

// fakeFFmpeg records every job and returns the queued errors in turn.
type fakeFFmpeg struct {
	jobs []FFmpegJob
	errs []error
}

func (f *fakeFFmpeg) run(ctx context.Context, job FFmpegJob) error {
	f.jobs = append(f.jobs, job)
	if len(f.jobs) > len(f.errs) {
		return fmt.Errorf("unexpected ffmpeg run %d", len(f.jobs))
	}
	return f.errs[len(f.jobs)-1]
}

func enqueueAndGet(t *testing.T, m *Manager, st *store.Store, pid, quality string) *store.Download {
	t.Helper()
	id, err := m.Enqueue(pid, quality, "Supplier.Test.S01E01."+quality+".WEB-DL.AAC.H264-iParr", "sonarr")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	dl, err := st.GetDownload(id)
	if err != nil || dl == nil {
		t.Fatalf("GetDownload(%q) = %v, %v", id, dl, err)
	}
	return dl
}

// GitHub #55 end to end: the first supplier refuses (ffmpeg 403), so the
// retry goes to the next supplier at the SAME quality, not a degraded one.
func TestProcessDownload_RefusedSupplierExcludedOnRetry(t *testing.T) {
	refusals := map[string]error{
		"ffmpeg 403": errors.New("ffmpeg: exit status 8 | stderr: Error opening input files: Server returned 403 Forbidden (access denied)"),
		"master 403": &MasterPlaylistError{Host: "mf_akamai.invalid", Status: http.StatusForbidden},
	}
	for name, refusal := range refusals {
		t.Run(name, func(t *testing.T) {
			m, st := newSupplierHarness(t, mediaXML("mf_akamai", "mf_cloudfront", "mf_bidi"))
			ff := &fakeFFmpeg{errs: []error{refusal, errors.New("ffmpeg: exit status 1")}}
			m.runFFmpeg = ff.run

			dl := enqueueAndGet(t, m, st, "b055sup1", "720p")
			m.processDownload(context.Background(), dl)

			if dl.FailureCode != store.FailCodeFFmpeg {
				t.Errorf("FailureCode = %q, want %q", dl.FailureCode, store.FailCodeFFmpeg)
			}
			if dl.CDNFailures != 0 {
				t.Errorf("CDNFailures = %d after a refusal, want 0", dl.CDNFailures)
			}
			if !dl.Retryable {
				t.Fatal("Retryable = false after the first failure, want true")
			}
			stored, err := st.GetDownload(dl.ID)
			if err != nil || stored == nil {
				t.Fatalf("GetDownload: %v, %v", stored, err)
			}
			if stored.Supplier != "mf_akamai" {
				t.Errorf("stored Supplier = %q, want mf_akamai", stored.Supplier)
			}
			if !reflect.DeepEqual(stored.FailedSuppliers, []string{"mf_akamai"}) {
				t.Errorf("stored FailedSuppliers = %v, want [mf_akamai]", stored.FailedSuppliers)
			}

			m.processDownload(context.Background(), dl)

			if len(ff.jobs) != 2 {
				t.Fatalf("ffmpeg ran %d times, want 2", len(ff.jobs))
			}
			if !strings.Contains(ff.jobs[0].StreamURL, "mf_akamai") {
				t.Errorf("first attempt URL = %s, want mf_akamai", ff.jobs[0].StreamURL)
			}
			if !strings.Contains(ff.jobs[1].StreamURL, "mf_cloudfront") {
				t.Errorf("retry URL = %s, want mf_cloudfront (next supplier)", ff.jobs[1].StreamURL)
			}
			if ff.jobs[1].TargetHeight != 720 {
				t.Errorf("retry TargetHeight = %d, want 720 (a refusal must not degrade)", ff.jobs[1].TargetHeight)
			}
			if dl.Supplier != "mf_cloudfront" {
				t.Errorf("Supplier = %q after the retry, want mf_cloudfront", dl.Supplier)
			}
		})
	}
}

// Any other ffmpeg failure keeps today's behaviour: the CDN-failure count
// goes up, the retry degrades quality, and no supplier is excluded.
func TestProcessDownload_NonRefusalFailureStillDegrades(t *testing.T) {
	m, st := newSupplierHarness(t, mediaXML("mf_akamai", "mf_cloudfront", "mf_bidi"))
	ff := &fakeFFmpeg{errs: []error{
		errors.New("ffmpeg: exit status 1 | stderr: Invalid data found when processing input"),
		errors.New("ffmpeg: exit status 1"),
	}}
	m.runFFmpeg = ff.run

	dl := enqueueAndGet(t, m, st, "b055sup2", "720p")
	m.processDownload(context.Background(), dl)

	if dl.CDNFailures != 1 {
		t.Errorf("CDNFailures = %d, want 1", dl.CDNFailures)
	}
	if len(dl.FailedSuppliers) != 0 {
		t.Errorf("FailedSuppliers = %v, want none", dl.FailedSuppliers)
	}

	m.processDownload(context.Background(), dl)

	if len(ff.jobs) != 2 {
		t.Fatalf("ffmpeg ran %d times, want 2", len(ff.jobs))
	}
	if ff.jobs[0].TargetHeight != 720 || ff.jobs[1].TargetHeight != 540 {
		t.Errorf("TargetHeight = %d then %d, want 720 then 540", ff.jobs[0].TargetHeight, ff.jobs[1].TargetHeight)
	}
	if !strings.Contains(ff.jobs[1].StreamURL, "mf_akamai") {
		t.Errorf("retry URL = %s, want mf_akamai (no supplier excluded)", ff.jobs[1].StreamURL)
	}
	if dl.CDNFailures != 2 {
		t.Errorf("CDNFailures = %d after the second failure, want 2", dl.CDNFailures)
	}
}

// Once every supplier has refused, the next attempt ignores the exclusion
// and goes back to the first HLS entry. The retry budget is unchanged, and
// the failed suppliers survive the move to history.
func TestProcessDownload_AllSuppliersRefusedFallsBack(t *testing.T) {
	m, st := newSupplierHarness(t, mediaXML("mf_akamai", "mf_bidi"))
	refusal := errors.New("ffmpeg: exit status 8 | stderr: Error opening input files: Server returned 403 Forbidden (access denied)")
	ff := &fakeFFmpeg{errs: []error{refusal, refusal, refusal}}
	m.runFFmpeg = ff.run

	dl := enqueueAndGet(t, m, st, "b055sup3", "1080p")
	for i := 0; i < 3; i++ {
		m.processDownload(context.Background(), dl)
	}

	if len(ff.jobs) != 3 {
		t.Fatalf("ffmpeg ran %d times, want 3", len(ff.jobs))
	}
	for i, want := range []string{"mf_akamai", "mf_bidi", "mf_akamai"} {
		if !strings.Contains(ff.jobs[i].StreamURL, want) {
			t.Errorf("attempt %d URL = %s, want %s", i+1, ff.jobs[i].StreamURL, want)
		}
		if ff.jobs[i].TargetHeight != 1080 {
			t.Errorf("attempt %d TargetHeight = %d, want 1080", i+1, ff.jobs[i].TargetHeight)
		}
	}
	if dl.Retryable {
		t.Error("Retryable = true after three failures, want false (maxRetries unchanged)")
	}
	hist, err := st.GetHistory(dl.ID)
	if err != nil || hist == nil {
		t.Fatalf("GetHistory: %v, %v", hist, err)
	}
	if !reflect.DeepEqual(hist.FailedSuppliers, []string{"mf_akamai", "mf_bidi"}) {
		t.Errorf("history FailedSuppliers = %v, want [mf_akamai mf_bidi]", hist.FailedSuppliers)
	}
	if hist.Supplier != "mf_akamai" {
		t.Errorf("history Supplier = %q, want mf_akamai", hist.Supplier)
	}
	if hist.CDNFailures != 0 {
		t.Errorf("history CDNFailures = %d, want 0", hist.CDNFailures)
	}
}

// writeFakeFFprobe puts an ffprobe on PATH that reports a 720-line video
// stream, so a completed attempt does not depend on a real ffprobe.
func writeFakeFFprobe(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ffprobe"), []byte("#!/bin/sh\necho 720\n"), 0o755); err != nil {
		t.Fatalf("write fake ffprobe: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// succeedWithOutput wraps ff so that a nil error also leaves a (sparse)
// output file large enough to clear the truncation gate.
func succeedWithOutput(ff *fakeFFmpeg) func(context.Context, FFmpegJob) error {
	return func(ctx context.Context, job FFmpegJob) error {
		if err := ff.run(ctx, job); err != nil {
			return err
		}
		f, err := os.Create(job.OutputPath)
		if err != nil {
			return err
		}
		defer f.Close()
		return f.Truncate(200 << 20)
	}
}

// A download that succeeds on a retry must not carry the earlier attempt's
// failure into history: no failure code, error, retry flag or retry time.
// The counters and the supplier record stay. GitHub #55 follow-up.
func TestProcessDownload_RetrySuccessClearsFailureState(t *testing.T) {
	cases := []struct {
		name            string
		firstErr        error
		wantCDNFailures int
		wantFailed      []string
		wantSupplier    string
	}{
		{
			name:            "after a supplier refusal",
			firstErr:        errors.New("ffmpeg: exit status 8 | stderr: Error opening input files: Server returned 403 Forbidden (access denied)"),
			wantCDNFailures: 0,
			wantFailed:      []string{"mf_akamai"},
			wantSupplier:    "mf_cloudfront",
		},
		{
			name:            "after a CDN failure",
			firstErr:        errors.New("ffmpeg: exit status 1 | stderr: Invalid data found when processing input"),
			wantCDNFailures: 1,
			wantFailed:      nil,
			wantSupplier:    "mf_akamai",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeFakeFFprobe(t)
			m, st := newSupplierHarness(t, mediaXML("mf_akamai", "mf_cloudfront"))
			ff := &fakeFFmpeg{errs: []error{tc.firstErr, nil}}
			m.runFFmpeg = succeedWithOutput(ff)

			dl := enqueueAndGet(t, m, st, "b055ok", "720p")
			m.processDownload(context.Background(), dl)
			if dl.Status != store.StatusFailed || !dl.Retryable || dl.RetryAfter.IsZero() || dl.FailureCode == "" {
				t.Fatalf("first attempt: Status=%q Retryable=%v RetryAfter=%v FailureCode=%q, want a retryable failure",
					dl.Status, dl.Retryable, dl.RetryAfter, dl.FailureCode)
			}

			m.processDownload(context.Background(), dl)

			if len(ff.jobs) != 2 {
				t.Fatalf("ffmpeg ran %d times, want 2", len(ff.jobs))
			}
			hist, err := st.GetHistory(dl.ID)
			if err != nil || hist == nil {
				t.Fatalf("GetHistory: %v, %v", hist, err)
			}
			if hist.Status != store.StatusCompleted {
				t.Fatalf("history Status = %q, want %q", hist.Status, store.StatusCompleted)
			}
			if hist.FailureCode != "" {
				t.Errorf("history FailureCode = %q, want empty", hist.FailureCode)
			}
			if hist.Error != "" {
				t.Errorf("history Error = %q, want empty", hist.Error)
			}
			if hist.Retryable {
				t.Error("history Retryable = true, want false")
			}
			if !hist.RetryAfter.IsZero() {
				t.Errorf("history RetryAfter = %v, want zero", hist.RetryAfter)
			}
			if hist.RetryCount != 1 {
				t.Errorf("history RetryCount = %d, want 1 (kept as a record)", hist.RetryCount)
			}
			if hist.CDNFailures != tc.wantCDNFailures {
				t.Errorf("history CDNFailures = %d, want %d (kept as a record)", hist.CDNFailures, tc.wantCDNFailures)
			}
			if !reflect.DeepEqual(hist.FailedSuppliers, tc.wantFailed) {
				t.Errorf("history FailedSuppliers = %v, want %v (kept as a record)", hist.FailedSuppliers, tc.wantFailed)
			}
			if hist.Supplier != tc.wantSupplier {
				t.Errorf("history Supplier = %q, want %q", hist.Supplier, tc.wantSupplier)
			}
		})
	}
}
