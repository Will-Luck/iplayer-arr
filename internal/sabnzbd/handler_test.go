package sabnzbd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// mockStarter records StartDownload calls and creates real store entries.
type mockStarter struct {
	st    *store.Store
	calls int
}

func (m *mockStarter) StartDownload(pid, quality, title, category string) (string, error) {
	m.calls++
	id := fmt.Sprintf("iparr_%s_%s", pid, quality)
	dl := &store.Download{
		ID:       id,
		PID:      pid,
		Quality:  quality,
		Title:    title,
		Category: category,
		Status:   store.StatusPending,
	}
	if err := m.st.PutDownload(dl); err != nil {
		return "", err
	}
	return id, nil
}

func (m *mockStarter) CancelDownload(nzoID string) error {
	return nil
}

func (m *mockStarter) IsPaused() bool {
	return false
}

func testHandler(t *testing.T) (*Handler, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.SetConfig("api_key", "test-key")
	t.Cleanup(func() { st.Close() })
	h := NewHandler(st, nil)
	return h, st
}

func TestVersionNoAuth(t *testing.T) {
	h, _ := testHandler(t)
	req := httptest.NewRequest("GET", "/sabnzbd/api?mode=version", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	var vResp struct{ Version string }
	json.Unmarshal(w.Body.Bytes(), &vResp)
	if vResp.Version != "4.0.0" {
		t.Errorf("version = %q", vResp.Version)
	}
}

func TestAuthRequired(t *testing.T) {
	h, _ := testHandler(t)
	req := httptest.NewRequest("GET", "/sabnzbd/api?mode=queue", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != false {
		t.Error("expected auth failure")
	}
}

func TestQueueEmpty(t *testing.T) {
	h, _ := testHandler(t)
	req := httptest.NewRequest("GET", "/sabnzbd/api?mode=queue&apikey=test-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var resp struct {
		Queue struct {
			Slots []interface{} `json:"slots"`
		} `json:"queue"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Queue.Slots) != 0 {
		t.Errorf("expected empty queue, got %d slots", len(resp.Queue.Slots))
	}
}

func TestHistoryWithDownload(t *testing.T) {
	h, st := testHandler(t)

	dl := &store.Download{
		ID:        "iparr_test1",
		Title:     "Test.Show.S01E01",
		Status:    store.StatusCompleted,
		OutputDir: "/downloads/Test.Show.S01E01/",
		Size:      1024000,
	}
	st.PutDownload(dl)
	st.MoveToHistory("iparr_test1")

	req := httptest.NewRequest("GET", "/sabnzbd/api?mode=history&apikey=test-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var resp struct {
		History struct {
			Slots []struct {
				NzoID   string `json:"nzo_id"`
				Name    string `json:"name"`
				Status  string `json:"status"`
				Storage string `json:"storage"`
			} `json:"slots"`
		} `json:"history"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.History.Slots) != 1 {
		t.Fatalf("expected 1 history slot, got %d", len(resp.History.Slots))
	}
	slot := resp.History.Slots[0]
	if slot.NzoID != "iparr_test1" {
		t.Errorf("nzo_id = %q", slot.NzoID)
	}
	if slot.Storage != "/downloads/Test.Show.S01E01/" {
		t.Errorf("storage = %q", slot.Storage)
	}
}

// TestHistoryDownloadTimeGuardsUnsetStartedAt pins the fix for the
// download_time overflow that took the SAB shim "offline" in Sonarr. A
// download that failed or was cancelled before it started has a zero
// StartedAt; CompletedAt.Sub(zeroTime) saturates to math.MaxInt64, so
// int(...Seconds()) is 9223372036 -- larger than the Int32 Sonarr
// deserialises download_time into. A single such slot makes Sonarr reject
// the whole history response. The slot must report 0 for an unset StartedAt.
func TestHistoryDownloadTimeGuardsUnsetStartedAt(t *testing.T) {
	h, st := testHandler(t)

	if err := st.PutHistory(&store.Download{
		ID:          "iparr_nostart",
		Title:       "Failed.Before.Start.S01E01",
		Status:      store.StatusFailed,
		CompletedAt: time.Now(),
		Error:       "no available stream",
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/sabnzbd/api?mode=history&apikey=test-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var resp struct {
		History struct {
			Slots []struct {
				DownloadTime int64 `json:"download_time"`
			} `json:"slots"`
		} `json:"history"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.History.Slots) != 1 {
		t.Fatalf("expected 1 history slot, got %d", len(resp.History.Slots))
	}
	if dt := resp.History.Slots[0].DownloadTime; dt != 0 {
		t.Errorf("download_time = %d, want 0 (unset StartedAt must not overflow Int32)", dt)
	}
}

// TestHistoryDownloadTimeReportsElapsed guards the normal path: when
// StartedAt is set, download_time stays the real elapsed seconds.
func TestHistoryDownloadTimeReportsElapsed(t *testing.T) {
	h, st := testHandler(t)
	now := time.Now()

	if err := st.PutHistory(&store.Download{
		ID:          "iparr_ok",
		Title:       "Good.Show.S01E01",
		Status:      store.StatusCompleted,
		StartedAt:   now.Add(-12 * time.Second),
		CompletedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/sabnzbd/api?mode=history&apikey=test-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var resp struct {
		History struct {
			Slots []struct {
				DownloadTime int64 `json:"download_time"`
			} `json:"slots"`
		} `json:"history"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.History.Slots) != 1 {
		t.Fatalf("expected 1 history slot, got %d", len(resp.History.Slots))
	}
	if dt := resp.History.Slots[0].DownloadTime; dt != 12 {
		t.Errorf("download_time = %d, want 12", dt)
	}
}

func TestGetConfig(t *testing.T) {
	h, _ := testHandler(t)
	// get_config requires apikey from v1.5.6 onwards (audit finding,
	// closing the parallel unauthenticated-info-leak that v1.5.0 fixed
	// on the Newznab side but missed on the SAB shim).
	req := httptest.NewRequest("GET", "/sabnzbd/api?mode=get_config&apikey=test-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	var resp struct {
		Config struct {
			Misc struct {
				CompleteDir string `json:"complete_dir"`
			} `json:"misc"`
			Categories []struct {
				Name string `json:"name"`
			} `json:"categories"`
		} `json:"config"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Config.Misc.CompleteDir == "" {
		t.Error("missing complete_dir")
	}
	if len(resp.Config.Categories) < 1 {
		t.Error("missing categories")
	}
}

// TestAuthRequiredForInfoModes pins the v1.5.6 fix: every mode except
// `version` must require the apikey query param. Prior to v1.5.6,
// `get_cats`, `get_config`, and `fullstatus` returned operator state
// (download directory, category list) over plain HTTP with no auth,
// so any LAN host could enumerate the configured NFS mount path. The
// fix moves the apikey check above the mode switch and allow-lists
// only `version` (which Sonarr probes before attaching the key, same
// as the Newznab `t=caps` probe).
func TestAuthRequiredForInfoModes(t *testing.T) {
	cases := []string{"get_cats", "get_config", "fullstatus"}
	for _, mode := range cases {
		t.Run(mode, func(t *testing.T) {
			h, _ := testHandler(t)
			req := httptest.NewRequest("GET", "/sabnzbd/api?mode="+mode, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d (want 200, SAB protocol returns 200 with status:false on auth fail)", w.Code)
			}
			var resp map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if resp["status"] != false {
				t.Errorf("mode=%s: expected status:false on missing apikey, got body %s", mode, w.Body.String())
			}
			if errStr, _ := resp["error"].(string); !strings.Contains(errStr, "API Key Incorrect") {
				t.Errorf("mode=%s: expected API Key Incorrect error, got %q", mode, errStr)
			}
		})
	}
}

// TestAuthRequiredForInfoModes_WrongKey covers the second half of the
// v1.5.6 fix: a present-but-wrong apikey must also be rejected. The
// pre-fix code returned the response body before ever consulting the
// stored key, so a query like `?mode=get_config&apikey=anything` would
// succeed. The fix gates all non-`version` modes on a strict equality
// check.
func TestAuthRequiredForInfoModes_WrongKey(t *testing.T) {
	h, _ := testHandler(t)
	req := httptest.NewRequest("GET", "/sabnzbd/api?mode=get_config&apikey=wrong", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != false {
		t.Errorf("expected status:false on wrong apikey, got %s", w.Body.String())
	}
}

func TestAddFile(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.SetConfig("api_key", "test-key")
	t.Cleanup(func() { st.Close() })

	ms := &mockStarter{st: st}
	h := NewHandler(st, ms)

	// create a mock NZB file containing a segment with pid:quality
	nzbXML := `<?xml version="1.0" encoding="UTF-8"?>
<nzb>
  <file subject="test">
    <groups><group>iparr.internal</group></groups>
    <segments><segment number="1">b039d07m:720p</segment></segments>
  </file>
</nzb>`

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("name", "test.nzb")
	part.Write([]byte(nzbXML))
	mw.Close()

	req := httptest.NewRequest("POST", "/sabnzbd/api?mode=addfile&apikey=test-key&cat=sonarr", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var resp struct {
		Status bool     `json:"status"`
		NzoIDs []string `json:"nzo_ids"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.Status {
		t.Errorf("expected status true, body: %s", w.Body.String())
	}
	if len(resp.NzoIDs) != 1 || resp.NzoIDs[0] == "iparr_placeholder" {
		t.Errorf("expected real nzo_id, got %v", resp.NzoIDs)
	}

	// verify download was created in store
	dl, _ := st.GetDownload(resp.NzoIDs[0])
	if dl == nil {
		t.Fatal("download not in store")
	}
	if dl.PID != "b039d07m" {
		t.Errorf("pid = %q", dl.PID)
	}
	if dl.Quality != "720p" {
		t.Errorf("quality = %q", dl.Quality)
	}
}

func TestSABnzbdLogSanitisesAPIKey(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "test.db"))
	defer st.Close()
	st.SetConfig("api_key", "secret-key-12345")

	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(os.Stderr)

	h := NewHandler(st, nil)
	req := httptest.NewRequest("GET", "/sabnzbd/api?mode=version&apikey=secret-key-12345", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	logOutput := logBuf.String()
	if strings.Contains(logOutput, "secret-key-12345") {
		t.Errorf("log output contains raw API key:\n%s", logOutput)
	}
	if !strings.Contains(logOutput, "apikey=***") {
		t.Errorf("log output should contain redacted apikey=***:\n%s", logOutput)
	}
}

// TestQueueDelete_PreservesHistory exercises audit item 20. SAB
// delete (mode=queue&name=delete) must leave the cancelled row in
// history so Sonarr can see a terminal status and stop re-discovering
// the same release. Before v1.5.2 the handler called MoveToHistory
// after the manager's CancelDownload had already removed the row from
// the downloads bucket, so MoveToHistory always failed and the
// fallback DeleteDownload erased the entry without trace.
func TestQueueDelete_PreservesHistory(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.SetConfig("api_key", "test-key")
	t.Cleanup(func() { st.Close() })

	// Seed a download row representing an active Sonarr-driven grab.
	const nzoID = "iparr_b001_720p"
	dl := &store.Download{
		ID:       nzoID,
		PID:      "b001",
		Quality:  "720p",
		Title:    "Test.Show.S01E01",
		Category: "sonarr",
		Status:   store.StatusDownloading,
	}
	if err := st.PutDownload(dl); err != nil {
		t.Fatalf("PutDownload: %v", err)
	}

	// Use a starter whose CancelDownload simulates the manager: it
	// deletes the row from the downloads bucket so the test exercises
	// the same ordering as production.
	starter := &deletingStarter{st: st}
	h := NewHandler(st, starter)

	url := fmt.Sprintf("/sabnzbd/api?mode=queue&name=delete&value=%s&apikey=test-key", nzoID)
	req := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}

	// Row must be gone from the active downloads bucket.
	if got, _ := st.GetDownload(nzoID); got != nil {
		t.Errorf("expected download cleared, got %+v", got)
	}

	// Row must now be in history with a failed/cancelled status and a
	// non-empty Error so Sonarr's history scrape can surface it.
	hist, err := st.GetHistory(nzoID)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if hist == nil {
		t.Fatal("expected history entry, got nil")
	}
	if hist.Status != store.StatusFailed {
		t.Errorf("history Status = %q, want %q", hist.Status, store.StatusFailed)
	}
	if !strings.Contains(strings.ToLower(hist.Error), "cancelled") {
		t.Errorf("history Error should mention cancelled, got %q", hist.Error)
	}
}

// deletingStarter simulates Manager.CancelDownload: removes the row
// from the downloads bucket. Used by TestQueueDelete_PreservesHistory
// to exercise the production ordering.
type deletingStarter struct {
	st *store.Store
}

func (d *deletingStarter) StartDownload(pid, quality, title, category string) (string, error) {
	return "", nil
}
func (d *deletingStarter) CancelDownload(nzoID string) error {
	return d.st.DeleteDownload(nzoID)
}
func (d *deletingStarter) IsPaused() bool { return false }

func TestSabnzbdGetConfig_UsesEnvDownloadDir(t *testing.T) {
	h, _ := testHandler(t)
	h.DownloadDir = "/data"

	req := httptest.NewRequest("GET", "/sabnzbd/api?mode=get_config&apikey=test-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "/data") {
		t.Errorf("expected response to contain /data, got: %s", body)
	}
}
