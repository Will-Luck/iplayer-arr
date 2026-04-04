package store

import (
	"fmt"
	"testing"
	"time"
)

// seedHistory inserts n completed history entries with titles "A", "B", "C", ...
// and ascending CompletedAt times starting from base.
func seedHistory(t *testing.T, s *Store, base time.Time, titles ...string) []*Download {
	t.Helper()
	dls := make([]*Download, 0, len(titles))
	for i, title := range titles {
		dl := &Download{
			ID:          "hist_seed_" + title,
			PID:         "pid_" + title,
			Title:       title,
			Status:      StatusCompleted,
			Size:        int64((i + 1) * 100),
			CompletedAt: base.Add(time.Duration(i) * time.Hour),
		}
		if err := s.PutHistory(dl); err != nil {
			t.Fatalf("PutHistory %q: %v", title, err)
		}
		dls = append(dls, dl)
	}
	return dls
}

func TestListHistoryFilteredNoFilter(t *testing.T) {
	s := testStore(t)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	seedHistory(t, s, base, "Alpha", "Beta", "Gamma")

	page, err := s.ListHistoryFiltered(HistoryFilter{Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("ListHistoryFiltered: %v", err)
	}
	if page.Total != 3 {
		t.Errorf("Total = %d, want 3", page.Total)
	}
	if len(page.Items) != 3 {
		t.Errorf("Items len = %d, want 3", len(page.Items))
	}
}

func TestListHistoryFilteredStatusFilter(t *testing.T) {
	s := testStore(t)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	completed := &Download{ID: "h_c1", PID: "pc1", Title: "Completed One", Status: StatusCompleted, CompletedAt: base}
	failed := &Download{ID: "h_f1", PID: "pf1", Title: "Failed One", Status: StatusFailed, CompletedAt: base.Add(time.Hour)}
	s.PutHistory(completed)
	s.PutHistory(failed)

	// Filter by completed.
	page, err := s.ListHistoryFiltered(HistoryFilter{Status: "completed", Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("ListHistoryFiltered completed: %v", err)
	}
	if page.Total != 1 {
		t.Errorf("completed Total = %d, want 1", page.Total)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "h_c1" {
		t.Errorf("unexpected items for completed filter: %v", page.Items)
	}

	// Filter by failed.
	page, err = s.ListHistoryFiltered(HistoryFilter{Status: "failed", Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("ListHistoryFiltered failed: %v", err)
	}
	if page.Total != 1 {
		t.Errorf("failed Total = %d, want 1", page.Total)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "h_f1" {
		t.Errorf("unexpected items for failed filter: %v", page.Items)
	}
}

func TestListHistoryFilteredSinceFilter(t *testing.T) {
	s := testStore(t)

	// Insert three entries with CompletedAt on separate days.
	entries := []struct {
		id          string
		title       string
		completedAt time.Time
	}{
		{"since_old", "Old", time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)},
		{"since_mid", "Mid", time.Date(2024, 6, 2, 12, 0, 0, 0, time.UTC)},
		{"since_new", "New", time.Date(2024, 6, 3, 12, 0, 0, 0, time.UTC)},
	}
	for _, e := range entries {
		dl := &Download{
			ID: e.id, PID: "pid_" + e.id, Title: e.title,
			Status: StatusCompleted, CompletedAt: e.completedAt,
		}
		if err := s.PutHistory(dl); err != nil {
			t.Fatalf("PutHistory %q: %v", e.title, err)
		}
	}

	// Since 2024-06-02 -- entries on 2 June and 3 June should be included;
	// the 1 June entry is before the cutoff and excluded.
	page, err := s.ListHistoryFiltered(HistoryFilter{
		Since:   "2024-06-02",
		Page:    1,
		PerPage: 20,
	})
	if err != nil {
		t.Fatalf("ListHistoryFiltered since: %v", err)
	}
	if page.Total != 2 {
		t.Errorf("Total = %d, want 2", page.Total)
	}
	for _, item := range page.Items {
		if item.Title == "Old" {
			t.Error("unexpected 'Old' entry in since-filtered results")
		}
	}
}

func TestListHistoryFilteredPagination(t *testing.T) {
	s := testStore(t)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	// Insert 5 entries with predictable CompletedAt ordering.
	seedHistory(t, s, base, "A", "B", "C", "D", "E")

	// Default sort is completed_at desc, so page 1 with per_page=2 gets the newest two.
	page1, err := s.ListHistoryFiltered(HistoryFilter{Page: 1, PerPage: 2})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if page1.Total != 5 {
		t.Errorf("Total = %d, want 5", page1.Total)
	}
	if len(page1.Items) != 2 {
		t.Errorf("page 1 Items len = %d, want 2", len(page1.Items))
	}

	page2, err := s.ListHistoryFiltered(HistoryFilter{Page: 2, PerPage: 2})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(page2.Items) != 2 {
		t.Errorf("page 2 Items len = %d, want 2", len(page2.Items))
	}
	if page2.Total != 5 {
		t.Errorf("page 2 Total = %d, want 5", page2.Total)
	}

	// Pages should not overlap.
	page1IDs := make(map[string]bool)
	for _, item := range page1.Items {
		page1IDs[item.ID] = true
	}
	for _, item := range page2.Items {
		if page1IDs[item.ID] {
			t.Errorf("ID %q appears on both page 1 and page 2", item.ID)
		}
	}

	// Page beyond the end returns empty items but correct total.
	pageOOB, err := s.ListHistoryFiltered(HistoryFilter{Page: 99, PerPage: 2})
	if err != nil {
		t.Fatalf("page OOB: %v", err)
	}
	if len(pageOOB.Items) != 0 {
		t.Errorf("out-of-bounds page should return 0 items, got %d", len(pageOOB.Items))
	}
	if pageOOB.Total != 5 {
		t.Errorf("out-of-bounds page Total = %d, want 5", pageOOB.Total)
	}
}

func TestListHistoryFilteredSortAscDesc(t *testing.T) {
	s := testStore(t)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	// Entries: A (oldest), B, C (newest).
	seedHistory(t, s, base, "A", "B", "C")

	// Ascending by completed_at: A should be first.
	asc, err := s.ListHistoryFiltered(HistoryFilter{Sort: "completed_at", Order: "asc", Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("asc: %v", err)
	}
	if asc.Items[0].Title != "A" {
		t.Errorf("asc[0] = %q, want A", asc.Items[0].Title)
	}
	if asc.Items[len(asc.Items)-1].Title != "C" {
		t.Errorf("asc[last] = %q, want C", asc.Items[len(asc.Items)-1].Title)
	}

	// Descending by completed_at: C should be first.
	desc, err := s.ListHistoryFiltered(HistoryFilter{Sort: "completed_at", Order: "desc", Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("desc: %v", err)
	}
	if desc.Items[0].Title != "C" {
		t.Errorf("desc[0] = %q, want C", desc.Items[0].Title)
	}
}

func TestListHistoryFilteredSortByTitle(t *testing.T) {
	s := testStore(t)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	// Entries inserted in reverse-alpha order but CompletedAt ascending.
	seedHistory(t, s, base, "Zebra", "Apple", "Mango")

	asc, err := s.ListHistoryFiltered(HistoryFilter{Sort: "title", Order: "asc", Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("title asc: %v", err)
	}
	if asc.Items[0].Title != "Apple" {
		t.Errorf("title asc[0] = %q, want Apple", asc.Items[0].Title)
	}
	if asc.Items[len(asc.Items)-1].Title != "Zebra" {
		t.Errorf("title asc[last] = %q, want Zebra", asc.Items[len(asc.Items)-1].Title)
	}
}

func TestClearHistory(t *testing.T) {
	s := testStore(t)

	for i := 0; i < 3; i++ {
		dl := &Download{
			ID:     fmt.Sprintf("test_%d", i),
			PID:    fmt.Sprintf("p%d", i),
			Status: StatusCompleted,
			Title:  fmt.Sprintf("Test %d", i),
		}
		if err := s.PutHistory(dl); err != nil {
			t.Fatalf("PutHistory: %v", err)
		}
	}

	all, _ := s.ListHistory()
	if len(all) != 3 {
		t.Fatalf("expected 3 history entries, got %d", len(all))
	}

	n, err := s.ClearHistory()
	if err != nil {
		t.Fatalf("ClearHistory: %v", err)
	}
	if n != 3 {
		t.Errorf("ClearHistory returned %d, want 3", n)
	}

	all, _ = s.ListHistory()
	if len(all) != 0 {
		t.Errorf("expected 0 history entries after clear, got %d", len(all))
	}
}
