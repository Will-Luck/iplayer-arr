package store

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// HistoryFilter describes optional filters, sorting, and pagination for
// ListHistoryFiltered.
type HistoryFilter struct {
	Status  string // "completed", "failed", or "" (all)
	Since   string // ISO date string (RFC3339 or date-only) or "" (all time)
	Page    int    // 1-based; defaults to 1
	PerPage int    // defaults to 20
	Sort    string // "completed_at" (default) or "title"
	Order   string // "asc" or "desc" (default)
}

// HistoryPage is the paginated result returned by ListHistoryFiltered.
type HistoryPage struct {
	Items []*Download `json:"items"`
	Total int         `json:"total"`
}

// ListHistoryFiltered reads all history entries, applies status/since filters,
// sorts, and paginates according to f.
func (s *Store) ListHistoryFiltered(f HistoryFilter) (*HistoryPage, error) {
	all, err := s.ListHistory()
	if err != nil {
		return nil, err
	}

	// Apply defaults.
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PerPage < 1 {
		f.PerPage = 20
	}
	if f.Sort == "" {
		f.Sort = "completed_at"
	}
	if f.Order == "" {
		f.Order = "desc"
	}

	// Parse since time if provided.
	var sinceTime time.Time
	if f.Since != "" {
		// Try RFC3339 first, then date-only.
		if t, err2 := time.Parse(time.RFC3339, f.Since); err2 == nil {
			sinceTime = t
		} else if t, err2 := time.Parse("2006-01-02", f.Since); err2 == nil {
			sinceTime = t
		}
	}

	// Filter.
	filtered := make([]*Download, 0, len(all))
	for _, dl := range all {
		if f.Status != "" && !strings.EqualFold(dl.Status, f.Status) {
			continue
		}
		if !sinceTime.IsZero() && dl.CompletedAt.Before(sinceTime) {
			continue
		}
		filtered = append(filtered, dl)
	}

	// Sort.
	sort.Slice(filtered, func(i, j int) bool {
		a, b := filtered[i], filtered[j]
		var less bool
		switch f.Sort {
		case "title":
			less = strings.ToLower(a.Title) < strings.ToLower(b.Title)
		default: // completed_at
			less = a.CompletedAt.Before(b.CompletedAt)
		}
		if f.Order == "asc" {
			return less
		}
		return !less
	})

	total := len(filtered)

	// Paginate.
	start := (f.Page - 1) * f.PerPage
	if start >= total {
		return &HistoryPage{Items: []*Download{}, Total: total}, nil
	}
	end := start + f.PerPage
	if end > total {
		end = total
	}

	return &HistoryPage{
		Items: filtered[start:end],
		Total: total,
	}, nil
}

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
