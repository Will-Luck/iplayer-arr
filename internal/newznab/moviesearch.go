package newznab

import (
	"fmt"
	"html"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
)

// movieSeparators maps the separator punctuation TMDB titles carry
// (hyphen, colon, en dash) to spaces so word-sequence comparison and
// release-title emission both see plain words. bareName cannot do this:
// it strips only year/country suffixes, not separator punctuation.
var movieSeparators = strings.NewReplacer("-", " ", ":", " ", "–", " ")

// hasMovieCategory reports whether the Newznab cat parameter names any
// Movies category (2000-2999). Radarr's text-fallback movie search
// arrives as t=search with movie cats rather than t=movie, so the
// general search endpoint uses this to route movie requests.
func hasMovieCategory(catParam string) bool {
	for _, c := range strings.Split(catParam, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(c))
		if err == nil && n >= 2000 && n <= 2999 {
			return true
		}
	}
	return false
}

// normaliseMovieName reduces a movie title or BBC name to a canonical
// comparison key: year/country suffixes stripped, separator punctuation
// spaced, whitespace collapsed, lower-cased.
func normaliseMovieName(s string) string {
	s = bareName(s)
	s = movieSeparators.Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ToLower(s)
	// Radarr strips the leading article from text-fallback queries
	// ("The Dark Side..." is searched as "Dark Side..."), so both
	// sides of the comparison shed one leading article. Mid-string
	// articles are meaningful and stay.
	for _, art := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(s, art) && len(s) > len(art) {
			s = s[len(art):]
			break
		}
	}
	return s
}

// movieMatchedCandidate reports whether a Radarr movie query names this
// BBC result and, when it does, returns the BBC-side candidate string
// that matched. TMDB catalogues one-off programmes inconsistently (bare
// title, "Brand: Subtitle", "Subtitle - Brand"), so the query is
// compared against all four brand/subtitle combinations. Exact
// normalised equality only: a loose match would mislabel wrong content.
//
// The candidate is used as the release title base: Radarr strips the
// leading article from the query it sends but its release-to-movie
// matcher keeps article words, so the title must come from BBC metadata
// (which carries the article) rather than echoing the query.
func movieMatchedCandidate(res bbc.IBLResult, query string) (string, bool) {
	q := normaliseMovieName(query)
	if q == "" {
		return "", false
	}
	for _, candidate := range []string{
		res.Title,
		res.Subtitle,
		res.Title + " " + res.Subtitle,
		res.Subtitle + " " + res.Title,
	} {
		if normaliseMovieName(candidate) == q {
			return strings.Join(strings.Fields(candidate), " "), true
		}
	}
	return "", false
}

func movieNameMatches(res bbc.IBLResult, query string) bool {
	_, ok := movieMatchedCandidate(res, query)
	return ok
}

// reTrailingQueryYear matches a standalone 4-digit year (optionally
// bracketed) at the end of a movie query, e.g. "Title 2026" or
// "Title (2026)". Radarr appends the year to text-fallback searches
// against q-only indexers.
var reTrailingQueryYear = regexp.MustCompile(`[\s.(]+\(?((?:19|20)\d{2})\)?\s*$`)

// extractMovieYear resolves the requested movie year: explicit `year`
// query parameter first, else a trailing year token in q (which is
// stripped from the returned query either way so BBC search and name
// matching see the bare title). A query that IS only a year (e.g. the
// film "1917") is kept whole.
func extractMovieYear(yearParam, q string) (string, int) {
	year := 0
	if n, err := strconv.Atoi(strings.TrimSpace(yearParam)); err == nil && n >= 1900 && n <= 2099 {
		year = n
	}
	if m := reTrailingQueryYear.FindStringSubmatch(q); m != nil {
		stripped := strings.TrimSpace(strings.TrimSuffix(q, m[0]))
		if stripped != "" {
			if year == 0 {
				if n, err := strconv.Atoi(m[1]); err == nil {
					year = n
				}
			}
			q = stripped
		}
	}
	return q, year
}

// airDateYear extracts the year from a normalised IBL air date.
// AirDate is not guaranteed YYYY-MM-DD (normaliseAirDate passes
// unparseable input through), so this is parse-based and fails open
// to 0 = unknown.
func airDateYear(airDate string) int {
	if t, err := time.Parse("2006-01-02", airDate); err == nil {
		return t.Year()
	}
	// BBC film rail entries often carry release_date as a bare year
	// (e.g. "2021"), which normaliseAirDate passes through unchanged.
	if t, err := time.Parse("2006", airDate); err == nil {
		y := t.Year()
		if y >= 1900 && y <= 2099 {
			return y
		}
	}
	return 0
}

func absInt(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

// movieBrowseLimit caps the films rail served for a q-less t=movie
// poll: enough items to satisfy Radarr's indexer test without a large
// browse feed.
const movieBrowseLimit = 10

// handleMovieSearch serves Newznab t=movie for Radarr: a query runs a
// search; a q-less poll (Radarr's indexer test and RSS sync) serves
// the films rail, because Radarr rejects an empty feed outright.
func (h *Handler) handleMovieSearch(w http.ResponseWriter, r *http.Request) {
	rawQ := strings.TrimSpace(r.URL.Query().Get("q"))
	log.Printf("[moviesearch] q=%q year=%q", rawQ, r.URL.Query().Get("year"))
	if h.ibl == nil {
		writeEmptyRSS(w)
		return
	}
	if rawQ == "" {
		// Radarr's indexer test and RSS sync poll t=movie with no q
		// and reject an empty feed, so serve the films rail.
		results, err := h.ibl.FilmsCtx(r.Context(), movieBrowseLimit)
		if err != nil {
			writeEmptyRSS(w)
			return
		}
		h.writeMovieRSS(w, r, results, "", 0, parseLimitParam(r))
		return
	}

	cleanQ, year := extractMovieYear(r.URL.Query().Get("year"), rawQ)
	results, err := h.ibl.SearchCtx(r.Context(), cleanQ, 1)
	if err != nil {
		writeEmptyRSS(w)
		return
	}
	h.writeMovieRSS(w, r, results, cleanQ, year, parseLimitParam(r))
}

// writeMovieRSS renders movie search results. Titles come from the
// matched BBC candidate string, not the Radarr query: Radarr strips the
// leading article from the query it sends but keeps article words when
// mapping a release title back to a movie, so an echoed title can never
// carry the article and Radarr rejects it as "Unknown Movie". The BBC
// candidate carries the article exactly and normalises identically to
// the query, so the movieMatchedCandidate filter keeps the wrong-content
// guarantee. Movies never consult the first-seen bucket: PIDs are
// BBC-wide and a TV-browse stamp would backdate a movie item.
func (h *Handler) writeMovieRSS(w http.ResponseWriter, r *http.Request, results []bbc.IBLResult, query string, wantYear, limit int) {
	// Radarr fetches grab URLs straight from the feed, so every guid/
	// link/enclosure carries the key inline (same as writeResultsRSS).
	apiKeyParam := ""
	if h.store != nil {
		if apiKey, _ := h.store.GetConfig("api_key"); apiKey != "" {
			apiKeyParam = "&amp;apikey=" + apiKey
		}
	}

	// keptMovie pairs a kept result with the BBC candidate string the
	// query matched, so the emit loop can title the release from the
	// candidate (which carries the leading article) rather than the
	// article-stripped query. In browse mode candidate is empty.
	type keptMovie struct {
		res       bbc.IBLResult
		candidate string
	}
	var kept []keptMovie
	var probeItems []bbc.ProbeItem
	seen := make(map[string]struct{}, len(results))
	for _, res := range results {
		if _, dup := seen[res.PID]; dup {
			continue
		}
		var candidate string
		if query != "" {
			var ok bool
			if candidate, ok = movieMatchedCandidate(res, query); !ok {
				continue
			}
		}
		if wantYear > 0 {
			if y := airDateYear(res.AirDate); y > 0 && absInt(y-wantYear) > 1 {
				continue
			}
		}
		seen[res.PID] = struct{}{}
		kept = append(kept, keptMovie{res: res, candidate: candidate})
		probeItems = append(probeItems, bbc.ProbeItem{PID: res.PID, ShowName: res.Title})
	}

	var probe bbc.PrefetchResult
	if h.prober != nil && len(probeItems) > 0 {
		probe = h.prober.PrefetchPIDs(r.Context(), probeItems)
	}

	var items []string
	itemCount := 0
	for _, km := range kept {
		res := km.res
		if limit > 0 && itemCount >= limit {
			break
		}
		if probe.NotYetAvailable[res.PID] {
			// Streams not published yet: advertising now gets the release
			// grabbed and blocklisted. Same guard as the TV path, issue #44.
			continue
		}

		ceiling := h.qualityCeilingHeight()
		var qualities []string
		if probe.Heights[res.PID] != nil {
			qualities = heightsToTags(capHeights(probe.Heights[res.PID], ceiling))
		} else {
			// Safe fallback: only what BBC universally delivers.
			qualities = capQualityTags([]string{"720p", "540p"}, ceiling)
		}
		if limit > 0 && itemCount+len(qualities) > limit {
			qualities = qualities[:limit-itemCount]
		}
		if len(qualities) == 0 {
			continue
		}
		itemCount += len(qualities)

		year := wantYear
		if year == 0 {
			year = airDateYear(res.AirDate)
		}
		// Search mode titles from the matched BBC candidate (carries the
		// article); browse mode has no query and titles from the BBC title.
		base := km.candidate
		if base == "" {
			base = res.Title
		}
		base = sanitiseForTitle(movieSeparators.Replace(bareName(base)))
		name := base
		if year > 0 {
			name = fmt.Sprintf("%s.%d", base, year)
		}

		// pubDate precedence: availability.start -> broadcast date -> now.
		pubDate := time.Now().Format(time.RFC1123Z)
		if !res.Available.IsZero() {
			pubDate = res.Available.Format(time.RFC1123Z)
		} else if res.AirDate != "" {
			if t, err := time.Parse("2006-01-02", res.AirDate); err == nil {
				pubDate = t.Format(time.RFC1123Z)
			}
		}

		for _, qual := range qualities {
			title := fmt.Sprintf("%s.%s.WEB-DL.AAC.H264-iParr", name, qual)
			guid := EncodeGUID(res.PID, qual, "original")

			cat := "2040" // Movies/HD
			switch qual {
			case "2160p":
				cat = "2045" // Movies/UHD
			case "540p", "396p":
				cat = "2030" // Movies/SD
			}

			size := estimateSize(res.Duration, qual)

			items = append(items, fmt.Sprintf(`    <item>
      <title>%s</title>
      <guid isPermaLink="true">%s/newznab/api?t=get&amp;id=%s%s</guid>
      <link>%s/newznab/api?t=get&amp;id=%s%s</link>
      <pubDate>%s</pubDate>
      <enclosure url="%s/newznab/api?t=get&amp;id=%s%s" length="%d" type="application/x-nzb" />
      <newznab:attr name="category" value="%s" />
      <newznab:attr name="size" value="%d" />
      <newznab:attr name="language" value="en" />
    </item>`,
				html.EscapeString(title), baseURL(r), guid, apiKeyParam, baseURL(r), guid, apiKeyParam, pubDate,
				baseURL(r), guid, apiKeyParam, size, cat, size))
		}
	}

	w.Header().Set("Content-Type", "application/xml")
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/">
  <channel>
    <title>iplayer-arr</title>
%s
  </channel>
</rss>`, strings.Join(items, "\n"))
}
