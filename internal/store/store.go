package store

import (
	"fmt"

	bolt "go.etcd.io/bbolt"
)

var (
	bucketDownloads    = []byte("downloads")
	bucketHistory      = []byte("history")
	bucketProgrammes   = []byte("programmes")
	bucketSeries       = []byte("series")
	bucketOverrides    = []byte("overrides")
	bucketConfig       = []byte("config")
	bucketQualityCache = []byte("quality_cache")
)

type Store struct {
	db *bolt.DB
}

func Open(path string) (*Store, error) {
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		return nil, fmt.Errorf("open bolt db: %w", err)
	}

	err = db.Update(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{
			bucketDownloads, bucketHistory, bucketProgrammes,
			bucketSeries, bucketOverrides, bucketConfig,
			bucketQualityCache,
		} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return fmt.Errorf("create bucket %s: %w", b, err)
			}
		}

		// Seed config defaults (only when key is absent).
		cfg := tx.Bucket(bucketConfig)
		defaults := map[string]string{
			"auto_cleanup": "true",
		}
		for k, v := range defaults {
			if cfg.Get([]byte(k)) == nil {
				if err := cfg.Put([]byte(k), []byte(v)); err != nil {
					return fmt.Errorf("seed default %s: %w", k, err)
				}
			}
		}

		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}
