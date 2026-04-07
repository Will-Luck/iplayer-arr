package newznab

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

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

// mockProber is a test double for the quality prefetcher. It returns a
// fixed map of PID -> heights (or nil for "probe failed"). Every
// PrefetchPIDs call appends the received probeItems slice to calls
// so tests can assert which PIDs were submitted.
type mockProber struct {
	results map[string][]int
	calls   [][]bbc.ProbeItem
}

func (m *mockProber) PrefetchPIDs(ctx context.Context, items []bbc.ProbeItem) map[string][]int {
	copied := make([]bbc.ProbeItem, len(items))
	copy(copied, items)
	m.calls = append(m.calls, copied)
	out := make(map[string][]int, len(items))
	for _, it := range items {
		if heights, ok := m.results[it.PID]; ok {
			out[it.PID] = heights
		} else {
			out[it.PID] = nil
		}
	}
	return out
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
