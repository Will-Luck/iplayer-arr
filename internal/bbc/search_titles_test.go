package bbc

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// GitHub #58: SearchTitles lists every search hit, including a brand
// whose episode listing failed, and returns the same episodes as Search.
func TestSearchTitles_KeepsBrandWhoseListingFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/new-search":
			w.Write([]byte(`{"new_search":{"results":[
				{"id":"b_parent","type":"programme","title":"Show"},
				{"id":"b_comp","type":"programme","title":"Show: Companion"},
				{"id":"e_one","type":"episode","title":"Other","subtitle":"Series 1: Episode 1"}
			]}}`))
		case "/programmes/b_comp/episodes":
			w.Write([]byte(`{"programme_episodes":{"page":1,"per_page":200,"count":1,"elements":[
				{"id":"c1","type":"episode","title":"Show: Companion","subtitle":"Series 1: Episode 1"}
			]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ibl := NewIBL(NewClient())
	ibl.BaseURL = srv.URL

	results, titles, err := ibl.SearchTitles("Show", 1)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Show", "Show: Companion", "Other"}; !slices.Equal(titles, want) {
		t.Errorf("titles = %q, want %q", titles, want)
	}
	var pids []string
	for _, r := range results {
		pids = append(pids, r.PID)
	}
	if want := []string{"c1", "e_one"}; !slices.Equal(pids, want) {
		t.Errorf("result PIDs = %q, want %q", pids, want)
	}

	plain, err := ibl.Search("Show", 1)
	if err != nil {
		t.Fatal(err)
	}
	var plainPIDs []string
	for _, r := range plain {
		plainPIDs = append(plainPIDs, r.PID)
	}
	if !slices.Equal(plainPIDs, pids) {
		t.Errorf("Search returned %q, SearchTitles %q", plainPIDs, pids)
	}
}
