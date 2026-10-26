package newznab

import (
	"net/http/httptest"
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
)

// A leading "The" is the show's own word when the name is TheTVDB's.
// Sonarr's title search drops it from some queries (Gitea #97), which
// exactNameMatch folds, but TheTVDB's "Missing (2012)" is no "The
// Missing". With the episode list unavailable nothing else checks the
// pair, and BBC's own numbers go out stamped with the searched series'
// tvdbid.
func TestArticleIsSignificantInATVDBNamedSearch(t *testing.T) {
	missing := bbc.IBLResult{PID: "b0missing", BrandPID: "b0missing", Title: "The Missing", Subtitle: "Series 1: Episode 3", Series: 1, EpisodeNum: 3, AirDate: "2014-11-14"}
	results := []bbc.IBLResult{missing}
	run := func(eps EpisodeSource, tvdbid string) []gh58Item {
		h := NewHandler(nil, newTestStore(t), nil, nil)
		h.SetEpisodeSource(eps)
		req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch", nil)
		w := httptest.NewRecorder()
		h.writeResultsRSS(w, req, results, []string{"The Missing"}, 1, 3, "", "Missing (2012)", 2012, tvdbid, "Missing (2012)", false, 0)
		return gh58ParseItems(t, w.Body.Bytes())
	}
	down := failingEpisodes{fail: "1234", next: fixedEpisodes{}}
	if items := run(down, "1234"); len(items) != 0 {
		t.Errorf("tvdbid search for Missing (2012) returned %q for iPlayer's The Missing; want none", items[0].Title)
	}
	// Sonarr's q is not TheTVDB's name: it drops the article, and
	// still finds the show.
	h := NewHandler(nil, newTestStore(t), nil, nil)
	req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch", nil)
	w := httptest.NewRecorder()
	h.writeResultsRSS(w, req, results, []string{"The Missing"}, 1, 3, "", "Missing", 0, "", "", false, 0)
	if items := gh58ParseItems(t, w.Body.Bytes()); len(items) == 0 {
		t.Error("title search q=Missing returned nothing for The Missing; the article fold is for Sonarr's q")
	}
}

func TestExactNameMatchKeepsTheArticleForTVDBNames(t *testing.T) {
	if exactNameMatchFor("The Missing", "Missing (2012)", true) {
		t.Error("TheTVDB's Missing (2012) matched The Missing")
	}
	if !exactNameMatchFor("The Missing", "The Missing", true) {
		t.Error("The Missing did not match itself")
	}
	if !exactNameMatchFor("The Missing", "Missing", false) {
		t.Error("Sonarr's q Missing did not match The Missing")
	}
}
