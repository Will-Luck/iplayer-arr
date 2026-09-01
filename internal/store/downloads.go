package store

import (
	"encoding/json"
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
