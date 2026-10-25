package newznab

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// Found by the review of the Gitea #104 work: an out-of-sample survey of
// 27 shows the rules were not tuned on, and a code review.

func gitea104Skyhook(t *testing.T, payload string) {
	t.Helper()
	sky := fakeSkyhookServer(t, payload)
	old := skyhookBaseURL
	skyhookBaseURL = sky.URL
	t.Cleanup(func() { skyhookBaseURL = old })
}

// iPlayer's bare "Ghosts" is the BBC series. A search for Ghosts (US)
// returned its series 3 to 5 at the US series' numbers, and they went out
// as Ghosts.US.*, which Sonarr would import as the US episodes. A
// programme of another country's series is left out unless TheTVDB
// numbers it; a UK series keeps the BBC's bare brand.
func TestGitea104_OtherCountrysSeriesLeftOut(t *testing.T) {
	const payload = `{"new_search":{"results":[
		{"id":"p0000301","type":"episode","title":"Some Show","subtitle":"Series 4: Episode 1","release_date":"2022-09-23"},
		{"id":"p0000302","type":"episode","title":"Some Show","subtitle":"Series 4: 2. The Return","release_date":"2024-10-17"}
	]}}`
	gitea104Skyhook(t, `{"title":"Some Show (US)","firstAired":"2021-10-07","episodes":[
		{"seasonNumber":1,"episodeNumber":1,"title":"Pilot","airDate":"2021-10-07"},
		{"seasonNumber":4,"episodeNumber":1,"title":"The Wedding","airDate":"2024-10-10"},
		{"seasonNumber":4,"episodeNumber":2,"title":"The Return","airDate":"2024-10-17"}
	]}`)
	h, _ := newHandlerWithBBCAndStore(t, payload)
	h.SetEpisodeSource(NewSkyhookEpisodes())
	if items := gh58Serve(t, h, "t=tvsearch&tvdbid=99003&season=4&ep=1"); len(items) != 0 {
		t.Errorf("S04E01: the BBC series' episode answered the US series' search: %q", items[0].Title)
	}
	// TheTVDB's own title within a week: the episode is the series'.
	gh58AssertTitle(t, gh58Serve(t, h, "t=tvsearch&tvdbid=99003&season=4&ep=2"), "p0000302",
		"Some.Show.US.S04E02.The.Return.720p.WEB-DL.AAC.H264-iParr")
}

func TestGitea104_UKSeriesKeepsTheBareBrand(t *testing.T) {
	const payload = `{"new_search":{"results":[
		{"id":"p0000311","type":"episode","title":"Some Show","subtitle":"Series 4: Episode 1","release_date":"2022-09-23"}
	]}}`
	gitea104Skyhook(t, `{"title":"Some Show (UK)","firstAired":"2019-10-07","episodes":[
		{"seasonNumber":4,"episodeNumber":1,"title":"Episode 1","airDate":"2022-09-23"}
	]}`)
	h, _ := newHandlerWithBBCAndStore(t, payload)
	h.SetEpisodeSource(NewSkyhookEpisodes())
	gh58AssertTitle(t, gh58Serve(t, h, "t=tvsearch&tvdbid=99004&season=4&ep=1"), "p0000311",
		"Some.Show.UK.S04E01.Series.4.Episode.1.720p.WEB-DL.AAC.H264-iParr")
}

// The in-era brand "Doctor Who (2005-2022)" is among iBL's search hits
// but its episode listing failed, so only the bare "Doctor Who" brand,
// Doctor Who (2023)'s, is left. It is of another era all the same: with
// b006q2x0's listing failing, no episode of p0gglvqn answers a Doctor Who
// (2005) search, except the 60th anniversary specials TheTVDB numbers
// for it.
func TestGitea104_CapturedInEraListingFailure(t *testing.T) {
	fixtures, _ := gh60Fixtures(t, http.StatusOK)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/programmes/b006q2x0/") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		resp, err := http.Get(fixtures.BaseURL + r.URL.RequestURI())
		if err != nil {
			t.Error(err)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		buf := make([]byte, 32<<10)
		for {
			n, err := resp.Body.Read(buf)
			w.Write(buf[:n])
			if err != nil {
				break
			}
		}
	}))
	t.Cleanup(srv.Close)
	ibl := bbc.NewIBL(bbc.NewClient())
	ibl.BaseURL = srv.URL
	h := gh60Handler(t, ibl, newTestStore(t))
	pids := gh60BrandPIDs(t, "p0gglvqn")
	specials := map[string]bool{}
	for _, it := range gh58Serve(t, h, "t=tvsearch&tvdbid=78804") {
		if !pids[it.PID] {
			continue
		}
		if !strings.HasPrefix(it.Title, "Doctor.Who.2005.S00E16") {
			t.Errorf("Doctor Who (2023) programme %s answered Doctor Who (2005): %q", it.PID, it.Title)
			continue
		}
		specials[it.PID] = true
	}
	for _, pid := range []string{"m001sx3h", "m001t42d", "m001tc3r"} {
		if !specials[pid] {
			t.Errorf("60th anniversary special %s missing", pid)
		}
	}
}

// A mapping stored without a year (v1.0.2, v1.1.0) and a failing Skyhook
// lookup leave no year for the era check, though the series' name has it.
func TestGitea104_YearlessMappingTakesTheYearFromTheName(t *testing.T) {
	const payload = `{"new_search":{"results":[
		{"id":"ep_2005","type":"episode","title":"Doctor Who (2005–2022)","subtitle":"Series 1: Episode 2","release_date":"2005-04-02"},
		{"id":"ep_2024","type":"episode","title":"Doctor Who","subtitle":"Season 1: Episode 2","release_date":"2024-05-11"}
	]}}`
	sky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(sky.Close)
	old := skyhookBaseURL
	skyhookBaseURL = sky.URL
	t.Cleanup(func() { skyhookBaseURL = old })
	h, st := newHandlerWithBBCAndStore(t, payload)
	h.SetEpisodeSource(NewSkyhookEpisodes())
	if err := st.PutSeriesMapping(&store.SeriesMapping{TVDBId: "78804", ShowName: "Doctor Who (2005)"}); err != nil {
		t.Fatal(err)
	}
	items := gh58Serve(t, h, "t=tvsearch&tvdbid=78804&season=1&ep=2")
	for _, it := range items {
		if it.PID == "ep_2024" {
			t.Errorf("Doctor Who (2023)'s episode answered Doctor Who (2005) S01E02: %q", it.Title)
		}
	}
	gh58AssertTitle(t, items, "ep_2005", "Doctor.Who.2005.S01E02.Series.1.Episode.2.720p.WEB-DL.AAC.H264-iParr")
}

// Sonarr's title tier (q=, sent after the tvdbid tier found nothing)
// searches iBL for the name Sonarr sent, as v1.9.1 did: the tvdbid tier
// has just searched for the stored name. The stored name still filters.
func TestGitea104_TitleTierSearchesTheNameSonarrSent(t *testing.T) {
	const ghosts = `{"new_search":{"results":[
		{"id":"p0000401","type":"episode","title":"Ghosts","subtitle":"Series 1: Episode 1","release_date":"2019-04-15"}
	]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/new-search" && r.URL.Query().Get("q") == "Ghosts" {
			w.Write([]byte(ghosts))
			return
		}
		w.Write([]byte(`{"new_search":{"results":[]}}`))
	}))
	t.Cleanup(srv.Close)
	ibl := bbc.NewIBL(bbc.NewClient())
	ibl.BaseURL = srv.URL
	st := newTestStore(t)
	if err := st.PutSeriesMapping(&store.SeriesMapping{TVDBId: "361701", ShowName: "Ghosts (2019)", Year: 2019}); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(ibl, st, nil, nil)
	if items := gh58Serve(t, h, "t=tvsearch&tvdbid=361701&season=1&ep=1"); len(items) != 0 {
		t.Fatalf("premise: the tvdbid tier should find nothing, got %d", len(items))
	}
	items := gh58Serve(t, h, "t=tvsearch&q=Ghosts&season=1&ep=1")
	gh58AssertTitle(t, items, "p0000401", "Ghosts.2019.S01E01.Series.1.Episode.1.720p.WEB-DL.AAC.H264-iParr")
	for _, it := range items {
		if it.TVDBID != "361701" {
			t.Errorf("%s carries tvdbid %q, want 361701", it.PID, it.TVDBID)
		}
	}
}

type gitea104FailingEpisodes struct{}

func (gitea104FailingEpisodes) Episodes(ctx context.Context, tvdbid string) ([]TVDBEpisode, error) {
	return nil, errors.New("skyhook down")
}

// The quality probe asks the BBC about each item it is given, so it is
// given only the items that go out.
func TestGitea104_LeftOutItemsAreNotProbed(t *testing.T) {
	const payload = `{"new_search":{"results":[
		{"id":"p0000501","type":"episode","title":"Some Show","subtitle":"Christmas Special","release_date":"2020-12-25","parent_position":7},
		{"id":"p0000502","type":"episode","title":"Some Show","subtitle":"Series 1: Episode 7","release_date":"2019-11-01","parent_position":7}
	]}}`
	prober := &mockProber{results: map[string][]int{}}
	h, st := newHandlerWithBBCProberAndStore(t, payload, prober)
	h.SetEpisodeSource(gitea104FailingEpisodes{})
	if err := st.PutSeriesMapping(&store.SeriesMapping{TVDBId: "99005", ShowName: "Some Show", Year: 2019}); err != nil {
		t.Fatal(err)
	}
	items := gh58Serve(t, h, "t=tvsearch&tvdbid=99005")
	for _, it := range items {
		if it.PID == "p0000501" {
			t.Fatalf("premise: the guessed special should be left out, got %q", it.Title)
		}
	}
	for _, call := range prober.calls {
		for _, pi := range call {
			if pi.PID == "p0000501" {
				t.Errorf("left-out %s was sent to the prober", pi.PID)
			}
		}
	}
}

// Naruto reached iPlayer in 2026, 23 years after TheTVDB's dates, as
// "Part I: N. Title" with no series, so Series 1 is only assumed. Its
// episodes were left out where TheTVDB's title differs: by a macron
// ("Chūnin"), by translation, or because the BBC gave three episodes one
// title. When TheTVDB bears out others of the same label at their own
// numbers, the label's numbers are the series' and stand.
func TestGitea104_BoughtInSeriesBorneOutByItsSiblings(t *testing.T) {
	all := func(*store.Programme) bool { return true }
	eps := []TVDBEpisode{
		{Season: 1, Episode: 1, Title: "Enter: Naruto Uzumaki!", AirDate: "2002-10-03"},
		{Season: 1, Episode: 2, Title: "My Name is Konohamaru!", AirDate: "2002-10-10"},
		{Season: 1, Episode: 3, Title: "Sasuke and Sakura: Friends or Foes?", AirDate: "2002-10-17"},
		{Season: 1, Episode: 13, Title: "Demonic Mirroring Ice Crystals", AirDate: "2002-12-26"},
		{Season: 1, Episode: 16, Title: "The Broken Seal", AirDate: "2003-01-22"},
		{Season: 1, Episode: 17, Title: "White Past: Hidden Ambition", AirDate: "2003-01-29"},
		{Season: 1, Episode: 18, Title: "The Weapons Known as Shinobi", AirDate: "2003-02-05"},
		{Season: 1, Episode: 20, Title: "A New Chapter Begins: The Chūnin Exam!", AirDate: "2003-02-19"},
	}
	type prog struct {
		pid, sub string
		n        int
		want     string
	}
	progs := []prog{
		{"m002qk01", "Part I: 1. Enter: Naruto Uzumaki!", 1, "S01E01"},
		{"m002qk02", "Part I: 2. My Name is Konohamaru!", 2, "S01E02"},
		{"m002qk03", "Part I: 3. Sasuke and Sakura: Friends or Foes?", 3, "S01E03"},
		{"m002qk13", "Part I: 13. Crystal Ice Mirrors", 13, ""},
		{"m002qk16", "Part I: 16. The Broken Seal", 16, "S01E16"},
		{"m002qk17", "Part I: 17. The Broken Seal", 17, ""},
		{"m002qk18", "Part I: 18. The Broken Seal", 18, ""},
		{"m002qkrb", "Part I: 20. A New Chapter Begins: The Chunin Exam!", 20, "S01E20"},
		// TheTVDB splits Part I into seasons, so it has no S01E131 to
		// bear these out or contradict them: they do not count.
		{"m002r7j1", "Part I: 131. Episode 131", 131, ""},
		{"m002r7j2", "Part I: 132. Episode 132", 132, ""},
		{"m002r7j3", "Part I: 133. Episode 133", 133, ""},
		{"m002r7j4", "Part I: 134. Episode 134", 134, ""},
		{"m002r7j5", "Part I: 135. Episode 135", 135, ""},
		{"m002r7j6", "Part I: 136. Episode 136", 136, ""},
		// No episode of this label is borne out: Boruto is another series.
		{"m002qb01", "Boruto: 1. Uzumaki Boruto!", 1, "out"},
		{"m002qb02", "Boruto: 2. The Hokage's Son...!", 2, "out"},
	}
	var ps []*store.Programme
	for _, p := range progs {
		ps = append(ps, &store.Programme{PID: p.pid, Name: "Naruto", Episode: p.sub, Series: 1, SeriesAssumed: true, EpisodeNum: p.n, Position: p.n, AirDate: "2026-02-01"})
	}
	newTVDBIndex(eps).numberFromTVDB(ps, all, all)
	for i, p := range ps {
		if got := gitea104Outcome(p); got != progs[i].want {
			t.Errorf("%s %q gave %q, want %q", p.PID, p.Episode, got, progs[i].want)
		}
	}
}

func TestGitea104_MacronsFold(t *testing.T) {
	if a, b := episodeTitleKey("The Chūnin Exam"), episodeTitleKey("The Chunin Exam"); a != b {
		t.Errorf("episodeTitleKey: %q != %q", a, b)
	}
	if got := sanitiseForTitle("Chūnin Ōkami"); got != "Chunin.Okami" {
		t.Errorf("sanitiseForTitle = %q, want Chunin.Okami", got)
	}
}

// iPlayer's Teletubbies are the 2015 series. Three of its episodes share
// a title with one of the 1997 series' (TheTVDB 72662), at any date. That
// does not make the 2015 programmes the 1997 series': only a match within
// tvdbMatchWindow bears the show out, so the rest, aired more than ten
// years after its run, are another show of the name.
func TestGitea104_OffWindowTitleDoesNotBearOutTheShow(t *testing.T) {
	all := func(*store.Programme) bool { return true }
	eps := []TVDBEpisode{
		{Season: 1, Episode: 4, Title: "Ducks", AirDate: "1997-04-03"},
		{Season: 1, Episode: 7, Title: "Ballet", AirDate: "1997-04-08"},
		{Season: 1, Episode: 40, Title: "Bubbles", AirDate: "1997-05-26"},
		{Season: 4, Episode: 60, Title: "Wobbly Day", AirDate: "2001-01-05"},
	}
	progs := []*store.Programme{
		{PID: "b06pydmh", Name: "Teletubbies", Episode: "Series 1: 7. Bubbles", Series: 1, EpisodeNum: 7, Position: 7, AirDate: "2015-11-16"},
		{PID: "b06ptqy4", Name: "Teletubbies", Episode: "Series 1: 4. Tiddlytubbies", Series: 1, EpisodeNum: 4, Position: 4, AirDate: "2015-11-12"},
	}
	newTVDBIndex(eps).numberFromTVDB(progs, all, all)
	for _, p := range progs {
		if got := gitea104Outcome(p); got != "out" {
			t.Errorf("%s %q gave %q, want out: the 2015 series is another show of the name", p.PID, p.Episode, got)
		}
	}
}

// iBL can list one programme twice, under its brand and its series. A
// programme counts once, in the label rule and in a tie.
func TestGitea104_ProgrammeListedTwiceCountsOnce(t *testing.T) {
	all := func(*store.Programme) bool { return true }
	mk := func(pid, sub string, n int) *store.Programme {
		return &store.Programme{PID: pid, Name: "Some Show", Episode: sub, Series: 1, SeriesAssumed: true, EpisodeNum: n, Position: n, AirDate: "2026-02-01"}
	}
	t.Run("label rule", func(t *testing.T) {
		eps := []TVDBEpisode{
			{Season: 1, Episode: 1, Title: "Alpha Harbour", AirDate: "2003-01-01"},
			{Season: 1, Episode: 2, Title: "Beta Mountain", AirDate: "2003-01-08"},
			{Season: 1, Episode: 3, Title: "Gamma Forest", AirDate: "2003-01-15"},
			{Season: 1, Episode: 4, Title: "Delta River", AirDate: "2003-01-22"},
		}
		progs := []*store.Programme{
			mk("x1", "Next: 1. Alpha Harbour", 1), mk("x1", "Next: 1. Alpha Harbour", 1),
			mk("x2", "Next: 2. Beta Mountain", 2), mk("x2", "Next: 2. Beta Mountain", 2),
			mk("x3", "Next: 3. Something Else Entirely", 3),
			mk("x4", "Next: 4. Another Thing Again", 4),
		}
		newTVDBIndex(eps).numberFromTVDB(progs, all, all)
		for _, p := range progs[4:] {
			if got := gitea104Outcome(p); got != "out" {
				t.Errorf("%s %q gave %q, want out: only two of the label's four are borne out", p.PID, p.Episode, got)
			}
		}
	})
	t.Run("tie", func(t *testing.T) {
		eps := []TVDBEpisode{
			{Season: 1, Episode: 16, Title: "The Broken Seal", AirDate: "2003-01-22"},
			{Season: 1, Episode: 17, Title: "White Past: Hidden Ambition", AirDate: "2003-01-29"},
		}
		progs := []*store.Programme{
			mk("q16", "Part I: 16. The Broken Seal", 16), mk("q16", "Part I: 16. The Broken Seal", 16),
			mk("q17", "Part I: 17. The Broken Seal", 17),
		}
		newTVDBIndex(eps).numberFromTVDB(progs, all, all)
		for _, p := range progs[:2] {
			if got := gitea104Outcome(p); got != "S01E16" {
				t.Errorf("%s %q gave %q, want S01E16", p.PID, p.Episode, got)
			}
		}
	})
}
