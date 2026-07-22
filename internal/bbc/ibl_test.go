package bbc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestIBLSearch(t *testing.T) {
	fixture, err := os.ReadFile("testdata/ibl_search.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			t.Error("missing q param")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture)
	}))
	defer srv.Close()

	ibl := NewIBL(NewClient())
	ibl.BaseURL = srv.URL

	results, err := ibl.Search("doctor who", 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1", len(results))
	}

	r := results[0]
	if r.PID != "b039d07m" {
		t.Errorf("PID = %q", r.PID)
	}
	if r.Title != "The Unquiet Dead" {
		t.Errorf("Title = %q", r.Title)
	}
	if r.Series != 1 {
		t.Errorf("Series = %d", r.Series)
	}
	if r.EpisodeNum != 3 {
		t.Errorf("EpisodeNum = %d", r.EpisodeNum)
	}
	if r.Channel != "BBC One" {
		t.Errorf("Channel = %q", r.Channel)
	}
}

func TestListEpisodesNormalisesLooseAirDate(t *testing.T) {
	// BBC IBL returns release_date in human format ("6 Apr 2026") for some
	// shows like EastEnders, alongside ISO format ("2026-04-09") for others.
	// IBLResult.AirDate must always be canonical YYYY-MM-DD so downstream code
	// (filters, title generation, pubDate) can rely on a single format.
	payload := `{
		"programme_episodes": {
			"elements": [
				{"id": "ep1", "type": "episode", "title": "EastEnders", "subtitle": "06/04/2026", "release_date": "6 Apr 2026", "parent_position": 7307},
				{"id": "ep2", "type": "episode", "title": "EastEnders", "subtitle": "07/04/2026", "release_date": "2026-04-07", "parent_position": 7308}
			],
			"page": 1, "per_page": 2, "count": 2
		}
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(payload))
	}))
	defer srv.Close()

	ibl := NewIBL(NewClient())
	ibl.BaseURL = srv.URL

	results, err := ibl.ListEpisodes("b006m86d")
	if err != nil {
		t.Fatalf("ListEpisodes: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len = %d, want 2", len(results))
	}
	if got := results[0].AirDate; got != "2026-04-06" {
		t.Errorf("loose date: AirDate = %q, want %q", got, "2026-04-06")
	}
	if got := results[1].AirDate; got != "2026-04-07" {
		t.Errorf("ISO date: AirDate = %q, want %q", got, "2026-04-07")
	}
}

func TestSearchNormalisesLooseAirDate(t *testing.T) {
	payload := `{
		"new_search": {
			"results": [
				{"id": "ep1", "type": "episode", "title": "EastEnders", "subtitle": "06/04/2026", "release_date": "6 Apr 2026", "parent_position": 7307}
			]
		}
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(payload))
	}))
	defer srv.Close()

	ibl := NewIBL(NewClient())
	ibl.BaseURL = srv.URL

	results, err := ibl.Search("eastenders", 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1", len(results))
	}
	if got := results[0].AirDate; got != "2026-04-06" {
		t.Errorf("AirDate = %q, want %q", got, "2026-04-06")
	}
}

func TestParseSubtitleNumbers(t *testing.T) {
	// BBC's iPlayer episode metadata uses two distinct subtitle layouts:
	//   - "Series N: M. Title"  (numbered list)        e.g. Drugs Map of Britain
	//   - "Series N: Episode M" (named, no list index) e.g. Little Britain
	// Both must produce the same (series, episode) pair so that the newznab
	// season/episode filter accepts the release for Sonarr. Issue #13.
	cases := []struct {
		subtitle string
		series   int
		episode  int
	}{
		{"Series 1: Episode 1", 1, 1},
		{"Series 1: Episode 2", 1, 2},
		{"Series 1: Episode 12", 1, 12},
		{"Series 11: Episode 4", 11, 4},
		{"Series 1: episode 5", 1, 5}, // case-insensitive
		{"Series 1: 1. Nitrous Oxide", 1, 1},
		{"Series 4: 12. Christmas Special", 4, 12},
		{"Series 1: 1", 1, 1},
		{"Cyfres 2: Pennod 4", 2, 4}, // Welsh
		{"Series 1: Pilot", 1, 0},    // unnumbered episode -> falls through to other tiers
		{"Series 1", 1, 0},           // no episode part
		{"Episode 1", 0, 0},          // no series part
	}
	for _, tc := range cases {
		s, e := parseSubtitleNumbers(tc.subtitle)
		if s != tc.series || e != tc.episode {
			t.Errorf("parseSubtitleNumbers(%q) = (%d, %d), want (%d, %d)",
				tc.subtitle, s, e, tc.series, tc.episode)
		}
	}
}

func TestListEpisodesPagination(t *testing.T) {
	page1 := `{
		"programme_episodes": {
			"elements": [
				{"id": "ep1", "type": "episode", "title": "Show", "subtitle": "1. First"}
			],
			"page": 1,
			"per_page": 1,
			"count": 2
		}
	}`
	page2 := `{
		"programme_episodes": {
			"elements": [
				{"id": "ep2", "type": "episode", "title": "Show", "subtitle": "2. Second"}
			],
			"page": 2,
			"per_page": 1,
			"count": 2
		}
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pg := r.URL.Query().Get("page")
		w.Header().Set("Content-Type", "application/json")
		if pg == "2" {
			w.Write([]byte(page2))
		} else {
			w.Write([]byte(page1))
		}
	}))
	defer srv.Close()

	ibl := NewIBL(NewClient())
	ibl.BaseURL = srv.URL

	results, err := ibl.ListEpisodes("brand_pid")
	if err != nil {
		t.Fatalf("ListEpisodes: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2 (pagination should fetch both pages)", len(results))
	}
	if results[0].PID != "ep1" || results[1].PID != "ep2" {
		t.Errorf("unexpected PIDs: %s, %s", results[0].PID, results[1].PID)
	}
}

func TestParseSubtitleNumbers_CompositeDateDoesNotExtractEpisode(t *testing.T) {
	// "2025/26: 22/03/2026" is BBC's Match of the Day composite format.
	// Without the guard, the "22" at the start of "22/03/2026" is
	// extracted as EpisodeNum. With the guard, it is correctly skipped
	// and EpisodeNum stays 0. See issue #15.
	series, episode := parseSubtitleNumbers("2025/26: 22/03/2026")
	if series != 0 || episode != 0 {
		t.Errorf("parseSubtitleNumbers(\"2025/26: 22/03/2026\") = (%d, %d), want (0, 0)", series, episode)
	}
}

func TestParseSubtitleNumbers_BareDateReturnsZero(t *testing.T) {
	// Bare dates have no ": " so the existing split-on-colon logic
	// already skips episode extraction. This regression test locks in
	// that pre-existing behaviour so the new guard does not accidentally
	// regress it.
	series, episode := parseSubtitleNumbers("22/03/2026")
	if series != 0 || episode != 0 {
		t.Errorf("parseSubtitleNumbers(\"22/03/2026\") = (%d, %d), want (0, 0)", series, episode)
	}
}

func TestSearchCtx_RespectsCancellation(t *testing.T) {
	// A server that hangs forever should be aborted by ctx cancellation.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	ibl := NewIBL(NewClient())
	ibl.BaseURL = srv.URL

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := ibl.SearchCtx(ctx, "anything", 1)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected cancellation error, got nil")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("SearchCtx did not honour ctx; elapsed=%v want <2s", elapsed)
	}
}

func TestListEpisodesCtx_RespectsCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	ibl := NewIBL(NewClient())
	ibl.BaseURL = srv.URL

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := ibl.ListEpisodesCtx(ctx, "b0071b63")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected cancellation error, got nil")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("ListEpisodesCtx did not honour ctx; elapsed=%v want <2s", elapsed)
	}
}

func TestGroupEpisodes_ParsesElements(t *testing.T) {
	fixture, err := os.ReadFile("testdata/groups_popular.json")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture)
	}))
	defer srv.Close()

	ibl := NewIBL(NewClient())
	ibl.BaseURL = srv.URL

	results, err := ibl.GroupEpisodes(context.Background(), "popular", 3)
	if err != nil {
		t.Fatalf("GroupEpisodes: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 element parsed, got 0")
	}
	for i, r := range results {
		if r.PID == "" {
			t.Errorf("element %d: empty PID", i)
		}
		if r.Title == "" {
			t.Errorf("element %d: empty title", i)
		}
	}
	hasDuration := false
	for _, r := range results {
		if r.Duration > 0 {
			hasDuration = true
			break
		}
	}
	if !hasDuration {
		t.Error("no element had Duration > 0; iblElementToResult did not parse versions[0].duration")
	}
}

func TestIBLAvailabilityPrefersDownloadableVersion(t *testing.T) {
	// #47: availability.start can differ between versions (e.g. audio-described
	// vs original). pubDate must reflect the version iParr actually grabs, so
	// prefer the download==true version's availability over an earlier one.
	const payload = `{"group_episodes":{"elements":[
		{"id":"pid-multi","type":"episode","title":"Multi Version","subtitle":"Series 1: Episode 1","release_date":"20 May 2026","versions":[
			{"download":false,"duration":{"value":"PT30M0.000S"},"availability":{"start":"2026-05-20T06:00:00Z"}},
			{"download":true,"duration":{"value":"PT30M0.000S"},"availability":{"start":"2026-05-26T17:45:00Z"}}
		]},
		{"id":"pid-none","type":"episode","title":"No Availability","subtitle":"Series 1: Episode 2","release_date":"21 May 2026","versions":[
			{"download":true,"duration":{"value":"PT30M0.000S"}}
		]}
	]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(payload))
	}))
	defer srv.Close()

	ibl := NewIBL(NewClient())
	ibl.BaseURL = srv.URL

	results, err := ibl.GroupEpisodes(context.Background(), "popular", 10)
	if err != nil {
		t.Fatalf("GroupEpisodes: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	wantDownloadable := time.Date(2026, 5, 26, 17, 45, 0, 0, time.UTC)
	if !results[0].Available.Equal(wantDownloadable) {
		t.Errorf("Available = %v, want %v (download==true version, not the earlier audio-described one)",
			results[0].Available, wantDownloadable)
	}
	if !results[1].Available.IsZero() {
		t.Errorf("element without availability: Available = %v, want zero", results[1].Available)
	}
}

func TestGroupEpisodes_RequestShape(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		w.Write([]byte(`{"group_episodes":{"page":1,"per_page":3,"count":0,"elements":[]}}`))
	}))
	defer srv.Close()

	ibl := NewIBL(NewClient())
	ibl.BaseURL = srv.URL

	if _, err := ibl.GroupEpisodes(context.Background(), "m001bm54", 50); err != nil {
		t.Fatalf("GroupEpisodes: %v", err)
	}
	if got == nil {
		t.Fatal("server never received a request")
	}
	if want := "/groups/m001bm54/episodes"; got.URL.Path != want {
		t.Errorf("path = %q, want %q", got.URL.Path, want)
	}
	if want := "50"; got.URL.Query().Get("per_page") != want {
		t.Errorf("per_page = %q, want %q", got.URL.Query().Get("per_page"), want)
	}
	if q := got.URL.Query(); len(q) != 1 {
		t.Errorf("unexpected query params: %v", q)
	}
}

func TestGroupEpisodes_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ibl := NewIBL(NewClient())
	ibl.BaseURL = srv.URL

	_, err := ibl.GroupEpisodes(context.Background(), "popular", 3)
	if err == nil {
		t.Fatal("expected error for 500, got nil")
	}
}

func TestBrowseFresh_MergesAllThree(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/new-search":
			w.Write([]byte(`{"new_search":{"results":[{"id":"pid_search_1","type":"episode","title":"S1"},{"id":"pid_search_2","type":"episode","title":"S2"}]}}`))
		case "/groups/popular/episodes":
			w.Write([]byte(`{"group_episodes":{"elements":[{"id":"pid_pop_1","type":"episode","title":"P1"},{"id":"pid_pop_2","type":"episode","title":"P2"}]}}`))
		case "/groups/m001bm54/episodes":
			w.Write([]byte(`{"group_episodes":{"elements":[{"id":"pid_trend_1","type":"episode","title":"T1"}]}}`))
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	ibl := NewIBL(NewClient())
	ibl.BaseURL = srv.URL

	results, err := ibl.BrowseFresh(context.Background())
	if err != nil {
		t.Fatalf("BrowseFresh: %v", err)
	}
	if got, want := len(results), 5; got != want {
		t.Errorf("merged length = %d, want %d", got, want)
	}
}

func TestBrowseFresh_DedupesByPID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/new-search":
			w.Write([]byte(`{"new_search":{"results":[{"id":"pid_dup","type":"episode","title":"FromSearch"}]}}`))
		case "/groups/popular/episodes":
			w.Write([]byte(`{"group_episodes":{"elements":[{"id":"pid_dup","type":"episode","title":"FromPopular"}]}}`))
		case "/groups/m001bm54/episodes":
			w.Write([]byte(`{"group_episodes":{"elements":[{"id":"pid_dup","type":"episode","title":"FromTrending"}]}}`))
		}
	}))
	defer srv.Close()

	ibl := NewIBL(NewClient())
	ibl.BaseURL = srv.URL

	results, err := ibl.BrowseFresh(context.Background())
	if err != nil {
		t.Fatalf("BrowseFresh: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 deduped result, got %d", len(results))
	}
	// Priority order: m001bm54 first, so the curated metadata wins.
	if results[0].Title != "FromTrending" {
		t.Errorf("dedupe winner Title = %q, want %q (m001bm54 should win)", results[0].Title, "FromTrending")
	}
}

func TestBrowseFresh_OrderingPriority(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/new-search":
			w.Write([]byte(`{"new_search":{"results":[{"id":"S","type":"episode","title":"S"}]}}`))
		case "/groups/popular/episodes":
			w.Write([]byte(`{"group_episodes":{"elements":[{"id":"P","type":"episode","title":"P"}]}}`))
		case "/groups/m001bm54/episodes":
			w.Write([]byte(`{"group_episodes":{"elements":[{"id":"T","type":"episode","title":"T"}]}}`))
		}
	}))
	defer srv.Close()

	ibl := NewIBL(NewClient())
	ibl.BaseURL = srv.URL

	results, err := ibl.BrowseFresh(context.Background())
	if err != nil {
		t.Fatalf("BrowseFresh: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	gotOrder := []string{results[0].PID, results[1].PID, results[2].PID}
	wantOrder := []string{"T", "P", "S"}
	if !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Errorf("ordering = %v, want %v", gotOrder, wantOrder)
	}
}

func TestBrowseFresh_FailsSoftPerPool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/groups/m001bm54/episodes":
			w.WriteHeader(http.StatusInternalServerError)
		case "/new-search":
			w.Write([]byte(`{"new_search":{"results":[{"id":"S","type":"episode","title":"S"}]}}`))
		case "/groups/popular/episodes":
			w.Write([]byte(`{"group_episodes":{"elements":[{"id":"P","type":"episode","title":"P"}]}}`))
		}
	}))
	defer srv.Close()

	ibl := NewIBL(NewClient())
	ibl.BaseURL = srv.URL

	results, err := ibl.BrowseFresh(context.Background())
	if err != nil {
		t.Fatalf("BrowseFresh returned error despite 2/3 success: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results from surviving pools, got %d", len(results))
	}
}

func TestBrowseFresh_AllFailReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ibl := NewIBL(NewClient())
	ibl.BaseURL = srv.URL

	_, err := ibl.BrowseFresh(context.Background())
	if err == nil {
		t.Fatal("expected error when all 3 pools fail, got nil")
	}
}

func TestBrowseFresh_RespectsContextTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// All three pools hang.
		<-r.Context().Done()
	}))
	defer srv.Close()

	ibl := NewIBL(NewClient())
	ibl.BaseURL = srv.URL

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _ = ibl.BrowseFresh(ctx)
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Errorf("BrowseFresh did not honour ctx; elapsed=%v want <2s", elapsed)
	}
}

func TestFilmsCtx(t *testing.T) {
	const filmsJSON = `{"category_programmes": {"elements": [
  {"id": "m001fyf7", "type": "programme_large", "title": "2003",
   "master_brand": {"titles": {"small": "BBC Two"}},
   "images": {"standard": "https://.../{recipe}/p0djqsjv.jpg"},
   "initial_children": [
     {"id": "m001fyf7", "type": "episode", "title": "2003",
      "release_date": "2021",
      "versions": [{"download": true,
                    "duration": {"value": "PT12M20.560S"},
                    "availability": {"start": "2022-12-05T00:21:36Z"}}]}
   ]}
]}}`

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(filmsJSON))
	}))
	defer srv.Close()

	ibl := NewIBL(NewClient())
	ibl.BaseURL = srv.URL

	results, err := ibl.FilmsCtx(context.Background(), 10)
	if err != nil {
		t.Fatalf("FilmsCtx: %v", err)
	}
	if !strings.Contains(gotPath, "/categories/films/programmes") {
		t.Errorf("request path = %q, want it to contain /categories/films/programmes", gotPath)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1", len(results))
	}
	r := results[0]
	if r.PID != "m001fyf7" {
		t.Errorf("PID = %q, want m001fyf7", r.PID)
	}
	if r.Title != "2003" {
		t.Errorf("Title = %q, want 2003", r.Title)
	}
	if r.Channel != "BBC Two" {
		t.Errorf("Channel = %q, want BBC Two", r.Channel)
	}
	if r.Duration != 740 {
		t.Errorf("Duration = %d, want 740 (PT12M20.560S truncated to seconds)", r.Duration)
	}
	if r.Available.IsZero() {
		t.Error("Available is zero, want non-zero")
	}
}
