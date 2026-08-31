package auth

import "testing"

func TestSecretsEqual(t *testing.T) {
	cases := []struct {
		name   string
		given  string
		stored string
		want   bool
	}{
		{"exact match", "abc123", "abc123", true},
		{"different value same length", "abc123", "abc124", false},
		{"prefix only", "abc", "abc123", false},
		{"longer guess", "abc1234", "abc123", false},
		{"empty stored rejects empty given", "", "", false},
		{"empty stored rejects any given", "abc123", "", false},
		{"empty given against real secret", "", "abc123", false},
		{"case sensitive", "ABC123", "abc123", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SecretsEqual(tc.given, tc.stored); got != tc.want {
				t.Errorf("SecretsEqual(%q, %q) = %v, want %v", tc.given, tc.stored, got, tc.want)
			}
		})
	}
}
