package bbc

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"sort"
	"strings"
)

const defaultMediaSelectorBase = "https://open.live.bbc.co.uk/mediaselector"

var errGeoBlocked = errors.New("geo-blocked: BBC iPlayer is only available in the UK")

// ErrMediaSelectorUnavailable matches a ResolveCtx failure in which no
// attempt got a mediaSelection document back: every request failed
// (transport error, timeout, HTTP error) or answered with something else
// (the BBC's HTML error page, an empty body). The BBC could not answer,
// which says nothing about whether the programme has streams. Gitea #94.
var ErrMediaSelectorUnavailable = errors.New("mediaselector gave no answer")

// errSelectionUnavailable is mediaselector's own "nothing for this vpid"
// answer: a real answer about the programme.
var errSelectionUnavailable = errors.New("content unavailable")

// isGeoBlockedBody reports whether a mediaselector body indicates a geo-block.
// Single source of truth for the markers, shared by parseResponse and CheckGeo.
func isGeoBlockedBody(body []byte) bool {
	s := string(body)
	return strings.Contains(s, "geolocation") || strings.Contains(s, `id="notuk"`)
}

// MediaSelector resolves a VPID to actual HLS stream URLs via BBC's
// open.live.bbc.co.uk mediaselector endpoint.
type MediaSelector struct {
	client   *Client
	BaseURL  string
	lookupIP func(ctx context.Context, host string) ([]net.IP, error)
}

// NewMediaSelector creates a MediaSelector backed by the given client.
func NewMediaSelector(client *Client) *MediaSelector {
	return &MediaSelector{
		client:  client,
		BaseURL: defaultMediaSelectorBase,
		lookupIP: func(ctx context.Context, host string) ([]net.IP, error) {
			return net.DefaultResolver.LookupIP(ctx, "ip4", host)
		},
	}
}

// StreamSet holds the resolved video streams and the captions URLs.
type StreamSet struct {
	Video []VideoStream
	// SubtitleURLs lists every captions URL mediaselector offered, the
	// https ones first, each group in mediaselector (supplier priority)
	// order, so a refusing supplier can be skipped. Gitea #91.
	SubtitleURLs []string
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

// ResolveCtx queries BBC mediaselector for the given VPID and returns a
// StreamSet. It tries v6 "iptv-all" first, then falls back through v5 and
// the "pc" mediaset. When no attempt got a mediaSelection document back
// the error matches ErrMediaSelectorUnavailable; when the BBC answered at
// least once but listed no usable video it does not. Gitea #94.
func (ms *MediaSelector) ResolveCtx(ctx context.Context, vpid string) (*StreamSet, error) {
	mediasets := []string{"iptv-all", "pc"}
	versions := []int{6, 5}

	answered := false
	var lastErr error
	for _, mediaset := range mediasets {
		for _, ver := range versions {
			cb := fmt.Sprintf("%05d", rand.Intn(100000))
			reqURL := fmt.Sprintf("%s/%d/select/version/2.0/mediaset/%s/vpid/%s/format/xml?cb=%s",
				ms.BaseURL, ver, mediaset, vpid, cb)

			body, err := ms.client.GetCtx(ctx, reqURL)
			if err != nil {
				// The mediaselector answers "no streams" as a 404 or
				// 410; the client drops the body, so the status is the
				// answer. 429, other 4xx, 5xx and transport errors stay
				// "no answer".
				var se *HTTPStatusError
				if errors.As(err, &se) && (se.Status == http.StatusNotFound || se.Status == http.StatusGone) {
					answered = true
				}
				lastErr = err
				continue
			}

			result, err := ms.parseResponse(body)
			if err != nil {
				if errors.Is(err, errGeoBlocked) {
					return nil, err
				}
				if errors.Is(err, errSelectionUnavailable) {
					answered = true
				}
				lastErr = err
				continue
			}
			answered = true

			if len(result.Video) > 0 {
				return result, nil
			}
		}
	}

	if !answered {
		return nil, fmt.Errorf("%w for vpid %s: %w", ErrMediaSelectorUnavailable, vpid, lastErr)
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
	if isGeoBlockedBody(body) {
		return nil, errGeoBlocked
	}
	bodyStr := string(body)
	if strings.Contains(bodyStr, "selectionunavailable") {
		return nil, errSelectionUnavailable
	}

	var sel mediaSelectionXML
	if err := xml.Unmarshal(body, &sel); err != nil {
		return nil, fmt.Errorf("parse media selector XML: %w", err)
	}

	result := &StreamSet{}
	seen := map[string]bool{}
	var httpsSubs, otherSubs []string
	seenSub := map[string]bool{}

	for _, m := range sel.Media {
		if m.Kind == "captions" {
			for _, c := range m.Connections {
				if c.Href == "" || seenSub[c.Href] {
					continue
				}
				seenSub[c.Href] = true
				if c.Protocol == "https" {
					httpsSubs = append(httpsSubs, c.Href)
				} else {
					otherSubs = append(otherSubs, c.Href)
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

	result.SubtitleURLs = append(httpsSubs, otherSubs...)

	// Stable, so streams of equal height and bitrate keep mediaselector's
	// order and the downloader's "first HLS entry" is the first supplier
	// the BBC listed. sort.Slice only keeps it for 12 or fewer entries.
	sort.SliceStable(result.Video, func(i, j int) bool {
		if result.Video[i].Height != result.Video[j].Height {
			return result.Video[i].Height > result.Video[j].Height
		}
		return result.Video[i].Bitrate > result.Video[j].Bitrate
	})

	return result, nil
}
