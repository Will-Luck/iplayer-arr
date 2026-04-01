package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GiteaLN/iplayer-arr/internal/bbc"
	"github.com/GiteaLN/iplayer-arr/internal/store"
)

// testAPI creates a temporary store with an API key set and returns a Handler wired up for testing.
func testAPI(t *testing.T) (*Handler, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.SetConfig("api_key", "test-api-key")
	t.Cleanup(func() { st.Close() })

	hub := NewHub()
	ibl := bbc.NewIBL(bbc.NewClient())
	status := &RuntimeStatus{FFmpegVersion: "ffmpeg version 6.0"}

	h := NewHandler(st, hub, nil, ibl, status)
	return h, st
}

func TestStatusNoAuth(t *testing.T) {
	h, _ := testAPI(t)
	req := httptest.NewRequest("GET", "/api/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status code = %d", w.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["ffmpeg"] != "ffmpeg version 6.0" {
		t.Errorf("ffmpeg = %v", resp["ffmpeg"])
	}
}

func TestDownloadsNoAuth(t *testing.T) {
	h, _ := testAPI(t)
	req := httptest.NewRequest("GET", "/api/downloads", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestDownloadsWithQueryAuth(t *testing.T) {
	h, _ := testAPI(t)
	req := httptest.NewRequest("GET", "/api/downloads?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status code = %d, body: %s", w.Code, w.Body.String())
	}
	var resp []interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp) != 0 {
		t.Errorf("expected empty array, got %d items", len(resp))
	}
}

func TestDownloadsWithBearerAuth(t *testing.T) {
	h, _ := testAPI(t)
	req := httptest.NewRequest("GET", "/api/downloads", nil)
	req.Header.Set("Authorization", "Bearer test-api-key")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status code = %d", w.Code)
	}
}

func TestConfigGet(t *testing.T) {
	h, st := testAPI(t)
	st.SetConfig("quality", "1080p")

	req := httptest.NewRequest("GET", "/api/config?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status code = %d", w.Code)
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["quality"] != "1080p" {
		t.Errorf("quality = %q", resp["quality"])
	}
	// api_key should be present in config response
	if resp["api_key"] != "test-api-key" {
		t.Errorf("api_key = %q", resp["api_key"])
	}
}

func TestConfigPut(t *testing.T) {
	h, st := testAPI(t)

	body := `{"key":"quality","value":"480p"}`
	req := httptest.NewRequest("PUT", "/api/config?apikey=test-api-key", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status code = %d, body: %s", w.Code, w.Body.String())
	}

	val, _ := st.GetConfig("quality")
	if val != "480p" {
		t.Errorf("stored quality = %q", val)
	}
}

func TestConfigPutBlocksAPIKey(t *testing.T) {
	h, _ := testAPI(t)

	body := `{"key":"api_key","value":"hacked"}`
	req := httptest.NewRequest("PUT", "/api/config?apikey=test-api-key", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestOverridesList(t *testing.T) {
	h, _ := testAPI(t)
	req := httptest.NewRequest("GET", "/api/overrides?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status code = %d", w.Code)
	}

	// Must be [] not null
	if w.Body.String() != "[]\n" {
		t.Errorf("expected empty array, got %q", w.Body.String())
	}
}

func TestOverridesPutAndList(t *testing.T) {
	h, _ := testAPI(t)

	body := `{"show_name":"Doctor Who","force_date_based":true}`
	req := httptest.NewRequest("PUT", "/api/overrides/Doctor+Who?apikey=test-api-key", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("PUT status = %d, body: %s", w.Code, w.Body.String())
	}

	// Now list
	req = httptest.NewRequest("GET", "/api/overrides?apikey=test-api-key", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var overrides []store.ShowOverride
	if err := json.Unmarshal(w.Body.Bytes(), &overrides); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(overrides) != 1 {
		t.Fatalf("expected 1 override, got %d", len(overrides))
	}
	if !overrides[0].ForceDateBased {
		t.Error("expected force_date_based=true")
	}
}

func TestOverridesDelete(t *testing.T) {
	h, st := testAPI(t)
	st.PutOverride(&store.ShowOverride{ShowName: "Test Show"})

	req := httptest.NewRequest("DELETE", "/api/overrides/Test+Show?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("DELETE status = %d", w.Code)
	}

	overrides, _ := st.ListOverrides()
	if len(overrides) != 0 {
		t.Errorf("expected 0 overrides after delete, got %d", len(overrides))
	}
}

func TestHistoryDelete(t *testing.T) {
	h, st := testAPI(t)

	dl := &store.Download{ID: "hist_1", PID: "p1", Title: "Test", Status: store.StatusCompleted}
	st.PutDownload(dl)
	st.MoveToHistory("hist_1")

	req := httptest.NewRequest("DELETE", "/api/history/hist_1?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("DELETE status = %d", w.Code)
	}

	entry, _ := st.GetHistory("hist_1")
	if entry != nil {
		t.Error("history entry should be deleted")
	}
}

func TestManualDownloadNoStarter(t *testing.T) {
	h, _ := testAPI(t)

	body := `{"pid":"b039d07m","quality":"720p"}`
	req := httptest.NewRequest("POST", "/api/download?apikey=test-api-key", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// With nil manager, should return error
	if w.Code != 500 {
		t.Fatalf("expected 500, got %d, body: %s", w.Code, w.Body.String())
	}
}

func TestEventsEndpointNoAuth(t *testing.T) {
	h, _ := testAPI(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("GET", "/api/events", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestUnknownRoute(t *testing.T) {
	h, _ := testAPI(t)
	req := httptest.NewRequest("GET", "/api/nonexistent?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
