package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleGetConfig_DownloadDirFromEnv(t *testing.T) {
	h, _ := testAPI(t)
	h.DownloadDir = "/data"

	req := httptest.NewRequest("GET", "/api/config", nil)
	req.Header.Set("X-Api-Key", "test-api-key")
	w := httptest.NewRecorder()
	h.handleGetConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := resp["download_dir"]; got != "/data" {
		t.Errorf("download_dir = %q, want /data", got)
	}
}

func TestHandleGetConfig_DownloadDirFallbackToDefault(t *testing.T) {
	h, _ := testAPI(t)
	h.DownloadDir = ""

	req := httptest.NewRequest("GET", "/api/config", nil)
	req.Header.Set("X-Api-Key", "test-api-key")
	w := httptest.NewRecorder()
	h.handleGetConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := resp["download_dir"]; got != "/downloads" {
		t.Errorf("download_dir = %q, want /downloads (default)", got)
	}
}
