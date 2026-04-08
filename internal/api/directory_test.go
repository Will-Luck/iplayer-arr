package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleListDirectory_UsesEnvDownloadDir(t *testing.T) {
	h, _ := testAPI(t)

	// handleListDirectory only lists subdirectories containing files, so create
	// a marker subdirectory inside the temp download dir with a known file.
	tmpDir := t.TempDir()
	markerDir := filepath.Join(tmpDir, "test-marker-show")
	if err := os.Mkdir(markerDir, 0o755); err != nil {
		t.Fatalf("create marker dir: %v", err)
	}
	markerFile := filepath.Join(markerDir, "episode.mp4")
	if err := os.WriteFile(markerFile, []byte("test"), 0o644); err != nil {
		t.Fatalf("create marker file: %v", err)
	}

	// Set DownloadDir to the temp dir; this should win over the store
	h.DownloadDir = tmpDir

	req := httptest.NewRequest("GET", "/api/downloads/directory", nil)
	req.Header.Set("X-Api-Key", "test-api-key")
	w := httptest.NewRecorder()
	h.handleListDirectory(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "test-marker-show") {
		t.Errorf("expected listing to contain test-marker-show, got: %s", body)
	}
	if !strings.Contains(body, "episode.mp4") {
		t.Errorf("expected listing to contain episode.mp4, got: %s", body)
	}
}
