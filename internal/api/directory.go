package api

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type directoryFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type directoryEntry struct {
	Name      string          `json:"name"`
	Path      string          `json:"path"`
	Files     []directoryFile `json:"files"`
	TotalSize int64           `json:"total_size"`
	Owned     bool            `json:"owned"`
}

func (h *Handler) handleListDirectory(w http.ResponseWriter, r *http.Request) {
	downloadDir := h.ResolveDownloadDir()

	entries, err := os.ReadDir(downloadDir)
	if err != nil {
		writeJSON(w, http.StatusOK, []directoryEntry{})
		return
	}

	ownedDirs, err := h.store.ListHistoryOutputDirs()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	var result []directoryEntry
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		fullPath := filepath.Join(downloadDir, entry.Name())

		files, err := os.ReadDir(fullPath)
		if err != nil {
			continue
		}

		var dirFiles []directoryFile
		var totalSize int64
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			dirFiles = append(dirFiles, directoryFile{
				Name: f.Name(),
				Size: info.Size(),
			})
			totalSize += info.Size()
		}

		result = append(result, directoryEntry{
			Name:      entry.Name(),
			Path:      fullPath,
			Files:     dirFiles,
			TotalSize: totalSize,
			Owned:     ownedDirs[fullPath],
		})
	}

	if result == nil {
		result = []directoryEntry{}
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleDeleteDirectory(w http.ResponseWriter, r *http.Request) {
	folder := strings.TrimPrefix(r.URL.Path, "/api/downloads/directory/")
	folder = filepath.Clean(folder)
	if folder == "" || folder == "." || folder == ".." || strings.ContainsAny(folder, "/\\") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid folder name"})
		return
	}

	downloadDir := h.ResolveDownloadDir()

	fullPath := filepath.Join(downloadDir, folder)
	if !strings.HasPrefix(fullPath, downloadDir+string(os.PathSeparator)) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid folder name"})
		return
	}

	ownedDirs, err := h.store.ListHistoryOutputDirs()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !ownedDirs[fullPath] {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "folder not owned by iplayer-arr"})
		return
	}

	if err := os.RemoveAll(fullPath); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	log.Printf("directory: deleted folder %s", folder)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": folder})
}
