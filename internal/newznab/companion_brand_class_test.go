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
// 2024 and disambiguateByYear runs after the new filter. "Doctor Who
// (2005-2022)" bare-names to "Doctor Who", so it is what switches
// exactOnly on here alongside the bare brand. Unleashed reuses the
// parent's numbering ("Season 2: 1. The Robot Revolution"), and the
// one-offs have no numbers at all, so the topical escape hatch in
// matchesSearchFilter lets them answer any S/E search. v1.8.2 emitted
// all of them as Doctor.Who.2023.*; the year bucket then had nothing
// suffixed that covers 2024 and kept every bare title.
func TestGH58_DoctorWho2023YearBucketSkipsCompanions(t *testing.T) {
	items := gh58Search(t, map[string]string{"Doctor Who (2023)": "search_Doctor_Who_2023.json"},
		gh58DoctorWhoBrands, nil, "t=tvsearch&tvdbid=449991&season=2&ep=1")

	// The parent keeps the bare BBC title, as in v1.8.2. This pins current
	// behaviour, not the ideal title: if parent items later get the Sonarr
	// name (Doctor.Who.2023.*), update this line.
	gh58AssertTitle(t, items, "m0029db8",
		"Doctor.Who.S02E01.Season.2.1.The.Robot.Revolution.720p.WEB-DL.AAC.H264-iParr")
	emitted := make(map[string]bool)
	for _, it := range items {
		emitted[it.PID] = true
	}
	for pid := range gh58BrandPIDs(t, "p0gglvqn") {
		if !emitted[pid] {
			t.Errorf("parent Doctor Who episode %s missing", pid)
		}
	}
	gh58AssertNone(t, items, gh58BrandPIDs(t, "m001sx3r"), "Doctor Who: Unleashed")
	gh58AssertNone(t, items, gh58DoctorWhoOneOffs, "one-off Doctor Who: X")
	// The other eras match the name exactly but not the year.
	gh58AssertNone(t, items, gh58BrandPIDs(t, "b006q2x0"), "Doctor Who (2005-2022)")
	gh58AssertNone(t, items, gh58BrandPIDs(t, "p0ggwr8l"), "Doctor Who (1963-1996)")
}

// TVDB 78804 "Doctor Who (2005)", S00E05 (TVDB's "The Infinite Quest").
// v1.8.2 already answered this from the 2005-2022 brand alone: a
// suffixed title covers 2005, so disambiguateByYear drops every bare
// one, Unleashed and the one-offs included. The new filter must not
// change that answer. Pinned from a v1.8.2 run over the same fixtures.
// season=00 means no season filter, so every E05 plus the unnumbered
// specials match; b0074fll (Series 2: 1.) is the one that must not.
func TestGH58_DoctorWho2005Unchanged(t *testing.T) {
	items := gh58Search(t, map[string]string{"Doctor Who (2005)": "search_Doctor_Who_2005.json"},
		gh58DoctorWhoBrands, nil, "t=tvsearch&tvdbid=78804&season=00&ep=5")

	want := map[string]string{
		"m001dhsb": "Doctor.Who.2005-2022.S01E39.The.Power.of.the.Doctor.720p.WEB-DL.AAC.H264-iParr",
		"m00123l4": "Doctor.Who.2005-2022.S13E05.Survivors.of.the.Flux.720p.WEB-DL.AAC.H264-iParr",
		"b06kv29n": "Doctor.Who.2005-2022.S09E05.The.Girl.Who.Died.720p.WEB-DL.AAC.H264-iParr",
		"p02ct985": "Doctor.Who.2005-2022.S00E25.Last.Christmas.720p.WEB-DL.AAC.H264-iParr",
	}
	for pid, title := range want {
		gh58AssertTitle(t, items, pid, title)
	}
	var wantPIDs []string
	for pid := range want {
		wantPIDs = append(wantPIDs, pid)
	}
	sort.Strings(wantPIDs)
	if got := gh58PIDSet(items); !slices.Equal(got, wantPIDs) {
		t.Errorf("emitted PIDs = %q, want %q", got, wantPIDs)
	}
}

// TVDB 75795 "The Apprentice (UK)". exactNameMatch strips the country
// tag, so the bare BBC brand "The Apprentice" counts as exact and keeps
// its own title (v1.8.2 titled it the same way). Two real per-episode
// collisions: You're Fired! reuses "Series 17: 1. Antigua Tourism" and
// Unfinished Business carries "Apprentice Series 20: 12. The Final".
// v1.8.2 renamed both companions to the query name, which is why its
// only The.Apprentice.UK.* titles were companion episodes.
func TestGH58_CountryTaggedParentSkipsCompanions(t *testing.T) {
	cases := []struct {
		query, parentPID, parentTitle string
	}{
		{"t=tvsearch&tvdbid=75795&season=17&ep=1", "m001gr7b",
			"The.Apprentice.S17E01.Antigua.Tourism.720p.WEB-DL.AAC.H264-iParr"},
		{"t=tvsearch&tvdbid=75795&season=20&ep=12", "m002v3q0",
			"The.Apprentice.S20E12.The.Final.720p.WEB-DL.AAC.H264-iParr"},
	}
	for _, tc := range cases {
		t.Run(tc.parentPID, func(t *testing.T) {
			items := gh58Search(t, map[string]string{"The Apprentice (UK)": "search_The_Apprentice_UK.json"},
				[]string{"b0071b63", "b007qgcl", "m002qm1x"}, nil, tc.query)

			// parentTitle pins v1.8.2's bare-brand title, not the ideal
			// one: if parent items later get the Sonarr name
			// (The.Apprentice.UK.*), update the cases above.
			gh58AssertTitle(t, items, tc.parentPID, tc.parentTitle)
			gh58AssertNone(t, items, gh58BrandPIDs(t, "b007qgcl"), "The Apprentice: You're Fired!")
			gh58AssertNone(t, items, gh58BrandPIDs(t, "m002qm1x"), "The Apprentice: Unfinished Business")
		})
	}
}

// TVDB 80435 "Gavin & Stacey". m0026d9p "Gavin & Stacey: A Fond
// Farewell" is not a companion with a series of its own: TVDB lists it
// as the parent's S00E18. It is dropped anyway, on purpose (see the
// exactOnly comment in writeResultsRSS): v1.8.2 offered it only as
// Gavin.and.Stacey.2025.01.01.*, a daily-style title Sonarr rejects for
// a Standard series, so the search loses nothing that worked. This test
// stops a later change from assuming every dropped title is a
// companion. The parent's three unnumbered specials still answer.
func TestGH58_ParentOneOffSpecialDroppedByDesign(t *testing.T) {
	items := gh58Search(t, map[string]string{"Gavin & Stacey": "search_Gavin_and_Stacey.json"},
		[]string{"b007nf70"}, nil, "t=tvsearch&tvdbid=80435&season=00&ep=18")

	gh58AssertNone(t, items, map[string]bool{"m0026d9p": true}, "one-off Gavin & Stacey: A Fond Farewell")
	gh58AssertTitle(t, items, "b00gbjhd", "Gavin.and.Stacey.S00E03.Christmas.Special.2008.720p.WEB-DL.AAC.H264-iParr")
	gh58AssertTitle(t, items, "m000csm7", "Gavin.and.Stacey.S00E08.Christmas.Special.2019.720p.WEB-DL.AAC.H264-iParr")
	gh58AssertTitle(t, items, "m0026dmr", "Gavin.and.Stacey.S01E09.Gavin.and.Stacey.The.Finale.720p.WEB-DL.AAC.H264-iParr")
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

	// FIXME: episode search leaves one old-era candidate, and
	// disambiguateByYear returns a lone candidate unchanged, so the 1992
	// episode goes out under its own title with the 2024 series' tvdbid.
	// Older than #58 (see the FIXME in writeResultsRSS). Pinned on purpose:
	// a fix to disambiguateByYear turns this subtest red, and it should
	// then be flipped to assert that ep_old_1 is absent.
	t.Run("one old-era candidate", func(t *testing.T) {
		items := gh58Serve(t, newHandlerWithBBC(t, payload), "t=tvsearch&tvdbid=999001&season=1&ep=1")
		gh58AssertTitle(t, items, "ep_old_1",
			"Example.Show.1992-2000.S01E01.Series.1.Episode.1.720p.WEB-DL.AAC.H264-iParr")
		gh58AssertNone(t, items, map[string]bool{"ep_after_hours": true}, "in-era extension")
	})
}
