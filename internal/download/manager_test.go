package download

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

	if m.IsCancelled(id) != true {
		t.Error("expected IsCancelled to return true for a cancelled download")
	}
}
