package download

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/store"
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

	paused  atomic.Bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	claimed map[string]context.CancelFunc
	claimMu sync.Mutex

	cancelled   map[string]struct{}
	cancelledMu sync.Mutex
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
		claimed:     make(map[string]context.CancelFunc),
		cancelled:   make(map[string]struct{}),
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

func (m *Manager) Pause() {
	m.paused.Store(true)
	m.hub.Broadcast("pause:changed", map[string]bool{"paused": true})
}

func (m *Manager) Resume() {
	m.paused.Store(false)
	m.hub.Broadcast("pause:changed", map[string]bool{"paused": false})
}

func (m *Manager) IsPaused() bool { return m.paused.Load() }

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
	safeTitle := sanitiseFilename(filepath.Base(title))
	if safeTitle == "" || safeTitle == "." || safeTitle == ".." {
		safeTitle = pid
	}
	outputDir := filepath.Join(m.downloadDir, safeTitle)

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
	m.MarkCancelled(nzoID)
	// If a worker is processing this download, cancel its context to kill ffmpeg
	m.claimMu.Lock()
	if cancel, ok := m.claimed[nzoID]; ok {
		cancel()
	}
	m.claimMu.Unlock()
	m.store.DeleteDownload(nzoID)
	return nil
}

func (m *Manager) MarkCancelled(id string) {
	m.cancelledMu.Lock()
	m.cancelled[id] = struct{}{}
	m.cancelledMu.Unlock()
}

func (m *Manager) IsCancelled(id string) bool {
	m.cancelledMu.Lock()
	defer m.cancelledMu.Unlock()
	_, ok := m.cancelled[id]
	return ok
}

func (m *Manager) clearCancelled(id string) {
	m.cancelledMu.Lock()
	delete(m.cancelled, id)
	m.cancelledMu.Unlock()
}

func (m *Manager) StartDownload(pid, quality, title, category string) (string, error) {
	return m.Enqueue(pid, quality, title, category)
}

func generateNzoID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "iparr_" + hex.EncodeToString(b)
}
