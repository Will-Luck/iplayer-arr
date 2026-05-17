package download

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// cancelWaitTimeout is the maximum time CancelDownload waits for an active
// worker to release its claim after the context is cancelled. Reached only
// if ffmpeg ignores SIGKILL or the worker hangs in an unrelated step; in
// that case CancelDownload proceeds with cleanup anyway so the user isn't
// blocked waiting on a stuck worker.
const cancelWaitTimeout = 15 * time.Second

// IncompleteDirName is the on-disk staging subdirectory under the
// download root that holds partially-fetched files. The producer side
// (Enqueue and cleanupIncompleteDir) and the consumer side (the
// directory listing API that hides this folder from the UI) both
// reference this constant so the two sides cannot silently drift.
// Audit item 39.
const IncompleteDirName = "incomplete"

// EventBroadcaster sends real-time events to connected clients (e.g. SSE hub).
type EventBroadcaster interface {
	Broadcast(eventType string, data interface{})
}

type Manager struct {
	store       *store.Store
	downloadDir string
	// maxWorkers is read once during Start() and is effectively
	// immutable for the lifetime of the manager. Resizing the worker
	// pool at runtime is out of scope for v1.5.x; restart the process
	// to apply a new max_workers value. Audit item 25.
	maxWorkers int

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

	// enqueueMu serialises the FindDownload + FindHistory + PutDownload
	// sequence so two concurrent Enqueue callers for the same
	// (pid, quality) can never both insert. Without this, Sonarr's RSS
	// sync + an interactive search firing the same release in <1ms can
	// produce duplicate downloads pointing at the same incomplete/
	// directory. Audit item 21.
	enqueueMu sync.Mutex
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
	// Hold the enqueue lock across the lookup+insert window. The store
	// itself uses Bolt's single-writer txn for the actual PutDownload,
	// but the Find* calls run in read-only txns; two concurrent
	// Enqueue callers can both observe "no existing row" and then both
	// race to insert. The lock is uncontended in the steady state and
	// only matters under search-storm conditions. Audit item 21.
	m.enqueueMu.Lock()
	defer m.enqueueMu.Unlock()

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
	outputDir := filepath.Join(m.downloadDir, IncompleteDirName, safeTitle)

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
	incompleteRoot := filepath.Join(m.downloadDir, IncompleteDirName)
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

	// v1.5.6 finalise rename: use the relocateNoReplace helper from
	// worker.go so we get a kernel-atomic move with RENAME_NOREPLACE
	// (closes the Stat+Rename TOCTOU that pre-v1.5.6 could lose under
	// two finalises racing on the same safeTitle). The helper falls
	// back to Stat+Rename on filesystems that don't support
	// RENAME_NOREPLACE, so behaviour on exotic mounts is unchanged.
	err := relocateNoReplace(dl.OutputDir, finalDir)
	switch {
	case err == nil:
		// fast path; nothing else to do.
	case errors.Is(err, unix.EEXIST):
		return fmt.Errorf("finalise: target %s already exists", finalDir)
	case isCrossDeviceLink(err):
		// EXDEV ("invalid cross-device link") fires when incomplete/
		// and the downloadDir are on different filesystems — this
		// happens when incomplete/ is a tmpfs-backed staging area,
		// an NFS sub-mount with a separate export, or a bind-mount
		// pointed at a different volume. Fall back to copy + remove.
		// Audit item 9.
		if copyErr := copyDir(dl.OutputDir, finalDir); copyErr != nil {
			// v1.5.6: cleanup BOTH ends on copy failure. Pre-v1.5.6
			// only removed `finalDir` (the partial destination) and
			// left `dl.OutputDir` (the source) in place, so a retry
			// that re-ran ffmpeg into the same incomplete dir could
			// then succeed at the move and silently ship a truncated
			// or mixed-state artefact. Force-clearing both ends
			// makes a retry start clean.
			_ = os.RemoveAll(finalDir)
			_ = os.RemoveAll(dl.OutputDir)
			return fmt.Errorf("rename EXDEV fallback: %w", copyErr)
		}
		if rmErr := os.RemoveAll(dl.OutputDir); rmErr != nil {
			log.Printf("EXDEV fallback: copy succeeded but cleanup of %s failed: %v", dl.OutputDir, rmErr)
		}
	default:
		return fmt.Errorf("rename %s -> %s: %w", dl.OutputDir, finalDir, err)
	}

	parent := filepath.Dir(dl.OutputDir) // the now-empty incomplete/ dir
	if err := os.Remove(parent); err != nil && !os.IsNotExist(err) && !isNotEmptyError(err) {
		// non-empty parent is expected (other in-flight downloads);
		// any other error is worth a warn so a stale .DS_Store /
		// lockfile doesn't silently rot the incomplete/ root.
		log.Printf("finalise: cleanup parent %s: %v", parent, err)
	}

	if dl.OutputFile != "" {
		dl.OutputFile = filepath.Join(finalDir, filepath.Base(dl.OutputFile))
	}
	dl.OutputDir = finalDir
	return nil
}

// isCrossDeviceLink returns true when err wraps a syscall.EXDEV
// (invalid cross-device link), which os.Rename raises when source
// and destination live on different filesystems. Lets the caller
// fall back to a copy + remove for the EXDEV case while still
// surfacing other errors.
func isCrossDeviceLink(err error) bool {
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return errors.Is(linkErr.Err, syscall.EXDEV)
	}
	return errors.Is(err, syscall.EXDEV)
}

// isNotEmptyError returns true when err is the standard "directory
// not empty" message for the platform. Used by the post-rename
// parent cleanup to distinguish "expected, other downloads in the
// same incomplete/ root" from real errors worth logging.
func isNotEmptyError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, syscall.ENOTEMPTY) ||
		strings.Contains(err.Error(), "not empty") ||
		strings.Contains(err.Error(), "directory not empty")
}

// copyDir recursively copies a directory tree, preserving file
// modes. Used as the EXDEV fallback when os.Rename refuses a
// cross-filesystem move. Not safe against concurrent writers — the
// caller must have stopped writing to src before invoking.
func copyDir(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("copyDir: %s is not a directory", src)
	}
	if err := os.MkdirAll(dst, srcInfo.Mode().Perm()); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(srcPath, dstPath); err != nil {
			return err
		}
	}
	return nil
}

// copyFile copies one file, preserving mode.
func copyFile(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, srcInfo.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
