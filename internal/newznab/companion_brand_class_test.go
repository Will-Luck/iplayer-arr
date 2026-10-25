package newznab

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
)

// GitHub #58 beyond the two Traitors pairs: the same colon-extended
// brands on other shows, the year and country suffixes that
// exactNameMatch strips, one-off specials, the daily path and the
// wildcard browse. All from real 2026-10-24 captures except the last
// test, which is synthetic.

// The Doctor Who brands in iBL search: the current "Doctor Who"
// (p0gglvqn), the companion "Doctor Who: Unleashed" (m001sx3r), the two
// year-suffixed earlier eras (p0ggwr8l 1963-1996, b006q2x0 2005-2022)
// and six one-off "Doctor Who: X" episodes that iBL returns as
// episode-type hits.
var (
	gh58DoctorWhoBrands  = []string{"p0gglvqn", "m001sx3r", "p0ggwr8l", "b006q2x0"}
	gh58DoctorWhoOneOffs = map[string]bool{
		"b00p3pvl": true, // Doctor Who: Dreamland
		"p04fttjc": true, // Doctor Who: A New Dimension
		"p0gsx48n": true, // Doctor Who: The Bedtime Story
		"b007zv0y": true, // Doctor Who: The Infinite Quest
		"m001zg9k": true, // Doctor Who: The Ultimate Guide
		"b00gfy2c": true, // Doctor Who: Top 5 Christmas Moments
	}
)

func gh58PIDSet(items []gh58Item) []string {
	seen := make(map[string]bool)
	var out []string
	for _, it := range items {
		if !seen[it.PID] {
			seen[it.PID] = true
			out = append(out, it.PID)
		}
	}
	sort.Strings(out)
	return out
}

// TVDB 449991 "Doctor Who (2023)", first aired 2024, so filterYear is
// 2024 and disambiguateByYear sees every programme titled exactly
// Doctor Who, of any era. "Doctor Who (2005-2022)" bare-names to
// "Doctor Who", so it is what switches exactOnly on here alongside the
// bare brand, and exactOnly drops Unleashed and the one-offs. Unleashed reuses the
// parent's numbering ("Season 2: 1. The Robot Revolution"), and the
// one-offs have no numbers at all, so the topical escape hatch in
// matchesSearchFilter lets them answer any S/E search. v1.8.2 emitted
// all of them as Doctor.Who.2023.*; the year bucket then had nothing
// suffixed that covers 2024 and kept every bare title. Joy to the World
// (m0026d24) has no BBC numbers either, but goes out as S01E04 from its
// position, so it no longer answers S02E01. GitHub #60.
func TestGH58_DoctorWho2023YearBucketSkipsCompanions(t *testing.T) {
	items := gh58Search(t, map[string]string{"Doctor Who (2023)": "search_Doctor_Who_2023.json"},
		gh58DoctorWhoBrands, nil, "t=tvsearch&tvdbid=449991&season=2&ep=1")

	// The parent goes out under the Sonarr name, not the bare BBC title
	// v1.8.2 gave it, which Sonarr reads as the classic Doctor Who.
	gh58AssertTitle(t, items, "m0029db8",
		"Doctor.Who.2023.S02E01.Season.2.1.The.Robot.Revolution.720p.WEB-DL.AAC.H264-iParr")
	gh58AssertNone(t, items, map[string]bool{"m0026d24": true}, "Joy to the World, S01E04 by position")
	gh58AssertNone(t, items, gh58BrandPIDs(t, "m001sx3r"), "Doctor Who: Unleashed")
	gh58AssertNone(t, items, gh58DoctorWhoOneOffs, "one-off Doctor Who: X")
	// The other eras match the name exactly but not the year.
	gh58AssertNone(t, items, gh58BrandPIDs(t, "b006q2x0"), "Doctor Who (2005-2022)")
	gh58AssertNone(t, items, gh58BrandPIDs(t, "p0ggwr8l"), "Doctor Who (1963-1996)")
}

// TVDB 78804 "Doctor Who (2005)". exactOnly drops Unleashed and the
// one-offs, and a suffixed title covers 2005, so disambiguateByYear
// drops the bare brand; only the 2005-2022 brand answers, as in v1.8.2.
// S02E01 is a number the bare brand has too (m0029db8, The Robot
// Revolution).
//
// S00E05 is TVDB's "The Infinite Quest", which iPlayer has only as the
// one-off b007zv0y "Doctor Who: The Infinite Quest", dropped by
// exactOnly. v1.8.2 and v1.9.0 answered it with four 2005-2022 programmes numbered
// otherwise (S01E39, S13E05, S09E05 and S00E25): season=00 was no
// season filter, and a programme with no BBC numbers answered any
// search. Now nothing does here. GitHub #60. With TheTVDB's episode
// list, which this handler lacks, the one-off answers it as S00E05
// (TestGH104_ParentOneOffTakesTheTVDBNumber, Gitea #104).
func TestGH58_DoctorWho2005AnswersFromItsOwnEra(t *testing.T) {
	search := func(query string) []gh58Item {
		return gh58Search(t, map[string]string{"Doctor Who (2005)": "search_Doctor_Who_2005.json"},
			gh58DoctorWhoBrands, nil, query)
	}

	items := search("t=tvsearch&tvdbid=78804&season=2&ep=1")
	gh58AssertTitle(t, items, "b0074fll", "Doctor.Who.2005.S02E01.New.Earth.720p.WEB-DL.AAC.H264-iParr")
	if got, want := gh58PIDSet(items), []string{"b0074fll"}; !slices.Equal(got, want) {
		t.Errorf("S02E01: emitted PIDs = %q, want %q", got, want)
	}

	if got := gh58PIDSet(search("t=tvsearch&tvdbid=78804&season=00&ep=5")); len(got) != 0 {
		t.Errorf("S00E05: emitted PIDs = %q, want none", got)
	}
}

// TVDB 75795 "The Apprentice (UK)". exactNameMatch strips the country
// tag, so the bare BBC brand "The Apprentice" counts as exact. v1.8.2
// gave it its own title and renamed both companions to the query name,
// so its only The.Apprentice.UK.* titles were companion episodes. Two
// real per-episode collisions: You're Fired! reuses "Series 17: 1.
// Antigua Tourism" and Unfinished Business carries "Apprentice Series
// 20: 12. The Final". Now the parent goes out under the Sonarr name and
// the companions not at all.
func TestGH58_CountryTaggedParentSkipsCompanions(t *testing.T) {
	cases := []struct {
		query, parentPID, parentTitle string
	}{
		{"t=tvsearch&tvdbid=75795&season=17&ep=1", "m001gr7b",
			"The.Apprentice.UK.S17E01.Antigua.Tourism.720p.WEB-DL.AAC.H264-iParr"},
		{"t=tvsearch&tvdbid=75795&season=20&ep=12", "m002v3q0",
			"The.Apprentice.UK.S20E12.The.Final.720p.WEB-DL.AAC.H264-iParr"},
	}
	for _, tc := range cases {
		t.Run(tc.parentPID, func(t *testing.T) {
			items := gh58Search(t, map[string]string{"The Apprentice (UK)": "search_The_Apprentice_UK.json"},
				[]string{"b0071b63", "b007qgcl", "m002qm1x"}, nil, tc.query)

			gh58AssertTitle(t, items, tc.parentPID, tc.parentTitle)
			gh58AssertNone(t, items, gh58BrandPIDs(t, "b007qgcl"), "The Apprentice: You're Fired!")
			gh58AssertNone(t, items, gh58BrandPIDs(t, "m002qm1x"), "The Apprentice: Unfinished Business")
		})
	}
}

// TVDB 80435 "Gavin & Stacey". m0026d9p "Gavin & Stacey: A Fond
// Farewell" is not a companion with a series of its own: TVDB lists it
// as the parent's S00E18. This handler has no TheTVDB episode list, so
// nothing numbers it and it is dropped, on purpose (see the exactOnly
// comment in writeResultsRSS): v1.8.2 offered it only as
// Gavin.and.Stacey.2025.01.01.*, a daily-style title Sonarr rejects for
// a Standard series, so the search loses nothing that worked. With the
// list it is S00E18 (TestGH104_ParentOneOffUnderTheShowsFullTitle,
// Gitea #104). This test stops a later change from assuming every
// dropped title is a companion. The parent's three unnumbered specials
// still answer the searches for their own numbers. v1.9.0 also offered
// them for S00E18, numbered otherwise. GitHub #60.
func TestGH58_ParentOneOffSpecialDroppedByDesign(t *testing.T) {
	search := func(query string) []gh58Item {
		return gh58Search(t, map[string]string{"Gavin & Stacey": "search_Gavin_and_Stacey.json"},
			[]string{"b007nf70"}, nil, query)
	}

	items := search("t=tvsearch&tvdbid=80435&season=00&ep=18")
	gh58AssertNone(t, items, map[string]bool{"m0026d9p": true}, "one-off Gavin & Stacey: A Fond Farewell")
	if got := gh58PIDSet(items); len(got) != 0 {
		t.Errorf("S00E18: emitted PIDs = %q, want none", got)
	}
	gh58AssertTitle(t, search("t=tvsearch&tvdbid=80435&season=0&ep=3"), "b00gbjhd",
		"Gavin.and.Stacey.S00E03.Christmas.Special.2008.720p.WEB-DL.AAC.H264-iParr")
	gh58AssertTitle(t, search("t=tvsearch&tvdbid=80435&season=0&ep=8"), "m000csm7",
		"Gavin.and.Stacey.S00E08.Christmas.Special.2019.720p.WEB-DL.AAC.H264-iParr")
	gh58AssertTitle(t, search("t=tvsearch&tvdbid=80435&season=1&ep=9"), "m0026dmr",
		"Gavin.and.Stacey.S01E09.Gavin.and.Stacey.The.Finale.720p.WEB-DL.AAC.H264-iParr")
}

// TVDB 71753 "EastEnders" is a daily series, so Sonarr searches by air
// date. "EastEnders: Christmas 1986 - 2007" (m0025c1l) is a separate
// BBC brand; v1.8.2 answered 1986-12-25 with its two parts titled
// EastEnders.S01E01 and S01E02, numbered as the series' first episodes,
// which aired in 1985. The second request is a control: the parent's own
// episode for its date still comes back.
func TestGH58_DailySearchSkipsColonBrand(t *testing.T) {
	searches := map[string]string{"EastEnders": "search_EastEnders.json"}
	brands := []string{"b006m86d", "m0025c1l"}

	items := gh58Search(t, searches, brands, nil, "t=tvsearch&tvdbid=71753&season=1986&ep=12/25")
	gh58AssertNone(t, items, gh58BrandPIDs(t, "m0025c1l"), "EastEnders: Christmas 1986 - 2007")

	items = gh58Search(t, searches, brands, nil, "t=tvsearch&tvdbid=71753&season=2026&ep=10/22")
	gh58AssertTitle(t, items, "m00336w8", "EastEnders.2026.10.22.720p.WEB-DL.AAC.H264-iParr")
}

// The wildcard RSS browse has no wanted name, so exactOnly stays off and
// a companion on an editorial rail keeps its own title with no tvdbid
// attr, as in v1.8.2. The rail here carries the real parent and
// Uncloaked S02E02 elements from the m002csng and m002kksc fixtures.
func TestGH58_WildcardBrowseKeepsCompanionTitle(t *testing.T) {
	var rail []json.RawMessage
	for brand, pid := range map[string]string{"m002csng": "m0030pxx", "m002kksc": "m00328ls"} {
		var resp struct {
			ProgrammeEpisodes struct {
				Elements []json.RawMessage `json:"elements"`
			} `json:"programme_episodes"`
		}
		if err := json.Unmarshal(gh58Read(t, "episodes_"+brand+".json"), &resp); err != nil {
			t.Fatal(err)
		}
		for _, raw := range resp.ProgrammeEpisodes.Elements {
			var e struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(raw, &e); err != nil {
				t.Fatal(err)
			}
			if e.ID == pid {
				rail = append(rail, raw)
			}
		}
	}
	if len(rail) != 2 {
		t.Fatalf("built a rail of %d elements, want 2", len(rail))
	}
	railBody, err := json.Marshal(map[string]any{"group_episodes": map[string]any{"elements": rail}})
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/groups/m001bm54/episodes":
			w.Write(railBody)
		case "/groups/popular/episodes":
			w.Write([]byte(`{"group_episodes":{"elements":[]}}`))
		case "/new-search":
			w.Write([]byte(`{"new_search":{"results":[]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	ibl := bbc.NewIBL(bbc.NewClient())
	ibl.BaseURL = srv.URL
	items := gh58Serve(t, NewHandler(ibl, newTestStore(t), nil, nil), "t=tvsearch")

	gh58AssertTitle(t, items, "m00328ls",
		"The.Celebrity.Traitors.Uncloaked.S02E02.Series.2.Episode.2.720p.WEB-DL.AAC.H264-iParr")
	gh58AssertTitle(t, items, "m0030pxx",
		"The.Celebrity.Traitors.S02E02.Series.2.Episode.2.720p.WEB-DL.AAC.H264-iParr")
	// Documents the contract only: the tvdbid attr comes from the
	// request, and a browse has none, so this loop cannot fail on its
	// own. The two title checks above are what catch a regression.
	for _, it := range items {
		if it.TVDBID != "" {
			t.Errorf("browse item %s carries tvdbid=%s", it.PID, it.TVDBID)
		}
	}
}

// Synthetic, pinning current behaviour on purpose. iBL has an exact
// brand from another era ("Example Show (1992-2000)") and an in-era
// "Example Show: After Hours", but no bare "Example Show". The old-era
// brand switches exactOnly on, so After Hours is dropped. v1.8.2
// returned it renamed to Example.Show.2024.*. See the exactOnly comment
// in writeResultsRSS for why an era-aware check was not used.
func TestGH58_OutOfEraExactBrandSuppressesExtension(t *testing.T) {
	sky := fakeSkyhookServer(t, `{"title":"Example Show (2024)","firstAired":"2024-03-02"}`)
	old := skyhookBaseURL
	skyhookBaseURL = sky.URL
	t.Cleanup(func() { skyhookBaseURL = old })

	const payload = `{"new_search":{"results":[
		{"id":"ep_old_1","type":"episode","title":"Example Show (1992-2000)","subtitle":"Series 1: Episode 1","release_date":"1992-01-04"},
		{"id":"ep_old_2","type":"episode","title":"Example Show (1992-2000)","subtitle":"Series 1: Episode 2","release_date":"1992-01-11"},
		{"id":"ep_after_hours","type":"episode","title":"Example Show: After Hours","subtitle":"Series 1: Episode 1","release_date":"2024-03-02"}
	]}}`

	// Season search: two old-era candidates remain, nothing suffixed
	// covers 2024 and nothing is bare, so disambiguateByYear returns
	// nothing. This is the trade-off the comment argues for.
	t.Run("several old-era candidates", func(t *testing.T) {
		items := gh58Serve(t, newHandlerWithBBC(t, payload), "t=tvsearch&tvdbid=999001&season=1")
		if len(items) != 0 {
			t.Errorf("got %d items, want none", len(items))
		}
	})

	// The episode search leaves one old-era candidate. disambiguateByYear
	// returns a lone candidate unchanged, and v1.9.0 ran it on what the
	// episode filter left, so the 1992 episode went out under its own
	// title with the 2024 series' tvdbid. It now runs before the filter,
	// on both old-era candidates, and drops them as the season search
	// does. GitHub #60.
	t.Run("one old-era candidate", func(t *testing.T) {
		items := gh58Serve(t, newHandlerWithBBC(t, payload), "t=tvsearch&tvdbid=999001&season=1&ep=1")
		if len(items) != 0 {
			t.Errorf("got %d items, want none", len(items))
		}
	})

	// When iPlayer has exactly one programme of the name and it is from
	// another era, it is dropped as several would be. Up to v1.9.1
	// disambiguateByYear returned a lone candidate unchanged, so the 1992
	// episode went out under its own title with the 2024 series' tvdbid.
	// Gitea #104.
	const lone = `{"new_search":{"results":[
		{"id":"ep_old_1","type":"episode","title":"Example Show (1992-2000)","subtitle":"Series 1: Episode 1","release_date":"1992-01-04"}
	]}}`
	t.Run("only programme of the name is old-era", func(t *testing.T) {
		for _, q := range []string{"t=tvsearch&tvdbid=999001&season=1&ep=1", "t=tvsearch&tvdbid=999001&season=1", "t=tvsearch&tvdbid=999001"} {
			if items := gh58Serve(t, newHandlerWithBBC(t, lone), q); len(items) != 0 {
				t.Errorf("%s: got %d items, want none", q, len(items))
			}
		}
	})

	// A lone programme of another era that TheTVDB numbers for the
	// searched series stays, as one among several does (GitHub #60):
	// here TheTVDB lists the 1992 episode as the 2024 series' S00E01. It
	// goes out under the searched series' name, as its number does.
	t.Run("only programme of the name is old-era, TheTVDB numbers it", func(t *testing.T) {
		const pilot = `{"new_search":{"results":[
			{"id":"ep_old_1","type":"episode","title":"Example Show (1992-2000)","subtitle":"Series 1: 1. The Pilot","release_date":"1992-01-04"}
		]}}`
		h := newHandlerWithBBC(t, pilot)
		h.SetEpisodeSource(fixedEpisodes{{Season: 0, Episode: 1, Title: "The Pilot", AirDate: "1992-01-04"}})
		gh58AssertTitle(t, gh58Serve(t, h, "t=tvsearch&tvdbid=999001&season=0&ep=1"), "ep_old_1",
			"Example.Show.2024.S00E01.The.Pilot.720p.WEB-DL.AAC.H264-iParr")
	})

	// A lone programme whose suffix covers the year, or that has none,
	// is kept.
	t.Run("only programme of the name is in era or bare", func(t *testing.T) {
		for _, title := range []string{"Example Show (2024)", "Example Show"} {
			payload := `{"new_search":{"results":[
				{"id":"ep_1","type":"episode","title":"` + title + `","subtitle":"Series 1: Episode 1","release_date":"2024-03-02"}
			]}}`
			items := gh58Serve(t, newHandlerWithBBC(t, payload), "t=tvsearch&tvdbid=999001&season=1&ep=1")
			if len(items) == 0 {
				t.Errorf("%q: got no items, want ep_1", title)
			}
		}
	})
}
