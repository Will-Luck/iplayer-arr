package store

import (
	"encoding/json"
	"errors"
	"strings"

	bolt "go.etcd.io/bbolt"
)

func (s *Store) PutSeriesMapping(m *SeriesMapping) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(m)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketSeries).Put([]byte(m.TVDBId), data)
	})
}

func (s *Store) GetSeriesMapping(tvdbId string) (*SeriesMapping, error) {
	var m *SeriesMapping
	err := s.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(bucketSeries).Get([]byte(tvdbId))
		if data == nil {
			return nil
		}
		m = &SeriesMapping{}
		return json.Unmarshal(data, m)
	})
	return m, err
}

// GetSeriesMappingByName returns the first SeriesMapping whose ShowName
// matches name (case-insensitive). Returns (nil, nil) when not found.
// Used by the tvsearch handler to rehydrate tvdbid on follow-up queries
// where Sonarr sends q=ShowName with an empty tvdbid.
//
// Matching rules (applied in order, first match wins):
//  1. Exact case-insensitive match: stored "Doctor Who" == q "Doctor Who".
//  2. Year-suffix match: stored "Doctor Who (2005)" matches q "Doctor Who".
//     Skyhook returns titles with a trailing " (YYYY)" disambiguator; Sonarr
//     sends the bare title without it on follow-up queries.
//
// Cost is O(n) in the number of tracked shows (one bucket scan per call).
// Typical deployments track tens to low hundreds of shows, so the linear
// scan is cheaper than maintaining a secondary name-index bucket. If
// this ever becomes a hot path, add a `bucketSeriesByName` secondary
// index that mirrors writes from PutSeriesMapping.
func (s *Store) GetSeriesMappingByName(name string) (*SeriesMapping, error) {
	if name == "" {
		return nil, nil
	}
	target := strings.ToLower(strings.TrimSpace(name))
	var found *SeriesMapping
	// yearSuffixMatch reports whether stored (lowercase) equals
	// target + " (YYYY)" where YYYY is exactly four ASCII digits.
	yearSuffixMatch := func(stored string) bool {
		prefix := target + " ("
		if !strings.HasPrefix(stored, prefix) {
			return false
		}
		rest := stored[len(prefix):]
		// rest must be exactly "YYYY)" -- 5 chars
		if len(rest) != 5 {
			return false
		}
		for _, c := range rest[:4] {
			if c < '0' || c > '9' {
				return false
			}
		}
		return rest[4] == ')'
	}
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketSeries).ForEach(func(_, data []byte) error {
			var m SeriesMapping
			if err := json.Unmarshal(data, &m); err != nil {
				return nil // skip malformed entries, keep scanning
			}
			stored := strings.ToLower(strings.TrimSpace(m.ShowName))
			if stored == target || yearSuffixMatch(stored) {
				found = &m
				return errStopIteration
			}
			return nil
		})
	})
	if err == errStopIteration {
		err = nil
	}
	return found, err
}

// errStopIteration short-circuits a bolt.Bucket.ForEach once the target
// row is found. Any non-nil return from ForEach's callback ends the
// scan; we use a sentinel so the caller can distinguish "stopped early"
// from a real error.
var errStopIteration = errors.New("iteration stopped")
