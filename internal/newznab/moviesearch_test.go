package newznab

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
)

// panoramaSearchPayload mirrors the live IBL new-search response for
// "The Dark Side of Married at First Sight" (verified 2026-07-22):
// the target Panorama episode plus an unrelated Newsnight episode that
// the name filter must reject. Field shapes (versions/duration/
// availability/master_brand) match iblElementJSON exactly; release_date
// uses BBC's "D Mon YYYY" form, as eastendersOneEpisodePayload does.
const panoramaSearchPayload = `{
  "new_search": {
    "results": [
      {
        "id": "m002wcf0",
        "type": "episode",
        "title": "Panorama",
        "subtitle": "The Dark Side of Married at First Sight",
        "release_date": "18 May 2026",
        "master_brand": {"titles": {"small": "BBC One"}},
        "versions": [{"download": true, "duration": {"value": "PT58M"}, "availability": {"start": "2026-05-18T20:00:00Z"}}]
      },
      {
        "id": "m002wgy8",
        "type": "episode",
        "title": "Newsnight",
        "subtitle": "An accident waiting to happen",
        "release_date": "19 May 2026",
        "master_brand": {"titles": {"small": "BBC Two"}},
        "versions": [{"download": true, "duration": {"value": "PT30M"}, "availability": {"start": "2026-05-19T22:00:00Z"}}]
      }
    ]
  }
}`

// filmsBrowsePayload mirrors the live IBL films-category response
// (verified 2026-07-22): a programme_large wrapper whose
// initial_children carry the episode payload. Radarr's q-less
// t=movie poll serves this rail. release_date is a bare year, as the
// real films rail returns.
const filmsBrowsePayload = `{"category_programmes": {"elements": [
  {"id": "m00lastr1", "type": "programme_large", "title": "The Last Rite",
   "master_brand": {"titles": {"small": "BBC Two"}},
   "images": {"standard": "https://.../{recipe}/p0djqsjv.jpg"},
   "initial_children": [
     {"id": "m00lastr1", "type": "episode", "title": "The Last Rite",
      "release_date": "2021",
      "versions": [{"download": true,
                    "duration": {"value": "PT12M20.560S"},
                    "availability": {"start": "2022-12-05T00:21:36Z"}}]}
   ]}
]}}`

func TestNormaliseMovieName(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain", "Panorama", "panorama"},
		// Leading "The" is stripped as an article (Radarr sheds it too).
		{"hyphen separator", "The Dark Side of Married at First Sight - Panorama", "dark side of married at first sight panorama"},
		{"colon separator", "Panorama: The Dark Side", "panorama the dark side"},
		{"en dash separator", "Storyville – The Contestant", "storyville the contestant"},
		{"year suffix stripped", "Doctor Who (2005)", "doctor who"},
		// Leading "The" is stripped as an article.
		{"country tag stripped", "The Apprentice (UK)", "apprentice"},
		// Leading "A" is the indefinite article and is stripped.
		{"whitespace collapsed", "  A   B  ", "b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normaliseMovieName(tc.in); got != tc.want {
				t.Errorf("normaliseMovieName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestMovieNameMatches(t *testing.T) {
	panorama := bbc.IBLResult{Title: "Panorama", Subtitle: "The Dark Side of Married at First Sight"}
	newsnight := bbc.IBLResult{Title: "Newsnight", Subtitle: "An accident waiting to happen"}
	oneOff := bbc.IBLResult{Title: "The Contestant", Subtitle: ""}

	cases := []struct {
		name  string
		res   bbc.IBLResult
		query string
		want  bool
	}{
		// Acceptance case: TMDB naming "Subtitle - Brand".
		{"subtitle dash brand", panorama, "The Dark Side of Married at First Sight - Panorama", true},
		// Subtitle alone.
		{"subtitle only", panorama, "The Dark Side of Married at First Sight", true},
		// Brand + subtitle order.
		{"brand then subtitle", panorama, "Panorama The Dark Side of Married at First Sight", true},
		// Brand-only queries match the Title combination by design: a
		// movie named exactly "Panorama" would be ambiguous here, but
		// the year gate narrows that ambiguity.
		{"brand only", panorama, "Panorama", true},
		// One-off whose BBC title IS the movie title.
		{"one-off title", oneOff, "The Contestant", true},
		// Wrong content must be rejected.
		{"unrelated episode", newsnight, "The Dark Side of Married at First Sight - Panorama", false},
		{"empty query", panorama, "", false},
		// Substring is not a match: filter is exact after normalisation.
		{"partial words", panorama, "Married at First Sight", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := movieNameMatches(tc.res, tc.query); got != tc.want {
				t.Errorf("movieNameMatches(%v/%v, %q) = %v, want %v",
					tc.res.Title, tc.res.Subtitle, tc.query, got, tc.want)
			}
		})
	}
}

func TestMovieMatchedCandidate(t *testing.T) {
	panorama := bbc.IBLResult{Title: "Panorama", Subtitle: "The Dark Side of Married at First Sight"}

	got, ok := movieMatchedCandidate(panorama, "Dark Side of Married at First Sight Panorama")
	if !ok || got != "The Dark Side of Married at First Sight Panorama" {
		t.Errorf("movieMatchedCandidate = (%q, %v), want subtitle+title candidate", got, ok)
	}

	if _, ok := movieMatchedCandidate(panorama, "Newsnight"); ok {
		t.Error("unrelated query must not match")
	}
}

// Radarr's text-fallback movie search arrives on the GENERAL search
// endpoint with movie categories in cat= and the year embedded in q
// (t=movie is only used by its indexer test). Debug-log verified
// against real Radarr.
func TestGeneralSearchWithMovieCategoriesRoutesToMoviePath(t *testing.T) {
	h, st := newHandlerWithBBCAndStore(t, panoramaSearchPayload)
	if err := st.SetConfig("api_key", "movie-key"); err != nil {
		t.Fatalf("seed api_key: %v", err)
	}

	// Exact shape Radarr sent, minus URL encoding: leading article
	// stripped, year in q, movie cats.
	req := httptest.NewRequest("GET",
		"/newznab/api?t=search&cat=2000,2030,2040,2045&extended=1&offset=0&limit=100&q=Dark+Side+of+Married+at+First+Sight+Panorama+2026&apikey=movie-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "The.Dark.Side.of.Married.at.First.Sight.Panorama.2026.") {
		t.Errorf("movie title must come from the matched BBC candidate (with article), got: %s", body)
	}
	if strings.Contains(body, "<title>Dark.Side") {
		t.Errorf("stripped-article query echo leaked into title: %s", body)
	}
	if !strings.Contains(body, `name="category" value="20`) {
		t.Errorf("movie-cat t=search missing Movies category attrs: %s", body)
	}
	if strings.Contains(body, "Newsnight") {
		t.Errorf("wrong-content guard lost on t=search movie route: %s", body)
	}
}

func TestGeneralSearchWithoutMovieCategoriesKeepsTVBehaviour(t *testing.T) {
	h, st := newHandlerWithBBCAndStore(t, panoramaSearchPayload)
	if err := st.SetConfig("api_key", "movie-key"); err != nil {
		t.Fatalf("seed api_key: %v", err)
	}

	for _, urlStr := range []string{
		"/newznab/api?t=search&q=Panorama&apikey=movie-key",
		"/newznab/api?t=search&cat=5030,5040&q=Panorama&apikey=movie-key",
	} {
		req := httptest.NewRequest("GET", urlStr, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		body := w.Body.String()
		if strings.Contains(body, `name="category" value="20`) {
			t.Errorf("%s: TV search emitted Movies categories: %s", urlStr, body)
		}
	}
}

func TestNormaliseMovieNameStripsLeadingArticle(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"leading the", "The Dark Side of Married at First Sight", "dark side of married at first sight"},
		{"leading a", "A Very English Scandal", "very english scandal"},
		{"leading an", "An Inspector Calls", "inspector calls"},
		{"mid-string the kept", "Panorama The Dark Side", "panorama the dark side"},
		{"article-only stays", "The", "the"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normaliseMovieName(tc.in); got != tc.want {
				t.Errorf("normaliseMovieName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestExtractMovieYear(t *testing.T) {
	cases := []struct {
		name, yearParam, q string
		wantQ              string
		wantYear           int
	}{
		{"year param wins", "2026", "The Dark Side of Married at First Sight", "The Dark Side of Married at First Sight", 2026},
		{"trailing bare year", "", "The Dark Side of Married at First Sight 2026", "The Dark Side of Married at First Sight", 2026},
		{"trailing bracketed year", "", "The Dark Side of Married at First Sight (2026)", "The Dark Side of Married at First Sight", 2026},
		{"param beats trailing", "2025", "Some Film 2026", "Some Film", 2025},
		{"no year anywhere", "", "Panorama", "Panorama", 0},
		{"invalid param ignored", "banana", "Panorama", "Panorama", 0},
		{"year-only query kept whole", "", "1917", "1917", 0},
		{"out of range param ignored", "1850", "Panorama", "Panorama", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotQ, gotYear := extractMovieYear(tc.yearParam, tc.q)
			if gotQ != tc.wantQ || gotYear != tc.wantYear {
				t.Errorf("extractMovieYear(%q, %q) = (%q, %d), want (%q, %d)",
					tc.yearParam, tc.q, gotQ, gotYear, tc.wantQ, tc.wantYear)
			}
		})
	}
}

func TestAirDateYear(t *testing.T) {
	cases := []struct {
		name, in string
		want     int
	}{
		{"iso date", "2026-05-18", 2026},
		{"empty", "", 0},
		// airDateYear parses strict ISO (2006-01-02) only; any other
		// format, including BBC's loose "D Mon YYYY" form, fails open
		// to 0 rather than slicing a substring.
		{"unnormalised passthrough", "18 May 2026", 0},
		{"garbage", "not-a-date", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := airDateYear(tc.in); got != tc.want {
				t.Errorf("airDateYear(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestCapsAdvertisesMovieSearch(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil)
	req := httptest.NewRequest("GET", "/newznab/api?t=caps", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	body := w.Body.String()
	for _, want := range []string{
		`<movie-search available="yes" supportedParams="q,year" />`,
		`<category id="2000" name="Movies">`,
		`<subcat id="2045" name="Movies/UHD" />`,
		`<subcat id="2040" name="Movies/HD" />`,
		`<subcat id="2030" name="Movies/SD" />`,
		// TV block must be untouched.
		`<category id="5000" name="TV">`,
		`<tv-search available="yes" supportedParams="q,season,ep,tvdbid" />`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("caps missing %s\nbody: %s", want, body)
		}
	}
	if strings.Contains(body, `<movie-search available="no"`) {
		t.Error("caps still advertises movie-search no")
	}
}

// TestMovieSearchAuthRequired pins t=movie into the same auth contract
// as every other content operation (v1.5.5 regression class).
func TestMovieSearchAuthRequired(t *testing.T) {
	h, st := newHandlerWithBBCAndStore(t, panoramaSearchPayload)
	if err := st.SetConfig("api_key", "test-key-123"); err != nil {
		t.Fatalf("seed api_key: %v", err)
	}

	req := httptest.NewRequest("GET", "/newznab/api?t=movie&q=Panorama", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("t=movie without key: code = %d, want 401", w.Code)
	}

	req = httptest.NewRequest("GET", "/newznab/api?t=movie&q=Panorama&apikey=test-key-123", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("t=movie with key: code = %d, want 200", w.Code)
	}
}

// Radarr's save-time indexer test polls t=movie with no q and rejects
// an empty feed outright, so the q-less poll serves the films rail.
func TestMovieSearchEmptyQueryReturnsFilmsBrowse(t *testing.T) {
	h, st := newHandlerWithBBCAndStore(t, filmsBrowsePayload)
	if err := st.SetConfig("api_key", "movie-key"); err != nil {
		t.Fatalf("seed api_key: %v", err)
	}

	req := httptest.NewRequest("GET", "/newznab/api?t=movie&apikey=movie-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "<item>") {
		t.Fatalf("browse feed empty, Radarr's indexer test would reject: %s", body)
	}
	// BBC-metadata title with bare-year release date resolved to a year.
	if !strings.Contains(body, "The.Last.Rite.2021.") {
		t.Errorf("browse title missing BBC-metadata name+year: %s", body)
	}
	if !strings.Contains(body, `name="category" value="20`) {
		t.Errorf("browse items missing Movies category: %s", body)
	}
}

func TestAirDateYearBareYear(t *testing.T) {
	if got := airDateYear("2021"); got != 2021 {
		t.Errorf("airDateYear(\"2021\") = %d, want 2021 (bare-year release_date)", got)
	}
	if got := airDateYear("1899"); got != 0 {
		t.Errorf("airDateYear(\"1899\") = %d, want 0 (implausible year rejected)", got)
	}
}

func TestMovieSearchEmitsMatchingResult(t *testing.T) {
	h, st := newHandlerWithBBCAndStore(t, panoramaSearchPayload)
	if err := st.SetConfig("api_key", "movie-key"); err != nil {
		t.Fatalf("seed api_key: %v", err)
	}

	req := httptest.NewRequest("GET",
		"/newznab/api?t=movie&q=The+Dark+Side+of+Married+at+First+Sight+-+Panorama&year=2026&apikey=movie-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	body := w.Body.String()
	// Query-echo title with year, separator stripped (never ".-.").
	if !strings.Contains(body, "The.Dark.Side.of.Married.at.First.Sight.Panorama.2026.") {
		t.Errorf("missing echoed movie title, body: %s", body)
	}
	if strings.Contains(body, ".-.") {
		t.Errorf("raw separator leaked into title: %s", body)
	}
	// Wrong-content guard: the Newsnight episode must not emit.
	if strings.Contains(body, "Newsnight") || strings.Contains(body, "m002wgy8") {
		t.Errorf("unrelated result leaked into movie feed: %s", body)
	}
	// Movies categories, not TV.
	if !strings.Contains(body, `name="category" value="20`) {
		t.Errorf("missing Movies category attr: %s", body)
	}
	if strings.Contains(body, `name="category" value="50`) {
		t.Errorf("TV category leaked into movie feed: %s", body)
	}
	// Grab URLs carry the apikey inline (v1.5.5 regression class).
	if !strings.Contains(body, "apikey=movie-key") {
		t.Errorf("feed URLs missing apikey: %s", body)
	}
	// Target PID present.
	if !strings.Contains(body, "t=get") {
		t.Errorf("no grab links emitted: %s", body)
	}
}

func TestMovieSearchYearGate(t *testing.T) {
	h, st := newHandlerWithBBCAndStore(t, panoramaSearchPayload)
	if err := st.SetConfig("api_key", "movie-key"); err != nil {
		t.Fatalf("seed api_key: %v", err)
	}

	// BBC air date year is 2026. year=2023 is >1 away: no items.
	req := httptest.NewRequest("GET",
		"/newznab/api?t=movie&q=The+Dark+Side+of+Married+at+First+Sight&year=2023&apikey=movie-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if strings.Contains(w.Body.String(), "<item>") {
		t.Errorf("year gate failed: 2023 request matched 2026 airing: %s", w.Body.String())
	}

	// year=2025 is within the +/-1 tolerance: items emit.
	req = httptest.NewRequest("GET",
		"/newznab/api?t=movie&q=The+Dark+Side+of+Married+at+First+Sight&year=2025&apikey=movie-key", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), "<item>") {
		t.Errorf("year tolerance failed: 2025 request rejected 2026 airing: %s", w.Body.String())
	}
}

// panoramaUnknownAirDatePayload is panoramaSearchPayload's target
// Panorama result with a release_date the loose BBC-date parser
// cannot read (no day token), so it reaches airDateYear unchanged
// and resolves to year 0.
const panoramaUnknownAirDatePayload = `{
  "new_search": {
    "results": [
      {
        "id": "m002wcf0",
        "type": "episode",
        "title": "Panorama",
        "subtitle": "The Dark Side of Married at First Sight",
        "release_date": "TBC",
        "master_brand": {"titles": {"small": "BBC One"}},
        "versions": [{"download": true, "duration": {"value": "PT58M"}, "availability": {"start": "2026-05-18T20:00:00Z"}}]
      }
    ]
  }
}`

// TestMovieYearGateFailsOpen: when the requested year is known but the
// BBC air date is unparseable, the year gate must not exclude the
// result -- an unknown air date is not evidence of a year mismatch.
func TestMovieYearGateFailsOpen(t *testing.T) {
	h, st := newHandlerWithBBCAndStore(t, panoramaUnknownAirDatePayload)
	if err := st.SetConfig("api_key", "movie-key"); err != nil {
		t.Fatalf("seed api_key: %v", err)
	}

	req := httptest.NewRequest("GET",
		"/newznab/api?t=movie&q=The+Dark+Side+of+Married+at+First+Sight&year=2026&apikey=movie-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), "<item>") {
		t.Errorf("year gate excluded a result with an unparseable air date: %s", w.Body.String())
	}
}

func TestMovieSearchSkipsNotYetAvailable(t *testing.T) {
	prober := &mockProber{notYetAvailable: map[string]bool{"m002wcf0": true}}
	h, st := newHandlerWithBBCProberAndStore(t, panoramaSearchPayload, prober)
	if err := st.SetConfig("api_key", "movie-key"); err != nil {
		t.Fatalf("seed api_key: %v", err)
	}

	req := httptest.NewRequest("GET",
		"/newznab/api?t=movie&q=The+Dark+Side+of+Married+at+First+Sight&apikey=movie-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	// The only payload result matching this query is the Panorama episode
	// m002wcf0, which the prober marks not-yet-available, so the feed must
	// carry zero items: no <item> element and no echoed title (issue #44
	// class). Asserting on the raw PID is vacuous -- GUIDs are base64
	// (EncodeGUID), so the PID literal never appears in the body regardless
	// of whether the guard runs.
	body := w.Body.String()
	if strings.Contains(body, "<item>") {
		t.Errorf("not-yet-available result advertised as an item (issue #44 class): %s", body)
	}
	if strings.Contains(body, "The.Dark.Side") {
		t.Errorf("not-yet-available result's title leaked into feed (issue #44 class): %s", body)
	}
}
