package newznab

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// overrideFilterRun runs one search over results through writeResultsRSS
// with o stored as "Some Show"'s Show Override, and returns each emitted
// PID's SxxEyy ("none" for a title with no number).
func overrideFilterRun(t *testing.T, o *store.ShowOverride, eps EpisodeSource, tvdbid string, results []bbc.IBLResult, season, ep int) map[string]string {
	t.Helper()
	st := newTestStore(t)
	if o != nil {
		o.ShowName = "Some Show"
		if err := st.PutOverride(o); err != nil {
			t.Fatal(err)
		}
	}
	h := NewHandler(nil, st, nil, nil)
	if eps != nil {
		h.SetEpisodeSource(eps)
	}
	req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch", nil)
	w := httptest.NewRecorder()
	h.writeResultsRSS(w, req, results, nil, season, ep, "", "Some Show", 0, tvdbid, "", false, 0)
	return gh60Numbers(t, gh58ParseItems(t, w.Body.Bytes()))
}

// A programme the BBC numbers is filtered on the number its title
// carries, with its Show Override applied, as one with no BBC numbers
// has been since v1.9.1. Up to v1.9.1 the filter read BBC's numbers:
// with a Series Offset of 1, BBC's Series 2 Episode 3 goes out as
// S03E03, so the S03E03 search found nothing and the S02E03 search
// returned it titled S03E03. Gitea #104.
func TestGH104_OverrideNumberOnBBCNumberedEpisodeIsTheOneFiltered(t *testing.T) {
	dated := bbc.IBLResult{PID: "p0000203", Title: "Some Show", Subtitle: "Series 2: 3. The Third One", Series: 2, EpisodeNum: 3, Position: 9, AirDate: "2026-10-20"}
	undated := dated
	undated.AirDate = ""
	cases := []struct {
		name     string
		res      bbc.IBLResult
		override store.ShowOverride
		want     string   // the SxxEyy its title carries
		notFor   []string // searches it must not answer
	}{
		{"series offset", dated, store.ShowOverride{SeriesOffset: 1}, "S03E03", []string{"S02E03"}},
		{"series offset, no air date", undated, store.ShowOverride{SeriesOffset: 1}, "S03E03", []string{"S02E03"}},
		{"episode offset", dated, store.ShowOverride{EpisodeOffset: -1}, "S02E02", []string{"S02E03"}},
		{"force series", dated, store.ShowOverride{ForceSeriesNum: 5}, "S05E03", []string{"S02E03"}},
		// Force Position drops BBC's numbers, so the position numbers it.
		{"force position", dated, store.ShowOverride{ForcePosition: true}, "S01E09", []string{"S02E03"}},
		{"force position and series", dated, store.ShowOverride{ForcePosition: true, ForceSeriesNum: 4}, "S04E09", []string{"S02E03", "S01E09"}},
		// A custom name leaves the numbers as they are.
		{"custom name only", dated, store.ShowOverride{CustomName: "Another Name"}, "S02E03", []string{"S03E03"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var s, e int
			fmt.Sscanf(c.want, "S%02dE%02d", &s, &e)
			o := c.override
			got := overrideFilterRun(t, &o, nil, "", []bbc.IBLResult{c.res}, s, e)
			if got[c.res.PID] != c.want {
				t.Errorf("%s search: numbered %q, want %s", c.want, got[c.res.PID], c.want)
			}
			o = c.override
			if got := overrideFilterRun(t, &o, nil, "", []bbc.IBLResult{c.res}, s, 0)[c.res.PID]; got != c.want {
				t.Errorf("season %d search: numbered %q, want %s", s, got, c.want)
			}
			for _, se := range c.notFor {
				fmt.Sscanf(se, "S%02dE%02d", &s, &e)
				o = c.override
				if got, ok := overrideFilterRun(t, &o, nil, "", []bbc.IBLResult{c.res}, s, e)[c.res.PID]; ok {
					t.Errorf("%s search returned it as %s", se, got)
				}
			}
		})
	}
}

// Force Date Based gives a programme the BBC numbers a date title, which
// carries no number. It answers the search for its own numbers, as it
// did before, and no other: Sonarr reads a date title only for a Daily
// series, which it searches by air date, so an S/E search shows it but
// never grabs it, and letting it answer every S/E search would only add
// to that noise (Gitea #100). Its air date search finds it. Gitea #104.
func TestGH104_ForceDateBasedAnswersItsOwnNumbers(t *testing.T) {
	res := []bbc.IBLResult{{PID: "p0000203", Title: "Some Show", Subtitle: "Series 2: 3. The Third One", Series: 2, EpisodeNum: 3, Position: 9, AirDate: "2026-10-20"}}
	run := func(season, ep int) (string, bool) {
		got, ok := overrideFilterRun(t, &store.ShowOverride{ForceDateBased: true}, nil, "", res, season, ep)["p0000203"]
		return got, ok
	}
	if got, ok := run(2, 3); !ok || got != "none" {
		t.Errorf("S02E03 search: got %q (present %v), want its date title", got, ok)
	}
	for _, se := range [][2]int{{2, 4}, {1, 9}, {0, 3}} {
		if got, ok := run(se[0], se[1]); ok {
			t.Errorf("S%02dE%02d search returned it as %s", se[0], se[1], got)
		}
	}

	st := newTestStore(t)
	if err := st.PutOverride(&store.ShowOverride{ShowName: "Some Show", ForceDateBased: true}); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(nil, st, nil, nil)
	req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch", nil)
	w := httptest.NewRecorder()
	h.writeResultsRSS(w, req, res, nil, 2026, 0, "2026-10-20", "Some Show", 0, "", "", false, 0)
	gh58AssertTitle(t, gh58ParseItems(t, w.Body.Bytes()), "p0000203",
		"Some.Show.2026.10.20.Series.2.3.The.Third.One.720p.WEB-DL.AAC.H264-iParr")
}

// TheTVDB's numbers ignore the numbering overrides in the title
// (GenerateTitle, GitHub #60), so the filter ignores them too: an
// episode TheTVDB has as S01E03 answers S01E03 whatever its Series
// Offset says. Force Date Based still dates it, and it then answers
// TheTVDB's number, as an episode BBC numbers answers BBC's. Gitea #104.
func TestGH104_TheTVDBNumbersIgnoreNumericOverridesInTheFilter(t *testing.T) {
	eps := fixedEpisodes{{Season: 1, Episode: 3, Title: "The Third One", AirDate: "2026-10-20"}}
	res := []bbc.IBLResult{{PID: "p0000203", Title: "Some Show", Subtitle: "Series 2: 3. The Third One", Series: 2, EpisodeNum: 3, Position: 9, AirDate: "2026-10-20"}}
	for name, o := range map[string]store.ShowOverride{
		"series offset":  {SeriesOffset: 1},
		"episode offset": {EpisodeOffset: 2},
		"force series":   {ForceSeriesNum: 5},
		"force position": {ForcePosition: true},
	} {
		t.Run(name, func(t *testing.T) {
			oo := o
			if got := overrideFilterRun(t, &oo, eps, "12345", res, 1, 3)["p0000203"]; got != "S01E03" {
				t.Errorf("S01E03 search: numbered %q, want S01E03", got)
			}
			for _, se := range [][2]int{{2, 3}, {3, 3}, {2, 5}, {5, 3}, {1, 9}} {
				oo = o
				if got, ok := overrideFilterRun(t, &oo, eps, "12345", res, se[0], se[1])["p0000203"]; ok {
					t.Errorf("S%02dE%02d search returned it as %s", se[0], se[1], got)
				}
			}
		})
	}
	if got, ok := overrideFilterRun(t, &store.ShowOverride{ForceDateBased: true}, eps, "12345", res, 1, 3)["p0000203"]; !ok || got != "none" {
		t.Errorf("Force Date Based, S01E03 search: got %q (present %v), want its date title", got, ok)
	}
	if got, ok := overrideFilterRun(t, &store.ShowOverride{ForceDateBased: true}, eps, "12345", res, 2, 3)["p0000203"]; ok {
		t.Errorf("Force Date Based, S02E03 search returned it as %s", got)
	}
}

// A series the BBC numbers by year ("Series 2024: 3.") goes out as
// S2024E03, which ParseTitle cannot read back. An override that moves
// the number leaves it answering the search it answered before rather
// than none. Gitea #104.
func TestGH104_UnreadableOverrideNumberKeepsBBCNumbers(t *testing.T) {
	p := &store.Programme{Name: "Some Show", Episode: "Series 2024: 3. Third", Series: 2024, EpisodeNum: 3, AirDate: "2024-10-20"}
	o := &store.ShowOverride{EpisodeOffset: 1}
	if !matchesSearchFilter(p, o, "Some Show", "", 2024, 3) {
		t.Errorf("season 2024 episode 3 search left it out")
	}
	if matchesSearchFilter(p, o, "Some Show", "", 2024, 5) {
		t.Errorf("season 2024 episode 5 search returned it")
	}
}

// A programme with no BBC numbers and no air date answers no episode
// search for its guessed number (TestGH60_UndatedGuessAnswersNoEpisodeSearch),
// but an override that changes the number in its title is the user's
// numbering, not a guess, and is what Sonarr reads from the RSS feed, so
// it answers that number's search. An override that leaves the title as
// it was leaves the programme as it was. Gitea #104.
func TestGH104_UndatedProgrammeAnswersItsOverrideNumber(t *testing.T) {
	res := []bbc.IBLResult{{PID: "p0000005", Title: "Some Show", Subtitle: "The Fifth One", Position: 5}}
	if got := overrideFilterRun(t, &store.ShowOverride{ForceSeriesNum: 3}, nil, "", res, 3, 5)["p0000005"]; got != "S03E05" {
		t.Errorf("Force series 3, S03E05 search: numbered %q, want S03E05", got)
	}
	if got, ok := overrideFilterRun(t, &store.ShowOverride{ForceSeriesNum: 3}, nil, "", res, 1, 5)["p0000005"]; ok {
		t.Errorf("Force series 3, S01E05 search returned it as %s", got)
	}
	// A Series Offset does not move a title numbered from its position.
	if got, ok := overrideFilterRun(t, &store.ShowOverride{SeriesOffset: 1}, nil, "", res, 1, 5)["p0000005"]; ok {
		t.Errorf("Series offset 1, S01E05 search returned it as %s", got)
	}
}
