package newznab

import (
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// Gitea #97. When the tvdbid search finds nothing, Sonarr 4 searches by
// title, cleaned as SearchCriteriaBase.GetCleanSceneTitle does: a
// leading "The" dropped, "&" as "and", apostrophes and dots deleted,
// other punctuation as separators. The name filter compared that with
// iPlayer's title as it was, so q=Celebrity Traitors never matched "The
// Celebrity Traitors".
func TestGitea97_NameMatchingFoldsWhatSonarrCleans(t *testing.T) {
	cases := []struct {
		prog, want   string
		exact, match bool
	}{
		{"The Celebrity Traitors", "Celebrity Traitors", true, true},
		{"David Fuller: Monster in the Morgue", "David Fuller Monster In The Morgue", true, true},
		{"Gavin & Stacey", "Gavin and Stacey", true, true},
		{"Mrs Brown's Boys", "Mrs Browns Boys", true, true},
		{"Mrs Brown's Boys", "Mrs. Brown's Boys", true, true},
		{"Later... with Jools Holland", "Later with Jools Holland", true, true},
		{"Talking Tom Heroes: Suddenly Super", "Talking Tom Heroes Suddenly Super", true, true},
		{"Doctor Who (2005\u20132022)", "Doctor Who (2005)", true, true},
		// The subtitle extension still needs iPlayer's colon.
		{"The Celebrity Traitors: Uncloaked", "Celebrity Traitors", false, true},
		{"Talking Tom Heroes: Suddenly Super", "Talking Tom Heroes", false, true},
		{"David Fuller: Monster in the Morgue: Extra", "David Fuller Monster In The Morgue", false, true},
		// Not the same show.
		{"The Traitors", "Celebrity Traitors", false, false},
		{"The Traitors NZ", "The Traitors", false, false},
		{"Tom Jones: Live", "Tom", false, false},
		{"Doctor Who (1963-1996): Shada", "Doctor Who", false, false},
		{"Theatre Live", "Atre Live", false, false},
	}
	for _, tc := range cases {
		if got := exactNameMatch(tc.prog, tc.want); got != tc.exact {
			t.Errorf("exactNameMatch(%q, %q) = %v, want %v", tc.prog, tc.want, got, tc.exact)
		}
		if got := nameMatches(tc.prog, tc.want); got != tc.match {
			t.Errorf("nameMatches(%q, %q) = %v, want %v", tc.prog, tc.want, got, tc.match)
		}
	}
}

// oneOffTitle used to cut iPlayer's title at the query's length. With
// "Mrs. Brown's Boys" one byte longer than iPlayer's "Mrs Brown's Boys",
// that cut the first letter off the one-off's own title.
func TestGitea97_OneOffTitleAfterAFoldedName(t *testing.T) {
	cases := []struct {
		res  bbc.IBLResult
		want string
		x    string
		ok   bool
	}{
		{bbc.IBLResult{PID: "p0000971", BrandPID: "p0000971", Title: "The Celebrity Traitors: The Reunion"}, "Celebrity Traitors", "The Reunion", true},
		{bbc.IBLResult{PID: "p0000972", BrandPID: "p0000972", Title: "Mrs Brown's Boys: Mammy's Ball"}, "Mrs. Brown's Boys", "Mammy's Ball", true},
		{bbc.IBLResult{PID: "b00p3pvl", BrandPID: "b00p3pvl", Title: "Doctor Who: Dreamland"}, "Doctor Who (2005)", "Dreamland", true},
		{bbc.IBLResult{PID: "p0000973", BrandPID: "p0000973", Title: "The Celebrity Traitors"}, "Celebrity Traitors", "", false},
	}
	for _, tc := range cases {
		x, ok := oneOffTitle(tc.res, tc.want)
		if x != tc.x || ok != tc.ok {
			t.Errorf("oneOffTitle(%q, %q) = %q, %v; want %q, %v", tc.res.Title, tc.want, x, ok, tc.x, tc.ok)
		}
	}
}

// Sonarr's title tier for TVDB 453660 on the captured iBL data (the
// capture for "The Celebrity Traitors", served for the query Sonarr
// sends). The parent's S02E02 comes back under iPlayer's name, the
// Uncloaked companion still does not, and with nothing stored there is
// no tvdbid to carry.
func TestGitea97_TitleSearchWithoutTheLeadingThe(t *testing.T) {
	items := gh58Search(t, map[string]string{"Celebrity Traitors": "search_The_Celebrity_Traitors.json"},
		gh58Brands, nil, "t=tvsearch&q=Celebrity+Traitors&season=2&ep=2")
	gh58AssertTitle(t, items, "m0030pxx",
		"The.Celebrity.Traitors.S02E02.Series.2.Episode.2.720p.WEB-DL.AAC.H264-iParr")
	gh58AssertNone(t, items, gh58BrandPIDs(t, "m002kksc"), "Uncloaked")
	for _, it := range items {
		if it.TVDBID != "" {
			t.Errorf("%s carries tvdbid %q with no series stored", it.Title, it.TVDBID)
		}
	}
}

// With the series stored by an earlier tvdbid search, the title tier is
// rehydrated to it: every item carries tvdbid 453660.
func TestGitea97_TitleSearchRehydratesTheStoredSeries(t *testing.T) {
	gh58Skyhook(t)
	srv := gh58IBL(t, map[string]string{"Celebrity Traitors": "search_The_Celebrity_Traitors.json"}, gh58Brands)
	ibl := bbc.NewIBL(bbc.NewClient())
	ibl.BaseURL = srv.URL
	st := newTestStore(t)
	if err := st.PutSeriesMapping(&store.SeriesMapping{TVDBId: "453660", ShowName: "The Celebrity Traitors", Year: 2025}); err != nil {
		t.Fatal(err)
	}
	items := gh58Serve(t, NewHandler(ibl, st, nil, nil), "t=tvsearch&q=Celebrity+Traitors&season=2&ep=2")
	gh58AssertTitle(t, items, "m0030pxx",
		"The.Celebrity.Traitors.S02E02.Series.2.Episode.2.720p.WEB-DL.AAC.H264-iParr")
	gh58AssertNone(t, items, gh58BrandPIDs(t, "m002kksc"), "Uncloaked")
	for _, it := range items {
		if it.TVDBID != "453660" {
			t.Errorf("%s carries tvdbid %q, want 453660", it.Title, it.TVDBID)
		}
	}
}

// Sonarr's clean title of "David Fuller: Monster in the Morgue" has no
// colon. The exact title wins and its colon extension stays out, as in
// TestGH58_ColonTitleWithExtensionSibling for the query with the colon.
func TestGitea97_TitleSearchWithPunctuationFolded(t *testing.T) {
	const payload = `{"new_search":{"results":[
		{"id":"ep_exact","type":"episode","title":"David Fuller: Monster in the Morgue","subtitle":"Series 1: Episode 1","release_date":"2023-11-01"},
		{"id":"ep_extra","type":"episode","title":"David Fuller: Monster in the Morgue: Extra","subtitle":"Series 1: Episode 1","release_date":"2023-11-01"}
	]}}`
	h := newHandlerWithBBC(t, payload)
	items := gh58Serve(t, h, "t=tvsearch&q=David+Fuller+Monster+In+The+Morgue&season=1&ep=1")
	gh58AssertTitle(t, items, "ep_exact",
		"David.Fuller.Monster.in.the.Morgue.S01E01.Series.1.Episode.1.720p.WEB-DL.AAC.H264-iParr")
	gh58AssertNone(t, items, map[string]bool{"ep_extra": true}, "colon extension")
}
