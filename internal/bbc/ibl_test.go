package bbc

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestIBLSearch(t *testing.T) {
	fixture, err := os.ReadFile("testdata/ibl_search.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			t.Error("missing q param")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture)
	}))
	defer srv.Close()

	ibl := NewIBL(NewClient())
	ibl.BaseURL = srv.URL

	results, err := ibl.Search("doctor who", 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1", len(results))
	}

	r := results[0]
	if r.PID != "b039d07m" {
		t.Errorf("PID = %q", r.PID)
	}
	if r.Title != "The Unquiet Dead" {
		t.Errorf("Title = %q", r.Title)
	}
	if r.Series != 1 {
		t.Errorf("Series = %d", r.Series)
	}
	if r.EpisodeNum != 3 {
		t.Errorf("EpisodeNum = %d", r.EpisodeNum)
	}
	if r.Channel != "BBC One" {
		t.Errorf("Channel = %q", r.Channel)
	}
}

func TestListEpisodesPagination(t *testing.T) {
	page1 := `{
		"programme_episodes": {
			"elements": [
				{"id": "ep1", "type": "episode", "title": "Show", "subtitle": "1. First"}
			],
			"page": 1,
			"per_page": 1,
			"count": 2
		}
	}`
	page2 := `{
		"programme_episodes": {
			"elements": [
				{"id": "ep2", "type": "episode", "title": "Show", "subtitle": "2. Second"}
			],
			"page": 2,
			"per_page": 1,
			"count": 2
		}
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pg := r.URL.Query().Get("page")
		w.Header().Set("Content-Type", "application/json")
		if pg == "2" {
			w.Write([]byte(page2))
		} else {
			w.Write([]byte(page1))
		}
	}))
	defer srv.Close()

	ibl := NewIBL(NewClient())
	ibl.BaseURL = srv.URL

	results, err := ibl.ListEpisodes("brand_pid")
	if err != nil {
		t.Fatalf("ListEpisodes: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2 (pagination should fetch both pages)", len(results))
	}
	if results[0].PID != "ep1" || results[1].PID != "ep2" {
		t.Errorf("unexpected PIDs: %s, %s", results[0].PID, results[1].PID)
	}
}
