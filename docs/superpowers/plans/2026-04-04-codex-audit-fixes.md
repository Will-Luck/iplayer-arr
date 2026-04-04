# Codex Audit Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix 5 verified defects from Codex static analysis -- a cancel/delete race, an API key leak, a log query-string leak, a frontend Clear All bug, and a 200-episode pagination cap.

**Architecture:** All fixes are independent and can be implemented in any order. The cancel race requires coordinating the download Manager and its worker loop via a "cancelled" set. The API key redaction and log sanitisation are one-liner changes. The frontend fix adds a server-side bulk-delete endpoint. The IBL pagination is a simple loop.

**Tech Stack:** Go 1.24, BoltDB, Solid.js (TypeScript), BBC iBL REST API

---

### Task 1: Fix cancel/delete race in download Manager

The race: `CancelDownload` cancels the worker context then immediately deletes the download from the store. The worker sees `ctx.Err() != nil` and writes the download back as pending via `setStatus`, re-creating the deleted record. Fix: track cancelled IDs so the worker skips the "return to pending" write for downloads that were explicitly cancelled.

**Files:**
- Modify: `internal/download/manager.go` (CancelDownload, struct fields)
- Modify: `internal/download/worker.go` (processDownload, context-cancelled branch)
- Modify: `internal/download/manager_test.go` (new test)

- [ ] **Step 1: Write the failing test**

Add to `internal/download/manager_test.go`:

```go
func TestCancelDownloadNoRezombie(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	m := NewManager(st, filepath.Join(dir, "downloads"), 2, nil, nil, nil, nil)

	// Enqueue a download so it exists in the store.
	id, err := m.Enqueue("p_cancel_test", "720p", "Cancel.Test.S01E01", "sonarr")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Cancel it (simulates SABnzbd delete request).
	m.CancelDownload(id)

	// The download should be gone from the store.
	dl, _ := st.GetDownload(id)
	if dl != nil {
		t.Fatalf("download %s should be deleted, but still exists with status %q", id, dl.Status)
	}

	// Simulate what the worker does when it sees ctx.Err() != nil:
	// it should NOT write back to pending if the download was cancelled.
	if m.IsCancelled(id) != true {
		t.Error("expected IsCancelled to return true for a cancelled download")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/lns/iplayer-arr && go test ./internal/download/ -run TestCancelDownloadNoRezombie -v`
Expected: FAIL with `m.IsCancelled undefined`

- [ ] **Step 3: Add cancelled-ID tracking to Manager**

In `internal/download/manager.go`, add a `cancelled` set to the struct and expose `IsCancelled` + `MarkCancelled`:

```go
// In the Manager struct, add after the claimed field:
	cancelled   map[string]struct{}
	cancelledMu sync.Mutex
```

In `NewManager`, initialise it alongside `claimed`:

```go
	claimed:     make(map[string]context.CancelFunc),
	cancelled:   make(map[string]struct{}),
```

Add two methods after `CancelDownload`:

```go
// MarkCancelled records that a download was explicitly cancelled by the user
// (as opposed to a context cancellation from shutdown). Workers check this
// before writing a cancelled download back to pending.
func (m *Manager) MarkCancelled(id string) {
	m.cancelledMu.Lock()
	m.cancelled[id] = struct{}{}
	m.cancelledMu.Unlock()
}

// IsCancelled returns true if the download was explicitly cancelled.
func (m *Manager) IsCancelled(id string) bool {
	m.cancelledMu.Lock()
	defer m.cancelledMu.Unlock()
	_, ok := m.cancelled[id]
	return ok
}

// clearCancelled removes a download from the cancelled set (called after
// the worker has acknowledged the cancellation).
func (m *Manager) clearCancelled(id string) {
	m.cancelledMu.Lock()
	delete(m.cancelled, id)
	m.cancelledMu.Unlock()
}
```

Update `CancelDownload` to mark before deleting:

```go
func (m *Manager) CancelDownload(nzoID string) error {
	m.MarkCancelled(nzoID)
	m.claimMu.Lock()
	if cancel, ok := m.claimed[nzoID]; ok {
		cancel()
	}
	m.claimMu.Unlock()
	m.store.DeleteDownload(nzoID)
	return nil
}
```

- [ ] **Step 4: Guard the worker's "return to pending" path**

In `internal/download/worker.go`, in `processDownload`, replace the context-cancelled block (lines 155-161):

```go
	ffErr := RunFFmpeg(ctx, job)
	if ffErr != nil {
		if ctx.Err() != nil {
			if m.IsCancelled(dl.ID) {
				m.clearCancelled(dl.ID)
				log.Printf("download %s cancelled by user, not returning to pending", dl.ID)
				return
			}
			m.setStatus(dl, store.StatusPending, "")
			log.Printf("download %s returned to pending (context cancelled)", dl.ID)
			return
		}
		m.failDownload(dl, store.FailCodeFFmpeg, ffErr)
		return
	}
```

- [ ] **Step 5: Run tests**

Run: `cd /home/lns/iplayer-arr && go test ./internal/download/ -v -count=1`
Expected: All PASS including TestCancelDownloadNoRezombie

- [ ] **Step 6: Commit**

```bash
cd /home/lns/iplayer-arr
git add internal/download/manager.go internal/download/worker.go internal/download/manager_test.go
git commit -m "fix: prevent cancelled downloads from reappearing as pending

CancelDownload now marks the ID in a cancelled set before deleting.
The worker checks this set and skips the return-to-pending write
when ffmpeg exits due to user cancellation vs shutdown."
```

---

### Task 2: Redact API key from GET /api/config

`handleGetConfig` returns all config keys including `api_key`. Redact it from the response.

**Files:**
- Modify: `internal/api/config.go:18-27` (handleGetConfig)
- Modify: `internal/api/handler_test.go` (update TestConfigGet)

- [ ] **Step 1: Write the failing test**

Add to `internal/api/handler_test.go` (or update the existing `TestConfigGet`):

```go
func TestConfigGetRedactsAPIKey(t *testing.T) {
	h, _ := testAPI(t)
	req := httptest.NewRequest("GET", "/api/config", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var cfg map[string]string
	json.NewDecoder(w.Body).Decode(&cfg)

	if val, ok := cfg["api_key"]; ok && val != "" {
		t.Errorf("api_key should be empty or absent in response, got %q", val)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/lns/iplayer-arr && go test ./internal/api/ -run TestConfigGetRedactsAPIKey -v`
Expected: FAIL -- api_key will contain the stored key value

- [ ] **Step 3: Redact in handleGetConfig**

In `internal/api/config.go`, in `handleGetConfig`, add after the config-building loop (before `writeJSON`):

```go
	// Never expose the API key over the wire.
	delete(cfg, "api_key")
```

- [ ] **Step 4: Run tests**

Run: `cd /home/lns/iplayer-arr && go test ./internal/api/ -run TestConfigGet -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
cd /home/lns/iplayer-arr
git add internal/api/config.go internal/api/handler_test.go
git commit -m "fix: redact api_key from GET /api/config response"
```

---

### Task 3: Sanitise SABnzbd query-string logging

`sabnzbd/handler.go:34` logs `r.URL.RawQuery` which includes `apikey=...`. These log lines enter the in-memory ring buffer and are served anonymously via `/api/logs`. Redact the apikey param before logging.

**Files:**
- Modify: `internal/sabnzbd/handler.go:34` (ServeHTTP log line)
- Modify: `internal/sabnzbd/handler_test.go` (new test)

- [ ] **Step 1: Write the failing test**

Add to `internal/sabnzbd/handler_test.go`:

```go
func TestSABnzbdLogSanitisesAPIKey(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "test.db"))
	defer st.Close()
	st.SetConfig("api_key", "secret-key-12345")

	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(os.Stderr)

	h := NewHandler(st, nil)
	req := httptest.NewRequest("GET", "/sabnzbd/api?mode=version&apikey=secret-key-12345", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	logOutput := logBuf.String()
	if strings.Contains(logOutput, "secret-key-12345") {
		t.Errorf("log output contains raw API key:\n%s", logOutput)
	}
	if !strings.Contains(logOutput, "apikey=***") {
		t.Errorf("log output should contain redacted apikey=***:\n%s", logOutput)
	}
}
```

You will need to add `"bytes"`, `"os"`, and `"strings"` to the test file imports, plus `"net/http/httptest"` if not already present.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/lns/iplayer-arr && go test ./internal/sabnzbd/ -run TestSABnzbdLogSanitisesAPIKey -v`
Expected: FAIL -- log contains the raw key

- [ ] **Step 3: Add query sanitiser and use it in the log line**

In `internal/sabnzbd/handler.go`, add a helper function before `ServeHTTP`:

```go
// sanitiseQuery replaces the apikey query parameter value with "***".
func sanitiseQuery(raw string) string {
	if !strings.Contains(raw, "apikey=") {
		return raw
	}
	params, err := url.ParseQuery(raw)
	if err != nil {
		return raw
	}
	if params.Has("apikey") {
		params.Set("apikey", "***")
	}
	return params.Encode()
}
```

Add `"net/url"` to the imports if not already present.

Replace line 34 in `ServeHTTP`:

```go
	log.Printf("[sabnzbd] %s %s mode=%s params=%s", r.Method, r.URL.Path, mode, sanitiseQuery(r.URL.RawQuery))
```

- [ ] **Step 4: Run tests**

Run: `cd /home/lns/iplayer-arr && go test ./internal/sabnzbd/ -v -count=1`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
cd /home/lns/iplayer-arr
git add internal/sabnzbd/handler.go internal/sabnzbd/handler_test.go
git commit -m "fix: redact apikey from SABnzbd handler log output"
```

---

### Task 4: Fix "Clear All" to delete all history, not just current page

The frontend's `clearAllHistory()` loops over `historyItems()` which is the current paginated slice. Fix: add a server-side `DELETE /api/history` endpoint that wipes the entire history bucket, then call it from the frontend.

**Files:**
- Modify: `internal/store/history.go` (add ClearHistory)
- Modify: `internal/store/history_test.go` (test ClearHistory)
- Modify: `internal/api/handler.go` (route DELETE /api/history)
- Modify: `internal/api/downloads.go` (add handleClearHistory)
- Modify: `internal/api/handler_test.go` (test endpoint)
- Modify: `frontend/src/api.ts` (add clearAllHistory)
- Modify: `frontend/src/pages/Dashboard.tsx` (use new endpoint)

- [ ] **Step 1: Write the store-level test**

Add to `internal/store/history_test.go`:

```go
func TestClearHistory(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Insert 3 history entries.
	for i := 0; i < 3; i++ {
		dl := &Download{
			ID:     fmt.Sprintf("test_%d", i),
			PID:    fmt.Sprintf("p%d", i),
			Status: StatusCompleted,
			Title:  fmt.Sprintf("Test %d", i),
		}
		if err := st.PutHistory(dl); err != nil {
			t.Fatalf("PutHistory: %v", err)
		}
	}

	// Verify they exist.
	all, _ := st.ListHistory()
	if len(all) != 3 {
		t.Fatalf("expected 3 history entries, got %d", len(all))
	}

	// Clear all.
	n, err := st.ClearHistory()
	if err != nil {
		t.Fatalf("ClearHistory: %v", err)
	}
	if n != 3 {
		t.Errorf("ClearHistory returned %d, want 3", n)
	}

	// Verify empty.
	all, _ = st.ListHistory()
	if len(all) != 0 {
		t.Errorf("expected 0 history entries after clear, got %d", len(all))
	}
}
```

You may need to add `"fmt"` to the test imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/lns/iplayer-arr && go test ./internal/store/ -run TestClearHistory -v`
Expected: FAIL -- `st.ClearHistory undefined`

- [ ] **Step 3: Implement ClearHistory in store**

Add to `internal/store/history.go`:

```go
// ClearHistory deletes all entries from the history bucket.
// Returns the number of entries deleted.
func (s *Store) ClearHistory() (int, error) {
	var count int
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketHistory)
		c := b.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			if err := b.Delete(k); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return count, err
}
```

- [ ] **Step 4: Run store test**

Run: `cd /home/lns/iplayer-arr && go test ./internal/store/ -run TestClearHistory -v`
Expected: PASS

- [ ] **Step 5: Add API endpoint handler**

Add to `internal/api/downloads.go`, after `handleDeleteHistory`:

```go
func (h *Handler) handleClearHistory(w http.ResponseWriter, r *http.Request) {
	n, err := h.store.ClearHistory()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"deleted": n})
}
```

- [ ] **Step 6: Wire the route in handler.go**

In `internal/api/handler.go`, in the `ServeHTTP` switch, find the existing history-delete case:

```go
	case strings.HasPrefix(path, "/api/history/") && r.Method == "DELETE":
		h.handleDeleteHistory(w, r)
```

Add a new case **before** it (order matters -- `/api/history` must match before `/api/history/`):

```go
	case path == "/api/history" && r.Method == "DELETE":
		h.handleClearHistory(w, r)
```

- [ ] **Step 7: Write API test**

Add to `internal/api/handler_test.go`:

```go
func TestClearAllHistory(t *testing.T) {
	h, st := testAPI(t)

	// Insert history entries.
	for i := 0; i < 5; i++ {
		st.PutHistory(&store.Download{
			ID:     fmt.Sprintf("h_%d", i),
			PID:    fmt.Sprintf("p%d", i),
			Status: store.StatusCompleted,
			Title:  fmt.Sprintf("Test %d", i),
		})
	}

	req := httptest.NewRequest("DELETE", "/api/history", nil)
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

	// Verify empty.
	all, _ := st.ListHistory()
	if len(all) != 0 {
		t.Errorf("history should be empty, got %d", len(all))
	}
}
```

Add `"fmt"` to test imports if not already present.

- [ ] **Step 8: Run API tests**

Run: `cd /home/lns/iplayer-arr && go test ./internal/api/ -v -count=1`
Expected: All PASS

- [ ] **Step 9: Update frontend API client**

In `frontend/src/api.ts`, add after the `deleteHistory` line:

```typescript
  clearAllHistory: () => del("/api/history"),
```

- [ ] **Step 10: Update Dashboard clearAllHistory**

In `frontend/src/pages/Dashboard.tsx`, replace the `clearAllHistory` function (lines 186-197):

```typescript
  async function clearAllHistory() {
    if (!confirm("Delete all history entries? This cannot be undone.")) return;
    try {
      await api.clearAllHistory();
    } catch {
      // fall back to per-item delete if bulk endpoint unavailable
      const items = historyItems();
      for (const dl of items) {
        try { await api.deleteHistory(dl.id); } catch { /* continue */ }
      }
    }
    refreshHistory();
  }
```

- [ ] **Step 11: Build frontend**

Run: `cd /home/lns/iplayer-arr/frontend && npm run build`
Expected: Build succeeds, output in `internal/web/dist/`

- [ ] **Step 12: Commit**

```bash
cd /home/lns/iplayer-arr
git add internal/store/history.go internal/store/history_test.go \
  internal/api/downloads.go internal/api/handler.go internal/api/handler_test.go \
  frontend/src/api.ts frontend/src/pages/Dashboard.tsx internal/web/dist/
git commit -m "fix: Clear All now deletes entire history, not just current page

Adds DELETE /api/history endpoint backed by store.ClearHistory().
Frontend calls bulk endpoint with per-item fallback."
```

---

### Task 5: Paginate IBL ListEpisodes beyond 200

`ListEpisodes` hard-codes `per_page=200&page=1` and never follows pagination. BBC brands with >200 episodes silently truncate.

**Files:**
- Modify: `internal/bbc/ibl.go:127-205` (ListEpisodes)
- Modify: `internal/bbc/ibl_test.go` (pagination test)

- [ ] **Step 1: Write the failing test**

Add to `internal/bbc/ibl_test.go`:

```go
func TestListEpisodesPagination(t *testing.T) {
	page1 := `{
		"programme_episodes": {
			"elements": [
				{"id": "ep1", "type": "episode", "title": "Show", "subtitle": "1. First"}
			],
			"page": 1,
			"per_page": 1,
			"count": 2
		}
	}`
	page2 := `{
		"programme_episodes": {
			"elements": [
				{"id": "ep2", "type": "episode", "title": "Show", "subtitle": "2. Second"}
			],
			"page": 2,
			"per_page": 1,
			"count": 2
		}
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pg := r.URL.Query().Get("page")
		w.Header().Set("Content-Type", "application/json")
		if pg == "2" {
			w.Write([]byte(page2))
		} else {
			w.Write([]byte(page1))
		}
	}))
	defer srv.Close()

	ibl := NewIBL(NewClient())
	ibl.BaseURL = srv.URL

	results, err := ibl.ListEpisodes("brand_pid")
	if err != nil {
		t.Fatalf("ListEpisodes: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2 (pagination should fetch both pages)", len(results))
	}
	if results[0].PID != "ep1" || results[1].PID != "ep2" {
		t.Errorf("unexpected PIDs: %s, %s", results[0].PID, results[1].PID)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/lns/iplayer-arr && go test ./internal/bbc/ -run TestListEpisodesPagination -v`
Expected: FAIL -- `got 1 results, want 2`

- [ ] **Step 3: Add pagination loop to ListEpisodes**

In `internal/bbc/ibl.go`, replace the `ListEpisodes` function. The key change: parse the response's `page`, `per_page`, and `count` fields, and loop until all pages are fetched. Safety cap at 20 pages (4000 episodes).

```go
func (ibl *IBL) ListEpisodes(pid string) ([]IBLResult, error) {
	var allResults []IBLResult
	const perPage = 200
	const maxPages = 20

	for page := 1; page <= maxPages; page++ {
		epURL := fmt.Sprintf("%s/programmes/%s/episodes?per_page=%d&page=%d",
			ibl.BaseURL, pid, perPage, page)

		body, err := ibl.client.Get(epURL)
		if err != nil {
			return nil, fmt.Errorf("iBL episodes page %d: %w", page, err)
		}

		var resp struct {
			ProgrammeEpisodes struct {
				Elements []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Title    string `json:"title"`
					Subtitle string `json:"subtitle"`
					Synopses struct {
						Small string `json:"small"`
					} `json:"synopses"`
					Images struct {
						Standard string `json:"standard"`
					} `json:"images"`
					MasterBrand struct {
						Titles struct {
							Small string `json:"small"`
						} `json:"titles"`
					} `json:"master_brand"`
					ReleaseDate    string `json:"release_date"`
					ParentPosition int    `json:"parent_position"`
					TleoID         string `json:"tleo_id"`
					Versions       []struct {
						Duration struct {
							Value string `json:"value"`
						} `json:"duration"`
					} `json:"versions"`
				} `json:"elements"`
				Page    int `json:"page"`
				PerPage int `json:"per_page"`
				Count   int `json:"count"`
			} `json:"programme_episodes"`
		}

		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("parse iBL episodes page %d: %w", page, err)
		}

		for _, e := range resp.ProgrammeEpisodes.Elements {
			thumb := ""
			if e.Images.Standard != "" {
				thumb = strings.Replace(e.Images.Standard, "{recipe}", "960x540", 1)
			}

			result := IBLResult{
				PID:       e.ID,
				Title:     e.Title,
				Subtitle:  e.Subtitle,
				Synopsis:  e.Synopses.Small,
				Channel:   e.MasterBrand.Titles.Small,
				Position:  e.ParentPosition,
				AirDate:   e.ReleaseDate,
				BrandPID:  e.TleoID,
				Thumbnail: thumb,
			}
			result.Series, result.EpisodeNum = parseSubtitleNumbers(e.Subtitle)

			if len(e.Versions) > 0 {
				result.Duration = parseDuration(e.Versions[0].Duration.Value)
			}

			allResults = append(allResults, result)
		}

		// Stop if we have all episodes or this page was empty.
		total := resp.ProgrammeEpisodes.Count
		if total <= 0 || len(allResults) >= total || len(resp.ProgrammeEpisodes.Elements) == 0 {
			break
		}
	}

	return allResults, nil
}
```

Note: This replaces the existing `ListEpisodes` in its entirety. The struct parsing and field mapping are identical to the original -- only the pagination loop is new.

- [ ] **Step 4: Run tests**

Run: `cd /home/lns/iplayer-arr && go test ./internal/bbc/ -v -count=1`
Expected: All PASS including TestListEpisodesPagination

- [ ] **Step 5: Commit**

```bash
cd /home/lns/iplayer-arr
git add internal/bbc/ibl.go internal/bbc/ibl_test.go
git commit -m "fix: paginate ListEpisodes beyond 200-episode BBC limit

Follows page/count fields to fetch all pages, capped at 4000 episodes."
```

---

### Task 6: Final verification

- [ ] **Step 1: Run full test suite**

Run: `cd /home/lns/iplayer-arr && go test ./... -count=1`
Expected: All PASS

- [ ] **Step 2: Run race detector**

Run: `cd /home/lns/iplayer-arr && go test ./... -race -count=1`
Expected: No races detected

- [ ] **Step 3: Run vet**

Run: `cd /home/lns/iplayer-arr && go vet ./...`
Expected: Clean

- [ ] **Step 4: Build frontend**

Run: `cd /home/lns/iplayer-arr/frontend && npm run build`
Expected: Clean build

- [ ] **Step 5: Build binary**

Run: `cd /home/lns/iplayer-arr && go build ./cmd/iplayer-arr/`
Expected: Clean build
