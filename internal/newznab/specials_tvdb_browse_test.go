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
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// GitHub #60, the RSS feed. Sonarr's RSS sync sends no tvdbid, so the
// feed numbers the episodes of shows Sonarr has searched by tvdbid before,
// from the series mappings those searches stored.

// gh60BrowseIBL serves an iBL whose "New & Trending" rail holds the
// testdata/gh60 elements with these PIDs, plus any extra elements, and
// whose other browse pools are empty.
func gh60BrowseIBL(t *testing.T, pids []string, extra ...string) *bbc.IBL {
	t.Helper()
	byPID := map[string]json.RawMessage{}
	for _, brand := range []string{"b006m9kn", "p05dsmwl", "b006q2x0", "p0gglvqn", "m001sx3r"} {
		b, err := os.ReadFile(filepath.Join(gh60Dir, "episodes_"+brand+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var listing struct {
			ProgrammeEpisodes struct {
				Elements []json.RawMessage `json:"elements"`
			} `json:"programme_episodes"`
		}
		if err := json.Unmarshal(b, &listing); err != nil {
			t.Fatal(err)
		}
		for _, raw := range listing.ProgrammeEpisodes.Elements {
			var e struct {
				ID string `json:"id"`
			}
			json.Unmarshal(raw, &e)
			byPID[e.ID] = raw
		}
	}
	var elements []string
	for _, pid := range pids {
		raw, ok := byPID[pid]
		if !ok {
			t.Fatalf("no fixture element %s", pid)
		}
		elements = append(elements, string(raw))
	}
	elements = append(elements, extra...)
	rail := fmt.Sprintf(`{"group_episodes":{"elements":[%s]}}`, strings.Join(elements, ","))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/groups/m001bm54/episodes":
			w.Write([]byte(rail))
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
	return ibl
}

// gh60SearchedShows stores the mappings Sonarr's tvdbid searches for
// Porridge and both Doctor Who series would have left.
func gh60SearchedShows(t *testing.T) *store.Store {
	t.Helper()
	st := newTestStore(t)
	for _, m := range []*store.SeriesMapping{
		{TVDBId: "76578", ShowName: "Porridge", Year: 1974},
		{TVDBId: "78804", ShowName: "Doctor Who (2005)", Year: 2005},
		{TVDBId: "449991", ShowName: "Doctor Who (2023)", Year: 2023},
	} {
		if err := st.PutSeriesMapping(m); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

type gh60Want struct {
	prefix string // the title up to the episode title
	tvdbid string
}

func gh60CheckItems(t *testing.T, items []gh58Item, want map[string]gh60Want) {
	t.Helper()
	seen := map[string]bool{}
	for _, it := range items {
		w, ok := want[it.PID]
		if !ok {
			continue
		}
		seen[it.PID] = true
		if !strings.HasPrefix(it.Title, w.prefix) || it.TVDBID != w.tvdbid {
			t.Errorf("%s: title %q tvdbid %q, want %s... tvdbid %q", it.PID, it.Title, it.TVDBID, w.prefix, w.tvdbid)
		}
	}
	for pid := range want {
		if !seen[pid] {
			t.Errorf("%s missing from the feed", pid)
		}
	}
}

// Each numbered item carries its series' Sonarr title and tvdbid, so
// Sonarr files Joy to the World under Doctor Who (2023) and The Runaway
// Bride under Doctor Who (2005), although iPlayer calls both "Doctor Who".
func TestGH60_BrowseFeedNumbersSearchedShows(t *testing.T) {
	gh60Fixtures(t, http.StatusOK) // Skyhook
	ibl := gh60BrowseIBL(t, []string{
		"m0026d24", "m001z8bz", // Doctor Who: Joy to the World, Space Babies
		"b0074g6q", "p01lhhv4", "b0074dlv", // Doctor Who (2005-2022): The Runaway Bride, Night of the Doctor, Rose
		"b007894v", "b007sf76", // Porridge: No Way Out, New Faces, Old Hands
		"p05dsx5r", // Porridge (2017): the 1974 series' list has nothing of 2017
		"m002d33x", // Doctor Who: Unleashed, a series of its own
	},
		// A companion episode titled as the special it is about would
		// take the special's number if "Doctor Who: Unleashed" counted
		// as Doctor Who.
		`{"id":"x0000033","type":"episode","title":"Doctor Who: Unleashed","subtitle":"Joy to the World","release_date":"25 Dec 2024","parent_position":3}`,
	)
	h := gh60Handler(t, ibl, gh60SearchedShows(t))
	gh60CheckItems(t, gh58Serve(t, h, "t=tvsearch"), map[string]gh60Want{
		"m0026d24": {"Doctor.Who.2023.S00E05.Joy.to.the.World.", "449991"}, // was S01E04
		"m001z8bz": {"Doctor.Who.2023.S01E01.", "449991"},
		"b0074g6q": {"Doctor.Who.2005.S00E04.The.Runaway.Bride.", "78804"}, // was S01E04
		"p01lhhv4": {"Doctor.Who.2005.S00E82.", "78804"},                   // was S01E20
		"b0074dlv": {"Doctor.Who.2005.S01E01.", "78804"},
		"b007894v": {"Porridge.S00E02.Christmas.Special.No.Way.Out.", "76578"}, // was S00E04
		"b007sf76": {"Porridge.S01E01.", "76578"},
		"p05dsx5r": {"Porridge.2017.S01E06.", ""},
		"m002d33x": {"Doctor.Who.Unleashed.S02E08.", ""},
		"x0000033": {"Doctor.Who.Unleashed.S01E03.", ""},
	})
}

// A brand stays as it was when any of its candidate series' lists is
// unavailable: without Doctor Who (2005)'s list, nothing says Joy to the
// World is not one of its specials. Its number, S01E04, is a guess from
// its position, so it is left out (Gitea #104): Sonarr takes
// "Doctor.Who.S01E04" as Aliens of London.
func TestGH60_BrowseFeedNeedsEveryCandidate(t *testing.T) {
	gh60Fixtures(t, http.StatusOK)
	ibl := gh60BrowseIBL(t, []string{"m0026d24", "b007894v"})
	h := NewHandler(ibl, gh60SearchedShows(t), nil, nil)
	h.SetEpisodeSource(failingEpisodes{fail: "78804", next: NewSkyhookEpisodes()})
	items := gh58Serve(t, h, "t=tvsearch")
	gh60CheckItems(t, items, map[string]gh60Want{
		"b007894v": {"Porridge.S00E02.", "76578"},
	})
	for _, it := range items {
		if it.PID == "m0026d24" {
			t.Errorf("Joy to the World emitted as %q", it.Title)
		}
	}
}

// A feed item whose position number Porridge's TheTVDB list has for
// another episode is left out, as in a search. An item no stored series
// matches is untouched.
func TestGH60_BrowseFeedLeavesOutContradictedPosition(t *testing.T) {
	gh60Fixtures(t, http.StatusOK)
	ibl := gh60BrowseIBL(t, []string{"b007894v"},
		// S00E02 is TheTVDB's "No Way Out" of 1975, and TheTVDB has no
		// special on this date.
		`{"id":"x0000030","type":"episode","title":"Porridge","subtitle":"Christmas Special 1976","release_date":"25 Dec 1976","parent_position":2}`,
		`{"id":"x0000031","type":"episode","title":"Some Other Show","subtitle":"Christmas Special","release_date":"25 Dec 1976","parent_position":2}`,
		// Both Doctor Who lists have other specials at S00E04, but Sonarr
		// files an unnumbered "Doctor.Who" item under neither, so they
		// say nothing about it.
		`{"id":"x0000032","type":"episode","title":"Doctor Who","subtitle":"Christmas Special 2030","release_date":"25 Dec 2030","parent_position":4}`,
	)
	h := gh60Handler(t, ibl, gh60SearchedShows(t))
	items := gh58Serve(t, h, "t=tvsearch")
	for _, it := range items {
		if it.PID == "x0000030" {
			t.Errorf("contradicted position emitted: %q", it.Title)
		}
	}
	gh60CheckItems(t, items, map[string]gh60Want{
		"b007894v": {"Porridge.S00E02.", "76578"},
		"x0000031": {"Some.Other.Show.S00E02.", ""},
		"x0000032": {"Doctor.Who.S00E04.", ""},
	})
}

// A programme that two stored series match as strongly takes neither's
// number.
func TestGH60_BrowseFeedAmbiguousSeries(t *testing.T) {
	ibl := gh60BrowseIBL(t, []string{"b007894v"})
	st := newTestStore(t)
	for _, m := range []*store.SeriesMapping{
		{TVDBId: "76578", ShowName: "Porridge", Year: 1974},
		{TVDBId: "99999", ShowName: "Porridge (1975)", Year: 1975},
	} {
		if err := st.PutSeriesMapping(m); err != nil {
			t.Fatal(err)
		}
	}
	h := NewHandler(ibl, st, nil, nil)
	h.SetEpisodeSource(fixedEpisodes{{Season: 0, Episode: 2, Title: "No Way Out", AirDate: "1975-12-24"}})
	gh60CheckItems(t, gh58Serve(t, h, "t=tvsearch"), map[string]gh60Want{
		"b007894v": {"Porridge.S00E04.", ""},
	})
}

// With the classic series stored too, "Doctor Who" items are checked
// against it, the series an unnumbered "Doctor.Who" item goes to. "The
// Church on Ruby Road"'s position, S00E02, is a 1983 special there, but
// the item takes Doctor Who (2023)'s number, so it is not left out.
func TestGH60_BrowseFeedNumberOverridesGuessCheck(t *testing.T) {
	gh60Fixtures(t, http.StatusOK)
	ibl := gh60BrowseIBL(t, []string{"m001tfnl", "m0026d24"})
	st := gh60SearchedShows(t)
	if err := st.PutSeriesMapping(&store.SeriesMapping{TVDBId: "76107", ShowName: "Doctor Who", Year: 1963}); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(ibl, st, nil, nil)
	h.SetEpisodeSource(extraEpisodes{id: "76107", eps: []TVDBEpisode{
		{Season: 0, Episode: 2, Title: "The Five Doctors", AirDate: "1983-11-25"},
		{Season: 1, Episode: 4, Title: "The Firemaker", AirDate: "1963-12-14"},
	}, next: NewSkyhookEpisodes()})
	gh60CheckItems(t, gh58Serve(t, h, "t=tvsearch"), map[string]gh60Want{
		"m001tfnl": {"Doctor.Who.2023.S00E04.", "449991"},
		"m0026d24": {"Doctor.Who.2023.S00E05.", "449991"},
	})
}

// A custom name set for "Doctor Who" (say "Doctor Who (2005)", once the
// way to file the feed's Doctor Who) must not send an item numbered from
// Doctor Who (2023) out under the other series' name.
func TestGH60_BrowseFeedIgnoresCustomNameOnNumberedItems(t *testing.T) {
	gh60Fixtures(t, http.StatusOK)
	ibl := gh60BrowseIBL(t, []string{"m0026d24"})
	st := gh60SearchedShows(t)
	if err := st.PutOverride(&store.ShowOverride{ShowName: "Doctor Who", CustomName: "Doctor Who (2005)"}); err != nil {
		t.Fatal(err)
	}
	h := gh60Handler(t, ibl, st)
	gh60CheckItems(t, gh58Serve(t, h, "t=tvsearch"), map[string]gh60Want{
		"m0026d24": {"Doctor.Who.2023.S00E05.", "449991"},
	})
}

// An empty list is treated as unavailable, as SkyhookEpisodes treats it:
// probably Skyhook mid-refresh, not a series with no episodes. So Joy to
// the World's guessed S01E04 is left out, as in
// TestGH60_BrowseFeedNeedsEveryCandidate.
func TestGH60_BrowseFeedEmptyListIsUnavailable(t *testing.T) {
	gh60Fixtures(t, http.StatusOK)
	ibl := gh60BrowseIBL(t, []string{"m0026d24", "b007894v"})
	h := NewHandler(ibl, gh60SearchedShows(t), nil, nil)
	h.SetEpisodeSource(extraEpisodes{id: "78804", eps: []TVDBEpisode{}, next: NewSkyhookEpisodes()})
	items := gh58Serve(t, h, "t=tvsearch")
	gh60CheckItems(t, items, map[string]gh60Want{
		"b007894v": {"Porridge.S00E02.", "76578"},
	})
	for _, it := range items {
		if it.PID == "m0026d24" {
			t.Errorf("Joy to the World emitted as %q", it.Title)
		}
	}
}

// The series an unnumbered item is checked against is found ignoring
// case, as Sonarr matches titles.
func TestGH60_BrowseFeedGuessCheckIgnoresCase(t *testing.T) {
	gh60Fixtures(t, http.StatusOK)
	ibl := gh60BrowseIBL(t, nil,
		`{"id":"x0000030","type":"episode","title":"Porridge","subtitle":"Christmas Special 1976","release_date":"25 Dec 1976","parent_position":2}`,
	)
	st := newTestStore(t)
	if err := st.PutSeriesMapping(&store.SeriesMapping{TVDBId: "76578", ShowName: "PORRIDGE", Year: 1974}); err != nil {
		t.Fatal(err)
	}
	for _, it := range gh58Serve(t, gh60Handler(t, ibl, st), "t=tvsearch") {
		if it.PID == "x0000030" {
			t.Errorf("contradicted position emitted: %q", it.Title)
		}
	}
}

// An item that goes out under a custom name is filed under that name's
// series, so the stored series named as its brand says nothing about it.
func TestGH60_BrowseFeedGuessCheckUsesCustomName(t *testing.T) {
	gh60Fixtures(t, http.StatusOK)
	ibl := gh60BrowseIBL(t, nil,
		`{"id":"x0000030","type":"episode","title":"Porridge","subtitle":"Christmas Special 1976","release_date":"25 Dec 1976","parent_position":2}`,
	)
	st := gh60SearchedShows(t)
	if err := st.PutOverride(&store.ShowOverride{ShowName: "Porridge", CustomName: "Porridge Classic"}); err != nil {
		t.Fatal(err)
	}
	gh60CheckItems(t, gh58Serve(t, gh60Handler(t, ibl, st), "t=tvsearch"), map[string]gh60Want{
		"x0000030": {"Porridge.Classic.S00E02.", ""},
	})
}

// One stored series bears out an item's guessed number, and another has
// a lone special on its air date. The air date alone does not move it to
// the other series.
func TestGH60_BrowseFeedGuessBorneOutElsewhere(t *testing.T) {
	ibl := gh60BrowseIBL(t, nil,
		`{"id":"x0000034","type":"episode","title":"Ghosts","subtitle":"Christmas Special","release_date":"25 Dec 2021","parent_position":7}`,
	)
	st := newTestStore(t)
	for _, m := range []*store.SeriesMapping{
		{TVDBId: "355483", ShowName: "Ghosts (2019)", Year: 2019},
		{TVDBId: "396362", ShowName: "Ghosts (US)", Year: 2021},
	} {
		if err := st.PutSeriesMapping(m); err != nil {
			t.Fatal(err)
		}
	}
	h := NewHandler(ibl, st, nil, nil)
	h.SetEpisodeSource(extraEpisodes{id: "355483", eps: []TVDBEpisode{
		{Season: 0, Episode: 7, Title: "The Ghost of Christmas", AirDate: "2021-12-25"},
	}, next: fixedEpisodes{
		{Season: 0, Episode: 1, Title: "Christmas Spirit", AirDate: "2021-12-25"},
	}})
	gh60CheckItems(t, gh58Serve(t, h, "t=tvsearch"), map[string]gh60Want{
		"x0000034": {"Ghosts.S00E07.", ""},
	})
}

// extraEpisodes serves a set list for one tvdbid and asks next for the
// rest.
type extraEpisodes struct {
	id   string
	eps  []TVDBEpisode
	next EpisodeSource
}

func (x extraEpisodes) Episodes(ctx context.Context, tvdbid string) ([]TVDBEpisode, error) {
	if tvdbid == x.id {
		return x.eps, nil
	}
	return x.next.Episodes(ctx, tvdbid)
}

// failingEpisodes fails for one tvdbid and asks next for the rest.
type failingEpisodes struct {
	fail string
	next EpisodeSource
}

func (f failingEpisodes) Episodes(ctx context.Context, tvdbid string) ([]TVDBEpisode, error) {
	if tvdbid == f.fail {
		return nil, fmt.Errorf("skyhook down for %s", tvdbid)
	}
	return f.next.Episodes(ctx, tvdbid)
}
