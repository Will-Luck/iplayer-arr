package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Will-Luck/iplayer-arr/internal/bbc"
	"github.com/Will-Luck/iplayer-arr/internal/store"
)

// testAPI creates a temporary store with an API key set and returns a Handler wired up for testing.
func testAPI(t *testing.T) (*Handler, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.SetConfig("api_key", "test-api-key")
	t.Cleanup(func() { st.Close() })

	hub := NewHub()
	ibl := bbc.NewIBL(bbc.NewClient())
	status := &RuntimeStatus{FFmpegVersion: "ffmpeg version 6.0"}

	h := NewHandler(st, hub, nil, ibl, status)
	h.RingBuf = NewRingBuffer(100)
	return h, st
}

// authedRequest builds a request that gets past the two gates in front of
// every /api route: the API key check (X-Api-Key carrying the key testAPI
// seeds) and, for state-changing methods, the same-origin CSRF check.
//
// Tests that exercise a handler's own behaviour use this so they fail on
// the behaviour rather than on the gate. The gates have dedicated tests:
// TestAPIRoutePopulationIsAuthenticatedOrAllowlisted for authentication
// and TestCSRF_OriginCheck for the origin rule.
func authedRequest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("X-Api-Key", "test-api-key")
	if isMutatingMethod(method) {
		req.Header.Set("Origin", "http://"+req.Host)
	}
	return req
}

// TestStatusReportsRuntimeHealth: /api/status was unauthenticated until
// GHSA-3hfw-5v8p-p588. It reports disk capacity, VPN geo posture and the
// ffmpeg build, so it now authenticates like the rest of /api.
func TestStatusReportsRuntimeHealth(t *testing.T) {
	h, _ := testAPI(t)
	req := authedRequest("GET", "/api/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status code = %d", w.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["ffmpeg"] != "ffmpeg version 6.0" {
		t.Errorf("ffmpeg = %v", resp["ffmpeg"])
	}
}

func TestDownloadsListWithHeaderAuth(t *testing.T) {
	h, _ := testAPI(t)
	req := authedRequest("GET", "/api/downloads", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHistoryList(t *testing.T) {
	h, _ := testAPI(t)
	req := authedRequest("GET", "/api/history", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var page store.HistoryPage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if page.Total != 0 {
		t.Errorf("total = %d, want 0", page.Total)
	}
	if len(page.Items) != 0 {
		t.Errorf("items len = %d, want 0", len(page.Items))
	}
}

func TestHistoryStatsEmpty(t *testing.T) {
	h, _ := testAPI(t)
	req := authedRequest("GET", "/api/history/stats", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var stats struct {
		Completed  int   `json:"completed"`
		Failed     int   `json:"failed"`
		TotalBytes int64 `json:"total_bytes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if stats.Completed != 0 {
		t.Errorf("completed = %d, want 0", stats.Completed)
	}
	if stats.Failed != 0 {
		t.Errorf("failed = %d, want 0", stats.Failed)
	}
	if stats.TotalBytes != 0 {
		t.Errorf("total_bytes = %d, want 0", stats.TotalBytes)
	}
}

// TestDownloadsWithQueryAuth deliberately does NOT use authedRequest.
// The helper adds an X-Api-Key header, which would carry the request on
// its own and let this test keep passing even if query-parameter auth
// were removed entirely. The credential under test has to be the only
// one present.
func TestDownloadsWithQueryAuth(t *testing.T) {
	h, _ := testAPI(t)
	req := httptest.NewRequest("GET", "/api/downloads?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status code = %d, body: %s", w.Code, w.Body.String())
	}
	var resp []interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp) != 0 {
		t.Errorf("expected empty array, got %d items", len(resp))
	}
}

// TestDownloadsWithBearerAuth is the only test that drives an Authorization:
// Bearer credential all the way through the router and the middleware, and
// Bearer is what the SPA sends on every non-SSE call. Same reasoning as
// TestDownloadsWithQueryAuth: no authedRequest, so the Bearer header is the
// only credential on the request.
func TestDownloadsWithBearerAuth(t *testing.T) {
	h, _ := testAPI(t)
	req := httptest.NewRequest("GET", "/api/downloads", nil)
	req.Header.Set("Authorization", "Bearer test-api-key")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status code = %d", w.Code)
	}
}

func TestConfigGet(t *testing.T) {
	h, st := testAPI(t)
	st.SetConfig("quality", "1080p")

	req := authedRequest("GET", "/api/config?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status code = %d", w.Code)
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["quality"] != "1080p" {
		t.Errorf("quality = %q", resp["quality"])
	}
}

// TestConfigGetOmitsAPIKey is the disclosure half of
// GHSA-3hfw-5v8p-p588. This endpoint used to hand the key to any caller;
// it must not return it now even to an authenticated one. The operator
// reads the key from <CONFIG_DIR>/api_key instead.
func TestConfigGetOmitsAPIKey(t *testing.T) {
	h, _ := testAPI(t)
	req := authedRequest("GET", "/api/config", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var cfg map[string]string
	json.NewDecoder(w.Body).Decode(&cfg)

	if _, present := cfg["api_key"]; present {
		t.Errorf("api_key present in the config response: %q", cfg["api_key"])
	}
	if strings.Contains(w.Body.String(), "test-api-key") {
		t.Error("the seeded key appears somewhere in the config response body")
	}
	// Sanity: the endpoint still answers with the fields it should.
	if cfg["quality"] == "" {
		t.Error("quality missing; the response looks empty rather than redacted")
	}
}

// TestConfigGetUnauthenticatedIsRejected: the exact request from the
// advisory. Before the fix this returned 200 with the key in cleartext.
func TestConfigGetUnauthenticatedIsRejected(t *testing.T) {
	h, _ := testAPI(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/config", nil))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if strings.Contains(w.Body.String(), "test-api-key") {
		t.Error("the key leaked in the 401 body")
	}
}

func TestConfigPut(t *testing.T) {
	h, st := testAPI(t)

	body := `{"key":"quality","value":"480p"}`
	req := authedRequest("PUT", "/api/config?apikey=test-api-key", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status code = %d, body: %s", w.Code, w.Body.String())
	}

	val, _ := st.GetConfig("quality")
	if val != "480p" {
		t.Errorf("stored quality = %q", val)
	}
}

func TestConfigPutMaxWorkers(t *testing.T) {
	h, st := testAPI(t)

	body := `{"key":"max_workers","value":"15"}`
	req := authedRequest("PUT", "/api/config", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body: %s", w.Code, w.Body.String())
	}

	val, _ := st.GetConfig("max_workers")
	if val != "15" {
		t.Errorf("stored max_workers = %q", val)
	}
}

func TestConfigPutBlocksAPIKey(t *testing.T) {
	h, _ := testAPI(t)

	body := `{"key":"api_key","value":"hacked"}`
	req := authedRequest("PUT", "/api/config?apikey=test-api-key", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestOverridesList(t *testing.T) {
	h, _ := testAPI(t)
	req := authedRequest("GET", "/api/overrides?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status code = %d", w.Code)
	}

	// Must be [] not null
	if w.Body.String() != "[]\n" {
		t.Errorf("expected empty array, got %q", w.Body.String())
	}
}

func TestOverridesPutAndList(t *testing.T) {
	h, _ := testAPI(t)

	body := `{"show_name":"Doctor Who","force_date_based":true}`
	req := authedRequest("PUT", "/api/overrides/Doctor+Who?apikey=test-api-key", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("PUT status = %d, body: %s", w.Code, w.Body.String())
	}

	// Now list
	req = authedRequest("GET", "/api/overrides?apikey=test-api-key", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var overrides []store.ShowOverride
	if err := json.Unmarshal(w.Body.Bytes(), &overrides); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(overrides) != 1 {
		t.Fatalf("expected 1 override, got %d", len(overrides))
	}
	if !overrides[0].ForceDateBased {
		t.Error("expected force_date_based=true")
	}
}

func TestOverridesDelete(t *testing.T) {
	h, st := testAPI(t)
	st.PutOverride(&store.ShowOverride{ShowName: "Test Show"})

	req := authedRequest("DELETE", "/api/overrides/Test+Show?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("DELETE status = %d", w.Code)
	}

	overrides, _ := st.ListOverrides()
	if len(overrides) != 0 {
		t.Errorf("expected 0 overrides after delete, got %d", len(overrides))
	}
}

func TestHistoryDelete(t *testing.T) {
	h, st := testAPI(t)

	dl := &store.Download{ID: "hist_1", PID: "p1", Title: "Test", Status: store.StatusCompleted}
	st.PutDownload(dl)
	st.MoveToHistory("hist_1")

	req := authedRequest("DELETE", "/api/history/hist_1?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("DELETE status = %d", w.Code)
	}

	entry, _ := st.GetHistory("hist_1")
	if entry != nil {
		t.Error("history entry should be deleted")
	}
}

func TestManualDownloadNoStarter(t *testing.T) {
	h, _ := testAPI(t)

	body := `{"pid":"b039d07m","quality":"720p"}`
	req := authedRequest("POST", "/api/download?apikey=test-api-key", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// With nil manager, should return error
	if w.Code != 500 {
		t.Fatalf("expected 500, got %d, body: %s", w.Code, w.Body.String())
	}
}

// TestEventsEndpointAcceptsQueryAuth: EventSource cannot set request
// headers, so the SSE route has to keep accepting ?apikey=. If this
// regresses to header-only the dashboard stops receiving live updates
// with no error the operator can see.
func TestEventsEndpointAcceptsQueryAuth(t *testing.T) {
	h, _ := testAPI(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("GET", "/api/events?apikey=test-api-key", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("Content-Type = %q", ct)
	}
}

// TestEventsEndpointRejectsMissingKey: the stream is operator data, so
// the query-parameter carve-out above must not become a hole.
func TestEventsEndpointRejectsMissingKey(t *testing.T) {
	h, _ := testAPI(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("GET", "/api/events", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestUnknownRoute(t *testing.T) {
	h, _ := testAPI(t)
	req := authedRequest("GET", "/api/nonexistent?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// seedAPIHistory inserts completed history entries directly into the store
// using PutHistory so that CompletedAt is deterministic.
func seedAPIHistory(t *testing.T, st *store.Store, base time.Time, entries []struct {
	id     string
	title  string
	status string
	size   int64
	offset time.Duration
}) {
	t.Helper()
	for _, e := range entries {
		dl := &store.Download{
			ID:          e.id,
			PID:         "pid_" + e.id,
			Title:       e.title,
			Status:      e.status,
			Size:        e.size,
			CompletedAt: base.Add(e.offset),
		}
		if err := st.PutHistory(dl); err != nil {
			t.Fatalf("PutHistory %q: %v", e.id, err)
		}
	}
}

// TestHistoryListEnvelope verifies GET /api/history returns a HistoryPage JSON
// object with "items" array and "total" integer.
func TestHistoryListEnvelope(t *testing.T) {
	h, st := testAPI(t)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	seedAPIHistory(t, st, base, []struct {
		id     string
		title  string
		status string
		size   int64
		offset time.Duration
	}{
		{"env_1", "Show One", store.StatusCompleted, 100, 0},
		{"env_2", "Show Two", store.StatusCompleted, 200, time.Hour},
		{"env_3", "Show Three", store.StatusFailed, 0, 2 * time.Hour},
	})

	req := authedRequest("GET", "/api/history?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}

	var page store.HistoryPage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("unmarshal: %v (body: %s)", err, w.Body.String())
	}
	if page.Total != 3 {
		t.Errorf("Total = %d, want 3", page.Total)
	}
	if len(page.Items) != 3 {
		t.Errorf("Items len = %d, want 3", len(page.Items))
	}
}

// TestHistoryListStatusFilter verifies ?status=completed filters correctly.
func TestHistoryListStatusFilter(t *testing.T) {
	h, st := testAPI(t)
	base := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	seedAPIHistory(t, st, base, []struct {
		id     string
		title  string
		status string
		size   int64
		offset time.Duration
	}{
		{"sf_c1", "Completed A", store.StatusCompleted, 100, 0},
		{"sf_c2", "Completed B", store.StatusCompleted, 200, time.Hour},
		{"sf_f1", "Failed A", store.StatusFailed, 0, 2 * time.Hour},
	})

	req := authedRequest("GET", "/api/history?apikey=test-api-key&status=completed", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}

	var page store.HistoryPage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if page.Total != 2 {
		t.Errorf("Total = %d, want 2", page.Total)
	}
	for _, item := range page.Items {
		if item.Status != store.StatusCompleted {
			t.Errorf("unexpected status %q for item %s", item.Status, item.ID)
		}
	}
}

// TestHistoryListPagination verifies ?page=1&per_page=2 returns only 2 items
// and that page 2 returns the next batch.
func TestHistoryListPagination(t *testing.T) {
	h, st := testAPI(t)
	base := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	seedAPIHistory(t, st, base, []struct {
		id     string
		title  string
		status string
		size   int64
		offset time.Duration
	}{
		{"pg_1", "Item 1", store.StatusCompleted, 100, 0},
		{"pg_2", "Item 2", store.StatusCompleted, 100, time.Hour},
		{"pg_3", "Item 3", store.StatusCompleted, 100, 2 * time.Hour},
		{"pg_4", "Item 4", store.StatusCompleted, 100, 3 * time.Hour},
		{"pg_5", "Item 5", store.StatusCompleted, 100, 4 * time.Hour},
	})

	doGet := func(url string) store.HistoryPage {
		t.Helper()
		req := authedRequest("GET", url, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
		}
		var page store.HistoryPage
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return page
	}

	p1 := doGet("/api/history?apikey=test-api-key&page=1&per_page=2")
	if p1.Total != 5 {
		t.Errorf("page1 Total = %d, want 5", p1.Total)
	}
	if len(p1.Items) != 2 {
		t.Errorf("page1 Items len = %d, want 2", len(p1.Items))
	}

	p2 := doGet("/api/history?apikey=test-api-key&page=2&per_page=2")
	if len(p2.Items) != 2 {
		t.Errorf("page2 Items len = %d, want 2", len(p2.Items))
	}

	// No overlap between pages.
	page1IDs := make(map[string]bool)
	for _, item := range p1.Items {
		page1IDs[item.ID] = true
	}
	for _, item := range p2.Items {
		if page1IDs[item.ID] {
			t.Errorf("ID %q appears on both page 1 and page 2", item.ID)
		}
	}
}

// TestHistoryStats verifies GET /api/history/stats returns completed/failed/total_bytes counts.
func TestHistoryStats(t *testing.T) {
	h, st := testAPI(t)
	base := time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)
	seedAPIHistory(t, st, base, []struct {
		id     string
		title  string
		status string
		size   int64
		offset time.Duration
	}{
		{"hs_c1", "Completed One", store.StatusCompleted, 1000, 0},
		{"hs_c2", "Completed Two", store.StatusCompleted, 2000, time.Hour},
		{"hs_f1", "Failed One", store.StatusFailed, 0, 2 * time.Hour},
	})

	req := authedRequest("GET", "/api/history/stats?apikey=test-api-key", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}

	var stats struct {
		Completed  int   `json:"completed"`
		Failed     int   `json:"failed"`
		TotalBytes int64 `json:"total_bytes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("unmarshal: %v (body: %s)", err, w.Body.String())
	}
	if stats.Completed != 2 {
		t.Errorf("completed = %d, want 2", stats.Completed)
	}
	if stats.Failed != 1 {
		t.Errorf("failed = %d, want 1", stats.Failed)
	}
	if stats.TotalBytes != 3000 {
		t.Errorf("total_bytes = %d, want 3000", stats.TotalBytes)
	}
}

// TestHistoryStatsSinceFilter verifies ?since= filters by date for stats.
func TestHistoryStatsSinceFilter(t *testing.T) {
	h, st := testAPI(t)
	// Two entries: one old, one new.
	oldTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	newTime := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)

	oldDL := &store.Download{
		ID: "hss_old", PID: "p_old", Title: "Old", Status: store.StatusCompleted,
		Size: 500, CompletedAt: oldTime,
	}
	newDL := &store.Download{
		ID: "hss_new", PID: "p_new", Title: "New", Status: store.StatusCompleted,
		Size: 1500, CompletedAt: newTime,
	}
	st.PutHistory(oldDL)
	st.PutHistory(newDL)

	// Stats since 2024-03-01 should only count the new entry.
	req := authedRequest("GET", "/api/history/stats?apikey=test-api-key&since=2024-03-01", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}

	var stats struct {
		Completed  int   `json:"completed"`
		TotalBytes int64 `json:"total_bytes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if stats.Completed != 1 {
		t.Errorf("completed = %d, want 1", stats.Completed)
	}
	if stats.TotalBytes != 1500 {
		t.Errorf("total_bytes = %d, want 1500", stats.TotalBytes)
	}
}

func TestClearAllHistory(t *testing.T) {
	h, st := testAPI(t)

	for i := 0; i < 5; i++ {
		st.PutHistory(&store.Download{
			ID:     fmt.Sprintf("h_%d", i),
			PID:    fmt.Sprintf("p%d", i),
			Status: store.StatusCompleted,
			Title:  fmt.Sprintf("Test %d", i),
		})
	}

	req := authedRequest("DELETE", "/api/history", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["deleted"] != float64(5) {
		t.Errorf("deleted = %v, want 5", resp["deleted"])
	}

	all, _ := st.ListHistory()
	if len(all) != 0 {
		t.Errorf("history should be empty, got %d", len(all))
	}
}

// TestCSRF_OriginCheck verifies the cross-origin rule on state-changing
// methods. Two properties matter:
//
//   - A cross-origin Origin is refused outright.
//   - An absent Origin is no longer trusted on its own. It is accepted
//     only when the caller supplies its credential in a request header,
//     which a page on another origin cannot do without a preflight this
//     service never answers. curl, Sonarr and Radarr set the header, so
//     nothing legitimate is affected.
//
// Safe methods (GET) are never subject to the check; they still have to
// authenticate, which is what the credential column exercises.
func TestCSRF_OriginCheck(t *testing.T) {
	h, _ := testAPI(t)

	cases := []struct {
		name       string
		method     string
		origin     string
		host       string
		headerCred bool
		queryCred  bool
		wantStatus int
	}{
		{name: "GET no origin, header credential", method: "GET", host: "iplayer-arr.lan:62001", headerCred: true, wantStatus: http.StatusOK},
		{name: "GET cross-origin allowed (safe method)", method: "GET", origin: "https://attacker.com", host: "iplayer-arr.lan:62001", headerCred: true, wantStatus: http.StatusOK},
		{name: "DELETE no origin with header credential allowed", method: "DELETE", host: "iplayer-arr.lan:62001", headerCred: true, wantStatus: http.StatusOK},
		{name: "DELETE no origin with only a query credential refused", method: "DELETE", host: "iplayer-arr.lan:62001", queryCred: true, wantStatus: http.StatusForbidden},
		{name: "DELETE no origin and no credential refused", method: "DELETE", host: "iplayer-arr.lan:62001", wantStatus: http.StatusForbidden},
		{name: "DELETE same origin allowed", method: "DELETE", origin: "http://iplayer-arr.lan:62001", host: "iplayer-arr.lan:62001", headerCred: true, wantStatus: http.StatusOK},
		{name: "DELETE cross-origin refused", method: "DELETE", origin: "https://attacker.com", host: "iplayer-arr.lan:62001", headerCred: true, wantStatus: http.StatusForbidden},
		{name: "PUT cross-origin refused", method: "PUT", origin: "https://attacker.com", host: "iplayer-arr.lan:62001", headerCred: true, wantStatus: http.StatusForbidden},
		{name: "POST cross-origin refused", method: "POST", origin: "https://attacker.com", host: "iplayer-arr.lan:62001", headerCred: true, wantStatus: http.StatusForbidden},
		{name: "DELETE different port refused", method: "DELETE", origin: "http://iplayer-arr.lan:9999", host: "iplayer-arr.lan:62001", headerCred: true, wantStatus: http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// /api/history is a real DELETE-able route; we use it to exercise CSRF
			// without needing the route to do anything destructive in this test.
			var target string
			switch tc.method {
			case "GET":
				target = "/api/status"
			case "POST":
				target = "/api/pause"
			case "PUT":
				target = "/api/config"
			case "DELETE":
				target = "/api/history"
			}
			if tc.queryCred {
				target += "?apikey=test-api-key"
			}
			req := httptest.NewRequest(tc.method, target, strings.NewReader(`{}`))
			req.Host = tc.host
			if tc.headerCred {
				req.Header.Set("X-Api-Key", "test-api-key")
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d. body: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}
