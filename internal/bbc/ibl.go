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
	reSeriesNum  = regexp.MustCompile(`(?i)(?:Series|Cyfres|Season)\s+(\d+)`)
	reEpisodeNum = regexp.MustCompile(`^(\d+)\.\s*`)
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
				PID:      r.ID,
				Title:    r.Title,
				Subtitle: r.Subtitle,
				Synopsis: r.Synopses.Small,
				Channel:  channel,
				Position: r.ParentPosition,
				AirDate:  r.ReleaseDate,
				BrandPID: r.TleoID,
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
func (ibl *IBL) ListEpisodes(pid string) ([]IBLResult, error) {
	epURL := fmt.Sprintf("%s/programmes/%s/episodes?per_page=200&page=1",
		ibl.BaseURL, pid)

	body, err := ibl.client.Get(epURL)
	if err != nil {
		return nil, fmt.Errorf("iBL episodes: %w", err)
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
		} `json:"programme_episodes"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse episodes: %w", err)
	}

	var results []IBLResult
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
			AirDate:  e.ReleaseDate,
			BrandPID: e.TleoID,
			Duration: duration,
		}
		if e.Images.Standard != "" {
			result.Thumbnail = strings.Replace(e.Images.Standard, "{recipe}", "960x540", 1)
		}
		result.Series, result.EpisodeNum = parseSubtitleNumbers(e.Subtitle)
		results = append(results, result)
	}

	assignMissingEpisodeNumbers(results)

	return results, nil
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

func parseSubtitleNumbers(subtitle string) (series, episode int) {
	if m := reSeriesNum.FindStringSubmatch(subtitle); len(m) > 1 {
		series, _ = strconv.Atoi(m[1])
	}

	parts := strings.SplitN(subtitle, ": ", 3)
	if len(parts) >= 2 {
		epPart := parts[len(parts)-1]
		if m := reEpisodeNum.FindStringSubmatch(epPart); len(m) > 1 {
			episode, _ = strconv.Atoi(m[1])
		}
	}

	return series, episode
}
