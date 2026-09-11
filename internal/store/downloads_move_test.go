package store

import (
	"errors"
	"testing"
	"time"
)

// seedPendingSeason writes S01E01..E0n of one show as pending rows with
// adversarial IDs (reverse of episode order) and returns them.
func seedPendingSeason(t *testing.T, s *Store, n int) {
	t.Helper()
	base := time.Date(2026, 8, 25, 16, 20, 0, 0, time.UTC)
	for i := 1; i <= n; i++ {
		dl := &Download{
			ID:        string(rune('a'+n-i)) + "_id",
			PID:       string(rune('p'+i)) + "pid",
			Title:     "Show.S01E0" + string(rune('0'+i)),
			ShowName:  "Waterloo Road",
			Season:    1,
			Episode:   i,
			Status:    StatusPending,
			CreatedAt: base.Add(time.Duration(i) * time.Second),
		}
		if err := s.PutDownload(dl); err != nil {
			t.Fatalf("PutDownload: %v", err)
		}
	}
}

func episodes(dls []*Download) []int {
	out := make([]int, 0, len(dls))
	for _, d := range dls {
		out = append(out, d.Episode)
	}
	return out
}

func assertEpisodes(t *testing.T, got []*Download, want ...int) {
	t.Helper()
	g := episodes(got)
	if len(g) != len(want) {
		t.Fatalf("episodes = %v, want %v", g, want)
	}
	for i := range want {
		if g[i] != want[i] {
			t.Fatalf("episodes = %v, want %v", g, want)
		}
	}
}

// GitHub #51 second half: the operator can move a pending row and the
// worker claim order (ListDownloads) follows the new order.
func TestMoveDownload_TopUpDownBottom(t *testing.T) {
	s := testStore(t)
	seedPendingSeason(t, s, 5)

	// E04 to the top.
	got, err := s.MoveDownload("b_id", MoveTop) // b_id is E04 (n-i = 1)
	if err != nil {
		t.Fatalf("top: %v", err)
	}
	assertEpisodes(t, got, 4, 1, 2, 3, 5)

	// E05 up one: swaps with E03.
	got, err = s.MoveDownload("a_id", MoveUp) // E05
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	assertEpisodes(t, got, 4, 1, 2, 5, 3)

	// E04 down one.
	got, err = s.MoveDownload("b_id", MoveDown)
	if err != nil {
		t.Fatalf("down: %v", err)
	}
	assertEpisodes(t, got, 1, 4, 2, 5, 3)

	// E01 to the bottom.
	got, err = s.MoveDownload("e_id", MoveBottom) // E01
	if err != nil {
		t.Fatalf("bottom: %v", err)
	}
	assertEpisodes(t, got, 4, 2, 5, 3, 1)

	// The claim loop reads ListDownloads; it must agree exactly.
	all, err := s.ListDownloads()
	if err != nil {
		t.Fatalf("ListDownloads: %v", err)
	}
	assertEpisodes(t, all, 4, 2, 5, 3, 1)

	// Priorities are materialised len..1 so the order is explicit and
	// the back row still outranks a zero-priority late arrival.
	for i, dl := range all {
		if want := len(all) - i; dl.Priority != want {
			t.Errorf("position %d (E%02d) priority = %d, want %d", i, dl.Episode, dl.Priority, want)
		}
	}
}

func TestMoveDownload_EdgesAreNoOps(t *testing.T) {
	s := testStore(t)
	seedPendingSeason(t, s, 3)

	got, err := s.MoveDownload("c_id", MoveUp) // E01 already first
	if err != nil {
		t.Fatalf("up at top: %v", err)
	}
	assertEpisodes(t, got, 1, 2, 3)

	got, err = s.MoveDownload("a_id", MoveDown) // E03 already last
	if err != nil {
		t.Fatalf("down at bottom: %v", err)
	}
	assertEpisodes(t, got, 1, 2, 3)
}

func TestMoveDownload_RefusesNonPending(t *testing.T) {
	s := testStore(t)
	seedPendingSeason(t, s, 3)
	dl, _ := s.GetDownload("b_id")
	dl.Status = StatusDownloading
	if err := s.PutDownload(dl); err != nil {
		t.Fatal(err)
	}

	_, err := s.MoveDownload("b_id", MoveTop)
	if !errors.Is(err, ErrDownloadNotPending) {
		t.Fatalf("err = %v, want ErrDownloadNotPending", err)
	}
	_, err = s.MoveDownload("nope", MoveTop)
	if !errors.Is(err, ErrDownloadNotFound) {
		t.Fatalf("err = %v, want ErrDownloadNotFound", err)
	}
	_, err = s.MoveDownload("a_id", "sideways")
	if !errors.Is(err, ErrInvalidMove) {
		t.Fatalf("err = %v, want ErrInvalidMove", err)
	}

	// The in-flight row is not part of the pending set and is untouched.
	got, err := s.MoveDownload("a_id", MoveTop)
	if err != nil {
		t.Fatal(err)
	}
	assertEpisodes(t, got, 3, 1)
	after, _ := s.GetDownload("b_id")
	if after.Priority != 0 || after.Status != StatusDownloading {
		t.Errorf("in-flight row changed: %+v", after)
	}
}

// A grab that arrives after the operator has arranged the queue goes to
// the back of the arranged block, not into the middle of it.
func TestMoveDownload_LaterArrivalAppends(t *testing.T) {
	s := testStore(t)
	seedPendingSeason(t, s, 3)
	if _, err := s.MoveDownload("a_id", MoveTop); err != nil { // E03 first
		t.Fatal(err)
	}
	late := &Download{
		ID: "0_late", PID: "late", ShowName: "Aardvark Hour", Season: 1, Episode: 1,
		Status: StatusPending, CreatedAt: time.Now(),
	}
	if err := s.PutDownload(late); err != nil {
		t.Fatal(err)
	}
	all, _ := s.ListDownloads()
	if all[len(all)-1].ID != "0_late" {
		t.Errorf("late arrival should be last, got %v", ids(all))
	}
	assertEpisodes(t, all[:3], 3, 1, 2)
}

// Rows with zero priority keep the pre-#51 natural order exactly, so an
// untouched queue is byte-for-byte the v1.7.0 behaviour.
func TestSortDownloadQueue_ZeroPriorityIsNaturalOrder(t *testing.T) {
	s := testStore(t)
	seedPendingSeason(t, s, 4)
	all, _ := s.ListDownloads()
	assertEpisodes(t, all, 1, 2, 3, 4)
	for _, dl := range all {
		if dl.Priority != 0 {
			t.Errorf("untouched row has priority %d", dl.Priority)
		}
	}
}
