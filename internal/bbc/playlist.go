package bbc

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const defaultPlaylistBase = "https://www.bbc.co.uk/programmes"

type PlaylistResolver struct {
	client  *Client
	BaseURL string
}

func NewPlaylistResolver(client *Client) *PlaylistResolver {
	return &PlaylistResolver{client: client, BaseURL: defaultPlaylistBase}
}

type PlaylistInfo struct {
	VPID      string
	Title     string
	Summary   string
	Duration  int
	Thumbnail string
	Type      string // "tv" or "radio"
	Versions  []VersionInfo
}

type VersionInfo struct {
	PID  string
	VPID string
	Type string // "original", "audiodescribed", "signed", etc.
}

func (r *PlaylistResolver) Resolve(pid string) (*PlaylistInfo, error) {
	url := fmt.Sprintf("%s/%s/playlist.json", r.BaseURL, pid)

	body, err := r.client.GetWithTimeout(url, 60*time.Second)
	if err != nil {
		return nil, fmt.Errorf("fetch playlist: %w", err)
	}

	var resp struct {
		DefaultAvailableVersion struct {
			SMPConfig struct {
				Title           string `json:"title"`
				Summary         string `json:"summary"`
				HoldingImageURL string `json:"holdingImageURL"`
				Items           []struct {
					Kind     string `json:"kind"`
					Duration int    `json:"duration"`
					VPID     string `json:"vpid"`
				} `json:"items"`
			} `json:"smpConfig"`
		} `json:"defaultAvailableVersion"`
		AllAvailableVersions []struct {
			PID       string   `json:"pid"`
			Types     []string `json:"types"`
			SMPConfig struct {
				Items []struct {
					VPID string `json:"vpid"`
				} `json:"items"`
			} `json:"smpConfig"`
		} `json:"allAvailableVersions"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse playlist: %w", err)
	}

	smp := resp.DefaultAvailableVersion.SMPConfig
	if len(smp.Items) == 0 {
		return nil, fmt.Errorf("no items in playlist for %s", pid)
	}

	info := &PlaylistInfo{
		VPID:      smp.Items[0].VPID,
		Title:     smp.Title,
		Summary:   smp.Summary,
		Duration:  smp.Items[0].Duration,
		Thumbnail: smp.HoldingImageURL,
		Type:      "tv",
	}

	if smp.Items[0].Kind == "radioProgramme" {
		info.Type = "radio"
	}

	for _, v := range resp.AllAvailableVersions {
		vi := VersionInfo{PID: v.PID}
		if len(v.SMPConfig.Items) > 0 {
			vi.VPID = v.SMPConfig.Items[0].VPID
		}
		if len(v.Types) > 0 {
			vi.Type = normaliseVersionType(v.Types[0])
		}
		info.Versions = append(info.Versions, vi)
	}

	return info, nil
}

func normaliseVersionType(raw string) string {
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "sign"):
		if strings.Contains(lower, "described") || strings.Contains(lower, "description") {
			return "combined"
		}
		return "signed"
	case strings.Contains(lower, "described") || strings.Contains(lower, "description"):
		return "audiodescribed"
	case strings.Contains(lower, "open subtitles"):
		return "opensubtitles"
	default:
		fields := strings.Fields(raw)
		if len(fields) == 0 {
			return "unknown"
		}
		return strings.ToLower(fields[0])
	}
}
