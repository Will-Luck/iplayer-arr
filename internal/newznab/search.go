package newznab

import (
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

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	if h.ibl == nil {
		writeEmptyRSS(w)
		return
	}

	q := r.URL.Query().Get("q")
	if q == "" {
		q = "BBC" // default query for RSS feed and Sonarr test
	}

	results, err := h.ibl.Search(q, 1)
	if err != nil {
		writeEmptyRSS(w)
		return
	}

	h.writeResultsRSS(w, r, results, 0, 0)
}

func (h *Handler) handleTVSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	tvdbid := r.URL.Query().Get("tvdbid")
	seasonStr := r.URL.Query().Get("season")
	epStr := r.URL.Query().Get("ep")

	log.Printf("[tvsearch] q=%q tvdbid=%q season=%q ep=%q", q, tvdbid, seasonStr, epStr)

	if q == "" && tvdbid != "" {
		// Try stored mapping first
		if h.store != nil {
			mapping, _ := h.store.GetSeriesMapping(tvdbid)
			if mapping != nil {
				q = mapping.ShowName
			}
		}
		// Fall back to Skyhook (Sonarr's TVDB lookup service)
		if q == "" {
			q = lookupTVDBTitle(tvdbid)
			if q != "" && h.store != nil {
				h.store.PutSeriesMapping(&store.SeriesMapping{TVDBId: tvdbid, ShowName: q})
			}
		}
	}

	if h.ibl == nil {
		writeEmptyRSS(w)
		return
	}
	if q == "" {
		q = "BBC"
	}

	results, err := h.ibl.Search(q, 1)
	if err != nil {
		writeEmptyRSS(w)
		return
	}

	season, _ := strconv.Atoi(seasonStr)
	ep, _ := strconv.Atoi(epStr)

	h.writeResultsRSS(w, r, results, season, ep)
}

func (h *Handler) writeResultsRSS(w http.ResponseWriter, r *http.Request, results []bbc.IBLResult, filterSeason, filterEp int) {
	var items []string

	for _, res := range results {
		prog := iblResultToProgramme(res)

		if filterSeason > 0 && prog.Series != filterSeason {
			continue
		}
		if filterEp > 0 && prog.EpisodeNum != filterEp {
			continue
		}

		var override *store.ShowOverride
		if h.store != nil {
			override, _ = h.store.GetOverride(prog.Name)
		}

		qualities := []string{"1080p", "720p", "540p"}
		if len(prog.Qualities) > 0 {
			qualities = nil
			for _, q := range prog.Qualities {
				qualities = append(qualities, q.Tag)
			}
		}

		for _, qual := range qualities {
			title, tier := GenerateTitle(prog, qual, override)
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

			pubDate := time.Now().Format(time.RFC1123Z)
			if res.AirDate != "" {
				if t, err := time.Parse("2006-01-02", res.AirDate); err == nil {
					pubDate = t.Format(time.RFC1123Z)
				}
			}

			item := fmt.Sprintf(`    <item>
      <title>%s</title>
      <guid isPermaLink="true">%s/newznab/api?t=get&amp;id=%s</guid>
      <link>%s/newznab/api?t=get&amp;id=%s</link>
      <pubDate>%s</pubDate>
      <enclosure url="%s/newznab/api?t=get&amp;id=%s" length="%d" type="application/x-nzb" />
      <newznab:attr name="category" value="%s" />
      <newznab:attr name="size" value="%d" />`,
				html.EscapeString(title), baseURL(r), guid, baseURL(r), guid, pubDate,
				baseURL(r), guid, size, cat, size)

			if tier == store.TierManual {
				item += `
      <newznab:attr name="iparr:manual" value="true" />`
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

func iblResultToProgramme(r bbc.IBLResult) *store.Programme {
	return &store.Programme{
		PID:        r.PID,
		Name:       r.Title,
		Episode:    r.Subtitle,
		Series:     r.Series,
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

func lookupTVDBTitle(tvdbid string) string {
	resp, err := http.Get("https://skyhook.sonarr.tv/v1/tvdb/shows/en/" + tvdbid)
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			resp.Body.Close()
		}
		return ""
	}
	defer resp.Body.Close()

	var show struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&show); err != nil {
		return ""
	}
	log.Printf("[tvsearch] resolved TVDB %s -> %q", tvdbid, show.Title)
	return show.Title
}
