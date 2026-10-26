package newznab

import (
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// Gitea #100. The topical escape hatch (GitHub #20) lets a programme
// with no series or episode number and an air date answer a season or
// episode search under a date title. m00326vx "Traitor or Faithful?
// Claudia Decides" answered every season and episode search for The
// Celebrity Traitors as The.Celebrity.Traitors.2026.10.01.*, which
// Sonarr cannot map to any episode of a Standard series. The numbered
// episodes are the m002csng fixture's (testdata/gh58); m00326vx has the
// shape the issue reports (no position, 2026-10-01).
const gitea100TraitorsPayload = `{"new_search":{"results":[
	{"id":"m0030pxt","type":"episode","title":"The Celebrity Traitors","subtitle":"Series 2: Episode 1","release_date":"1 Oct 2026","parent_position":1},
	{"id":"m0030pxx","type":"episode","title":"The Celebrity Traitors","subtitle":"Series 2: Episode 2","release_date":"2 Oct 2026","parent_position":2},
	{"id":"m00326vx","type":"episode","title":"The Celebrity Traitors","subtitle":"Traitor or Faithful? Claudia Decides","release_date":"1 Oct 2026"}
]}}`

const gitea100DateTitle = "The.Celebrity.Traitors.2026.10.01.Traitor.or.Faithful.Claudia.Decides.720p.WEB-DL.AAC.H264-iParr"

var gitea100Dated = map[string]bool{"m00326vx": true}

func TestGitea100_DateTitleAnswersNoSeasonOrEpisodeSearchOfANumberedShow(t *testing.T) {
	h := newHandlerWithBBC(t, gitea100TraitorsPayload)

	items := gh58Serve(t, h, "t=tvsearch&q=The+Celebrity+Traitors&season=2&ep=2")
	gh58AssertTitle(t, items, "m0030pxx",
		"The.Celebrity.Traitors.S02E02.Series.2.Episode.2.720p.WEB-DL.AAC.H264-iParr")
	gh58AssertNone(t, items, gitea100Dated, "date-titled")

	items = gh58Serve(t, h, "t=tvsearch&q=The+Celebrity+Traitors&season=2")
	gh58AssertTitle(t, items, "m0030pxt",
		"The.Celebrity.Traitors.S02E01.Series.2.Episode.1.720p.WEB-DL.AAC.H264-iParr")
	gh58AssertNone(t, items, gitea100Dated, "date-titled")

	// A number the show does not have: nothing at all.
	if items := gh58Serve(t, h, "t=tvsearch&q=The+Celebrity+Traitors&season=7&ep=1"); len(items) != 0 {
		t.Errorf("S07E01 returned %d items, first %q; want none", len(items), items[0].Title)
	}
}

// Searches that ask for no number, or for an air date, still find it.
// Sonarr searches a Daily series with the year as the season and MM/DD
// as the episode, so the season filter is set there too, and searches a
// Daily series' season with the year alone (DailySeasonSearchCriteria),
// which has no air date to match by.
func TestGitea100_DateTitleStillAnswersSearchesThatAskNoNumber(t *testing.T) {
	h := newHandlerWithBBC(t, gitea100TraitorsPayload)
	for _, q := range []string{
		"t=tvsearch&q=The+Celebrity+Traitors",
		"t=search&q=The+Celebrity+Traitors",
		"t=tvsearch&q=The+Celebrity+Traitors&season=2026&ep=10%2F01",
		"t=tvsearch&q=The+Celebrity+Traitors&season=2026",
	} {
		gh58AssertTitle(t, gh58Serve(t, h, q), "m00326vx", gitea100DateTitle)
	}
}

// GitHub #20 stays: a show iPlayer never numbers keeps the hatch. A
// special among its programmes goes out as S00E<position>, a number
// iplayer-arr guesses, and that does not make the show numbered.
func TestGitea100_TopicalShowKeepsTheHatch(t *testing.T) {
	const payload = `{"new_search":{"results":[
		{"id":"qt1","type":"episode","title":"Question Time","subtitle":"26/03/2026","release_date":"2026-03-26","parent_position":23},
		{"id":"qt2","type":"episode","title":"Question Time","subtitle":"Leaders' Special","release_date":"2026-03-30","parent_position":24}
	]}}`
	h := newHandlerWithBBC(t, payload)
	gh58AssertTitle(t, gh58Serve(t, h, "t=tvsearch&q=question+time&season=48&ep=23"), "qt1",
		"Question.Time.2026.03.26.720p.WEB-DL.AAC.H264-iParr")
}

// A Show Override that forces date titles gives the whole show date
// titles, so nothing of it goes out numbered and the hatch still works,
// as it did before.
func TestGitea100_ForceDateBasedShowKeepsTheHatch(t *testing.T) {
	h, st := newHandlerWithBBCAndStore(t, gitea100TraitorsPayload)
	if err := st.PutOverride(&store.ShowOverride{ShowName: "The Celebrity Traitors", ForceDateBased: true}); err != nil {
		t.Fatal(err)
	}
	gh58AssertTitle(t, gh58Serve(t, h, "t=tvsearch&q=The+Celebrity+Traitors&season=2&ep=2"), "m00326vx", gitea100DateTitle)
}
