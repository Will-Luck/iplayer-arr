package download

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/store"
)

func throttleTestManager(t *testing.T, maxWorkers int, opts ...ManagerOption) (*Manager, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return NewManager(st, filepath.Join(dir, "downloads"), maxWorkers, nil, nil, nil, nil, opts...), st
}

// TestFailDownload_FreezesBudgetDuringCooldown: a clustered victim's stall
// budget is net-zeroed (frozen) while a cooldown is active, so a synchronised
// season-grab burst is not silently lost. #56 preserved (CDNFailures untouched).
func TestFailDownload_FreezesBudgetDuringCooldown(t *testing.T) {
	m, st := throttleTestManager(t, 2)
	m.throttle.recordStall("other-a")
	m.throttle.recordStall("other-b") // trip the cooldown
	if !m.throttle.inCooldown() {
		t.Fatal("expected a cooldown after two distinct stalls")
	}

	dl := &store.Download{ID: "victim", PID: "p", Status: store.StatusPending, Quality: "1080p"}
	st.PutDownload(dl)

	for i := 0; i < maxStalls+2; i++ {
		m.failDownload(dl, store.FailCodeStalled, fmt.Errorf("stall %d", i))
		if !dl.Retryable {
			t.Fatalf("a frozen victim must stay retryable during cooldown (iter %d)", i)
		}
	}
	if dl.StallCount != 0 {
		t.Errorf("StallCount = %d, want 0 (net-zero freeze)", dl.StallCount)
	}
	if dl.StallCredits != maxStalls+2 {
		t.Errorf("StallCredits = %d, want %d", dl.StallCredits, maxStalls+2)
	}
	if dl.CDNFailures != 0 {
		t.Errorf("freeze must not touch CDNFailures, got %d (#56)", dl.CDNFailures)
	}
}

// TestFailDownload_StallCreditsCeilingReachesPermanence: the freeze is bounded
// by maxStallCredits so a persistently-throttled/dead stream still terminates
// to permanence (handed to Sonarr) rather than looping forever (bounds D3).
func TestFailDownload_StallCreditsCeilingReachesPermanence(t *testing.T) {
	m, st := throttleTestManager(t, 2)
	m.throttle.recordStall("a")
	m.throttle.recordStall("b") // trip and hold (real clock, 180s window)
	dl := &store.Download{ID: "dead", PID: "p", Status: store.StatusPending, Quality: "1080p"}
	st.PutDownload(dl)

	permanentAt := -1
	for i := 0; i < maxStallCredits+maxStalls+3 && permanentAt < 0; i++ {
		m.failDownload(dl, store.FailCodeStalled, fmt.Errorf("stall %d", i))
		if !dl.Retryable {
			permanentAt = i
		}
	}
	if permanentAt < 0 {
		t.Fatal("victim never reached permanence: the freeze is unbounded (D3 regression)")
	}
	if dl.StallCredits != maxStallCredits {
		t.Errorf("StallCredits = %d, want %d (capped)", dl.StallCredits, maxStallCredits)
	}
	if dl.StallCount != maxStalls {
		t.Errorf("StallCount = %d, want %d at permanence", dl.StallCount, maxStalls)
	}
}

// TestProcessNext_GatedDuringCooldown: the admission gate stops new claims
// while a cooldown is active; the pending download is not claimed.
func TestProcessNext_GatedDuringCooldown(t *testing.T) {
	clk := newFakeClock()
	m, st := throttleTestManager(t, 2, WithClock(clk.now))
	m.throttle.recordStall("a")
	m.throttle.recordStall("b")
	if !m.throttle.inCooldown() {
		t.Fatal("expected cooldown")
	}
	dl := &store.Download{ID: "pend", PID: "p", Status: store.StatusPending, Quality: "1080p"}
	st.PutDownload(dl)

	m.processNext(context.Background(), 0)

	got, err := st.GetDownload("pend")
	if err != nil {
		t.Fatalf("GetDownload: %v", err)
	}
	if got.Status != store.StatusPending {
		t.Errorf("download was claimed during cooldown: status=%v, want Pending", got.Status)
	}
	if m.claimedLen() != 0 {
		t.Errorf("claimedLen = %d, want 0 (nothing claimed during cooldown)", m.claimedLen())
	}
}

// TestProcessDownload_RecordsStallForClusterDetection: the full pipeline wires
// recordStall, so two distinct stalling downloads open a cooldown.
func TestProcessDownload_RecordsStallForClusterDetection(t *testing.T) {
	m, st := newSeamHarness(t)
	m.runFFmpeg = func(ctx context.Context, job FFmpegJob) error {
		return fmt.Errorf("ffmpeg watchdog: %w", ErrStalled)
	}
	id1, err := m.Enqueue("b0cluster1", "1080p", "A.S01E01.1080p", "sonarr")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	id2, err := m.Enqueue("b0cluster2", "1080p", "B.S01E02.1080p", "sonarr")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	dl1, _ := st.GetDownload(id1)
	dl2, _ := st.GetDownload(id2)

	m.processDownload(context.Background(), dl1)
	if m.throttle.inCooldown() {
		t.Fatal("one distinct stall should not trip a cooldown (threshold 2)")
	}
	m.processDownload(context.Background(), dl2)
	if !m.throttle.inCooldown() {
		t.Fatal("two distinct stalling downloads should open a cooldown")
	}
}
