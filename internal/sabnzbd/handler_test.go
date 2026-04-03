package sabnzbd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/GiteaLN/iplayer-arr/internal/store"
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

func TestGetConfig(t *testing.T) {
	h, _ := testHandler(t)
	req := httptest.NewRequest("GET", "/sabnzbd/api?mode=get_config", nil)
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
