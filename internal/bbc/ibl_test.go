package bbc

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
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
