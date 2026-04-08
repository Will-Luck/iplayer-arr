package newznab

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// reYearSuffix matches a trailing year suffix like "(1963)" or
// "(1963-1996)" or "(1963\u20131996)" (en-dash). Used by bareName to
// strip suffixes for cross-name comparison. Only year-shaped suffixes
// match - non-year parenthesised suffixes like "(Special Edition)" are
// preserved.
var reYearSuffix = regexp.MustCompile(`\s*\((\d{4})(?:[-\x{2013}]\d{4})?\)\s*$`)

// reYearRange parses the (start, end) years from a programme title's
// year suffix. Returns nil if there is no suffix.
var reYearRange = regexp.MustCompile(`\((\d{4})(?:[-\x{2013}](\d{4}))?\)\s*$`)

// bareName strips a trailing year suffix from a programme name. Returns
// the input unchanged if there is no year suffix. Non-year suffixes
// like "(Special Edition)" are preserved.
//
//	bareName("Doctor Who")                 -> "Doctor Who"
//	bareName("Doctor Who (2005)")          -> "Doctor Who"
//	bareName("Doctor Who (1963-1996)")     -> "Doctor Who"
//	bareName("Doctor Who (1963\u20131996)") -> "Doctor Who"  (en-dash)
//	bareName("Newsround (Special Edition)") -> "Newsround (Special Edition)"
func bareName(s string) string {
	return reYearSuffix.ReplaceAllString(s, "")
}

// extractYearRange parses the year suffix from a programme title.
// Returns (start, end) where:
//   - (0, 0) means no suffix found
//   - (Y, Y) means single-year suffix "(YYYY)"
//   - (S, E) means range suffix "(YYYY-YYYY)" or "(YYYY\u2013YYYY)"
func extractYearRange(s string) (start, end int) {
	m := reYearRange.FindStringSubmatch(s)
	if m == nil {
		return 0, 0
	}
	start, _ = strconv.Atoi(m[1])
	if m[2] != "" {
		end, _ = strconv.Atoi(m[2])
	} else {
		end = start
	}
	return start, end
}

// nameMatchesWithYear is a per-candidate check: does this programme
// match the wanted name (after bare-name normalisation) AND does its
// year range cover the year hint?
//
// Returns true when:
//   - the bare names match (case-insensitive), AND
//   - either yearHint == 0 (no hint), OR the candidate has no year
//     suffix (kept for caller-side tiebreak in disambiguateByYear),
//     OR the candidate's year range covers yearHint
func nameMatchesWithYear(progName, wantName string, yearHint int) bool {
	if !strings.EqualFold(bareName(progName), bareName(wantName)) {
		return false
	}
	if yearHint == 0 {
		return true
	}
	start, end := extractYearRange(progName)
	if start == 0 && end == 0 {
		// Candidate has no year suffix. Keep for the caller-side
		// tiebreak in disambiguateByYear.
		return true
	}
	return yearHint >= start && yearHint <= end
}

// disambiguateByYear is a set-level tiebreak. When the candidate list
// contains both year-suffixed and bare-name matches, prefer the
// year-suffixed ones if any cover the year hint. When all candidates
// are bare-name (no year info), return them all unchanged. When
// yearHint == 0 (no Skyhook lookup happened), return the input
// unchanged.
//
// This is the second pass of the Phase 4 disambiguation logic. The
// first pass is nameMatchesWithYear in matchesSearchFilter.
func disambiguateByYear(progs []*store.Programme, yearHint int) []*store.Programme {
	if yearHint == 0 || len(progs) <= 1 {
		return progs
	}

	var suffixed, bare []*store.Programme
	for _, p := range progs {
		start, end := extractYearRange(p.Name)
		if start == 0 && end == 0 {
			bare = append(bare, p)
			continue
		}
		if yearHint >= start && yearHint <= end {
			suffixed = append(suffixed, p)
		}
	}

	if len(suffixed) > 0 {
		return suffixed
	}
	return bare
}
