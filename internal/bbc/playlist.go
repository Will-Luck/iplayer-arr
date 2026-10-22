package bbc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
)

// ErrNotYetAvailable signals that a programme cannot be downloaded yet but
// may be later: a freshly-aired episode whose VOD asset BBC has not
// published. Callers treat this as a transient "try again later" state
// distinct from a genuine fetch/parse failure: the indexer skips
// advertising it and the downloader defers rather than permanently
// failing + blocklisting. Issue #44.
//
// From iBL (GitHub #57) it covers: "episodes": [] (iBL gives the same body
// for an unpublished, expired, unknown, radio or brand PID), an episode
// that is airing live, and one whose on-demand versions all start later.
var ErrNotYetAvailable = errors.New("not yet available on iplayer")

// errInvalidPID rejects anything that is not a BBC PID before it reaches
// a URL. iBL batches comma-separated PIDs, so an unchecked value could
// resolve a different episode. GitHub #57.
var errInvalidPID = errors.New("not a BBC programme PID")

// errUnexpectedIBLShape is a response iBL should never give for an
// episode PID. It is deliberately not ErrNotYetAvailable, so a changed
// API fails loudly instead of quietly hiding every item from the feed.
var errUnexpectedIBLShape = errors.New("unexpected iBL episode response (BBC may have changed the API)")

var pidPattern = regexp.MustCompile(`^[0-9a-z]{1,32}$`)

// PlaylistResolver turns an episode PID into the version id (VPID) that
// mediaselector takes. It used to read www.bbc.co.uk/programmes/{pid}/
// playlist.json; since 2026-10-21 BBC redirects every /programmes URL to
// an error page, so it reads iBL's episode endpoint, the same API iPlayer
// and our search use. GitHub #57.
type PlaylistResolver struct {
	client  *Client
	BaseURL string
	// now is the clock the availability check reads; nil means time.Now.
	now func() time.Time
}

func NewPlaylistResolver(client *Client) *PlaylistResolver {
	return &PlaylistResolver{client: client, BaseURL: defaultIBLBase}
}

type PlaylistInfo struct {
	VPID      string
	Title     string
	Summary   string
	Duration  int
	Thumbnail string
	Type      string // "tv" or "radio"
	Versions  []VersionInfo
	// AccessibilityOnly is set when iBL lists only audio-described or
	// signed versions, so VPID is one of those. Its heights describe that
	// version alone (audio-described is 396p only), so the quality prober
	// does not cache them. GitHub #57.
	AccessibilityOnly bool
}

type VersionInfo struct {
	PID  string
	VPID string
	Type string // "original", "audiodescribed", "signed", etc.
}

type iblEpisodeResponse struct {
	// A pointer, so a missing "episodes" key is told apart from an empty list.
	Episodes *[]iblEpisode `json:"episodes"`
}

type iblEpisode struct {
	ID       string `json:"id"`
	Live     bool   `json:"live"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	Synopses struct {
		Small string `json:"small"`
	} `json:"synopses"`
	Images struct {
		Standard string `json:"standard"`
	} `json:"images"`
	// A pointer for the same reason as Episodes: iBL sends "versions": []
	// for an episode with nothing playable yet, and never omits the key.
	Versions *[]iblVersion `json:"versions"`
}

type iblVersion struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Duration struct {
		Value string `json:"value"`
	} `json:"duration"`
	Availability struct {
		Start string `json:"start"`
	} `json:"availability"`
}

func (r *PlaylistResolver) ResolveCtx(ctx context.Context, pid string) (*PlaylistInfo, error) {
	if !pidPattern.MatchString(pid) {
		return nil, fmt.Errorf("%q: %w", pid, errInvalidPID)
	}

	body, err := r.client.GetJSONCtx(ctx, r.BaseURL+"/episodes/"+pid)
	if err != nil {
		return nil, fmt.Errorf("fetch episode: %w", err)
	}

	var resp iblEpisodeResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse episode: %w", err)
	}
	if resp.Episodes == nil {
		return nil, fmt.Errorf("pid %s: no \"episodes\" field: %w", pid, errUnexpectedIBLShape)
	}
	if len(*resp.Episodes) == 0 {
		return nil, fmt.Errorf("pid %s is not listed on BBC iPlayer (not published yet, expired, or not a TV episode PID): %w", pid, ErrNotYetAvailable)
	}
	ep := (*resp.Episodes)[0]
	if ep.ID != pid {
		return nil, fmt.Errorf("asked for pid %s, got episode %q: %w", pid, ep.ID, errUnexpectedIBLShape)
	}
	// Checked before the versions: a live pop-up channel lists a 24-hour
	// webcast, and a programme on air lists only its simulcast.
	if ep.Live {
		return nil, fmt.Errorf("pid %s is airing live; BBC has not published the on-demand version yet: %w", pid, ErrNotYetAvailable)
	}

	if ep.Versions == nil {
		return nil, fmt.Errorf("pid %s: no \"versions\" field: %w", pid, errUnexpectedIBLShape)
	}
	v, accessibilityOnly, err := r.pickVersion(pid, *ep.Versions)
	if err != nil {
		return nil, err
	}
	if v.ID == "" {
		return nil, fmt.Errorf("pid %s: version %q has no id: %w", pid, v.Kind, errUnexpectedIBLShape)
	}

	info := &PlaylistInfo{
		VPID:              v.ID,
		Title:             ep.Title,
		Summary:           ep.Synopses.Small,
		Duration:          parseISODuration(v.Duration.Value),
		Thumbnail:         strings.ReplaceAll(ep.Images.Standard, "{recipe}", "960x540"),
		Type:              "tv", // iBL lists TV only
		AccessibilityOnly: accessibilityOnly,
	}
	if ep.Subtitle != "" {
		info.Title += ": " + ep.Subtitle
	}
	for _, ver := range *ep.Versions {
		info.Versions = append(info.Versions, VersionInfo{PID: ver.ID, VPID: ver.ID, Type: normaliseVersionType(ver.Kind)})
	}

	log.Printf("resolved pid=%s vpid=%s kind=%s duration=%ds via iBL", pid, v.ID, v.Kind, info.Duration)
	return info, nil
}

// pickVersion returns the first version, in iBL order (the order iPlayer
// plays them), that is neither an accessibility version nor a live one
// and has already started. Failing that, an audio-described or signed
// version that has started, reported as accessibility-only: some
// programmes (the "Signed:" brands) only exist that way. GitHub #57.
func (r *PlaylistResolver) pickVersion(pid string, versions []iblVersion) (iblVersion, bool, error) {
	now := time.Now
	if r.now != nil {
		now = r.now
	}
	t := now()

	var firstStart time.Time
	accessible := -1
	for i, v := range versions {
		started := true
		if start, err := time.Parse(time.RFC3339, v.Availability.Start); err == nil && start.After(t) {
			started = false
		}
		switch normaliseVersionType(v.Kind) {
		case "simulcast", "webcast":
			continue
		case "audiodescribed", "signed", "combined":
			if started && accessible < 0 {
				accessible = i
			}
			continue
		}
		if !started {
			start, _ := time.Parse(time.RFC3339, v.Availability.Start)
			if firstStart.IsZero() || start.Before(firstStart) {
				firstStart = start
			}
			continue
		}
		return v, false, nil
	}

	if !firstStart.IsZero() {
		return iblVersion{}, false, fmt.Errorf("pid %s is available on iPlayer from %s: %w",
			pid, firstStart.UTC().Format(time.RFC3339), ErrNotYetAvailable)
	}
	if accessible >= 0 {
		return versions[accessible], true, nil
	}
	return iblVersion{}, false, fmt.Errorf("pid %s has no on-demand version yet: %w", pid, ErrNotYetAvailable)
}

func (r *PlaylistResolver) Resolve(pid string) (*PlaylistInfo, error) {
	// Preserve the live-code 60s per-call cap that the old implementation
	// had via client.GetWithTimeout(url, 60*time.Second). Existing call
	// sites that don't pass a context continue to see the same ceiling.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return r.ResolveCtx(ctx, pid)
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
