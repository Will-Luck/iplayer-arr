package download

import (
	"path/filepath"
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// bulkGrab mirrors a real Sonarr bulk push captured in production on
// 2026-08-14T00:18: two shows interleaved, two seconds apart, each
// release title exactly as this project's newznab feed emits it. The
// order below is the order Sonarr actually pushed them in.
var bulkGrab = []struct {
	pid   string
	title string
}{
	{"m002ypj4", "Knee.High.Spies.S01E07.Mission.Dino.in.Danger.1080p.WEB-DL.AAC.H264-iParr"},
	{"m002ypj7", "Knee.High.Spies.S01E08.Mission.Pink.Prank.1080p.WEB-DL.AAC.H264-iParr"},
	{"m002ypj8", "Knee.High.Spies.S01E09.Mission.No.Sleep.Sleepover.1080p.WEB-DL.AAC.H264-iParr"},
	{"m002ypjc", "Knee.High.Spies.S01E10.Mission.Domino.Disaster.1080p.WEB-DL.AAC.H264-iParr"},
	{"m002ypjg", "Knee.High.Spies.S01E11.Mission.Rocket.Rescue.1080p.WEB-DL.AAC.H264-iParr"},
	{"m002ynss", "Do.Not.Watch.This.Show.S01E01.Series.1.Frog.1080p.WEB-DL.AAC.H264-iParr"},
	{"m002ypjh", "Knee.High.Spies.S01E12.Mission.Shhh.1080p.WEB-DL.AAC.H264-iParr"},
	{"m002yntg", "Do.Not.Watch.This.Show.S01E02.Series.1.Vanish.1080p.WEB-DL.AAC.H264-iParr"},
	{"m002yntf", "Do.Not.Watch.This.Show.S01E03.Series.1.Parents.1080p.WEB-DL.AAC.H264-iParr"},
	{"m002yntc", "Do.Not.Watch.This.Show.S01E04.Series.1.Ghost.1080p.WEB-DL.AAC.H264-iParr"},
}

func testManager(t *testing.T) (*Manager, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return NewManager(st, filepath.Join(dir, "downloads"), 2, nil, nil, nil, nil), st
}

func titlesOf(dls []*store.Download) []string {
	out := make([]string, 0, len(dls))
	for _, d := range dls {
		out = append(out, d.Title)
	}
	return out
}

// TestBulkSonarrGrabClaimsInEpisodeOrder is the GitHub #51 regression
// test. It replays a real interleaved Sonarr bulk grab through
// StartDownload, the exact entry point the SABnzbd shim calls, and
// asserts the queue that the worker claims from comes back grouped by
// show and ascending by episode.
//
// Before the fix this failed because ListDownloads walks the Bolt bucket
// in byte-sorted key order and the key is a random hex nzo id, so the
// queue was a stable scramble.
func TestBulkSonarrGrabClaimsInEpisodeOrder(t *testing.T) {
	m, st := testManager(t)

	for _, g := range bulkGrab {
		if _, err := m.StartDownload(g.pid, "1080p", g.title, "sonarr"); err != nil {
			t.Fatalf("StartDownload(%s): %v", g.pid, err)
		}
	}

	got, err := st.ListDownloads()
	if err != nil {
		t.Fatalf("ListDownloads: %v", err)
	}
	if len(got) != len(bulkGrab) {
		t.Fatalf("queue length = %d, want %d", len(got), len(bulkGrab))
	}

	want := []string{
		"Do.Not.Watch.This.Show.S01E01.Series.1.Frog.1080p.WEB-DL.AAC.H264-iParr",
		"Do.Not.Watch.This.Show.S01E02.Series.1.Vanish.1080p.WEB-DL.AAC.H264-iParr",
		"Do.Not.Watch.This.Show.S01E03.Series.1.Parents.1080p.WEB-DL.AAC.H264-iParr",
		"Do.Not.Watch.This.Show.S01E04.Series.1.Ghost.1080p.WEB-DL.AAC.H264-iParr",
		"Knee.High.Spies.S01E07.Mission.Dino.in.Danger.1080p.WEB-DL.AAC.H264-iParr",
		"Knee.High.Spies.S01E08.Mission.Pink.Prank.1080p.WEB-DL.AAC.H264-iParr",
		"Knee.High.Spies.S01E09.Mission.No.Sleep.Sleepover.1080p.WEB-DL.AAC.H264-iParr",
		"Knee.High.Spies.S01E10.Mission.Domino.Disaster.1080p.WEB-DL.AAC.H264-iParr",
		"Knee.High.Spies.S01E11.Mission.Rocket.Rescue.1080p.WEB-DL.AAC.H264-iParr",
		"Knee.High.Spies.S01E12.Mission.Shhh.1080p.WEB-DL.AAC.H264-iParr",
	}
	gotTitles := titlesOf(got)
	for i := range want {
		if gotTitles[i] != want[i] {
			t.Fatalf("queue order wrong at position %d\n got = %v\nwant = %v", i, gotTitles, want)
		}
	}
}

// TestEnqueuePopulatesEpisodeIdentity checks the persisted side of the
// fix: Enqueue parses the release title once, at enqueue time, so the
// ordering never has to re-parse and the dashboard can show what it
// resolved.
func TestEnqueuePopulatesEpisodeIdentity(t *testing.T) {
	m, st := testManager(t)

	id, err := m.Enqueue("m002ypj4", "1080p",
		"Knee.High.Spies.S01E07.Mission.Dino.in.Danger.1080p.WEB-DL.AAC.H264-iParr", "sonarr")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	dl, err := st.GetDownload(id)
	if err != nil || dl == nil {
		t.Fatalf("GetDownload: %v", err)
	}
	if dl.ShowName != "Knee High Spies" {
		t.Errorf("ShowName = %q, want %q", dl.ShowName, "Knee High Spies")
	}
	if dl.Season != 1 {
		t.Errorf("Season = %d, want 1", dl.Season)
	}
	if dl.Episode != 7 {
		t.Errorf("Episode = %d, want 7", dl.Episode)
	}
}

// TestEnqueueUnparseableTitleLeavesZeroValues pins the total-parse
// requirement. A movie release (Radarr) carries no season, episode or
// air date by construction, and a tier-4 manual title carries none
// either. Neither may panic or error; both leave the identity fields at
// their zero values.
func TestEnqueueUnparseableTitleLeavesZeroValues(t *testing.T) {
	m, st := testManager(t)

	for _, tc := range []struct{ pid, title string }{
		{"m00movie", "Blade.Runner.2049.1080p.WEB-DL.AAC.H264-iParr"},
		{"m00manual", "Secret.History.The.Lost.City.720p.WEB-DL.AAC.H264-iParr"},
		{"m00empty", ""},
	} {
		id, err := m.Enqueue(tc.pid, "1080p", tc.title, "radarr")
		if err != nil {
			t.Fatalf("Enqueue(%q): %v", tc.title, err)
		}
		dl, err := st.GetDownload(id)
		if err != nil || dl == nil {
			t.Fatalf("GetDownload(%q): %v", tc.title, err)
		}
		if dl.ShowName != "" || dl.Season != 0 || dl.Episode != 0 || dl.AirDate != "" {
			t.Errorf("title %q parsed to show=%q s=%d e=%d date=%q, want all zero",
				tc.title, dl.ShowName, dl.Season, dl.Episode, dl.AirDate)
		}
	}
}
