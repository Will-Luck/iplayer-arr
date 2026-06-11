package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/download"
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

// testAPIWithManager wires a real (unstarted) download.Manager into the
// handler so tests can observe the exact title handed to Enqueue. Workers
// are never started, so enqueued rows stay pending and nothing touches
// the network.
func testAPIWithManager(t *testing.T) (*Handler, *store.Store) {
	t.Helper()
	h, st := testAPI(t)
	h.mgr = download.NewManager(st, t.TempDir(), 1, nil, nil, nil, NewHub())
	return h, st
}

func postManualDownload(t *testing.T, h *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/download?apikey=test-api-key", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// Issue #48: manual downloads from the web GUI passed the bare brand
// title ("EastEnders") straight to Enqueue, so daily-soap files saved as
// EastEnders/EastEnders.mp4 with no date and Sonarr could not import
// them. When the request carries episode metadata the handler must build
// the same release title the Newznab feed advertises.
func TestManualDownload_DailySoapBuildsDateTitle(t *testing.T) {
	h, st := testAPIWithManager(t)

	body := `{"pid":"m002xjwx","quality":"720p","title":"EastEnders","subtitle":"11/06/2026","position":7346,"airDate":"2026-06-11"}`
	if w := postManualDownload(t, h, body); w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}

	dl, err := st.FindDownloadByPIDQuality("m002xjwx", "720p")
	if err != nil || dl == nil {
		t.Fatalf("download not enqueued: %v", err)
	}
	want := "EastEnders.2026.06.11.720p.WEB-DL.AAC.H264-iParr"
	if dl.Title != want {
		t.Errorf("title = %q, want %q", dl.Title, want)
	}
}

func TestManualDownload_SeriesEpisodeBuildsSxxExxTitle(t *testing.T) {
	h, st := testAPIWithManager(t)

	body := `{"pid":"m000abcd","quality":"1080p","title":"Doctor Who","subtitle":"The Robot Revolution","series":2,"episodeNum":1,"airDate":"2026-04-12"}`
	if w := postManualDownload(t, h, body); w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}

	dl, err := st.FindDownloadByPIDQuality("m000abcd", "1080p")
	if err != nil || dl == nil {
		t.Fatalf("download not enqueued: %v", err)
	}
	want := "Doctor.Who.S02E01.The.Robot.Revolution.1080p.WEB-DL.AAC.H264-iParr"
	if dl.Title != want {
		t.Errorf("title = %q, want %q", dl.Title, want)
	}
}

// A force-date-based show override must apply to manual downloads the
// same way it applies to the indexer feed. Without the override this
// payload would emit S01E7346 via the position tier.
func TestManualDownload_OverrideForceDateBased(t *testing.T) {
	h, st := testAPIWithManager(t)
	if err := st.PutOverride(&store.ShowOverride{ShowName: "EastEnders", ForceDateBased: true}); err != nil {
		t.Fatal(err)
	}

	body := `{"pid":"m002xjwy","quality":"720p","title":"EastEnders","subtitle":"Late night edition","position":7346,"airDate":"2026-06-11"}`
	if w := postManualDownload(t, h, body); w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}

	dl, err := st.FindDownloadByPIDQuality("m002xjwy", "720p")
	if err != nil || dl == nil {
		t.Fatalf("download not enqueued: %v", err)
	}
	want := "EastEnders.2026.06.11.Late.night.edition.720p.WEB-DL.AAC.H264-iParr"
	if dl.Title != want {
		t.Errorf("title = %q, want %q", dl.Title, want)
	}
}

// BBC long-runners (Casualty, One Piece 1999) carry subtitles like
// "Learning Curve Episode 3" that parse to Series=0, EpisodeNum=3. The
// feed path promotes these to Series=1 (GitHub #32) so Sonarr can match
// them; the manual path must apply the same promotion or it would fall
// through to the position tier and emit a wrong S01E<position> title.
func TestManualDownload_EpisodeNumberOnlyPromotesSeries1(t *testing.T) {
	h, st := testAPIWithManager(t)

	body := `{"pid":"m000wxyz","quality":"720p","title":"Casualty","subtitle":"Learning Curve Episode 3","episodeNum":3,"position":1303,"airDate":"2026-05-02"}`
	if w := postManualDownload(t, h, body); w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}

	dl, err := st.FindDownloadByPIDQuality("m000wxyz", "720p")
	if err != nil || dl == nil {
		t.Fatalf("download not enqueued: %v", err)
	}
	want := "Casualty.S01E03.Learning.Curve.Episode.3.720p.WEB-DL.AAC.H264-iParr"
	if dl.Title != want {
		t.Errorf("title = %q, want %q", dl.Title, want)
	}
}

// Requests without any episode metadata (the pre-#48 wire format, e.g.
// curl users supplying a deliberate custom title) keep the raw title
// byte-for-byte.
func TestManualDownload_NoMetadataKeepsRawTitle(t *testing.T) {
	h, st := testAPIWithManager(t)

	body := `{"pid":"b039d07m","quality":"720p","title":"My Custom Name"}`
	if w := postManualDownload(t, h, body); w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}

	dl, err := st.FindDownloadByPIDQuality("b039d07m", "720p")
	if err != nil || dl == nil {
		t.Fatalf("download not enqueued: %v", err)
	}
	if dl.Title != "My Custom Name" {
		t.Errorf("title = %q, want raw title preserved", dl.Title)
	}
}
