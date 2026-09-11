package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	bolt "go.etcd.io/bbolt"
)

func (s *Store) PutDownload(dl *Download) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(dl)
		if err != nil {
			return fmt.Errorf("marshal download: %w", err)
		}
		return tx.Bucket(bucketDownloads).Put([]byte(dl.ID), data)
	})
}

func (s *Store) GetDownload(id string) (*Download, error) {
	var dl *Download
	err := s.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(bucketDownloads).Get([]byte(id))
		if data == nil {
			return nil
		}
		dl = &Download{}
		return json.Unmarshal(data, dl)
	})
	return dl, err
}

// ListDownloads returns the active download queue in claim order.
//
// Bolt's ForEach yields keys in byte order and the key is the nzo id, 8
// bytes of crypto/rand rendered as hex. Until GitHub #51 that was the
// queue order: not random, but a stable scramble, identical on every
// poll and across restarts, which is exactly what a bulk Sonarr grab
// looked like from the outside. sortDownloadQueue replaces it with a
// deterministic, human-sensible order. The worker claim loop, the
// dashboard and the SABnzbd queue view all read this one function, so
// all three agree.
//
// The sort is deliberately unconditional. If ForEach aborts partway on a
// decode error it returns both the rows it managed to read and the
// error, and the SABnzbd queue view ignores that error and renders what
// it was given. Sorting the partial slice costs nothing and keeps that
// degraded view consistent with the healthy one.
func (s *Store) ListDownloads() ([]*Download, error) {
	var downloads []*Download
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketDownloads).ForEach(func(k, v []byte) error {
			var dl Download
			if err := json.Unmarshal(v, &dl); err != nil {
				return err
			}
			downloads = append(downloads, &dl)
			return nil
		})
	})
	sortDownloadQueue(downloads)
	return downloads, err
}

// sortDownloadQueue imposes the queue's claim order. GitHub #51.
//
// The keys, in order:
//
//  1. ShowName. Sonarr pushes a bulk grab of two shows interleaved, so
//     grouping by show is what keeps each show's episodes contiguous
//     instead of alternating. Rows with no parsed identity have an empty
//     ShowName and therefore sit at the HEAD of the queue. That is a
//     decision, not an accident of string comparison: movie releases
//     carry no season or episode by construction (moviesearch builds
//     "<name>.<year>.<quality>.WEB-DL..."), so parking unidentified rows
//     at the tail would make every Radarr grab wait behind every Sonarr
//     season. Unidentified rows arrive as rare singletons and identified
//     rows arrive in bursts, so putting the singletons first costs the
//     burst a bounded, first-come-first-served prefix.
//  2. Season, then 3. Episode. This is the binding requirement from the
//     report: within one show's bulk grab, claim in ascending season
//     then episode order. Season 0 is the specials season, so specials
//     lead their show.
//  4. AirDate, for date-tier releases (BBC daily soaps, sports fixtures)
//     where the date is the episode identity and there is no numbering.
//     ISO YYYY-MM-DD sorts correctly as a string.
//  5. CreatedAt. The tiebreaker, and the sole ordering among rows with
//     no identity at all, so those keep strict first-come-first-served.
//  6. ID. Last only, to make the order total. The random-hex id is never
//     a primary key again; that was the bug.
//
// ShowName is compared without case folding: it is derived from titles
// this project generated, so a given show's casing is stable.
//
// Legacy rows written before the identity fields existed decode with all
// four at their zero values, which is the same shape as an unparseable
// title, so they order by CreatedAt and never panic.
func sortDownloadQueue(dls []*Download) {
	sort.Slice(dls, func(i, j int) bool {
		a, b := dls[i], dls[j]
		// Operator-set priority wins over the natural order so a moved
		// row jumps its show grouping. Ties (the common case, all zero)
		// fall through to the episode-order keys. GitHub #51.
		if a.Priority != b.Priority {
			return a.Priority > b.Priority
		}
		if a.ShowName != b.ShowName {
			return a.ShowName < b.ShowName
		}
		if a.Season != b.Season {
			return a.Season < b.Season
		}
		if a.Episode != b.Episode {
			return a.Episode < b.Episode
		}
		if a.AirDate != b.AirDate {
			return a.AirDate < b.AirDate
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		// IDs are Bolt bucket keys, so they are unique and this makes
		// the ordering total.
		return a.ID < b.ID
	})
}

// Queue move operations accepted by MoveDownload.
const (
	MoveTop    = "top"
	MoveUp     = "up"
	MoveDown   = "down"
	MoveBottom = "bottom"
)

var (
	ErrDownloadNotFound   = errors.New("download not found")
	ErrDownloadNotPending = errors.New("download is not pending")
	ErrInvalidMove        = errors.New("invalid move")
)

// MoveDownload repositions one pending download within the pending
// queue and returns the queue in its new order. GitHub #51.
//
// Only pending rows take part. A row that is resolving, downloading,
// converting, or failed-awaiting-retry is already past the point where
// order matters (the worker has claimed it, or the retry clock decides),
// so moving it is refused with ErrDownloadNotPending rather than
// silently reordering something the caller cannot see move.
//
// The pending rows are read, sorted with the same comparator the claim
// loop uses, the target is shifted to its new index, and every pending
// row is renumbered so Priority runs from len at the front down to 1
// at the back. That materialises the whole order explicitly, so "up"
// swaps exactly one neighbour even when the two rows tied on Priority,
// and the result survives restarts. The back row stops at 1, never 0,
// so rows that arrive after a renumber (Priority 0) always append
// behind the arranged block instead of sorting into it by show name,
// which is the behaviour an operator who just arranged the queue
// expects.
//
// The read, shift and writes happen inside one Bolt update transaction,
// so a worker claim cannot interleave: either the worker's status write
// lands before this and the row is refused as not pending, or it lands
// after and overwrites only the row it holds, whose Priority no longer
// matters because it has been claimed.
func (s *Store) MoveDownload(id, op string) ([]*Download, error) {
	switch op {
	case MoveTop, MoveUp, MoveDown, MoveBottom:
	default:
		return nil, fmt.Errorf("%w: %q", ErrInvalidMove, op)
	}

	var pending []*Download
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketDownloads)
		pending = pending[:0]
		var target *Download
		if err := b.ForEach(func(k, v []byte) error {
			var dl Download
			if err := json.Unmarshal(v, &dl); err != nil {
				return err
			}
			if dl.ID == id {
				target = &dl
			}
			if dl.Status == StatusPending {
				pending = append(pending, &dl)
			}
			return nil
		}); err != nil {
			return err
		}
		if target == nil {
			return ErrDownloadNotFound
		}
		if target.Status != StatusPending {
			return ErrDownloadNotPending
		}

		sortDownloadQueue(pending)
		idx := -1
		for i, dl := range pending {
			if dl.ID == id {
				idx = i
				break
			}
		}
		newIdx := idx
		switch op {
		case MoveTop:
			newIdx = 0
		case MoveUp:
			newIdx = idx - 1
		case MoveDown:
			newIdx = idx + 1
		case MoveBottom:
			newIdx = len(pending) - 1
		}
		if newIdx < 0 {
			newIdx = 0
		}
		if newIdx > len(pending)-1 {
			newIdx = len(pending) - 1
		}
		if newIdx != idx {
			moved := pending[idx]
			pending = append(pending[:idx], pending[idx+1:]...)
			pending = append(pending[:newIdx], append([]*Download{moved}, pending[newIdx:]...)...)
		}

		for i, dl := range pending {
			want := len(pending) - i
			if dl.Priority == want {
				continue
			}
			dl.Priority = want
			data, err := json.Marshal(dl)
			if err != nil {
				return fmt.Errorf("marshal download: %w", err)
			}
			if err := b.Put([]byte(dl.ID), data); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return pending, nil
}

// PendingDownloads returns the pending rows in claim order, the same
// slice MoveDownload returns, for callers that need the queue after a
// change made elsewhere.
func (s *Store) PendingDownloads() ([]*Download, error) {
	all, err := s.ListDownloads()
	if err != nil {
		return nil, err
	}
	out := make([]*Download, 0, len(all))
	for _, dl := range all {
		if dl.Status == StatusPending {
			out = append(out, dl)
		}
	}
	return out, nil
}

func (s *Store) DeleteDownload(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketDownloads).Delete([]byte(id))
	})
}

func (s *Store) FindDownloadByPIDQuality(pid, quality string) (*Download, error) {
	var found *Download
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketDownloads).ForEach(func(k, v []byte) error {
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

func (s *Store) MoveToHistory(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		dlBucket := tx.Bucket(bucketDownloads)
		hBucket := tx.Bucket(bucketHistory)

		data := dlBucket.Get([]byte(id))
		if data == nil {
			return fmt.Errorf("download %s not found", id)
		}

		if err := hBucket.Put([]byte(id), data); err != nil {
			return err
		}
		return dlBucket.Delete([]byte(id))
	})
}
