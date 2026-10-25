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

// ErrAmbiguousSeriesName is GetSeriesMappingByName's answer for a name
// more than one stored series could be. Gitea #104.
var ErrAmbiguousSeriesName = errors.New("name matches more than one stored series")

// GetSeriesMappingByName returns the SeriesMapping a Sonarr text query
// for name means (case-insensitive). Returns (nil, nil) when not found.
// Used by the tvsearch handler to rehydrate tvdbid on follow-up queries
// where Sonarr sends q=ShowName with an empty tvdbid.
//
// A stored mapping matches when its ShowName is:
//  1. name itself: stored "Doctor Who" for q "Doctor Who".
//  2. name with a year suffix: stored "Doctor Who (2005)" for q "Doctor
//     Who". Skyhook returns titles with a trailing " (YYYY)"
//     disambiguator; Sonarr sends the bare title without it on follow-up
//     queries.
//  3. for a name ending in a year, name with that year in brackets:
//     stored "Doctor Who (2005)" for q "Doctor Who 2005", the query
//     Sonarr makes of the title "Doctor Who (2005)"
//     (SearchCriteriaBase.GetCleanSceneTitle drops the brackets).
//
// When more than one mapping matches, it returns ErrAmbiguousSeriesName
// rather than picking one. With Doctor Who (2005) and (2023) stored, q
// "Doctor Who" used to come back as whichever bolt's key order put
// first, 449991, although Sonarr sends that query for Doctor Who (2005)
// (a scene alias) and for the classic Doctor Who, and its scene names
// for 449991 have no bare "Doctor Who" (Sonarr's scene mappings,
// 2026-10-25). Gitea #104.
//
// Cost is O(n) in the number of tracked shows (one bucket scan per call).
// Typical deployments track tens to low hundreds of shows, so the linear
// scan is cheaper than maintaining a secondary name-index bucket. If
// this ever becomes a hot path, add a `bucketSeriesByName` secondary
// index that mirrors writes from PutSeriesMapping.
func (s *Store) GetSeriesMappingByName(name string) (*SeriesMapping, error) {
	target := strings.ToLower(strings.TrimSpace(name))
	if target == "" {
		return nil, nil
	}
	// withYear is target as a year-suffixed title, "doctor who (2005)"
	// for "doctor who 2005", or "" when target does not end in a year.
	var withYear string
	if base, year, ok := cutTrailingYear(target); ok {
		withYear = base + " (" + year + ")"
	}
	var found []SeriesMapping
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketSeries).ForEach(func(_, data []byte) error {
			var m SeriesMapping
			if err := json.Unmarshal(data, &m); err != nil {
				return nil // skip malformed entries, keep scanning
			}
			stored := strings.ToLower(strings.TrimSpace(m.ShowName))
			if stored == target || stored == withYear || isYearSuffixed(stored, target) {
				found = append(found, m)
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	switch len(found) {
	case 0:
		return nil, nil
	case 1:
		return &found[0], nil
	}
	return nil, ErrAmbiguousSeriesName
}

// isYearSuffixed reports whether stored is name + " (YYYY)" where YYYY
// is exactly four ASCII digits.
func isYearSuffixed(stored, name string) bool {
	rest, ok := strings.CutPrefix(stored, name+" (")
	return ok && len(rest) == 5 && isFourDigits(rest[:4]) && rest[4] == ')'
}

// cutTrailingYear splits "doctor who 2005" into "doctor who" and "2005".
func cutTrailingYear(s string) (base, year string, ok bool) {
	i := strings.LastIndexByte(s, ' ')
	if i < 1 || !isFourDigits(s[i+1:]) {
		return "", "", false
	}
	return strings.TrimSpace(s[:i]), s[i+1:], true
}

func isFourDigits(s string) bool {
	if len(s) != 4 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ListSeriesMappings returns every stored SeriesMapping, skipping
// malformed entries as GetSeriesMappingByName does. The RSS feed uses
// them to number the episodes of shows Sonarr has searched by tvdbid.
// GitHub #60.
func (s *Store) ListSeriesMappings() ([]SeriesMapping, error) {
	var out []SeriesMapping
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketSeries).ForEach(func(_, data []byte) error {
			var m SeriesMapping
			if err := json.Unmarshal(data, &m); err != nil {
				return nil
			}
			out = append(out, m)
			return nil
		})
	})
	return out, err
}
