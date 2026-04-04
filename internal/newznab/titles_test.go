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
