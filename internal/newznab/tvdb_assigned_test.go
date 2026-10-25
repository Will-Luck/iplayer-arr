package newznab

import (
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// iPlayer lists Strictly Come Dancing's Series 24 shows and its "Series 24
// Extras" clips with a series and no episode numbers, so
// assignMissingEpisodeNumbers numbers them all by air date. A clip of the
// pairing dances went out as S24E05, TheTVDB's "Week 3". Gitea #104.
func TestGitea104_AssignedNumberContradictedLeftOut(t *testing.T) {
	all := func(*store.Programme) bool { return true }
	idx := newTVDBIndex([]TVDBEpisode{
		{Season: 23, Episode: 2, Title: "Week 1", AirDate: "2025-09-27"},
		{Season: 24, Episode: 5, Title: "Week 3", AirDate: "2026-10-10"},
	})
	clip := IBLResultToProgramme(bbc.IBLResult{PID: "m0031xhy", Title: "Strictly Come Dancing", Subtitle: "Series 24 Extras: Pairing Dance: Group Three", Series: 24, EpisodeNum: 5, EpisodeNumAssigned: true, AirDate: "2026-09-19"})
	// Series 23 had no clips, so date order gave the shows TheTVDB's numbers.
	show := IBLResultToProgramme(bbc.IBLResult{PID: "m002k7dp", Title: "Strictly Come Dancing", Subtitle: "Series 23: Week 1", Series: 23, EpisodeNum: 2, EpisodeNumAssigned: true, AirDate: "2025-09-27"})
	// The same number from BBC's own data keeps it, as a box set's does.
	bbcNumbered := IBLResultToProgramme(bbc.IBLResult{PID: "x0000048", Title: "Strictly Come Dancing", Subtitle: "Series 24: 5. The Clip Show", Series: 24, EpisodeNum: 5, Position: 5, AirDate: "2026-09-19"})
	idx.numberFromTVDB([]*store.Programme{clip, show, bbcNumbered}, all, all)
	for _, c := range []struct {
		p    *store.Programme
		want string
	}{{clip, "out"}, {show, ""}, {bbcNumbered, ""}} {
		if got := gitea104Outcome(c.p); got != c.want {
			t.Errorf("%s %q gave %q, want %q", c.p.PID, c.p.Episode, got, c.want)
		}
	}
}
