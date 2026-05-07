package bbc

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultIBLBase = "https://ibl.api.bbci.co.uk/ibl/v1"

type IBL struct {
	client  *Client
	BaseURL string
}

func NewIBL(client *Client) *IBL {
	return &IBL{client: client, BaseURL: defaultIBLBase}
}

type IBLResult struct {
	PID        string
	Title      string
	Subtitle   string
	Synopsis   string
	Channel    string
	Series     int
	EpisodeNum int
	Position   int
	AirDate    string
	Thumbnail  string
	BrandPID   string
	Duration   int // seconds
}

var (
	reSeriesNum = regexp.MustCompile(`(?i)(?:Series|Cyfres|Season)\s+(\d+)`)
	// Match either "1. Title" (numbered list) or "Episode 1" / "Pennod 1"
	// (named) forms. BBC uses both across iPlayer; without the named form
	// shows like Little Britain end up with EpisodeNum=0 and get filtered
	// out of Sonarr's tvsearch results. See issue #13.
	reEpisodeNum = regexp.MustCompile(`(?i)(?:^|(?:Episode|Pennod)\s+)(\d+)`)

	// reDateEpPart matches an epPart (the string after splitting the
	// subtitle on ": ") that is itself a bare date like "22/03/2026".
	// When the composite split yields a date as the trailing part, the
	// leading digits are day-of-month, not an episode number, and must
	// not be extracted. See issue #15.
	reDateEpPart = regexp.MustCompile(`^\s*\d{1,2}[/.\-]\d{1,2}[/.\-]\d{4}\s*$`)
)

// SearchCtx is the context-aware variant of Search. It honours ctx
// cancellation through Client.GetCtx so callers like BrowseFresh can
// bound wall time. Brand/series results are expanded via
// ListEpisodesCtx, so ctx propagates through pagination as well as
// the search call itself.
func (ibl *IBL) SearchCtx(ctx context.Context, query string, page int) ([]IBLResult, error) {
	searchURL := fmt.Sprintf("%s/new-search?q=%s&rights=web&page=%d&per_page=20",
		ibl.BaseURL, url.QueryEscape(query), page)

	body, err := ibl.client.GetCtx(ctx, searchURL)
	if err != nil {
		return nil, fmt.Errorf("iBL search: %w", err)
	}

	var resp struct {
		NewSearch struct {
			Results []iblElementJSON `json:"results"`
		} `json:"new_search"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse iBL response: %w", err)
	}

	var results []IBLResult
	for _, r := range resp.NewSearch.Results {
		channel := r.MasterBrand.Titles.Small
		thumb := ""
		if r.Images.Standard != "" {
			thumb = strings.Replace(r.Images.Standard, "{recipe}", "960x540", 1)
		}

		if r.Type == "episode" {
			results = append(results, iblElementToResult(r, channel, thumb))
		} else {
			// Brand or series -- expand into individual episodes.
			episodes, err := ibl.ListEpisodesCtx(ctx, r.ID)
			if err != nil {
				continue
			}
			for i := range episodes {
				if episodes[i].Channel == "" {
					episodes[i].Channel = channel
				}
				if episodes[i].Thumbnail == "" {
					episodes[i].Thumbnail = thumb
				}
			}
			results = append(results, episodes...)
		}
	}

	return results, nil
}

// Search is a thin wrapper around SearchCtx with a background context.
// Existing call sites that don't need cancellation use this. Behaviour
// is unchanged from prior versions.
func (ibl *IBL) Search(query string, page int) ([]IBLResult, error) {
	return ibl.SearchCtx(context.Background(), query, page)
}

// ListEpisodesCtx is the context-aware variant of ListEpisodes. It
// fetches all pages of episodes for a brand or series PID, honouring
// ctx cancellation between pages. Used by SearchCtx's brand-expansion
// path and by callers that want to bound brand-expansion wall time.
//
// Pagination semantics match the previous ListEpisodes exactly: up to
// 20 pages of 200 episodes each (4000 episode ceiling), terminating
// when the cumulative result count reaches the response Count or a
// page returns zero elements.
func (ibl *IBL) ListEpisodesCtx(ctx context.Context, pid string) ([]IBLResult, error) {
	var allResults []IBLResult
	const perPage = 200
	const maxPages = 20

	for page := 1; page <= maxPages; page++ {
		epURL := fmt.Sprintf("%s/programmes/%s/episodes?per_page=%d&page=%d",
			ibl.BaseURL, pid, perPage, page)

		body, err := ibl.client.GetCtx(ctx, epURL)
		if err != nil {
			return nil, fmt.Errorf("iBL episodes page %d: %w", page, err)
		}

		var resp struct {
			ProgrammeEpisodes struct {
				Elements []iblElementJSON `json:"elements"`
				Page     int              `json:"page"`
				PerPage  int              `json:"per_page"`
				Count    int              `json:"count"`
			} `json:"programme_episodes"`
		}

		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("parse iBL episodes page %d: %w", page, err)
		}

		for _, e := range resp.ProgrammeEpisodes.Elements {
			if e.Type != "episode" {
				continue
			}
			allResults = append(allResults, iblElementToResult(e, "", ""))
		}

		// Stop if we have all episodes or this page was empty.
		total := resp.ProgrammeEpisodes.Count
		if total <= 0 || len(allResults) >= total || len(resp.ProgrammeEpisodes.Elements) == 0 {
			break
		}
	}

	assignMissingEpisodeNumbers(allResults)
	return allResults, nil
}

// ListEpisodes is a thin wrapper around ListEpisodesCtx with a
// background context. Behaviour unchanged from prior versions.
func (ibl *IBL) ListEpisodes(pid string) ([]IBLResult, error) {
	return ibl.ListEpisodesCtx(context.Background(), pid)
}

// Editorial-pool group IDs used by BrowseFresh. `popular` is BBC's
// auto-generated popularity ranking (~150 items) -- stable. `m001bm54`
// is the "New & Trending" editorial rail (~45 items) -- editorial PIDs
// rotate seasonally, so BrowseFresh is fail-soft if it returns 404.
const (
	groupPopular        = "popular"
	groupNewAndTrending = "m001bm54"
)

// GroupEpisodes fetches a single page of episodes from an IBL editorial
// or automated group. Used by BrowseFresh to widen the wildcard RSS
// browse with curated content. Element shape matches
// programme_episodes.elements, so iblElementToResult parses both.
//
// Single-page by design: BrowseFresh caps consumption at perPage and
// discards the rest of the pool, so pagination would be wasted work.
func (ibl *IBL) GroupEpisodes(ctx context.Context, groupID string, perPage int) ([]IBLResult, error) {
	gURL := fmt.Sprintf("%s/groups/%s/episodes?per_page=%d",
		ibl.BaseURL, url.PathEscape(groupID), perPage)

	body, err := ibl.client.GetCtx(ctx, gURL)
	if err != nil {
		return nil, fmt.Errorf("iBL group episodes: %w", err)
	}

	var resp struct {
		GroupEpisodes struct {
			Elements []iblElementJSON `json:"elements"`
		} `json:"group_episodes"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse iBL group response: %w", err)
	}

	results := make([]IBLResult, 0, len(resp.GroupEpisodes.Elements))
	for _, e := range resp.GroupEpisodes.Elements {
		results = append(results, iblElementToResult(e, "", ""))
	}
	return results, nil
}

// BrowseFresh runs the wildcard RSS browse fan-out: SearchCtx("BBC")
// plus two editorial group pools, in parallel. Results are merged
// in priority order (m001bm54 → popular → search) and deduped by PID,
// so curated metadata wins when a PID appears in multiple pools.
//
// Per-pool failures are logged via slog and dropped (fail-soft). If
// all three pools fail the last error is returned so the handler can
// emit empty RSS rather than wedge.
//
// A 10s deadline is derived from the parent ctx to bound the slowest
// pool against Sonarr's 30s default RSS timeout.
func (ibl *IBL) BrowseFresh(ctx context.Context) ([]IBLResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var (
		wg      sync.WaitGroup
		results [3][]IBLResult
		errs    [3]error
		totals  [3]int
	)

	wg.Add(3)

	// Slot 0: m001bm54 (New & Trending) — highest priority.
	go func() {
		defer wg.Done()
		r, err := ibl.GroupEpisodes(ctx, "m001bm54", 50)
		results[0] = r
		errs[0] = err
		totals[0] = len(r)
	}()

	// Slot 1: popular — middle priority.
	go func() {
		defer wg.Done()
		r, err := ibl.GroupEpisodes(ctx, "popular", 150)
		results[1] = r
		errs[1] = err
		totals[1] = len(r)
	}()

	// Slot 2: search "BBC" — lowest priority slot.
	go func() {
		defer wg.Done()
		r, err := ibl.SearchCtx(ctx, "BBC", 1)
		results[2] = r
		errs[2] = err
		totals[2] = len(r)
	}()

	wg.Wait()

	// Fail-soft: log each failure but continue with whatever pools succeeded.
	successCount := 0
	poolNames := [3]string{"m001bm54", "popular", "search:BBC"}
	for i, err := range errs {
		if err != nil {
			slog.Warn("browse-fresh: pool failed",
				"pool", poolNames[i],
				"err", err)
			continue
		}
		successCount++
	}

	if successCount == 0 {
		// All three pools failed; return the last error. errs[2] is the
		// generic search BBC slot, the lowest-priority pool.
		return nil, errs[2]
	}

	// Merge with PID dedupe in priority order.
	seen := make(map[string]struct{})
	var merged []IBLResult
	for _, batch := range results {
		for _, r := range batch {
			if _, dup := seen[r.PID]; dup {
				continue
			}
			seen[r.PID] = struct{}{}
			merged = append(merged, r)
		}
	}

	slog.Info("browse-fresh: returned",
		"total", len(merged),
		"trending", totals[0],
		"popular", totals[1],
		"search", totals[2])

	return merged, nil
}

// parseISODuration parses an ISO 8601 duration like "PT10M0.040S" into seconds.
func parseISODuration(iso string) int {
	iso = strings.TrimPrefix(iso, "PT")
	var total float64
	// Parse hours
	if i := strings.Index(iso, "H"); i >= 0 {
		h, _ := strconv.ParseFloat(iso[:i], 64)
		total += h * 3600
		iso = iso[i+1:]
	}
	// Parse minutes
	if i := strings.Index(iso, "M"); i >= 0 {
		m, _ := strconv.ParseFloat(iso[:i], 64)
		total += m * 60
		iso = iso[i+1:]
	}
	// Parse seconds
	if i := strings.Index(iso, "S"); i >= 0 {
		s, _ := strconv.ParseFloat(iso[:i], 64)
		total += s
	}
	return int(total)
}

// assignMissingEpisodeNumbers fills in episode numbers for series that have
// Series>0 but all episodes have EpisodeNum=0 and Position=0.  It sorts
// episodes within each series by air date (ascending) and assigns 1, 2, 3...
// This handles shows like "Rafi the Wishing Wizard" where the BBC provides
// series numbers but no per-episode numbering or parent_position.
func assignMissingEpisodeNumbers(results []IBLResult) {
	// Group indices by series number, skipping series 0 (no series)
	bySeries := map[int][]int{}
	for i, r := range results {
		if r.Series > 0 {
			bySeries[r.Series] = append(bySeries[r.Series], i)
		}
	}

	for _, indices := range bySeries {
		// Check: all episodes in this series must lack numbering
		allMissing := true
		for _, i := range indices {
			if results[i].EpisodeNum > 0 || results[i].Position > 0 {
				allMissing = false
				break
			}
		}
		if !allMissing {
			continue
		}

		// Sort by air date ascending (earliest = episode 1)
		sort.Slice(indices, func(a, b int) bool {
			return parseLooseDate(results[indices[a]].AirDate).Before(
				parseLooseDate(results[indices[b]].AirDate))
		})

		for ep, i := range indices {
			results[i].EpisodeNum = ep + 1
		}
	}
}

// parseLooseDate handles BBC's inconsistent date format ("1 Jan 2026" or "2026-01-01").
func parseLooseDate(s string) time.Time {
	for _, layout := range []string{"2 Jan 2006", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// normaliseAirDate converts BBC's mixed release_date formats ("6 Apr 2026",
// "2026-04-06", etc.) to canonical YYYY-MM-DD so downstream code (filters,
// title generation, RSS pubDate) can rely on a single format. Returns the
// input unchanged if it can't be parsed.
func normaliseAirDate(s string) string {
	if s == "" {
		return ""
	}
	if t := parseLooseDate(s); !t.IsZero() {
		return t.Format("2006-01-02")
	}
	return s
}

func parseSubtitleNumbers(subtitle string) (series, episode int) {
	if m := reSeriesNum.FindStringSubmatch(subtitle); len(m) > 1 {
		series, _ = strconv.Atoi(m[1])
	}

	parts := strings.SplitN(subtitle, ": ", 2)
	if len(parts) >= 2 {
		epPart := parts[1]
		if reDateEpPart.MatchString(epPart) {
			// epPart is itself a date; the leading digits are day-of-month,
			// not episode number. Leave episode = 0. See issue #15.
			return series, 0
		}
		if m := reEpisodeNum.FindStringSubmatch(epPart); len(m) > 1 {
			episode, _ = strconv.Atoi(m[1])
		}
	}

	return series, episode
}

// iblElementJSON is the shared shape between programme_episodes.elements
// and group_episodes.elements. Both endpoints return episodes with
// identical field names. SearchCtx also unmarshals new_search.results
// into this struct; those payloads omit the `versions` JSON key, so
// the Versions slice unmarshals as nil and Duration stays 0.
type iblElementJSON struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	Synopses struct {
		Small string `json:"small"`
	} `json:"synopses"`
	Images struct {
		Standard string `json:"standard"`
	} `json:"images"`
	MasterBrand struct {
		Titles struct {
			Small string `json:"small"`
		} `json:"titles"`
	} `json:"master_brand"`
	ReleaseDate    string `json:"release_date"`
	ParentPosition int    `json:"parent_position"`
	TleoID         string `json:"tleo_id"`
	Versions       []struct {
		Duration struct {
			Value string `json:"value"`
		} `json:"duration"`
	} `json:"versions"`
}

// iblElementToResult converts a parsed IBL episode element to an
// IBLResult, applying channel and thumbnail fallbacks when the element
// itself doesn't carry them. Shared between ListEpisodes and the
// upcoming GroupEpisodes.
func iblElementToResult(e iblElementJSON, fallbackChannel, fallbackThumb string) IBLResult {
	channel := e.MasterBrand.Titles.Small
	if channel == "" {
		channel = fallbackChannel
	}
	thumb := ""
	if e.Images.Standard != "" {
		thumb = strings.Replace(e.Images.Standard, "{recipe}", "960x540", 1)
	}
	if thumb == "" {
		thumb = fallbackThumb
	}
	r := IBLResult{
		PID:       e.ID,
		Title:     e.Title,
		Subtitle:  e.Subtitle,
		Synopsis:  e.Synopses.Small,
		Channel:   channel,
		Position:  e.ParentPosition,
		AirDate:   normaliseAirDate(e.ReleaseDate),
		BrandPID:  e.TleoID,
		Thumbnail: thumb,
	}
	r.Series, r.EpisodeNum = parseSubtitleNumbers(e.Subtitle)
	if len(e.Versions) > 0 && e.Versions[0].Duration.Value != "" {
		r.Duration = parseISODuration(e.Versions[0].Duration.Value)
	}
	return r
}
