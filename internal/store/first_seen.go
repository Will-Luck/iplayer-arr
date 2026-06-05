package store

import (
	"time"

	bolt "go.etcd.io/bbolt"
)

// GetOrSetFirstSeenBatch returns the timestamp each PID first appeared
// in the RSS feed, stamping now for any PID without an existing entry.
// The whole batch runs in a single Update transaction: Bolt fsyncs per
// transaction, so per-PID transactions would cost one disk sync per
// item on every RSS poll. Issue #47.
func (s *Store) GetOrSetFirstSeenBatch(pids []string, now time.Time) (map[string]time.Time, error) {
	seen := make(map[string]time.Time, len(pids))
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketFirstSeen)
		for _, pid := range pids {
			if raw := b.Get([]byte(pid)); raw != nil {
				if ts, err := time.Parse(time.RFC3339, string(raw)); err == nil {
					seen[pid] = ts
					continue
				}
				// Malformed value: fall through and re-stamp.
			}
			if err := b.Put([]byte(pid), []byte(now.Format(time.RFC3339))); err != nil {
				return err
			}
			seen[pid] = now
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return seen, nil
}

// GetFirstSeenBatch returns the existing first-seen timestamp for each
// PID that already has one, without stamping anything. The q=/tvdbid=
// search paths use this read-only variant so an already-stamped PID
// advertises the same pubDate as the RSS feed while unstamped PIDs
// keep their availability-derived fallback; only the wildcard browse
// path may create stamps (see writeResultsRSS). Malformed values are
// skipped -- the browse path re-stamps them. Issue #47.
func (s *Store) GetFirstSeenBatch(pids []string) (map[string]time.Time, error) {
	seen := make(map[string]time.Time, len(pids))
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketFirstSeen)
		for _, pid := range pids {
			raw := b.Get([]byte(pid))
			if raw == nil {
				continue
			}
			if ts, err := time.Parse(time.RFC3339, string(raw)); err == nil {
				seen[pid] = ts
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return seen, nil
}

// PurgeStaleFirstSeen deletes first-seen entries older than maxAge and
// reports how many were removed. Entries that no longer parse are
// treated as stale -- they cannot be compared against the cutoff and
// would otherwise sit in the bucket forever.
func (s *Store) PurgeStaleFirstSeen(maxAge time.Duration) (int, error) {
	cutoff := time.Now().Add(-maxAge)
	purged := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		c := tx.Bucket(bucketFirstSeen).Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			ts, err := time.Parse(time.RFC3339, string(v))
			if err == nil && !ts.Before(cutoff) {
				continue
			}
			if err := c.Delete(); err != nil {
				return err
			}
			purged++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return purged, nil
}
