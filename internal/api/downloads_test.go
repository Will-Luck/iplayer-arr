package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// TestCancelDownload_MovesToHistory exercises issue #27: a thin
// DELETE /api/downloads/:id route should cancel the download (best-effort
// killing of the worker context when a manager is wired) and move the
// row out of the downloads bucket into history so the UI no longer
// shows it as active.
func TestCancelDownload_MovesToHistory(t *testing.T) {
	h, st := testAPI(t)

	dl := &store.Download{
		ID:        "iparr_cancel1",
		PID:       "b00cancel",
		Title:     "Test.Show.S01E01",
		Status:    store.StatusDownloading,
		Category:  "sonarr",
		OutputDir: "/tmp/Test.Show.S01E01",
	}
	if err := st.PutDownload(dl); err != nil {
		t.Fatalf("PutDownload: %v", err)
	}

	req := httptest.NewRequest("DELETE", "/api/downloads/iparr_cancel1?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, body: %s", w.Code, w.Body.String())
	}

	dls, _ := st.ListDownloads()
	for _, d := range dls {
		if d.ID == "iparr_cancel1" {
			t.Fatalf("download still in downloads bucket after cancel")
		}
	}

	hist, _ := st.ListHistory()
	found := false
	for _, d := range hist {
		if d.ID == "iparr_cancel1" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("download not present in history after cancel")
	}
}

// TestCancelDownload_UnknownID returns 200 (idempotent: the download
// may have already finished or been cancelled in another tab).
func TestCancelDownload_UnknownID(t *testing.T) {
	h, _ := testAPI(t)
	req := httptest.NewRequest("DELETE", "/api/downloads/iparr_nope?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for unknown id (idempotent), got %d", w.Code)
	}
}
