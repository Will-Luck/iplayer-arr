package bbc

import (
	"strings"
	"testing"
)

func TestRandomUA(t *testing.T) {
	ua := RandomUserAgent()
	if ua == "" {
		t.Fatal("empty user agent")
	}

	// should contain browser identifier
	found := false
	for _, sig := range []string{"Chrome", "Firefox", "Safari", "Edge"} {
		if strings.Contains(ua, sig) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("UA %q doesn't look like a browser UA", ua)
	}

	// calling twice should eventually return different UAs (probabilistic)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		seen[RandomUserAgent()] = true
	}
	if len(seen) < 2 {
		t.Error("expected at least 2 different UAs from 50 calls")
	}
}

func TestValidPID(t *testing.T) {
	cases := []struct {
		pid  string
		want bool
	}{
		{"b006qpgr", true},
		{"m001pb7h", true},
		{"b006qpgr1", true},
		{"B006QPGR", false},
		{"b006qpg", false},
		{"b006qp!r", false},
		{"", false},
		{"abcdefgh", false}, // 'a' and 'e' are vowels
	}
	for _, tc := range cases {
		if got := ValidPID(tc.pid); got != tc.want {
			t.Errorf("ValidPID(%q) = %v, want %v", tc.pid, got, tc.want)
		}
	}
}
