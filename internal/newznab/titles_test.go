package newznab

import (
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/store"
)

func TestGenerateTitleTier1(t *testing.T) {
	p := &store.Programme{
		Name:       "Doctor Who",
		Episode:    "The Unquiet Dead",
		Series:     1,
		EpisodeNum: 3,
	}
	title, tier := GenerateTitle(p, "720p", nil)
	if tier != store.TierFull {
		t.Errorf("tier = %q, want full", tier)
	}
	expected := "Doctor.Who.S01E03.The.Unquiet.Dead.720p.WEB-DL.AAC.H264-iParr"
	if title != expected {
		t.Errorf("title = %q\nwant  = %q", title, expected)
	}
}

func TestGenerateTitleTier1WithOverride(t *testing.T) {
	p := &store.Programme{
		Name:       "Doctor Who",
		Episode:    "The Unquiet Dead",
		Series:     1,
		EpisodeNum: 3,
	}
	override := &store.ShowOverride{SeriesOffset: 5, CustomName: "Dr Who"}
	title, tier := GenerateTitle(p, "720p", override)
	if tier != store.TierFull {
		t.Errorf("tier = %q", tier)
	}
	expected := "Dr.Who.S06E03.The.Unquiet.Dead.720p.WEB-DL.AAC.H264-iParr"
	if title != expected {
		t.Errorf("title = %q\nwant  = %q", title, expected)
	}
}

func TestGenerateTitleTier2(t *testing.T) {
	p := &store.Programme{
		Name:     "Blue Peter",
		Episode:  "The Big Day Out",
		Position: 5,
	}
	title, tier := GenerateTitle(p, "720p", nil)
	if tier != store.TierPosition {
		t.Errorf("tier = %q, want position", tier)
	}
	expected := "Blue.Peter.S01E05.The.Big.Day.Out.720p.WEB-DL.AAC.H264-iParr"
	if title != expected {
		t.Errorf("title = %q\nwant  = %q", title, expected)
	}
}

func TestGenerateTitleTier3(t *testing.T) {
	p := &store.Programme{
		Name:    "EastEnders",
		Episode: "Episode 6521",
		AirDate: "2026-03-28",
	}
	title, tier := GenerateTitle(p, "540p", nil)
	if tier != store.TierDate {
		t.Errorf("tier = %q, want date", tier)
	}
	expected := "EastEnders.2026.03.28.Episode.6521.540p.WEB-DL.AAC.H264-iParr"
	if title != expected {
		t.Errorf("title = %q\nwant  = %q", title, expected)
	}
}

func TestGenerateTitleTier4(t *testing.T) {
	p := &store.Programme{
		Name:    "Secret History",
		Episode: "The Lost City",
	}
	title, tier := GenerateTitle(p, "720p", nil)
	if tier != store.TierManual {
		t.Errorf("tier = %q, want manual", tier)
	}
	expected := "Secret.History.The.Lost.City.720p.WEB-DL.AAC.H264-iParr"
	if title != expected {
		t.Errorf("title = %q\nwant  = %q", title, expected)
	}
}

func TestGenerateTitleSpecial(t *testing.T) {
	p := &store.Programme{
		Name:    "Doctor Who",
		Episode: "Christmas Special",
		AirDate: "2026-12-25",
	}
	title, tier := GenerateTitle(p, "1080p", nil)
	if tier != store.TierFull {
		t.Errorf("tier = %q, want full (special)", tier)
	}
	expected := "Doctor.Who.S00E1225.Christmas.Special.1080p.WEB-DL.AAC.H264-iParr"
	if title != expected {
		t.Errorf("title = %q\nwant  = %q", title, expected)
	}
}

func TestGenerateTitleForceDateBased(t *testing.T) {
	p := &store.Programme{
		Name:       "The One Show",
		Episode:    "Episode 42",
		Series:     1,
		EpisodeNum: 42,
		AirDate:    "2026-04-01",
	}
	override := &store.ShowOverride{ForceDateBased: true}
	title, tier := GenerateTitle(p, "720p", override)
	if tier != store.TierDate {
		t.Errorf("tier = %q, want date", tier)
	}
	expected := "The.One.Show.2026.04.01.Episode.42.720p.WEB-DL.AAC.H264-iParr"
	if title != expected {
		t.Errorf("title = %q\nwant  = %q", title, expected)
	}
}

func TestGenerateTitleForcePosition(t *testing.T) {
	p := &store.Programme{
		Name:       "Newsnight",
		Episode:    "Budget Analysis",
		Series:     2,
		EpisodeNum: 15,
		Position:   3,
	}
	override := &store.ShowOverride{ForcePosition: true}
	title, tier := GenerateTitle(p, "720p", override)
	if tier != store.TierPosition {
		t.Errorf("tier = %q, want position", tier)
	}
	expected := "Newsnight.S01E03.Budget.Analysis.720p.WEB-DL.AAC.H264-iParr"
	if title != expected {
		t.Errorf("title = %q\nwant  = %q", title, expected)
	}
}

func TestGenerateTitleSubtitleIsBareDate(t *testing.T) {
	// BBC daily soaps (EastEnders, Casualty, Holby City, Coronation Street,
	// Doctors, Neighbours) come from iPlayer with the subtitle as a literal
	// date and parent_position as a flat cumulative counter. Without
	// auto-detection, the title would emit "S01E7307" via Tier 2, which
	// Sonarr's parser maps to season 1 episode 7307 — no such episode exists
	// for any of these long-running shows, and the release is rejected.
	//
	// When the subtitle is a bare date and we have an air date, GenerateTitle
	// must promote to date tier so Sonarr's daily-episode parser matches by
	// air date and finds the correct S/E.
	p := &store.Programme{
		Name:     "EastEnders",
		Episode:  "06/04/2026",
		Position: 7307,
		AirDate:  "2026-04-06",
	}
	title, tier := GenerateTitle(p, "1080p", nil)
	if tier != store.TierDate {
		t.Errorf("tier = %q, want %q", tier, store.TierDate)
	}
	expected := "EastEnders.2026.04.06.1080p.WEB-DL.AAC.H264-iParr"
	if title != expected {
		t.Errorf("title = %q\nwant  = %q", title, expected)
	}
}

func TestGenerateTitleSubtitleDateAlternateSeparators(t *testing.T) {
	cases := []string{
		"06/04/2026",
		"06-04-2026",
		"06.04.2026",
		"6/4/2026",
	}
	for _, sub := range cases {
		p := &store.Programme{
			Name:     "Casualty",
			Episode:  sub,
			Position: 1234,
			AirDate:  "2026-04-06",
		}
		title, tier := GenerateTitle(p, "720p", nil)
		if tier != store.TierDate {
			t.Errorf("subtitle=%q: tier = %q, want %q", sub, tier, store.TierDate)
		}
		expected := "Casualty.2026.04.06.720p.WEB-DL.AAC.H264-iParr"
		if title != expected {
			t.Errorf("subtitle=%q: title = %q\nwant  = %q", sub, title, expected)
		}
	}
}

func TestGenerateTitleNumberedShowNotPromoted(t *testing.T) {
	// Shows with proper S/E numbering (Doctor Who etc.) must continue to use
	// Tier 1 even if their air date is set. Auto-detection should only fire
	// when series/episode numbering is missing.
	p := &store.Programme{
		Name:       "Doctor Who",
		Episode:    "The Unquiet Dead",
		Series:     1,
		EpisodeNum: 3,
		AirDate:    "2005-04-09",
	}
	title, tier := GenerateTitle(p, "720p", nil)
	if tier != store.TierFull {
		t.Errorf("tier = %q, want %q", tier, store.TierFull)
	}
	expected := "Doctor.Who.S01E03.The.Unquiet.Dead.720p.WEB-DL.AAC.H264-iParr"
	if title != expected {
		t.Errorf("title = %q\nwant  = %q", title, expected)
	}
}

func TestSanitiseTitle(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"Hello World", "Hello.World"},
		{"It's a test!", "Its.a.test"},
		{"  spaces  ", "spaces"},
		{"BBC: News & More", "BBC.News.and.More"},
	}
	for _, tt := range tests {
		got := sanitiseForTitle(tt.in)
		if got != tt.want {
			t.Errorf("sanitise(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
