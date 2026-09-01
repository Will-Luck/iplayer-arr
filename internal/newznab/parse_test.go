package newznab

import (
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// TestParseTitle_RoundTripsGenerateTitle is the important one: every case
// runs a Programme through GenerateTitle and feeds the real emitted
// title straight back into ParseTitle. The parser is therefore pinned to
// the formats this project actually produces rather than to strings
// invented for the test. If GenerateTitle's layout ever changes, this
// fails instead of the ordering silently degrading.
func TestParseTitle_RoundTripsGenerateTitle(t *testing.T) {
	tests := []struct {
		name     string
		prog     *store.Programme
		quality  string
		wantTier string
		want     EpisodeIdentity
	}{
		{
			name: "tier full, series and episode known",
			prog: &store.Programme{
				Name: "Doctor Who", Episode: "The Unquiet Dead",
				Series: 1, EpisodeNum: 3,
			},
			quality:  "720p",
			wantTier: store.TierFull,
			want:     EpisodeIdentity{ShowName: "Doctor Who", Season: 1, Episode: 3},
		},
		{
			name: "tier position, parent position as episode in series 1",
			prog: &store.Programme{
				Name: "Blue Peter", Episode: "The Big Day Out", Position: 5,
			},
			quality:  "720p",
			wantTier: store.TierPosition,
			want:     EpisodeIdentity{ShowName: "Blue Peter", Season: 1, Episode: 5},
		},
		{
			name: "tier date with an episode subtitle",
			prog: &store.Programme{
				Name: "EastEnders", Episode: "Episode 6521", AirDate: "2026-03-28",
			},
			quality:  "540p",
			wantTier: store.TierDate,
			want:     EpisodeIdentity{ShowName: "EastEnders", AirDate: "2026-03-28"},
		},
		{
			name: "tier date, daily soap whose subtitle is a bare date",
			prog: &store.Programme{
				Name: "EastEnders", Episode: "06/04/2026",
				AirDate: "2026-04-06", Position: 7307,
			},
			quality:  "1080p",
			wantTier: store.TierDate,
			want:     EpisodeIdentity{ShowName: "EastEnders", AirDate: "2026-04-06"},
		},
		{
			name: "special, S00E<mmdd> from the air date",
			prog: &store.Programme{
				Name: "Doctor Who", Episode: "Christmas Special", AirDate: "2025-12-25",
			},
			quality:  "1080p",
			wantTier: store.TierFull,
			want:     EpisodeIdentity{ShowName: "Doctor Who", Season: 0, Episode: 1225},
		},
		{
			name: "tier manual, no numbering at all",
			prog: &store.Programme{
				Name: "Secret History", Episode: "The Lost City",
			},
			quality:  "720p",
			wantTier: store.TierManual,
			want:     EpisodeIdentity{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, tier := GenerateTitle(tt.prog, tt.quality, nil)
			if tier != tt.wantTier {
				t.Fatalf("GenerateTitle tier = %q, want %q (title %q)", tier, tt.wantTier, title)
			}
			got := ParseTitle(title)
			if got != tt.want {
				t.Errorf("ParseTitle(%q)\n got = %+v\nwant = %+v", title, got, tt.want)
			}
		})
	}
}

// TestParseTitle_ProductionTitles uses release titles copied verbatim
// from this server's iplayer-arr logs, including the interleaved bulk
// Sonarr grab that GitHub #51 was reported against.
func TestParseTitle_ProductionTitles(t *testing.T) {
	tests := []struct {
		title string
		want  EpisodeIdentity
	}{
		{
			"Knee.High.Spies.S01E07.Mission.Dino.in.Danger.1080p.WEB-DL.AAC.H264-iParr",
			EpisodeIdentity{ShowName: "Knee High Spies", Season: 1, Episode: 7},
		},
		{
			"Knee.High.Spies.S01E20.Mission.Hooray.for.Holidays.1080p.WEB-DL.AAC.H264-iParr",
			EpisodeIdentity{ShowName: "Knee High Spies", Season: 1, Episode: 20},
		},
		{
			// The episode subtitle itself contains "Series.1", after the
			// numbering. The first SxxExx must win.
			"Do.Not.Watch.This.Show.S01E01.Series.1.Frog.1080p.WEB-DL.AAC.H264-iParr",
			EpisodeIdentity{ShowName: "Do Not Watch This Show", Season: 1, Episode: 1},
		},
		{
			"Match.of.the.Day.2026.03.22.1080p.WEB-DL.AAC.H264-iParr",
			EpisodeIdentity{ShowName: "Match of the Day", AirDate: "2026-03-22"},
		},
		{
			// Sonarr may hand back the NZB filename rather than the
			// release name.
			"Casualty.2026.04.06.720p.WEB-DL.AAC.H264-iParr.nzb",
			EpisodeIdentity{ShowName: "Casualty", AirDate: "2026-04-06"},
		},
	}

	for _, tt := range tests {
		if got := ParseTitle(tt.title); got != tt.want {
			t.Errorf("ParseTitle(%q)\n got = %+v\nwant = %+v", tt.title, got, tt.want)
		}
	}
}

// TestParseTitle_NoIdentityIsNotAFailure pins the total-parse contract.
// None of these may panic, and all must return the zero value so the
// queue orders them by creation time instead.
func TestParseTitle_NoIdentityIsNotAFailure(t *testing.T) {
	titles := []string{
		"",
		"   ",
		".",
		"..nzb",
		"m002ypj4", // bare PID, the last-resort fallback in handleAdd
		"Secret.History.The.Lost.City.720p.WEB-DL.AAC.H264-iParr",
		// Radarr movie releases: "<name>.<year>.<quality>.WEB-DL...".
		// The trailing year must not be mistaken for an air date.
		"Blade.Runner.2049.1080p.WEB-DL.AAC.H264-iParr",
		"Blade.Runner.2049.2017.1080p.WEB-DL.AAC.H264-iParr",
		"Apollo.13.1995.1080p.WEB-DL.AAC.H264-iParr",
		"1917.2019.2160p.WEB-DL.AAC.H264-iParr",
		// Show names that flirt with the numbering patterns.
		"Se7en.1995.1080p.WEB-DL.AAC.H264-iParr",
		"Doctor.Who.1963.to.1996.1080p.WEB-DL.AAC.H264-iParr",
		// Out-of-range date components must be rejected, not clamped.
		"Some.Show.2026.13.01.1080p.WEB-DL.AAC.H264-iParr",
		"Some.Show.2026.02.00.1080p.WEB-DL.AAC.H264-iParr",
	}

	for _, title := range titles {
		got := ParseTitle(title)
		if got != (EpisodeIdentity{}) {
			t.Errorf("ParseTitle(%q) = %+v, want the zero identity", title, got)
		}
	}
}

// TestParseTitle_SxxExxWinsOverDate covers a release that carries both a
// real SxxExx and a date somewhere in the episode subtitle. Sonarr
// matches such a release on its numbering, so that is its identity.
func TestParseTitle_SxxExxWinsOverDate(t *testing.T) {
	got := ParseTitle("Newsnight.S01E03.Budget.2026.03.11.Analysis.720p.WEB-DL.AAC.H264-iParr")
	want := EpisodeIdentity{ShowName: "Newsnight", Season: 1, Episode: 3}
	if got != want {
		t.Errorf("got = %+v, want = %+v", got, want)
	}
}
