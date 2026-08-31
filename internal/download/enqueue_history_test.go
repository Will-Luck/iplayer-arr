package download

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// enqueueHistoryFixture spins up a real store plus an unstarted Manager.
// Workers are never launched, so an enqueued row stays pending and
// nothing touches the network.
func enqueueHistoryFixture(t *testing.T) (*Manager, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return NewManager(st, filepath.Join(dir, "downloads"), 1, nil, nil, nil, nil), st
}

// deadNotYetAvailableRow is the history row that issue #52 leaves behind:
// an episode grabbed before BBC published its playlist, failed
// maxNotYetAvailableRetries times and moved to history by failDownload.
func deadNotYetAvailableRow() *store.Download {
	return &store.Download{
		ID:          "iparr_dead52",
		PID:         "m00free1",
		Quality:     "1080p",
		Title:       "Match.of.the.Day.2026.08.30.1080p.WEB-DL.AAC.H264-iParr",
		Category:    "sonarr",
		Status:      store.StatusFailed,
		FailureCode: store.FailCodeNotYetAvailable,
		RetryCount:  maxNotYetAvailableRetries,
		Retryable:   false,
		Error:       "not yet available",
		CompletedAt: time.Now(),
	}
}

// TestEnqueue_FailedHistoryDoesNotBlockReEnqueue is the core of issue #52.
// FindHistoryByPIDQuality is status-blind, so once a not-yet-available
// grab exhausted its retries and landed in history, every later
// re-enqueue of the same pid+quality was handed the dead ID and nothing
// was ever queued again. A failed history row is not completed work and
// must not short-circuit the enqueue.
func TestEnqueue_FailedHistoryDoesNotBlockReEnqueue(t *testing.T) {
	m, st := enqueueHistoryFixture(t)

	dead := deadNotYetAvailableRow()
	if err := st.PutHistory(dead); err != nil {
		t.Fatalf("PutHistory: %v", err)
	}

	id, err := m.Enqueue(dead.PID, dead.Quality, dead.Title, "sonarr")
	if err != nil {
		t.Fatalf("Enqueue over a failed history row: %v", err)
	}
	if id == dead.ID {
		t.Fatalf("Enqueue returned the dead history ID %q, expected a fresh download", dead.ID)
	}

	dl, err := st.GetDownload(id)
	if err != nil {
		t.Fatalf("GetDownload(%s): %v", id, err)
	}
	if dl == nil {
		t.Fatalf("no new download row created for id %q", id)
	}
	if dl.Status != store.StatusPending {
		t.Errorf("new download status = %q, want %q", dl.Status, store.StatusPending)
	}
	if dl.PID != dead.PID || dl.Quality != dead.Quality {
		t.Errorf("new download = pid %q quality %q, want %q / %q",
			dl.PID, dl.Quality, dead.PID, dead.Quality)
	}
}

// TestEnqueue_CompletedHistoryStillDedups guards the behaviour the #52
// fix must not break: something already downloaded is still deduped by
// the history bucket and is never re-downloaded.
func TestEnqueue_CompletedHistoryStillDedups(t *testing.T) {
	m, st := enqueueHistoryFixture(t)

	done := &store.Download{
		ID:          "iparr_done52",
		PID:         "m00done1",
		Quality:     "1080p",
		Title:       "Show.S01E01.1080p.WEB-DL.AAC.H264-iParr",
		Status:      store.StatusCompleted,
		Size:        1234,
		CompletedAt: time.Now(),
	}
	if err := st.PutHistory(done); err != nil {
		t.Fatalf("PutHistory: %v", err)
	}

	id, err := m.Enqueue(done.PID, done.Quality, done.Title, "sonarr")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if id != done.ID {
		t.Fatalf("Enqueue returned %q, want the existing history ID %q", id, done.ID)
	}

	active, err := st.ListDownloads()
	if err != nil {
		t.Fatalf("ListDownloads: %v", err)
	}
	if len(active) != 0 {
		t.Errorf("expected no new download row for a completed grab, got %d", len(active))
	}
	if hist, _ := st.GetHistory(done.ID); hist == nil {
		t.Error("completed history row must be preserved by a dedup hit")
	}
}

// TestEnqueue_NonNotYetAvailableFailureStillDedups is the blast-radius
// guard on the #52 fix. Only the not-yet-available failure is a "come
// back later" that deserves a fresh attempt. Every other terminal
// failure must keep deduping exactly as it did before:
//
//   - truncated / ffmpeg_error would refetch a whole episode that died
//     at 95%, on every later grab;
//   - expired / geo_blocked / stream_unavailable cannot succeed on a
//     retry at all, so retrying just burns bandwidth and re-fails;
//   - a row with no failure code at all (the SABnzbd queue-delete path
//     in internal/sabnzbd/handler.go writes StatusFailed and leaves
//     FailureCode unset) keeps its existing behaviour untouched.
//
// The list is store/types.go's full FailCode enumeration minus
// not_yet_available, plus the empty-code case, so a newly added failure
// code is the only way this can silently stop covering the population.
func TestEnqueue_NonNotYetAvailableFailureStillDedups(t *testing.T) {
	codes := []string{
		store.FailCodeGeoBlocked,
		store.FailCodeExpired,
		store.FailCodeUnavailable,
		store.FailCodeFFmpeg,
		store.FailCodeTruncated,
		store.FailCodeStalled,
		store.FailCodeTimeout,
		store.FailCodeUnknown,
		"", // cancelled via the SAB shim: StatusFailed, no code
	}

	for _, code := range codes {
		name := code
		if name == "" {
			name = "no_failure_code"
		}
		t.Run(name, func(t *testing.T) {
			m, st := enqueueHistoryFixture(t)

			failed := &store.Download{
				ID:          "iparr_failed_" + name,
				PID:         "m00fail1",
				Quality:     "1080p",
				Title:       "Show.S01E01.1080p.WEB-DL.AAC.H264-iParr",
				Status:      store.StatusFailed,
				FailureCode: code,
				CompletedAt: time.Now(),
			}
			if err := st.PutHistory(failed); err != nil {
				t.Fatalf("PutHistory: %v", err)
			}

			id, err := m.Enqueue(failed.PID, failed.Quality, failed.Title, "sonarr")
			if err != nil {
				t.Fatalf("Enqueue: %v", err)
			}
			if id != failed.ID {
				t.Errorf("Enqueue returned %q, want the existing history ID %q: failure code %q must not trigger a re-download",
					id, failed.ID, code)
			}

			active, err := st.ListDownloads()
			if err != nil {
				t.Fatalf("ListDownloads: %v", err)
			}
			if len(active) != 0 {
				t.Errorf("failure code %q created %d new download rows, want 0", code, len(active))
			}
			if hist, _ := st.GetHistory(failed.ID); hist == nil {
				t.Errorf("failure code %q had its history row deleted; only not_yet_available is superseded", code)
			}
		})
	}
}

// TestEnqueue_ReEnqueueDeletesStaleFailedHistoryRow pins the stale-row
// policy: the superseded failed row is removed rather than left beside
// the retry. FindHistoryByPIDQuality iterates with ForEach and keeps a
// single match, so letting failed and completed rows accumulate for one
// pid+quality would make the dedup lookup depend on Bolt key order.
func TestEnqueue_ReEnqueueDeletesStaleFailedHistoryRow(t *testing.T) {
	m, st := enqueueHistoryFixture(t)

	dead := deadNotYetAvailableRow()
	if err := st.PutHistory(dead); err != nil {
		t.Fatalf("PutHistory: %v", err)
	}

	if _, err := m.Enqueue(dead.PID, dead.Quality, dead.Title, "sonarr"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	hist, err := st.GetHistory(dead.ID)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if hist != nil {
		t.Fatalf("stale failed history row %q survived the re-enqueue", dead.ID)
	}

	// The lookup must now be clean: no history row claims this
	// pid+quality, so a second re-enqueue is decided by the live
	// download row alone.
	found, err := st.FindHistoryByPIDQuality(dead.PID, dead.Quality)
	if err != nil {
		t.Fatalf("FindHistoryByPIDQuality: %v", err)
	}
	if found != nil {
		t.Errorf("FindHistoryByPIDQuality still returns %q after the stale row was cleared", found.ID)
	}
}

// TestEnqueue_NotYetAvailableGrabMustNotError is the regression guard for
// the constraint issue #44 established: Enqueue must never return an
// error for an episode BBC has not published yet. The SABnzbd shim's
// addfile surfaces an Enqueue error as a failed grab and Sonarr
// blocklists the release, which is exactly what #44 removed ("defer
// not-yet-available episodes instead of blocklisting"). Deferral lives
// in the worker; the enqueue path must stay quiet and succeed.
//
// The assertion has teeth because the fixture is the state twelve
// not-yet-available retries actually leave behind: a failed history row
// for the same pid+quality. A future change that rejects such a grab,
// or that resurrects the dead ID, fails here.
func TestEnqueue_NotYetAvailableGrabMustNotError(t *testing.T) {
	m, st := enqueueHistoryFixture(t)

	dead := deadNotYetAvailableRow()
	if err := st.PutHistory(dead); err != nil {
		t.Fatalf("PutHistory: %v", err)
	}

	id, err := m.Enqueue(dead.PID, dead.Quality, dead.Title, "sonarr")
	if err != nil {
		t.Fatalf("Enqueue must not error on a not-yet-available grab (Sonarr would blocklist the release): %v", err)
	}
	if id == "" {
		t.Fatal("Enqueue returned an empty id with no error")
	}
	if id == dead.ID {
		t.Fatalf("Enqueue returned the dead history ID %q instead of queueing a retry", dead.ID)
	}

	dl, err := st.GetDownload(id)
	if err != nil {
		t.Fatalf("GetDownload(%s): %v", id, err)
	}
	if dl == nil || dl.Status != store.StatusPending {
		t.Fatalf("expected a queued pending row for %q, got %+v", id, dl)
	}
}
