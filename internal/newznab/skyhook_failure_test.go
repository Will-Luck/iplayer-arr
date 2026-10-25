package newznab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// Gitea #104 items 2 and 3: what a tvdbid search answers when Skyhook
// fails. A guessed number that nothing can check is left out, and a
// search whose show name is unknown answers with nothing.

// gh104WarmDoctorWho stores the mapping a Doctor Who (2005) search
// leaves, so a search needs Skyhook only for the episode list.
func gh104WarmDoctorWho(t *testing.T) *store.Store {
	t.Helper()
	st := newTestStore(t)
	if err := st.PutSeriesMapping(&store.SeriesMapping{TVDBId: "78804", ShowName: "Doctor Who (2005)", Year: 2005}); err != nil {
		t.Fatal(err)
	}
	return st
}

// Without TheTVDB's list, Doctor Who (2005) S00E15 returned "A Christmas
// Carol" as S00E15, the number of "Dreamland", and S01E04 returned "The
// Runaway Bride" as S01E04 beside "Aliens of London": both numbers are
// guesses from the programme's place under the brand, the GitHub #60
// bug. Sonarr accepts a right number on the wrong programme. The BBC's
// own numbers still go out.
func TestGitea104_FailedListLeavesOutGuesses(t *testing.T) {
	ibl, _ := gh60Fixtures(t, http.StatusInternalServerError)
	h := gh60Handler(t, ibl, gh104WarmDoctorWho(t))

	if got := gh60Numbers(t, gh58Serve(t, h, "t=tvsearch&tvdbid=78804&season=0&ep=15")); len(got) != 0 {
		t.Errorf("S00E15 search returned %v, want nothing", got)
	}
	got := gh60Numbers(t, gh58Serve(t, h, "t=tvsearch&tvdbid=78804&season=1&ep=4"))
	if got["b0074dp9"] != "S01E04" {
		t.Errorf("S01E04 search: Aliens of London = %q, want S01E04 (all: %v)", got["b0074dp9"], got)
	}
	if se, ok := got["b0074g6q"]; ok {
		t.Errorf("S01E04 search returned The Runaway Bride as %s", se)
	}

	got = gh60Numbers(t, gh58Serve(t, h, "t=tvsearch&tvdbid=78804"))
	gh60Check(t, got, map[string]string{
		"b0074dlv": "S01E01", // Rose, "Series 1: 1. Rose"
		"b0074dp9": "S01E04",
	})
	for _, pid := range []string{"b00wyj5p", "b0074g6q", "b0074f9p"} {
		if se, ok := got[pid]; ok {
			t.Errorf("bare search returned guessed %s as %s", pid, se)
		}
	}
}

// An empty list is more likely Skyhook mid-refresh than a series with no
// episodes (SkyhookEpisodes retries it as soon as a failure), so it
// checks no guess either.
func TestGitea104_EmptyListLeavesOutGuesses(t *testing.T) {
	ibl, _ := gh60Fixtures(t, http.StatusOK)
	h := NewHandler(ibl, gh104WarmDoctorWho(t), nil, nil)
	h.SetEpisodeSource(extraEpisodes{id: "78804", eps: []TVDBEpisode{}, next: NewSkyhookEpisodes()})
	if got := gh60Numbers(t, gh58Serve(t, h, "t=tvsearch&tvdbid=78804&season=0&ep=15")); len(got) != 0 {
		t.Errorf("S00E15 search returned %v, want nothing", got)
	}
}

// A Show Override that changes a guessed programme's title is the user's
// numbering, so it goes out as with TheTVDB's list.
func TestGitea104_FailedListKeepsOverriddenGuess(t *testing.T) {
	ibl, _ := gh60Fixtures(t, http.StatusInternalServerError)
	st := newTestStore(t)
	if err := st.PutSeriesMapping(&store.SeriesMapping{TVDBId: "76578", ShowName: "Porridge", Year: 1974}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutOverride(&store.ShowOverride{ShowName: "Porridge", ForceSeriesNum: 9}); err != nil {
		t.Fatal(err)
	}
	h := gh60Handler(t, ibl, st)
	gh60Check(t, gh60Numbers(t, gh58Serve(t, h, "t=tvsearch&tvdbid=76578")), map[string]string{
		"b00789bm": "S09E01", // the 1973 pilot, position 1
	})
}

// The RSS feed leaves a programme alone when one of its stored series'
// lists is unavailable. Its BBC numbers go out, its guessed ones do not:
// Sonarr's RSS sync grabs any item that maps to a missing episode, and
// "Porridge.S00E04" maps to "The Porridge Movie: Doing Time".
func TestGitea104_BrowseFeedLeavesOutGuessesWhenListUnavailable(t *testing.T) {
	gh60Fixtures(t, http.StatusOK)
	ibl := gh60BrowseIBL(t, []string{"b007894v", "b007sf76", "b00789bm", "m0026d24"})
	h := NewHandler(ibl, gh60SearchedShows(t), nil, nil)
	h.SetEpisodeSource(failingEpisodes{fail: "76578", next: NewSkyhookEpisodes()})
	items := gh58Serve(t, h, "t=tvsearch")
	for _, it := range items {
		if it.PID == "b007894v" || it.PID == "b00789bm" {
			t.Errorf("guessed %s emitted: %q", it.PID, it.Title)
		}
	}
	gh60CheckItems(t, items, map[string]gh60Want{
		"b007sf76": {"Porridge.S01E01.", ""},
		// Both Doctor Who lists loaded: numbered as usual.
		"m0026d24": {"Doctor.Who.2023.S00E05.", "449991"},
	})
}

// gh104LookupFixture serves an iBL with one programme for a search for
// "Porridge" and one for "BBC", counting searches, and a Skyhook that
// answers every request with status, counting those.
func gh104LookupFixture(t *testing.T, status int) (h *Handler, iblHits, skyHits *atomic.Int32) {
	t.Helper()
	iblHits, skyHits = new(atomic.Int32), new(atomic.Int32)
	searches := map[string]string{
		"Porridge": `{"id":"p0000105","type":"episode","title":"Porridge","subtitle":"Series 1: 1. New Faces, Old Hands","release_date":"1974-09-05"}`,
		"BBC":      `{"id":"p0000104","type":"episode","title":"BBC Comedy Short Films","subtitle":"Series 1: 1. A Film","release_date":"2026-01-02"}`,
	}
	iblSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		iblHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"new_search":{"results":[` + searches[r.URL.Query().Get("q")] + `]}}`))
	}))
	t.Cleanup(iblSrv.Close)
	sky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		skyHits.Add(1)
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Write([]byte(`{"title":"Porridge","firstAired":"1974-09-05"}`))
	}))
	t.Cleanup(sky.Close)
	old := skyhookBaseURL
	skyhookBaseURL = sky.URL
	t.Cleanup(func() { skyhookBaseURL = old })

	ibl := bbc.NewIBL(bbc.NewClient())
	ibl.BaseURL = iblSrv.URL
	return NewHandler(ibl, newTestStore(t), nil, nil), iblHits, skyHits
}

// With no stored series and the Skyhook show lookup failing, the search
// went to iBL for "BBC" with no name filter, and every item carried the
// request's tvdbid, so Sonarr took "BBC Comedy Short Films" S01E01 as the
// searched series' S01E01. It now answers with nothing.
func TestGitea104_UnknownShowAnswersNothing(t *testing.T) {
	h, iblHits, _ := gh104LookupFixture(t, http.StatusInternalServerError)
	for _, q := range []string{"t=tvsearch&tvdbid=76578&season=1&ep=1", "t=tvsearch&tvdbid=76578&season=1", "t=tvsearch&tvdbid=76578"} {
		if items := gh58Serve(t, h, q); len(items) != 0 {
			t.Errorf("%s: got %d items (%q ...), want none", q, len(items), items[0].Title)
		}
	}
	if n := iblHits.Load(); n != 0 {
		t.Errorf("iBL searched %d times for a show with no name", n)
	}
}

// A mapping stored without a year (v1.0.2 and v1.1.0 stored none) is
// looked up again for the year. When that fails, the stored name still
// says which show it is.
func TestGitea104_FailedLookupUsesStoredNameWithoutYear(t *testing.T) {
	h, _, _ := gh104LookupFixture(t, http.StatusInternalServerError)
	if err := h.store.PutSeriesMapping(&store.SeriesMapping{TVDBId: "76578", ShowName: "Porridge"}); err != nil {
		t.Fatal(err)
	}
	items := gh58Serve(t, h, "t=tvsearch&tvdbid=76578&season=1&ep=1")
	gh58AssertTitle(t, items, "p0000105",
		"Porridge.S01E01.New.Faces.Old.Hands.720p.WEB-DL.AAC.H264-iParr")
	for _, it := range items {
		if it.PID != "p0000105" {
			t.Errorf("emitted %s %q", it.PID, it.Title)
		}
	}
}

// A failed show lookup is remembered for skyhookEpisodesFailTTL, as a
// failed episode list is: Sonarr searches a newly added series one
// season at a time, and each search waited on Skyhook again.
func TestGitea104_FailedLookupIsCached(t *testing.T) {
	h, _, skyHits := gh104LookupFixture(t, http.StatusInternalServerError)
	gh58Serve(t, h, "t=tvsearch&tvdbid=76578&season=1")
	gh58Serve(t, h, "t=tvsearch&tvdbid=76578&season=2")
	if n := skyHits.Load(); n != 1 {
		t.Fatalf("two searches made %d Skyhook requests, want 1", n)
	}
	gh58Serve(t, h, "t=tvsearch&tvdbid=78804&season=1")
	if n := skyHits.Load(); n != 2 {
		t.Errorf("another series made %d Skyhook requests in all, want 2", n)
	}
}

// After skyhookEpisodesFailTTL the lookup is tried again, and a name it
// finds is stored, so it is not asked for again.
func TestGitea104_FailedLookupRetriedAfterTTL(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusInternalServerError)
	var hits atomic.Int32
	sky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if s := int(status.Load()); s != http.StatusOK {
			w.WriteHeader(s)
			return
		}
		w.Write([]byte(`{"title":"Porridge","firstAired":"1974-09-05"}`))
	}))
	defer sky.Close()
	old := skyhookBaseURL
	skyhookBaseURL = sky.URL
	defer func() { skyhookBaseURL = old }()

	h := newHandlerWithBBC(t, `{"new_search":{"results":[]}}`)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	h.shows.now = func() time.Time { return now }

	gh58Serve(t, h, "t=tvsearch&tvdbid=76578&season=1")
	status.Store(http.StatusOK)
	now = now.Add(skyhookEpisodesFailTTL - time.Second)
	gh58Serve(t, h, "t=tvsearch&tvdbid=76578&season=1")
	if n := hits.Load(); n != 1 {
		t.Fatalf("inside the failure TTL: %d Skyhook requests, want 1", n)
	}
	now = now.Add(time.Second)
	gh58Serve(t, h, "t=tvsearch&tvdbid=76578&season=1")
	if m, _ := h.store.GetSeriesMapping("76578"); hits.Load() != 2 || m == nil || m.ShowName != "Porridge" {
		t.Fatalf("after the failure TTL: %d Skyhook requests, mapping %+v; want 2 and Porridge", hits.Load(), m)
	}
	gh58Serve(t, h, "t=tvsearch&tvdbid=76578&season=2")
	if n := hits.Load(); n != 2 {
		t.Errorf("with the name stored: %d Skyhook requests, want 2", n)
	}
}

// The show lookup used http.Get, which has no timeout and ignores the
// request: a Skyhook that accepted the connection and never answered held
// the search after Sonarr had given up. A cancelled lookup says nothing
// about Skyhook, so it is not remembered as a failure.
func TestGitea104_LookupStopsWithTheSearch(t *testing.T) {
	release := make(chan struct{})
	var hits atomic.Int32
	sky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			select {
			case <-release:
			case <-time.After(5 * time.Second):
			}
			return
		}
		w.Write([]byte(`{"title":"Porridge","firstAired":"1974-09-05"}`))
	}))
	defer sky.Close()
	defer close(release)
	old := skyhookBaseURL
	skyhookBaseURL = sky.URL
	defer func() { skyhookBaseURL = old }()

	iblSrv := fakeBBCSearchServer(t, `{"new_search":{"results":[]}}`)
	ibl := bbc.NewIBL(bbc.NewClient())
	ibl.BaseURL = iblSrv.URL
	h := NewHandler(ibl, newTestStore(t), nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch&tvdbid=76578&season=1&apikey="+testStoreAPIKey, nil).WithContext(ctx)
	start := time.Now()
	h.ServeHTTP(httptest.NewRecorder(), req)
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("search took %v after Sonarr gave up at 200ms", d)
	}

	gh58Serve(t, h, "t=tvsearch&tvdbid=76578&season=1")
	if m, _ := h.store.GetSeriesMapping("76578"); m == nil || m.ShowName != "Porridge" {
		t.Errorf("after a cancelled lookup, the next search stored %+v, want Porridge", m)
	}
}
