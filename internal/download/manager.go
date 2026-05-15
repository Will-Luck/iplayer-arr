package download

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// cancelWaitTimeout is the maximum time CancelDownload waits for an active
// worker to release its claim after the context is cancelled. Reached only
// if ffmpeg ignores SIGKILL or the worker hangs in an unrelated step; in
// that case CancelDownload proceeds with cleanup anyway so the user isn't
// blocked waiting on a stuck worker.
const cancelWaitTimeout = 15 * time.Second

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
	// Stage downloads in <downloadDir>/incomplete/<safeTitle>/ so partial
	// ffmpeg output is never visible to watch-folder import flows. The
	// final atomic rename to <downloadDir>/<safeTitle>/ runs in
	// finaliseDownload after the file has been probed and reconciled.
	// Issue #29.
	outputDir := filepath.Join(m.downloadDir, "incomplete", safeTitle)

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

	// Snapshot OutputDir before signalling cancel so the worker can't
	// rewrite it (finaliseDownload mutates the field) in between.
	var outputDir string
	if dl, _ := m.store.GetDownload(nzoID); dl != nil {
		outputDir = dl.OutputDir
	}

	// If a worker is processing this download, cancel its context to kill
	// ffmpeg, then wait for the worker to release the claim. Polling on
	// the claimed map avoids restructuring the claim/release contract; the
	// worker calls m.release after processDownload returns.
	m.claimMu.Lock()
	cancel, active := m.claimed[nzoID]
	m.claimMu.Unlock()
	if active {
		cancel()
		deadline := time.Now().Add(cancelWaitTimeout)
		for time.Now().Before(deadline) {
			m.claimMu.Lock()
			_, stillActive := m.claimed[nzoID]
			m.claimMu.Unlock()
			if !stillActive {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	// Remove the orphaned partial mp4 + parent dir so a cancel doesn't
	// leak disk on the NFS mount. Refuses to clean anything outside
	// <downloadDir>/incomplete/. A completed download whose OutputDir
	// has already been moved out of incomplete/ is preserved.
	if err := m.cleanupIncompleteDir(outputDir); err != nil {
		log.Printf("CancelDownload %s: cleanup %s: %v", nzoID, outputDir, err)
	}

	// Clear the cancelled-set entry. Without this the map grows by one
	// per cancel for the lifetime of the process; processDownload
	// only clears it when the worker observes the cancel mid-flight,
	// which doesn't fire on pending or already-released cancels.
	// Audit finding item 10.
	m.clearCancelled(nzoID)

	return m.store.DeleteDownload(nzoID)
}

// cleanupIncompleteDir removes outputDir on disk only when it sits under
// <downloadDir>/incomplete/. Returns nil for empty input or when the
// path has already been finalised (outside incomplete/). Refuses any
// path that would escape the incomplete/ root via "..".
func (m *Manager) cleanupIncompleteDir(outputDir string) error {
	if outputDir == "" {
		return nil
	}
	incompleteRoot := filepath.Join(m.downloadDir, "incomplete")
	rel, err := filepath.Rel(incompleteRoot, outputDir)
	if err != nil {
		return fmt.Errorf("rel: %w", err)
	}
	// rel == "." would mean outputDir IS the incomplete root; refuse.
	if rel == "." || rel == "" {
		return fmt.Errorf("refusing to clean incomplete root itself: %s", outputDir)
	}
	// rel starts with ".." (or contains a ".." segment) means the path
	// escapes the incomplete/ root. Completed downloads whose OutputDir
	// has been finalised to <downloadDir>/<title>/ also produce a "../"
	// rel — that's the correct refusal path.
	if strings.HasPrefix(rel, "..") || strings.Contains(rel, string(filepath.Separator)+"..") {
		return nil // not an error: post-finalise downloads land here
	}
	return os.RemoveAll(outputDir)
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

// finaliseDownload performs the post-completion atomic rename: the
// download's working directory under `<downloadDir>/incomplete/` is
// moved up to `<downloadDir>/<safeTitle>` so the file is only visible
// in its final location once ffmpeg, reconcile and subtitles are all
// done. dl.OutputDir and dl.OutputFile are updated to the new paths so
// the SABnzbd history slot reports a truthful storage path. Issue #29.
//
// Idempotent: if dl.OutputDir is already at its final location (e.g. a
// pre-issue-29 row reprocessed) this is a no-op. If the target already
// exists, returns an error and leaves the incomplete dir in place so
// the caller can decide how to recover.
func (m *Manager) finaliseDownload(dl *store.Download) error {
	base := filepath.Base(dl.OutputDir)
	finalDir := filepath.Join(m.downloadDir, base)
	if finalDir == dl.OutputDir {
		return nil
	}

	if err := os.MkdirAll(m.downloadDir, 0o755); err != nil {
		return fmt.Errorf("ensure download dir: %w", err)
	}

	if _, err := os.Stat(finalDir); err == nil {
		return fmt.Errorf("finalise: target %s already exists", finalDir)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat target: %w", err)
	}

	if err := os.Rename(dl.OutputDir, finalDir); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", dl.OutputDir, finalDir, err)
	}

	parent := filepath.Dir(dl.OutputDir) // the now-empty incomplete/ dir
	_ = os.Remove(parent)                // best-effort, only succeeds if empty

	if dl.OutputFile != "" {
		dl.OutputFile = filepath.Join(finalDir, filepath.Base(dl.OutputFile))
	}
	dl.OutputDir = finalDir
	return nil
}
