package api

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
)

// DiagSonarrReport is the JSON returned by /api/diag/sonarr-handshake.
//
// New in v1.5.6 to catch issue-#40-class regressions before a release.
// The endpoint synthesises a Sonarr round-trip in-process so operators
// can verify the full integration chain (tvsearch -> RSS parse ->
// enclosure embed -> grab) with a single curl, without needing a live
// Sonarr instance to drive it. Auth-gated like every other /newznab/
// op that returns content.
//
// Example use:
//
//	curl -s "http://localhost:8191/api/diag/sonarr-handshake?apikey=$KEY" | jq .verdict
//	# "pass" or "fail"
//	# Inspect .checks_failed for any failure reasons.
//
// A `pass` verdict requires every per-component check to be `ok: true`.
// A `fail` verdict lists the failed checks in `checks_failed` and
// keeps the per-component detail so the operator can drill in.
type DiagSonarrReport struct {
	Verdict       string              `json:"verdict"`
	ChecksFailed  []string            `json:"checks_failed"`
	Newznab       diagNewznabCheck    `json:"newznab"`
	FeedAPIKey    diagFeedAPIKeyCheck `json:"feed_apikey"`
	GrabRoundtrip diagGrabCheck       `json:"grab_roundtrip"`
	FFmpeg        diagFFmpegCheck     `json:"ffmpeg"`
	Geo           diagGeoCheck        `json:"geo"`
	Store         diagStoreCheck      `json:"store"`
}

type diagNewznabCheck struct {
	OK            bool   `json:"ok"`
	StatusCode    int    `json:"status_code"`
	ItemsReturned int    `json:"items_returned"`
	Error         string `json:"error,omitempty"`
}

type diagFeedAPIKeyCheck struct {
	OK              bool `json:"ok"`
	AllURLsEmbedKey bool `json:"all_urls_embed_key"`
	CheckedURLs     int  `json:"checked_urls"`
	MissingURLs     int  `json:"missing_urls"`
}

type diagGrabCheck struct {
	OK          bool   `json:"ok"`
	HTTPCode    int    `json:"http_code"`
	ContentType string `json:"content_type"`
	BodySize    int    `json:"body_size"`
	Error       string `json:"error,omitempty"`
}

type diagFFmpegCheck struct {
	OK      bool   `json:"ok"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

type diagGeoCheck struct {
	OK        bool   `json:"ok"`
	CheckedAt string `json:"checked_at,omitempty"`
}

type diagStoreCheck struct {
	OK           bool `json:"ok"`
	APIKeySeeded bool `json:"api_key_seeded"`
}

// rssEnvelope is the minimal shape needed to count tvsearch items and
// pull the first enclosure URL out of writeResultsRSS' output. The
// production newznab handler emits a richer feed (categories, sizes,
// pubDates, tvdbid attrs); diag only needs to confirm the URLs carry
// `apikey=` and that one of them grab-resolves to an NZB body.
type rssEnvelope struct {
	XMLName xml.Name `xml:"rss"`
	Channel struct {
		Items []struct {
			GUID      string `xml:"guid"`
			Link      string `xml:"link"`
			Enclosure struct {
				URL string `xml:"url,attr"`
			} `xml:"enclosure"`
		} `xml:"item"`
	} `xml:"channel"`
}

// handleDiagSonarrHandshake runs a synthetic Sonarr round-trip in
// process via httptest.NewRecorder. Steps:
//
//  1. /newznab/api?t=tvsearch&apikey=...&limit=3 (the same call shape
//     Sonarr's "Test Indexer" button uses).
//  2. Parse the returned RSS, count items, extract the first
//     enclosure URL.
//  3. Verify every `<guid>` / `<link>` / `<enclosure url>` carries
//     `apikey=` — this is the exact check that would have caught
//     the v1.5.5 regression that took out issue #40.
//  4. Hit the first enclosure URL as t=get and confirm the response
//     is an `application/x-nzb` body. Sonarr follows feed URLs
//     verbatim so this exercises the exact code path it would.
//  5. Sanity-check ffmpeg/geo/store as supporting context.
//
// Returns a DiagSonarrReport with `verdict: "pass"` only when every
// check is OK. Auth-gated by the standard h.authenticate(r) so the
// endpoint can't be used as an unauthenticated reconnaissance tool.
func (h *Handler) handleDiagSonarrHandshake(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid apikey"})
		return
	}

	report := DiagSonarrReport{}

	// --- store check ---
	apiKey, _ := h.store.GetConfig("api_key")
	report.Store.APIKeySeeded = apiKey != ""
	report.Store.OK = apiKey != ""

	// --- ffmpeg check ---
	if out, err := exec.Command("ffmpeg", "-version").CombinedOutput(); err == nil {
		report.FFmpeg.Version = parseFFmpegVersion(string(out))
		report.FFmpeg.OK = report.FFmpeg.Version != ""
	} else {
		report.FFmpeg.Error = err.Error()
	}

	// --- geo check ---
	if h.status != nil {
		_, geoOK, geoAt := h.status.Snapshot()
		report.Geo.OK = geoOK
		report.Geo.CheckedAt = geoAt
	}

	// --- newznab round-trip ---
	if h.newznabHandler != nil && apiKey != "" {
		h.runSyntheticHandshake(r, apiKey, &report)
	} else if h.newznabHandler == nil {
		report.Newznab.Error = "newznab handler not wired (test harness or main.go SetNewznabHandler missed)"
	}

	// --- verdict aggregation ---
	if !report.Store.OK {
		report.ChecksFailed = append(report.ChecksFailed, "store: api_key not seeded")
	}
	if !report.FFmpeg.OK {
		report.ChecksFailed = append(report.ChecksFailed, "ffmpeg: not detected")
	}
	if !report.Geo.OK {
		report.ChecksFailed = append(report.ChecksFailed, "geo: not OK")
	}
	if !report.Newznab.OK {
		report.ChecksFailed = append(report.ChecksFailed, "newznab: tvsearch did not return a usable feed")
	}
	if !report.FeedAPIKey.OK {
		report.ChecksFailed = append(report.ChecksFailed, "feed_apikey: missing &apikey= in one or more feed URLs")
	}
	if !report.GrabRoundtrip.OK {
		report.ChecksFailed = append(report.ChecksFailed, "grab_roundtrip: enclosure URL did not return an NZB")
	}

	if len(report.ChecksFailed) == 0 {
		report.Verdict = "pass"
	} else {
		report.Verdict = "fail"
	}
	writeJSON(w, http.StatusOK, report)
}

func (h *Handler) runSyntheticHandshake(r *http.Request, apiKey string, report *DiagSonarrReport) {
	// 1. tvsearch
	tvReq := httptest.NewRequest("GET", "/newznab/api?t=tvsearch&apikey="+apiKey+"&limit=3", nil)
	tvReq.Host = r.Host
	tvW := httptest.NewRecorder()
	h.newznabHandler.ServeHTTP(tvW, tvReq)
	report.Newznab.StatusCode = tvW.Code

	if tvW.Code != http.StatusOK {
		report.Newznab.Error = strings.TrimSpace(tvW.Body.String())
		return
	}

	var feed rssEnvelope
	if err := xml.Unmarshal(tvW.Body.Bytes(), &feed); err != nil {
		report.Newznab.Error = "parse rss: " + err.Error()
		return
	}
	report.Newznab.ItemsReturned = len(feed.Channel.Items)
	report.Newznab.OK = true

	// 2. feed apikey embed check (the precise check that catches issue #40 shape)
	checked := 0
	missing := 0
	var firstEnclosure string
	for _, item := range feed.Channel.Items {
		for _, url := range []string{item.GUID, item.Link, item.Enclosure.URL} {
			if url == "" {
				continue
			}
			checked++
			if !strings.Contains(url, "apikey=") {
				missing++
			}
		}
		if firstEnclosure == "" {
			firstEnclosure = item.Enclosure.URL
		}
	}
	report.FeedAPIKey.CheckedURLs = checked
	report.FeedAPIKey.MissingURLs = missing
	report.FeedAPIKey.AllURLsEmbedKey = checked > 0 && missing == 0
	report.FeedAPIKey.OK = report.FeedAPIKey.AllURLsEmbedKey

	// 3. grab roundtrip (follow the feed URL exactly as Sonarr would)
	if firstEnclosure == "" {
		report.GrabRoundtrip.Error = "no enclosure URLs in tvsearch response (catalogue empty?)"
		return
	}
	grabPath := stripSchemeHost(firstEnclosure)
	grabReq := httptest.NewRequest("GET", grabPath, nil)
	grabReq.Host = r.Host
	grabW := httptest.NewRecorder()
	h.newznabHandler.ServeHTTP(grabW, grabReq)
	report.GrabRoundtrip.HTTPCode = grabW.Code
	report.GrabRoundtrip.ContentType = grabW.Header().Get("Content-Type")
	body, _ := io.ReadAll(grabW.Body)
	report.GrabRoundtrip.BodySize = len(body)
	report.GrabRoundtrip.OK = grabW.Code == http.StatusOK &&
		strings.Contains(report.GrabRoundtrip.ContentType, "nzb")
	if !report.GrabRoundtrip.OK && grabW.Code != http.StatusOK {
		report.GrabRoundtrip.Error = strings.TrimSpace(string(body))
	}
}

// parseFFmpegVersion pulls the version token out of `ffmpeg -version`
// output. The first line is shaped:
//
//	ffmpeg version 8.0.1 Copyright (c) 2000-2025 the FFmpeg developers
//
// returns "8.0.1" (or "" if the line shape changed).
func parseFFmpegVersion(out string) string {
	first := strings.SplitN(out, "\n", 2)[0]
	const marker = "version "
	i := strings.Index(first, marker)
	if i < 0 {
		return ""
	}
	rest := first[i+len(marker):]
	if j := strings.Index(rest, " "); j > 0 {
		return rest[:j]
	}
	return rest
}

// stripSchemeHost reduces "http://host:port/newznab/api?...&apikey=X"
// to "/newznab/api?...&apikey=X" so httptest.NewRequest accepts it.
// Tolerant of bare path inputs.
func stripSchemeHost(absoluteURL string) string {
	if i := strings.Index(absoluteURL, "://"); i >= 0 {
		rest := absoluteURL[i+3:]
		if j := strings.Index(rest, "/"); j >= 0 {
			return rest[j:]
		}
	}
	return absoluteURL
}
