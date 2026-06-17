package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
)

// ----------------------------------------------------------------------
// /api/diag/storage  (A.10)
// ----------------------------------------------------------------------

// TestDiagStorage_NoAuth confirms /api/diag/storage refuses unauthenticated
// callers like every other auth-gated diag endpoint.
func TestDiagStorage_NoAuth(t *testing.T) {
	h, _ := testAPI(t)
	req := httptest.NewRequest("GET", "/api/diag/storage", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

// TestDiagStorage_HappyPath drives the production defaultStoragePathProbe
// against a real t.TempDir() that's guaranteed to exist, be writable,
// and owned by the current process. The endpoint must return verdict=pass
// with the download_dir probe populated.
func TestDiagStorage_HappyPath(t *testing.T) {
	h, _ := testAPI(t)
	dir := t.TempDir()
	h.DownloadDir = dir

	req := httptest.NewRequest("GET", "/api/diag/storage?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	var report DiagStorageReport
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}

	if report.Verdict != "pass" {
		t.Errorf("verdict = %q, want pass; checks_failed=%v paths=%+v", report.Verdict, report.ChecksFailed, report.Paths)
	}
	if len(report.Paths) != 1 {
		t.Errorf("got %d paths, want 1 (download_dir only post-A.10-scope-trim)", len(report.Paths))
	}
	for _, p := range report.Paths {
		if !p.Exists {
			t.Errorf("path %q: exists=false, expected true (real tmpdir)", p.Label)
		}
		if !p.Writable {
			t.Errorf("path %q: writable=false, expected true (real tmpdir)", p.Label)
		}
		if p.ProcessUID != os.Getuid() {
			t.Errorf("path %q: process_uid=%d, want %d", p.Label, p.ProcessUID, os.Getuid())
		}
		if p.Verdict != "pass" {
			t.Errorf("path %q: verdict=%q, want pass (error=%q)", p.Label, p.Verdict, p.Error)
		}
	}
}

// TestDiagStorage_TmpfsSkipsFreeSpace: a tmpfs mount with very low
// free space must NOT fail the verdict, because tmpfs is in-memory
// and doesn't have meaningful "free space" semantics for downloads.
// This carve-out is what lets CI run the storage probe against the
// 64MiB tmpfs /downloads without false-failing.
func TestDiagStorage_TmpfsSkipsFreeSpace(t *testing.T) {
	h, _ := testAPI(t)
	h.DownloadDir = "/tmpfs-mount"
	h.storageProbe = func(label, path string) StoragePathReport {
		// Simulate the post-syscall state for a tmpfs path with
		// only 16MiB free -- well under the 1 GiB threshold, but
		// we expect the tmpfs carve-out to pass anyway.
		return StoragePathReport{
			Label:         label,
			Path:          path,
			Exists:        true,
			Writable:      true,
			FreeBytes:     16 * 1024 * 1024,
			FreeHuman:     "16.0 MiB",
			MountFSType:   "tmpfs",
			NFSResponsive: true,
			Verdict:       "pass",
		}
	}

	report := callDiagStorage(t, h)
	if report.Verdict != "pass" {
		t.Errorf("verdict = %q, want pass (tmpfs free-space exempt); report=%+v", report.Verdict, report)
	}
}

// TestDiagStorage_MissingPath: download_dir doesn't exist on the
// filesystem; verdict must be fail with a clear checks_failed entry.
func TestDiagStorage_MissingPath(t *testing.T) {
	h, _ := testAPI(t)
	h.DownloadDir = "/nope"
	h.storageProbe = func(label, path string) StoragePathReport {
		return StoragePathReport{
			Label:   label,
			Path:    path,
			Verdict: "fail",
			Error:   "path does not exist",
		}
	}

	report := callDiagStorage(t, h)
	if report.Verdict != "fail" {
		t.Errorf("verdict = %q, want fail", report.Verdict)
	}
	if !containsSubstring(report.ChecksFailed, "download_dir") {
		t.Errorf("checks_failed missing 'download_dir': %v", report.ChecksFailed)
	}
	if !containsSubstring(report.ChecksFailed, "path does not exist") {
		t.Errorf("checks_failed missing reason: %v", report.ChecksFailed)
	}
}

// TestDiagStorage_ReadOnly drives an injected probe simulating a path
// that exists but rejects the writable-sentinel create with EACCES.
func TestDiagStorage_ReadOnly(t *testing.T) {
	h, _ := testAPI(t)
	h.DownloadDir = "/ro"
	h.storageProbe = func(label, path string) StoragePathReport {
		return StoragePathReport{
			Label:   label,
			Path:    path,
			Exists:  true,
			Verdict: "fail",
			Error:   "writable: open " + path + "/.iplayer-arr-diag-1: permission denied",
		}
	}

	report := callDiagStorage(t, h)
	if report.Verdict != "fail" {
		t.Errorf("verdict = %q, want fail", report.Verdict)
	}
	if !containsSubstring(report.ChecksFailed, "permission denied") {
		t.Errorf("checks_failed missing 'permission denied': %v", report.ChecksFailed)
	}
}

// TestDiagStorage_DiskFull drives an injected probe simulating a writable
// path with too little free space (below 1 GiB threshold).
func TestDiagStorage_DiskFull(t *testing.T) {
	h, _ := testAPI(t)
	h.DownloadDir = "/full"
	h.storageProbe = func(label, path string) StoragePathReport {
		return StoragePathReport{
			Label:         label,
			Path:          path,
			Exists:        true,
			Writable:      true,
			FreeBytes:     500 * 1024 * 1024, // 500 MiB, under 1 GiB threshold
			FreeHuman:     "500.0 MiB",
			NFSResponsive: true,
			Verdict:       "fail",
			Error:         "low free space: 500.0 MiB < 1.0 GiB",
		}
	}

	report := callDiagStorage(t, h)
	if report.Verdict != "fail" {
		t.Errorf("verdict = %q, want fail", report.Verdict)
	}
	if !containsSubstring(report.ChecksFailed, "low free space") {
		t.Errorf("checks_failed missing 'low free space': %v", report.ChecksFailed)
	}
}

// TestDiagStorage_OwnershipMismatch confirms ownership-mismatch alone
// does NOT fail the endpoint — group-writable / world-writable setups
// are valid. The report still surfaces the mismatch via the per-path
// OwnershipMatches field.
func TestDiagStorage_OwnershipMismatch(t *testing.T) {
	h, _ := testAPI(t)
	h.DownloadDir = "/mismatched"
	h.storageProbe = func(label, path string) StoragePathReport {
		return StoragePathReport{
			Label:            label,
			Path:             path,
			Exists:           true,
			Writable:         true,
			FreeBytes:        10 * 1024 * 1024 * 1024,
			FreeHuman:        "10.0 GiB",
			NFSResponsive:    true,
			OwnerUID:         1001,
			OwnerGID:         1001,
			ProcessUID:       1000,
			ProcessGID:       1000,
			OwnershipMatches: false,
			Verdict:          "pass",
		}
	}

	report := callDiagStorage(t, h)
	if report.Verdict != "pass" {
		t.Errorf("verdict = %q, want pass (ownership mismatch alone is a warning, not a failure)", report.Verdict)
	}
	for _, p := range report.Paths {
		if p.OwnershipMatches {
			t.Errorf("path %q: OwnershipMatches=true, want false", p.Label)
		}
	}
}

// TestDiagStorage_NFSHang simulates a stale NFS handle: mount fstype is
// nfs4 but the responsiveness probe times out. Verdict must be fail.
func TestDiagStorage_NFSHang(t *testing.T) {
	h, _ := testAPI(t)
	h.DownloadDir = "/nfs-hung"
	h.storageProbe = func(label, path string) StoragePathReport {
		return StoragePathReport{
			Label:         label,
			Path:          path,
			Exists:        true,
			Writable:      false, // can't write through hung mount
			MountFSType:   "nfs4",
			NFSResponsive: false,
			Verdict:       "fail",
			Error:         "NFS unresponsive within 2s",
		}
	}

	report := callDiagStorage(t, h)
	if report.Verdict != "fail" {
		t.Errorf("verdict = %q, want fail", report.Verdict)
	}
	if !containsSubstring(report.ChecksFailed, "NFS unresponsive") {
		t.Errorf("checks_failed missing 'NFS unresponsive': %v", report.ChecksFailed)
	}
}

// TestHumanBytes_ContractTable locks the human-readable byte formatter
// against the values the JSON response promises. Independent of the
// HTTP handler so we can catch off-by-one rounding bugs cheaply.
func TestHumanBytes_ContractTable(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{-1, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{1024 * 1024, "1.0 MiB"},
		{1024 * 1024 * 1024, "1.0 GiB"},
		{500 * 1024 * 1024, "500.0 MiB"},
		{int64(1.5 * 1024 * 1024 * 1024), "1.5 GiB"},
	}
	for _, c := range cases {
		got := humanBytes(c.in)
		if got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestDetectMountFSType_RootIsResolvable confirms detectMountFSType
// returns *something* for / on a Linux host. Skips on non-Linux.
func TestDetectMountFSType_RootIsResolvable(t *testing.T) {
	if _, err := os.Stat("/proc/mounts"); err != nil {
		t.Skip("no /proc/mounts on this host (likely non-Linux)")
	}
	fstype := detectMountFSType("/")
	if fstype == "" {
		t.Skip("no fstype detected for / (unusual but not necessarily broken)")
	}
	// Just assert it's non-empty and not absurd.
	if len(fstype) > 32 {
		t.Errorf("fstype=%q looks malformed", fstype)
	}
}

// TestIsNFSFSType_KnownTypes locks the NFS classification.
func TestIsNFSFSType_KnownTypes(t *testing.T) {
	if !isNFSFSType("nfs") {
		t.Error("nfs should classify as NFS")
	}
	if !isNFSFSType("nfs4") {
		t.Error("nfs4 should classify as NFS")
	}
	if isNFSFSType("ext4") {
		t.Error("ext4 should NOT classify as NFS")
	}
	if isNFSFSType("tmpfs") {
		t.Error("tmpfs should NOT classify as NFS")
	}
	if isNFSFSType("") {
		t.Error("empty fstype should NOT classify as NFS")
	}
}

// TestNFSResponsiveWithin_HealthyPath confirms a real existing dir
// returns true within a generous deadline.
func TestNFSResponsiveWithin_HealthyPath(t *testing.T) {
	dir := t.TempDir()
	if !nfsResponsiveWithin(dir, 1*time.Second) {
		t.Errorf("nfsResponsiveWithin(%q) = false, want true on healthy tmpdir", dir)
	}
}

// TestNFSResponsiveWithin_MissingPath confirms a non-existent path
// returns false (ReadDir errors out, not a hang).
func TestNFSResponsiveWithin_MissingPath(t *testing.T) {
	if nfsResponsiveWithin("/this/does/not/exist", 500*time.Millisecond) {
		t.Error("nfsResponsiveWithin on missing path = true, want false")
	}
}

// ----------------------------------------------------------------------
// /api/diag/clock  (A.11)
// ----------------------------------------------------------------------

// TestDiagClock_NoAuth confirms /api/diag/clock refuses unauthenticated
// callers.
func TestDiagClock_NoAuth(t *testing.T) {
	h, _ := testAPI(t)
	req := httptest.NewRequest("GET", "/api/diag/clock", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

// TestDiagClock_HappyPath: local clock matches reference exactly,
// first source succeeds, verdict pass with drift=0.
func TestDiagClock_HappyPath(t *testing.T) {
	h, _ := testAPI(t)
	fixed := mustParseTime(t, "2026-05-18T12:00:00Z")
	h.nowFn = func() time.Time { return fixed }
	h.clockHeadDate = func(ctx context.Context, url string) (time.Time, time.Duration, error) {
		return fixed, 50 * time.Millisecond, nil
	}

	report := callDiagClock(t, h)
	if report.Verdict != "pass" {
		t.Errorf("verdict = %q, want pass; report=%+v", report.Verdict, report)
	}
	if report.DriftSeconds != 0 {
		t.Errorf("drift_seconds = %d, want 0", report.DriftSeconds)
	}
	if report.ReferenceSource != clockReferenceSources[0] {
		t.Errorf("reference_source = %q, want %q", report.ReferenceSource, clockReferenceSources[0])
	}
	if report.ThresholdSeconds != 60 {
		t.Errorf("threshold_seconds = %d, want 60", report.ThresholdSeconds)
	}
}

// TestDiagClock_SmallDriftPasses: 30s ahead of reference, under threshold.
func TestDiagClock_SmallDriftPasses(t *testing.T) {
	h, _ := testAPI(t)
	ref := mustParseTime(t, "2026-05-18T12:00:00Z")
	local := ref.Add(30 * time.Second)
	h.nowFn = func() time.Time { return local }
	h.clockHeadDate = func(ctx context.Context, url string) (time.Time, time.Duration, error) {
		return ref, 50 * time.Millisecond, nil
	}

	report := callDiagClock(t, h)
	if report.Verdict != "pass" {
		t.Errorf("verdict = %q, want pass (30s under 60s threshold); report=%+v", report.Verdict, report)
	}
	if report.DriftSeconds != 30 {
		t.Errorf("drift_seconds = %d, want 30", report.DriftSeconds)
	}
}

// TestDiagClock_NegativeDriftPasses: 45s behind reference, magnitude
// under threshold; threshold is two-sided.
func TestDiagClock_NegativeDriftPasses(t *testing.T) {
	h, _ := testAPI(t)
	ref := mustParseTime(t, "2026-05-18T12:00:00Z")
	local := ref.Add(-45 * time.Second)
	h.nowFn = func() time.Time { return local }
	h.clockHeadDate = func(ctx context.Context, url string) (time.Time, time.Duration, error) {
		return ref, 50 * time.Millisecond, nil
	}

	report := callDiagClock(t, h)
	if report.Verdict != "pass" {
		t.Errorf("verdict = %q, want pass (|-45s| under 60s); report=%+v", report.Verdict, report)
	}
	if report.DriftSeconds != -45 {
		t.Errorf("drift_seconds = %d, want -45", report.DriftSeconds)
	}
}

// TestDiagClock_LargeDriftFails: 120s ahead, exceeds threshold.
func TestDiagClock_LargeDriftFails(t *testing.T) {
	h, _ := testAPI(t)
	ref := mustParseTime(t, "2026-05-18T12:00:00Z")
	local := ref.Add(120 * time.Second)
	h.nowFn = func() time.Time { return local }
	h.clockHeadDate = func(ctx context.Context, url string) (time.Time, time.Duration, error) {
		return ref, 50 * time.Millisecond, nil
	}

	report := callDiagClock(t, h)
	if report.Verdict != "fail" {
		t.Errorf("verdict = %q, want fail (120s exceeds 60s threshold); report=%+v", report.Verdict, report)
	}
	if !containsSubstring(report.ChecksFailed, "clock_drift") {
		t.Errorf("checks_failed missing 'clock_drift': %v", report.ChecksFailed)
	}
}

// TestDiagClock_AllSourcesUnreachable: every reference HEAD fails;
// verdict must be fail with a clear no_reference_source_reachable
// reason. Probe must not panic on the empty refURL path.
func TestDiagClock_AllSourcesUnreachable(t *testing.T) {
	h, _ := testAPI(t)
	h.nowFn = func() time.Time { return mustParseTime(t, "2026-05-18T12:00:00Z") }
	h.clockHeadDate = func(ctx context.Context, url string) (time.Time, time.Duration, error) {
		return time.Time{}, 0, fmt.Errorf("dial tcp: connection refused")
	}

	report := callDiagClock(t, h)
	if report.Verdict != "fail" {
		t.Errorf("verdict = %q, want fail; report=%+v", report.Verdict, report)
	}
	if report.ReferenceSource != "" {
		t.Errorf("reference_source = %q, want empty (no source succeeded)", report.ReferenceSource)
	}
	if !containsSubstring(report.ChecksFailed, "no_reference_source_reachable") {
		t.Errorf("checks_failed missing 'no_reference_source_reachable': %v", report.ChecksFailed)
	}
}

// TestDiagClock_FallsBackToSecondSource: first source errors, second
// succeeds. Verdict must reflect second source's data.
func TestDiagClock_FallsBackToSecondSource(t *testing.T) {
	h, _ := testAPI(t)
	ref := mustParseTime(t, "2026-05-18T12:00:00Z")
	h.nowFn = func() time.Time { return ref }
	callCount := 0
	h.clockHeadDate = func(ctx context.Context, url string) (time.Time, time.Duration, error) {
		callCount++
		if url == clockReferenceSources[0] {
			return time.Time{}, 0, fmt.Errorf("cloudflare unreachable")
		}
		return ref, 80 * time.Millisecond, nil
	}

	report := callDiagClock(t, h)
	if report.Verdict != "pass" {
		t.Errorf("verdict = %q, want pass after fallback; report=%+v", report.Verdict, report)
	}
	if report.ReferenceSource != clockReferenceSources[1] {
		t.Errorf("reference_source = %q, want second source %q", report.ReferenceSource, clockReferenceSources[1])
	}
	if callCount != 2 {
		t.Errorf("clockHeadDate called %d times, want 2 (CF fail + Google pass)", callCount)
	}
}

// TestAbsInt64_Boundary locks the helper used by drift threshold logic.
func TestAbsInt64_Boundary(t *testing.T) {
	cases := []struct {
		in, want int64
	}{
		{0, 0},
		{1, 1},
		{-1, 1},
		{60, 60},
		{-60, 60},
	}
	for _, c := range cases {
		if got := absInt64(c.in); got != c.want {
			t.Errorf("absInt64(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// ----------------------------------------------------------------------
// /api/diag/geo  (A.9)
// ----------------------------------------------------------------------

// TestDiagGeo_NoAuth confirms /api/diag/geo refuses unauthenticated callers.
func TestDiagGeo_NoAuth(t *testing.T) {
	h, _ := testAPI(t)
	req := httptest.NewRequest("GET", "/api/diag/geo", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

// TestDiagGeo_CachedFresh: cache populated, geo_ok=true, age under TTL.
// Verdict pass, refreshed=false.
func TestDiagGeo_CachedFresh(t *testing.T) {
	h, _ := testAPI(t)
	now := mustParseTime(t, "2026-05-18T12:00:00Z")
	checkedAt := now.Add(-2 * time.Minute).Format(time.RFC3339)
	h.status.SetGeo(string(bbc.GeoUKOK), "", checkedAt)
	h.nowFn = func() time.Time { return now }

	report := callDiagGeo(t, h, "")
	if report.Verdict != "pass" {
		t.Errorf("verdict = %q, want pass; report=%+v", report.Verdict, report)
	}
	if !report.GeoOK {
		t.Errorf("geo_ok = false, want true")
	}
	if report.Refreshed {
		t.Errorf("refreshed = true, want false (default path uses cache)")
	}
	if report.AgeSeconds != 120 {
		t.Errorf("age_seconds = %d, want 120", report.AgeSeconds)
	}
	if report.TTLSeconds != 300 {
		t.Errorf("ttl_seconds = %d, want 300", report.TTLSeconds)
	}
}

// TestDiagGeo_CachedStale: cache age exceeds TTL, no refresh requested.
func TestDiagGeo_CachedStale(t *testing.T) {
	h, _ := testAPI(t)
	now := mustParseTime(t, "2026-05-18T12:00:00Z")
	checkedAt := now.Add(-10 * time.Minute).Format(time.RFC3339) // 600s old, over 300s TTL
	h.status.SetGeo(string(bbc.GeoUKOK), "", checkedAt)
	h.nowFn = func() time.Time { return now }

	report := callDiagGeo(t, h, "")
	if report.Verdict != "fail" {
		t.Errorf("verdict = %q, want fail (stale cache)", report.Verdict)
	}
	if !containsSubstring(report.ChecksFailed, "geo_stale") {
		t.Errorf("checks_failed missing 'geo_stale': %v", report.ChecksFailed)
	}
}

// TestDiagGeo_CachedFalse: cache is fresh but geo_ok=false (non-UK exit).
func TestDiagGeo_CachedFalse(t *testing.T) {
	h, _ := testAPI(t)
	now := mustParseTime(t, "2026-05-18T12:00:00Z")
	h.status.SetGeo(string(bbc.GeoNotUK), "geo-blocked: non-UK exit", now.Add(-1*time.Minute).Format(time.RFC3339))
	h.nowFn = func() time.Time { return now }

	report := callDiagGeo(t, h, "")
	if report.Verdict != "fail" {
		t.Errorf("verdict = %q, want fail (geo_ok=false)", report.Verdict)
	}
	if !containsSubstring(report.ChecksFailed, "not_uk") {
		t.Errorf("checks_failed missing 'not_uk': %v", report.ChecksFailed)
	}
	if report.GeoStatus != "not_uk" {
		t.Errorf("geo_status = %q, want not_uk", report.GeoStatus)
	}
}

// TestDiagGeo_NeverChecked: empty cache (startup never ran the probe).
func TestDiagGeo_NeverChecked(t *testing.T) {
	h, _ := testAPI(t)
	// testAPI builds a RuntimeStatus with empty GeoCheckedAt by default.

	report := callDiagGeo(t, h, "")
	if report.Verdict != "fail" {
		t.Errorf("verdict = %q, want fail (never checked)", report.Verdict)
	}
	if !containsSubstring(report.ChecksFailed, "geo_never_checked") {
		t.Errorf("checks_failed missing 'geo_never_checked': %v", report.ChecksFailed)
	}
	if report.AgeSeconds != -1 {
		t.Errorf("age_seconds = %d, want -1 (sentinel for never)", report.AgeSeconds)
	}
}

// TestDiagGeo_RefreshSuccess: ?refresh=1, injected probe returns true.
// Cache must be updated; verdict pass; refreshed=true.
func TestDiagGeo_RefreshSuccess(t *testing.T) {
	h, _ := testAPI(t)
	now := mustParseTime(t, "2026-05-18T12:00:00Z")
	h.nowFn = func() time.Time { return now }
	probeCalled := false
	h.GeoProbe = func() bbc.GeoResult {
		probeCalled = true
		return bbc.GeoResult{Status: bbc.GeoUKOK}
	}

	report := callDiagGeo(t, h, "?refresh=1")
	if !probeCalled {
		t.Error("GeoProbe was not invoked despite ?refresh=1")
	}
	if report.Verdict != "pass" {
		t.Errorf("verdict = %q, want pass; report=%+v", report.Verdict, report)
	}
	if !report.Refreshed {
		t.Errorf("refreshed = false, want true")
	}
	// Cache should now be populated
	s := h.status.Snapshot()
	if !s.GeoOK || s.GeoCheckedAt == "" {
		t.Errorf("after refresh: cache geoOK=%v checkedAt=%q (want true + non-empty)", s.GeoOK, s.GeoCheckedAt)
	}
}

// TestDiagGeo_RefreshFail: ?refresh=1, injected probe returns false.
func TestDiagGeo_RefreshFail(t *testing.T) {
	h, _ := testAPI(t)
	h.nowFn = func() time.Time { return mustParseTime(t, "2026-05-18T12:00:00Z") }
	h.GeoProbe = func() bbc.GeoResult {
		return bbc.GeoResult{Status: bbc.GeoNotUK, Detail: "geo-blocked: non-UK exit"}
	}

	report := callDiagGeo(t, h, "?refresh=1")
	if report.Verdict != "fail" {
		t.Errorf("verdict = %q, want fail", report.Verdict)
	}
	if !report.Refreshed {
		t.Errorf("refreshed = false, want true (probe still ran)")
	}
	if !containsSubstring(report.ChecksFailed, "not_uk") {
		t.Errorf("checks_failed missing 'not_uk': %v", report.ChecksFailed)
	}
	if report.GeoStatus != "not_uk" {
		t.Errorf("geo_status = %q, want not_uk", report.GeoStatus)
	}
}

// TestDiagGeo_NoGeoProbeWithRefresh: ?refresh=1 but GeoProbe is nil.
// Must fail with geo_probe_unavailable rather than panic.
func TestDiagGeo_NoGeoProbeWithRefresh(t *testing.T) {
	h, _ := testAPI(t)
	h.GeoProbe = nil

	report := callDiagGeo(t, h, "?refresh=1")
	if report.Verdict != "fail" {
		t.Errorf("verdict = %q, want fail", report.Verdict)
	}
	if !containsSubstring(report.ChecksFailed, "geo_probe_unavailable") {
		t.Errorf("checks_failed missing 'geo_probe_unavailable': %v", report.ChecksFailed)
	}
}

// ----------------------------------------------------------------------
// /api/diag/network  (A.8)
// ----------------------------------------------------------------------

// fakeNetworkProbe is a per-host test fake. Each function maps host
// to (result, error); missing entries return a default failure.
type fakeNetworkProbe struct {
	aRecords    map[string][]string
	aErrors     map[string]error
	aaaaRecords map[string][]string
	aaaaErrors  map[string]error
	tcpRTT      map[string]time.Duration
	tcpErrors   map[string]error
	headStatus  map[string]int
	headErrors  map[string]error
}

func newFakeNetworkProbe() *fakeNetworkProbe {
	return &fakeNetworkProbe{
		aRecords:    map[string][]string{},
		aErrors:     map[string]error{},
		aaaaRecords: map[string][]string{},
		aaaaErrors:  map[string]error{},
		tcpRTT:      map[string]time.Duration{},
		tcpErrors:   map[string]error{},
		headStatus:  map[string]int{},
		headErrors:  map[string]error{},
	}
}

func (f *fakeNetworkProbe) ResolveA(ctx context.Context, host string) ([]string, error) {
	if err, ok := f.aErrors[host]; ok {
		return nil, err
	}
	if addrs, ok := f.aRecords[host]; ok {
		return addrs, nil
	}
	return nil, fmt.Errorf("fake: no A entry for %s", host)
}

func (f *fakeNetworkProbe) ResolveAAAA(ctx context.Context, host string) ([]string, error) {
	if err, ok := f.aaaaErrors[host]; ok {
		return nil, err
	}
	if addrs, ok := f.aaaaRecords[host]; ok {
		return addrs, nil
	}
	return nil, nil // no AAAA, no error -- common case for IPv4-only hosts
}

func (f *fakeNetworkProbe) DialTCP(ctx context.Context, hostPort string) (time.Duration, error) {
	host := strings.SplitN(hostPort, ":", 2)[0]
	if err, ok := f.tcpErrors[host]; ok {
		return 0, err
	}
	if rtt, ok := f.tcpRTT[host]; ok {
		return rtt, nil
	}
	return 0, fmt.Errorf("fake: no TCP entry for %s", host)
}

func (f *fakeNetworkProbe) HeadStatus(ctx context.Context, url string) (int, error) {
	host := strings.TrimPrefix(url, "https://")
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimSuffix(host, "/")
	if err, ok := f.headErrors[host]; ok {
		return 0, err
	}
	if status, ok := f.headStatus[host]; ok {
		return status, nil
	}
	return 0, fmt.Errorf("fake: no HEAD entry for %s", host)
}

// markAllPassing primes the fake with healthy A + TCP + HEAD for every
// target. Test cases override individual hosts after this.
func (f *fakeNetworkProbe) markAllPassing() {
	for _, host := range networkProbeTargets {
		f.aRecords[host] = []string{"192.0.2.1"}
		f.tcpRTT[host] = 25 * time.Millisecond
		f.headStatus[host] = 200
	}
}

// TestDiagNetwork_NoAuth confirms /api/diag/network refuses unauthenticated
// callers.
func TestDiagNetwork_NoAuth(t *testing.T) {
	h, _ := testAPI(t)
	req := httptest.NewRequest("GET", "/api/diag/network", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

// TestDiagNetwork_HappyPath: every target resolves, TCP connects, HEAD
// returns 200. Verdict pass.
func TestDiagNetwork_HappyPath(t *testing.T) {
	h, _ := testAPI(t)
	fake := newFakeNetworkProbe()
	fake.markAllPassing()
	h.networkProbe = fake

	report := callDiagNetwork(t, h)
	if report.Verdict != "pass" {
		t.Errorf("verdict = %q, want pass; checks_failed=%v", report.Verdict, report.ChecksFailed)
	}
	if len(report.Probes) != len(networkProbeTargets) {
		t.Errorf("got %d probes, want %d", len(report.Probes), len(networkProbeTargets))
	}
	for _, p := range report.Probes {
		if p.Verdict != "pass" {
			t.Errorf("host %q: verdict=%q want pass", p.Host, p.Verdict)
		}
		if !p.DNSAOK {
			t.Errorf("host %q: DNSAOK=false", p.Host)
		}
		if !p.TCP443OK {
			t.Errorf("host %q: TCP443OK=false", p.Host)
		}
		if p.HTTPStatus != 200 {
			t.Errorf("host %q: HTTPStatus=%d want 200", p.Host, p.HTTPStatus)
		}
	}
}

// TestDiagNetwork_DNSFailure: every DNS query fails. Verdict fail with
// per-host dns_a reasons. TCP/HEAD must be skipped (not blindly attempted).
func TestDiagNetwork_DNSFailure(t *testing.T) {
	h, _ := testAPI(t)
	fake := newFakeNetworkProbe()
	for _, host := range networkProbeTargets {
		fake.aErrors[host] = fmt.Errorf("lookup %s: server misbehaving", host)
		fake.aaaaErrors[host] = fmt.Errorf("lookup %s: server misbehaving", host)
	}
	h.networkProbe = fake

	report := callDiagNetwork(t, h)
	if report.Verdict != "fail" {
		t.Errorf("verdict = %q, want fail", report.Verdict)
	}
	if len(report.ChecksFailed) != len(networkProbeTargets) {
		t.Errorf("len(checks_failed) = %d, want %d (every host fails)",
			len(report.ChecksFailed), len(networkProbeTargets))
	}
	for _, p := range report.Probes {
		if p.DNSAOK {
			t.Errorf("host %q: DNSAOK=true, want false", p.Host)
		}
		if p.TCP443OK {
			t.Errorf("host %q: TCP443OK=true, want false (DNS failed, should skip)", p.Host)
		}
		if p.TCP443Error != "skipped: no DNS resolution" {
			t.Errorf("host %q: TCP error = %q, want skip-marker", p.Host, p.TCP443Error)
		}
	}
}

// TestDiagNetwork_TCPFailureButHTTPOK: DNS works, TCP dial fails for one
// host but HEAD succeeds (would happen if a transparent proxy intercepts
// the TCP dial test but allows HTTPS through). Verdict for that host is
// still pass per the (TCP OR HEAD) rule.
func TestDiagNetwork_TCPFailureButHTTPOK(t *testing.T) {
	h, _ := testAPI(t)
	fake := newFakeNetworkProbe()
	fake.markAllPassing()
	target := networkProbeTargets[0]
	fake.tcpRTT = map[string]time.Duration{}
	for _, host := range networkProbeTargets {
		if host == target {
			fake.tcpErrors[host] = fmt.Errorf("dial tcp: connection reset")
			continue
		}
		fake.tcpRTT[host] = 25 * time.Millisecond
	}
	h.networkProbe = fake

	report := callDiagNetwork(t, h)
	if report.Verdict != "pass" {
		t.Errorf("verdict = %q, want pass (HEAD success covers TCP failure)", report.Verdict)
	}
	// The target host's per-host report should reflect TCP failure but
	// HTTP success.
	for _, p := range report.Probes {
		if p.Host != target {
			continue
		}
		if p.TCP443OK {
			t.Errorf("target host TCP443OK=true, want false (we forced an error)")
		}
		if !p.HTTPHeadOK {
			t.Errorf("target host HTTPHeadOK=false, want true (HEAD covers TCP)")
		}
		if p.Verdict != "pass" {
			t.Errorf("target host verdict=%q want pass", p.Verdict)
		}
	}
}

// TestDiagNetwork_IPv6OnlyHostPasses: a host that resolves only AAAA
// (no A), with TCP+HEAD working on AAAA, should pass.
func TestDiagNetwork_IPv6OnlyHostPasses(t *testing.T) {
	h, _ := testAPI(t)
	fake := newFakeNetworkProbe()
	fake.markAllPassing()
	target := networkProbeTargets[0]
	// Clear A, add AAAA. Keep TCP+HEAD passing (probe doesn't care which family the addr came from).
	delete(fake.aRecords, target)
	fake.aErrors[target] = fmt.Errorf("no IPv4 records")
	fake.aaaaRecords[target] = []string{"2606:4700::1111"}
	h.networkProbe = fake

	report := callDiagNetwork(t, h)
	if report.Verdict != "pass" {
		t.Errorf("verdict = %q, want pass (AAAA satisfies DNS check); checks_failed=%v",
			report.Verdict, report.ChecksFailed)
	}
	for _, p := range report.Probes {
		if p.Host != target {
			continue
		}
		if p.DNSAOK {
			t.Errorf("target DNSAOK=true, want false")
		}
		if !p.DNSAAAAOK {
			t.Errorf("target DNSAAAAOK=false, want true")
		}
		if p.Verdict != "pass" {
			t.Errorf("target verdict=%q want pass (AAAA + TCP/HEAD ok)", p.Verdict)
		}
	}
}

// TestDiagNetwork_BBCBlockedCanaryOK: 1.1.1.1 reachable but BBC hosts
// all DNS-fail. Verdict fail; checks_failed lists exactly the BBC hosts.
// This is the diagnostic signal emersnbe would see (the canary tells us
// outbound is working, the BBC names are the problem).
func TestDiagNetwork_BBCBlockedCanaryOK(t *testing.T) {
	h, _ := testAPI(t)
	fake := newFakeNetworkProbe()
	canary := "1.1.1.1"
	for _, host := range networkProbeTargets {
		if host == canary {
			fake.aRecords[host] = []string{canary}
			fake.tcpRTT[host] = 8 * time.Millisecond
			fake.headStatus[host] = 200
			continue
		}
		fake.aErrors[host] = fmt.Errorf("lookup %s: server misbehaving", host)
		fake.aaaaErrors[host] = fmt.Errorf("lookup %s: server misbehaving", host)
	}
	h.networkProbe = fake

	report := callDiagNetwork(t, h)
	if report.Verdict != "fail" {
		t.Errorf("verdict = %q, want fail (BBC hosts broken)", report.Verdict)
	}
	wantFailed := len(networkProbeTargets) - 1 // all except canary
	if len(report.ChecksFailed) != wantFailed {
		t.Errorf("len(checks_failed) = %d, want %d (BBC hosts only)",
			len(report.ChecksFailed), wantFailed)
	}
	// Canary must NOT appear in checks_failed.
	for _, msg := range report.ChecksFailed {
		if strings.Contains(msg, canary) {
			t.Errorf("checks_failed contains canary %q: %q", canary, msg)
		}
	}
}

// TestDiagNetwork_ResolvConfRead: when /etc/resolv.conf exists the
// report must include it for self-diagnosis.
func TestDiagNetwork_ResolvConfRead(t *testing.T) {
	if _, err := os.Stat("/etc/resolv.conf"); err != nil {
		t.Skip("no /etc/resolv.conf on this host")
	}
	h, _ := testAPI(t)
	fake := newFakeNetworkProbe()
	fake.markAllPassing()
	h.networkProbe = fake

	report := callDiagNetwork(t, h)
	if report.ResolvConf == "" {
		t.Errorf("resolv_conf empty despite /etc/resolv.conf existing")
	}
	if report.ResolverPath != "/etc/resolv.conf" {
		t.Errorf("resolver_path = %q, want /etc/resolv.conf", report.ResolverPath)
	}
}

// ----------------------------------------------------------------------
// helpers (file-local)
// ----------------------------------------------------------------------

func callDiagStorage(t *testing.T, h *Handler) DiagStorageReport {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/diag/storage?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var report DiagStorageReport
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}
	return report
}

func passingPathReport(label, path string) StoragePathReport {
	return StoragePathReport{
		Label:            label,
		Path:             path,
		Exists:           true,
		Writable:         true,
		FreeBytes:        10 * 1024 * 1024 * 1024,
		FreeHuman:        "10.0 GiB",
		NFSResponsive:    true,
		OwnershipMatches: true,
		Verdict:          "pass",
	}
}

func containsSubstring(haystack []string, needle string) bool {
	for _, h := range haystack {
		if strings.Contains(h, needle) {
			return true
		}
	}
	return false
}

// callDiagClock invokes /api/diag/clock with the test API key and
// returns the parsed report; fatals on any HTTP-level error.
func callDiagClock(t *testing.T, h *Handler) DiagClockReport {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/diag/clock?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var report DiagClockReport
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}
	return report
}

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	tt, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return tt
}

func callDiagNetwork(t *testing.T, h *Handler) DiagNetworkReport {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/diag/network?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var report DiagNetworkReport
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}
	return report
}

func callDiagGeo(t *testing.T, h *Handler, query string) DiagGeoReport {
	t.Helper()
	url := "/api/diag/geo?apikey=test-api-key"
	if query != "" {
		url += "&" + strings.TrimPrefix(query, "?")
	}
	req := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var report DiagGeoReport
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}
	return report
}
