package store

import (
	"math/rand/v2"
	"testing"
	"time"
)

// ids returns the IDs of a download slice in order, for readable diffs.
func ids(dls []*Download) []string {
	out := make([]string, 0, len(dls))
	for _, d := range dls {
		out = append(out, d.ID)
	}
	return out
}

// TestListDownloads_EpisodeOrderWithinShow is the store-level half of
// GitHub #51. Rows are written with deliberately adversarial IDs: the
// random-hex ID order (which is what Bolt's ForEach yields) is the exact
// reverse of episode order, so a ListDownloads that does not sort fails
// loudly rather than passing by luck.
func TestListDownloads_EpisodeOrderWithinShow(t *testing.T) {
	s := testStore(t)

	base := time.Date(2026, 8, 14, 0, 18, 0, 0, time.UTC)
	// Written E01..E05, but the IDs sort E05..E01.
	for i := 1; i <= 5; i++ {
		dl := &Download{
			ID:        string(rune('a'+5-i)) + "_id",
			PID:       string(rune('p'+i)) + "pid",
			ShowName:  "Knee High Spies",
			Season:    1,
			Episode:   i,
			Status:    StatusPending,
			CreatedAt: base.Add(time.Duration(i) * time.Second),
		}
		if err := s.PutDownload(dl); err != nil {
			t.Fatalf("PutDownload: %v", err)
		}
	}

	got, err := s.ListDownloads()
	if err != nil {
		t.Fatalf("ListDownloads: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("len = %d, want 5", len(got))
	}
	for i, dl := range got {
		if want := i + 1; dl.Episode != want {
			t.Errorf("position %d has episode %d, want %d (full order: %v)",
				i, dl.Episode, want, ids(got))
		}
	}
}

// TestListDownloads_SeasonBeforeEpisode pins season as the key ahead of
// episode, so S01E10 is claimed before S02E01.
func TestListDownloads_SeasonBeforeEpisode(t *testing.T) {
	s := testStore(t)

	type row struct {
		id      string
		season  int
		episode int
	}
	// IDs ascend in the wrong order on purpose.
	rows := []row{
		{"a", 2, 1},
		{"b", 1, 10},
		{"c", 2, 2},
		{"d", 1, 2},
	}
	for _, r := range rows {
		if err := s.PutDownload(&Download{
			ID:        r.id,
			PID:       r.id + "pid",
			ShowName:  "Doctor Who",
			Season:    r.season,
			Episode:   r.episode,
			Status:    StatusPending,
			CreatedAt: time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC),
		}); err != nil {
			t.Fatalf("PutDownload: %v", err)
		}
	}

	got, err := s.ListDownloads()
	if err != nil {
		t.Fatalf("ListDownloads: %v", err)
	}
	want := []string{"d", "b", "a", "c"} // S01E02, S01E10, S02E01, S02E02
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("order = %v, want %v", ids(got), want)
		}
	}
}

// TestListDownloads_GroupsByShow pins the multi-show ruling: a show's
// episodes stay contiguous and shows are ordered by name, so two bulk
// grabs pushed interleaved by Sonarr are de-interleaved into two blocks,
// each in ascending episode order.
func TestListDownloads_GroupsByShow(t *testing.T) {
	s := testStore(t)

	base := time.Date(2026, 8, 14, 0, 18, 0, 0, time.UTC)
	// Arrival order mirrors the real interleave seen in production:
	// Knee High Spies E07, Do Not Watch E01, Knee High Spies E08, ...
	type row struct {
		id      string
		show    string
		episode int
		n       int
	}
	rows := []row{
		{"z1", "Knee High Spies", 7, 0},
		{"y2", "Do Not Watch This Show", 1, 1},
		{"x3", "Knee High Spies", 8, 2},
		{"w4", "Do Not Watch This Show", 2, 3},
	}
	for _, r := range rows {
		if err := s.PutDownload(&Download{
			ID:        r.id,
			PID:       r.id + "pid",
			ShowName:  r.show,
			Season:    1,
			Episode:   r.episode,
			Status:    StatusPending,
			CreatedAt: base.Add(time.Duration(r.n) * time.Second),
		}); err != nil {
			t.Fatalf("PutDownload: %v", err)
		}
	}

	got, err := s.ListDownloads()
	if err != nil {
		t.Fatalf("ListDownloads: %v", err)
	}
	want := []string{"y2", "w4", "z1", "x3"}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("order = %v, want %v (shows must be contiguous and in episode order)",
				ids(got), want)
		}
	}
}

// TestListDownloads_DailyShowByAirDate covers shows whose identity is the
// air date rather than a season/episode pair (BBC daily soaps, tier
// "date"). They must ascend by air date.
func TestListDownloads_DailyShowByAirDate(t *testing.T) {
	s := testStore(t)

	dates := []string{"2026-04-06", "2026-03-28", "2026-04-01"}
	for i, d := range dates {
		if err := s.PutDownload(&Download{
			ID:        string(rune('a'+i)) + "_daily",
			PID:       "pid" + d,
			ShowName:  "EastEnders",
			AirDate:   d,
			Status:    StatusPending,
			CreatedAt: time.Date(2026, 8, 14, 0, 0, i, 0, time.UTC),
		}); err != nil {
			t.Fatalf("PutDownload: %v", err)
		}
	}

	got, err := s.ListDownloads()
	if err != nil {
		t.Fatalf("ListDownloads: %v", err)
	}
	want := []string{"2026-03-28", "2026-04-01", "2026-04-06"}
	for i, d := range want {
		if got[i].AirDate != d {
			t.Fatalf("air-date order = %v, want %v",
				[]string{got[0].AirDate, got[1].AirDate, got[2].AirDate}, want)
		}
	}
}

// TestListDownloads_NoIdentityOrdersByCreatedAt covers rows that carry no
// episode identity at all. They are ordered strictly by CreatedAt, never
// by the random-hex ID, so their position is reproducible across polls
// and restarts. This is also the legacy-row case: a Bolt row written
// before the identity fields existed decodes with exactly these zero
// values.
func TestListDownloads_NoIdentityOrdersByCreatedAt(t *testing.T) {
	s := testStore(t)

	base := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	// IDs are the reverse of the CreatedAt order.
	rows := []struct {
		id     string
		offset int
	}{
		{"iparr_ff", 0},
		{"iparr_cc", 1},
		{"iparr_aa", 2},
	}
	for _, r := range rows {
		if err := s.PutDownload(&Download{
			ID:        r.id,
			PID:       r.id + "pid",
			Title:     "Secret.History.The.Lost.City.720p.WEB-DL.AAC.H264-iParr",
			Status:    StatusPending,
			CreatedAt: base.Add(time.Duration(r.offset) * time.Minute),
		}); err != nil {
			t.Fatalf("PutDownload: %v", err)
		}
	}

	got, err := s.ListDownloads()
	if err != nil {
		t.Fatalf("ListDownloads: %v", err)
	}
	want := []string{"iparr_ff", "iparr_cc", "iparr_aa"}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("order = %v, want %v (CreatedAt must decide, not the ID)",
				ids(got), want)
		}
	}
}

// TestListDownloads_UnidentifiedSortAhead pins the deliberate ruling for
// rows with no parsed identity: they sit at the head of the queue in
// CreatedAt order rather than the tail. Movie grabs (Radarr) carry no
// season/episode by construction, so parking unidentified rows at the
// tail would make every movie wait behind every TV season.
func TestListDownloads_UnidentifiedSortAhead(t *testing.T) {
	s := testStore(t)

	base := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	if err := s.PutDownload(&Download{
		ID:        "zzz_movie",
		PID:       "moviepid",
		Title:     "Blade.Runner.2049.1080p.WEB-DL.AAC.H264-iParr",
		Status:    StatusPending,
		CreatedAt: base.Add(time.Hour), // queued LAST
	}); err != nil {
		t.Fatalf("PutDownload: %v", err)
	}
	if err := s.PutDownload(&Download{
		ID:        "aaa_episode",
		PID:       "eppid",
		ShowName:  "Doctor Who",
		Season:    1,
		Episode:   1,
		Status:    StatusPending,
		CreatedAt: base,
	}); err != nil {
		t.Fatalf("PutDownload: %v", err)
	}

	got, err := s.ListDownloads()
	if err != nil {
		t.Fatalf("ListDownloads: %v", err)
	}
	if got[0].ID != "zzz_movie" {
		t.Fatalf("order = %v, want the unidentified row first", ids(got))
	}
}

// TestListDownloads_Deterministic pins the property the whole issue turns
// on at the ListDownloads level: repeated calls return the same order,
// so the queue a poll reports is the queue the next poll reports.
//
// Note what this does NOT prove. ForEach always yields the same
// byte-sorted input permutation, and sort.Slice is deterministic for a
// given input, so this would pass even against a badly-behaved
// comparator, and it passed before the fix too. The strict weak ordering
// is earned by TestSortDownloadQueue_TotalOrderUnderShuffle below, which
// varies the input permutation.
func TestListDownloads_Deterministic(t *testing.T) {
	s := testStore(t)

	base := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 12; i++ {
		if err := s.PutDownload(&Download{
			ID:        string(rune('a'+i)) + "_det",
			PID:       string(rune('a'+i)) + "_pid",
			ShowName:  []string{"Alpha", "Beta", ""}[i%3],
			Season:    i % 2,
			Episode:   i % 4,
			Status:    StatusPending,
			CreatedAt: base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("PutDownload: %v", err)
		}
	}

	first, err := s.ListDownloads()
	if err != nil {
		t.Fatalf("ListDownloads: %v", err)
	}
	for n := 0; n < 5; n++ {
		again, err := s.ListDownloads()
		if err != nil {
			t.Fatalf("ListDownloads: %v", err)
		}
		for i := range first {
			if first[i].ID != again[i].ID {
				t.Fatalf("call %d order = %v, first call = %v", n, ids(again), ids(first))
			}
		}
	}
}

// TestSortDownloadQueue_TotalOrderUnderShuffle exercises the comparator
// directly, which is the only way to vary the input permutation:
// ListDownloads can never do it, because ForEach always hands the sort
// the same byte-sorted slice.
//
// sort.Slice uses an unstable pdqsort, so if the comparator were not a
// strict weak ordering the result would depend on the starting
// permutation. Sorting many different shuffles of one fixture and
// demanding an identical result each time is what makes the ordering
// claim real rather than asserted.
//
// The fixture is built to be adversarial: rows that tie on every key
// down to CreatedAt and separate only at ID, rows that tie on ShowName
// and Season, and unidentified rows that tie on everything but
// CreatedAt. Those ties are exactly where an inconsistent comparator
// would show itself.
func TestSortDownloadQueue_TotalOrderUnderShuffle(t *testing.T) {
	base := time.Date(2026, 8, 14, 0, 18, 0, 0, time.UTC)

	build := func() []*Download {
		var out []*Download
		add := func(id, show string, season, episode int, airDate string, offset int) {
			out = append(out, &Download{
				ID:        id,
				PID:       id + "_pid",
				ShowName:  show,
				Season:    season,
				Episode:   episode,
				AirDate:   airDate,
				Status:    StatusPending,
				CreatedAt: base.Add(time.Duration(offset) * time.Second),
			})
		}
		// Two shows, contiguous blocks, ascending episodes.
		add("k07", "Knee High Spies", 1, 7, "", 0)
		add("k08", "Knee High Spies", 1, 8, "", 2)
		add("k09", "Knee High Spies", 1, 9, "", 4)
		add("d01", "Do Not Watch This Show", 1, 1, "", 6)
		add("d02", "Do Not Watch This Show", 1, 2, "", 8)
		// A specials row: season 0 leads its show.
		add("k00", "Knee High Spies", 0, 1225, "", 10)
		// Date-tier rows for one daily show, same show name, no numbering.
		add("e01", "EastEnders", 0, 0, "2026-03-28", 12)
		add("e02", "EastEnders", 0, 0, "2026-04-01", 14)
		// Full ties down to CreatedAt: only ID separates these two.
		add("t_a", "Tie Show", 3, 4, "", 20)
		add("t_b", "Tie Show", 3, 4, "", 20)
		// Unidentified rows: everything zero but CreatedAt.
		add("m1", "", 0, 0, "", 30)
		add("m2", "", 0, 0, "", 31)
		add("m3", "", 0, 0, "", 32)
		return out
	}

	reference := build()
	sortDownloadQueue(reference)
	want := ids(reference)

	// The ordering must actually be the one we claim, not merely stable.
	expected := []string{
		"m1", "m2", "m3", // unidentified, first, by CreatedAt
		"d01", "d02", // Do Not Watch This Show
		"e01", "e02", // EastEnders, by air date
		"k00",               // Knee High Spies specials (season 0) lead
		"k07", "k08", "k09", // then season 1 in episode order
		"t_a", "t_b", // full tie broken by ID
	}
	for i := range expected {
		if want[i] != expected[i] {
			t.Fatalf("comparator order = %v, want %v", want, expected)
		}
	}

	// Deterministically seeded so a failure is reproducible.
	rng := rand.New(rand.NewPCG(51, 2026))
	for n := 0; n < 200; n++ {
		shuffled := build()
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		sortDownloadQueue(shuffled)
		got := ids(shuffled)
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("shuffle %d sorted to %v, want %v", n, got, want)
			}
		}
	}
}
