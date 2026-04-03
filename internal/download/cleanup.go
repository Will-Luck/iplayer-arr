package download

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (m *Manager) RunCleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.runCleanup()
		}
	}
}

func (m *Manager) runCleanup() {
	enabled, _ := m.store.GetConfig("auto_cleanup")
	if enabled != "true" {
		return
	}

	entries, err := os.ReadDir(m.downloadDir)
	if err != nil {
		log.Printf("cleanup: read dir: %v", err)
		return
	}

	ownedDirs, err := m.store.ListHistoryOutputDirs()
	if err != nil {
		log.Printf("cleanup: list history dirs: %v", err)
		return
	}

	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		fullPath := filepath.Join(m.downloadDir, entry.Name())
		if !ownedDirs[fullPath] {
			continue
		}

		hasMp4 := false
		files, err := os.ReadDir(fullPath)
		if err != nil {
			continue
		}
		for _, f := range files {
			if !f.IsDir() && strings.HasSuffix(strings.ToLower(f.Name()), ".mp4") {
				hasMp4 = true
				break
			}
		}

		if !hasMp4 {
			if err := os.RemoveAll(fullPath); err != nil {
				log.Printf("cleanup: remove %s: %v", entry.Name(), err)
				continue
			}
			if len(files) == 0 {
				log.Printf("cleanup: removed empty folder %s", entry.Name())
			} else {
				log.Printf("cleanup: removed stale folder %s", entry.Name())
			}
			removed++
		}
	}

	if removed > 0 {
		m.hub.Broadcast("cleanup:completed", map[string]int{"removed": removed})
	}
}
