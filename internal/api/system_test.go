package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/download"

	"github.com/Will-Luck/iplayer-arr/internal/store"
)

func TestHandleSystemBasic(t *testing.T) {
	h, _ := testAPI(t)
	h.StartedAt = time.Now().Add(-5 * time.Second)

	req := authedRequest(http.MethodGet, "/api/system?apikey=test-api-key", nil)
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

// TestHandleSystem_NilMgr_RuntimeConfigSafelyZero: with no Manager
// wired (the default testAPI shape), /api/system still responds 200
// and the runtime-config fields default to zero. Locks the safe
// degradation path so we never NPE on early-boot or test fixtures.
func TestHandleSystem_NilMgr_RuntimeConfigSafelyZero(t *testing.T) {
	h, _ := testAPI(t)
	if h.mgr != nil {
		t.Fatalf("testAPI wired a non-nil mgr; precondition broken")
	}

	req := authedRequest(http.MethodGet, "/api/system?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var info SystemInfo
	if err := json.NewDecoder(w.Body).Decode(&info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if info.MaxWorkers != 0 {
		t.Errorf("MaxWorkers = %d, want 0 (nil mgr)", info.MaxWorkers)
	}
	if info.WatchdogTimeoutSeconds != 0 {
		t.Errorf("WatchdogTimeoutSeconds = %d, want 0 (nil mgr)", info.WatchdogTimeoutSeconds)
	}
}

// TestHandleSystem_RuntimeConfigSurfacesManagerValues: when a Manager
// is wired, MaxWorkers and WatchdogTimeoutSeconds reflect its actual
// values. Verdict on watchdog: zero on the Manager -> 60s (package
// default) is exposed; positive value passes through. Closes the loop
// on #42 self-verification.
func TestHandleSystem_RuntimeConfigSurfacesManagerValues(t *testing.T) {
	h, _ := testAPI(t)

	// Manager with explicit watchdog timeout override.
	mgrWithOverride := download.NewManager(
		nil, t.TempDir(), 3, nil, nil, nil, nil,
		download.WithWatchdogTimeout(120*time.Second))
	h.mgr = mgrWithOverride

	req := authedRequest(http.MethodGet, "/api/system?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var info SystemInfo
	if err := json.NewDecoder(w.Body).Decode(&info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if info.MaxWorkers != 3 {
		t.Errorf("MaxWorkers = %d, want 3", info.MaxWorkers)
	}
	if info.WatchdogTimeoutSeconds != 120 {
		t.Errorf("WatchdogTimeoutSeconds = %d, want 120 (override)", info.WatchdogTimeoutSeconds)
	}

	// Same Handler, new Manager without override -> package default surfaces.
	mgrDefault := download.NewManager(nil, t.TempDir(), 2, nil, nil, nil, nil)
	h.mgr = mgrDefault

	req = authedRequest(http.MethodGet, "/api/system?apikey=test-api-key", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)

	info = SystemInfo{}
	if err := json.NewDecoder(w.Body).Decode(&info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if info.MaxWorkers != 2 {
		t.Errorf("MaxWorkers = %d, want 2", info.MaxWorkers)
	}
	if info.WatchdogTimeoutSeconds != 60 {
		t.Errorf("WatchdogTimeoutSeconds = %d, want 60 (package default surfaces when Manager.WatchdogTimeout is zero)", info.WatchdogTimeoutSeconds)
	}
}

func TestHandleSystemNoAuth(t *testing.T) {
	h, _ := testAPI(t)
	h.StartedAt = time.Now().Add(-5 * time.Second)

	req := authedRequest(http.MethodGet, "/api/system", nil)
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

	req := authedRequest(http.MethodGet, "/api/system?apikey=test-api-key", nil)
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

	req := authedRequest(http.MethodGet, "/api/system?apikey=test-api-key", nil)
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
	h.GeoProbe = func() bbc.GeoResult { return bbc.GeoResult{Status: bbc.GeoUKOK} }

	req := authedRequest(http.MethodPost, "/api/system/geo-check?apikey=test-api-key", nil)
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
	if resp["geo_status"] != "uk_ok" {
		t.Errorf("geo_status = %v, want uk_ok", resp["geo_status"])
	}
	if !h.status.GeoOK {
		t.Error("expected h.status.GeoOK to be updated to true")
	}
	if h.status.GeoCheckedAt == "" {
		t.Error("expected GeoCheckedAt to be set")
	}
}

func TestHandleGeoCheckDNSFailed(t *testing.T) {
	h, _ := testAPI(t)
	h.status = &RuntimeStatus{GeoOK: true}
	h.GeoProbe = func() bbc.GeoResult {
		return bbc.GeoResult{Status: bbc.GeoDNSFailed, Detail: "server misbehaving"}
	}

	req := authedRequest(http.MethodPost, "/api/system/geo-check?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["geo_ok"].(bool) {
		t.Error("expected geo_ok=false for dns_failed")
	}
	if resp["geo_status"] != "dns_failed" {
		t.Errorf("geo_status = %v, want dns_failed", resp["geo_status"])
	}
	if h.status.GeoOK {
		t.Error("expected h.status.GeoOK to be cleared to false")
	}
}

func TestHandleGeoCheckNilProbe(t *testing.T) {
	h, _ := testAPI(t)
	// geoProbe is nil by default in testAPI

	req := authedRequest(http.MethodPost, "/api/system/geo-check?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestHandleGeoCheckNoAuth(t *testing.T) {
	h, _ := testAPI(t)
	h.status = &RuntimeStatus{GeoOK: false}
	h.GeoProbe = func() bbc.GeoResult { return bbc.GeoResult{Status: bbc.GeoUKOK} }

	req := authedRequest(http.MethodPost, "/api/system/geo-check", nil)
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
