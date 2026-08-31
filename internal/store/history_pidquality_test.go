package store

import (
	"testing"
	"time"
)

// TestFindHistoryByPIDQuality_PrefersNonFailedEntry pins the tie-break.
// ForEach walks the bucket in key order and the keys are random nzo IDs,
// so a lookup that simply kept the last match would answer differently
// depending on which ID sorted higher. Here the failed row deliberately
// sorts after the completed one: the completed row must still win, so a
// re-grab of something already downloaded is never turned into a fresh
// download by key order alone. Issue #52.
func TestFindHistoryByPIDQuality_PrefersNonFailedEntry(t *testing.T) {
	s := testStore(t)

	done := &Download{
		ID:          "iparr_aaa_done",
		PID:         "m00free1",
		Quality:     "1080p",
		Status:      StatusCompleted,
		CompletedAt: time.Now(),
	}
	failed := &Download{
		ID:          "iparr_zzz_failed",
		PID:         "m00free1",
		Quality:     "1080p",
		Status:      StatusFailed,
		FailureCode: FailCodeNotYetAvailable,
		CompletedAt: time.Now(),
	}
	for _, dl := range []*Download{done, failed} {
		if err := s.PutHistory(dl); err != nil {
			t.Fatalf("PutHistory(%s): %v", dl.ID, err)
		}
	}

	got, err := s.FindHistoryByPIDQuality("m00free1", "1080p")
	if err != nil {
		t.Fatalf("FindHistoryByPIDQuality: %v", err)
	}
	if got == nil {
		t.Fatal("expected a match, got nil")
	}
	if got.ID != done.ID {
		t.Errorf("FindHistoryByPIDQuality = %q (status %q), want the completed row %q",
			got.ID, got.Status, done.ID)
	}
}

// TestFindHistoryByPIDQuality_ReturnsFailedWhenItIsAllThereIs keeps the
// preference from hiding a lone failed row: Enqueue needs to see it in
// order to clear it as it queues the retry.
func TestFindHistoryByPIDQuality_ReturnsFailedWhenItIsAllThereIs(t *testing.T) {
	s := testStore(t)

	failed := &Download{
		ID:          "iparr_only_failed",
		PID:         "m00free2",
		Quality:     "720p",
		Status:      StatusFailed,
		FailureCode: FailCodeNotYetAvailable,
		CompletedAt: time.Now(),
	}
	if err := s.PutHistory(failed); err != nil {
		t.Fatalf("PutHistory: %v", err)
	}

	got, err := s.FindHistoryByPIDQuality("m00free2", "720p")
	if err != nil {
		t.Fatalf("FindHistoryByPIDQuality: %v", err)
	}
	if got == nil || got.ID != failed.ID {
		t.Fatalf("FindHistoryByPIDQuality = %+v, want the failed row %q", got, failed.ID)
	}
}
