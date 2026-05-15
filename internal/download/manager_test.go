package download

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/store"
)

func TestManagerEnqueueAndList(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "test.db"))
	defer st.Close()

	m := NewManager(st, filepath.Join(dir, "downloads"), 2, nil, nil, nil, nil)

	id, err := m.Enqueue("b039d07m", "720p", "Test.S01E01.720p", "sonarr")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if id == "" {
		t.Fatal("empty id")
	}

	dl, _ := st.GetDownload(id)
	if dl == nil {
		t.Fatal("download not found in store")
	}
	if dl.Status != store.StatusPending {
		t.Errorf("status = %q, want pending", dl.Status)
	}
	if dl.Category != "sonarr" {
		t.Errorf("category = %q", dl.Category)
	}
}

func TestManagerDeduplicate(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "test.db"))
	defer st.Close()

	m := NewManager(st, filepath.Join(dir, "downloads"), 2, nil, nil, nil, nil)

	id1, _ := m.Enqueue("b039d07m", "720p", "Test.S01E01.720p", "sonarr")
	id2, _ := m.Enqueue("b039d07m", "720p", "Test.S01E01.720p", "sonarr")

	if id1 != id2 {
		t.Errorf("duplicate enqueue should return same ID: %q != %q", id1, id2)
	}
}

func TestPickStream(t *testing.T) {
	streams := []bbc.VideoStream{
		{Height: 1080, Bitrate: 5000, URL: "http://1080"},
		{Height: 720, Bitrate: 2500, URL: "http://720"},
		{Height: 480, Bitrate: 1200, URL: "http://480"},
	}

	tests := []struct {
		quality    string
		wantHeight int
	}{
		{"1080p", 1080},
		{"720p", 720},
		{"480p", 480},
		{"360p", 480},   // closest to 360 is 480
		{"1440p", 1080}, // closest to 1440 is 1080
	}

	for _, tt := range tests {
		got := pickStream(streams, tt.quality)
		if got.Height != tt.wantHeight {
			t.Errorf("pickStream(%q) height = %d, want %d", tt.quality, got.Height, tt.wantHeight)
		}
	}
}

func TestQualityToHeight(t *testing.T) {
	tests := []struct {
		q    string
		want int
	}{
		{"720p", 720},
		{"1080p", 1080},
		{"480p", 480},
		{"1080P", 1080},
		{"invalid", 720},
		{"", 720},
	}

	for _, tt := range tests {
		got := qualityToHeight(tt.q)
		if got != tt.want {
			t.Errorf("qualityToHeight(%q) = %d, want %d", tt.q, got, tt.want)
		}
	}
}

func TestHeightToQualityTag(t *testing.T) {
	tests := []struct {
		h    int
		want string
	}{
		{0, ""},
		{395, ""},
		{396, "396p"},
		{480, "396p"}, // codebase taxonomy has no literal 480p tier; height 480 falls in the >=396 bucket
		{539, "396p"},
		{540, "540p"},
		{720, "720p"},
		{1080, "1080p"},
		{2160, "2160p"},
		{9999, "2160p"},
	}

	for _, tt := range tests {
		got := heightToQualityTag(tt.h)
		if got != tt.want {
			t.Errorf("heightToQualityTag(%d) = %q, want %q", tt.h, got, tt.want)
		}
	}
}

func TestEstimateSize(t *testing.T) {
	// 60 minutes at 720p: ~2.5Mbps * 3600s / 8 = ~1.125 GB
	size := estimateSize(3600, "720p")
	if size < 1_000_000_000 || size > 1_200_000_000 {
		t.Errorf("estimateSize(3600, 720p) = %d, expected ~1.125GB", size)
	}
}

func TestFailDownloadRetryability(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "test.db"))
	defer st.Close()

	m := NewManager(st, filepath.Join(dir, "downloads"), 1, nil, nil, nil, nil)

	// GeoBlocked is not retryable
	dl := &store.Download{ID: "test1", PID: "p1", Status: store.StatusPending}
	st.PutDownload(dl)
	m.failDownload(dl, store.FailCodeGeoBlocked, fmt.Errorf("geo"))
	if dl.Retryable {
		t.Error("geo-blocked should not be retryable")
	}

	// Expired is not retryable
	dl2 := &store.Download{ID: "test2", PID: "p2", Status: store.StatusPending}
	st.PutDownload(dl2)
	m.failDownload(dl2, store.FailCodeExpired, fmt.Errorf("expired"))
	if dl2.Retryable {
		t.Error("expired should not be retryable")
	}

	// FFmpeg error is retryable on first attempt
	dl3 := &store.Download{ID: "test3", PID: "p3", Status: store.StatusPending}
	st.PutDownload(dl3)
	m.failDownload(dl3, store.FailCodeFFmpeg, fmt.Errorf("ffmpeg died"))
	if !dl3.Retryable {
		t.Error("ffmpeg error on first attempt should be retryable")
	}
	if dl3.RetryCount != 1 {
		t.Errorf("retry count = %d, want 1", dl3.RetryCount)
	}

	// Truncated is retryable (often caused by CDN rate-limiting, not
	// permanently missing content)
	dl4 := &store.Download{ID: "test4", PID: "p4", Status: store.StatusPending}
	st.PutDownload(dl4)
	m.failDownload(dl4, store.FailCodeTruncated, fmt.Errorf("truncated"))
	if !dl4.Retryable {
		t.Error("truncated on first attempt should be retryable")
	}
	if dl4.RetryAfter.IsZero() {
		t.Error("retryable failure should have a non-zero RetryAfter")
	}
}

func TestSanitiseFilename(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"Normal Title", "Normal Title"},
		{"Title: With Colon", "Title - With Colon"},
		{"Title/With/Slashes", "Title-With-Slashes"},
		{"Bad<>Chars|Here", "BadCharsHere"},
	}
	for _, tt := range tests {
		got := sanitiseFilename(tt.in)
		if got != tt.want {
			t.Errorf("sanitiseFilename(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestWorkerLifecycle verifies the worker loop picks up a pending download
// and progresses it past the pending state. This test uses mock HTTP servers
// for the BBC playlist and media selector endpoints.
func TestWorkerLifecycle(t *testing.T) {
	if _, err := CheckFFmpeg(); err != nil {
		t.Skip("ffmpeg not available, skipping worker lifecycle test")
	}

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	// Mock playlist endpoint
	playlistServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"defaultAvailableVersion": map[string]interface{}{
				"smpConfig": map[string]interface{}{
					"title":   "Test Programme",
					"summary": "A test programme",
					"items": []map[string]interface{}{
						{"kind": "programme", "duration": 1800, "vpid": "p_test_vpid"},
					},
				},
			},
			"allAvailableVersions": []interface{}{},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer playlistServer.Close()

	// Mock media selector endpoint - return a dummy stream
	// The worker will attempt ffmpeg on this URL which will fail,
	// but we can verify the download progressed past pending.
	mediaSelectorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<?xml version="1.0"?>
<mediaSelection>
  <media kind="video" type="video/mp4" encoding="h264" bitrate="2500" width="1280" height="720">
    <connection supplier="akamai" transferFormat="hls" protocol="https" href="https://invalid.example.com/stream.m3u8"/>
  </media>
</mediaSelection>`)
	}))
	defer mediaSelectorServer.Close()

	// Create BBC clients pointing at our mock servers
	bbcClient := bbc.NewClient()
	playlist := bbc.NewPlaylistResolver(bbcClient)
	playlist.BaseURL = playlistServer.URL

	ms := bbc.NewMediaSelector(bbcClient)
	ms.BaseURL = mediaSelectorServer.URL

	m := NewManager(st, filepath.Join(dir, "downloads"), 1, bbcClient, playlist, ms, nil)

	// Enqueue a download
	id, err := m.Enqueue("b099test", "720p", "Test.S01E01.720p", "sonarr")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Start manager and let worker process
	ctx, cancel := context.WithCancel(context.Background())
	m.Start(ctx)

	// Wait for the download to progress past pending (up to 5 seconds)
	deadline := time.Now().Add(5 * time.Second)
	var dl *store.Download
	for time.Now().Before(deadline) {
		dl, _ = st.GetDownload(id)
		if dl != nil && dl.Status != store.StatusPending {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	cancel()
	m.Stop()

	if dl == nil {
		t.Fatal("download not found in store after worker run")
	}

	// Verify VPID was set from playlist resolve
	if dl.VPID != "p_test_vpid" {
		t.Errorf("VPID = %q, want %q", dl.VPID, "p_test_vpid")
	}

	// Verify the download progressed past pending
	if dl.Status == store.StatusPending {
		t.Error("download should have progressed past pending status")
	}

	// The download will most likely fail at ffmpeg (invalid stream URL),
	// but it should have gone through resolving and downloading stages.
	t.Logf("final status: %s (error: %s)", dl.Status, dl.Error)
}

func TestCancelDownloadNoRezombie(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	m := NewManager(st, filepath.Join(dir, "downloads"), 2, nil, nil, nil, nil)

	id, err := m.Enqueue("p_cancel_test", "720p", "Cancel.Test.S01E01", "sonarr")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	m.CancelDownload(id)

	dl, _ := st.GetDownload(id)
	if dl != nil {
		t.Fatalf("download %s should be deleted, but still exists with status %q", id, dl.Status)
	}

	// v1.4.1+: CancelDownload now waits for any active worker to
	// release before deleting the DB row, so the rezombie window
	// the cancelled-map flag used to protect against can no longer
	// happen. v1.5.1 then clears the flag explicitly to stop the map
	// growing one entry per cancel (audit item 10). The remaining
	// protection is the DB row deletion + the synchronous wait, both
	// asserted above.
	if m.IsCancelled(id) {
		t.Error("expected cancelled-map entry to be cleared by CancelDownload (item 10)")
	}
}

func TestReconcileTitle(t *testing.T) {
	tests := []struct {
		name  string
		title string
		oldQ  string
		newQ  string
		want  string
	}{
		{
			name:  "1080p downgrade to 540p (Catherine Tate baseline)",
			title: "Catherine.Tate.Show.S01E01.1080p.WEB-DL.AAC.H264-iParr",
			oldQ:  "1080p",
			newQ:  "540p",
			want:  "Catherine.Tate.Show.S01E01.540p.WEB-DL.AAC.H264-iParr",
		},
		{
			name:  "720p upgrade to 1080p (FHD prober case)",
			title: "Some.Show.S03E04.720p.WEB-DL.AAC.H264-iParr",
			oldQ:  "720p",
			newQ:  "1080p",
			want:  "Some.Show.S03E04.1080p.WEB-DL.AAC.H264-iParr",
		},
		{
			name:  "title without .WEB-DL. is unchanged",
			title: "Custom.Manual.Title",
			oldQ:  "1080p",
			newQ:  "540p",
			want:  "Custom.Manual.Title",
		},
		{
			name:  "duplicate quality token: only the WEB-DL one is replaced",
			title: "1080p.Show.S01E01.1080p.WEB-DL.AAC.H264-iParr",
			oldQ:  "1080p",
			newQ:  "540p",
			want:  "1080p.Show.S01E01.540p.WEB-DL.AAC.H264-iParr",
		},
		{
			name:  "empty oldQ is a no-op",
			title: "Catherine.Tate.Show.S01E01.1080p.WEB-DL.AAC.H264-iParr",
			oldQ:  "",
			newQ:  "540p",
			want:  "Catherine.Tate.Show.S01E01.1080p.WEB-DL.AAC.H264-iParr",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reconcileTitle(tt.title, tt.oldQ, tt.newQ)
			if got != tt.want {
				t.Errorf("reconcileTitle(%q, %q, %q) = %q, want %q", tt.title, tt.oldQ, tt.newQ, got, tt.want)
			}
		})
	}
}

func TestEnqueueDedupSurvivesActualQualityUpdate_HistoryPath(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "test.db"))
	defer st.Close()

	m := NewManager(st, filepath.Join(dir, "downloads"), 2, nil, nil, nil, nil)

	// First enqueue, simulate worker reconciling actual=540p, then move to history.
	id1, err := m.Enqueue("p0123ab", "1080p", "Show.S01E01.1080p.WEB-DL.AAC.H264-iParr", "sonarr")
	if err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	dl, err := st.GetDownload(id1)
	if err != nil {
		t.Fatalf("get download: %v", err)
	}
	dl.ActualQuality = "540p"
	dl.Status = store.StatusCompleted
	if err := st.PutDownload(dl); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := st.MoveToHistory(id1); err != nil {
		t.Fatalf("move to history: %v", err)
	}

	// Sonarr re-grabs at the same requested quality. Dedup must hit
	// the history row keyed on the immutable Quality, not the
	// post-reconciliation ActualQuality.
	id2, err := m.Enqueue("p0123ab", "1080p", "Show.S01E01.1080p.WEB-DL.AAC.H264-iParr", "sonarr")
	if err != nil {
		t.Fatalf("second enqueue: %v", err)
	}
	if id1 != id2 {
		t.Errorf("history dedup broke: id1=%q, id2=%q", id1, id2)
	}

	// Independent assertion that FindHistoryByPIDQuality matches on
	// the requested-quality key.
	hit, err := st.FindHistoryByPIDQuality("p0123ab", "1080p")
	if err != nil {
		t.Fatalf("FindHistoryByPIDQuality: %v", err)
	}
	if hit == nil {
		t.Fatal("FindHistoryByPIDQuality returned nil despite ActualQuality=540p")
	}
	if hit.ID != id1 {
		t.Errorf("history row ID mismatch: got %q, want %q", hit.ID, id1)
	}
}

func TestEnqueueDedupSurvivesActualQualityUpdate_ActivePath(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "test.db"))
	defer st.Close()

	m := NewManager(st, filepath.Join(dir, "downloads"), 2, nil, nil, nil, nil)

	id1, err := m.Enqueue("p0987zy", "1080p", "Show.S02E03.1080p.WEB-DL.AAC.H264-iParr", "sonarr")
	if err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	dl, err := st.GetDownload(id1)
	if err != nil {
		t.Fatalf("get download: %v", err)
	}
	dl.ActualQuality = "540p" // mid-flight, before the worker calls MoveToHistory
	if err := st.PutDownload(dl); err != nil {
		t.Fatalf("put: %v", err)
	}

	id2, err := m.Enqueue("p0987zy", "1080p", "Show.S02E03.1080p.WEB-DL.AAC.H264-iParr", "sonarr")
	if err != nil {
		t.Fatalf("second enqueue: %v", err)
	}
	if id1 != id2 {
		t.Errorf("active dedup broke: id1=%q, id2=%q", id1, id2)
	}

	hit, err := st.FindDownloadByPIDQuality("p0987zy", "1080p")
	if err != nil {
		t.Fatalf("FindDownloadByPIDQuality: %v", err)
	}
	if hit == nil {
		t.Fatal("FindDownloadByPIDQuality returned nil despite ActualQuality=540p")
	}
	if hit.ID != id1 {
		t.Errorf("active row ID mismatch: got %q, want %q", hit.ID, id1)
	}
}

// TestCancelDownload_RemovesIncompleteDir verifies that cancelling an
// enqueued (pending) download with an existing incomplete/ output dir
// also wipes the directory, so a cancel doesn't leak partial mp4s on
// the NFS mount.
func TestCancelDownload_RemovesIncompleteDir(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "test.db"))
	defer st.Close()

	downloadDir := filepath.Join(dir, "downloads")
	m := NewManager(st, downloadDir, 2, nil, nil, nil, nil)

	id, err := m.Enqueue("p0cancel", "720p", "Cancel.Test.S01E01.720p", "sonarr")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	dl, _ := st.GetDownload(id)
	if dl == nil {
		t.Fatal("download not in store after Enqueue")
	}
	// Create the incomplete dir on disk so we can assert the cleanup.
	if err := os.MkdirAll(dl.OutputDir, 0o755); err != nil {
		t.Fatalf("mkdir incomplete: %v", err)
	}
	partial := filepath.Join(dl.OutputDir, "Cancel.Test.S01E01.720p.partial.mp4")
	if err := os.WriteFile(partial, []byte("not really mp4"), 0o644); err != nil {
		t.Fatalf("write partial: %v", err)
	}

	if err := m.CancelDownload(id); err != nil {
		t.Fatalf("CancelDownload: %v", err)
	}

	// DB row must be gone.
	if got, _ := st.GetDownload(id); got != nil {
		t.Errorf("download still in store after cancel: %+v", got)
	}
	// Output dir must be gone.
	if _, err := os.Stat(dl.OutputDir); !os.IsNotExist(err) {
		t.Errorf("incomplete dir %s still present after cancel: %v", dl.OutputDir, err)
	}
}

// TestCancelDownload_PreservesFinalisedDir verifies that if a worker
// already moved OutputDir out of incomplete/ to its final location,
// CancelDownload deletes the DB row but does NOT delete the completed
// file from disk.
func TestCancelDownload_PreservesFinalisedDir(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "test.db"))
	defer st.Close()

	downloadDir := filepath.Join(dir, "downloads")
	m := NewManager(st, downloadDir, 2, nil, nil, nil, nil)

	id, err := m.Enqueue("p0fina", "720p", "Final.Test.S01E01.720p", "sonarr")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	// Simulate finaliseDownload having moved OutputDir to the post-rename path.
	finalDir := filepath.Join(downloadDir, "Final.Test.S01E01.720p")
	if err := os.MkdirAll(finalDir, 0o755); err != nil {
		t.Fatalf("mkdir final: %v", err)
	}
	finalFile := filepath.Join(finalDir, "Final.Test.S01E01.720p.mp4")
	if err := os.WriteFile(finalFile, []byte("complete"), 0o644); err != nil {
		t.Fatalf("write final: %v", err)
	}
	dl, _ := st.GetDownload(id)
	dl.OutputDir = finalDir
	dl.OutputFile = finalFile
	if err := st.PutDownload(dl); err != nil {
		t.Fatalf("put: %v", err)
	}

	if err := m.CancelDownload(id); err != nil {
		t.Fatalf("CancelDownload: %v", err)
	}

	if got, _ := st.GetDownload(id); got != nil {
		t.Errorf("download still in store after cancel: %+v", got)
	}
	if _, err := os.Stat(finalFile); err != nil {
		t.Errorf("completed file removed by cancel (should be preserved): %v", err)
	}
}

// TestCancelDownload_WaitsForActiveWorker verifies that when a worker
// holds an active claim, CancelDownload polls until the worker
// releases. We don't run a real worker — we register a manual claim
// and release it after a short delay.
func TestCancelDownload_WaitsForActiveWorker(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "test.db"))
	defer st.Close()

	downloadDir := filepath.Join(dir, "downloads")
	m := NewManager(st, downloadDir, 2, nil, nil, nil, nil)

	id, err := m.Enqueue("p0wait", "720p", "Wait.Test.S01E01.720p", "sonarr")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Pretend a worker is processing this download.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelObserved := make(chan struct{})
	wrappedCancel := func() {
		close(cancelObserved)
		cancel()
	}
	if !m.claim(id, wrappedCancel) {
		t.Fatal("claim failed")
	}

	// Release the claim after 300ms — long enough that the poll has
	// to iterate at least once, short enough that the test runs fast.
	go func() {
		<-cancelObserved
		time.Sleep(300 * time.Millisecond)
		m.release(id)
	}()

	start := time.Now()
	if err := m.CancelDownload(id); err != nil {
		t.Fatalf("CancelDownload: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed < 250*time.Millisecond {
		t.Errorf("CancelDownload returned in %v, expected >=250ms (it should have waited for release)", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Errorf("CancelDownload took %v, expected <2s (polling backed up)", elapsed)
	}
	if got, _ := st.GetDownload(id); got != nil {
		t.Errorf("download still in store after cancel: %+v", got)
	}
	_ = ctx
}

// TestCleanupIncompleteDir_PathGuards verifies that the cleanup helper
// refuses paths outside <downloadDir>/incomplete/.
func TestCleanupIncompleteDir_PathGuards(t *testing.T) {
	dir := t.TempDir()
	downloadDir := filepath.Join(dir, "downloads")
	m := NewManager(nil, downloadDir, 0, nil, nil, nil, nil)

	if err := m.cleanupIncompleteDir(""); err != nil {
		t.Errorf("empty path should return nil, got %v", err)
	}
	// Path inside incomplete/ — happy path, dir doesn't exist but
	// RemoveAll on missing path is fine.
	good := filepath.Join(downloadDir, "incomplete", "Some.Title")
	if err := m.cleanupIncompleteDir(good); err != nil {
		t.Errorf("incomplete subdir: unexpected error %v", err)
	}
	// Path outside incomplete/ — finalised case, returns nil (not an error).
	final := filepath.Join(downloadDir, "Some.Title")
	if err := m.cleanupIncompleteDir(final); err != nil {
		t.Errorf("finalised dir should return nil, got %v", err)
	}
	// Refuses the incomplete root itself.
	root := filepath.Join(downloadDir, "incomplete")
	if err := m.cleanupIncompleteDir(root); err == nil {
		t.Error("cleaning the incomplete root itself should error, got nil")
	}
	// Refuses traversal: outside the downloadDir entirely.
	escape := filepath.Join(downloadDir, "incomplete", "..", "..", "etc")
	if err := m.cleanupIncompleteDir(escape); err != nil {
		t.Errorf("escape path should return nil (not an error), got %v", err)
	}
	// The escape MUST NOT actually delete /etc. The path test was just
	// that the function doesn't error — but the safety check is that
	// the path resolves outside incomplete/ so RemoveAll never runs.
	if _, err := os.Stat("/etc"); err != nil {
		t.Fatalf("/etc disappeared during test — path-guard regression: %v", err)
	}
}
