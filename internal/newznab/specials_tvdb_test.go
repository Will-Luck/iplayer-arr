package newznab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// GitHub #60. Specials came out with BBC's position as their episode
// number (Porridge's Christmas specials as S00E04 and S00E05, TheTVDB
// S00E02 and S00E03), or in Series 1 (Doctor Who's "The Runaway Bride"
// as S01E04, the number of "Aliens of London"), so Sonarr grabbed them
// as other episodes. A tvdbid search now numbers every episode whose
// title and air date match TheTVDB from TheTVDB.
//
// Fixtures under testdata/gh60 are real iBL and Skyhook responses
// captured on 2026-10-24 and trimmed; testdata/gh60/README.md says how.

const gh60Dir = "testdata/gh60"

// gh60Fixtures serves the testdata/gh60 iBL searches and listings, and
// points skyhookBaseURL at the Skyhook files. skyhookStatus, when not
// 200, is returned for every Skyhook request instead. It returns the
// number of Skyhook requests served so far.
func gh60Fixtures(t *testing.T, skyhookStatus int) (*bbc.IBL, *atomic.Int32) {
	t.Helper()
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join(gh60Dir, name))
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		return b
	}
	// Live iBL returned the same brands for "Doctor Who 2005", Sonarr's
	// title-tier query, as for "Doctor Who (2005)" (2026-10-25), and the
	// same for 2023.
	searches := map[string][]byte{
		"Porridge":          read("search_Porridge.json"),
		"Doctor Who (2005)": read("search_Doctor_Who_2005.json"),
		"Doctor Who 2005":   read("search_Doctor_Who_2005.json"),
		"Doctor Who (2023)": read("search_Doctor_Who_2023.json"),
		"Doctor Who 2023":   read("search_Doctor_Who_2023.json"),
	}
	episodes := map[string][]byte{}
	for _, pid := range []string{"b006m9kn", "p05dsmwl", "b006q2x0", "p0gglvqn", "p0ggwr8l", "m001sx3r"} {
		episodes[pid] = read("episodes_" + pid + ".json")
	}
	skyhook := map[string][]byte{}
	for _, id := range []string{"76578", "78804", "449991"} {
		skyhook[id] = read("skyhook_" + id + ".json")
	}

	iblSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/new-search" {
			if b, ok := searches[r.URL.Query().Get("q")]; ok {
				w.Write(b)
				return
			}
			w.Write([]byte(`{"new_search":{"results":[]}}`))
			return
		}
		if pid, ok := strings.CutPrefix(r.URL.Path, "/programmes/"); ok {
			pid = strings.TrimSuffix(pid, "/episodes")
			if b, ok := episodes[pid]; ok && r.URL.Query().Get("page") == "1" {
				w.Write(b)
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(iblSrv.Close)

	var skyhookHits atomic.Int32
	skySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		skyhookHits.Add(1)
		if skyhookStatus != http.StatusOK {
			w.WriteHeader(skyhookStatus)
			return
		}
		id, _ := strings.CutPrefix(r.URL.Path, "/v1/tvdb/shows/en/")
		b, ok := skyhook[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	}))
	t.Cleanup(skySrv.Close)
	old := skyhookBaseURL
	skyhookBaseURL = skySrv.URL
	t.Cleanup(func() { skyhookBaseURL = old })

	ibl := bbc.NewIBL(bbc.NewClient())
	ibl.BaseURL = iblSrv.URL
	return ibl, &skyhookHits
}

// gh60Handler is a handler with the Skyhook episode source, as main.go
// wires it.
func gh60Handler(t *testing.T, ibl *bbc.IBL, st *store.Store) *Handler {
	t.Helper()
	h := NewHandler(ibl, st, nil, nil)
	h.SetEpisodeSource(NewSkyhookEpisodes())
	return h
}

// gh60Numbers maps each emitted PID to the SxxEyy of its titles. Every
// quality of a PID must carry the same numbering.
func gh60Numbers(t *testing.T, items []gh58Item) map[string]string {
	t.Helper()
	got := map[string]string{}
	for _, it := range items {
		id := ParseTitle(it.Title)
		se := "none"
		if id.Episode > 0 {
			se = fmt.Sprintf("S%02dE%02d", id.Season, id.Episode)
		}
		if prev, ok := got[it.PID]; ok && prev != se {
			t.Fatalf("PID %s emitted as both %s and %s", it.PID, prev, se)
		}
		got[it.PID] = se
	}
	return got
}

func gh60Check(t *testing.T, got map[string]string, want map[string]string) {
	t.Helper()
	for pid, se := range want {
		if got[pid] != se {
			t.Errorf("PID %s numbered %q, want %s", pid, got[pid], se)
		}
	}
}

func TestGH60_PorridgeSpecialsTakeTVDBNumbers(t *testing.T) {
	ibl, _ := gh60Fixtures(t, http.StatusOK)
	h := gh60Handler(t, ibl, newTestStore(t))
	items := gh58Serve(t, h, "t=tvsearch&tvdbid=76578")

	gh60Check(t, gh60Numbers(t, items), map[string]string{
		// The 1973 pilot: was S01E01, the number of "New Faces, Old Hands".
		"b00789bm": "S00E01",
		// "Christmas Special: No Way Out" and "...The Desperate Hours":
		// were S00E04 and S00E05, their position under the brand.
		"b007894v": "S00E02",
		"b0078956": "S00E03",
		// Regular episodes keep the numbers they already had.
		"b007sf76": "S01E01",
		"b007871w": "S02E01",
		"b00787vf": "S03E06",
	})
	for _, it := range items {
		if it.PID == "b007894v" && !strings.HasPrefix(it.Title, "Porridge.S00E02.Christmas.Special.No.Way.Out.") {
			t.Errorf("No Way Out title = %q", it.Title)
		}
	}
}

func TestGH60_DoctorWho2005SpecialsTakeTVDBNumbers(t *testing.T) {
	ibl, _ := gh60Fixtures(t, http.StatusOK)
	h := gh60Handler(t, ibl, newTestStore(t))
	items := gh58Serve(t, h, "t=tvsearch&tvdbid=78804")

	gh60Check(t, gh60Numbers(t, items), map[string]string{
		// Brand-level specials were S01E<position>, colliding with Series 1.
		"b0074g6q": "S00E04",  // The Runaway Bride (was S01E04, Aliens of London)
		"b008lyb2": "S00E07",  // Voyage of the Damned (was S01E06, Dalek)
		"b00gd1mr": "S00E09",  // The Next Doctor
		"b00jz2t4": "S00E13",  // Planet of the Dead
		"b00p05x8": "S00E14",  // The Waters of Mars
		"b00pk651": "S00E16",  // The End of Time - Part One; TheTVDB "(1)"
		"b00pk7ls": "S00E17",  // The End of Time - Part Two
		"b018nrhp": "S00E48",  // The Doctor, the Widow and the Wardrobe
		"p01l1z04": "S00E83",  // The Day of the Doctor
		"p01mj6k8": "S00E84",  // The Time of the Doctor
		"b06tl32t": "S00E148", // The Husbands of River Song
		"b086tqm4": "S00E149", // The Return of Doctor Mysterio
		"b09kkt1m": "S00E154", // Twice Upon a Time
		"b0bx2rj3": "S00E156", // Resolution
		"m000qr8k": "S00E157", // Revolution of the Daleks
		"m00134mm": "S00E158", // Eve of the Daleks
		"m0016lkw": "S00E159", // Legend of the Sea Devils
		"m001dhsb": "S00E161", // The Power of the Doctor
		// Keyword specials were S00E<position>.
		"b00wyj5p": "S00E34",  // A Christmas Carol (was S00E15, Dreamland)
		"p02ct985": "S00E142", // Last Christmas (was S00E25)
		"b0074f9p": "S00E02",  // The Christmas Invasion (already right)
		// BBC counts these inside a series; TheTVDB does not.
		"p011gpsb": "S00E66",  // "Series 7: 6. The Snowmen"
		"b01rryzz": "S07E06",  // "Series 7: 7. The Bells of Saint John"
		"b01skh8t": "S07E13",  // "Series 7: 14. The Name of the Doctor"
		"p0326pdj": "S00E146", // "Series 9: 1. New Series Prologue"
		// Regular episodes: matched or not, the numbers are BBC's.
		"b0074dlv": "S01E01", // Rose, matched
		"b0074dq8": "S01E06", // Dalek, matched
		"b0074dp9": "S01E04", // Aliens of London: TheTVDB has no plain "Aliens of London"
		"b06dpx8x": "S09E01", // The Magician's Apprentice, likewise
		// "Mini Episode - The Night of the Doctor" matches no TheTVDB
		// title, and TheTVDB has three specials on its air date: it takes
		// S00E82, "The Night of the Doctor", the one whose title its
		// subtitle contains. It was S01E20, BBC's position.
		"p01lhhv4": "S00E82",
	})
}

func TestGH60_DoctorWho2023SpecialsTakeTVDBNumbers(t *testing.T) {
	ibl, _ := gh60Fixtures(t, http.StatusOK)
	h := gh60Handler(t, ibl, newTestStore(t))
	items := gh58Serve(t, h, "t=tvsearch&tvdbid=449991")

	gh60Check(t, gh60Numbers(t, items), map[string]string{
		// "Specials: 1. The Star Beast" read "1." as the episode and
		// became S01E01, colliding with Space Babies.
		"m001sx3h": "S00E01",
		"m001t42d": "S00E02",
		"m001tc3r": "S00E03",
		// "Christmas Special: The Church on Ruby Road" was S00E02, the
		// number of Wild Blue Yonder.
		"m001tfnl": "S00E04",
		// "Joy to the World" was S01E04 (73 Yards).
		"m0026d24": "S00E05",
		"m001z8bz": "S01E01", // Space Babies
		"m002d3lr": "S02E08", // The Reality War
	})
}

// Sonarr searches one episode with season= and ep=. The TheTVDB numbers
// have to be in place before the filter, or the search for S07E06 finds
// "The Snowmen" (BBC's 7x06) and misses "The Bells of Saint John".
func TestGH60_EpisodeSearchUsesTVDBNumbers(t *testing.T) {
	ibl, _ := gh60Fixtures(t, http.StatusOK)
	h := gh60Handler(t, ibl, newTestStore(t))

	got := gh60Numbers(t, gh58Serve(t, h, "t=tvsearch&tvdbid=78804&season=7&ep=6"))
	if got["b01rryzz"] != "S07E06" {
		t.Errorf("S07E06 search: The Bells of Saint John = %q, want S07E06 (all: %v)", got["b01rryzz"], got)
	}
	if se, ok := got["p011gpsb"]; ok {
		t.Errorf("S07E06 search returned The Snowmen as %s", se)
	}
	for pid, se := range got {
		if se == "S07E06" && pid != "b01rryzz" {
			t.Errorf("S07E06 search also returned %s as S07E06", pid)
		}
	}

	// season=0 is the specials, so the special is all that comes back.
	// v1.9.0 did not filter season=0 by season and also returned every
	// other episode numbered 4.
	got = gh60Numbers(t, gh58Serve(t, h, "t=tvsearch&tvdbid=78804&season=0&ep=4"))
	if got["b0074g6q"] != "S00E04" {
		t.Errorf("S00E04 search: Runaway Bride = %q, want S00E04 (all: %v)", got["b0074g6q"], got)
	}
	for pid, se := range got {
		if pid != "b0074g6q" {
			t.Errorf("S00E04 search also returned %s as %s", pid, se)
		}
	}
}

// One Skyhook request serves the show lookup's cold path and one serves
// the episode list; a second search within the TTL makes no request.
func TestGH60_EpisodeListIsCached(t *testing.T) {
	ibl, hits := gh60Fixtures(t, http.StatusOK)
	h := gh60Handler(t, ibl, newTestStore(t))
	gh58Serve(t, h, "t=tvsearch&tvdbid=76578")
	if n := hits.Load(); n != 2 {
		t.Fatalf("first search made %d Skyhook requests, want 2 (show lookup, episode list)", n)
	}
	gh58Serve(t, h, "t=tvsearch&tvdbid=76578&season=1")
	if n := hits.Load(); n != 2 {
		t.Errorf("second search made %d more Skyhook requests, want 0", n-2)
	}
}

// When Skyhook fails, the search still answers with BBC's numbering, but
// not with a number iplayer-arr guessed: without TheTVDB's list nothing
// checks it, and the 1973 pilot's S01E01 and No Way Out's S00E04 are
// other episodes' numbers. Gitea #104.
func TestGH60_SkyhookFailureKeepsBBCNumbering(t *testing.T) {
	ibl, hits := gh60Fixtures(t, http.StatusInternalServerError)
	st := newTestStore(t)
	// A warm show mapping, so the search does not need Skyhook for the name.
	if err := st.PutSeriesMapping(&store.SeriesMapping{TVDBId: "76578", ShowName: "Porridge", Year: 1974}); err != nil {
		t.Fatal(err)
	}
	h := gh60Handler(t, ibl, st)
	got := gh60Numbers(t, gh58Serve(t, h, "t=tvsearch&tvdbid=76578"))
	gh60Check(t, got, map[string]string{
		"b007sf76": "S01E01",
		"b007871w": "S02E01",
	})
	for _, pid := range []string{"b00789bm", "b007894v", "b0078956"} {
		if se, ok := got[pid]; ok {
			t.Errorf("guessed %s emitted as %s", pid, se)
		}
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("Skyhook requests = %d, want 1 (the failed episode list)", n)
	}
}

// fixedEpisodes is an EpisodeSource with a set list.
type fixedEpisodes []TVDBEpisode

func (f fixedEpisodes) Episodes(context.Context, string) ([]TVDBEpisode, error) {
	return f, nil
}

// Only the searched show's episodes are numbered, so nothing is without
// a name. (When the tvdbid lookup failed, handleTVSearch searched iBL for
// "BBC" with no name; it now answers with nothing, Gitea #104.) A daily
// series' season search (season=<year>) keeps its date titles.
func TestGH60_NumbersOnlyTheNamedShow(t *testing.T) {
	h := NewHandler(nil, newTestStore(t), nil, nil)
	h.SetEpisodeSource(fixedEpisodes{{Season: 3, Episode: 1, Title: "Episode 1", AirDate: "2026-10-20"}})
	results := []bbc.IBLResult{{PID: "p0000001", Title: "Some Other Show", Subtitle: "Episode 1", Position: 1, AirDate: "2026-10-20"}}
	run := func(filterSeason int, filterName string) map[string]string {
		req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch", nil)
		w := httptest.NewRecorder()
		h.writeResultsRSS(w, req, results, nil, filterSeason, 0, "", filterName, 0, "12345", "", false, 0)
		return gh60Numbers(t, gh58ParseItems(t, w.Body.Bytes()))
	}
	if got := run(noSeasonFilter, "")["p0000001"]; got != "S01E01" {
		t.Errorf("unnamed search: numbered %s, want its own S01E01", got)
	}
	if got := run(noSeasonFilter, "Some Other Show")["p0000001"]; got != "S03E01" {
		t.Errorf("named search: numbered %s, want S03E01", got)
	}
	if got, ok := run(2026, "Some Other Show")["p0000001"]; ok && got == "S03E01" {
		t.Errorf("daily season search numbered it from TheTVDB")
	}
}

// Two programmes matching the same TheTVDB episode: one matched on its
// whole title keeps the number, one matched only on what follows a label
// does not; two label-only matches both keep BBC numbering.
func TestGH60_SharedEpisodeGoesToTheFullMatch(t *testing.T) {
	idx := newTVDBIndex([]TVDBEpisode{
		{Season: 7, Episode: 6, Title: "The Bells of Saint John", AirDate: "2013-03-30"},
		{Season: 0, Episode: 2, Title: "No Way Out", AirDate: "1975-12-24"},
	})
	all := func(*store.Programme) bool { return true }

	real := &store.Programme{PID: "b01rryzz", Episode: "Series 7: 7. The Bells of Saint John", AirDate: "2013-03-30"}
	label := &store.Programme{PID: "x0000001", Episode: "Christmas Special: The Bells of Saint John", AirDate: "2013-03-29"}
	if n := idx.numberFromTVDB([]*store.Programme{label, real}, all, all); n != 1 {
		t.Errorf("numbered %d, want 1", n)
	}
	if !real.TVDBNumbered || real.Series != 7 || real.EpisodeNum != 6 {
		t.Errorf("full match = %+v, want S07E06", real)
	}
	if label.TVDBNumbered {
		t.Errorf("label match took the number too: %+v", label)
	}

	a := &store.Programme{PID: "x0000002", Episode: "Christmas Special: No Way Out", AirDate: "1975-12-24"}
	b := &store.Programme{PID: "x0000003", Episode: "Specials: 1. No Way Out", AirDate: "1975-12-24"}
	if n := idx.numberFromTVDB([]*store.Programme{a, b}, all, all); n != 0 || a.TVDBNumbered || b.TVDBNumbered {
		t.Errorf("two label matches: numbered %d (%v, %v), want 0", n, a.TVDBNumbered, b.TVDBNumbered)
	}

	// The same PID listed twice is one programme.
	c1 := &store.Programme{PID: "b007894v", Episode: "Christmas Special: No Way Out", AirDate: "1975-12-24"}
	c2 := &store.Programme{PID: "b007894v", Episode: "Christmas Special: No Way Out", AirDate: "1975-12-24"}
	if n := idx.numberFromTVDB([]*store.Programme{c1, c2}, all, all); n != 2 || !c1.TVDBNumbered || !c2.TVDBNumbered {
		t.Errorf("one PID twice: numbered %d, want both", n)
	}

	// Two PIDs with the same subtitle are one programme listed twice
	// (Mrs Brown's Boys has "2025 Specials: 2. Stormin' Mammy" under
	// m002p19k and m002psxk).
	d1 := &store.Programme{PID: "x0000004", Episode: "Christmas Special: No Way Out", AirDate: "1975-12-24"}
	d2 := &store.Programme{PID: "x0000005", Episode: "Christmas Special: No Way Out", AirDate: "1975-12-25"}
	if n := idx.numberFromTVDB([]*store.Programme{d1, d2}, all, all); n != 2 || !d1.TVDBNumbered || !d2.TVDBNumbered {
		t.Errorf("two PIDs, one subtitle: numbered %d, want both", n)
	}
}

// A Skyhook failure is cached for a few minutes and a list for hours,
// and a cancelled search caches nothing.
func TestGH60_SkyhookEpisodesCache(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusInternalServerError)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if s := int(status.Load()); s != http.StatusOK {
			w.WriteHeader(s)
			return
		}
		w.Write([]byte(`{"episodes":[{"seasonNumber":0,"episodeNumber":2,"title":"No Way Out","airDate":"1975-12-24"}]}`))
	}))
	defer srv.Close()
	old := skyhookBaseURL
	skyhookBaseURL = srv.URL
	defer func() { skyhookBaseURL = old }()

	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	s := NewSkyhookEpisodes()
	s.now = func() time.Time { return now }
	ctx := context.Background()

	if _, err := s.Episodes(ctx, "76578"); err == nil {
		t.Fatal("want an error from a 500")
	}
	status.Store(http.StatusOK)
	if _, err := s.Episodes(ctx, "76578"); err == nil || hits.Load() != 1 {
		t.Fatalf("failure not cached: err=%v hits=%d", err, hits.Load())
	}
	now = now.Add(skyhookEpisodesFailTTL)
	eps, err := s.Episodes(ctx, "76578")
	if err != nil || len(eps) != 1 || eps[0].Title != "No Way Out" || hits.Load() != 2 {
		t.Fatalf("after the failure TTL: eps=%v err=%v hits=%d", eps, err, hits.Load())
	}
	now = now.Add(skyhookEpisodesTTL - time.Second)
	if _, err := s.Episodes(ctx, "76578"); err != nil || hits.Load() != 2 {
		t.Fatalf("list not cached: err=%v hits=%d", err, hits.Load())
	}
	now = now.Add(time.Second)
	if _, err := s.Episodes(ctx, "76578"); err != nil || hits.Load() != 3 {
		t.Fatalf("list not refreshed after the TTL: err=%v hits=%d", err, hits.Load())
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Episodes(cancelled, "80435"); err == nil {
		t.Fatal("want an error from a cancelled search")
	}
	if _, err := s.Episodes(ctx, "80435"); err != nil {
		t.Fatalf("a cancelled search was cached as a failure: %v", err)
	}

	before := hits.Load()
	for _, bad := range []string{"", "76578/../x", "1?y=2", "abc"} {
		if _, err := s.Episodes(ctx, bad); err == nil {
			t.Errorf("tvdbid %q accepted", bad)
		}
	}
	if hits.Load() != before {
		t.Errorf("a bad tvdbid reached Skyhook")
	}
}

// An empty list is retried after the failure TTL, not the list TTL.
func TestGH60_SkyhookEmptyListRetriedSoon(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(`{"episodes":[]}`))
	}))
	defer srv.Close()
	old := skyhookBaseURL
	skyhookBaseURL = srv.URL
	defer func() { skyhookBaseURL = old }()

	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	s := NewSkyhookEpisodes()
	s.now = func() time.Time { return now }
	s.Episodes(context.Background(), "76578")
	s.Episodes(context.Background(), "76578")
	if hits.Load() != 1 {
		t.Fatalf("hits = %d, want 1 inside the failure TTL", hits.Load())
	}
	now = now.Add(skyhookEpisodesFailTTL)
	s.Episodes(context.Background(), "76578")
	if hits.Load() != 2 {
		t.Errorf("hits = %d, want 2 after the failure TTL", hits.Load())
	}
}

func TestGH60_TVDBMatch(t *testing.T) {
	idx := newTVDBIndex([]TVDBEpisode{
		{Season: 0, Episode: 2, Title: "No Way Out", AirDate: "1975-12-24"},
		{Season: 0, Episode: 16, Title: "The End of Time (1)", AirDate: "2009-12-25"},
		{Season: 0, Episode: 1, Title: "The Star Beast", AirDate: "2023-11-25"},
		{Season: 23, Episode: 16, Title: "VG: Part 2", AirDate: "2026-01-02"},
		{Season: 24, Episode: 9, Title: "Week 5", AirDate: "2026-10-18"},
		{Season: 23, Episode: 8, Title: "Week 4 Results", AirDate: "2025-10-12"},
		{Season: 11, Episode: 8, Title: "Episode 8", AirDate: "2026-10-20"},
		{Season: 3, Episode: 1, Title: "Rose", AirDate: "2007-01-01"},
		{Season: 3, Episode: 2, Title: "Rose", AirDate: "2007-01-03"},
		{Season: 1, Episode: 0, Title: "Pilot", AirDate: "2001-01-01"},
		{Season: 1, Episode: 5, Title: "Undated"},
		{Season: 7, Episode: 6, Title: "The Bells of Saint John", AirDate: "2013-03-30"},
		{Season: 0, Episode: 19, Title: "Tardisode 01: New Earth", AirDate: "2006-04-08"},
		{Season: 2, Episode: 1, Title: "New Earth", AirDate: "2006-04-15"},
		{Season: 4, Episode: 1, Title: "The New World", AirDate: "2011-07-14"},
		{Season: 2, Episode: 1, Title: "Kryten", AirDate: "1988-09-06"},
	})
	cases := []struct {
		subtitle, airDate string
		want              string // "" for no match
	}{
		{"Christmas Special: No Way Out", "1975-12-24", "S00E02"},
		{"No Way Out", "1975-12-31", "S00E02"}, // a week out
		{"No Way Out", "1976-01-01", ""},       // eight days out
		{"No Way Out", "", ""},
		{"The End of Time - Part One", "2009-12-25", "S00E16"},
		{"Specials: 1. The Star Beast", "2023-11-25", "S00E01"},
		{"Series W: 16. VG: Part 2", "2026-01-02", "S23E16"},
		// What follows a label must say which episode it is.
		{"Series 24 Extras: Weekly Rewind - Week Five", "2026-10-18", ""},
		{"Weekly Rewind: Week 5", "2026-10-18", ""},
		{"Beyond the Barn: Episode 8", "2026-10-20", ""},
		// Only a specials label, or one that numbers the episode, is
		// stripped: an extra about an episode keeps its label.
		{"Prequel: The Bells of Saint John", "2013-03-23", ""},
		{"Behind the Scenes: The Bells of Saint John", "2013-03-30", ""},
		{"Miracle Day: 1. The New World", "2011-07-14", "S04E01"},
		{"Series 23: Week 4 Results", "2025-10-12", "S23E08"},
		{"Series 23 Extras: Week 4 Results", "2025-10-12", ""},
		{"Series 24: Week 5", "2026-10-18", ""}, // "Week 5" alone is generic
		{"II: 1. Kryten", "1988-09-06", "S02E01"},
		// Leading zeros do not count, so the whole title matches and the
		// "New Earth" after the label is never tried.
		{"Tardisode 1: New Earth", "2006-04-08", "S00E19"},
		// Two TheTVDB episodes fit.
		{"Rose", "2007-01-02", ""},
		{"Rose", "2006-12-26", "S03E01"}, // S03E02 is eight days out
		{"Pilot", "2001-01-01", ""},      // TheTVDB episode 0
		{"Undated", "2001-01-01", ""},
	}
	for _, c := range cases {
		e, ok := idx.match(c.subtitle, c.airDate)
		got := ""
		if ok {
			got = fmt.Sprintf("S%02dE%02d", e.Season, e.Episode)
		}
		if got != c.want {
			t.Errorf("match(%q, %q) = %q, want %q", c.subtitle, c.airDate, got, c.want)
		}
	}

	// A daily soap's date subtitle keeps its date title.
	soap := newTVDBIndex([]TVDBEpisode{{Season: 42, Episode: 120, Title: "06/04/2026", AirDate: "2026-04-06"}})
	prog := &store.Programme{PID: "p0000002", Episode: "06/04/2026", AirDate: "2026-04-06"}
	if soap.numberFromTVDB([]*store.Programme{prog}, func(*store.Programme) bool { return true }, func(*store.Programme) bool { return true }) != 0 || prog.TVDBNumbered {
		t.Errorf("date subtitle numbered from TheTVDB: %+v", prog)
	}
}

// The overrides correct BBC numbering, so TheTVDB numbers skip them.
// The name override and forced date titles still apply.
func TestGH60_GenerateTitleTVDBNumbered(t *testing.T) {
	p := &store.Programme{
		Name: "Porridge", Episode: "Christmas Special: No Way Out",
		Series: 0, EpisodeNum: 2, Position: 4, AirDate: "1975-12-24", TVDBNumbered: true,
	}
	title, tier := GenerateTitle(p, "720p", &store.ShowOverride{
		ShowName: "Porridge", ForceSeriesNum: 3, SeriesOffset: 1, EpisodeOffset: 2, ForcePosition: true,
		CustomName: "Porridge UK",
	})
	if want := "Porridge.UK.S00E02.Christmas.Special.No.Way.Out.720p.WEB-DL.AAC.H264-iParr"; title != want || tier != store.TierFull {
		t.Errorf("title = %q (%s), want %q (%s)", title, tier, want, store.TierFull)
	}

	title, tier = GenerateTitle(p, "720p", &store.ShowOverride{ShowName: "Porridge", ForceDateBased: true})
	if want := "Porridge.1975.12.24.Christmas.Special.No.Way.Out.720p.WEB-DL.AAC.H264-iParr"; title != want || tier != store.TierDate {
		t.Errorf("forced date title = %q (%s), want %q", title, tier, want)
	}
}

// BBC and TheTVDB often title a special differently ("Christmas Special
// 2022" is TheTVDB's "The Death Ghost"), but both have it on the day it
// aired. An unmatched special whose only number is its position takes
// the one TheTVDB special of that day. The cases are real iPlayer and
// TheTVDB data from the 2026-10-24 survey.
func TestGH60_SpecialsMatchedOnAirDate(t *testing.T) {
	all := func(*store.Programme) bool { return true }
	cases := []struct {
		name string
		eps  []TVDBEpisode
		prog store.Programme
		want string // "" when TheTVDB does not number it
	}{
		{
			"Call the Midwife, plural and Part 1",
			[]TVDBEpisode{
				{Season: 0, Episode: 1, Title: "Christmas Special 2012", AirDate: "2012-12-25"},
				{Season: 0, Episode: 15, Title: "Christmas Special 2024 (1)", AirDate: "2024-12-25"},
				{Season: 0, Episode: 16, Title: "Christmas Special 2024 (2)", AirDate: "2024-12-26"},
			},
			store.Programme{PID: "m0026fg0", Episode: "Christmas Specials 2024: Part 1", Position: 1, AirDate: "2024-12-25"},
			"S00E15",
		},
		{
			// "Episode 1" counts within the 2025 specials; the Series 1
			// is only assumed (GitHub #32), so S01E01 was a guess.
			"Call the Midwife, numbered within the specials",
			[]TVDBEpisode{
				{Season: 1, Episode: 1, Title: "Episode 1", AirDate: "2012-01-15"},
				{Season: 0, Episode: 17, Title: "Christmas Special 2025 (1)", AirDate: "2025-12-25"},
			},
			store.Programme{PID: "m002p134", Episode: "Christmas Specials 2025: Episode 1", Series: 1, SeriesAssumed: true, EpisodeNum: 1, Position: 1, AirDate: "2025-12-25"},
			"S00E17",
		},
		{
			// GitHub #32's case: no special, so the assumed Series 1 stands.
			"assumed series, not a special",
			[]TVDBEpisode{
				{Season: 1, Episode: 3, Title: "Episode 3", AirDate: "1986-09-20"},
				{Season: 0, Episode: 1, Title: "Behind the Scenes", AirDate: "2026-10-18"},
			},
			store.Programme{PID: "x0000014", Episode: "Learning Curve Episode 3", Series: 1, SeriesAssumed: true, EpisodeNum: 3, AirDate: "2026-10-18"},
			"",
		},
		{
			"Death in Paradise, another title",
			[]TVDBEpisode{{Season: 0, Episode: 4, Title: "The Death Ghost", AirDate: "2022-12-26"}},
			store.Programme{PID: "m001gnd0", Episode: "Christmas Special 2022", Position: 12, AirDate: "2022-12-26"},
			"S00E04",
		},
		{
			"Inside No. 9, TheTVDB's label",
			[]TVDBEpisode{
				{Season: 1, Episode: 5, Title: "The Understudy", AirDate: "2014-03-05"},
				{Season: 0, Episode: 2, Title: "Live: Dead Line", AirDate: "2018-10-28"},
			},
			store.Programme{PID: "b0bqn4g3", Episode: "Dead Line", Position: 5, AirDate: "2018-10-28"},
			"S00E02",
		},
		{
			"three specials that day, one named in the subtitle",
			[]TVDBEpisode{
				{Season: 0, Episode: 82, Title: "The Night of the Doctor", AirDate: "2013-11-14"},
				{Season: 0, Episode: 90, Title: "The Science of Doctor Who (2013)", AirDate: "2013-11-14"},
				{Season: 0, Episode: 114, Title: "The Doctors Revisited: The Eleventh Doctor", AirDate: "2013-11-14"},
			},
			store.Programme{PID: "p01lhhv4", Episode: "Mini Episode - The Night of the Doctor", Position: 20, AirDate: "2013-11-14"},
			"S00E82",
		},
		{
			// A short subtitle inside a longer TheTVDB title is not enough.
			"three specials that day, subtitle inside one title",
			[]TVDBEpisode{
				{Season: 0, Episode: 82, Title: "The Night of the Doctor", AirDate: "2013-11-14"},
				{Season: 0, Episode: 90, Title: "The Science of Doctor Who (2013)", AirDate: "2013-11-14"},
				{Season: 0, Episode: 114, Title: "The Doctors Revisited: The Eleventh Doctor", AirDate: "2013-11-14"},
			},
			store.Programme{PID: "x0000016", Episode: "The Doctor", Position: 3, AirDate: "2013-11-14"},
			"",
		},
		{
			// The guess, S00E01, aired a week before; the special of the
			// day is another one.
			"guessed special a week out",
			[]TVDBEpisode{
				{Season: 0, Episode: 1, Title: "Christmas Special", AirDate: "2020-12-25"},
				{Season: 0, Episode: 2, Title: "Revolution", AirDate: "2021-01-01"},
			},
			store.Programme{PID: "x0000017", Episode: "New Year Special", Position: 1, AirDate: "2021-01-01"},
			"S00E02",
		},
		{
			"title fits two TheTVDB episodes",
			[]TVDBEpisode{
				{Season: 2, Episode: 6, Title: "The Finale", AirDate: "2020-12-20"},
				{Season: 0, Episode: 3, Title: "The Finale", AirDate: "2020-12-20"},
				{Season: 1, Episode: 6, Title: "Something", AirDate: "2019-01-01"},
			},
			store.Programme{PID: "x0000018", Episode: "The Finale", Position: 6, AirDate: "2020-12-20"},
			"",
		},
		{
			"two specials that day, neither named",
			[]TVDBEpisode{
				{Season: 0, Episode: 5, Title: "The Grand Tour", AirDate: "2020-12-25"},
				{Season: 0, Episode: 6, Title: "Behind the Tinsel", AirDate: "2020-12-25"},
			},
			store.Programme{PID: "x0000010", Episode: "Christmas Special 2020", Position: 3, AirDate: "2020-12-25"},
			"",
		},
		{
			"only generic words in common",
			[]TVDBEpisode{
				{Season: 0, Episode: 1, Title: "Christmas", AirDate: "2020-12-25"},
				{Season: 0, Episode: 2, Title: "Christmas Day Live", AirDate: "2020-12-25"},
			},
			store.Programme{PID: "x0000011", Episode: "The Christmas Special", Position: 7, AirDate: "2020-12-25"},
			"",
		},
		{
			"no special that day",
			[]TVDBEpisode{{Season: 0, Episode: 41, Title: "Top of the Pups: A Duggee Duggee Christmas", AirDate: "2022-11-26"}},
			store.Programme{PID: "p0d6bwpz", Episode: "Top of the Pups: The Halloween Song", AirDate: "2023-10-17"},
			"",
		},
		{
			// TheTVDB's S01E03 aired that day, so the position is right
			// and the special shown the same night is something else.
			"position agrees with TheTVDB",
			[]TVDBEpisode{
				{Season: 1, Episode: 3, Title: "Episode 3", AirDate: "2020-01-10"},
				{Season: 0, Episode: 1, Title: "Behind the Scenes", AirDate: "2020-01-10"},
			},
			store.Programme{PID: "x0000012", Episode: "The Third One", Position: 3, AirDate: "2020-01-10"},
			"",
		},
		{
			"numbered by its subtitle",
			[]TVDBEpisode{{Season: 0, Episode: 1, Title: "Behind the Scenes", AirDate: "2020-01-10"}},
			store.Programme{PID: "x0000013", Episode: "Series 2: 3. Something", Series: 2, EpisodeNum: 3, Position: 3, AirDate: "2020-01-10"},
			"",
		},
	}
	for _, c := range cases {
		p := c.prog
		newTVDBIndex(c.eps).numberFromTVDB([]*store.Programme{&p}, all, all)
		got := ""
		if p.TVDBNumbered {
			got = fmt.Sprintf("S%02dE%02d", p.Series, p.EpisodeNum)
		}
		if got != c.want {
			t.Errorf("%s: numbered %q, want %q", c.name, got, c.want)
		}
	}
}

// A match on the air date alone is the weakest claim: the signed version
// of The Repair Shop's 2025 Christmas special gives S00E35 up to the
// special itself, and is then left with a position TheTVDB has for its
// 2017 special.
func TestGH60_AirDateMatchGivesWay(t *testing.T) {
	all := func(*store.Programme) bool { return true }
	idx := newTVDBIndex([]TVDBEpisode{
		{Season: 0, Episode: 1, Title: "Christmas", AirDate: "2017-12-19"},
		{Season: 0, Episode: 35, Title: "At Christmas 2025", AirDate: "2025-12-26"},
	})
	special := &store.Programme{PID: "m002psw8", Episode: "At Christmas 2025", Position: 2, AirDate: "2025-12-26"}
	signed := &store.Programme{PID: "m002psw9", Episode: "At Christmas 2025 - Signed", Position: 1, AirDate: "2025-12-26"}
	if n := idx.numberFromTVDB([]*store.Programme{signed, special}, all, all); n != 1 {
		t.Errorf("numbered %d, want 1", n)
	}
	if !special.TVDBNumbered || special.EpisodeNum != 35 || special.TVDBContradicted {
		t.Errorf("special = %+v, want S00E35", special)
	}
	if signed.TVDBNumbered || !signed.TVDBContradicted {
		t.Errorf("signed = %+v, want unnumbered and contradicted", signed)
	}

	// When neither matches on its title, the signed version still gives
	// way to the version it signs, rather than both losing S00E04.
	idx = newTVDBIndex([]TVDBEpisode{{Season: 0, Episode: 4, Title: "The Death Ghost", AirDate: "2022-12-26"}})
	special = &store.Programme{PID: "m001gnd0", Episode: "Christmas Special 2022", Position: 12, AirDate: "2022-12-26"}
	signed = &store.Programme{PID: "x0000015", Episode: "Christmas Special 2022 - Signed", Position: 13, AirDate: "2022-12-26"}
	idx.numberFromTVDB([]*store.Programme{signed, special}, all, all)
	if !special.TVDBNumbered || special.EpisodeNum != 4 || signed.TVDBNumbered {
		t.Errorf("special = %+v, signed = %+v, want only the special as S00E04", special, signed)
	}
}

// A special TheTVDB cannot number, whose guessed number TheTVDB has for
// another episode aired years before, is left out rather than offered as
// that episode. The cases are Outnumbered's. The guess stays when TheTVDB
// has nothing there, when the episode there has the same title, when the
// programme is not a special (an iPlayer date years after TheTVDB's is
// common for bought-in series), and when an override renumbers it.
func TestGH60_ContradictedPositionLeftOut(t *testing.T) {
	st := newTestStore(t)
	h := NewHandler(nil, st, nil, nil)
	h.SetEpisodeSource(fixedEpisodes{
		{Season: 0, Episode: 2, Title: "Christmas Special 2008", AirDate: "2008-12-24"},
		{Season: 0, Episode: 3, Title: "Sport Relief Special 2010", AirDate: "2010-03-19"},
		{Season: 1, Episode: 5, Title: "The Hamster", AirDate: "2007-09-25"},
	})
	results := []bbc.IBLResult{
		// TheTVDB's "The Robbers (Christmas Special 2009)" is missing from
		// this list, so its air date finds nothing either.
		{PID: "b00pk6h2", Title: "Outnumbered", Subtitle: "Christmas Special 2009", Position: 3, AirDate: "2009-12-27"},
		{PID: "b018ttrg", Title: "Outnumbered", Subtitle: "Christmas Special 2011", Position: 6, AirDate: "2011-12-24"},
		{PID: "x0000021", Title: "Outnumbered", Subtitle: "Christmas Special 2008", Position: 2, AirDate: "2009-12-20"},
		{PID: "x0000022", Title: "Outnumbered", Subtitle: "The Shed", Position: 5, AirDate: "2024-03-01"},
	}
	run := func() map[string]string {
		req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch", nil)
		w := httptest.NewRecorder()
		h.writeResultsRSS(w, req, results, nil, noSeasonFilter, 0, "", "Outnumbered", 0, "80592", "", false, 0)
		return gh60Numbers(t, gh58ParseItems(t, w.Body.Bytes()))
	}

	got := run()
	if se, ok := got["b00pk6h2"]; ok {
		t.Errorf("contradicted special emitted as %s", se)
	}
	gh60Check(t, got, map[string]string{
		"b018ttrg": "S00E06", // TheTVDB has no S00E06
		"x0000021": "S00E02", // same title, re-shown a year later
		"x0000022": "S01E05", // not a special
	})

	// An offset leaves a special's S00E<position> as it was, so the
	// special is still left out; a forced date title is another title.
	if err := st.PutOverride(&store.ShowOverride{ShowName: "Outnumbered", SeriesOffset: 1}); err != nil {
		t.Fatal(err)
	}
	if se, ok := run()["b00pk6h2"]; ok {
		t.Errorf("with an offset override, contradicted special emitted as %s", se)
	}
	if err := st.PutOverride(&store.ShowOverride{ShowName: "Outnumbered", ForceDateBased: true}); err != nil {
		t.Fatal(err)
	}
	if got := run()["b00pk6h2"]; got != "none" {
		t.Errorf("with Force Date Based: numbered %q, want its date title", got)
	}
}

// When iPlayer has nothing titled exactly as the show, a companion brand
// can be among a search's programmes. It must not take a special's
// number on its air date alone.
func TestGH60_CompanionNotMatchedOnAirDate(t *testing.T) {
	h := NewHandler(nil, newTestStore(t), nil, nil)
	h.SetEpisodeSource(fixedEpisodes{
		{Season: 0, Episode: 5, Title: "Joy to the World", AirDate: "2024-12-25"},
		{Season: 1, Episode: 6, Title: "Rogue", AirDate: "2024-06-08"},
	})
	results := []bbc.IBLResult{{PID: "x0000023", Title: "Doctor Who: Unleashed", Subtitle: "The Festive Episode", Position: 6, AirDate: "2024-12-25"}}
	req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch", nil)
	w := httptest.NewRecorder()
	h.writeResultsRSS(w, req, results, nil, 0, 5, "", "Doctor Who (2023)", 0, "449991", "", false, 0)
	if got := gh60Numbers(t, gh58ParseItems(t, w.Body.Bytes()))["x0000023"]; got == "S00E05" {
		t.Errorf("companion numbered as the special S00E05")
	}
}

// From iPlayer's own fields, as a search sees them: Call the Midwife's
// 2025 specials have "Episode 1" and "Episode 2" in their subtitles and
// no series, so v1.8.3 made them S01E01 and S01E02, the first episodes
// of 2012. TheTVDB has them as S00E17 and S00E18.
func TestGH60_SpecialNumberedWithinItsGroup(t *testing.T) {
	h := NewHandler(nil, newTestStore(t), nil, nil)
	h.SetEpisodeSource(fixedEpisodes{
		{Season: 1, Episode: 1, Title: "Episode 1", AirDate: "2012-01-15"},
		{Season: 1, Episode: 2, Title: "Episode 2", AirDate: "2012-01-22"},
		{Season: 0, Episode: 17, Title: "Christmas Special 2025 (1)", AirDate: "2025-12-25"},
		{Season: 0, Episode: 18, Title: "Christmas Special 2025 (2)", AirDate: "2025-12-26"},
	})
	results := []bbc.IBLResult{
		{PID: "m002p134", Title: "Call the Midwife", Subtitle: "Christmas Specials 2025: Episode 1", EpisodeNum: 1, Position: 1, AirDate: "2025-12-25"},
		{PID: "m002p16x", Title: "Call the Midwife", Subtitle: "Christmas Specials 2025: Episode 2", EpisodeNum: 2, Position: 2, AirDate: "2025-12-26"},
		{PID: "p0118rpd", Title: "Call the Midwife", Subtitle: "Series 1: Episode 1", Series: 1, EpisodeNum: 1, Position: 1, AirDate: "2012-01-15"},
	}
	req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch", nil)
	w := httptest.NewRecorder()
	h.writeResultsRSS(w, req, results, nil, noSeasonFilter, 0, "", "Call the Midwife", 0, "255192", "", false, 0)
	gh60Check(t, gh60Numbers(t, gh58ParseItems(t, w.Body.Bytes())), map[string]string{
		"m002p134": "S00E17",
		"m002p16x": "S00E18",
		"p0118rpd": "S01E01",
	})
}

// positionGuess must give exactly the number GenerateTitle emits from a
// position or air date, and nothing for a number from the subtitle.
func TestGH60_PositionGuessMatchesGenerateTitle(t *testing.T) {
	cases := []struct {
		prog  store.Programme
		guess bool
	}{
		{store.Programme{Episode: "Christmas Special", Position: 4, AirDate: "2020-12-25"}, true},
		{store.Programme{Episode: "Christmas Special", AirDate: "2020-12-25"}, true},
		{store.Programme{Episode: "Christmas Special", Series: 3, Position: 7, AirDate: "2020-12-25"}, true},
		{store.Programme{Episode: "The Third One", Position: 3, AirDate: "2020-01-10"}, true},
		{store.Programme{Episode: "Series 2: 3. Something", Series: 2, EpisodeNum: 3, Position: 3}, false},
		{store.Programme{Episode: "Christmas Special", Series: 2, EpisodeNum: 9, Position: 9}, false},
		{store.Programme{Episode: "Christmas Specials 2025: Episode 1", Series: 1, SeriesAssumed: true, EpisodeNum: 1, Position: 1}, true},
		{store.Programme{Episode: "Learning Curve Episode 3", Series: 1, SeriesAssumed: true, EpisodeNum: 3}, false},
		{store.Programme{Episode: "24/10/2026", Position: 7400, AirDate: "2026-10-24"}, false},
		{store.Programme{Episode: "One-off", AirDate: "2020-01-10"}, false},
		{store.Programme{Episode: "One-off"}, false},
	}
	for _, c := range cases {
		p := c.prog
		p.Name = "Show"
		s, e, ok := positionGuess(&p)
		title, _ := GenerateTitle(&p, "720p", nil)
		id := ParseTitle(title)
		if ok != c.guess {
			t.Errorf("%q: guess = %v, want %v", p.Episode, ok, c.guess)
			continue
		}
		if ok && (id.Season != s || id.Episode != e) {
			t.Errorf("%q: guess S%02dE%02d, GenerateTitle %q", p.Episode, s, e, title)
		}
		if !ok && id.Episode > 0 && (id.Season != p.Series || id.Episode != p.EpisodeNum) {
			t.Errorf("%q: no guess, but GenerateTitle %q numbers it from elsewhere", p.Episode, title)
		}
	}
}

// TVDB 78804 S00E15 is "Dreamland", which iPlayer has only as the
// one-off "Doctor Who: Dreamland" (b00p3pvl, not in these fixtures;
// TestGH104_ParentOneOffTakesTheTVDBNumber has it as S00E15, Gitea
// #104). v1.9.0 answered it with Joy to the World
// (m0026d24), a Doctor Who (2023) special with no BBC numbers, titled
// Doctor.Who.S01E04 from its position, which Sonarr took as Aliens of
// London. Whatever a search returns must carry the number searched.
// This pins the reported search as a whole; either half of the fix
// keeps Joy out of it on its own (TestGH60_EraCheckSeesTheWholeShow pins
// the era check alone).
func TestGH60_SearchReturnsOnlyTheNumberSearched(t *testing.T) {
	ibl, _ := gh60Fixtures(t, http.StatusOK)
	h := gh60Handler(t, ibl, newTestStore(t))
	items := gh58Serve(t, h, "t=tvsearch&tvdbid=78804&season=0&ep=15")

	for pid, se := range gh60Numbers(t, items) {
		if se != "S00E15" {
			t.Errorf("PID %s emitted as %s in a search for S00E15", pid, se)
		}
	}
	gh58AssertNone(t, items, gh60BrandPIDs(t, "p0gglvqn"), "Doctor Who (2023)")
}

// gh60BrandPIDs returns the PIDs of a brand's testdata/gh60 listing.
func gh60BrandPIDs(t *testing.T, brand string) map[string]bool {
	t.Helper()
	var resp struct {
		ProgrammeEpisodes struct {
			Elements []struct {
				ID string `json:"id"`
			} `json:"elements"`
		} `json:"programme_episodes"`
	}
	b, err := os.ReadFile(filepath.Join(gh60Dir, "episodes_"+brand+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatal(err)
	}
	out := make(map[string]bool)
	for _, e := range resp.ProgrammeEpisodes.Elements {
		out[e.ID] = true
	}
	return out
}

// Every episode search answers with that episode or nothing. For each
// episode TheTVDB lists, a tvdbid search must emit only items numbered
// as searched, and none from another era's brand: which show a
// programme belongs to must not depend on which episode was asked for.
// v1.9.0 failed this three ways: season=0 was no season filter, so
// S00E02 also returned S01E02 to S13E02; a programme with no BBC
// numbers answered every search, titled S01E<position>; and the year
// check ran on what the episode filter left, so a Doctor Who (2005)
// S00E15 search, which no 2005-2022 programme answers, kept the bare
// "Doctor Who" brand, Doctor Who (2023). The one exception is a
// programme of another era that TheTVDB numbers for the searched
// series, at that number (TestGH60_OtherEraProgrammeTheTVDBNumbersIsKept).
func TestGH60_EveryEpisodeSearchAnswersOnlyItsNumber(t *testing.T) {
	ibl, _ := gh60Fixtures(t, http.StatusOK)
	for _, c := range []struct {
		tvdbid string
		others []string          // brands of the same name from other eras
		kept   map[string]string // their programmes TheTVDB numbers, and as what
	}{
		{"78804", []string{"p0gglvqn", "p0ggwr8l", "m001sx3r"},
			map[string]string{"m001sx3h": "S00E162", "m001t42d": "S00E163", "m001tc3r": "S00E164"}},
		{"449991", []string{"b006q2x0", "p0ggwr8l", "m001sx3r"}, nil},
		{"76578", []string{"p05dsmwl"}, nil},
	} {
		var sky struct {
			Episodes []struct {
				SeasonNumber  int `json:"seasonNumber"`
				EpisodeNumber int `json:"episodeNumber"`
			} `json:"episodes"`
		}
		b, err := os.ReadFile(filepath.Join(gh60Dir, "skyhook_"+c.tvdbid+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &sky); err != nil {
			t.Fatal(err)
		}
		others := map[string]bool{}
		for _, brand := range c.others {
			for pid := range gh60BrandPIDs(t, brand) {
				others[pid] = true
			}
		}
		h := gh60Handler(t, ibl, newTestStore(t))
		for _, e := range sky.Episodes {
			if e.EpisodeNumber < 1 {
				continue
			}
			want := fmt.Sprintf("S%02dE%02d", e.SeasonNumber, e.EpisodeNumber)
			items := gh58Serve(t, h, fmt.Sprintf("t=tvsearch&tvdbid=%s&season=%d&ep=%d", c.tvdbid, e.SeasonNumber, e.EpisodeNumber))
			for pid, se := range gh60Numbers(t, items) {
				if se != want {
					t.Errorf("tvdbid %s %s: %s emitted as %s", c.tvdbid, want, pid, se)
				}
				if others[pid] && c.kept[pid] != se {
					t.Errorf("tvdbid %s %s: %s is another era's programme", c.tvdbid, want, pid)
				}
			}
		}
	}
}

// A programme with no BBC numbers is filtered on the number its title
// carries, with its Show Override applied: ForceSeriesNum puts a
// position-numbered episode in that series, so it answers S03E05 and
// nothing else. v1.9.0 let it answer every search.
func TestGH60_OverrideNumberIsTheOneFiltered(t *testing.T) {
	st := newTestStore(t)
	if err := st.PutOverride(&store.ShowOverride{ShowName: "Some Show", ForceSeriesNum: 3}); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(nil, st, nil, nil)
	results := []bbc.IBLResult{{PID: "p0000005", Title: "Some Show", Subtitle: "The Fifth One", Position: 5, AirDate: "2026-10-20"}}
	run := func(season, ep int) map[string]string {
		req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch", nil)
		w := httptest.NewRecorder()
		h.writeResultsRSS(w, req, results, nil, season, ep, "", "Some Show", 0, "", "", false, 0)
		return gh60Numbers(t, gh58ParseItems(t, w.Body.Bytes()))
	}
	if got := run(3, 5)["p0000005"]; got != "S03E05" {
		t.Errorf("S03E05 search: numbered %q, want S03E05", got)
	}
	for _, se := range [][2]int{{1, 5}, {3, 4}, {0, 5}} {
		if got, ok := run(se[0], se[1])["p0000005"]; ok {
			t.Errorf("S%02dE%02d search returned it as %s", se[0], se[1], got)
		}
	}
}

// TheTVDB lists Doctor Who's 60th anniversary specials, which iPlayer
// has under the bare "Doctor Who" brand of Doctor Who (2023), as Doctor
// Who (2005) S00E162 to S00E164 as well. A programme of another era
// that TheTVDB numbers for the searched series stays in the search.
func TestGH60_OtherEraProgrammeTheTVDBNumbersIsKept(t *testing.T) {
	ibl, _ := gh60Fixtures(t, http.StatusOK)
	h := gh60Handler(t, ibl, newTestStore(t))
	for pid, se := range map[string][2]int{"m001sx3h": {0, 162}, "m001t42d": {0, 163}, "m001tc3r": {0, 164}} {
		got := gh60Numbers(t, gh58Serve(t, h, fmt.Sprintf("t=tvsearch&tvdbid=78804&season=%d&ep=%d", se[0], se[1])))
		if want := fmt.Sprintf("S%02dE%02d", se[0], se[1]); got[pid] != want {
			t.Errorf("%s search: %s numbered %q, want %s", want, pid, got[pid], want)
		}
	}
}

// A programme with no BBC numbers and no air date is filtered on its
// BBC numbers, as before v1.9.1: TheTVDB cannot check a guess without
// a date, so its S00E<position> must not answer that episode's search.
func TestGH60_UndatedGuessAnswersNoEpisodeSearch(t *testing.T) {
	h := NewHandler(nil, newTestStore(t), nil, nil)
	results := []bbc.IBLResult{{PID: "p0000004", Title: "Some Show", Subtitle: "Christmas Special", Position: 4}}
	run := func(season, ep int) map[string]string {
		req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch", nil)
		w := httptest.NewRecorder()
		h.writeResultsRSS(w, req, results, nil, season, ep, "", "Some Show", 0, "", "", false, 0)
		return gh60Numbers(t, gh58ParseItems(t, w.Body.Bytes()))
	}
	if got, ok := run(0, 4)["p0000004"]; ok {
		t.Errorf("S00E04 search returned it as %s", got)
	}
	if got := run(noSeasonFilter, 0)["p0000004"]; got != "S00E04" {
		t.Errorf("search with no season: numbered %q, want S00E04", got)
	}
}

// A title number ParseTitle cannot read back (an episode over 9999)
// cannot be searched for, so the programme answers only a search with
// no season or episode.
func TestGH60_UnreadableNumberAnswersOnlyUnfilteredSearch(t *testing.T) {
	h := NewHandler(nil, newTestStore(t), nil, nil)
	results := []bbc.IBLResult{{PID: "p0012345", Title: "Some Show", Subtitle: "Late Edition", Position: 12345, AirDate: "2026-10-20"}}
	run := func(season, ep int) bool {
		req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch", nil)
		w := httptest.NewRecorder()
		h.writeResultsRSS(w, req, results, nil, season, ep, "", "Some Show", 0, "", "", false, 0)
		return len(gh58ParseItems(t, w.Body.Bytes())) > 0
	}
	if !run(noSeasonFilter, 0) {
		t.Errorf("search with no season or episode left it out")
	}
	for _, se := range [][2]int{{0, 0}, {1, 0}, {0, 1}} {
		if run(se[0], se[1]) {
			t.Errorf("season=%d ep=%d search returned it", se[0], se[1])
		}
	}
}

// An other-era programme kept because TheTVDB numbers it for the
// searched series goes out under the searched series' name, not its
// brand's and not a custom one its brand's Show Override sets, so the
// name, numbers and tvdbid all point at one series, as
// numberBrowseFromTVDB does for the feed. In its own series it goes out
// under that series' name, also in place of the custom name.
func TestGH60_OtherEraProgrammeKeepsNoCustomName(t *testing.T) {
	ibl, _ := gh60Fixtures(t, http.StatusOK)
	st := newTestStore(t)
	if err := st.PutOverride(&store.ShowOverride{ShowName: "Doctor Who", CustomName: "Doctor Who Custom"}); err != nil {
		t.Fatal(err)
	}
	h := gh60Handler(t, ibl, st)
	gh58AssertTitle(t, gh58Serve(t, h, "t=tvsearch&tvdbid=78804&season=0&ep=162"), "m001sx3h",
		"Doctor.Who.2005.S00E162.Specials.1.The.Star.Beast.720p.WEB-DL.AAC.H264-iParr")
	gh58AssertTitle(t, gh58Serve(t, h, "t=tvsearch&tvdbid=449991&season=0&ep=1"), "m001sx3h",
		"Doctor.Who.2023.S00E01.Specials.1.The.Star.Beast.720p.WEB-DL.AAC.H264-iParr")
}

// The era check sees the whole show, not what the episode filter left.
// With Aliens of London (b0074dp9) gone from iPlayer, no 2005-2022
// programme answers Doctor Who (2005) S01E04, and v1.9.0 then kept the
// bare "Doctor Who" brand: 73 Yards ("Season 1: 4.") and Joy to the
// World went out as Doctor.Who.S01E04 with tvdbid 78804, numbered as
// the episode searched, so Sonarr would grab them. GitHub #60.
func TestGH60_EraCheckSeesTheWholeShow(t *testing.T) {
	fixtures, _ := gh60Fixtures(t, http.StatusOK)
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
		if pe, ok := body["programme_episodes"].(map[string]any); ok && strings.Contains(r.URL.Path, "/b006q2x0/") {
			var kept []any
			for _, e := range pe["elements"].([]any) {
				if e.(map[string]any)["id"] != "b0074dp9" {
					kept = append(kept, e)
				}
			}
			pe["elements"] = kept
			pe["count"] = len(kept)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	ibl := bbc.NewIBL(bbc.NewClient())
	ibl.BaseURL = srv.URL
	h := gh60Handler(t, ibl, newTestStore(t))

	items := gh58Serve(t, h, "t=tvsearch&tvdbid=78804&season=1&ep=4")
	gh58AssertNone(t, items, gh60BrandPIDs(t, "p0gglvqn"), "Doctor Who (2023)")
	if got := gh58PIDSet(items); len(got) != 0 {
		t.Errorf("S01E04 with Aliens of London gone: emitted PIDs = %q, want none", got)
	}
}
