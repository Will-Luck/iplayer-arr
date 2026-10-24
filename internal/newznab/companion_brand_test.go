package newznab

import (
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
)

// GitHub #58. iBL lists a companion programme such as "The Celebrity
// Traitors: Uncloaked" as its own brand next to the parent. A tvdbid
// search for the parent used to emit the companion's episodes renamed
// to the parent and stamped with the parent's tvdbid, so Sonarr took
// them as the parent's episodes.
//
// Fixtures under testdata/gh58 are real iBL and Skyhook responses
// captured on 2026-10-24 and trimmed; testdata/gh58/README.md says how.

const gh58Dir = "testdata/gh58"

func gh58Read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(gh58Dir, name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// gh58IBL serves /new-search by its q parameter and
// /programmes/<pid>/episodes by pid. Only page 1 exists: the trimmed
// fixtures set count to the elements they keep, so ListEpisodesCtx
// stops there. A brand missing from episodes 404s, which SearchCtx
// treats as a listing failure and skips. An unknown query gets an empty
// result list.
//
// Every fixture is read up front: the handler runs on the server's
// goroutine, where t.Fatalf must not be called.
func gh58IBL(t *testing.T, searches map[string]string, episodes []string) *httptest.Server {
	t.Helper()
	searchBodies := make(map[string][]byte, len(searches))
	for q, f := range searches {
		searchBodies[q] = gh58Read(t, f)
	}
	episodeBodies := make(map[string][]byte, len(episodes))
	for _, pid := range episodes {
		episodeBodies[pid] = gh58Read(t, "episodes_"+pid+".json")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/new-search" {
			if b, ok := searchBodies[r.URL.Query().Get("q")]; ok {
				w.Write(b)
				return
			}
			w.Write([]byte(`{"new_search":{"results":[]}}`))
			return
		}
		if pid, ok := strings.CutPrefix(r.URL.Path, "/programmes/"); ok {
			pid = strings.TrimSuffix(pid, "/episodes")
			if b, ok := episodeBodies[pid]; ok && r.URL.Query().Get("page") == "1" {
				w.Write(b)
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// gh58Skyhook points lookupTVDBShow at the skyhook_<tvdbid>.json
// fixtures. An unknown id 404s, so the handler falls back to its
// no-name path.
func gh58Skyhook(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := strings.CutPrefix(r.URL.Path, "/v1/tvdb/shows/en/")
		if !ok {
			http.NotFound(w, r)
			return
		}
		b, err := os.ReadFile(filepath.Join(gh58Dir, "skyhook_"+id+".json"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	old := skyhookBaseURL
	skyhookBaseURL = srv.URL
	t.Cleanup(func() { skyhookBaseURL = old })
}

type gh58Item struct {
	Title  string
	PID    string
	Size   int64
	TVDBID string // "" when the item has no tvdbid attr
}

// gh58Serve runs one Newznab request through the full handler and
// returns the RSS items.
func gh58Serve(t *testing.T, h *Handler, query string) []gh58Item {
	t.Helper()
	req := httptest.NewRequest("GET", "/newznab/api?"+query+"&apikey="+testStoreAPIKey, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body:\n%s", w.Code, w.Body.String())
	}
	return gh58ParseItems(t, w.Body.Bytes())
}

// gh58Search wires the fixture Skyhook and iBL servers into a handler
// and runs one request against it.
func gh58Search(t *testing.T, searches map[string]string, episodes []string, prober qualityProber, query string) []gh58Item {
	t.Helper()
	gh58Skyhook(t)
	srv := gh58IBL(t, searches, episodes)
	ibl := bbc.NewIBL(bbc.NewClient())
	ibl.BaseURL = srv.URL
	return gh58Serve(t, NewHandler(ibl, newTestStore(t), nil, prober), query)
}

func gh58ParseItems(t *testing.T, body []byte) []gh58Item {
	t.Helper()
	var rss struct {
		Channel struct {
			Items []struct {
				Title     string `xml:"title"`
				GUID      string `xml:"guid"`
				Enclosure struct {
					Length int64 `xml:"length,attr"`
				} `xml:"enclosure"`
				Attrs []struct {
					Name  string `xml:"name,attr"`
					Value string `xml:"value,attr"`
				} `xml:"attr"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(body, &rss); err != nil {
		t.Fatalf("parse RSS: %v\n%s", err, body)
	}
	var items []gh58Item
	for _, it := range rss.Channel.Items {
		u, err := url.Parse(it.GUID)
		if err != nil {
			t.Fatalf("parse guid %q: %v", it.GUID, err)
		}
		gi, err := DecodeGUID(u.Query().Get("id"))
		if err != nil {
			t.Fatalf("decode guid %q: %v", it.GUID, err)
		}
		item := gh58Item{Title: it.Title, PID: gi.PID, Size: it.Enclosure.Length}
		for _, a := range it.Attrs {
			if a.Name == "tvdbid" {
				item.TVDBID = a.Value
			}
		}
		items = append(items, item)
	}
	for _, it := range items {
		t.Logf("emitted %s  %-70s  %d bytes  tvdbid=%s", it.PID, it.Title, it.Size, it.TVDBID)
	}
	return items
}

// gh58BrandPIDs lists every episode PID in an episodes fixture.
func gh58BrandPIDs(t *testing.T, brand string) map[string]bool {
	t.Helper()
	var resp struct {
		ProgrammeEpisodes struct {
			Elements []struct {
				ID string `json:"id"`
			} `json:"elements"`
		} `json:"programme_episodes"`
	}
	if err := json.Unmarshal(gh58Read(t, "episodes_"+brand+".json"), &resp); err != nil {
		t.Fatal(err)
	}
	out := make(map[string]bool)
	for _, e := range resp.ProgrammeEpisodes.Elements {
		out[e.ID] = true
	}
	return out
}

func gh58AssertNone(t *testing.T, items []gh58Item, pids map[string]bool, what string) {
	t.Helper()
	for _, it := range items {
		if pids[it.PID] {
			t.Errorf("%s episode %s emitted as %q (%d bytes)", what, it.PID, it.Title, it.Size)
		}
	}
}

func gh58AssertTitle(t *testing.T, items []gh58Item, pid, title string) {
	t.Helper()
	for _, it := range items {
		if it.PID == pid && it.Title == title {
			return
		}
	}
	t.Errorf("no item for %s titled %q", pid, title)
}

var gh58CelebritySearch = map[string]string{
	"The Celebrity Traitors":            "search_The_Celebrity_Traitors.json",
	"The Celebrity Traitors: Uncloaked": "search_The_Celebrity_Traitors_colon_Uncloaked.json",
}

// The Celebrity Traitors and its Uncloaked companion, and The Traitors
// and its Uncloaked companion.
var gh58Brands = []string{"m002csng", "m002kksc", "p0db9b2t", "m001ty6w"}

// The reporter's search: Sonarr 4 sends tvdbid + season + ep and no q.
// m0030pxx is the parent's S02E02 (3633 s), m00328ls is Uncloaked's
// S02E02 (1620 s); at 1080p estimateSize makes those the 2.1 GiB and
// 965.6 MiB rows in the report.
func TestGH58_ParentEpisodeSearchSkipsCompanionBrand(t *testing.T) {
	prober := &mockProber{results: map[string][]int{
		"m0030pxx": {1080, 720},
		"m00328ls": {1080, 720},
	}}
	items := gh58Search(t, gh58CelebritySearch, gh58Brands, prober,
		"t=tvsearch&tvdbid=453660&season=2&ep=2")

	gh58AssertTitle(t, items, "m0030pxx",
		"The.Celebrity.Traitors.S02E02.Series.2.Episode.2.1080p.WEB-DL.AAC.H264-iParr")
	gh58AssertNone(t, items, gh58BrandPIDs(t, "m002kksc"), "Uncloaked")
}

func TestGH58_ParentSeasonSearchSkipsCompanionBrand(t *testing.T) {
	items := gh58Search(t, gh58CelebritySearch, gh58Brands, nil,
		"t=tvsearch&tvdbid=453660&season=2")

	parent := gh58BrandPIDs(t, "m002csng")
	got := 0
	for _, it := range items {
		if parent[it.PID] {
			got++
		}
	}
	if got == 0 {
		t.Error("season search returned no parent episodes")
	}
	gh58AssertNone(t, items, gh58BrandPIDs(t, "m002kksc"), "Uncloaked")
}

// Same shape, second programme: "Traitors Series 4: Episode 5" on the
// companion parses as S04E05, the same identity as the parent's.
func TestGH58_TraitorsSearchSkipsUncloaked(t *testing.T) {
	items := gh58Search(t, map[string]string{"The Traitors": "search_The_Traitors.json"},
		gh58Brands, nil, "t=tvsearch&tvdbid=420543&season=4&ep=5")

	gh58AssertTitle(t, items, "m002pkvf",
		"The.Traitors.S04E05.Series.4.Episode.5.720p.WEB-DL.AAC.H264-iParr")
	gh58AssertNone(t, items, gh58BrandPIDs(t, "m001ty6w"), "The Traitors: Uncloaked")
}

// The text search path shares writeResultsRSS.
func TestGH58_TextSearchSkipsCompanionBrand(t *testing.T) {
	items := gh58Search(t, gh58CelebritySearch, gh58Brands, nil,
		"t=search&q=The+Celebrity+Traitors")
	if len(items) == 0 {
		t.Fatal("text search returned nothing")
	}
	gh58AssertNone(t, items, gh58BrandPIDs(t, "m002kksc"), "Uncloaked")
}

// The parent brand is in the search hits but its episode listing fails
// (404 here; SearchCtx skips a brand whose listing errors). The parent
// still exists on iBL, so the companion must stay out. Only the search
// hit titles can tell: the expanded results hold nothing but the
// companion's episodes.
func TestGH58_ParentListingFailureStillSkipsCompanion(t *testing.T) {
	items := gh58Search(t, gh58CelebritySearch, []string{"m002kksc"}, nil,
		"t=tvsearch&tvdbid=453660&season=2&ep=2")
	gh58AssertNone(t, items, gh58BrandPIDs(t, "m002kksc"), "Uncloaked")
}

// The same listing failure on the t=search path. handleSearch must pass
// its own hit titles to writeResultsRSS; with nil titles the companion
// is the only thing iBL "returned" and the extension switches back on.
func TestGH58_TextSearchParentListingFailureStillSkipsCompanion(t *testing.T) {
	items := gh58Search(t, gh58CelebritySearch, []string{"m002kksc"}, nil,
		"t=search&q=The+Celebrity+Traitors")
	gh58AssertNone(t, items, gh58BrandPIDs(t, "m002kksc"), "Uncloaked")
}

// iBL does not always rank the parent first (for "Gladiators (2024)" it
// came second on 2026-10-24, behind "Election 2024"). Here the companion
// is the top hit and the parent's listing fails, so only a check over
// every hit title, not just the first, keeps the companion out. The search
// body is the real capture for the companion's own name, which iBL
// ranks that way round, served for the parent's name.
func TestGH58_ParentListingFailureCompanionRankedFirst(t *testing.T) {
	items := gh58Search(t,
		map[string]string{"The Celebrity Traitors": "search_The_Celebrity_Traitors_colon_Uncloaked.json"},
		[]string{"m002kksc"}, nil, "t=tvsearch&tvdbid=453660&season=2&ep=2")
	gh58AssertNone(t, items, gh58BrandPIDs(t, "m002kksc"), "Uncloaked")
}

// TVDB 468742 is "The Celebrity Traitors: Uncloaked". Tracking it as
// its own series must keep working and must not pull in the parent.
func TestGH58_CompanionFindableUnderOwnName(t *testing.T) {
	items := gh58Search(t, gh58CelebritySearch, gh58Brands, nil,
		"t=tvsearch&tvdbid=468742&season=2&ep=2")

	gh58AssertTitle(t, items, "m00328ls",
		"The.Celebrity.Traitors.Uncloaked.S02E02.Series.2.Episode.2.720p.WEB-DL.AAC.H264-iParr")
	gh58AssertNone(t, items, gh58BrandPIDs(t, "m002csng"), "parent")
}

// Synthetic: the wanted title has its own colon and iBL also carries a
// colon extension of it. The exact title wins, the extension is dropped.
func TestGH58_ColonTitleWithExtensionSibling(t *testing.T) {
	const payload = `{"new_search":{"results":[
		{"id":"ep_exact","type":"episode","title":"David Fuller: Monster in the Morgue","subtitle":"Series 1: Episode 1","release_date":"2023-11-01"},
		{"id":"ep_extra","type":"episode","title":"David Fuller: Monster in the Morgue: Extra","subtitle":"Series 1: Episode 1","release_date":"2023-11-01"}
	]}}`
	h := newHandlerWithBBC(t, payload)
	items := gh58Serve(t, h, "t=tvsearch&q=David+Fuller%3A+Monster+In+The+Morgue&season=1&ep=1")

	gh58AssertTitle(t, items, "ep_exact",
		"David.Fuller.Monster.in.the.Morgue.S01E01.Series.1.Episode.1.720p.WEB-DL.AAC.H264-iParr")
	gh58AssertNone(t, items, map[string]bool{"ep_extra": true}, "colon extension")
}

// The case the subtitle extension exists for: iBL has only "Talking Tom
// Heroes: Suddenly Super" and no brand titled exactly "Talking Tom
// Heroes", so the extension still matches and the title uses the
// Sonarr name.
func TestGH58_SubtitleOnlyBrandStillMatches(t *testing.T) {
	items := gh58Search(t, map[string]string{"Talking Tom Heroes": "search_Talking_Tom_Heroes.json"},
		[]string{"m002lgjh"}, nil, "t=tvsearch&tvdbid=367318&season=1&ep=40")

	gh58AssertTitle(t, items, "m002rw0y",
		"Talking.Tom.Heroes.S01E40.Suddenly.Stuck.720p.WEB-DL.AAC.H264-iParr")
}
