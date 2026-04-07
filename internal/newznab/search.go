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

	h.writeResultsRSS(w, r, results, 0, 0, "", filterName)
}

func (h *Handler) handleTVSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	tvdbid := r.URL.Query().Get("tvdbid")
	seasonStr := r.URL.Query().Get("season")
	epStr := r.URL.Query().Get("ep")

	log.Printf("[tvsearch] q=%q tvdbid=%q season=%q ep=%q", q, tvdbid, seasonStr, epStr)

	if q == "" && tvdbid != "" {
		// Try stored mapping first
		if h.store != nil {
			mapping, _ := h.store.GetSeriesMapping(tvdbid)
			if mapping != nil {
				q = mapping.ShowName
			}
		}
		// Fall back to Skyhook (Sonarr's TVDB lookup service)
		if q == "" {
			q = lookupTVDBTitle(tvdbid)
			if q != "" && h.store != nil {
				h.store.PutSeriesMapping(&store.SeriesMapping{TVDBId: tvdbid, ShowName: q})
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

	h.writeResultsRSS(w, r, results, season, ep, filterDate, filterName)
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

func (h *Handler) writeResultsRSS(w http.ResponseWriter, r *http.Request, results []bbc.IBLResult, filterSeason, filterEp int, filterDate, filterName string) {
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

func lookupTVDBTitle(tvdbid string) string {
	resp, err := http.Get("https://skyhook.sonarr.tv/v1/tvdb/shows/en/" + tvdbid)
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			resp.Body.Close()
		}
		return ""
	}
	defer resp.Body.Close()

	var show struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&show); err != nil {
		return ""
	}
	log.Printf("[tvsearch] resolved TVDB %s -> %q", tvdbid, show.Title)
	return show.Title
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
	if wantName != "" && !strings.EqualFold(strings.TrimSpace(prog.Name), wantName) {
		return false
	}
	if filterDate != "" {
		return prog.AirDate == filterDate
	}
	if filterSeason > 0 && prog.Series != filterSeason {
		return false
	}
	if filterEp > 0 && prog.EpisodeNum != filterEp {
		return false
	}
	return true
}
