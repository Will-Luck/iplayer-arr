package newznab

import (
	"context"
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

// Wildcard-browse constants. Exposed via tests, not config.
const (
	// browseCapPIDs is the maximum number of unique PIDs returned from
	// BrowseFresh after dedupe. With per-PID quality cap of 2, the
	// emitted RSS contains ≤ browseCapPIDs * 2 = 100 items, matching
	// the advertised Newznab cap (caps.go max=100 default=50).
	browseCapPIDs = 50

	// browseQualitiesPerPID is the per-PID quality emission cap in
	// wildcard mode. heightsToTags returns highest-first, so trimming
	// keeps the best two qualities per PID.
	browseQualitiesPerPID = 2

	// browseProbeDeadline bounds the quality prober's wall time on
	// wildcard browse so first-run misses fall through to safe
	// fallback heights without exceeding Sonarr's 30s.
	browseProbeDeadline = 5 * time.Second
)

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	if h.ibl == nil {
		writeEmptyRSS(w)
		return
	}

	// Radarr's text-fallback movie search uses the general search
	// endpoint with Movies categories rather than t=movie (its
	// indexer test uses t=movie, searches do not). Route those to
	// the movie path so they get movie titles, categories and the
	// wrong-content name guard.
	if hasMovieCategory(r.URL.Query().Get("cat")) {
		h.handleMovieSearch(w, r)
		return
	}

	q := r.URL.Query().Get("q")
	filterName := q
	isWildcard := q == ""

	var results []bbc.IBLResult
	var err error
	if isWildcard {
		results, err = h.wildcardBrowse(r.Context())
	} else {
		results, err = h.ibl.Search(q, 1)
	}
	if err != nil {
		writeEmptyRSS(w)
		return
	}

	h.writeResultsRSS(w, r, results, 0, 0, "", filterName, 0, "", isWildcard, parseLimitParam(r))
}

func (h *Handler) handleTVSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	tvdbid := r.URL.Query().Get("tvdbid")
	seasonStr := r.URL.Query().Get("season")
	epStr := r.URL.Query().Get("ep")

	log.Printf("[tvsearch] q=%q tvdbid=%q season=%q ep=%q", q, tvdbid, seasonStr, epStr)

	// Issue #31: Sonarr sends q=ShowName with an empty tvdbid on
	// episode-level follow-up queries (after the initial tvdbid-only
	// lookup warmed the store). Recover the tvdbid from the store so
	// the <newznab:attr name="tvdbid"> echo in writeResultsRSS still
	// fires on these follow-ups. The request's own tvdbid parameter
	// always wins -- we only rehydrate when tvdbid == "".
	var filterYear int
	if q != "" && tvdbid == "" && h.store != nil {
		cached, _ := h.store.GetSeriesMappingByName(q)
		if cached != nil {
			tvdbid = cached.TVDBId
			if cached.Year > 0 {
				filterYear = cached.Year
			}
			log.Printf("[tvsearch] rehydrated tvdbid=%q for q=%q from store",
				tvdbid, q)
		}
	}
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
	isWildcard := q == "" && tvdbid == ""

	var results []bbc.IBLResult
	var err error
	if isWildcard {
		results, err = h.wildcardBrowse(r.Context())
	} else {
		if q == "" {
			q = "BBC"
		}
		results, err = h.ibl.Search(q, 1)
	}
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

	h.writeResultsRSS(w, r, results, season, ep, filterDate, filterName, filterYear, tvdbid, isWildcard, parseLimitParam(r))
}

// parseLimitParam reads Sonarr's `limit=N` query parameter, the
// advertised pagination cap (caps.go advertises max=100). Returns 0
// when absent or invalid (callers treat 0 as "no client-side cap").
// Clamps any requested value to the advertised max so a hostile or
// misconfigured client cannot demand more items than the indexer
// claims to support. Audit item 24.
func parseLimitParam(r *http.Request) int {
	s := r.URL.Query().Get("limit")
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0
	}
	const advertisedMax = 100 // matches caps.go <limits max="100" .../>
	if n > advertisedMax {
		return advertisedMax
	}
	return n
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

func (h *Handler) writeResultsRSS(w http.ResponseWriter, r *http.Request, results []bbc.IBLResult, filterSeason, filterEp int, filterDate, filterName string, filterYear int, tvdbid string, wildcardBrowse bool, limit int) {
	var items []string
	wantName := strings.TrimSpace(filterName)

	// Sonarr applies its configured apikey to the initial tvsearch but
	// fetches the grab URL straight from the feed, so every link/guid/
	// enclosure has to carry the key inline or authenticate() 401s the grab.
	apiKeyParam := ""
	if h.store != nil {
		if apiKey, _ := h.store.GetConfig("api_key"); apiKey != "" {
			apiKeyParam = "&amp;apikey=" + apiKey
		}
	}

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
		prog := IBLResultToProgramme(res)
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

	var probe bbc.PrefetchResult
	if h.prober != nil && len(probeItems) > 0 {
		probeCtx := r.Context()
		if wildcardBrowse {
			var cancel context.CancelFunc
			probeCtx, cancel = context.WithTimeout(r.Context(), browseProbeDeadline)
			defer cancel()
		}
		probe = h.prober.PrefetchPIDs(probeCtx, probeItems)
	}

	// Build the final emit list before rendering. The limit cap is
	// enforced here rather than on the rendered slice so that
	// first-seen timestamps are stamped only for items Sonarr actually
	// receives: stamping a trimmed item would backdate its pubDate
	// before Sonarr ever saw it, recreating issue #47 in miniature.
	// The cap honours Sonarr's `limit=N` query parameter, already
	// clamped to the advertised max=100 in parseLimitParam. Audit
	// item 24.
	type emitItem struct {
		res       bbc.IBLResult
		prog      *store.Programme
		override  *store.ShowOverride
		qualities []string
	}
	var emits []emitItem
	itemCount := 0
	for _, it := range filtered {
		if limit > 0 && itemCount >= limit {
			break
		}
		res, prog := it.res, it.prog

		if probe.NotYetAvailable[res.PID] {
			// Episode exists in metadata but BBC has not published its streams
			// yet. Skip advertising so Sonarr re-queries on the next RSS cycle
			// once it is live, instead of grabbing a 720p fallback that cannot
			// be downloaded and gets blocklisted. Issue #44.
			continue
		}

		// When the BBC title has a subtitle that the Sonarr query
		// doesn't (e.g. "Talking Tom Heroes: Suddenly Super" vs
		// "Talking Tom Heroes"), use the Sonarr-provided name for
		// title generation so Sonarr can match it back to TVDB.
		if wantName != "" && !strings.EqualFold(
			strings.TrimSpace(bareName(prog.Name)),
			strings.TrimSpace(bareName(wantName))) {
			prog.Name = wantName
		}

		var override *store.ShowOverride
		if h.store != nil {
			override, _ = h.store.GetOverride(prog.Name)
		}

		ceiling := h.qualityCeilingHeight()
		var qualities []string
		if probe.Heights[res.PID] != nil {
			qualities = heightsToTags(capHeights(probe.Heights[res.PID], ceiling))
		} else {
			// Safe fallback: only what BBC universally delivers.
			qualities = capQualityTags([]string{"720p", "540p"}, ceiling)
		}

		if wildcardBrowse && len(qualities) > browseQualitiesPerPID {
			qualities = qualities[:browseQualitiesPerPID]
		}
		if limit > 0 && itemCount+len(qualities) > limit {
			qualities = qualities[:limit-itemCount]
		}
		if len(qualities) == 0 {
			continue
		}
		itemCount += len(qualities)
		emits = append(emits, emitItem{res: res, prog: prog, override: override, qualities: qualities})
	}

	// Stamp each emitted PID with the time it first appeared in the
	// feed and use that stamp as the primary pubDate source. BBC
	// promotes items onto the browse rails long after
	// availability.start, so an availability-derived pubDate can still
	// sit below Sonarr's RSS watermark the first time we surface an
	// item. Issue #47.
	//
	// Writes are browse-only: Sonarr's watermark advances with the
	// wildcard RSS feed, so a stamp created by a q=/tvdbid= search at
	// T1 would let the item debut in the feed later with pubDate=T1 --
	// already below the watermark, recreating issue #47 via the search
	// path. Searches do a read-only lookup instead, so already-stamped
	// PIDs advertise the same pubDate as the feed and unstamped ones
	// keep the fallback chain below.
	//
	// The stamp lands before the first response byte, so an aborted
	// response still stamps and the next successful poll carries a
	// pubDate one interval older than Sonarr's actual first sight.
	// Accepted: that stamp still post-dates the previous successful
	// poll, keeping it above the watermark.
	var firstSeen map[string]time.Time
	if h.store != nil && len(emits) > 0 {
		pids := make([]string, 0, len(emits))
		for _, e := range emits {
			pids = append(pids, e.res.PID)
		}
		var err error
		if wildcardBrowse {
			firstSeen, err = h.store.GetOrSetFirstSeenBatch(pids, time.Now())
		} else {
			firstSeen, err = h.store.GetFirstSeenBatch(pids)
		}
		if err != nil {
			log.Printf("first-seen lookup failed, falling back to availability dates: %v", err)
		}
	}

	for _, e := range emits {
		res, prog := e.res, e.prog

		// pubDate precedence: first-seen -> availability.start ->
		// broadcast date -> now. Computed once per PID so every quality
		// variant of an episode shares the same age in Sonarr.
		pubDate := time.Now().Format(time.RFC1123Z)
		if ts, ok := firstSeen[res.PID]; ok {
			pubDate = ts.Format(time.RFC1123Z)
		} else if !res.Available.IsZero() {
			pubDate = res.Available.Format(time.RFC1123Z)
		} else if res.AirDate != "" {
			if t, err := time.Parse("2006-01-02", res.AirDate); err == nil {
				pubDate = t.Format(time.RFC1123Z)
			}
		}

		// Carry the availability date (else broadcast date) as a
		// bespoke attribute for diagnostics. Deliberately NOT named
		// usenetdate: Sonarr prefers usenetdate over pubDate for
		// release age, which would resurrect issue #47.
		broadcastDate := ""
		if !res.Available.IsZero() {
			broadcastDate = res.Available.Format(time.RFC1123Z)
		} else if res.AirDate != "" {
			if t, err := time.Parse("2006-01-02", res.AirDate); err == nil {
				broadcastDate = t.Format(time.RFC1123Z)
			}
		}

		for _, qual := range e.qualities {
			title, tier := GenerateTitle(prog, qual, e.override)
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

			item := fmt.Sprintf(`    <item>
      <title>%s</title>
      <guid isPermaLink="true">%s/newznab/api?t=get&amp;id=%s%s</guid>
      <link>%s/newznab/api?t=get&amp;id=%s%s</link>
      <pubDate>%s</pubDate>
      <enclosure url="%s/newznab/api?t=get&amp;id=%s%s" length="%d" type="application/x-nzb" />
      <newznab:attr name="category" value="%s" />
      <newznab:attr name="size" value="%d" />
      <newznab:attr name="language" value="en" />`,
				html.EscapeString(title), baseURL(r), guid, apiKeyParam, baseURL(r), guid, apiKeyParam, pubDate,
				baseURL(r), guid, apiKeyParam, size, cat, size)

			if tvdbid != "" {
				item += fmt.Sprintf("\n      <newznab:attr name=\"tvdbid\" value=\"%s\" />", html.EscapeString(tvdbid))
			}

			if tier == store.TierManual {
				item += `
      <newznab:attr name="iparr:manual" value="true" />`
			}

			if broadcastDate != "" {
				item += fmt.Sprintf("\n      <newznab:attr name=\"iparr:broadcastdate\" value=\"%s\" />", broadcastDate)
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

// IBLResultToProgramme converts an IBL search result into the Programme
// shape consumed by GenerateTitle. Exported so the manual-download API
// path can apply the exact same identity normalisation as the feed
// (issue #48).
func IBLResultToProgramme(r bbc.IBLResult) *store.Programme {
	// Position-based identity promotion. BBC long-runners like Casualty
	// and One Piece 1999 carry subtitles such as "Learning Curve Episode 3"
	// that parseSubtitleNumbers reads as Series=0, EpisodeNum=3. Sonarr
	// sends season=1 for these shows (their TVDB record has a single
	// series), so matchesSearchFilter's Series==filterSeason gate rejects
	// every item. Promote Series=1 whenever we have a real episode number
	// but no series prefix. Position alone is not enough -- one-offs and
	// specials also carry Position>0. GitHub #32.
	series := r.Series
	if series == 0 && r.EpisodeNum > 0 {
		series = 1
	}
	return &store.Programme{
		PID:        r.PID,
		Name:       r.Title,
		Episode:    r.Subtitle,
		Series:     series,
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

// qualityCeilingHeight returns the maximum video height (pixels) the
// indexer should advertise to Sonarr, honouring the "quality" config
// value. Empty string, "any", or any value that doesn't parse as a
// Newznab quality tag (e.g. "1080p" -> 1080) means no ceiling and
// returns 0; callers treat 0 as "do not filter". Issue #28.
func (h *Handler) qualityCeilingHeight() int {
	if h.store == nil {
		return 0
	}
	v, _ := h.store.GetConfig("quality")
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" || v == "any" {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSuffix(v, "p"))
	if err != nil {
		return 0
	}
	return n
}

// capHeights drops any heights strictly greater than ceiling. A ceiling
// of 0 disables filtering and returns heights unchanged. Issue #28.
func capHeights(heights []int, ceiling int) []int {
	if ceiling <= 0 {
		return heights
	}
	out := make([]int, 0, len(heights))
	for _, h := range heights {
		if h <= ceiling {
			out = append(out, h)
		}
	}
	return out
}

// capQualityTags clamps the fallback quality-tag slice (e.g. ["720p","540p"])
// to those whose height is <= ceiling. A ceiling of 0 disables filtering.
// Issue #28.
func capQualityTags(tags []string, ceiling int) []string {
	if ceiling <= 0 {
		return tags
	}
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		n, err := strconv.Atoi(strings.TrimSuffix(t, "p"))
		if err != nil || n <= ceiling {
			out = append(out, t)
		}
	}
	return out
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

// nameMatches reports whether progName matches wantName after stripping
// year suffixes. Also accepts a BBC subtitle extension ("Show: Subtitle"
// matches a query for "Show"). GitHub #21.
func nameMatches(progName, wantName string) bool {
	bare := strings.TrimSpace(bareName(progName))
	want := strings.TrimSpace(bareName(wantName))
	if strings.EqualFold(bare, want) {
		return true
	}
	if len(bare) > len(want)+2 &&
		strings.EqualFold(bare[:len(want)], want) &&
		bare[len(want):len(want)+2] == ": " {
		return true
	}
	return false
}

// matchesSearchFilter applies every filter that the emit loop applies,
// in the same order. Shared between the prefetch and emit passes so
// they cannot drift out of sync.
func matchesSearchFilter(prog *store.Programme, wantName, filterDate string, filterSeason, filterEp int) bool {
	if wantName != "" && !nameMatches(prog.Name, wantName) {
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

// wildcardBrowse runs the BrowseFresh fan-out and applies the
// browseCapPIDs cap before returning. Used by handleSearch and
// handleTVSearch when their respective wildcard conditions hold.
func (h *Handler) wildcardBrowse(ctx context.Context) ([]bbc.IBLResult, error) {
	results, err := h.ibl.BrowseFresh(ctx)
	if err != nil {
		return nil, err
	}
	return capBrowseResults(results, browseCapPIDs), nil
}

// capBrowseResults trims the merged BrowseFresh slice to at most n
// entries, preserving the m001bm54 → popular → search-BBC ordering
// established in BrowseFresh.
func capBrowseResults(results []bbc.IBLResult, n int) []bbc.IBLResult {
	if len(results) <= n {
		return results
	}
	return results[:n]
}
