package bbc

import "testing"

// Episode numbers assignMissingEpisodeNumbers gives from air-date order
// are marked as such, so the TheTVDB check can treat them as the guess
// they are. Gitea #104.
func TestAssignMissingEpisodeNumbers_MarksAssigned(t *testing.T) {
	results := []IBLResult{
		{PID: "m0031xhw", Subtitle: "Series 24 Extras: Pairing Dance: Group Two", Series: 24, AirDate: "2026-09-19"},
		{PID: "m00328xl", Subtitle: "Series 24: Week 1", Series: 24, AirDate: "2026-09-26"},
		{PID: "b0074dlv", Subtitle: "Series 1: 1. Rose", Series: 1, EpisodeNum: 1, Position: 1, AirDate: "2005-03-26"},
	}
	assignMissingEpisodeNumbers(results)
	for _, c := range []struct {
		i        int
		ep       int
		assigned bool
	}{{0, 1, true}, {1, 2, true}, {2, 1, false}} {
		r := results[c.i]
		if r.EpisodeNum != c.ep || r.EpisodeNumAssigned != c.assigned {
			t.Errorf("%s: EpisodeNum %d assigned %v, want %d %v", r.PID, r.EpisodeNum, r.EpisodeNumAssigned, c.ep, c.assigned)
		}
	}
}
