package newznab

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// GitHub #60, the reporter's follow-up on v1.9.1: a Porridge (76578)
// search returned "Porridge Rebooted", TheTVDB's S00E08 of the 1974 show,
// as Porridge.2017.S00E08.Special, under the name of the revival's BBC
// brand. Doctor Who (2005) searches did the same with every programme of
// the "Doctor Who (2005-2022)" brand. Sonarr maps neither name to the
// searched series by title (Sonarr's /api/v3/parse gives no series for
// Doctor.Who.2005-2022.S00E34 or Porridge.2017.S00E08, and Doctor Who
// 76107 for Doctor.Who.S00E05), so a grab matched only by the tvdbid
// attribute, and Sonarr does not import such a grab from an automatic
// search (CompletedDownloadService.Check). A search for a known series
// now sends every item out under that series' name.

// gh60FixturesWithRebooted is gh60Fixtures with "Porridge Rebooted"
// (b07tlt91, captured 2026-10-25) back in the trimmed Porridge (2017)
// listing.
func gh60FixturesWithRebooted(t *testing.T) *bbc.IBL {
	t.Helper()
	fixtures, _ := gh60Fixtures(t, http.StatusOK)
	rebooted := map[string]any{
		"id": "b07tlt91", "type": "episode", "title": "Porridge (2017)", "subtitle": "Special",
		"release_date": "28 Aug 2016", "parent_position": 1, "tleo_id": "p05dsmwl",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp, err := http.Get(fixtures.BaseURL + r.URL.RequestURI())
		if err != nil {
			t.Error(err)
			return
		}
		defer resp.Body.Close()
		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			w.WriteHeader(resp.StatusCode)
			return
		}
		if pe, ok := body["programme_episodes"].(map[string]any); ok && strings.Contains(r.URL.Path, "/p05dsmwl/") {
			pe["elements"] = append(pe["elements"].([]any), rebooted)
			pe["count"] = len(pe["elements"].([]any))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	ibl := bbc.NewIBL(bbc.NewClient())
	ibl.BaseURL = srv.URL
	return ibl
}

func TestGH60_SearchItemsGoOutUnderTheSeriesName(t *testing.T) {
	h := gh60Handler(t, gh60FixturesWithRebooted(t), newTestStore(t))
	for _, c := range []struct{ query, pid, title, tvdbid string }{
		// Brand "Doctor Who (2005-2022)", series "Doctor Who (2005)".
		{"t=tvsearch&tvdbid=78804&season=0&ep=34", "b00wyj5p", "Doctor.Who.2005.S00E34.A.Christmas.Carol.720p.WEB-DL.AAC.H264-iParr", "78804"},
		{"t=tvsearch&tvdbid=78804&season=0&ep=2", "b0074f9p", "Doctor.Who.2005.S00E02.The.Christmas.Invasion.720p.WEB-DL.AAC.H264-iParr", "78804"},
		// The bare "Doctor Who" brand is Doctor Who (2023)'s, but TheTVDB
		// also has The Star Beast as Doctor Who (2005) S00E162.
		{"t=tvsearch&tvdbid=78804&season=0&ep=162", "m001sx3h", "Doctor.Who.2005.S00E162.Specials.1.The.Star.Beast.720p.WEB-DL.AAC.H264-iParr", "78804"},
		{"t=tvsearch&tvdbid=449991&season=0&ep=5", "m0026d24", "Doctor.Who.2023.S00E05.Joy.to.the.World.720p.WEB-DL.AAC.H264-iParr", "449991"},
		// The reporter's case: brand "Porridge (2017)", series "Porridge".
		{"t=tvsearch&tvdbid=76578&season=0&ep=8", "b07tlt91", "Porridge.S00E08.Special.720p.WEB-DL.AAC.H264-iParr", "76578"},
		// Brand and series already share the name.
		{"t=tvsearch&tvdbid=76578&season=0&ep=2", "b007894v", "Porridge.S00E02.Christmas.Special.No.Way.Out.720p.WEB-DL.AAC.H264-iParr", "76578"},
	} {
		items := gh58Serve(t, h, c.query)
		gh58AssertTitle(t, items, c.pid, c.title)
		for _, it := range items {
			if it.TVDBID != c.tvdbid {
				t.Errorf("%s: %s carries tvdbid %q, want %s", c.query, it.Title, it.TVDBID, c.tvdbid)
			}
		}
	}

	// Every item of a whole-show search, whatever its brand.
	items := gh58Serve(t, h, "t=tvsearch&tvdbid=78804")
	if len(items) == 0 {
		t.Fatal("tvdbid=78804 returned nothing")
	}
	var other []string
	for _, it := range items {
		if !strings.HasPrefix(it.Title, "Doctor.Who.2005.S") {
			other = append(other, it.Title)
		}
	}
	if len(other) > 0 {
		t.Errorf("tvdbid=78804: %d of %d items under another name, first %q", len(other), len(items), other[0])
	}
}

// The Show Override is still found under the BBC brand's name, the key
// users set it with, so its numbering still applies. Its custom name
// gives way to the series' name, as in the RSS feed: in a search the
// series is the one Sonarr asked for. A search for a series iplayer-arr
// does not know keeps the custom name.
func TestGH60_SeriesNameKeepsTheBrandOverride(t *testing.T) {
	const payload = `{"new_search":{"results":[
		{"id":"p0000105","type":"episode","title":"Some Show","subtitle":"The Fifth One","release_date":"2019-10-20","parent_position":5}
	]}}`
	sky := fakeSkyhookServer(t, `{"title":"Some Show (2019)","firstAired":"2019-10-06"}`)
	old := skyhookBaseURL
	skyhookBaseURL = sky.URL
	t.Cleanup(func() { skyhookBaseURL = old })
	override := &store.ShowOverride{ShowName: "Some Show", ForceSeriesNum: 3, CustomName: "Custom Show"}

	h, st := newHandlerWithBBCAndStore(t, payload)
	if err := st.PutOverride(override); err != nil {
		t.Fatal(err)
	}
	items := gh58Serve(t, h, "t=tvsearch&tvdbid=99001&season=3&ep=5")
	gh58AssertTitle(t, items, "p0000105", "Some.Show.2019.S03E05.The.Fifth.One.720p.WEB-DL.AAC.H264-iParr")

	h, st = newHandlerWithBBCAndStore(t, payload)
	if err := st.PutOverride(override); err != nil {
		t.Fatal(err)
	}
	items = gh58Serve(t, h, "t=tvsearch&q=Some+Show&season=3&ep=5")
	gh58AssertTitle(t, items, "p0000105", "Custom.Show.S03E05.The.Fifth.One.720p.WEB-DL.AAC.H264-iParr")
	for _, it := range items {
		if it.TVDBID != "" {
			t.Errorf("unknown series: %s carries tvdbid %q", it.Title, it.TVDBID)
		}
	}
}

// A programme whose BBC title says it is another series keeps that
// title. exactNameMatch ignores country tags, so "Some Show (US)" passes
// the name filter of a search for Some Show (2019); renamed, Sonarr would
// import it as that series' episode. TheTVDB numbering is the evidence
// that overrides the tag, and there is none here.
func TestGH60_OtherCountryKeepsItsOwnName(t *testing.T) {
	const payload = `{"new_search":{"results":[
		{"id":"p0000201","type":"episode","title":"Some Show","subtitle":"Series 1: Episode 1","release_date":"2019-10-06"},
		{"id":"p0000202","type":"episode","title":"Some Show (US)","subtitle":"Series 1: Episode 1","release_date":"2021-10-07"}
	]}}`
	sky := fakeSkyhookServer(t, `{"title":"Some Show (2019)","firstAired":"2019-10-06"}`)
	old := skyhookBaseURL
	skyhookBaseURL = sky.URL
	t.Cleanup(func() { skyhookBaseURL = old })
	h, _ := newHandlerWithBBCAndStore(t, payload)
	items := gh58Serve(t, h, "t=tvsearch&tvdbid=99002&season=1&ep=1")
	gh58AssertTitle(t, items, "p0000201", "Some.Show.2019.S01E01.Series.1.Episode.1.720p.WEB-DL.AAC.H264-iParr")
	gh58AssertTitle(t, items, "p0000202", "Some.Show.US.S01E01.Series.1.Episode.1.720p.WEB-DL.AAC.H264-iParr")
}

// Gitea #104 item 4. When the tvdbid tier of a search returns nothing,
// Sonarr sends its title tier: q=<each of the series' scene titles>,
// cleaned (SearchCriteriaBase.GetCleanSceneTitle), with no tvdbid. For
// Doctor Who (2005) that is q=Doctor Who 2005 and, from a scene alias,
// q=Doctor Who; for Doctor Who (2023), q=Doctor Who 2023 and q=Doctor Who
// 2024. With both series stored, q=Doctor Who was answered as whichever
// came first in the store, Doctor Who (2023), and q=Doctor Who 2005 was
// not recognised at all.
const gitea104TwoDoctorsPayload = `{"new_search":{"results":[
	{"id":"ep_2005","type":"episode","title":"Doctor Who (2005\u20132022)","subtitle":"Series 1: Episode 2","release_date":"2005-04-02","parent_position":2},
	{"id":"ep_2024","type":"episode","title":"Doctor Who","subtitle":"Season 1: Episode 2","release_date":"2024-05-11","parent_position":2}
]}}`

func gitea104Store(t *testing.T, st *store.Store, mappings ...store.SeriesMapping) {
	t.Helper()
	for i := range mappings {
		if err := st.PutSeriesMapping(&mappings[i]); err != nil {
			t.Fatal(err)
		}
	}
}

var (
	gitea104Doctor2005 = store.SeriesMapping{TVDBId: "78804", ShowName: "Doctor Who (2005)", Year: 2005}
	gitea104Doctor2023 = store.SeriesMapping{TVDBId: "449991", ShowName: "Doctor Who (2023)", Year: 2024}
	gitea104DoctorWho  = store.SeriesMapping{TVDBId: "76107", ShowName: "Doctor Who", Year: 1963}
)

// gitea104Only checks that items are pid alone, with tvdbid, and that
// one of them is titled title.
func gitea104Only(t *testing.T, items []gh58Item, pid, title, tvdbid string) {
	t.Helper()
	gh58AssertTitle(t, items, pid, title)
	for _, it := range items {
		if it.PID != pid || it.TVDBID != tvdbid {
			t.Errorf("got %s %q tvdbid=%q, want only %s with tvdbid=%s", it.PID, it.Title, it.TVDBID, pid, tvdbid)
		}
	}
}

// A query with a year names one series: q=Doctor Who 2005 is the stored
// "Doctor Who (2005)" and is answered as a tvdbid=78804 search would be.
func TestGitea104_YearQueryRehydratesToItsSeries(t *testing.T) {
	h, st := newHandlerWithBBCAndStore(t, gitea104TwoDoctorsPayload)
	gitea104Store(t, st, gitea104Doctor2005, gitea104Doctor2023)

	gitea104Only(t, gh58Serve(t, h, "t=tvsearch&q=Doctor+Who+2005&season=1&ep=2"),
		"ep_2005", "Doctor.Who.2005.S01E02.Series.1.Episode.2.720p.WEB-DL.AAC.H264-iParr", "78804")
	gitea104Only(t, gh58Serve(t, h, "t=tvsearch&q=Doctor+Who+2023&season=1&ep=2"),
		"ep_2024", "Doctor.Who.2023.S01E02.Season.1.Episode.2.720p.WEB-DL.AAC.H264-iParr", "449991")
	// No stored series is "Doctor Who (2024)": not rehydrated, and
	// nothing iPlayer has is titled "Doctor Who 2024".
	if items := gh58Serve(t, h, "t=tvsearch&q=Doctor+Who+2024&season=1&ep=2"); len(items) != 0 {
		t.Errorf("q=Doctor Who 2024 returned %d items", len(items))
	}
}

// On the captured data: once the tvdbid searches have stored both
// series, Sonarr's title-tier query for a series is answered exactly as
// its tvdbid search is, items, names and tvdbid alike.
func TestGitea104_YearQueryAnswersAsTheTVDBIDSearch(t *testing.T) {
	ibl, _ := gh60Fixtures(t, http.StatusOK)
	h := gh60Handler(t, ibl, newTestStore(t))
	gh58Serve(t, h, "t=tvsearch&tvdbid=78804")
	gh58Serve(t, h, "t=tvsearch&tvdbid=449991")
	for _, c := range []struct{ byTitle, byID string }{
		{"q=Doctor+Who+2005&season=00&ep=34", "tvdbid=78804&season=00&ep=34"},
		{"q=Doctor+Who+2005&season=00&ep=162", "tvdbid=78804&season=00&ep=162"},
		{"q=Doctor+Who+2005&season=7&ep=6", "tvdbid=78804&season=7&ep=6"},
		{"q=Doctor+Who+2023&season=00&ep=5", "tvdbid=449991&season=00&ep=5"},
	} {
		want := gh58Serve(t, h, "t=tvsearch&"+c.byID)
		got := gh58Serve(t, h, "t=tvsearch&"+c.byTitle)
		if len(want) == 0 {
			t.Fatalf("%s returned nothing", c.byID)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s returned %d items, %s %d; first %+v, want %+v", c.byTitle, len(got), c.byID, len(want), got, want[0])
		}
	}
}

// A bare name two stored series share could be either, or a third Sonarr
// knows by a scene alias: Sonarr sends q=Doctor Who for Doctor Who (2005)
// as well as for Doctor Who (76107). Answering as one of them gives the
// other's programmes that series' tvdbid. Not answering at all is no
// worse than no tvdbid: Sonarr would then map "Doctor.Who.S01E02" by
// title alone. Nothing is lost in practice either: Sonarr sends the title
// tier only after the tvdbid tier came back empty.
func TestGitea104_AmbiguousBareNameAnswersNothing(t *testing.T) {
	for _, stored := range [][]store.SeriesMapping{
		{gitea104Doctor2005, gitea104Doctor2023},
		{gitea104DoctorWho, gitea104Doctor2005},
	} {
		h, st := newHandlerWithBBCAndStore(t, gitea104TwoDoctorsPayload)
		gitea104Store(t, st, stored...)
		for _, q := range []string{"q=Doctor+Who&season=1&ep=2", "q=doctor+who&season=0&ep=5", "q=Doctor+Who"} {
			if items := gh58Serve(t, h, "t=tvsearch&"+q); len(items) != 0 {
				t.Errorf("stored %s and %s: %s returned %d items, first %q tvdbid=%q",
					stored[0].ShowName, stored[1].ShowName, q, len(items), items[0].Title, items[0].TVDBID)
			}
		}
		// The request's own tvdbid still wins over the store.
		if items := gh58Serve(t, h, "t=tvsearch&q=Doctor+Who&tvdbid=78804&season=1&ep=2"); len(items) == 0 {
			t.Errorf("q=Doctor Who with tvdbid=78804 returned nothing")
		}
	}
}

// With only one of them stored, the bare name is that series, as before
// (#31), and its items now carry the series' name.
func TestGitea104_OneStoredSeriesStillRehydrates(t *testing.T) {
	h, st := newHandlerWithBBCAndStore(t, gitea104TwoDoctorsPayload)
	gitea104Store(t, st, gitea104Doctor2005)
	gitea104Only(t, gh58Serve(t, h, "t=tvsearch&q=Doctor+Who&season=1&ep=2"),
		"ep_2005", "Doctor.Who.2005.S01E02.Series.1.Episode.2.720p.WEB-DL.AAC.H264-iParr", "78804")
}
