package newznab

import (
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/store"
)

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	if h.ibl == nil {
		writeEmptyRSS(w)
		return
	}

	q := r.URL.Query().Get("q")
	filterName := q // captured before the BBC fallback so wildcard browses don't apply a filter
	if q == "" {
		q = "BBC" // default query for RSS feed and Sonarr test
	}

	results, err := h.ibl.Search(q, 1)
	if err != nil {
		writeEmptyRSS(w)
		return
	}

	h.writeResultsRSS(w, r, results, 0, 0, "", filterName, 0)
}

func (h *Handler) handleTVSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	tvdbid := r.URL.Query().Get("tvdbid")
	seasonStr := r.URL.Query().Get("season")
	epStr := r.URL.Query().Get("ep")

	log.Printf("[tvsearch] q=%q tvdbid=%q season=%q ep=%q", q, tvdbid, seasonStr, epStr)

	var filterYear int
	if q == "" && tvdbid != "" {
		// Try stored mapping first - but only use the warm cache if it
		// has a year (Year > 0). Old v1.0.2/v1.1.0 records have no year
		// field in the JSON and deserialise to Year=0 - those need to
		// be backfilled by re-hitting Skyhook on first use after the
		// upgrade. See issue #18 and the Phase 4 design doc.
		if h.store != nil {
			cached, _ := h.store.GetSeriesMapping(tvdbid)
			if cached != nil && cached.Year > 0 {
				q = cached.ShowName
				filterYear = cached.Year
			}
		}
		// Fall back to Skyhook when there's no warm cache OR when the
		// warm cache has Year == 0 (backfill case).
		if q == "" {
			title, year, err := lookupTVDBShow(tvdbid)
			if err == nil && title != "" {
				q = title
				filterYear = year
				if h.store != nil {
					h.store.PutSeriesMapping(&store.SeriesMapping{
						TVDBId:   tvdbid,
						ShowName: title,
						Year:     year,
					})
				}
			}
		}
	}

	if h.ibl == nil {
		writeEmptyRSS(w)
		return
	}
	// Capture the resolved show name (either Sonarr's q= or the
	// tvdbid → Skyhook lookup) BEFORE the BBC fallback so the wildcard
	// browse path doesn't accidentally inherit a filter.
	filterName := q
	if q == "" {
		q = "BBC"
	}

	results, err := h.ibl.Search(q, 1)
	if err != nil {
		writeEmptyRSS(w)
		return
	}

	season, _ := strconv.Atoi(seasonStr)
	ep, _ := strconv.Atoi(epStr)

	// Sonarr sends two distinct tvsearch shapes:
	//   - Standard:    season=<int>          ep=<int>
	//   - Daily series: season=<YYYY>         ep=<MM/DD>
	// For daily soaps the integer compare against prog.Series/EpisodeNum
	// can never match (iPlayer reports Series=0 + Position=<flat counter>),
	// so detect the daily shape and filter by air date instead.
	filterDate := parseDailySearchDate(seasonStr, epStr)

	h.writeResultsRSS(w, r, results, season, ep, filterDate, filterName, filterYear)
}

// parseDailySearchDate returns YYYY-MM-DD when season looks like a 4-digit
// year and ep looks like MM/DD (Sonarr's daily-series tvsearch convention).
// Returns "" for any other shape so the standard integer filter is used.
func parseDailySearchDate(seasonStr, epStr string) string {
	if len(seasonStr) != 4 {
		return ""
	}
	year, err := strconv.Atoi(seasonStr)
	if err != nil || year < 1900 || year > 2100 {
		return ""
	}
	parts := strings.Split(epStr, "/")
	if len(parts) != 2 {
		return ""
	}
	mm, err1 := strconv.Atoi(parts[0])
	dd, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return ""
	}
	if mm < 1 || mm > 12 || dd < 1 || dd > 31 {
		return ""
	}
	return fmt.Sprintf("%04d-%02d-%02d", year, mm, dd)
}

func (h *Handler) writeResultsRSS(w http.ResponseWriter, r *http.Request, results []bbc.IBLResult, filterSeason, filterEp int, filterDate, filterName string, filterYear int) {
	var items []string
	wantName := strings.TrimSpace(filterName)

	type filteredItem struct {
		res  bbc.IBLResult
		prog *store.Programme
	}

	// Single pass: filter, dedupe by PID, and build the prefetch list
	// from the exact set of items that will emit. See spec section
	// "Search-handler integration" for the rationale.
	var filtered []filteredItem
	var probeItems []bbc.ProbeItem
	seen := make(map[string]struct{}, len(results))
	for _, res := range results {
		if _, dup := seen[res.PID]; dup {
			continue
		}
		prog := iblResultToProgramme(res)
		if !matchesSearchFilter(prog, wantName, filterDate, filterSeason, filterEp) {
			continue
		}
		seen[res.PID] = struct{}{}
		filtered = append(filtered, filteredItem{res: res, prog: prog})
		probeItems = append(probeItems, bbc.ProbeItem{PID: res.PID, ShowName: prog.Name})
	}

	// Phase 4 disambiguation: when a TVDB lookup gave us a year hint,
	// drop candidates whose year-suffixed brand title doesn't cover the
	// hint year. This routes Sonarr searches for shows with duplicate
	// BBC brand names (classic Doctor Who vs modern Doctor Who) to the
	// correct era. See issue #18.
	if filterYear > 0 {
		var progsByPID = make(map[string]*store.Programme, len(filtered))
		var orderedProgs []*store.Programme
		for _, it := range filtered {
			if _, ok := progsByPID[it.res.PID]; !ok {
				progsByPID[it.res.PID] = it.prog
				orderedProgs = append(orderedProgs, it.prog)
			}
		}
		kept := disambiguateByYear(orderedProgs, filterYear)
		keptPIDs := make(map[string]bool, len(kept))
		for _, p := range kept {
			for pid, prog := range progsByPID {
				if prog == p {
					keptPIDs[pid] = true
				}
			}
		}
		// Rebuild filtered and probeItems to include only kept PIDs
		var newFiltered []filteredItem
		var newProbeItems []bbc.ProbeItem
		for i, it := range filtered {
			if keptPIDs[it.res.PID] {
				newFiltered = append(newFiltered, it)
				if i < len(probeItems) {
					newProbeItems = append(newProbeItems, probeItems[i])
				}
			}
		}
		filtered = newFiltered
		probeItems = newProbeItems
	}

	var probedHeights map[string][]int
	if h.prober != nil && len(probeItems) > 0 {
		probedHeights = h.prober.PrefetchPIDs(r.Context(), probeItems)
	}

	for _, it := range filtered {
		res, prog := it.res, it.prog

		var override *store.ShowOverride
		if h.store != nil {
			override, _ = h.store.GetOverride(prog.Name)
		}

		// Quality decision: probe result > safe fallback.
		// The previous `if len(prog.Qualities) > 0 { ... }` override branch
		// is removed because Programme.Qualities was never set anywhere in
		// the repo. See spec round-1 finding 3 for full explanation.
		var qualities []string
		if probedHeights[res.PID] != nil {
			qualities = heightsToTags(probedHeights[res.PID])
		} else {
			// No prober wired, OR probe failed (nil result-map entry). Emit
			// only what BBC universally delivers. Never advertise a speculative
			// 1080p — that is the EastEnders bug this whole feature fixes.
			qualities = []string{"720p", "540p"}
		}

		for _, qual := range qualities {
			title, tier := GenerateTitle(prog, qual, override)
			guid := EncodeGUID(res.PID, qual, "original")

			cat := "5040" // HD
			switch qual {
			case "2160p":
				cat = "5045" // UHD
			case "540p", "396p":
				cat = "5030" // SD
			}

			size := estimateSize(prog.Duration, qual)
			prog.IdentityTier = tier

			pubDate := time.Now().Format(time.RFC1123Z)
			if res.AirDate != "" {
				if t, err := time.Parse("2006-01-02", res.AirDate); err == nil {
					pubDate = t.Format(time.RFC1123Z)
				}
			}

			item := fmt.Sprintf(`    <item>
      <title>%s</title>
      <guid isPermaLink="true">%s/newznab/api?t=get&amp;id=%s</guid>
      <link>%s/newznab/api?t=get&amp;id=%s</link>
      <pubDate>%s</pubDate>
      <enclosure url="%s/newznab/api?t=get&amp;id=%s" length="%d" type="application/x-nzb" />
      <newznab:attr name="category" value="%s" />
      <newznab:attr name="size" value="%d" />`,
				html.EscapeString(title), baseURL(r), guid, baseURL(r), guid, pubDate,
				baseURL(r), guid, size, cat, size)

			if tier == store.TierManual {
				item += `
      <newznab:attr name="iparr:manual" value="true" />`
			}

			item += "\n    </item>"
			items = append(items, item)
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

func writeEmptyRSS(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/xml")
	w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/">
  <channel><title>iplayer-arr</title></channel>
</rss>`))
}

func iblResultToProgramme(r bbc.IBLResult) *store.Programme {
	return &store.Programme{
		PID:        r.PID,
		Name:       r.Title,
		Episode:    r.Subtitle,
		Series:     r.Series,
		EpisodeNum: r.EpisodeNum,
		Position:   r.Position,
		AirDate:    r.AirDate,
		Channel:    r.Channel,
		Thumbnail:  r.Thumbnail,
		Duration:   r.Duration,
	}
}

func estimateSize(durationSec int, quality string) int64 {
	if durationSec == 0 {
		durationSec = 1800 // default 30 min if unknown
	}
	// Realistic BBC iPlayer bitrates (video + audio combined)
	kbps := map[string]int{
		"1080p": 5000,
		"720p":  3200,
		"540p":  1800,
		"396p":  1000,
	}
	rate, ok := kbps[quality]
	if !ok {
		rate = 3200
	}
	return int64(durationSec) * int64(rate) * 1000 / 8
}

func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, r.Host)
}

// skyhookBaseURL is the base URL for TheTVDB-to-BBC title resolution
// via the Sonarr Skyhook service. Overridable in tests to point at
// httptest.NewServer without touching global HTTP transport.
var skyhookBaseURL = "https://skyhook.sonarr.tv"

// lookupTVDBShow resolves a TVDB ID to (showName, firstAiredYear) via
// the Skyhook service. Returns ("", 0, err) on any failure - callers
// fall back to bare-name behaviour with no year disambiguation.
//
// Replaces the v1.1.0 lookupTVDBTitle which only returned the show
// name. The year is needed for Phase 4 disambiguation of shows with
// duplicate BBC brand names (classic Doctor Who vs modern Doctor Who).
func lookupTVDBShow(tvdbid string) (title string, year int, err error) {
	resp, err := http.Get(skyhookBaseURL + "/v1/tvdb/shows/en/" + tvdbid)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("skyhook returned status %d", resp.StatusCode)
	}

	var show struct {
		Title      string `json:"title"`
		FirstAired string `json:"firstAired"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&show); err != nil {
		return "", 0, err
	}

	title = show.Title
	if len(show.FirstAired) >= 4 {
		year, _ = strconv.Atoi(show.FirstAired[:4])
	}
	log.Printf("[tvsearch] resolved TVDB %s -> %q (year %d)", tvdbid, title, year)
	return title, year, nil
}

// heightsToTags converts a descending list of heights to Newznab quality
// tags. Returns nil for an empty slice (not []string{}) so callers can
// distinguish "no quality info" from "empty list". The mapping matches
// the existing hardcoded tag set in writeResultsRSS.
func heightsToTags(heights []int) []string {
	if len(heights) == 0 {
		return nil
	}
	out := make([]string, 0, len(heights))
	for _, h := range heights {
		switch {
		case h >= 2160:
			out = append(out, "2160p")
		case h >= 1080:
			out = append(out, "1080p")
		case h >= 720:
			out = append(out, "720p")
		case h >= 540:
			out = append(out, "540p")
		case h >= 396:
			out = append(out, "396p")
		}
	}
	return out
}

// matchesSearchFilter applies every filter that the emit loop applies,
// in the same order. Extracted into a shared helper so the prefetch
// pass and the emit pass cannot drift out of sync. Returns true if the
// programme should appear in the RSS response.
//
// The Programme type is *store.Programme (the persistence model, see
// store/types.go:35) — NOT *bbc.Programme, which does not exist.
// iblResultToProgramme at line ~231 below returns *store.Programme.
func matchesSearchFilter(prog *store.Programme, wantName, filterDate string, filterSeason, filterEp int) bool {
	if wantName != "" && !strings.EqualFold(strings.TrimSpace(bareName(prog.Name)), bareName(wantName)) {
		return false
	}
	if filterDate != "" {
		return prog.AirDate == filterDate
	}
	// Topical/weekly escape hatch. Shows like Question Time or Newsnight
	// arrive from iPlayer with no series/episode numbering (Series=0,
	// EpisodeNum=0) but a valid AirDate. A strict integer-S/E filter
	// would reject every such release, which is why Sonarr's interactive
	// search returns nothing even though the in-app search finds the
	// episode. Accept them so GenerateTitle can emit a date-tier title;
	// the user must set the series type to "Daily" in Sonarr for it to
	// match by air date. See GitHub issue #20.
	if prog.Series == 0 && prog.EpisodeNum == 0 && prog.AirDate != "" {
		return true
	}
	if filterSeason > 0 && prog.Series != filterSeason {
		return false
	}
	if filterEp > 0 && prog.EpisodeNum != filterEp {
		return false
	}
	return true
}
