package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/GiteaLN/iplayer-arr/internal/store"
)

func TestHandleSystemBasic(t *testing.T) {
	h, _ := testAPI(t)
	h.StartedAt = time.Now().Add(-5 * time.Second)

	req := httptest.NewRequest(http.MethodGet, "/api/system?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var info SystemInfo
	if err := json.NewDecoder(w.Body).Decode(&info); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if info.GoVersion == "" {
		t.Error("go_version is empty")
	}
	if info.UptimeSeconds < 5 {
		t.Errorf("uptime_seconds = %d, want >= 5", info.UptimeSeconds)
	}
	if info.Version == "" {
		t.Error("version is empty")
	}
}

func TestHandleSystemNoAuth(t *testing.T) {
	h, _ := testAPI(t)
	h.StartedAt = time.Now().Add(-5 * time.Second)

	req := httptest.NewRequest(http.MethodGet, "/api/system", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestHandleSystemGeoStatus(t *testing.T) {
	h, _ := testAPI(t)
	h.status = &RuntimeStatus{
		GeoOK:         true,
		GeoCheckedAt:  "2026-04-01T10:00:00Z",
		FFmpegVersion: "ffmpeg version 6.0",
	}

	req := httptest.NewRequest(http.MethodGet, "/api/system?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var info SystemInfo
	json.NewDecoder(w.Body).Decode(&info)

	if !info.GeoOK {
		t.Error("expected geo_ok=true")
	}
	if info.GeoCheckedAt != "2026-04-01T10:00:00Z" {
		t.Errorf("geo_checked_at = %q", info.GeoCheckedAt)
	}
	if info.FFmpegVersion != "ffmpeg version 6.0" {
		t.Errorf("ffmpeg_version = %q", info.FFmpegVersion)
	}
}

func TestHandleSystemHistoryCounts(t *testing.T) {
	h, st := testAPI(t)

	// Two completed downloads, one failed.
	for _, dl := range []*store.Download{
		{ID: "sys_c1", PID: "p1", Title: "A", Status: store.StatusCompleted, Size: 500_000_000},
		{ID: "sys_c2", PID: "p2", Title: "B", Status: store.StatusCompleted, Size: 300_000_000},
		{ID: "sys_f1", PID: "p3", Title: "C", Status: store.StatusFailed},
	} {
		st.PutDownload(dl)
		st.MoveToHistory(dl.ID)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/system?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var info SystemInfo
	json.NewDecoder(w.Body).Decode(&info)

	if info.DownloadsCompleted != 2 {
		t.Errorf("downloads_completed = %d, want 2", info.DownloadsCompleted)
	}
	if info.DownloadsFailed != 1 {
		t.Errorf("downloads_failed = %d, want 1", info.DownloadsFailed)
	}
	if info.DownloadsTotalBytes != 800_000_000 {
		t.Errorf("downloads_total_bytes = %d, want 800000000", info.DownloadsTotalBytes)
	}
}

func TestHandleGeoCheckSuccess(t *testing.T) {
	h, _ := testAPI(t)
	h.status = &RuntimeStatus{GeoOK: false}
	h.GeoProbe = func() bool { return true }

	req := httptest.NewRequest(http.MethodPost, "/api/system/geo-check?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	if !resp["geo_ok"].(bool) {
		t.Error("expected geo_ok=true in response")
	}
	if !h.status.GeoOK {
		t.Error("expected h.status.GeoOK to be updated to true")
	}
	if h.status.GeoCheckedAt == "" {
		t.Error("expected GeoCheckedAt to be set")
	}
}

func TestHandleGeoCheckNilProbe(t *testing.T) {
	h, _ := testAPI(t)
	// geoProbe is nil by default in testAPI

	req := httptest.NewRequest(http.MethodPost, "/api/system/geo-check?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestHandleGeoCheckNoAuth(t *testing.T) {
	h, _ := testAPI(t)
	h.status = &RuntimeStatus{GeoOK: false}
	h.GeoProbe = func() bool { return true }

	req := httptest.NewRequest(http.MethodPost, "/api/system/geo-check", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if !resp["geo_ok"].(bool) {
		t.Error("expected geo_ok=true in response")
	}
}
