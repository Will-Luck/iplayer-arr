package store

import (
	"encoding/json"
	"fmt"

	bolt "go.etcd.io/bbolt"
)

func (s *Store) GetHistory(id string) (*Download, error) {
	var dl *Download
	err := s.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(bucketHistory).Get([]byte(id))
		if data == nil {
			return nil
		}
		dl = &Download{}
		return json.Unmarshal(data, dl)
	})
	return dl, err
}

func (s *Store) ListHistory() ([]*Download, error) {
	var history []*Download
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketHistory).ForEach(func(k, v []byte) error {
			var dl Download
			if err := json.Unmarshal(v, &dl); err != nil {
				return err
			}
			history = append(history, &dl)
			return nil
		})
	})
	return history, err
}

func (s *Store) ListHistoryOutputDirs() (map[string]bool, error) {
	dirs := make(map[string]bool)
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketHistory).ForEach(func(k, v []byte) error {
			var dl Download
			if err := json.Unmarshal(v, &dl); err != nil {
				return err
			}
			if dl.OutputDir != "" {
				dirs[dl.OutputDir] = true
			}
			return nil
		})
	})
	return dirs, err
}

func (s *Store) DeleteHistory(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketHistory).Delete([]byte(id))
	})
}

func (s *Store) FindHistoryByPIDQuality(pid, quality string) (*Download, error) {
	var found *Download
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketHistory).ForEach(func(k, v []byte) error {
			var dl Download
			if err := json.Unmarshal(v, &dl); err != nil {
				return err
			}
			if dl.PID == pid && dl.Quality == quality {
				found = &dl
			}
			return nil
		})
	})
	return found, err
}

// PutHistory writes a Download directly to the history bucket (used by MoveToHistory
// and for direct history inserts without a prior downloads entry).
func (s *Store) PutHistory(dl *Download) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(dl)
		if err != nil {
			return fmt.Errorf("marshal history entry: %w", err)
		}
		return tx.Bucket(bucketHistory).Put([]byte(dl.ID), data)
	})
}
