package bbc

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestResolveVPID(t *testing.T) {
	fixture, err := os.ReadFile("testdata/playlist.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture)
	}))
	defer srv.Close()

	resolver := NewPlaylistResolver(NewClient())
	resolver.BaseURL = srv.URL

	info, err := resolver.Resolve("b039d07m")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if info.VPID != "b039d080" {
		t.Errorf("VPID = %q, want b039d080", info.VPID)
	}
	if info.Duration != 2700 {
		t.Errorf("Duration = %d, want 2700", info.Duration)
	}
	if len(info.Versions) != 2 {
		t.Fatalf("Versions len = %d, want 2", len(info.Versions))
	}
	if info.Versions[0].Type != "original" {
		t.Errorf("Versions[0].Type = %q, want original", info.Versions[0].Type)
	}
	if info.Versions[1].Type != "audiodescribed" {
		t.Errorf("Versions[1].Type = %q, want audiodescribed", info.Versions[1].Type)
	}
}

func TestNormaliseVersionType(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Original", "original"},
		{"Audio Described", "audiodescribed"},
		{"Audio Description", "audiodescribed"},
		{"Signed", "signed"},
		{"Signed and Audio Described", "combined"},
		{"Open Subtitles", "opensubtitles"},
		{"Dubbbed", "dubbbed"},
		{"", "unknown"},
		{"  ", "unknown"},
	}
	for _, tc := range cases {
		if got := normaliseVersionType(tc.in); got != tc.want {
			t.Errorf("normaliseVersionType(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
