package download

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/GiteaLN/iplayer-arr/internal/bbc"
	"github.com/GiteaLN/iplayer-arr/internal/store"
)

// EventBroadcaster sends real-time events to connected clients (e.g. SSE hub).
type EventBroadcaster interface {
	Broadcast(eventType string, data interface{})
}

type Manager struct {
	store       *store.Store
	downloadDir string
	maxWorkers  int

	client   *bbc.Client
	playlist *bbc.PlaylistResolver
	ms       *bbc.MediaSelector
	hub      EventBroadcaster

	cancel  context.CancelFunc
	wg      sync.WaitGroup
	claimed map[string]struct{}
	claimMu sync.Mutex
}

func NewManager(st *store.Store, downloadDir string, maxWorkers int,
	client *bbc.Client, playlist *bbc.PlaylistResolver, ms *bbc.MediaSelector,
	hub EventBroadcaster) *Manager {
	return &Manager{
		store:       st,
		downloadDir: downloadDir,
		maxWorkers:  maxWorkers,
		client:      client,
		playlist:    playlist,
		ms:          ms,
		hub:         hub,
		claimed:     make(map[string]struct{}),
	}
}

// Start launches the worker goroutines that poll for pending downloads.
func (m *Manager) Start(ctx context.Context) {
	ctx, m.cancel = context.WithCancel(ctx)
	for i := 0; i < m.maxWorkers; i++ {
		m.wg.Add(1)
		id := i
		go func() {
			defer m.wg.Done()
			m.worker(ctx, id)
		}()
	}
	log.Printf("download manager started with %d workers", m.maxWorkers)
}

// Stop cancels the worker context and waits for all workers to finish.
func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
	log.Println("download manager stopped")
}

func (m *Manager) Enqueue(pid, quality, title, category string) (string, error) {
	existing, _ := m.store.FindDownloadByPIDQuality(pid, quality)
	if existing != nil {
		return existing.ID, nil
	}

	hist, _ := m.store.FindHistoryByPIDQuality(pid, quality)
	if hist != nil {
		return hist.ID, nil
	}

	id := generateNzoID()
	outputDir := filepath.Join(m.downloadDir, title)

	dl := &store.Download{
		ID:        id,
		PID:       pid,
		Quality:   quality,
		Title:     title,
		Category:  category,
		Status:    store.StatusPending,
		OutputDir: outputDir,
		CreatedAt: time.Now(),
	}

	if err := m.store.PutDownload(dl); err != nil {
		return "", fmt.Errorf("store download: %w", err)
	}

	return id, nil
}

func (m *Manager) CancelDownload(nzoID string) error {
	m.store.DeleteDownload(nzoID)
	return nil
}

func (m *Manager) StartDownload(pid, quality, title, category string) (string, error) {
	return m.Enqueue(pid, quality, title, category)
}

func generateNzoID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "iparr_" + hex.EncodeToString(b)
}
