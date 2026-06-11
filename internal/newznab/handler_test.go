package newznab

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// fakeBBCServer returns an httptest.Server that responds to BBC iBL Search
// (/new-search) with the supplied JSON. Used by handleTVSearch tests.
func fakeBBCSearchServer(t *testing.T, payload string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fakeSkyhookServer returns an httptest.Server that responds to any
// path with the supplied JSON payload, simulating Skyhook's
// /v1/tvdb/shows/en/<tvdbid> endpoint. Used by tests that need to
// exercise the lookupTVDBShow path (cold-cache lookup).
func fakeSkyhookServer(t *testing.T, payload string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// doctorWhoThreeBrandsPayload is a synthetic IBL search response with
// three episode-type results, one per Doctor Who brand. Episode-type
// results bypass ListEpisodes brand expansion (which the existing
// fakeBBCSearchServer cannot mock), so the disambiguation logic
// receives the three programmes directly via iblResultToProgramme.
//
// Subtitles use "Series 1: Episode 2" so parseSubtitleNumbers can
// extract Series=1, Episode=2 (the colon-and-space split is required
// per internal/bbc/ibl.go).
const doctorWhoThreeBrandsPayload = `{
    "new_search": {
        "results": [
            {"id": "ep_modern", "type": "episode", "title": "Doctor Who", "subtitle": "Series 1: Episode 2", "release_date": "2024-05-18", "parent_position": 2},
            {"id": "ep_classic", "type": "episode", "title": "Doctor Who (1963-1996)", "subtitle": "Series 1: Episode 2", "release_date": "1963-11-30", "parent_position": 2},
            {"id": "ep_legacy", "type": "episode", "title": "Doctor Who (2005-2022)", "subtitle": "Series 1: Episode 2", "release_date": "2005-04-02", "parent_position": 2}
        ]
    }
}`

// newHandlerWithBBC builds a Handler whose IBL is pointed at a fake BBC
// server. Used by handleTVSearch tests.
func newHandlerWithBBC(t *testing.T, payload string) *Handler {
	return newHandlerWithBBCProber(t, payload, nil)
}

func newHandlerWithBBCProber(t *testing.T, payload string, prober qualityProber) *Handler {
	t.Helper()
	srv := fakeBBCSearchServer(t, payload)
	ibl := bbc.NewIBL(bbc.NewClient())
	ibl.BaseURL = srv.URL
	return NewHandler(ibl, nil, nil, prober)
}

// newHandlerWithBBCProberAndStore wires both a prober and a real
// BoltDB store. Used by tests that exercise config-driven behaviour
// (e.g. the configured quality ceiling).
func newHandlerWithBBCProberAndStore(t *testing.T, payload string, prober qualityProber) (*Handler, *store.Store) {
	t.Helper()
	srv := fakeBBCSearchServer(t, payload)
	ibl := bbc.NewIBL(bbc.NewClient())
	ibl.BaseURL = srv.URL

	st, err := store.Open(filepath.Join(t.TempDir(), "newznab-test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	return NewHandler(ibl, st, nil, prober), st
}

// newHandlerWithBBCAndStore is a variant of newHandlerWithBBC that
// also wires a real BoltDB store so tests can pre-populate
// SeriesMapping records for cache-warm-path assertions. Used by the
// Phase 4 warm-cache regression test.
func newHandlerWithBBCAndStore(t *testing.T, payload string) (*Handler, *store.Store) {
	t.Helper()
	srv := fakeBBCSearchServer(t, payload)
	ibl := bbc.NewIBL(bbc.NewClient())
	ibl.BaseURL = srv.URL

	st, err := store.Open(filepath.Join(t.TempDir(), "newznab-test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	return NewHandler(ibl, st, nil, nil), st
}

// mockProber is a test double for the quality prefetcher. It returns a
// fixed map of PID -> heights (or nil for "probe failed") plus an optional
// set of PIDs flagged not-yet-available (Issue #44). Every PrefetchPIDs
// call appends the received probeItems slice to calls so tests can assert
// which PIDs were submitted.
type mockProber struct {
	results         map[string][]int
	notYetAvailable map[string]bool
	calls           [][]bbc.ProbeItem
}

func (m *mockProber) PrefetchPIDs(ctx context.Context, items []bbc.ProbeItem) bbc.PrefetchResult {
	copied := make([]bbc.ProbeItem, len(items))
	copy(copied, items)
	m.calls = append(m.calls, copied)
	out := bbc.PrefetchResult{
		Heights:         make(map[string][]int, len(items)),
		NotYetAvailable: make(map[string]bool),
	}
	for _, it := range items {
		if heights, ok := m.results[it.PID]; ok {
			out.Heights[it.PID] = heights
		} else {
			out.Heights[it.PID] = nil
		}
		if m.notYetAvailable[it.PID] {
			out.NotYetAvailable[it.PID] = true
		}
	}
	return out
}

// hangingProber blocks until ctx fires, then returns an empty result. Used
// by the browse-mode probe-deadline test.
type hangingProber struct{}

func (h *hangingProber) PrefetchPIDs(ctx context.Context, items []bbc.ProbeItem) bbc.PrefetchResult {
	<-ctx.Done()
	return bbc.PrefetchResult{}
}

const eastendersOneEpisodePayload = `{
	"new_search": {
		"results": [
			{"id": "m002ttg5", "type": "episode", "title": "EastEnders", "subtitle": "06/04/2026", "release_date": "6 Apr 2026", "parent_position": 7307}
		]
	}
}`

func TestCapsEndpoint(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil)
	req := httptest.NewRequest("GET", "/newznab/api?t=caps", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `<searching>`) {
		t.Error("missing <searching> in caps")
	}
	if !strings.Contains(body, `supportedParams="q,season,ep,tvdbid"`) {
		t.Error("missing tvsearch supportedParams")
	}
	if !strings.Contains(body, `id="5000"`) {
		t.Error("missing TV category 5000")
	}

	var caps struct{}
	if err := xml.Unmarshal(w.Body.Bytes(), &caps); err != nil {
		t.Errorf("invalid XML: %v", err)
	}
}

type rssItem struct {
	Title string `xml:"title"`
	GUID  string `xml:"guid"`
}

// itemTitles extracts <title> values from a Newznab RSS body, skipping the
// channel title.
func itemTitles(body string) []string {
	var doc struct {
		Channel struct {
			Items []rssItem `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal([]byte(body), &doc); err != nil {
		return nil
	}
	titles := make([]string, 0, len(doc.Channel.Items))
	for _, it := range doc.Channel.Items {
		titles = append(titles, it.Title)
	}
	return titles
}

func rssItems(body string) []rssItem {
	var doc struct {
		Channel struct {
			Items []rssItem `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal([]byte(body), &doc); err != nil {
		return nil
	}
	return doc.Channel.Items
}

func itemQualities(t *testing.T, body string) []string {
	t.Helper()
	items := rssItems(body)
	qualities := make([]string, 0, len(items))
	for _, it := range items {
		u, err := url.Parse(strings.TrimSpace(it.GUID))
		if err != nil {
			t.Fatalf("parse GUID URL %q: %v", it.GUID, err)
		}
		info, err := DecodeGUID(u.Query().Get("id"))
		if err != nil {
			t.Fatalf("decode GUID %q: %v", it.GUID, err)
		}
		qualities = append(qualities, info.Quality)
	}
	return qualities
}

func countQuality(qualities []string, want string) int {
	count := 0
	for _, quality := range qualities {
		if quality == want {
			count++
		}
	}
	return count
}

func newSearchPayload(results ...string) string {
	return `{"new_search":{"results":[` + strings.Join(results, ",") + `]}}`
}

func TestHandleTVSearchDailyMatchByDate(t *testing.T) {
	// Sonarr daily-series search format: season=YYYY, ep=MM/DD.
	// EastEnders has no S/E numbering on iPlayer (subtitle is the date,
	// parent_position is the cumulative counter). The handler must
	// recognise the year+date query and match by air date instead of by
	// integer season/episode.
	h := newHandlerWithBBC(t, eastendersOneEpisodePayload)
	req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch&q=eastenders&season=2026&ep=04%2F06", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	titles := itemTitles(w.Body.String())
	if len(titles) == 0 {
		t.Fatalf("expected at least one item, got empty body:\n%s", w.Body.String())
	}
	for _, title := range titles {
		if !strings.Contains(title, "EastEnders.2026.04.06") {
			t.Errorf("title = %q, want it to contain %q", title, "EastEnders.2026.04.06")
		}
		if strings.Contains(title, "S01E7307") {
			t.Errorf("title = %q must not use S01E<position> for daily shows", title)
		}
	}
}

func TestHandleTVSearchTopicalWeeklyFallbackToDate(t *testing.T) {
	// GitHub #20: Sonarr searches Question Time with standard integer
	// season/ep (season=48&ep=23) because TVDB numbers the show. BBC
	// iPlayer reports topical/weekly shows with no series/episode
	// numbering (Series=0, EpisodeNum=0) but a valid release_date. A
	// strict integer-S/E filter returned zero items, so Sonarr could
	// never match it even though the in-app search found the episode.
	// The handler must fall back to a date-tier release so the user can
	// set the Sonarr series type to "Daily" and match by air date.
	payload := `{
		"new_search": {
			"results": [
				{"id": "qt1", "type": "episode", "title": "Question Time", "subtitle": "26/03/2026", "release_date": "2026-03-26", "parent_position": 23}
			]
		}
	}`
	h := newHandlerWithBBC(t, payload)
	req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch&q=question+time&season=48&ep=23", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	titles := itemTitles(w.Body.String())
	if len(titles) == 0 {
		t.Fatalf("expected at least one item for topical fallback, got empty body:\n%s", w.Body.String())
	}
	for _, title := range titles {
		if !strings.Contains(title, "Question.Time.2026.03.26") {
			t.Errorf("title = %q, want it to contain %q", title, "Question.Time.2026.03.26")
		}
		if strings.Contains(title, "S48E23") {
			t.Errorf("title = %q must not claim S/E numbering iPlayer did not provide", title)
		}
	}
}

func TestHandleTVSearchDailyMismatchByDate(t *testing.T) {
	// Wrong date should return zero items.
	h := newHandlerWithBBC(t, eastendersOneEpisodePayload)
	req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch&q=eastenders&season=2026&ep=01%2F01", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	titles := itemTitles(w.Body.String())
	if len(titles) != 0 {
		t.Errorf("expected zero items for mismatched date, got %d: %v", len(titles), titles)
	}
}

func TestHandleTVSearchPubDateFromAvailability(t *testing.T) {
	// #47 fallback chain: with no store wired, the RSS <pubDate> must
	// reflect when the episode became available on iPlayer
	// (versions[].availability.start), not its original broadcast date
	// (release_date). A stale broadcast-dated pubDate sinks the release
	// below Sonarr's RSS watermark, so Sonarr never auto-grabs it. With
	// a store, the first-seen stamp takes precedence; see
	// TestHandleTVSearch_FirstSeenBecomesPubDate.
	payload := `{
		"new_search": {
			"results": [
				{"id": "gcrj11", "type": "episode", "title": "Great Continental Railway Journeys", "subtitle": "Series 9: Episode 11", "release_date": "2026-05-20", "parent_position": 11, "versions": [
					{"download": true, "duration": {"value": "PT28M45.040S"}, "availability": {"start": "2026-05-26T17:45:00Z"}}
				]}
			]
		}
	}`
	h := newHandlerWithBBC(t, payload)
	req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch&q=great+continental+railway+journeys&season=9&ep=11", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}

	var feed struct {
		Channel struct {
			Items []struct {
				Title   string `xml:"title"`
				PubDate string `xml:"pubDate"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(w.Body.Bytes(), &feed); err != nil {
		t.Fatalf("parse RSS: %v\n%s", err, w.Body.String())
	}
	if len(feed.Channel.Items) == 0 {
		t.Fatalf("expected at least one item, got none:\n%s", w.Body.String())
	}

	wantAvail := time.Date(2026, 5, 26, 17, 45, 0, 0, time.UTC).Format(time.RFC1123Z)
	broadcast := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Format(time.RFC1123Z)
	got := feed.Channel.Items[0].PubDate
	if got == broadcast {
		t.Errorf("pubDate = %q is the broadcast date; want the iPlayer availability date %q", got, wantAvail)
	}
	if got != wantAvail {
		t.Errorf("pubDate = %q, want %q (versions[].availability.start)", got, wantAvail)
	}
}

// feedEntry pairs each RSS item's decoded GUID with its pubDate so
// tests can assert per-PID dates without relying on item order alone.
type feedEntry struct {
	pid     string
	quality string
	pubDate string
}

func feedEntries(t *testing.T, body string) []feedEntry {
	t.Helper()
	var doc struct {
		Channel struct {
			Items []struct {
				GUID    string `xml:"guid"`
				PubDate string `xml:"pubDate"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("parse RSS: %v\n%s", err, body)
	}
	entries := make([]feedEntry, 0, len(doc.Channel.Items))
	for _, it := range doc.Channel.Items {
		u, err := url.Parse(strings.TrimSpace(it.GUID))
		if err != nil {
			t.Fatalf("parse GUID URL %q: %v", it.GUID, err)
		}
		info, err := DecodeGUID(u.Query().Get("id"))
		if err != nil {
			t.Fatalf("decode GUID %q: %v", it.GUID, err)
		}
		entries = append(entries, feedEntry{pid: info.PID, quality: info.Quality, pubDate: it.PubDate})
	}
	return entries
}

// TestHandleTVSearch_FirstSeenBecomesPubDate pins the second half of
// issue #47: availability.start can still pre-date the moment an item
// first surfaces in this feed (BBC promotes items onto the browse
// rails late), so the wildcard RSS path stamps each PID on first emit
// and reuses that stamp as <pubDate> on every later poll.
func TestHandleTVSearch_FirstSeenBecomesPubDate(t *testing.T) {
	payload := `{
		"new_search": {
			"results": [
				{
					"id": "m0023x9y",
					"type": "episode",
					"title": "Great Continental Railway Journeys",
					"subtitle": "Series 9: Episode 11",
					"release_date": "2020-05-20",
					"versions": [
						{
							"download": true,
							"duration": {"value": "PT58M"},
							"availability": {"start": "2020-05-26T17:45:00Z"}
						}
					]
				}
			]
		}
	}`
	h, _ := newHandlerWithBBCAndStore(t, payload)

	get := func() string {
		// No q= and no tvdbid=: the wildcard RSS-sync shape, the only
		// path allowed to create first-seen stamps.
		req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d", w.Code)
		}
		return w.Body.String()
	}

	before := time.Now()
	first := feedEntries(t, get())
	if len(first) == 0 {
		t.Fatal("expected at least one item")
	}

	avail := time.Date(2020, 5, 26, 17, 45, 0, 0, time.UTC).Format(time.RFC1123Z)
	if first[0].pubDate == avail {
		t.Errorf("pubDate = availability date %q; want a fresh first-seen stamp", avail)
	}
	stamp, err := time.Parse(time.RFC1123Z, first[0].pubDate)
	if err != nil {
		t.Fatalf("parse pubDate %q: %v", first[0].pubDate, err)
	}
	if stamp.Before(before.Add(-time.Minute)) || stamp.After(time.Now().Add(time.Minute)) {
		t.Errorf("pubDate %v not stamped around request time", stamp)
	}

	second := feedEntries(t, get())
	if second[0].pubDate != first[0].pubDate {
		t.Errorf("second request pubDate %q != first %q; first-seen stamp must be stable",
			second[0].pubDate, first[0].pubDate)
	}
}

// TestSearch_QuerySearchDoesNotStamp pins the first-seen write gate:
// only the wildcard RSS path creates stamps. If a q= search stamped,
// an item surfaced by a search at T1 but never grabbed would later
// debut in the wildcard feed with pubDate=T1 -- already below
// Sonarr's RSS watermark, recreating issue #47 via the search path.
func TestSearch_QuerySearchDoesNotStamp(t *testing.T) {
	payload := newSearchPayload(`{
		"id": "pidee",
		"type": "episode",
		"title": "Trawler Wars",
		"subtitle": "Series 1: Episode 1",
		"release_date": "2020-05-20",
		"versions": [
			{
				"download": true,
				"duration": {"value": "PT28M"},
				"availability": {"start": "2020-05-26T17:45:00Z"}
			}
		]
	}`)
	h, st := newHandlerWithBBCAndStore(t, payload)

	get := func(query string) string {
		req := httptest.NewRequest("GET", "/newznab/api?"+query, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d", w.Code)
		}
		return w.Body.String()
	}

	// A q= search falls back to the availability date and must not
	// start the PID's first-seen clock.
	avail := time.Date(2020, 5, 26, 17, 45, 0, 0, time.UTC).Format(time.RFC1123Z)
	searched := feedEntries(t, get("t=search&q=trawler+wars"))
	if len(searched) == 0 {
		t.Fatal("search emitted no items")
	}
	if searched[0].pubDate != avail {
		t.Errorf("search pubDate = %q, want availability fallback %q", searched[0].pubDate, avail)
	}
	if stamps, err := st.GetFirstSeenBatch([]string{"pidee"}); err != nil {
		t.Fatalf("read stamps: %v", err)
	} else if len(stamps) != 0 {
		t.Errorf("q= search stamped pidee at %v; writes are browse-only", stamps["pidee"])
	}

	// The wildcard browse then stamps fresh: its pubDate must not
	// inherit the search-time fallback date.
	before := time.Now()
	browsed := feedEntries(t, get("t=search"))
	if len(browsed) == 0 {
		t.Fatal("browse emitted no items")
	}
	if browsed[0].pubDate == avail {
		t.Errorf("browse pubDate = search-time fallback %q; want a fresh stamp", avail)
	}
	stamp, err := time.Parse(time.RFC1123Z, browsed[0].pubDate)
	if err != nil {
		t.Fatalf("parse pubDate %q: %v", browsed[0].pubDate, err)
	}
	if stamp.Before(before.Add(-time.Minute)) {
		t.Errorf("browse pubDate %v pre-dates the poll that first emitted the item", stamp)
	}

	// A follow-up browse returns the same fresh stamp.
	again := feedEntries(t, get("t=search"))
	if len(again) == 0 {
		t.Fatal("second browse emitted no items")
	}
	if again[0].pubDate != browsed[0].pubDate {
		t.Errorf("second browse pubDate %q != first %q; stamp must be stable",
			again[0].pubDate, browsed[0].pubDate)
	}
}

// TestSearch_QuerySearchReturnsExistingStamp pins the read side of the
// write gate: once the wildcard feed has stamped a PID, a q= search
// for it advertises the same pubDate (so Sonarr and Prowlarr caches
// agree with the feed) without disturbing the stored stamp.
func TestSearch_QuerySearchReturnsExistingStamp(t *testing.T) {
	payload := newSearchPayload(`{
		"id": "pidff",
		"type": "episode",
		"title": "Trawler Wars",
		"subtitle": "Series 1: Episode 1",
		"release_date": "2020-05-20",
		"versions": [
			{
				"download": true,
				"duration": {"value": "PT28M"},
				"availability": {"start": "2020-05-26T17:45:00Z"}
			}
		]
	}`)
	h, st := newHandlerWithBBCAndStore(t, payload)

	get := func(query string) string {
		req := httptest.NewRequest("GET", "/newznab/api?"+query, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d", w.Code)
		}
		return w.Body.String()
	}

	// Browse first: the wildcard feed stamps pidff.
	browsed := feedEntries(t, get("t=search"))
	if len(browsed) == 0 {
		t.Fatal("browse emitted no items")
	}

	// Sentinel-probe: a stamped PID returns its original timestamp
	// rather than adopting the sentinel.
	sentinel := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	stamps, err := st.GetOrSetFirstSeenBatch([]string{"pidff"}, sentinel)
	if err != nil {
		t.Fatalf("sentinel probe: %v", err)
	}
	if stamps["pidff"].Equal(sentinel) {
		t.Fatal("browse did not stamp pidff")
	}
	original := stamps["pidff"]

	// The q= search reuses the feed's stamp, not the availability
	// fallback.
	searched := feedEntries(t, get("t=search&q=trawler+wars"))
	if len(searched) == 0 {
		t.Fatal("search emitted no items")
	}
	if searched[0].pubDate != browsed[0].pubDate {
		t.Errorf("search pubDate = %q, want feed stamp %q", searched[0].pubDate, browsed[0].pubDate)
	}

	// The search's read-only lookup must not have moved the stamp.
	after, err := st.GetFirstSeenBatch([]string{"pidff"})
	if err != nil {
		t.Fatalf("read stamps: %v", err)
	}
	if !after["pidff"].Equal(original) {
		t.Errorf("stamp moved from %v to %v across a q= search; lookup must be read-only",
			original, after["pidff"])
	}
}

// TestSearch_LimitTrimmedItemNotStamped pins the limit/stamp ordering
// on the wildcard feed: an item trimmed by Sonarr's `limit=N`
// parameter never reached the client, so it must not be stamped --
// otherwise its pubDate would pre-date the first feed Sonarr actually
// saw it in, recreating issue #47 in miniature.
func TestSearch_LimitTrimmedItemNotStamped(t *testing.T) {
	payload := newSearchPayload(
		`{"id": "pidaa", "type": "episode", "title": "Trawler Wars", "subtitle": "Series 1: Episode 1"}`,
		`{"id": "pidbb", "type": "episode", "title": "Trawler Wars", "subtitle": "Series 1: Episode 2"}`,
	)
	h, st := newHandlerWithBBCAndStore(t, payload)

	get := func(query string) string {
		req := httptest.NewRequest("GET", "/newznab/api?"+query, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d", w.Code)
		}
		return w.Body.String()
	}

	// limit=1 emits only pidaa's first quality variant; pidbb is
	// trimmed before rendering.
	capped := feedEntries(t, get("t=search&limit=1"))
	if len(capped) != 1 {
		t.Fatalf("limit=1 emitted %d items, want 1", len(capped))
	}
	if capped[0].pid != "pidaa" {
		t.Fatalf("limit=1 emitted %s, want pidaa", capped[0].pid)
	}

	// Probe the store with a sentinel: an unstamped PID adopts it, a
	// stamped PID returns its original timestamp.
	sentinel := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	stamps, err := st.GetOrSetFirstSeenBatch([]string{"pidaa", "pidbb"}, sentinel)
	if err != nil {
		t.Fatalf("sentinel probe: %v", err)
	}
	if stamps["pidaa"].Equal(sentinel) {
		t.Error("pidaa was emitted under limit=1 but has no first-seen stamp")
	}
	if !stamps["pidbb"].Equal(sentinel) {
		t.Errorf("pidbb was trimmed by limit=1 yet carries stamp %v; trimmed items must not be stamped",
			stamps["pidbb"])
	}

	// An uncapped request now emits pidbb with the sentinel stamp,
	// proving the feed reads the store stamp and that the limit=1
	// request never wrote one for pidbb.
	full := feedEntries(t, get("t=search"))
	if len(full) != 4 {
		t.Fatalf("uncapped request emitted %d items, want 4", len(full))
	}
	wantSentinel := sentinel.Format(time.RFC1123Z)
	for _, e := range full {
		switch e.pid {
		case "pidaa":
			if e.pubDate != capped[0].pubDate {
				t.Errorf("pidaa pubDate = %q, want original stamp %q", e.pubDate, capped[0].pubDate)
			}
		case "pidbb":
			if e.pubDate != wantSentinel {
				t.Errorf("pidbb pubDate = %q, want sentinel stamp %q", e.pubDate, wantSentinel)
			}
		default:
			t.Errorf("unexpected PID %s in feed", e.pid)
		}
	}
}

// TestSearch_BroadcastDateAttrCarriesAvailability verifies the bespoke
// iparr:broadcastdate attribute carries the availability date once the
// first-seen stamp owns <pubDate>, and is omitted when the result has
// neither an availability window nor a broadcast date. The attribute
// is deliberately not called usenetdate: Sonarr prefers usenetdate
// over pubDate for release age, which would resurrect issue #47.
func TestSearch_BroadcastDateAttrCarriesAvailability(t *testing.T) {
	payload := newSearchPayload(
		`{
			"id": "pidcc",
			"type": "episode",
			"title": "Trawler Wars",
			"subtitle": "Series 1: Episode 1",
			"release_date": "2020-05-20",
			"versions": [
				{
					"download": true,
					"duration": {"value": "PT28M"},
					"availability": {"start": "2020-05-26T17:45:00Z"}
				}
			]
		}`,
		`{"id": "piddd", "type": "episode", "title": "Trawler Wars", "subtitle": "Series 1: Episode 2"}`,
	)
	h, _ := newHandlerWithBBCAndStore(t, payload)

	req := httptest.NewRequest("GET", "/newznab/api?t=search&q=trawler+wars", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	body := w.Body.String()

	wantAvail := time.Date(2020, 5, 26, 17, 45, 0, 0, time.UTC).Format(time.RFC1123Z)
	wantAttr := `<newznab:attr name="iparr:broadcastdate" value="` + wantAvail + `" />`
	if !strings.Contains(body, wantAttr) {
		t.Errorf("feed missing %q:\n%s", wantAttr, body)
	}
	// Both of pidcc's quality variants carry the attribute; piddd has
	// neither availability nor release_date, so its variants omit it.
	if got := strings.Count(body, `name="iparr:broadcastdate"`); got != 2 {
		t.Errorf("broadcastdate attr count = %d, want 2", got)
	}
	if strings.Contains(body, `name="usenetdate"`) {
		t.Error("feed must not emit usenetdate; Sonarr would prefer it over pubDate for release age")
	}
}

// TestSearch_QualityVariantsShareOnePubDate verifies pubDate is
// computed once per PID: every quality variant of an episode must
// advertise the same age to Sonarr.
func TestSearch_QualityVariantsShareOnePubDate(t *testing.T) {
	prober := &mockProber{results: map[string][]int{"m002ttg5": {1080, 720}}}
	h, _ := newHandlerWithBBCProberAndStore(t, eastendersOneEpisodePayload, prober)

	req := httptest.NewRequest("GET", "/newznab/api?t=search&q=eastenders", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	entries := feedEntries(t, w.Body.String())
	if len(entries) != 2 {
		t.Fatalf("emitted %d items, want 2 quality variants", len(entries))
	}
	if entries[0].pid != entries[1].pid {
		t.Fatalf("expected one PID, got %s and %s", entries[0].pid, entries[1].pid)
	}
	if entries[0].pubDate != entries[1].pubDate {
		t.Errorf("variants disagree on pubDate: %q (%s) vs %q (%s)",
			entries[0].pubDate, entries[0].quality, entries[1].pubDate, entries[1].quality)
	}
}

// TestSearch_NotYetAvailable_NotStampedUntilEmitted pins the
// interaction between issues #44 and #47: an episode skipped because
// BBC has not published its streams yet must not collect a first-seen
// stamp while hidden, then gets stamped on the wildcard-feed cycle it
// first emits.
func TestSearch_NotYetAvailable_NotStampedUntilEmitted(t *testing.T) {
	prober := &mockProber{
		results:         map[string][]int{"m002ttg5": {720}},
		notYetAvailable: map[string]bool{"m002ttg5": true},
	}
	h, st := newHandlerWithBBCProberAndStore(t, eastendersOneEpisodePayload, prober)

	get := func() string {
		req := httptest.NewRequest("GET", "/newznab/api?t=search", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d", w.Code)
		}
		return w.Body.String()
	}

	// Cycle 1: streams not published yet -- no item emitted.
	if entries := feedEntries(t, get()); len(entries) != 0 {
		t.Fatalf("not-yet-available cycle emitted %d items, want 0", len(entries))
	}

	// Prove the skipped PID was not stamped: an unstamped PID adopts
	// the sentinel. Purge the sentinel (it is older than the 90-day
	// window) so the next cycle starts clean.
	sentinel := time.Now().UTC().Truncate(time.Second).Add(-365 * 24 * time.Hour)
	stamps, err := st.GetOrSetFirstSeenBatch([]string{"m002ttg5"}, sentinel)
	if err != nil {
		t.Fatalf("sentinel probe: %v", err)
	}
	if !stamps["m002ttg5"].Equal(sentinel) {
		t.Fatalf("PID stamped at %v while not-yet-available; skipped items must not be stamped",
			stamps["m002ttg5"])
	}
	if n, err := st.PurgeStaleFirstSeen(90 * 24 * time.Hour); err != nil || n != 1 {
		t.Fatalf("purge sentinel: n=%d err=%v", n, err)
	}

	// Cycle 2: streams live -- the item emits with a fresh stamp.
	prober.notYetAvailable = map[string]bool{}
	before := time.Now()
	entries := feedEntries(t, get())
	if len(entries) != 1 {
		t.Fatalf("available cycle emitted %d items, want 1", len(entries))
	}
	stamp, err := time.Parse(time.RFC1123Z, entries[0].pubDate)
	if err != nil {
		t.Fatalf("parse pubDate %q: %v", entries[0].pubDate, err)
	}
	if stamp.Before(before.Add(-time.Minute)) {
		t.Errorf("pubDate %v pre-dates the cycle the item first emitted", stamp)
	}
}

func TestHandleTVSearchFiltersOtherShowsByName(t *testing.T) {
	// Regression: BBC iPlayer's IBL search is relevance-ranked, so a query
	// like "Little Britain" returns ~24 unrelated programmes whose titles
	// merely contain "Britain" (Cunk on Britain, Drugs Map of Britain, A
	// History of Ancient Britain, Inside Britain's National Parks, ...).
	// Without a show-name filter every one of those gets expanded into
	// episodes and matched against Sonarr's S01E01 query, flooding the
	// manual search UI with false positives. Issue #13.
	payload := `{
		"new_search": {
			"results": [
				{"id": "b0074d8v", "type": "episode", "title": "Little Britain", "subtitle": "Series 1: Episode 1", "release_date": "2003-09-16", "parent_position": 1},
				{"id": "cunk1", "type": "episode", "title": "Cunk on Britain", "subtitle": "Series 1: Episode 1", "release_date": "2018-04-03", "parent_position": 1},
				{"id": "drugs1", "type": "episode", "title": "Drugs Map of Britain", "subtitle": "Series 1: 1. Nitrous Oxide", "release_date": "2017-11-08", "parent_position": 1},
				{"id": "history1", "type": "episode", "title": "A History of Ancient Britain", "subtitle": "Series 1: 1. Age of Ice", "release_date": "2011-02-03", "parent_position": 1}
			]
		}
	}`
	h := newHandlerWithBBC(t, payload)
	req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch&q=little+britain&season=1&ep=1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	titles := itemTitles(w.Body.String())
	if len(titles) == 0 {
		t.Fatalf("expected Little Britain releases, got empty body:\n%s", w.Body.String())
	}
	for _, title := range titles {
		if !strings.HasPrefix(title, "Little.Britain.S01E01") {
			t.Errorf("title = %q, want Little.Britain.S01E01.* (other-show filter should reject this)", title)
		}
	}
}

func TestHandleSearchBrowseHasNoNameFilter(t *testing.T) {
	// When neither q nor tvdbid is set Sonarr is doing a wildcard browse
	// for the RSS test feed (and the iplayer-arr web UI uses the same
	// path). The handler falls back to q="BBC" internally, but that must
	// not be applied as a show-name filter — every BBC programme should
	// still be returned.
	payload := `{
		"new_search": {
			"results": [
				{"id": "b0074d8v", "type": "episode", "title": "Little Britain", "subtitle": "Series 1: Episode 1", "release_date": "2003-09-16", "parent_position": 1},
				{"id": "drugs1", "type": "episode", "title": "Drugs Map of Britain", "subtitle": "Series 1: 1. Nitrous Oxide", "release_date": "2017-11-08", "parent_position": 1}
			]
		}
	}`
	h := newHandlerWithBBC(t, payload)
	req := httptest.NewRequest("GET", "/newznab/api?t=search", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	titles := itemTitles(w.Body.String())
	if len(titles) == 0 {
		t.Fatalf("expected browse results, got empty body:\n%s", w.Body.String())
	}
	gotLB := false
	gotDM := false
	for _, title := range titles {
		if strings.HasPrefix(title, "Little.Britain") {
			gotLB = true
		}
		if strings.HasPrefix(title, "Drugs.Map.of.Britain") {
			gotDM = true
		}
	}
	if !gotLB || !gotDM {
		t.Errorf("browse must include both shows (Little Britain seen=%v, Drugs Map of Britain seen=%v); titles=%v", gotLB, gotDM, titles)
	}
}

func TestHandleTVSearchStandardSEStillWorks(t *testing.T) {
	// Doctor Who S1E3 — proper S/E numbering must continue to filter by
	// integer season/episode and produce a Tier 1 title.
	payload := `{
		"new_search": {
			"results": [
				{"id": "b039d07m", "type": "episode", "title": "Doctor Who", "subtitle": "Series 1: 3. The Unquiet Dead", "release_date": "2005-04-09", "parent_position": 3}
			]
		}
	}`
	h := newHandlerWithBBC(t, payload)
	req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch&q=doctor+who&season=1&ep=3", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	titles := itemTitles(w.Body.String())
	if len(titles) == 0 {
		t.Fatalf("expected items, got empty body:\n%s", w.Body.String())
	}
	for _, title := range titles {
		if !strings.Contains(title, "S01E03") {
			t.Errorf("title = %q, want S01E03", title)
		}
	}
}

func TestSearch_ProbedPIDWith1080p_Emits1080p(t *testing.T) {
	prober := &mockProber{results: map[string][]int{"m002ttg5": {1080, 720, 540}}}
	h := newHandlerWithBBCProber(t, eastendersOneEpisodePayload, prober)
	req := httptest.NewRequest("GET", "/newznab/api?t=search&q=eastenders", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	qualities := itemQualities(t, w.Body.String())
	if countQuality(qualities, "1080p") == 0 {
		t.Fatalf("expected at least one 1080p item, got %v", qualities)
	}
}

func TestSearch_ProbedPIDWith720pOnly_OmitsFake1080p(t *testing.T) {
	prober := &mockProber{results: map[string][]int{"m002ttg5": {720, 540}}}
	h := newHandlerWithBBCProber(t, eastendersOneEpisodePayload, prober)
	req := httptest.NewRequest("GET", "/newznab/api?t=search&q=eastenders", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	qualities := itemQualities(t, w.Body.String())
	if countQuality(qualities, "1080p") != 0 {
		t.Fatalf("expected no 1080p items, got %v", qualities)
	}
	if len(qualities) != 2 || countQuality(qualities, "720p") != 1 || countQuality(qualities, "540p") != 1 {
		t.Fatalf("expected exactly [720p 540p], got %v", qualities)
	}
}

func TestSearch_ProbeFailure_Emits720pAnd540pFallback(t *testing.T) {
	prober := &mockProber{results: map[string][]int{"m002ttg5": nil}}
	h := newHandlerWithBBCProber(t, eastendersOneEpisodePayload, prober)
	req := httptest.NewRequest("GET", "/newznab/api?t=search&q=eastenders", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	qualities := itemQualities(t, w.Body.String())
	if len(qualities) != 2 || countQuality(qualities, "720p") != 1 || countQuality(qualities, "540p") != 1 || countQuality(qualities, "1080p") != 0 {
		t.Fatalf("expected fallback qualities [720p 540p], got %v", qualities)
	}
}

// TestSearch_NotYetAvailable_SkipsItem pins the Issue #44 indexer change:
// when the prober flags a PID not-yet-available (BBC streams not published
// yet), the search handler must emit no <item> for it rather than fall back
// to a 720p/540p advert that Sonarr would grab and then blocklist.
func TestSearch_NotYetAvailable_SkipsItem(t *testing.T) {
	prober := &mockProber{
		results:         map[string][]int{"m002ttg5": nil},
		notYetAvailable: map[string]bool{"m002ttg5": true},
	}
	h := newHandlerWithBBCProber(t, eastendersOneEpisodePayload, prober)
	req := httptest.NewRequest("GET", "/newznab/api?t=search&q=eastenders", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if items := rssItems(w.Body.String()); len(items) != 0 {
		t.Fatalf("expected no items for not-yet-available PID, got %d", len(items))
	}
}

func TestSearch_PrefetchOnlyForFilteredResults_NameFilter(t *testing.T) {
	payload := newSearchPayload(
		`{"id":"dw1","type":"episode","title":"Doctor Who","subtitle":"Series 1: 1. Rose","release_date":"2005-03-26","parent_position":1}`,
		`{"id":"dw2","type":"episode","title":"Doctor Who","subtitle":"Series 1: 2. The End of the World","release_date":"2005-04-02","parent_position":2}`,
		`{"id":"other1","type":"episode","title":"EastEnders","subtitle":"Series 1: 1. One","release_date":"2026-04-01","parent_position":1}`,
		`{"id":"other2","type":"episode","title":"Newsnight","subtitle":"Series 1: 1. One","release_date":"2026-04-01","parent_position":1}`,
		`{"id":"other3","type":"episode","title":"Blue Peter","subtitle":"Series 1: 1. One","release_date":"2026-04-01","parent_position":1}`,
		`{"id":"other4","type":"episode","title":"Panorama","subtitle":"Series 1: 1. One","release_date":"2026-04-01","parent_position":1}`,
		`{"id":"other5","type":"episode","title":"Question Time","subtitle":"Series 1: 1. One","release_date":"2026-04-01","parent_position":1}`,
		`{"id":"other6","type":"episode","title":"Casualty","subtitle":"Series 1: 1. One","release_date":"2026-04-01","parent_position":1}`,
		`{"id":"other7","type":"episode","title":"Silent Witness","subtitle":"Series 1: 1. One","release_date":"2026-04-01","parent_position":1}`,
		`{"id":"other8","type":"episode","title":"Gardeners' World","subtitle":"Series 1: 1. One","release_date":"2026-04-01","parent_position":1}`,
	)
	prober := &mockProber{results: map[string][]int{"dw1": {720, 540}, "dw2": {720, 540}}}
	h := newHandlerWithBBCProber(t, payload, prober)
	req := httptest.NewRequest("GET", "/newznab/api?t=search&q=doctor+who", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if len(prober.calls) != 1 {
		t.Fatalf("expected one prefetch call, got %d", len(prober.calls))
	}
	if len(prober.calls[0]) != 2 {
		t.Fatalf("expected 2 prefetched PIDs, got %d: %+v", len(prober.calls[0]), prober.calls[0])
	}
	got := map[string]bool{}
	for _, item := range prober.calls[0] {
		got[item.PID] = true
	}
	if !got["dw1"] || !got["dw2"] || len(got) != 2 {
		t.Fatalf("expected prefetched PIDs dw1 and dw2, got %+v", prober.calls[0])
	}
}

func TestSearch_PrefetchOnlyForFilteredResults_SeasonEpisode(t *testing.T) {
	payload := newSearchPayload(
		`{"id":"p1","type":"episode","title":"Doctor Who","subtitle":"Series 14: 1. One","release_date":"2026-04-01","parent_position":1}`,
		`{"id":"p2","type":"episode","title":"Doctor Who","subtitle":"Series 14: 2. Two","release_date":"2026-04-08","parent_position":2}`,
		`{"id":"p3","type":"episode","title":"Doctor Who","subtitle":"Series 14: 3. Three","release_date":"2026-04-15","parent_position":3}`,
		`{"id":"p4","type":"episode","title":"Doctor Who","subtitle":"Series 14: 4. Four","release_date":"2026-04-22","parent_position":4}`,
		`{"id":"p5","type":"episode","title":"Doctor Who","subtitle":"Series 14: 5. Five","release_date":"2026-04-29","parent_position":5}`,
		`{"id":"p6","type":"episode","title":"Doctor Who","subtitle":"Series 14: 6. Six","release_date":"2026-05-06","parent_position":6}`,
		`{"id":"p7","type":"episode","title":"Doctor Who","subtitle":"Series 14: 7. Seven","release_date":"2026-05-13","parent_position":7}`,
		`{"id":"p8","type":"episode","title":"Doctor Who","subtitle":"Series 14: 8. Eight","release_date":"2026-05-20","parent_position":8}`,
		`{"id":"p9","type":"episode","title":"Doctor Who","subtitle":"Series 14: 9. Nine","release_date":"2026-05-27","parent_position":9}`,
		`{"id":"p10","type":"episode","title":"Doctor Who","subtitle":"Series 14: 10. Ten","release_date":"2026-06-03","parent_position":10}`,
		`{"id":"p11","type":"episode","title":"Doctor Who","subtitle":"Series 14: 11. Eleven","release_date":"2026-06-10","parent_position":11}`,
		`{"id":"p12","type":"episode","title":"Doctor Who","subtitle":"Series 14: 12. Twelve","release_date":"2026-06-17","parent_position":12}`,
	)
	prober := &mockProber{results: map[string][]int{"p3": {720, 540}}}
	h := newHandlerWithBBCProber(t, payload, prober)
	req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch&q=doctor+who&season=14&ep=3", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if len(prober.calls) != 1 {
		t.Fatalf("expected one prefetch call, got %d", len(prober.calls))
	}
	if len(prober.calls[0]) != 1 || prober.calls[0][0].PID != "p3" {
		t.Fatalf("expected exactly pid p3 to be prefetched, got %+v", prober.calls[0])
	}
}

func TestSearch_PrefetchOnlyForFilteredResults_DailyDate(t *testing.T) {
	payload := newSearchPayload(
		`{"id":"n1","type":"episode","title":"Newsnight","subtitle":"05/04/2026","release_date":"2026-04-05","parent_position":1}`,
		`{"id":"n2","type":"episode","title":"Newsnight","subtitle":"04/04/2026","release_date":"2026-04-04","parent_position":2}`,
		`{"id":"n3","type":"episode","title":"Newsnight","subtitle":"03/04/2026","release_date":"2026-04-03","parent_position":3}`,
		`{"id":"n4","type":"episode","title":"Newsnight","subtitle":"02/04/2026","release_date":"2026-04-02","parent_position":4}`,
		`{"id":"n5","type":"episode","title":"Newsnight","subtitle":"01/04/2026","release_date":"2026-04-01","parent_position":5}`,
		`{"id":"n6","type":"episode","title":"Newsnight","subtitle":"06/04/2026","release_date":"2026-04-06","parent_position":6}`,
		`{"id":"n7","type":"episode","title":"Newsnight","subtitle":"07/04/2026","release_date":"2026-04-07","parent_position":7}`,
	)
	prober := &mockProber{results: map[string][]int{"n1": {720, 540}}}
	h := newHandlerWithBBCProber(t, payload, prober)
	req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch&q=newsnight&season=2026&ep=04%2F05", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if len(prober.calls) != 1 {
		t.Fatalf("expected one prefetch call, got %d", len(prober.calls))
	}
	if len(prober.calls[0]) != 1 || prober.calls[0][0].PID != "n1" {
		t.Fatalf("expected exactly pid n1 to be prefetched, got %+v", prober.calls[0])
	}
}

func TestSearch_NoProberConfigured_OmitsExtraQualities(t *testing.T) {
	h := newHandlerWithBBC(t, eastendersOneEpisodePayload)
	req := httptest.NewRequest("GET", "/newznab/api?t=search&q=eastenders", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	qualities := itemQualities(t, w.Body.String())
	if len(qualities) != 2 || countQuality(qualities, "720p") != 1 || countQuality(qualities, "540p") != 1 || countQuality(qualities, "1080p") != 0 {
		t.Fatalf("expected no-prober fallback qualities [720p 540p], got %v", qualities)
	}
}

func TestSearch_DuplicatePIDFromBrandAndEpisode_ProbesOnce(t *testing.T) {
	payload := newSearchPayload(
		`{"id":"dup1","type":"episode","title":"Doctor Who","subtitle":"Series 14: 3. Three","release_date":"2026-04-15","parent_position":3}`,
		`{"id":"dup1","type":"episode","title":"Doctor Who","subtitle":"Series 14: 3. Three","release_date":"2026-04-15","parent_position":3}`,
	)
	prober := &mockProber{results: map[string][]int{"dup1": {1080, 720, 540}}}
	h := newHandlerWithBBCProber(t, payload, prober)
	req := httptest.NewRequest("GET", "/newznab/api?t=search&q=doctor+who", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if len(prober.calls) != 1 {
		t.Fatalf("expected one prefetch call, got %d", len(prober.calls))
	}
	if len(prober.calls[0]) != 1 || prober.calls[0][0].PID != "dup1" {
		t.Fatalf("expected duplicate PID to be prefetched once, got %+v", prober.calls[0])
	}

	items := rssItems(w.Body.String())
	if len(items) != 3 {
		t.Fatalf("expected one item per quality for a deduped PID, got %d items", len(items))
	}
	seenGUIDs := map[string]struct{}{}
	for _, item := range items {
		if _, dup := seenGUIDs[item.GUID]; dup {
			t.Fatalf("duplicate GUID detected: %q", item.GUID)
		}
		seenGUIDs[item.GUID] = struct{}{}
	}
}

func TestMatchesSearchFilter_TableDriven(t *testing.T) {
	cases := []struct {
		name                   string
		prog                   *store.Programme
		wantName, filterDate   string
		filterSeason, filterEp int
		want                   bool
	}{
		{"no filters, all pass", &store.Programme{Name: "Doctor Who"}, "", "", 0, 0, true},
		{"name match", &store.Programme{Name: "Doctor Who"}, "doctor who", "", 0, 0, true},
		{"name mismatch", &store.Programme{Name: "EastEnders"}, "doctor who", "", 0, 0, false},
		{"season match", &store.Programme{Name: "Doctor Who", Series: 14}, "doctor who", "", 14, 0, true},
		{"season mismatch", &store.Programme{Name: "Doctor Who", Series: 13}, "doctor who", "", 14, 0, false},
		{"season+ep match", &store.Programme{Name: "Doctor Who", Series: 14, EpisodeNum: 3}, "doctor who", "", 14, 3, true},
		{"season+ep mismatch", &store.Programme{Name: "Doctor Who", Series: 14, EpisodeNum: 2}, "doctor who", "", 14, 3, false},
		{"daily date match", &store.Programme{Name: "Newsnight", AirDate: "2026-04-05"}, "newsnight", "2026-04-05", 0, 0, true},
		{"daily date mismatch", &store.Programme{Name: "Newsnight", AirDate: "2026-04-04"}, "newsnight", "2026-04-05", 0, 0, false},
		// Topical/weekly fallback: programme has no S/E numbering but a
		// valid AirDate. Sonarr sends integer season/ep (from TVDB) but
		// iPlayer never numbered the show. Must pass so the emit loop
		// produces a date-tier title. GitHub #20.
		{"topical weekly no numbering passes integer filter",
			&store.Programme{Name: "Question Time", AirDate: "2026-03-26"},
			"question time", "", 48, 23, true},
		{"topical without air date still rejected",
			&store.Programme{Name: "Question Time"},
			"question time", "", 48, 23, false},
		// Subtitle prefix: BBC brands a show with ": Subtitle" but Sonarr
		// searches the bare name. GitHub #21.
		{"subtitle prefix match",
			&store.Programme{Name: "Talking Tom Heroes: Suddenly Super", Series: 1, EpisodeNum: 40},
			"Talking Tom Heroes", "", 1, 40, true},
		{"subtitle prefix case insensitive",
			&store.Programme{Name: "talking tom heroes: suddenly super", Series: 1, EpisodeNum: 1},
			"Talking Tom Heroes", "", 1, 1, true},
		{"partial word before colon is not a match",
			&store.Programme{Name: "Tom Jones: Live"},
			"Tom", "", 0, 0, false},
		{"full colon title exact match",
			&store.Programme{Name: "Talking Tom Heroes: Suddenly Super", Series: 1, EpisodeNum: 1},
			"Talking Tom Heroes: Suddenly Super", "", 1, 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matchesSearchFilter(tc.prog, tc.wantName, tc.filterDate, tc.filterSeason, tc.filterEp)
			if got != tc.want {
				t.Errorf("matchesSearchFilter(%+v, %q, %q, %d, %d) = %v, want %v",
					tc.prog, tc.wantName, tc.filterDate, tc.filterSeason, tc.filterEp, got, tc.want)
			}
		})
	}
}

func TestSearch_DoctorWhoClassicTVDB_OnlyMatchesClassicBrand(t *testing.T) {
	// Cold cache: Skyhook returns ("Doctor Who", 1963) for TVDB 76107.
	// IBL returns all three Doctor Who brands. Only the classic-era
	// brand should appear in the RSS.

	skyhook := fakeSkyhookServer(t, `{"title":"Doctor Who","firstAired":"1963-11-23"}`)
	oldBase := skyhookBaseURL
	skyhookBaseURL = skyhook.URL
	t.Cleanup(func() { skyhookBaseURL = oldBase })

	h, _ := newHandlerWithBBCAndStore(t, doctorWhoThreeBrandsPayload)

	req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch&tvdbid=76107&season=1&ep=2", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()

	if !strings.Contains(body, "Doctor.Who.1963-1996") {
		t.Errorf("expected classic brand in RSS, got:\n%s", body)
	}
	if strings.Contains(body, "2005-2022") {
		t.Errorf("unexpected 2005-2022 brand leaked through, got:\n%s", body)
	}
}

func TestSearch_DoctorWhoModernTVDB_OnlyMatchesModernBrand(t *testing.T) {
	// Cold cache: Skyhook returns ("Doctor Who (2005)", 2005) for TVDB 78804.
	// IBL returns all three Doctor Who brands. Only the 2005-2022 brand
	// should appear in the RSS - the year 2005 falls within [2005, 2022]
	// but not within [1963, 1996].

	skyhook := fakeSkyhookServer(t, `{"title":"Doctor Who (2005)","firstAired":"2005-03-26"}`)
	oldBase := skyhookBaseURL
	skyhookBaseURL = skyhook.URL
	t.Cleanup(func() { skyhookBaseURL = oldBase })

	h, _ := newHandlerWithBBCAndStore(t, doctorWhoThreeBrandsPayload)

	req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch&tvdbid=78804&season=1&ep=2", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()

	if !strings.Contains(body, "2005-2022") {
		t.Errorf("expected 2005-2022 brand in RSS, got:\n%s", body)
	}
	if strings.Contains(body, "1963-1996") {
		t.Errorf("unexpected classic brand leaked through, got:\n%s", body)
	}
}

func TestSearch_DoctorWhoClassicTVDB_WarmCacheRetainsYear(t *testing.T) {
	// Scenario: TVDB 76107 has been looked up previously and the
	// mapping was cached with Year=1963. A new Sonarr search with
	// tvdbid=76107 must reuse the cached name AND the cached year, so
	// disambiguateByYear still routes to the classic brand WITHOUT
	// calling Skyhook.
	//
	// This test guards two distinct invariants:
	//   1. The disambiguation correctly routes to the classic brand
	//   2. Skyhook is NOT called - the cache is genuinely consulted
	//
	// (1) alone would also pass for an implementation that ignores
	// the cache and falls through to Skyhook (because Skyhook would
	// return the same data). The fail-fast Skyhook server makes (2)
	// observable.

	// Fail-fast Skyhook: any HTTP hit on this server is a test
	// failure. Tracks hits via atomic counter for an explicit final
	// assertion in case t.Errorf inside the goroutine doesn't fire
	// before the test finishes.
	var skyhookHits int32
	failSkyhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&skyhookHits, 1)
		t.Errorf("warm-cache test: Skyhook must not be called, but got %s %s", r.Method, r.URL.Path)
		http.Error(w, "warm-cache test: Skyhook unexpected", http.StatusServiceUnavailable)
	}))
	defer failSkyhook.Close()

	oldSkyhookBase := skyhookBaseURL
	skyhookBaseURL = failSkyhook.URL
	t.Cleanup(func() { skyhookBaseURL = oldSkyhookBase })

	h, st := newHandlerWithBBCAndStore(t, doctorWhoThreeBrandsPayload)

	// Pre-populate the cache as if a previous Skyhook lookup had run
	if err := st.PutSeriesMapping(&store.SeriesMapping{
		TVDBId:   "76107",
		ShowName: "Doctor Who",
		Year:     1963,
	}); err != nil {
		t.Fatalf("PutSeriesMapping: %v", err)
	}

	req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch&tvdbid=76107&season=1&ep=2", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()

	// Disambiguation correctness: classic brand emitted, modern brand not
	if !strings.Contains(body, "Doctor.Who.1963-1996") {
		t.Errorf("warm-cache test: expected classic brand name in RSS, got:\n%s", body)
	}
	if strings.Contains(body, "2005-2022") {
		t.Errorf("warm-cache test: unexpected 2005-2022 brand leaked through, got:\n%s", body)
	}

	// Cache-was-used invariant: Skyhook must not have been called
	if hits := atomic.LoadInt32(&skyhookHits); hits != 0 {
		t.Errorf("warm-cache test: expected 0 Skyhook hits, got %d", hits)
	}
}

// TestHandleTVSearch_TVDBIDRehydratedFromStore covers GitHub issue #31:
// Sonarr sends q=ShowName with an empty tvdbid on episode-level follow-up
// queries. Before the fix, the resulting RSS items had no tvdbid attr and
// Sonarr could not match them back to the series. After the fix, the
// handler does a reverse store lookup and threads the tvdbid through to
// writeResultsRSS.
func TestHandleTVSearch_TVDBIDRehydratedFromStore(t *testing.T) {
	payload := `{
		"new_search": {
			"results": [
				{"id": "b039d07m", "type": "episode", "title": "Doctor Who", "subtitle": "Series 14: 3. Boom", "release_date": "2024-05-18", "parent_position": 3}
			]
		}
	}`
	h, _ := newHandlerWithBBCAndStore(t, payload)

	// Seed the store as if an earlier tvdbid=78804 request had resolved.
	if err := h.store.PutSeriesMapping(&store.SeriesMapping{
		TVDBId: "78804", ShowName: "Doctor Who", Year: 2005,
	}); err != nil {
		t.Fatalf("seed PutSeriesMapping: %v", err)
	}

	// Sonarr's follow-up request: q filled in, tvdbid empty.
	req := httptest.NewRequest("GET",
		"/newznab/api?t=tvsearch&q=Doctor+Who&tvdbid=&season=14&ep=3", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `<newznab:attr name="tvdbid" value="78804"`) {
		t.Errorf("RSS missing rehydrated tvdbid attr.\nbody:\n%s", body)
	}
}

// TestHandleTVSearch_TVDBIDRehydrationCaseInsensitive covers the
// case-insensitive path of the reverse lookup.
func TestHandleTVSearch_TVDBIDRehydrationCaseInsensitive(t *testing.T) {
	payload := `{
		"new_search": {
			"results": [
				{"id": "m002pwlf", "type": "episode", "title": "Casualty", "subtitle": "Series 1: 1. Learning Curve", "release_date": "1986-09-06", "parent_position": 1}
			]
		}
	}`
	h, _ := newHandlerWithBBCAndStore(t, payload)
	h.store.PutSeriesMapping(&store.SeriesMapping{
		TVDBId: "71756", ShowName: "Casualty", Year: 1986,
	})

	// Note lower-case q in the request -- mapping is stored with title-case.
	req := httptest.NewRequest("GET",
		"/newznab/api?t=tvsearch&q=casualty&tvdbid=&season=1&ep=1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if !strings.Contains(w.Body.String(),
		`<newznab:attr name="tvdbid" value="71756"`) {
		t.Errorf("case-insensitive rehydration missed.\nbody:\n%s", w.Body.String())
	}
}

// TestHandleTVSearch_TVDBIDNoRehydrationWhenUnknown verifies we do not
// invent a tvdbid when the store has no mapping for the requested show.
func TestHandleTVSearch_TVDBIDNoRehydrationWhenUnknown(t *testing.T) {
	payload := `{
		"new_search": {
			"results": [
				{"id": "b039d07m", "type": "episode", "title": "Doctor Who", "subtitle": "Series 14: 3. Boom", "release_date": "2024-05-18", "parent_position": 3}
			]
		}
	}`
	h, _ := newHandlerWithBBCAndStore(t, payload)
	// deliberately no PutSeriesMapping

	req := httptest.NewRequest("GET",
		"/newznab/api?t=tvsearch&q=Doctor+Who&tvdbid=&season=14&ep=3", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if strings.Contains(w.Body.String(), `<newznab:attr name="tvdbid"`) {
		t.Errorf("tvdbid attr emitted without a store mapping.\nbody:\n%s",
			w.Body.String())
	}
}

// TestHandleTVSearch_TVDBIDRequestParamWinsOverStore covers the shape
// where Sonarr sends a tvdbid explicitly -- the store lookup must not
// override it, even if the store happens to have a different entry
// for the same show name.
func TestHandleTVSearch_TVDBIDRequestParamWinsOverStore(t *testing.T) {
	payload := `{
		"new_search": {
			"results": [
				{"id": "b039d07m", "type": "episode", "title": "Doctor Who", "subtitle": "Series 14: 3. Boom", "release_date": "2024-05-18", "parent_position": 3}
			]
		}
	}`
	h, _ := newHandlerWithBBCAndStore(t, payload)
	// Store has a STALE mapping (wrong tvdbid for this name).
	h.store.PutSeriesMapping(&store.SeriesMapping{
		TVDBId: "99999", ShowName: "Doctor Who", Year: 2005,
	})

	// Request supplies the correct tvdbid.
	req := httptest.NewRequest("GET",
		"/newznab/api?t=tvsearch&q=Doctor+Who&tvdbid=78804&season=14&ep=3", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `<newznab:attr name="tvdbid" value="78804"`) {
		t.Errorf("expected request tvdbid=78804 to win, body:\n%s", body)
	}
	if strings.Contains(body, `value="99999"`) {
		t.Errorf("store tvdbid=99999 leaked over request tvdbid=78804, body:\n%s", body)
	}
}

// GitHub #32. BBC long-runners with no "Series N" prefix in the subtitle
// (Casualty: "Learning Curve Episode 3", One Piece 1999: "Episode 47 - ...")
// parse to Series=0 + EpisodeNum=N via parseSubtitleNumbers. Sonarr sends
// integer season/ep filters from the TVDB record; matchesSearchFilter then
// rejects every item because prog.Series=0 != filterSeason=1.
//
// Fix: promote Series=1 at the identity-resolution boundary
// (iblResultToProgramme) when Series=0 but EpisodeNum>0. Position alone
// must NOT trigger promotion -- one-offs and specials have Position>0 but
// no real episode numbering.
func TestIblResultToProgramme_PromotesSeriesOneForPositionBasedShows(t *testing.T) {
	cases := []struct {
		name       string
		in         bbc.IBLResult
		wantSeries int
		wantEpNum  int
	}{
		{"parsed episode, no series -> promote to series 1",
			bbc.IBLResult{PID: "m001", Series: 0, EpisodeNum: 3}, 1, 3},
		{"large parsed episode, no series -> still series 1",
			bbc.IBLResult{PID: "m002", Series: 0, EpisodeNum: 147}, 1, 147},
		{"explicit series, parsed episode -> leave series alone",
			bbc.IBLResult{PID: "m003", Series: 2, EpisodeNum: 3}, 2, 3},
		{"topical (no series, no episode, has airdate) -> leave at 0",
			bbc.IBLResult{PID: "m004", Series: 0, EpisodeNum: 0, AirDate: "2026-04-01"}, 0, 0},
		{"position-only, no parsed episode -> do NOT promote on Position alone",
			bbc.IBLResult{PID: "m005", Series: 0, EpisodeNum: 0, Position: 5}, 0, 0},
		{"series present but no episode -> leave as-is",
			bbc.IBLResult{PID: "m006", Series: 3, EpisodeNum: 0}, 3, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IBLResultToProgramme(tc.in)
			if got.Series != tc.wantSeries {
				t.Errorf("Series = %d, want %d (input %+v)", got.Series, tc.wantSeries, tc.in)
			}
			if got.EpisodeNum != tc.wantEpNum {
				t.Errorf("EpisodeNum = %d, want %d (input %+v)", got.EpisodeNum, tc.wantEpNum, tc.in)
			}
		})
	}
}

// Regression for GitHub #32. Full path: a Casualty-shaped item (Series=0
// from subtitle parsing, EpisodeNum=3 from "Episode 3") must survive
// matchesSearchFilter when Sonarr queries season=1&ep=3.
func TestMatchesSearchFilter_CasualtyPositionBasedAcceptsSeasonOne(t *testing.T) {
	r := bbc.IBLResult{
		PID:        "casualty-s01e03",
		Title:      "Casualty",
		Subtitle:   "Learning Curve Episode 3",
		Series:     0,
		EpisodeNum: 3,
		AirDate:    "1986-09-20",
	}
	prog := IBLResultToProgramme(r)
	if !matchesSearchFilter(prog, "Casualty", "", 1, 3) {
		t.Errorf("matchesSearchFilter rejected Casualty position-based item; prog=%+v", prog)
	}
	// And a mismatching episode should still be rejected.
	if matchesSearchFilter(prog, "Casualty", "", 1, 4) {
		t.Errorf("matchesSearchFilter wrongly accepted ep=4 for Casualty E3 item; prog=%+v", prog)
	}
}

func TestHandleSearch_RSSSyncUsesBrowseFresh(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/groups/m001bm54/episodes":
			w.Write([]byte(`{"group_episodes":{"elements":[{"id":"trend1","type":"episode","title":"FromTrending","subtitle":"Series 1: 1. Pilot"}]}}`))
		case "/groups/popular/episodes":
			w.Write([]byte(`{"group_episodes":{"elements":[{"id":"pop1","type":"episode","title":"FromPopular","subtitle":"Series 1: 2. Episode"}]}}`))
		case "/new-search":
			w.Write([]byte(`{"new_search":{"results":[{"id":"search1","type":"episode","title":"FromSearch","subtitle":"Series 1: 3. Episode"}]}}`))
		}
	}))
	defer srv.Close()

	h := newHandlerWithBBC(t, "")
	h.ibl.BaseURL = srv.URL

	req := httptest.NewRequest("GET", "/newznab/api?t=search", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	body := rr.Body.String()
	for _, want := range []string{"FromTrending", "FromPopular", "FromSearch"} {
		if !strings.Contains(body, want) {
			t.Errorf("RSS body missing %q; body:\n%s", want, body)
		}
	}
}

func TestHandleTVSearch_RSSSyncUsesBrowseFresh(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/groups/m001bm54/episodes":
			w.Write([]byte(`{"group_episodes":{"elements":[{"id":"trend1","type":"episode","title":"FromTrending","subtitle":"Series 1: 1. Pilot"}]}}`))
		case "/groups/popular/episodes":
			w.Write([]byte(`{"group_episodes":{"elements":[{"id":"pop1","type":"episode","title":"FromPopular","subtitle":"Series 1: 2. Episode"}]}}`))
		case "/new-search":
			w.Write([]byte(`{"new_search":{"results":[{"id":"search1","type":"episode","title":"FromSearch","subtitle":"Series 1: 3. Episode"}]}}`))
		}
	}))
	defer srv.Close()

	h := newHandlerWithBBC(t, "")
	h.ibl.BaseURL = srv.URL

	// t=tvsearch with no q and no tvdbid is the RSS-sync case.
	req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	body := rr.Body.String()
	for _, want := range []string{"FromTrending", "FromPopular", "FromSearch"} {
		if !strings.Contains(body, want) {
			t.Errorf("RSS body missing %q; body:\n%s", want, body)
		}
	}
}

func TestWildcardRoute_PerPIDQualityCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/groups/m001bm54/episodes":
			w.Write([]byte(`{"group_episodes":{"elements":[{"id":"a","type":"episode","title":"A","subtitle":"Series 1: 1. Pilot"}]}}`))
		case "/groups/popular/episodes":
			w.Write([]byte(`{"group_episodes":{"elements":[{"id":"b","type":"episode","title":"B","subtitle":"Series 1: 2. Two"}]}}`))
		case "/new-search":
			w.Write([]byte(`{"new_search":{"results":[{"id":"c","type":"episode","title":"C","subtitle":"Series 1: 3. Three"}]}}`))
		}
	}))
	defer srv.Close()

	heights := []int{1080, 720, 540, 396}
	prober := &mockProber{results: map[string][]int{
		"a": heights,
		"b": heights,
		"c": heights,
		"x": heights,
	}}
	h := newHandlerWithBBCProber(t, "", prober)
	h.ibl.BaseURL = srv.URL

	// Wildcard route: 3 PIDs × ≤ 2 qualities = ≤ 6 items.
	req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	wildcardItems := strings.Count(rr.Body.String(), "<item>")
	if wildcardItems != 6 {
		t.Errorf("wildcard items = %d, want 6 (3 PIDs × 2 qualities)", wildcardItems)
	}

	// Targeted route with q != "": 1 PID (whichever Search returns) × 4 qualities.
	// Reset srv to return a Search payload the targeted path will use.
	srvTargeted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"new_search":{"results":[{"id":"x","type":"episode","title":"X","subtitle":"Series 1: 1. One"}]}}`))
	}))
	defer srvTargeted.Close()
	h.ibl.BaseURL = srvTargeted.URL

	req = httptest.NewRequest("GET", "/newznab/api?t=tvsearch&q=X", nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	targetedItems := strings.Count(rr.Body.String(), "<item>")
	if targetedItems != 4 {
		t.Errorf("targeted items = %d, want 4 (1 PID × 4 qualities)", targetedItems)
	}
}

func TestWildcardRoute_ProbeDeadlineEnforced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/groups/m001bm54/episodes":
			w.Write([]byte(`{"group_episodes":{"elements":[{"id":"a","type":"episode","title":"A","subtitle":"Series 1: 1. P"}]}}`))
		case "/groups/popular/episodes":
			w.Write([]byte(`{"group_episodes":{"elements":[]}}`))
		case "/new-search":
			w.Write([]byte(`{"new_search":{"results":[]}}`))
		}
	}))
	defer srv.Close()

	h := newHandlerWithBBCProber(t, "", &hangingProber{})
	h.ibl.BaseURL = srv.URL

	req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch", nil)
	rr := httptest.NewRecorder()

	start := time.Now()
	h.ServeHTTP(rr, req)
	elapsed := time.Since(start)

	if elapsed > 6*time.Second {
		t.Errorf("wildcard route did not honour probe deadline; elapsed=%v want ≤ 6s", elapsed)
	}
	body := rr.Body.String()
	// Even with the prober hanging, safe-fallback heights should produce
	// items for the merged PIDs.
	if !strings.Contains(body, "720p") && !strings.Contains(body, "540p") {
		t.Errorf("expected safe-fallback heights in body when prober hangs; body:\n%s", body)
	}
}

func TestWildcardRoute_TotalItemCeiling(t *testing.T) {
	// Build 60 unique PIDs distributed across the three pools so the
	// browseCapPIDs=50 cap clips 10. With per-PID quality cap of 2,
	// the resulting RSS body should contain exactly 100 items.
	mkElements := func(prefix string, n int) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(fmt.Sprintf(`{"id":"%s_%d","type":"episode","title":"%s%d","subtitle":"Series 1: %d. E"}`, prefix, i, prefix, i, i+1))
		}
		return b.String()
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/groups/m001bm54/episodes":
			w.Write([]byte(`{"group_episodes":{"elements":[` + mkElements("trend", 30) + `]}}`))
		case "/groups/popular/episodes":
			w.Write([]byte(`{"group_episodes":{"elements":[` + mkElements("pop", 20) + `]}}`))
		case "/new-search":
			w.Write([]byte(`{"new_search":{"results":[` + mkElements("search", 10) + `]}}`))
		}
	}))
	defer srv.Close()

	heights := []int{1080, 720, 540, 396}
	results := make(map[string][]int, 60)
	for _, prefix := range []string{"trend", "pop", "search"} {
		for i := 0; i < 30; i++ {
			results[fmt.Sprintf("%s_%d", prefix, i)] = heights
		}
	}
	prober := &mockProber{results: results}
	h := newHandlerWithBBCProber(t, "", prober)
	h.ibl.BaseURL = srv.URL

	req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	body := rr.Body.String()
	gotItems := strings.Count(body, "<item>")
	if gotItems > 100 {
		t.Errorf("RSS items = %d, want ≤ 100 (50 PIDs × 2 qualities)", gotItems)
	}
	if gotItems != 100 {
		t.Errorf("RSS items = %d, want exactly 100", gotItems)
	}
	// First emitted item should come from the m001bm54 'trend' pool,
	// locking down priority ordering after cap-clipping. Skip the
	// channel-level <title>; find the first <item> then look at its
	// title. (GUIDs are base64-encoded so substring matching there
	// doesn't work.)
	itemStart := strings.Index(body, "<item>")
	if itemStart == -1 {
		t.Fatalf("no <item> elements in RSS body")
	}
	titleOpen := strings.Index(body[itemStart:], "<title>")
	titleClose := strings.Index(body[itemStart:], "</title>")
	if titleOpen == -1 || titleClose == -1 {
		t.Fatalf("first item has no <title> element")
	}
	firstTitle := body[itemStart+titleOpen : itemStart+titleClose]
	if !strings.Contains(firstTitle, "trend") {
		t.Errorf("first item title = %q, want a 'trend' program (m001bm54 priority)", firstTitle)
	}
}

func TestHandleTVSearch_TargetedDoesNotBrowse(t *testing.T) {
	var groupHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/groups/") {
			groupHits++
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(`{"new_search":{"results":[{"id":"x","type":"episode","title":"X","subtitle":"Series 1: 1. E"}]}}`))
	}))
	defer srv.Close()

	h := newHandlerWithBBC(t, "")
	h.ibl.BaseURL = srv.URL

	// Per-show search.
	req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch&q=Apprentice", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if groupHits != 0 {
		t.Errorf("per-show tvsearch hit /groups/ %d times, want 0", groupHits)
	}

	// tvdbid-resolved search.
	groupHits = 0
	req = httptest.NewRequest("GET", "/newznab/api?t=tvsearch&tvdbid=12345&season=1&ep=1", nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if groupHits != 0 {
		t.Errorf("tvdbid tvsearch hit /groups/ %d times, want 0", groupHits)
	}
}

func TestHandleSearch_TargetedDoesNotBrowse(t *testing.T) {
	var groupHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/groups/") {
			groupHits++
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(`{"new_search":{"results":[]}}`))
	}))
	defer srv.Close()

	h := newHandlerWithBBC(t, "")
	h.ibl.BaseURL = srv.URL

	req := httptest.NewRequest("GET", "/newznab/api?t=search&q=Foo", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if groupHits != 0 {
		t.Errorf("targeted search hit /groups/ %d times, want 0", groupHits)
	}
}

// TestSearch_ConfiguredQualityCeiling_FiltersHigher exercises issue #28:
// the Default quality config value should act as a ceiling on the heights
// emitted to Sonarr. When config quality is 720p and the prober reports
// {1080,720,540}, the RSS body must drop 1080p so Sonarr cannot request it.
func TestSearch_ConfiguredQualityCeiling_FiltersHigher(t *testing.T) {
	prober := &mockProber{results: map[string][]int{"m002ttg5": {1080, 720, 540}}}
	h, st := newHandlerWithBBCProberAndStore(t, eastendersOneEpisodePayload, prober)
	if err := st.SetConfig("quality", "720p"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	req := httptest.NewRequest("GET", "/newznab/api?t=search&q=eastenders", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	qualities := itemQualities(t, w.Body.String())
	if countQuality(qualities, "1080p") != 0 {
		t.Fatalf("expected no 1080p items (capped at 720p), got %v", qualities)
	}
	if countQuality(qualities, "720p") != 1 || countQuality(qualities, "540p") != 1 {
		t.Fatalf("expected exactly [720p 540p], got %v", qualities)
	}
}

// TestSearch_ConfiguredQualityAny_NoCeiling verifies the explicit
// opt-out value: when config quality is "any", every probed height is
// emitted just like before the ceiling existed.
func TestSearch_ConfiguredQualityAny_NoCeiling(t *testing.T) {
	prober := &mockProber{results: map[string][]int{"m002ttg5": {1080, 720, 540}}}
	h, st := newHandlerWithBBCProberAndStore(t, eastendersOneEpisodePayload, prober)
	if err := st.SetConfig("quality", "any"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	req := httptest.NewRequest("GET", "/newznab/api?t=search&q=eastenders", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	qualities := itemQualities(t, w.Body.String())
	if countQuality(qualities, "1080p") == 0 {
		t.Fatalf("expected 1080p when ceiling is 'any', got %v", qualities)
	}
}

// TestSearch_QualityCeilingAppliesToFallback verifies the no-probe
// fallback path (qualities = [720p,540p]) is also clamped: a 540p
// ceiling must drop the 720p fallback item.
func TestSearch_QualityCeilingAppliesToFallback(t *testing.T) {
	prober := &mockProber{results: map[string][]int{"m002ttg5": nil}}
	h, st := newHandlerWithBBCProberAndStore(t, eastendersOneEpisodePayload, prober)
	if err := st.SetConfig("quality", "540p"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	req := httptest.NewRequest("GET", "/newznab/api?t=search&q=eastenders", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	qualities := itemQualities(t, w.Body.String())
	if countQuality(qualities, "720p") != 0 {
		t.Fatalf("expected no 720p fallback item (capped at 540p), got %v", qualities)
	}
	if countQuality(qualities, "540p") != 1 {
		t.Fatalf("expected single 540p item, got %v", qualities)
	}
}

// TestServeHTTP_AuthRequired verifies that any operation other than
// t=caps refuses requests without a valid apikey when the store has
// one seeded.
func TestServeHTTP_AuthRequired(t *testing.T) {
	h, st := newHandlerWithBBCAndStore(t, eastendersOneEpisodePayload)
	if err := st.SetConfig("api_key", "test-key-123"); err != nil {
		t.Fatalf("seed api_key: %v", err)
	}

	cases := []struct {
		name       string
		url        string
		header     string
		wantStatus int
	}{
		{"caps without key allowed", "/newznab/api?t=caps", "", http.StatusOK},
		{"search without key denied", "/newznab/api?t=search&q=foo", "", http.StatusUnauthorized},
		{"tvsearch without key denied", "/newznab/api?t=tvsearch&q=foo", "", http.StatusUnauthorized},
		{"get without key denied", "/newznab/api?t=get&id=abc", "", http.StatusUnauthorized},
		{"search wrong key denied", "/newznab/api?t=search&q=foo&apikey=wrong", "", http.StatusUnauthorized},
		{"search right key allowed", "/newznab/api?t=search&q=foo&apikey=test-key-123", "", http.StatusOK},
		{"search bearer wrong denied", "/newznab/api?t=search&q=foo", "Bearer nope", http.StatusUnauthorized},
		{"search bearer right allowed", "/newznab/api?t=search&q=foo", "Bearer test-key-123", http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.url, nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d. body: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

// TestServeHTTP_AuthError100Body verifies the Newznab-style error
// envelope on auth failure: error code 100 with description.
func TestServeHTTP_AuthError100Body(t *testing.T) {
	h, st := newHandlerWithBBCAndStore(t, eastendersOneEpisodePayload)
	st.SetConfig("api_key", "test-key-abc")

	req := httptest.NewRequest("GET", "/newznab/api?t=search&q=anything", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `code="100"`) {
		t.Errorf("body missing error code 100: %s", body)
	}
	if !strings.Contains(body, "Invalid API Key") {
		t.Errorf("body missing Invalid API Key description: %s", body)
	}
}

// Regression for the "Sonarr finds N matches but downloads zero" bug:
// every link/guid/enclosure in the feed must carry the apikey inline,
// because Sonarr fetches the grab URL straight from the feed without
// re-applying its configured key.
func TestFeedURLsIncludeAPIKey(t *testing.T) {
	h, st := newHandlerWithBBCProberAndStore(t, eastendersOneEpisodePayload, nil)
	const seededKey = "test-secret-key"
	if err := st.SetConfig("api_key", seededKey); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	req := httptest.NewRequest("GET",
		"/newznab/api?t=tvsearch&q=eastenders&season=2026&ep=04%2F06&apikey="+seededKey, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<item>") {
		t.Fatalf("expected at least one <item>, got:\n%s", body)
	}

	wantSuffix := "&amp;apikey=" + seededKey
	for _, ln := range strings.Split(body, "\n") {
		for _, marker := range []string{"<guid", "<link>", "<enclosure"} {
			if strings.Contains(ln, marker) && !strings.Contains(ln, wantSuffix) {
				t.Errorf("%s line missing apikey suffix:\n  %s", marker, strings.TrimSpace(ln))
			}
		}
	}
}
