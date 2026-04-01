package bbc

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
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
		result := IBLResult{
			PID:      r.ID,
			Title:    r.Title,
			Subtitle: r.Subtitle,
			Synopsis: r.Synopses.Small,
			Channel:  r.MasterBrand.Titles.Small,
			Position: r.ParentPosition,
			AirDate:  r.ReleaseDate,
			BrandPID: r.TleoID,
		}

		if r.Images.Standard != "" {
			result.Thumbnail = strings.Replace(r.Images.Standard, "{recipe}", "960x540", 1)
		}

		result.Series, result.EpisodeNum = parseSubtitleNumbers(r.Subtitle)

		results = append(results, result)
	}

	return results, nil
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
