package newznab

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
)

// fakeBBCServer returns an httptest.Server that responds to BBC iBL Search
// (/new-search) with the supplied JSON. Used by handleTVSearch tests.
func fakeBBCSearchServer(t *testing.T, payload string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newHandlerWithBBC builds a Handler whose IBL is pointed at a fake BBC
// server. Used by handleTVSearch tests.
func newHandlerWithBBC(t *testing.T, payload string) *Handler {
	t.Helper()
	srv := fakeBBCSearchServer(t, payload)
	ibl := bbc.NewIBL(bbc.NewClient())
	ibl.BaseURL = srv.URL
	return NewHandler(ibl, nil, nil)
}

const eastendersOneEpisodePayload = `{
	"new_search": {
		"results": [
			{"id": "m002ttg5", "type": "episode", "title": "EastEnders", "subtitle": "06/04/2026", "release_date": "6 Apr 2026", "parent_position": 7307}
		]
	}
}`

func TestCapsEndpoint(t *testing.T) {
	h := NewHandler(nil, nil, nil)
	req := httptest.NewRequest("GET", "/newznab/api?t=caps", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `<searching>`) {
		t.Error("missing <searching> in caps")
	}
	if !strings.Contains(body, `supportedParams="q,season,ep,tvdbid"`) {
		t.Error("missing tvsearch supportedParams")
	}
	if !strings.Contains(body, `id="5000"`) {
		t.Error("missing TV category 5000")
	}

	var caps struct{}
	if err := xml.Unmarshal(w.Body.Bytes(), &caps); err != nil {
		t.Errorf("invalid XML: %v", err)
	}
}

// itemTitles extracts <title> values from a Newznab RSS body, skipping the
// channel title.
func itemTitles(body string) []string {
	var doc struct {
		Channel struct {
			Items []struct {
				Title string `xml:"title"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal([]byte(body), &doc); err != nil {
		return nil
	}
	titles := make([]string, 0, len(doc.Channel.Items))
	for _, it := range doc.Channel.Items {
		titles = append(titles, it.Title)
	}
	return titles
}

func TestHandleTVSearchDailyMatchByDate(t *testing.T) {
	// Sonarr daily-series search format: season=YYYY, ep=MM/DD.
	// EastEnders has no S/E numbering on iPlayer (subtitle is the date,
	// parent_position is the cumulative counter). The handler must
	// recognise the year+date query and match by air date instead of by
	// integer season/episode.
	h := newHandlerWithBBC(t, eastendersOneEpisodePayload)
	req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch&q=eastenders&season=2026&ep=04%2F06", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	titles := itemTitles(w.Body.String())
	if len(titles) == 0 {
		t.Fatalf("expected at least one item, got empty body:\n%s", w.Body.String())
	}
	for _, title := range titles {
		if !strings.Contains(title, "EastEnders.2026.04.06") {
			t.Errorf("title = %q, want it to contain %q", title, "EastEnders.2026.04.06")
		}
		if strings.Contains(title, "S01E7307") {
			t.Errorf("title = %q must not use S01E<position> for daily shows", title)
		}
	}
}

func TestHandleTVSearchDailyMismatchByDate(t *testing.T) {
	// Wrong date should return zero items.
	h := newHandlerWithBBC(t, eastendersOneEpisodePayload)
	req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch&q=eastenders&season=2026&ep=01%2F01", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	titles := itemTitles(w.Body.String())
	if len(titles) != 0 {
		t.Errorf("expected zero items for mismatched date, got %d: %v", len(titles), titles)
	}
}

func TestHandleTVSearchStandardSEStillWorks(t *testing.T) {
	// Doctor Who S1E3 — proper S/E numbering must continue to filter by
	// integer season/episode and produce a Tier 1 title.
	payload := `{
		"new_search": {
			"results": [
				{"id": "b039d07m", "type": "episode", "title": "Doctor Who", "subtitle": "Series 1: 3. The Unquiet Dead", "release_date": "2005-04-09", "parent_position": 3}
			]
		}
	}`
	h := newHandlerWithBBC(t, payload)
	req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch&q=doctor+who&season=1&ep=3", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	titles := itemTitles(w.Body.String())
	if len(titles) == 0 {
		t.Fatalf("expected items, got empty body:\n%s", w.Body.String())
	}
	for _, title := range titles {
		if !strings.Contains(title, "S01E03") {
			t.Errorf("title = %q, want S01E03", title)
		}
	}
}
