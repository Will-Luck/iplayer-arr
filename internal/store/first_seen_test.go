package store

import (
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func TestFirstSeen_StampsNewPIDs(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	got, err := s.GetOrSetFirstSeenBatch([]string{"p1", "p2"}, now)
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	for _, pid := range []string{"p1", "p2"} {
		if !got[pid].Equal(now) {
			t.Errorf("%s = %v, want %v", pid, got[pid], now)
		}
	}
}

func TestFirstSeen_SecondCallReturnsOriginalStamp(t *testing.T) {
	s := newTestStore(t)
	first := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	if _, err := s.GetOrSetFirstSeenBatch([]string{"p1"}, first); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	later := first.Add(48 * time.Hour)
	got, err := s.GetOrSetFirstSeenBatch([]string{"p1"}, later)
	if err != nil {
		t.Fatalf("second batch: %v", err)
	}
	if !got["p1"].Equal(first) {
		t.Errorf("second call returned %v, want original stamp %v", got["p1"], first)
	}
}

func TestFirstSeen_BatchMixesNewAndExisting(t *testing.T) {
	s := newTestStore(t)
	first := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	if _, err := s.GetOrSetFirstSeenBatch([]string{"existing"}, first); err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	later := first.Add(time.Hour)
	got, err := s.GetOrSetFirstSeenBatch([]string{"existing", "fresh"}, later)
	if err != nil {
		t.Fatalf("mixed batch: %v", err)
	}
	if !got["existing"].Equal(first) {
		t.Errorf("existing = %v, want original %v", got["existing"], first)
	}
	if !got["fresh"].Equal(later) {
		t.Errorf("fresh = %v, want %v", got["fresh"], later)
	}
}

func TestFirstSeen_PurgeRemovesOnlyStaleEntries(t *testing.T) {
	s := newTestStore(t)
	stale := time.Now().UTC().Truncate(time.Second).Add(-100 * 24 * time.Hour)
	fresh := time.Now().UTC().Truncate(time.Second)
	if _, err := s.GetOrSetFirstSeenBatch([]string{"old1", "old2"}, stale); err != nil {
		t.Fatalf("seed stale: %v", err)
	}
	if _, err := s.GetOrSetFirstSeenBatch([]string{"new1"}, fresh); err != nil {
		t.Fatalf("seed fresh: %v", err)
	}
	n, err := s.PurgeStaleFirstSeen(90 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 2 {
		t.Errorf("purged = %d, want 2", n)
	}
	// Purged PIDs re-stamp with the probe time; the fresh PID keeps
	// its original stamp.
	probe := fresh.Add(time.Minute)
	got, err := s.GetOrSetFirstSeenBatch([]string{"old1", "new1"}, probe)
	if err != nil {
		t.Fatalf("post-purge batch: %v", err)
	}
	if !got["old1"].Equal(probe) {
		t.Errorf("old1 = %v, want re-stamp %v", got["old1"], probe)
	}
	if !got["new1"].Equal(fresh) {
		t.Errorf("new1 = %v, want original %v", got["new1"], fresh)
	}
}

func TestFirstSeen_GetBatchReadsWithoutStamping(t *testing.T) {
	s := newTestStore(t)
	stamp := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	if _, err := s.GetOrSetFirstSeenBatch([]string{"stamped"}, stamp); err != nil {
		t.Fatalf("seed batch: %v", err)
	}

	got, err := s.GetFirstSeenBatch([]string{"stamped", "missing"})
	if err != nil {
		t.Fatalf("read batch: %v", err)
	}
	if !got["stamped"].Equal(stamp) {
		t.Errorf("stamped = %v, want %v", got["stamped"], stamp)
	}
	if ts, ok := got["missing"]; ok {
		t.Errorf("missing PID present with %v; want absent", ts)
	}

	// The lookup must not have created an entry for the missing PID.
	if err := s.db.View(func(tx *bolt.Tx) error {
		if v := tx.Bucket(bucketFirstSeen).Get([]byte("missing")); v != nil {
			t.Errorf("read-only batch wrote %q for missing PID", v)
		}
		return nil
	}); err != nil {
		t.Fatalf("inspect bucket: %v", err)
	}
}

func TestFirstSeen_GetBatchSkipsMalformedValues(t *testing.T) {
	s := newTestStore(t)
	if err := s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketFirstSeen).Put([]byte("bad"), []byte("not-a-timestamp"))
	}); err != nil {
		t.Fatalf("seed malformed value: %v", err)
	}

	got, err := s.GetFirstSeenBatch([]string{"bad"})
	if err != nil {
		t.Fatalf("read batch: %v", err)
	}
	if ts, ok := got["bad"]; ok {
		t.Errorf("malformed value surfaced as %v; want skipped", ts)
	}
}
