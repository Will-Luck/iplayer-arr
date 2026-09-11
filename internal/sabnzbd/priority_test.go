package sabnzbd

import (
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/store"
)

func TestSabPriorityToMove(t *testing.T) {
	cases := map[string]string{
		"2": store.MoveTop, "1": store.MoveTop, "-1": store.MoveBottom,
		"0": "", "-100": "", "-2": "", "": "", "junk": "",
	}
	for in, want := range cases {
		if got := sabPriorityToMove(in); got != want {
			t.Errorf("sabPriorityToMove(%q) = %q, want %q", in, got, want)
		}
	}
}

func seedSabQueue(t *testing.T) (*Handler, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.SetConfig("api_key", "test-key")
	t.Cleanup(func() { st.Close() })
	base := time.Date(2026, 8, 25, 16, 20, 0, 0, time.UTC)
	for i := 1; i <= 3; i++ {
		dl := &store.Download{
			ID: fmt.Sprintf("iparr_e%d", i), PID: fmt.Sprintf("b00%d", i),
			ShowName: "Waterloo Road", Season: 1, Episode: i, Category: "sonarr",
			Status: store.StatusPending, CreatedAt: base.Add(time.Duration(i) * time.Second),
		}
		if err := st.PutDownload(dl); err != nil {
			t.Fatal(err)
		}
	}
	return NewHandler(st, &deletingStarter{st: st}), st
}

// GitHub #51: Sonarr's post-hoc priority change moves the row.
func TestQueuePriority_ForceMovesToTop(t *testing.T) {
	h, st := seedSabQueue(t)

	req := httptest.NewRequest("GET", "/sabnzbd/api?mode=queue&name=priority&value=iparr_e3&value2=2&apikey=test-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	all, _ := st.ListDownloads()
	if all[0].ID != "iparr_e3" {
		t.Errorf("expected iparr_e3 first after Force priority, got %s", all[0].ID)
	}

	req = httptest.NewRequest("GET", "/sabnzbd/api?mode=queue&name=priority&value=iparr_e3&value2=-1&apikey=test-key", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	all, _ = st.ListDownloads()
	if all[len(all)-1].ID != "iparr_e3" {
		t.Errorf("expected iparr_e3 last after Low priority, got %s", all[len(all)-1].ID)
	}
}

// A priority for a row already in flight is ignored, not an error.
func TestQueuePriority_InFlightIgnored(t *testing.T) {
	h, st := seedSabQueue(t)
	dl, _ := st.GetDownload("iparr_e2")
	dl.Status = store.StatusDownloading
	st.PutDownload(dl)

	req := httptest.NewRequest("GET", "/sabnzbd/api?mode=queue&name=priority&value=iparr_e2&value2=2&apikey=test-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	after, _ := st.GetDownload("iparr_e2")
	if after.Priority != 0 || after.Status != store.StatusDownloading {
		t.Errorf("in-flight row changed: %+v", after)
	}
}
