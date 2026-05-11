package download

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// TestEnqueue_PlacesUnderIncomplete exercises issue #29: new downloads
// must write into a `<downloadDir>/incomplete/<safeTitle>` staging dir
// so partial ffmpeg output is never mistaken for a finished file by any
// watch-folder import flow.
func TestEnqueue_PlacesUnderIncomplete(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	downloadDir := filepath.Join(dir, "downloads")
	m := NewManager(st, downloadDir, 1, nil, nil, nil, nil)

	id, err := m.Enqueue("b00inc", "720p", "Test.S01E01.720p", "sonarr")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	dl, err := st.GetDownload(id)
	if err != nil || dl == nil {
		t.Fatalf("GetDownload: dl=%v err=%v", dl, err)
	}

	want := filepath.Join(downloadDir, "incomplete", "Test.S01E01.720p")
	if dl.OutputDir != want {
		t.Fatalf("OutputDir = %q, want %q", dl.OutputDir, want)
	}
}

// TestFinaliseDownload_MovesIncompleteToFinal verifies the post-download
// atomic rename: a populated incomplete/<title>/ dir is moved into
// <downloadDir>/<title>/ and dl.OutputDir / OutputFile reflect the final
// location so the SABnzbd history slot reports the truthful path.
func TestFinaliseDownload_MovesIncompleteToFinal(t *testing.T) {
	dir := t.TempDir()
	downloadDir := filepath.Join(dir, "downloads")
	incompleteDir := filepath.Join(downloadDir, "incomplete", "Test.Show.S01E01")
	finalDir := filepath.Join(downloadDir, "Test.Show.S01E01")

	if err := os.MkdirAll(incompleteDir, 0o755); err != nil {
		t.Fatalf("mkdir incomplete: %v", err)
	}
	incompleteFile := filepath.Join(incompleteDir, "Test.Show.S01E01.mp4")
	if err := os.WriteFile(incompleteFile, []byte("video bytes"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	m := NewManager(st, downloadDir, 1, nil, nil, nil, nil)
	dl := &store.Download{
		ID:         "iparr_final1",
		PID:        "b00fin",
		Title:      "Test.Show.S01E01",
		OutputDir:  incompleteDir,
		OutputFile: incompleteFile,
	}

	if err := m.finaliseDownload(dl); err != nil {
		t.Fatalf("finaliseDownload: %v", err)
	}

	finalFile := filepath.Join(finalDir, "Test.Show.S01E01.mp4")
	if _, err := os.Stat(finalFile); err != nil {
		t.Fatalf("expected final file at %s: %v", finalFile, err)
	}
	if _, err := os.Stat(incompleteFile); !os.IsNotExist(err) {
		t.Fatalf("incomplete file still present: stat err=%v", err)
	}
	if dl.OutputDir != finalDir {
		t.Fatalf("dl.OutputDir = %q, want %q", dl.OutputDir, finalDir)
	}
	if dl.OutputFile != finalFile {
		t.Fatalf("dl.OutputFile = %q, want %q", dl.OutputFile, finalFile)
	}
}

// TestListDirectory_SkipsIncomplete is wired in api/directory_test.go;
// this placeholder documents the cross-package dependency for issue #29.
