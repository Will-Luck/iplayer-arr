package store

import (
	"encoding/json"

	bolt "go.etcd.io/bbolt"
)

// GetQualityCache returns the cached quality entry for a PID, or
// (nil, nil) if no entry exists. A missing entry is NOT an error —
// callers use the nil-check to distinguish "cache miss, probe fresh"
// from "read error, log and fall through".
func (s *Store) GetQualityCache(pid string) (*QualityCache, error) {
	var qc *QualityCache
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bucketQualityCache).Get([]byte(pid))
		if raw == nil {
			return nil
		}
		var decoded QualityCache
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return err
		}
		qc = &decoded
		return nil
	})
	return qc, err
}

// PutQualityCache upserts a quality cache entry for qc.PID. Before
// writing, qc.ShowName is normalised via normaliseShowName (the same
// helper PutOverride uses in overrides.go:14) so that every entry in
// the bucket has a comparable, case-insensitive show name for the
// future DeleteQualityCacheByShow refresh path.
func (s *Store) PutQualityCache(qc *QualityCache) error {
	qc.ShowName = normaliseShowName(qc.ShowName)
	return s.db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(qc)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketQualityCache).Put([]byte(qc.PID), data)
	})
}

// DeleteQualityCache removes the cache entry for a single PID.
// Missing entries are treated as success.
func (s *Store) DeleteQualityCache(pid string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketQualityCache).Delete([]byte(pid))
	})
}

// DeleteQualityCacheByShow removes every cache entry whose normalised
// ShowName matches the normalised argument. Designed for the future
// v1.2 refresh-by-show UI. The argument is normalised once (via
// normaliseShowName) before the bucket scan, so "Doctor Who",
// "doctor who", "DOCTOR WHO", and " Doctor Who " all match the same
// set of entries.
func (s *Store) DeleteQualityCacheByShow(showName string) error {
	target := normaliseShowName(showName)
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketQualityCache)
		var toDelete [][]byte
		if err := b.ForEach(func(k, v []byte) error {
			var qc QualityCache
			if err := json.Unmarshal(v, &qc); err != nil {
				return nil // skip malformed entries
			}
			if qc.ShowName == target {
				// Copy key because ForEach's k is only valid inside the callback.
				key := make([]byte, len(k))
				copy(key, k)
				toDelete = append(toDelete, key)
			}
			return nil
		}); err != nil {
			return err
		}
		for _, k := range toDelete {
			if err := b.Delete(k); err != nil {
				return err
			}
		}
		return nil
	})
}
