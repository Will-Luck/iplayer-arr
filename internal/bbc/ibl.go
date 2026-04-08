package bbc

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
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

func (ibl *IBL) Search(query string, page int) ([]IBLResult, error) {
	searchURL := fmt.Sprintf("%s/new-search?q=%s&rights=web&page=%d&per_page=20",
		ibl.BaseURL, url.QueryEscape(query), page)

	body, err := ibl.client.Get(searchURL)
	if err != nil {
		return nil, fmt.Errorf("iBL search: %w", err)
	}

	var resp struct {
		NewSearch struct {
			Results []struct {
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
			} `json:"results"`
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
			result := IBLResult{
				PID:       r.ID,
				Title:     r.Title,
				Subtitle:  r.Subtitle,
				Synopsis:  r.Synopses.Small,
				Channel:   channel,
				Position:  r.ParentPosition,
				AirDate:   normaliseAirDate(r.ReleaseDate),
				BrandPID:  r.TleoID,
				Thumbnail: thumb,
			}
			result.Series, result.EpisodeNum = parseSubtitleNumbers(r.Subtitle)
			results = append(results, result)
		} else {
			// Brand or series -- expand into individual episodes
			episodes, err := ibl.ListEpisodes(r.ID)
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

// ListEpisodes fetches all episodes for a brand or series PID.
// It paginates through all pages (up to 20, i.e. 4000 episodes) to avoid
// silently truncating brands with more than 200 episodes.
func (ibl *IBL) ListEpisodes(pid string) ([]IBLResult, error) {
	var allResults []IBLResult
	const perPage = 200
	const maxPages = 20

	for page := 1; page <= maxPages; page++ {
		epURL := fmt.Sprintf("%s/programmes/%s/episodes?per_page=%d&page=%d",
			ibl.BaseURL, pid, perPage, page)

		body, err := ibl.client.Get(epURL)
		if err != nil {
			return nil, fmt.Errorf("iBL episodes page %d: %w", page, err)
		}

		var resp struct {
			ProgrammeEpisodes struct {
				Elements []struct {
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
				} `json:"elements"`
				Page    int `json:"page"`
				PerPage int `json:"per_page"`
				Count   int `json:"count"`
			} `json:"programme_episodes"`
		}

		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("parse iBL episodes page %d: %w", page, err)
		}

		for _, e := range resp.ProgrammeEpisodes.Elements {
			if e.Type != "episode" {
				continue
			}
			duration := 0
			if len(e.Versions) > 0 && e.Versions[0].Duration.Value != "" {
				duration = parseISODuration(e.Versions[0].Duration.Value)
			}
			result := IBLResult{
				PID:      e.ID,
				Title:    e.Title,
				Subtitle: e.Subtitle,
				Synopsis: e.Synopses.Small,
				Channel:  e.MasterBrand.Titles.Small,
				Position: e.ParentPosition,
				AirDate:  normaliseAirDate(e.ReleaseDate),
				BrandPID: e.TleoID,
				Duration: duration,
			}
			if e.Images.Standard != "" {
				result.Thumbnail = strings.Replace(e.Images.Standard, "{recipe}", "960x540", 1)
			}
			result.Series, result.EpisodeNum = parseSubtitleNumbers(e.Subtitle)
			allResults = append(allResults, result)
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

	parts := strings.SplitN(subtitle, ": ", 3)
	if len(parts) >= 2 {
		epPart := parts[len(parts)-1]
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
