package newznab

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
)

// Gitea #104. iPlayer lists some of a show's own episodes as one-offs
// of their own, titled "Show: Episode" with no subtitle, beside the
// show's brand: b00p3pvl "Doctor Who: Dreamland" is TheTVDB's Doctor Who
// (2005) S00E15. The GitHub #58 rule that keeps companion brands out of
// a search for the show dropped them too, so a search for the episode
// returned nothing.

// gh104FixtureEpisodes reads a Skyhook show fixture's episode list.
func gh104FixtureEpisodes(t *testing.T, path string) fixedEpisodes {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var show struct {
		Episodes []struct {
			SeasonNumber  int    `json:"seasonNumber"`
			EpisodeNumber int    `json:"episodeNumber"`
			Title         string `json:"title"`
			AirDate       string `json:"airDate"`
		} `json:"episodes"`
	}
	if err := json.Unmarshal(b, &show); err != nil {
		t.Fatal(err)
	}
	var eps fixedEpisodes
	for _, e := range show.Episodes {
		eps = append(eps, TVDBEpisode{Season: e.SeasonNumber, Episode: e.EpisodeNumber, Title: e.Title, AirDate: e.AirDate})
	}
	if len(eps) == 0 {
		t.Fatalf("%s has no episodes", path)
	}
	return eps
}

// gh104Search runs one request against the testdata/gh58 iBL fixtures
// with eps as TheTVDB's episode list.
func gh104Search(t *testing.T, searches map[string]string, brands []string, eps EpisodeSource, query string) []gh58Item {
	t.Helper()
	gh58Skyhook(t)
	srv := gh58IBL(t, searches, brands)
	ibl := bbc.NewIBL(bbc.NewClient())
	ibl.BaseURL = srv.URL
	h := NewHandler(ibl, newTestStore(t), nil, nil)
	h.SetEpisodeSource(eps)
	return gh58Serve(t, h, query)
}

// TVDB 78804 "Doctor Who (2005)". iBL returns "Doctor Who: Dreamland"
// (b00p3pvl, 2009-12-05) and "Doctor Who: The Infinite Quest"
// (b007zv0y, 2007-06-30) as episode-type hits that are their own
// top-level programmes. TheTVDB has them as S00E15 (2009-12-24, 19 days
// on) and S00E05 (the same day). The companion "Doctor Who: Unleashed"
// stays out of every search, its episodes being a brand's.
func TestGH104_ParentOneOffTakesTheTVDBNumber(t *testing.T) {
	eps := gh104FixtureEpisodes(t, filepath.Join(gh60Dir, "skyhook_78804.json"))
	search := func(query string) []gh58Item {
		return gh104Search(t, map[string]string{"Doctor Who (2005)": "search_Doctor_Who_2005.json"},
			gh58DoctorWhoBrands, eps, query)
	}
	unleashed := gh58BrandPIDs(t, "m001sx3r")

	for _, tc := range []struct {
		query, pid, title, se string
	}{
		{"t=tvsearch&tvdbid=78804&season=0&ep=15", "b00p3pvl",
			"Doctor.Who.2005.S00E15.Dreamland.720p.WEB-DL.AAC.H264-iParr", "S00E15"},
		{"t=tvsearch&tvdbid=78804&season=0&ep=5", "b007zv0y",
			"Doctor.Who.2005.S00E05.The.Infinite.Quest.720p.WEB-DL.AAC.H264-iParr", "S00E05"},
	} {
		t.Run(tc.se, func(t *testing.T) {
			items := search(tc.query)
			gh58AssertTitle(t, items, tc.pid, tc.title)
			for pid, se := range gh60Numbers(t, items) {
				if se != tc.se {
					t.Errorf("PID %s emitted as %s in a search for %s", pid, se, tc.se)
				}
			}
			for _, it := range items {
				if it.PID == tc.pid && it.TVDBID != "78804" {
					t.Errorf("%s carries tvdbid=%q, want 78804", it.PID, it.TVDBID)
				}
			}
			gh58AssertNone(t, items, unleashed, "Doctor Who: Unleashed")
		})
	}

	items := search("t=tvsearch&tvdbid=78804&season=0")
	gh60Check(t, gh60Numbers(t, items), map[string]string{"b00p3pvl": "S00E15", "b007zv0y": "S00E05"})
	gh58AssertNone(t, items, unleashed, "Doctor Who: Unleashed")
}

// TVDB 80435 "Gavin & Stacey" lists m0026d9p "Gavin & Stacey: A Fond
// Farewell" under its whole iPlayer title as S00E18, aired the same day
// (2025-01-01). The three episodes are TheTVDB's, from Skyhook on
// 2026-10-25. TestGH58_ParentOneOffSpecialDroppedByDesign is the same
// search without TheTVDB's list.
func TestGH104_ParentOneOffUnderTheShowsFullTitle(t *testing.T) {
	eps := fixedEpisodes{
		{Season: 0, Episode: 17, Title: "Gavin & Stacey: The Finale", AirDate: "2024-12-25"},
		{Season: 0, Episode: 18, Title: "Gavin & Stacey: A Fond Farewell", AirDate: "2025-01-01"},
		{Season: 0, Episode: 19, Title: "The Gavin & Stacey Experience", AirDate: "2024-12-13"},
	}
	items := gh104Search(t, map[string]string{"Gavin & Stacey": "search_Gavin_and_Stacey.json"},
		[]string{"b007nf70"}, eps, "t=tvsearch&tvdbid=80435&season=00&ep=18")
	gh58AssertTitle(t, items, "m0026d9p",
		"Gavin.and.Stacey.S00E18.A.Fond.Farewell.720p.WEB-DL.AAC.H264-iParr")
	if got := gh58PIDSet(items); len(got) != 1 {
		t.Errorf("S00E18: emitted PIDs = %q, want only m0026d9p", got)
	}
}

// TVDB 449991 "Doctor Who (2023)" has none of the six one-off "Doctor
// Who: X" programmes iBL returns, so no search for its episodes returns
// any of them, the season 0 search included.
func TestGH104_OneOffOfAnotherSeriesStaysOut(t *testing.T) {
	eps := gh104FixtureEpisodes(t, filepath.Join(gh60Dir, "skyhook_449991.json"))
	queries := []string{"t=tvsearch&tvdbid=449991&season=0", "t=tvsearch&tvdbid=449991"}
	for _, e := range eps {
		if e.Season == 0 && e.Episode > 0 {
			queries = append(queries, "t=tvsearch&tvdbid=449991&season=0&ep="+strconv.Itoa(e.Episode))
		}
	}
	for _, q := range queries {
		items := gh104Search(t, map[string]string{"Doctor Who (2023)": "search_Doctor_Who_2023.json"},
			gh58DoctorWhoBrands, eps, q)
		gh58AssertNone(t, items, gh58DoctorWhoOneOffs, q+": one-off Doctor Who: X")
	}
}

// A one-off is "Show: X" with no subtitle that is its own top-level
// programme on iPlayer. BrandPID is iBL's tleo_id.
func TestGH104_OneOffTitle(t *testing.T) {
	cases := []struct {
		name string
		res  bbc.IBLResult
		want string
		ok   bool
	}{
		{"one-off", bbc.IBLResult{PID: "b00p3pvl", BrandPID: "b00p3pvl", Title: "Doctor Who: Dreamland"}, "Dreamland", true},
		{"companion brand episode", bbc.IBLResult{PID: "m001tv5z", BrandPID: "m001sx3r", Title: "Doctor Who: Unleashed"}, "", false},
		{"one-off with a subtitle", bbc.IBLResult{PID: "p0000001", BrandPID: "p0000001", Title: "Doctor Who: Dreamland", Subtitle: "Part 1"}, "", false},
		{"no tleo_id", bbc.IBLResult{PID: "p0000002", Title: "Doctor Who: Dreamland"}, "", false},
		{"the show itself", bbc.IBLResult{PID: "p0000003", BrandPID: "p0000003", Title: "Doctor Who (2005\u20132022)"}, "", false},
		{"another show", bbc.IBLResult{PID: "m001tvcp", BrandPID: "m001tvcp", Title: "Peaky Blinders: Rambert\u2019s The Redemption of Thomas Shelby"}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := oneOffTitle(tc.res, "Doctor Who (2005)")
			if got != tc.want || ok != tc.ok {
				t.Errorf("oneOffTitle(%q) = %q, %v; want %q, %v", tc.res.Title, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// matchOneOff takes the one TheTVDB episode titled as the one-off, by
// its own title or its whole one, aired within tvdbOneOffWindow. The
// first six titles and dates are TheTVDB's (Doctor Who (2005), and
// Gavin & Stacey's S00E18 renumbered to share the index); the last
// three are made up.
func TestGH104_MatchOneOff(t *testing.T) {
	idx := newTVDBIndex([]TVDBEpisode{
		{Season: 0, Episode: 5, Title: "The Infinite Quest", AirDate: "2007-06-30"},
		{Season: 0, Episode: 9, Title: "The Next Doctor", AirDate: "2008-12-25"},
		{Season: 0, Episode: 15, Title: "Dreamland", AirDate: "2009-12-24"},
		{Season: 0, Episode: 18, Title: "The Ultimate Guide", AirDate: "2010-04-17"},
		{Season: 0, Episode: 91, Title: "The Ultimate Guide (2013)", AirDate: "2013-11-18"},
		{Season: 0, Episode: 50, Title: "Gavin & Stacey: A Fond Farewell", AirDate: "2025-01-01"},
		{Season: 0, Episode: 60, Title: "Christmas Special", AirDate: "2020-12-25"},
		{Season: 0, Episode: 70, Title: "Twin Peaks", AirDate: "2021-03-01"},
		{Season: 0, Episode: 71, Title: "Twin Peaks", AirDate: "2021-03-20"},
	})
	cases := []struct {
		name, title, full, airDate string
		want                       int // TheTVDB S00 episode, 0 for none
	}{
		{"same day", "The Infinite Quest", "Doctor Who: The Infinite Quest", "2007-06-30", 5},
		{"19 days before TheTVDB", "Dreamland", "Doctor Who: Dreamland", "2009-12-05", 15},
		{"whole title", "A Fond Farewell", "Gavin & Stacey: A Fond Farewell", "2025-01-01", 50},
		// TheTVDB's 2010 "The Ultimate Guide" is a different programme.
		{"same title years away", "The Ultimate Guide", "Doctor Who: The Ultimate Guide", "2013-11-18", 0},
		// TheTVDB has The Next Doctor that day; a date alone is not a match.
		{"air date only", "Top 5 Christmas Moments", "Doctor Who: Top 5 Christmas Moments", "2008-12-25", 0},
		{"generic title", "Christmas Special", "Doctor Who: Christmas Special", "2020-12-25", 0},
		{"two episodes of the title", "Twin Peaks", "Doctor Who: Twin Peaks", "2021-03-10", 0},
		{"outside the window", "Dreamland", "Doctor Who: Dreamland", "2009-11-20", 0},
		{"year only", "Dreamland", "Doctor Who: Dreamland", "2009", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, ok := idx.matchOneOff(tc.title, tc.full, tc.airDate)
			got := 0
			if ok {
				got = e.Episode
			}
			if got != tc.want {
				t.Errorf("matchOneOff(%q, %q) = S00E%02d (ok=%v), want S00E%02d", tc.title, tc.airDate, got, ok, tc.want)
			}
		})
	}
}

// Synthetic. Only a one-off that is its own programme is admitted, only
// in a search TheTVDB numbers, and never at a number another programme
// already has.
func TestGH104_OneOffAdmissionRules(t *testing.T) {
	eps := fixedEpisodes{
		{Season: 1, Episode: 1, Title: "Pilot", AirDate: "2024-03-02"},
		{Season: 0, Episode: 1, Title: "The Lost Island", AirDate: "2024-12-20"},
		{Season: 0, Episode: 2, Title: "Behind the Island", AirDate: "2024-12-21"},
		{Season: 0, Episode: 3, Title: "Island Memories", AirDate: "2025-01-01"},
	}
	const parent = `{"id":"ep_parent","type":"episode","title":"Example Show","subtitle":"Series 1: 1. Pilot","release_date":"2024-03-02","parent_position":1,"tleo_id":"brand_parent"}`
	const oneOff = `{"id":"ep_oneoff","type":"episode","title":"Example Show: The Lost Island","release_date":"2024-12-01","tleo_id":"ep_oneoff"}`
	serve := func(t *testing.T, query string, elements ...string) []gh58Item {
		t.Helper()
		sky := fakeSkyhookServer(t, `{"title":"Example Show","firstAired":"2024-03-02"}`)
		old := skyhookBaseURL
		skyhookBaseURL = sky.URL
		t.Cleanup(func() { skyhookBaseURL = old })
		payload := `{"new_search":{"results":[` + parent
		for _, e := range elements {
			payload += "," + e
		}
		payload += `]}}`
		h := newHandlerWithBBC(t, payload)
		h.SetEpisodeSource(eps)
		return gh58Serve(t, h, query)
	}

	t.Run("own programme", func(t *testing.T) {
		items := serve(t, "t=tvsearch&tvdbid=999104&season=0&ep=1", oneOff)
		gh58AssertTitle(t, items, "ep_oneoff", "Example.Show.S00E01.The.Lost.Island.720p.WEB-DL.AAC.H264-iParr")
		if got := gh58PIDSet(serve(t, "t=tvsearch&tvdbid=999104&season=1&ep=1", oneOff)); len(got) != 1 || got[0] != "ep_parent" {
			t.Errorf("S01E01: emitted PIDs = %q, want only ep_parent", got)
		}
	})

	// An episode of a companion brand, with no subtitle, whose title after
	// the colon is TheTVDB's.
	t.Run("companion brand episode", func(t *testing.T) {
		companion := `{"id":"ep_companion","type":"episode","title":"Example Show: Behind the Island","release_date":"2024-12-21","tleo_id":"brand_companion"}`
		gh58AssertNone(t, serve(t, "t=tvsearch&tvdbid=999104&season=0&ep=2", companion),
			map[string]bool{"ep_companion": true}, "companion")
	})

	t.Run("one-off with a subtitle", func(t *testing.T) {
		subtitled := `{"id":"ep_subtitled","type":"episode","title":"Example Show: Island Memories","subtitle":"Part 1","release_date":"2025-01-01","tleo_id":"ep_subtitled"}`
		gh58AssertNone(t, serve(t, "t=tvsearch&tvdbid=999104&season=0&ep=3", subtitled),
			map[string]bool{"ep_subtitled": true}, "subtitled one-off")
	})

	// A daily search keeps date titles and numbers nothing from TheTVDB,
	// so a one-off would go out by its date, which Sonarr takes for that
	// day's episode of a daily series.
	t.Run("daily search", func(t *testing.T) {
		gh58AssertNone(t, serve(t, "t=tvsearch&tvdbid=999104&season=2024&ep=12/01", oneOff),
			map[string]bool{"ep_oneoff": true}, "one-off in a daily search")
	})

	// The show's own programme has S00E01 by its title, so the one-off does
	// not add a second item there.
	t.Run("number already taken", func(t *testing.T) {
		own := `{"id":"ep_own","type":"episode","title":"Example Show","subtitle":"The Lost Island","release_date":"2024-12-20","tleo_id":"brand_parent"}`
		items := serve(t, "t=tvsearch&tvdbid=999104&season=0&ep=1", own, oneOff)
		if got := gh58PIDSet(items); len(got) != 1 || got[0] != "ep_own" {
			t.Errorf("S00E01: emitted PIDs = %q, want only ep_own", got)
		}
	})
}

// TVDB 78996 "Wallace & Gromit". iPlayer has no programme titled exactly
// "Wallace & Gromit", only one-offs such as b00g8hbw "Wallace & Gromit:
// A Matter of Loaf and Death" (2008-12-25), TheTVDB's S01E04 that day,
// so the subtitle extension was already on and they answered every
// search as Wallace.and.Gromit.2008.12.25, a title Sonarr rejects for a
// standard series. b01pzqj3 "A Grand Day Out" has only a year from iBL
// ("1992"), so TheTVDB cannot check it and it keeps its title.
func TestGH104_OneOffWithoutTheShowNumbered(t *testing.T) {
	h := NewHandler(nil, newTestStore(t), nil, nil)
	h.SetEpisodeSource(fixedEpisodes{
		{Season: 1, Episode: 1, Title: "A Grand Day Out", AirDate: "1989-11-04"},
		{Season: 1, Episode: 4, Title: "A Matter of Loaf and Death", AirDate: "2008-12-25"},
	})
	results := []bbc.IBLResult{
		{PID: "b00g8hbw", BrandPID: "b00g8hbw", Title: "Wallace & Gromit: A Matter of Loaf and Death", AirDate: "2008-12-25"},
		{PID: "b01pzqj3", BrandPID: "b01pzqj3", Title: "Wallace & Gromit: A Grand Day Out", AirDate: "1992"},
	}
	hits := []string{results[0].Title, results[1].Title}
	run := func(season, ep int) []gh58Item {
		req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch", nil)
		w := httptest.NewRecorder()
		h.writeResultsRSS(w, req, results, hits, season, ep, "", "Wallace & Gromit", 1989, "78996", "", false, 0)
		return gh58ParseItems(t, w.Body.Bytes())
	}
	gh58AssertTitle(t, run(1, 4), "b00g8hbw",
		"Wallace.and.Gromit.S01E04.A.Matter.of.Loaf.and.Death.720p.WEB-DL.AAC.H264-iParr")
	items := run(1, 1)
	gh58AssertNone(t, items, map[string]bool{"b00g8hbw": true}, "S01E04 in a search for S01E01")
	gh58AssertTitle(t, items, "b01pzqj3", "Wallace.and.Gromit.1992.720p.WEB-DL.AAC.H264-iParr")
}
