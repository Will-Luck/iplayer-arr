package bbc

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
)

const defaultMediaSelectorBase = "https://open.live.bbc.co.uk/mediaselector"

var errGeoBlocked = errors.New("geo-blocked: BBC iPlayer is only available in the UK")

// MediaSelector resolves a VPID to actual HLS stream URLs via BBC's
// open.live.bbc.co.uk mediaselector endpoint.
type MediaSelector struct {
	client  *Client
	BaseURL string
}

// NewMediaSelector creates a MediaSelector backed by the given client.
func NewMediaSelector(client *Client) *MediaSelector {
	return &MediaSelector{client: client, BaseURL: defaultMediaSelectorBase}
}

// StreamSet holds the resolved video streams and optional subtitle URL.
type StreamSet struct {
	Video       []VideoStream
	SubtitleURL string
}

// VideoStream is a single resolved HLS video stream.
type VideoStream struct {
	URL      string
	Height   int
	Width    int
	Bitrate  int
	Supplier string
	Protocol string
	Format   string // "hls" or "dash"
}

// IsGeoBlocked reports whether err is a geo-block error from MediaSelector.
func IsGeoBlocked(err error) bool {
	return errors.Is(err, errGeoBlocked)
}

// --- XML types ---

type mediaSelectionXML struct {
	XMLName xml.Name       `xml:"mediaSelection"`
	Media   []mediaElement `xml:"media"`
	Error   *struct {
		ID string `xml:"id,attr"`
	} `xml:"error"`
}

type mediaElement struct {
	Kind        string          `xml:"kind,attr"`
	Type        string          `xml:"type,attr"`
	Encoding    string          `xml:"encoding,attr"`
	Bitrate     int             `xml:"bitrate,attr"`
	Width       int             `xml:"width,attr"`
	Height      int             `xml:"height,attr"`
	Connections []connectionXML `xml:"connection"`
}

type connectionXML struct {
	Supplier       string `xml:"supplier,attr"`
	TransferFormat string `xml:"transferFormat,attr"`
	Protocol       string `xml:"protocol,attr"`
	Href           string `xml:"href,attr"`
}

// Resolve queries BBC mediaselector for the given VPID and returns a StreamSet.
// It tries v6 "iptv-all" first, then falls back through v5 and the "pc" mediaset.
func (ms *MediaSelector) ResolveCtx(ctx context.Context, vpid string) (*StreamSet, error) {
	mediasets := []string{"iptv-all", "pc"}
	versions := []int{6, 5}

	for _, mediaset := range mediasets {
		for _, ver := range versions {
			cb := fmt.Sprintf("%05d", rand.Intn(100000))
			reqURL := fmt.Sprintf("%s/%d/select/version/2.0/mediaset/%s/vpid/%s/format/xml?cb=%s",
				ms.BaseURL, ver, mediaset, vpid, cb)

			body, err := ms.client.GetCtx(ctx, reqURL)
			if err != nil {
				continue
			}

			result, err := ms.parseResponse(body)
			if err != nil {
				if errors.Is(err, errGeoBlocked) {
					return nil, err
				}
				continue
			}

			if len(result.Video) > 0 {
				return result, nil
			}
		}
	}

	return nil, fmt.Errorf("no streams found for vpid %s", vpid)
}

func (ms *MediaSelector) Resolve(vpid string) (*StreamSet, error) {
	// MediaSelector has no per-call timeout in live code today (it relies
	// on the underlying bbc.Client default). Preserve that behaviour:
	// pass a background context, not a WithTimeout context. Adding an
	// arbitrary timeout here would be a silent behaviour change.
	return ms.ResolveCtx(context.Background(), vpid)
}

func (ms *MediaSelector) parseResponse(body []byte) (*StreamSet, error) {
	bodyStr := string(body)
	if strings.Contains(bodyStr, "geolocation") || strings.Contains(bodyStr, `id="notuk"`) {
		return nil, errGeoBlocked
	}
	if strings.Contains(bodyStr, "selectionunavailable") {
		return nil, fmt.Errorf("content unavailable")
	}

	var sel mediaSelectionXML
	if err := xml.Unmarshal(body, &sel); err != nil {
		return nil, fmt.Errorf("parse media selector XML: %w", err)
	}

	result := &StreamSet{}
	seen := map[string]bool{}

	for _, m := range sel.Media {
		if m.Kind == "captions" {
			for _, c := range m.Connections {
				if c.Protocol == "https" || result.SubtitleURL == "" {
					result.SubtitleURL = c.Href
				}
			}
			continue
		}

		if m.Kind != "video" {
			continue
		}

		for _, c := range m.Connections {
			if strings.Contains(c.Supplier, "vbidi") {
				continue
			}
			if c.TransferFormat != "dash" && c.TransferFormat != "hls" {
				continue
			}

			if c.Protocol != "https" {
				continue
			}

			dedupKey := fmt.Sprintf("%d-%d-%s-%s", m.Height, m.Bitrate, c.Supplier, c.TransferFormat)
			if seen[dedupKey] {
				continue
			}
			seen[dedupKey] = true

			result.Video = append(result.Video, VideoStream{
				URL:      c.Href,
				Height:   m.Height,
				Width:    m.Width,
				Bitrate:  m.Bitrate,
				Supplier: c.Supplier,
				Protocol: c.Protocol,
				Format:   c.TransferFormat,
			})
		}
	}

	sort.Slice(result.Video, func(i, j int) bool {
		if result.Video[i].Height != result.Video[j].Height {
			return result.Video[i].Height > result.Video[j].Height
		}
		return result.Video[i].Bitrate > result.Video[j].Bitrate
	})

	return result, nil
}
