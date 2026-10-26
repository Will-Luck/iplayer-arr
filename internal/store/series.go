package store

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode"

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
// for name means. Returns (nil, nil) when not found. Used by the
// tvsearch handler to rehydrate tvdbid on follow-up queries where Sonarr
// sends q=ShowName with an empty tvdbid.
//
// Names are compared by ShowNameKey, the form Sonarr's title search
// sends a title in: q "Celebrity Traitors" is the stored "The Celebrity
// Traitors" and q "Gavin and Stacey" is "Gavin & Stacey". Gitea #97.
// A stored mapping matches when its ShowName is:
//  1. name itself: stored "Doctor Who" for q "Doctor Who". A year
//     suffix keys as the year, so stored "Doctor Who (2005)" is also q
//     "Doctor Who 2005", the query Sonarr makes of the title "Doctor Who
//     (2005)" (SearchCriteriaBase.GetCleanSceneTitle drops the brackets).
//  2. name with a year suffix: stored "Doctor Who (2005)" for q "Doctor
//     Who". Skyhook returns titles with a trailing " (YYYY)"
//     disambiguator; Sonarr sends the bare title without it on follow-up
//     queries.
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
	target := ShowNameKey(name)
	if target == "" {
		return nil, nil
	}
	var found []SeriesMapping
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketSeries).ForEach(func(_, data []byte) error {
			var m SeriesMapping
			if err := json.Unmarshal(data, &m); err != nil {
				return nil // skip malformed entries, keep scanning
			}
			if seriesNameIs(m.ShowName, name, target) {
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

// seriesNameIs reports whether a stored ShowName is the series a query
// for name, whose ShowNameKey is target, names: the name itself, or the
// name with a " (YYYY)" suffix. A stored name that ends in another
// bracket, a country such as "Ghosts (US)", keeps its brackets: it is
// told from "Ghosts US" as before, because the stored name would become
// the name filter, which iPlayer's own "Ghosts US" never matches.
func seriesNameIs(stored, name, target string) bool {
	if _, ok := cutYearSuffix(strings.TrimSpace(stored)); !ok && strings.HasSuffix(strings.TrimSpace(stored), ")") {
		return strings.EqualFold(strings.TrimSpace(stored), strings.TrimSpace(name))
	}
	if ShowNameKey(stored) == target {
		return true
	}
	base, ok := cutYearSuffix(strings.TrimSpace(stored))
	return ok && ShowNameKey(base) == target
}

// cutYearSuffix splits "Doctor Who (2005)" into "Doctor Who". ok is
// false when s does not end in " (YYYY)" with exactly four digits.
func cutYearSuffix(s string) (base string, ok bool) {
	i := strings.LastIndex(s, " (")
	if i < 0 {
		return "", false
	}
	rest := s[i+2:]
	if len(rest) != 5 || !isFourDigits(rest[:4]) || rest[4] != ')' {
		return "", false
	}
	return s[:i], true
}

// showNameDropped are the characters Sonarr deletes from a title before
// it turns every other non-word character into a separator
// (SearchCriteriaBase.SpecialCharacter): "Mrs. Brown's Boys" is searched
// as "Mrs Browns Boys".
var showNameDropped = strings.NewReplacer("'", "", ".", "", "`", "", "\u00b4", "", "\u2018", "", "\u2019", "")

// ShowNameKey reduces a show name to the form Sonarr's title search
// sends it in (SearchCriteriaBase.GetCleanSceneTitle), for comparing
// names: lower case, "&" as "and", apostrophes and dots deleted, any
// other character that is not a letter or a digit a space, spaces
// collapsed, and one leading "the" dropped. "The Celebrity Traitors"
// and "Celebrity Traitors" have the same key, and so do "David Fuller:
// Monster in the Morgue" and "David Fuller Monster In The Morgue".
// Accents are kept, where Sonarr strips them. Gitea #97.
func ShowNameKey(name string) string {
	s := ShowNameKeyKeepArticle(name)
	if rest, ok := strings.CutPrefix(s, "the "); ok {
		s = rest
	}
	return s
}

// ShowNameKeyKeepArticle is ShowNameKey without the fold of a leading
// "The". It is the key for a name that is TheTVDB's, where "Missing" and
// "The Missing" are different shows.
func ShowNameKeyKeepArticle(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, "&", "and")
	s = showNameDropped.Replace(s)
	s = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return ' '
	}, s)
	return strings.Join(strings.Fields(s), " ")
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
