package newznab

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// Gitea #104 items 1 and 8: a programme whose number matches the search
// but whose content is another episode. Sonarr accepts it as the episode
// searched for and grabs it. The cases are real iPlayer and TheTVDB data
// from the 2026-10-25 survey captures.

// gitea104Outcome is what numberFromTVDB made of p: TheTVDB's SxxEyy, "out"
// when it is left out as contradicted, or "" when it keeps its number.
func gitea104Outcome(p *store.Programme) string {
	switch {
	case p.TVDBNumbered:
		return fmt.Sprintf("S%02dE%02d", p.Series, p.EpisodeNum)
	case p.TVDBContradicted:
		return "out"
	}
	return ""
}

func TestGitea104_RightNumberWrongContent(t *testing.T) {
	all := func(*store.Programme) bool { return true }
	type prog struct {
		p    store.Programme
		want string
	}
	cases := []struct {
		name  string
		eps   []TVDBEpisode
		progs []prog
	}{
		// Series 1 is only assumed (GitHub #32) when the subtitle numbers
		// the episode under a label that is not "Series N".
		{
			"QI, lettered series",
			[]TVDBEpisode{
				{Season: 1, Episode: 1, Title: "Adam", AirDate: "2003-09-11"},
				{Season: 6, Episode: 1, Title: "Families (Children in Need Special)", AirDate: "2008-11-14"},
			},
			[]prog{{store.Programme{PID: "b00fkbs8", Episode: "Series F: 1. Family", Series: 1, SeriesAssumed: true, EpisodeNum: 1, Position: 1, AirDate: "2008-11-14"}, "out"}},
		},
		{
			"Torchwood, a series with a name",
			[]TVDBEpisode{
				{Season: 1, Episode: 1, Title: "Everything Changes", AirDate: "2006-10-22"},
				{Season: 1, Episode: 2, Title: "Day One", AirDate: "2006-10-22"},
				{Season: 3, Episode: 1, Title: "Day One", AirDate: "2009-07-06"},
			},
			[]prog{{store.Programme{PID: "b00ln5cv", Episode: "Children of Earth: 1. Day One", Series: 1, SeriesAssumed: true, EpisodeNum: 1, Position: 1, AirDate: "2009-07-06"}, "out"}},
		},
		{
			"Casualty, a box set under the brand",
			[]TVDBEpisode{{Season: 1, Episode: 1, Title: "Gas", AirDate: "1986-09-06"}},
			[]prog{{store.Programme{PID: "m002vqlh", Episode: "Lethal Legacy: Episode 1", Series: 1, SeriesAssumed: true, EpisodeNum: 1, Position: 1, AirDate: "2026-04-25"}, "out"}},
		},
		{
			"assumed Series 1 that TheTVDB bears out",
			[]TVDBEpisode{{Season: 1, Episode: 3, Title: "Episode 3", AirDate: "1986-09-20"}},
			[]prog{{store.Programme{PID: "x0000040", Episode: "Learning Curve: Episode 3", Series: 1, SeriesAssumed: true, EpisodeNum: 3, AirDate: "1986-09-20"}, ""}},
		},
		{
			"assumed Series 1, nothing at that number",
			[]TVDBEpisode{{Season: 1, Episode: 1, Title: "Gas", AirDate: "1986-09-06"}},
			[]prog{{store.Programme{PID: "x0000041", Episode: "Lethal Legacy: Episode 5", Series: 1, SeriesAssumed: true, EpisodeNum: 5, AirDate: "2026-05-30"}, ""}},
		},
		{
			// 1.9.0 keeps these: a bought-in series reaches iPlayer years
			// after TheTVDB's dates. Here it is 17 years, past the run
			// another show of the name would be beyond, and TheTVDB
			// bears out nothing else.
			"position guess, not a special",
			[]TVDBEpisode{{Season: 1, Episode: 5, Title: "The Hamster", AirDate: "2007-09-25"}},
			[]prog{{store.Programme{PID: "x0000022", Episode: "The Shed", Position: 5, AirDate: "2024-03-01"}, ""}},
		},

		// A specific title TheTVDB has once, outside the week a title
		// match needs, numbers a programme whose own number it does not
		// bear out, in a show TheTVDB bears out (each case lists another
		// programme of the show, as iPlayer has).
		{
			"Red Dwarf X, a year for a date",
			[]TVDBEpisode{
				{Season: 1, Episode: 1, Title: "The End", AirDate: "1988-02-15"},
				{Season: 10, Episode: 1, Title: "Trojan", AirDate: "2012-10-04"},
			},
			[]prog{
				{store.Programme{PID: "p090t6pp", Episode: "X: 1. Trojan", Series: 1, SeriesAssumed: true, EpisodeNum: 1, Position: 1, AirDate: "2012"}, "S10E01"},
				{store.Programme{PID: "b008ncn6", Episode: "I: 1. The End", Series: 1, SeriesAssumed: true, EpisodeNum: 1, Position: 1, AirDate: "1988-02-15"}, "S01E01"},
			},
		},
		{
			"QI, shown a month after TheTVDB's date",
			[]TVDBEpisode{
				{Season: 1, Episode: 2, Title: "Astronomy", AirDate: "2003-09-18"},
				{Season: 16, Episode: 2, Title: "Peril", AirDate: "2018-09-17"},
			},
			[]prog{
				{store.Programme{PID: "b0bkz89w", Episode: "Series P: 2. Peril", Series: 1, SeriesAssumed: true, EpisodeNum: 2, Position: 2, AirDate: "2018-10-20"}, "S16E02"},
				{store.Programme{PID: "b0074g1h", Episode: "Series A: 2. Astronomy", Series: 1, SeriesAssumed: true, EpisodeNum: 2, Position: 2, AirDate: "2003-09-18"}, "S01E02"},
			},
		},
		{
			"title TheTVDB has twice",
			[]TVDBEpisode{
				{Season: 1, Episode: 2, Title: "Astronomy", AirDate: "2003-09-18"},
				{Season: 16, Episode: 2, Title: "Peril", AirDate: "2018-09-17"},
				{Season: 17, Episode: 5, Title: "Peril", AirDate: "2019-10-01"},
			},
			[]prog{{store.Programme{PID: "b0bkz89w", Episode: "Series P: 2. Peril", Series: 1, SeriesAssumed: true, EpisodeNum: 2, Position: 2, AirDate: "2018-10-20"}, "out"}},
		},
		{
			// Would I Lie to You?'s compilations reuse their titles.
			"BBC number TheTVDB bears out on the day",
			[]TVDBEpisode{
				{Season: 13, Episode: 11, Title: "More Unseen Bits (Series 13)", AirDate: "2020-02-21"},
				{Season: 16, Episode: 11, Title: "More Unseen Bits", AirDate: "2023-04-14"},
			},
			[]prog{{store.Programme{PID: "m000fk3m", Episode: "Series 13: 11. More Unseen Bits", Series: 13, EpisodeNum: 11, Position: 11, AirDate: "2020-02-21"}, ""}},
		},
		{
			"BBC number TheTVDB bears out, the title once before",
			[]TVDBEpisode{
				{Season: 12, Episode: 11, Title: "More Unseen Bits", AirDate: "2019-02-01"},
				{Season: 13, Episode: 11, Title: "More Unseen Bits (Series 13)", AirDate: "2020-02-21"},
			},
			[]prog{{store.Programme{PID: "m000fk3m", Episode: "Series 13: 11. More Unseen Bits", Series: 13, EpisodeNum: 11, Position: 11, AirDate: "2020-02-21"}, ""}},
		},
		{
			"title of an episode first shown decades later",
			[]TVDBEpisode{
				{Season: 7, Episode: 1, Title: "Spearhead from Space (1)", AirDate: "1970-01-03"},
				{Season: 7, Episode: 4, Title: "Inferno", AirDate: "1970-01-24"},
				{Season: 9, Episode: 4, Title: "Rose", AirDate: "2015-05-01"},
			},
			[]prog{
				{store.Programme{PID: "x0000042", Episode: "Season 7: 1. Spearhead from Space: Part 1", Series: 7, EpisodeNum: 1, Position: 1, AirDate: "1970-01-03"}, "S07E01"},
				{store.Programme{PID: "x0000043", Episode: "Season 7: 2. Rose", Series: 7, EpisodeNum: 2, Position: 2, AirDate: "1970-01-10"}, ""},
			},
		},
		{
			"Top Gear's compilations are TheTVDB specials",
			[]TVDBEpisode{
				{Season: 1, Episode: 1, Title: "Episode 1", AirDate: "2002-10-20"},
				{Season: 0, Episode: 84, Title: "Best Of (1)", AirDate: "2018-12-16"},
			},
			[]prog{{store.Programme{PID: "b0bw5hyx", Episode: "Best Of: Episode 1", Series: 1, SeriesAssumed: true, EpisodeNum: 1, Position: 1, AirDate: "2018-12-16"}, "S00E84"}},
		},

		// Classic Doctor Who numbers parts within a story: "Episode 1" of
		// The Tenth Planet is TheTVDB's S04E05, not S04E01.
		{
			"part of a story",
			[]TVDBEpisode{
				{Season: 4, Episode: 1, Title: "The Smugglers (1)", AirDate: "1966-09-10"},
				{Season: 4, Episode: 5, Title: "The Tenth Planet (1)", AirDate: "1966-10-08"},
			},
			[]prog{{store.Programme{PID: "p00v2xpn", Episode: "Season 4: The Tenth Planet: Episode 1", Series: 4, EpisodeNum: 1, Position: 5, AirDate: "1966-10-08"}, "S04E05"}},
		},
		{
			"part of a story, no season",
			[]TVDBEpisode{
				{Season: 1, Episode: 1, Title: "An Unearthly Child: An Unearthly Child (1)", AirDate: "1963-11-23"},
				{Season: 4, Episode: 31, Title: "The Faceless Ones (1)", AirDate: "1967-04-08"},
			},
			[]prog{{store.Programme{PID: "p00v2znq", Episode: "The Faceless Ones: Episode 1", Series: 1, SeriesAssumed: true, EpisodeNum: 1, AirDate: "1967-04-08"}, "S04E31"}},
		},
		{
			"animated part, the only version",
			[]TVDBEpisode{
				{Season: 4, Episode: 1, Title: "The Smugglers (1)", AirDate: "1966-09-10"},
				{Season: 4, Episode: 9, Title: "The Power of the Daleks (1)", AirDate: "1966-11-05"},
			},
			[]prog{{store.Programme{PID: "p0ghdslw", Episode: "Season 4: The Power of the Daleks: Episode 1 (Animated)", Series: 4, EpisodeNum: 1, Position: 9, AirDate: "1966-11-05"}, "S04E09"}},
		},
		{
			"animated part beside the original",
			[]TVDBEpisode{
				{Season: 4, Episode: 2, Title: "The Smugglers (2)", AirDate: "1966-09-17"},
				{Season: 4, Episode: 20, Title: "The Underwater Menace (2)", AirDate: "1967-01-21"},
			},
			[]prog{
				{store.Programme{PID: "p00v2z8g", Episode: "Season 4: The Underwater Menace: Episode 2", Series: 4, EpisodeNum: 2, Position: 20, AirDate: "1967-01-21"}, "S04E20"},
				{store.Programme{PID: "m002md6p", Episode: "Season 4: The Underwater Menace: Episode 2 (Animated)", Series: 4, EpisodeNum: 2, Position: 20, AirDate: "1967-01-21"}, "out"},
			},
		},
		{
			"animated and reconstructed parts, no original",
			[]TVDBEpisode{
				{Season: 4, Episode: 1, Title: "The Smugglers (1)", AirDate: "1966-09-10"},
				{Season: 4, Episode: 19, Title: "The Underwater Menace (1)", AirDate: "1967-01-14"},
			},
			[]prog{
				{store.Programme{PID: "m002md6m", Episode: "Season 4: The Underwater Menace: Episode 1 (Animated)", Series: 4, EpisodeNum: 1, Position: 19, AirDate: "1967-01-14"}, "out"},
				{store.Programme{PID: "p00v2z72", Episode: "Season 4: The Underwater Menace: Episode 1 (Reconstruction)", Series: 4, EpisodeNum: 1, Position: 19, AirDate: "1967-01-14"}, "out"},
			},
		},
		{
			"animated story made decades later",
			[]TVDBEpisode{
				{Season: 5, Episode: 1, Title: "The Tomb of the Cybermen (1)", AirDate: "1967-09-02"},
				{Season: 5, Episode: 5, Title: "The Abominable Snowmen (1)", AirDate: "1967-09-30"},
			},
			[]prog{
				{store.Programme{PID: "m001wrs2", Episode: "Season 5: The Abominable Snowmen: Episode 1 (Animated)", Series: 5, EpisodeNum: 1, Position: 5, AirDate: "2024-03-05"}, "S05E05"},
				{store.Programme{PID: "p00v4bs3", Episode: "Season 5: The Tomb of the Cybermen: Episode 1", Series: 5, EpisodeNum: 1, Position: 1, AirDate: "1967-09-02"}, "S05E01"},
			},
		},
		{
			"part of a story TheTVDB does not have",
			[]TVDBEpisode{{Season: 4, Episode: 1, Title: "The Smugglers (1)", AirDate: "1966-09-10"}},
			[]prog{{store.Programme{PID: "x0000044", Episode: "Season 4: The Lost Planet: Episode 1", Series: 4, EpisodeNum: 1, AirDate: "1966-12-31"}, "out"}},
		},
		{
			"first story of a season",
			[]TVDBEpisode{{Season: 1, Episode: 1, Title: "An Unearthly Child: An Unearthly Child (1)", AirDate: "1963-11-23"}},
			[]prog{{store.Programme{PID: "x0000045", Episode: "Season 1: An Unearthly Child: Episode 1", Series: 1, EpisodeNum: 1, Position: 1, AirDate: "1963-11-23"}, ""}},
		},

		// Item 8: iPlayer's bare "Gladiators" brand is the 2024 series.
		// TheTVDB 81208 is the 1992 one.
		{
			"another show of the name, decades later",
			[]TVDBEpisode{
				{Season: 1, Episode: 1, Title: "Heat 1", AirDate: "1992-10-10"},
				{Season: 1, Episode: 2, Title: "Heat 2", AirDate: "1992-10-17"},
				{Season: 8, Episode: 3, Title: "Grand Final", AirDate: "1999-12-25"},
			},
			[]prog{
				{store.Programme{PID: "m001vfg6", Episode: "Series 1: Episode 1", Series: 1, EpisodeNum: 1, Position: 1, AirDate: "2024-01-13"}, "out"},
				{store.Programme{PID: "m001vn7y", Episode: "Series 1: Episode 2", Series: 1, EpisodeNum: 2, Position: 2, AirDate: "2024-01-20"}, "out"},
				// TheTVDB has nothing at S09E01 to contradict.
				{store.Programme{PID: "x0000046", Episode: "Series 9: Episode 1", Series: 9, EpisodeNum: 1, Position: 1, AirDate: "2024-01-13"}, ""},
			},
		},
		{
			// TheTVDB 426330 is an Egyptian series of 2022.
			"another show of the name, years earlier",
			[]TVDBEpisode{{Season: 1, Episode: 1, Title: "Where Does Our Story Begin?", AirDate: "2022-09-29"}},
			[]prog{{store.Programme{PID: "p05j1k3t", Episode: "Series 1: Episode 1", Series: 1, EpisodeNum: 1, Position: 1, AirDate: "2017-11-07"}, "out"}},
		},
		{
			// Keeping Up Appearances b007bs5r: iPlayer's date is a repeat.
			"one far date in a show TheTVDB bears out",
			[]TVDBEpisode{
				{Season: 5, Episode: 1, Title: "The Senior Citizen's Outing", AirDate: "1995-09-03"},
				{Season: 5, Episode: 2, Title: "The Mayor's Fancy Dress Ball", AirDate: "1995-09-10"},
			},
			[]prog{
				{store.Programme{PID: "b007bs5r", Episode: "Series 5: Episode 1", Series: 5, EpisodeNum: 1, Position: 1, AirDate: "2012-06-23"}, ""},
				{store.Programme{PID: "b007ckrt", Episode: "Series 5: Episode 2", Series: 5, EpisodeNum: 2, Position: 2, AirDate: "1995-09-10"}, ""},
			},
		},
		{
			// Waterloo Road's series go on iPlayer whole, weeks before
			// TheTVDB's weekly dates.
			"box set",
			[]TVDBEpisode{
				{Season: 11, Episode: 1, Title: "Episode 1", AirDate: "2023-01-03"},
				{Season: 11, Episode: 7, Title: "Episode 7", AirDate: "2023-02-14"},
			},
			[]prog{
				{store.Programme{PID: "p0djrdv5", Episode: "Series 11: Episode 1", Series: 11, EpisodeNum: 1, Position: 1, AirDate: "2023-01-03"}, ""},
				{store.Programme{PID: "p0djrgm0", Episode: "Series 11: Episode 7", Series: 11, EpisodeNum: 7, Position: 7, AirDate: "2023-01-03"}, ""},
			},
		},
	}
	for _, c := range cases {
		var progs []*store.Programme
		for i := range c.progs {
			p := c.progs[i].p
			p.Name = "Show"
			progs = append(progs, &p)
		}
		newTVDBIndex(c.eps).numberFromTVDB(progs, all, all)
		for i, p := range progs {
			if got := gitea104Outcome(p); got != c.progs[i].want {
				t.Errorf("%s: %s %q gave %q, want %q", c.name, p.PID, p.Episode, got, c.progs[i].want)
			}
		}
	}
}

// Through the handler: Gladiators (1992) S01E01 returned the 2024 series'
// first episode as Gladiators.S01E01 with tvdbid 81208. Gitea #104 item 8.
func TestGitea104_OtherShowOfTheNameAnswersNothing(t *testing.T) {
	h := NewHandler(nil, newTestStore(t), nil, nil)
	h.SetEpisodeSource(fixedEpisodes{
		{Season: 1, Episode: 1, Title: "Heat 1", AirDate: "1992-10-10"},
		{Season: 8, Episode: 3, Title: "Grand Final", AirDate: "1999-12-25"},
	})
	results := []bbc.IBLResult{
		{PID: "m001vfg6", Title: "Gladiators", Subtitle: "Series 1: Episode 1", Series: 1, EpisodeNum: 1, Position: 1, AirDate: "2024-01-13"},
	}
	req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch", nil)
	w := httptest.NewRecorder()
	h.writeResultsRSS(w, req, results, nil, 1, 1, "", "Gladiators", 1992, "81208", "", false, 0)
	if items := gh58ParseItems(t, w.Body.Bytes()); len(items) != 0 {
		t.Errorf("Gladiators (1992) S01E01 returned %q", items[0].Title)
	}
}

// Through the handler: a classic serial's part answers the search for
// TheTVDB's number and not the one BBC's subtitle gives it.
func TestGitea104_StoryPartAnswersItsTVDBNumber(t *testing.T) {
	h := NewHandler(nil, newTestStore(t), nil, nil)
	h.SetEpisodeSource(fixedEpisodes{
		{Season: 4, Episode: 1, Title: "The Smugglers (1)", AirDate: "1966-09-10"},
		{Season: 4, Episode: 5, Title: "The Tenth Planet (1)", AirDate: "1966-10-08"},
	})
	results := []bbc.IBLResult{
		{PID: "p00v2xpn", Title: "Doctor Who (1963-1996)", Subtitle: "Season 4: The Tenth Planet: Episode 1", Series: 4, EpisodeNum: 1, Position: 5, AirDate: "1966-10-08"},
	}
	search := func(ep int) []gh58Item {
		req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch", nil)
		w := httptest.NewRecorder()
		h.writeResultsRSS(w, req, results, nil, 4, ep, "", "Doctor Who", 1963, "76107", "", false, 0)
		return gh58ParseItems(t, w.Body.Bytes())
	}
	if items := search(1); len(items) != 0 {
		t.Errorf("S04E01, The Smugglers (1), returned %q", items[0].Title)
	}
	if got := gh60Numbers(t, search(5))["p00v2xpn"]; got != "S04E05" {
		t.Errorf("S04E05 search numbered p00v2xpn %q, want S04E05", got)
	}
}

// The RSS feed resolves with the same rules: with only Gladiators (1992)
// searched, the 2024 series' episodes would go out as Gladiators.S01E01,
// which Sonarr files under the series of that name.
func TestGitea104_BrowseFeedLeavesOutOtherShowOfTheName(t *testing.T) {
	ibl := gh60BrowseIBL(t, nil,
		`{"id":"m001vfg6","type":"episode","title":"Gladiators","subtitle":"Series 1: Episode 1","release_date":"13 Jan 2024","parent_position":1}`,
		`{"id":"x0000047","type":"episode","title":"Some Other Show","subtitle":"Series 1: Episode 1","release_date":"13 Jan 2024","parent_position":1}`,
	)
	st := newTestStore(t)
	if err := st.PutSeriesMapping(&store.SeriesMapping{TVDBId: "81208", ShowName: "Gladiators", Year: 1992}); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(ibl, st, nil, nil)
	h.SetEpisodeSource(fixedEpisodes{
		{Season: 1, Episode: 1, Title: "Heat 1", AirDate: "1992-10-10"},
		{Season: 8, Episode: 3, Title: "Grand Final", AirDate: "1999-12-25"},
	})
	items := gh58Serve(t, h, "t=tvsearch")
	for _, it := range items {
		if it.PID == "m001vfg6" {
			t.Errorf("2024 episode in the feed as %q", it.Title)
		}
	}
	gh60CheckItems(t, items, map[string]gh60Want{"x0000047": {"Some.Other.Show.S01E01.", ""}})
}

// A Show Override that renumbers a contradicted programme keeps it, as it
// keeps a contradicted special: the user has said what its number is.
func TestGitea104_OverrideKeepsContradictedAssumedSeries(t *testing.T) {
	st := newTestStore(t)
	if err := st.PutOverride(&store.ShowOverride{ShowName: "QI", SeriesOffset: 5}); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(nil, st, nil, nil)
	h.SetEpisodeSource(fixedEpisodes{
		{Season: 1, Episode: 1, Title: "Adam", AirDate: "2003-09-11"},
		{Season: 6, Episode: 1, Title: "Families (Children in Need Special)", AirDate: "2008-11-14"},
	})
	results := []bbc.IBLResult{{PID: "b00fkbs8", Title: "QI", Subtitle: "Series F: 1. Family", EpisodeNum: 1, Position: 1, AirDate: "2008-11-14"}}
	run := func() map[string]string {
		req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch", nil)
		w := httptest.NewRecorder()
		h.writeResultsRSS(w, req, results, nil, noSeasonFilter, 0, "", "QI", 2003, "72716", "", false, 0)
		return gh60Numbers(t, gh58ParseItems(t, w.Body.Bytes()))
	}
	if got := run()["b00fkbs8"]; got != "S06E01" {
		t.Errorf("with a series offset of 5: numbered %q, want S06E01", got)
	}
	if err := st.DeleteOverride("QI"); err != nil {
		t.Fatal(err)
	}
	if se, ok := run()["b00fkbs8"]; ok {
		t.Errorf("without the override: emitted as %s", se)
	}
}

// episodesByID serves a TheTVDB list per tvdbid.
type episodesByID map[string][]TVDBEpisode

func (e episodesByID) Episodes(_ context.Context, tvdbid string) ([]TVDBEpisode, error) {
	return e[tvdbid], nil
}

// The feed matches "Doctor Who (2005-2022)" to both Doctor Who series
// Sonarr has searched. A title the classic series has once does not number
// a programme whose own number Doctor Who (2005) bears out, although
// Doctor Who (2005) has no title for it.
func TestGitea104_BrowseFeedTitleAnyDateGivesWay(t *testing.T) {
	ibl := gh60BrowseIBL(t, nil,
		`{"id":"x0000049","type":"episode","title":"Doctor Who (2005-2022)","subtitle":"Series 4: 10. Midnight","release_date":"14 Jun 2008","parent_position":10}`,
		`{"id":"x0000050","type":"episode","title":"Doctor Who (1963-1996)","subtitle":"Season 4: The Tenth Planet: Episode 1","release_date":"8 Oct 1966","parent_position":5}`,
	)
	st := newTestStore(t)
	for _, m := range []*store.SeriesMapping{
		{TVDBId: "76107", ShowName: "Doctor Who", Year: 1963},
		{TVDBId: "78804", ShowName: "Doctor Who (2005)", Year: 2005},
	} {
		if err := st.PutSeriesMapping(m); err != nil {
			t.Fatal(err)
		}
	}
	h := NewHandler(ibl, st, nil, nil)
	h.SetEpisodeSource(episodesByID{
		"76107": {
			{Season: 4, Episode: 5, Title: "The Tenth Planet (1)", AirDate: "1966-10-08"},
			{Season: 4, Episode: 10, Title: "The Power of the Daleks (2)", AirDate: "1966-11-12"},
			{Season: 9, Episode: 4, Title: "Midnight", AirDate: "1972-01-01"},
		},
		"78804": {
			{Season: 4, Episode: 1, Title: "Partners in Crime", AirDate: "2008-04-05"},
			{Season: 4, Episode: 10, Title: "Episode Ten", AirDate: "2008-06-14"},
		},
	})
	gh60CheckItems(t, gh58Serve(t, h, "t=tvsearch"), map[string]gh60Want{
		"x0000049": {"Doctor.Who.2005-2022.S04E10.", ""},
		"x0000050": {"Doctor.Who.S04E05.", "76107"},
	})
}
