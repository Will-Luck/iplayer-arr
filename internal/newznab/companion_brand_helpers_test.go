package newznab

import (
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
)

func TestHasExactTitle(t *testing.T) {
	r := func(titles ...string) []bbc.IBLResult {
		var out []bbc.IBLResult
		for _, s := range titles {
			out = append(out, bbc.IBLResult{Title: s})
		}
		return out
	}
	cases := []struct {
		name    string
		hits    []string
		results []bbc.IBLResult
		want    string
		exact   bool
	}{
		{"parent beside companion", nil, r("The Celebrity Traitors: Uncloaked", "The Celebrity Traitors"), "The Celebrity Traitors", true},
		{"parent only in the hits", []string{"The Celebrity Traitors", "The Celebrity Traitors: Uncloaked"}, r("The Celebrity Traitors: Uncloaked"), "The Celebrity Traitors", true},
		// iBL's real hit order for "Gladiators (2024)" on 2026-10-24: the
		// parent is the second hit, so every hit must be checked, not just
		// the top one. The results hold only the companion, as when the
		// parent's listing fails.
		{"parent not the first hit", []string{"Election 2024", "Gladiators", "Gladiators: Epic Pranks"}, r("Gladiators: Epic Pranks"), "Gladiators (2024)", true},
		{"subtitle-only brand", []string{"Talking Tom Heroes: Suddenly Super"}, r("Talking Tom Heroes: Suddenly Super"), "Talking Tom Heroes", false},
		{"colon in the real title", nil, r("David Fuller: Monster in the Morgue"), "David Fuller: Monster In The Morgue", true},
		// iBL writes the range with an en dash, as here.
		{"year suffix on both sides", nil, r("Doctor Who: Unleashed", "Doctor Who (2005\u20132022)"), "Doctor Who (2005)", true},
		// Year coverage is not checked on purpose; see the exactOnly comment
		// in writeResultsRSS and TestGH58_OutOfEraExactBrandSuppressesExtension.
		{"exact brand from another era", []string{"Example Show (1992-2000)", "Example Show: After Hours"}, nil, "Example Show (2024)", true},
		{"country tag on the query", nil, r("The Apprentice: You're Fired!", "The Apprentice"), "The Apprentice (UK)", true},
		{"prefix without colon is not exact", []string{"The Traitors NZ"}, r("The Traitors NZ"), "The Traitors", false},
		{"nothing", nil, nil, "Anything", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasExactTitle(tc.want, tc.hits, tc.results); got != tc.exact {
				t.Errorf("hasExactTitle(%q) = %v, want %v", tc.want, got, tc.exact)
			}
		})
	}
}
