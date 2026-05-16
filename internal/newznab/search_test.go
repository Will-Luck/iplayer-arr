package newznab

import (
	"net/http/httptest"
	"testing"
)

// TestParseLimitParam exercises audit item 24: the Newznab caps XML
// advertises max="100" but pre-v1.5.2 the server ignored Sonarr's
// limit=N query parameter, leaving Sonarr to truncate up to 100 items
// down to its requested 50 on the client side. parseLimitParam now
// clamps to the advertised max so a hostile or misconfigured client
// cannot request more.
func TestParseLimitParam(t *testing.T) {
	tests := []struct {
		name string
		qs   string
		want int
	}{
		{"no param", "", 0},
		{"empty value", "limit=", 0},
		{"valid 25", "limit=25", 25},
		{"valid 50", "limit=50", 50},
		{"valid 100 (cap)", "limit=100", 100},
		{"clamped to advertised max", "limit=500", 100},
		{"non-numeric", "limit=abc", 0},
		{"negative", "limit=-5", 0},
		{"zero", "limit=0", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/api?" + tt.qs
			req := httptest.NewRequest("GET", url, nil)
			if got := parseLimitParam(req); got != tt.want {
				t.Errorf("parseLimitParam(%q) = %d, want %d", tt.qs, got, tt.want)
			}
		})
	}
}
