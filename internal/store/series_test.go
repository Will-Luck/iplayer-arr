package store

import (
	"errors"
	"testing"
)

// Gitea #97. Sonarr's title search (tier 2) sends a series' title
// cleaned by SearchCriteriaBase.GetCleanSceneTitle: a leading "The"
// dropped, "&" as "and", apostrophes and dots deleted, other punctuation
// as separators. q=Celebrity Traitors did not find the stored "The
// Celebrity Traitors", so the search lost its tvdbid.
func TestGetSeriesMappingByName_Gitea97SonarrTitleTier(t *testing.T) {
	s := testStore(t)
	for _, m := range []*SeriesMapping{
		{TVDBId: "453660", ShowName: "The Celebrity Traitors", Year: 2025},
		{TVDBId: "80435", ShowName: "Gavin & Stacey", Year: 2007},
		{TVDBId: "219631", ShowName: "Mrs. Brown's Boys", Year: 2011},
		{TVDBId: "72150", ShowName: "Later... with Jools Holland", Year: 1992},
		{TVDBId: "78804", ShowName: "Doctor Who (2005)", Year: 2005},
	} {
		if err := s.PutSeriesMapping(m); err != nil {
			t.Fatal(err)
		}
	}
	for q, want := range map[string]string{
		"Celebrity Traitors":       "453660",
		"The Celebrity Traitors":   "453660",
		"celebrity traitors":       "453660",
		"Gavin and Stacey":         "80435",
		"Mrs Browns Boys":          "219631",
		"Later with Jools Holland": "72150",
		"Doctor Who 2005":          "78804",
		"Doctor Who":               "78804",
	} {
		got, err := s.GetSeriesMappingByName(q)
		if err != nil || got == nil || got.TVDBId != want {
			t.Errorf("GetSeriesMappingByName(%q) = %+v, %v; want TVDBId %s", q, got, err, want)
		}
	}
	for _, q := range []string{"Traitors", "Celebrity", "Doctor Who 2023", "Gavin"} {
		if got, err := s.GetSeriesMappingByName(q); got != nil || err != nil {
			t.Errorf("GetSeriesMappingByName(%q) = %+v, %v; want nil, nil", q, got, err)
		}
	}
}

// Folding must not merge two stored series: a bare name both could be
// is still not answered, and the year still picks one.
func TestGetSeriesMappingByName_Gitea97FoldingKeepsAmbiguity(t *testing.T) {
	s := testStore(t)
	for _, m := range []*SeriesMapping{
		{TVDBId: "81208", ShowName: "Gladiators", Year: 1992},
		{TVDBId: "435784", ShowName: "Gladiators (2024)", Year: 2024},
	} {
		if err := s.PutSeriesMapping(m); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := s.GetSeriesMappingByName("Gladiators"); got != nil || !errors.Is(err, ErrAmbiguousSeriesName) {
		t.Errorf("GetSeriesMappingByName(Gladiators) = %+v, %v; want nil, ErrAmbiguousSeriesName", got, err)
	}
	if got, err := s.GetSeriesMappingByName("Gladiators 2024"); err != nil || got == nil || got.TVDBId != "435784" {
		t.Errorf("GetSeriesMappingByName(Gladiators 2024) = %+v, %v; want TVDBId 435784", got, err)
	}
}

// ShowNameKey is the form Sonarr's title search sends a series' title
// in (SearchCriteriaBase.GetCleanSceneTitle), apart from accents. Only
// one leading "the" goes, and only as a word.
func TestShowNameKey(t *testing.T) {
	for in, want := range map[string]string{
		"The Celebrity Traitors":              "celebrity traitors",
		"Celebrity Traitors":                  "celebrity traitors",
		"David Fuller: Monster in the Morgue": "david fuller monster in the morgue",
		"Gavin & Stacey":                      "gavin and stacey",
		"Mrs. Brown's Boys":                   "mrs browns boys",
		"Mrs Brown\u2019s Boys":               "mrs browns boys",
		"Later... with Jools Holland":         "later with jools holland",
		"Inside No. 9":                        "inside no 9",
		"Doctor Who (2005)":                   "doctor who 2005",
		"Doctor Who (2005\u20132022)":         "doctor who 2005 2022",
		"  The   Office  ":                    "office",
		"The The":                             "the",
		"Theatre Live":                        "theatre live",
		"The":                                 "the",
		"":                                    "",
	} {
		if got := ShowNameKey(in); got != want {
			t.Errorf("ShowNameKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// A stored name with a country suffix is not the same series as the name
// without it: "Ghosts (US)" is not q "Ghosts US" (the survey's warm title
// tier lost every Ghosts US item when it was, because the stored name
// "Ghosts (US)" then became a name filter that iPlayer's "Ghosts US" never
// matches). Only a year suffix folds into the query. Gitea #97.
func TestGetSeriesMappingByName_Gitea97CountrySuffixIsKept(t *testing.T) {
	s := testStore(t)
	if err := s.PutSeriesMapping(&SeriesMapping{TVDBId: "400267", ShowName: "Ghosts (US)", Year: 2021}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"Ghosts US", "Ghosts", "ghosts  us"} {
		if got, err := s.GetSeriesMappingByName(q); got != nil || err != nil {
			t.Errorf("GetSeriesMappingByName(%q) = %+v, %v; want nil, nil", q, got, err)
		}
	}
	if got, err := s.GetSeriesMappingByName("ghosts (us)"); err != nil || got == nil || got.TVDBId != "400267" {
		t.Errorf("GetSeriesMappingByName(ghosts (us)) = %+v, %v; want TVDBId 400267", got, err)
	}
}
