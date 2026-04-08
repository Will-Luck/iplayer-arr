package newznab

import (
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// --- bareName ---

func TestBareName_NoSuffix(t *testing.T) {
	if got := bareName("Doctor Who"); got != "Doctor Who" {
		t.Errorf("bareName(\"Doctor Who\") = %q, want %q", got, "Doctor Who")
	}
}

func TestBareName_SingleYearSuffix(t *testing.T) {
	if got := bareName("Doctor Who (2005)"); got != "Doctor Who" {
		t.Errorf("bareName(\"Doctor Who (2005)\") = %q, want %q", got, "Doctor Who")
	}
}

func TestBareName_AsciiHyphenRange(t *testing.T) {
	if got := bareName("Doctor Who (1963-1996)"); got != "Doctor Who" {
		t.Errorf("bareName(\"Doctor Who (1963-1996)\") = %q, want %q", got, "Doctor Who")
	}
}

func TestBareName_EnDashRange(t *testing.T) {
	// BBC's actual brand titles use the en-dash character U+2013, not
	// ASCII hyphen. The regex must handle both.
	if got := bareName("Doctor Who (1963\u20131996)"); got != "Doctor Who" {
		t.Errorf("bareName(\"Doctor Who (1963\u20131996)\") = %q, want %q", got, "Doctor Who")
	}
}

func TestBareName_NonYearSuffixPreserved(t *testing.T) {
	// Suffixes that aren't year-shaped must be preserved.
	if got := bareName("Newsround (Special Edition)"); got != "Newsround (Special Edition)" {
		t.Errorf("bareName(\"Newsround (Special Edition)\") = %q, want it preserved", got)
	}
}

// --- extractYearRange ---

func TestExtractYearRange_NoSuffix(t *testing.T) {
	start, end := extractYearRange("Doctor Who")
	if start != 0 || end != 0 {
		t.Errorf("extractYearRange(\"Doctor Who\") = (%d, %d), want (0, 0)", start, end)
	}
}

func TestExtractYearRange_SingleYear(t *testing.T) {
	start, end := extractYearRange("Doctor Who (2005)")
	if start != 2005 || end != 2005 {
		t.Errorf("extractYearRange(\"Doctor Who (2005)\") = (%d, %d), want (2005, 2005)", start, end)
	}
}

func TestExtractYearRange_AsciiHyphenRange(t *testing.T) {
	start, end := extractYearRange("Doctor Who (1963-1996)")
	if start != 1963 || end != 1996 {
		t.Errorf("extractYearRange(\"Doctor Who (1963-1996)\") = (%d, %d), want (1963, 1996)", start, end)
	}
}

func TestExtractYearRange_EnDashRange(t *testing.T) {
	start, end := extractYearRange("Doctor Who (1963\u20131996)")
	if start != 1963 || end != 1996 {
		t.Errorf("extractYearRange(\"Doctor Who (1963\u20131996)\") = (%d, %d), want (1963, 1996)", start, end)
	}
}

// --- nameMatchesWithYear ---

func TestNameMatchesWithYear_ClassicYearMatchesClassicBrand(t *testing.T) {
	if !nameMatchesWithYear("Doctor Who (1963-1996)", "Doctor Who", 1963) {
		t.Errorf("expected 1963 to match Doctor Who (1963-1996)")
	}
}

func TestNameMatchesWithYear_ClassicYearRejectsModernBrand(t *testing.T) {
	if nameMatchesWithYear("Doctor Who (2005-2022)", "Doctor Who", 1963) {
		t.Errorf("expected 1963 to NOT match Doctor Who (2005-2022)")
	}
}

func TestNameMatchesWithYear_BareNameKeptForCallerSideTiebreak(t *testing.T) {
	// Bare-name candidate (no year suffix) is kept by the per-candidate
	// check. The caller (disambiguateByYear) decides whether to drop it
	// based on whether any year-suffixed candidates exist.
	if !nameMatchesWithYear("Doctor Who", "Doctor Who", 1963) {
		t.Errorf("expected bare name to be kept for caller-side tiebreak")
	}
}

func TestNameMatchesWithYear_NoYearHintMatchesAll(t *testing.T) {
	// When yearHint is 0 (no Skyhook lookup), the function falls back
	// to bare-name equality only.
	if !nameMatchesWithYear("Doctor Who (1963-1996)", "Doctor Who", 0) {
		t.Errorf("expected bare-name match with no year hint")
	}
}

// --- disambiguateByYear ---

func TestDisambiguateByYear_SingleCandidateUnchanged(t *testing.T) {
	progs := []*store.Programme{
		{Name: "Doctor Who (1963-1996)"},
	}
	got := disambiguateByYear(progs, 1963)
	if len(got) != 1 || got[0].Name != "Doctor Who (1963-1996)" {
		t.Errorf("expected single candidate unchanged, got %v", got)
	}
}

func TestDisambiguateByYear_PreferYearSuffixedWhenHintMatches(t *testing.T) {
	progs := []*store.Programme{
		{Name: "Doctor Who"},
		{Name: "Doctor Who (1963-1996)"},
		{Name: "Doctor Who (2005-2022)"},
	}
	got := disambiguateByYear(progs, 1963)
	if len(got) != 1 || got[0].Name != "Doctor Who (1963-1996)" {
		t.Errorf("expected only Doctor Who (1963-1996) to remain, got %v", got)
	}
}

func TestDisambiguateByYear_FallBackToBareWhenNoSuffixedMatches(t *testing.T) {
	// When no year-suffixed candidate covers the year hint, fall back
	// to bare-name candidates.
	progs := []*store.Programme{
		{Name: "Doctor Who"},
		{Name: "Doctor Who (1963-1996)"},
	}
	got := disambiguateByYear(progs, 2030)
	if len(got) != 1 || got[0].Name != "Doctor Who" {
		t.Errorf("expected bare Doctor Who to remain (no suffixed candidate covers 2030), got %v", got)
	}
}

func TestDisambiguateByYear_NoYearHintReturnsUnchanged(t *testing.T) {
	progs := []*store.Programme{
		{Name: "Doctor Who"},
		{Name: "Doctor Who (1963-1996)"},
	}
	got := disambiguateByYear(progs, 0)
	if len(got) != 2 {
		t.Errorf("expected unchanged length 2 when yearHint=0, got %d", len(got))
	}
}
