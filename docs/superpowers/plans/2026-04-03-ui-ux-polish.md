# UI/UX Polish Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Polish iplayer-arr's UI for public release with a distinctive visual identity, collapsible sidebar, setup wizard, log viewer, system health page, and enhanced history.

**Architecture:** Incremental enhancement of the existing Solid.js SPA + Go backend. Each phase produces a working, committable state. Backend endpoints are added before the frontend pages that consume them. CSS theme is applied first as the foundation for all subsequent UI work.

**Tech Stack:** Go 1.24 (backend), Solid.js + TypeScript (frontend), Vite (build), BoltDB (store), SSE (real-time), CSS custom properties (theming)

**Spec:** `docs/superpowers/specs/2026-04-03-ui-ux-polish-design.md`

---

## File Map

### New Files

| File | Purpose |
|------|---------|
| `internal/api/system.go` | `GET /api/system`, `POST /api/system/geo-check` handlers |
| `internal/api/system_test.go` | Tests for system endpoints |
| `internal/api/logs.go` | `GET /api/logs` handler, ring buffer |
| `internal/api/logs_test.go` | Tests for log endpoint and ring buffer |
| `frontend/src/pages/Logs.tsx` | Log viewer page |
| `frontend/src/pages/System.tsx` | System health page |
| `frontend/src/components/SetupWizard.tsx` | First-run setup wizard modal |

### Modified Files

| File | Changes |
|------|---------|
| `frontend/src/styles.css` | New colour palette, sidebar collapse, mobile, new page styles |
| `frontend/src/components/Nav.tsx` | Collapsible sidebar, mobile hamburger |
| `frontend/src/App.tsx` | Add `/logs`, `/system` routes; add wizard mount |
| `frontend/src/pages/Dashboard.tsx` | Health strip, download speed, enhanced history |
| `frontend/src/pages/Config.tsx` | "Re-run Setup" button |
| `frontend/src/api.ts` | New API methods, history pagination types |
| `frontend/src/types.ts` | New types (SystemInfo, LogEntry, HistoryPage) |
| `frontend/src/sse.ts` | Add `log:line` event type |
| `internal/api/handler.go` | Route new endpoints, add `startedAt`/`downloadDir` fields to Handler |
| `internal/api/status.go` | Add `disk_free`, `disk_total`, `last_indexer_request` to response |
| `internal/api/downloads.go` | History pagination/filtering/sorting |
| `internal/api/handler_test.go` | Tests for history pagination |
| `internal/store/history.go` | `ListHistoryFiltered` method with pagination |
| `internal/store/history_test.go` | Tests for filtered history (create if missing) |
| `cmd/iplayer-arr/main.go` | Wire ring buffer, pass downloadDir, track startedAt |

---

## Phase 1: Visual Foundation (Theme + Inline Style Extraction)

### Task 1: Update CSS colour palette

**Files:**
- Modify: `frontend/src/styles.css:1-19`

- [ ] **Step 1: Replace `:root` variables**

```css
:root {
  --bg-base: #0f1117;
  --bg-surface: #161921;
  --bg-elevated: #1e2230;
  --border: #2a2e3d;
  --text-primary: #e8eaed;
  --text-secondary: #8b8fa3;
  --text-muted: #6b6f83;
  --accent: #c73e64;
  --accent-hover: #d4516f;
  --success: #22c55e;
  --warning: #f59e0b;
  --danger: #ef4444;
  --progress-bg: #1e2230;
  --progress-fill: #c73e64;
  --nav-width: 220px;
  --nav-collapsed: 56px;
  --radius: 8px;
}
```

- [ ] **Step 2: Update all CSS rules that reference old variable names**

The old variables that changed names:
- `--bg-primary` -> `--bg-base`
- `--bg-secondary` -> `--bg-surface`
- `--bg-card` -> `--bg-surface` (cards use surface)
- `--bg-input` -> `--bg-elevated`

Search `styles.css` for each old name and replace. Key rules:

```css
body {
  background: var(--bg-base);
  color: var(--text-primary);
  /* rest unchanged */
}

.nav {
  background: var(--bg-surface);
  border-right: 1px solid var(--border);
  /* rest unchanged */
}

.card {
  background: var(--bg-surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  /* add: */ box-shadow: none;
  transition: box-shadow 0.15s;
  margin-bottom: 16px;
}

.input {
  background: var(--bg-elevated);
  border: 1px solid var(--border);
  /* rest unchanged */
}
```

- [ ] **Step 3: Add interactive card hover**

```css
.card-interactive:hover {
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.3);
}
```

- [ ] **Step 4: Verify visually**

Run: `cd /home/lns/iplayer-arr/frontend && npm run build`
Expected: Build succeeds, no CSS errors.

- [ ] **Step 5: Commit**

```bash
cd /home/lns/iplayer-arr
git add frontend/src/styles.css
git commit -m "feat(ui): new colour palette with rose accent"
```

### Task 2: Extract remaining inline styles

**Files:**
- Modify: `frontend/src/pages/Dashboard.tsx`
- Modify: `frontend/src/pages/Config.tsx`
- Modify: `frontend/src/pages/Overrides.tsx`
- Modify: `frontend/src/pages/Search.tsx`
- Modify: `frontend/src/pages/Downloads.tsx`
- Modify: `frontend/src/styles.css`

- [ ] **Step 1: Audit all inline `style=` attributes**

Run: `grep -rn 'style=' frontend/src/pages/ frontend/src/components/`

For each inline style, create a named CSS class. Examples:

```css
.pause-btn {
  margin-left: auto;
  color: white;
}
.pause-btn--active {
  background: var(--warning);
}
.pause-btn--inactive {
  background: var(--text-muted);
}

.config-disabled {
  opacity: 0.5;
}

.config-hint {
  font-size: 11px;
  margin-top: 4px;
}

.override-input-name { min-width: 140px; }
.override-input-num { width: 64px; }
.override-input-custom { min-width: 120px; }

.dl-folder-name {
  max-width: 400px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dl-folder-exts {
  font-size: 11px;
  color: var(--text-muted);
}

.btn-delete {
  background: var(--danger);
  color: white;
}

.btn-cancel {
  background: var(--bg-elevated);
  color: var(--text-secondary);
}

.search-quality {
  width: auto;
}
```

- [ ] **Step 2: Replace all inline styles in TSX files with the new classes**

Replace each `style="..."` or `style={{...}}` with the corresponding `class="..."`.

- [ ] **Step 3: Verify no inline styles remain**

Run: `grep -c 'style=' frontend/src/pages/*.tsx frontend/src/components/*.tsx`
Expected: 0 matches in all files (except `style={{ width: ... }}` for the progress bar which uses dynamic values -- that one stays).

- [ ] **Step 4: Build and verify**

Run: `cd /home/lns/iplayer-arr/frontend && npm run build`

- [ ] **Step 5: Commit**

```bash
cd /home/lns/iplayer-arr
git add frontend/
git commit -m "refactor(ui): extract all inline styles to CSS classes"
```

---

## Phase 2: Collapsible Sidebar + Mobile

### Task 3: Collapsible sidebar

**Files:**
- Modify: `frontend/src/components/Nav.tsx`
- Modify: `frontend/src/styles.css`
- Modify: `frontend/src/App.tsx`

- [ ] **Step 1: Add sidebar CSS**

Add to `styles.css`:

```css
.sidebar-toggle {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 12px;
  margin-top: auto;
  cursor: pointer;
  color: var(--text-secondary);
  border: none;
  background: none;
  transition: color 0.15s;
}
.sidebar-toggle:hover {
  color: var(--text-primary);
}
.sidebar-toggle svg {
  width: 16px;
  height: 16px;
  transition: transform 0.2s;
}

.nav.collapsed {
  width: var(--nav-collapsed);
}
.nav.collapsed .nav-brand {
  font-size: 0;
  /* show monogram */
}
.nav.collapsed .nav-brand::after {
  content: "ia";
  font-size: 14px;
  font-weight: 700;
  color: var(--accent);
}
.nav.collapsed .nav-link {
  justify-content: center;
  padding: 10px 0;
  font-size: 0;
  gap: 0;
}
.nav.collapsed .nav-link svg {
  width: 20px;
  height: 20px;
}
.nav.collapsed .sidebar-toggle svg {
  transform: rotate(180deg);
}

.layout {
  display: flex;
  min-height: 100vh;
}

.main {
  flex: 1;
  margin-left: var(--nav-width);
  transition: margin-left 0.2s;
  padding: 24px;
  max-width: 1200px;
}
.nav.collapsed ~ .main,
body.sidebar-collapsed .main {
  margin-left: var(--nav-collapsed);
}
```

- [ ] **Step 2: Update Nav.tsx**

```tsx
import { A, useLocation } from "@solidjs/router";
import { createSignal, onMount } from "solid-js";

function Nav() {
  const location = useLocation();
  const [collapsed, setCollapsed] = createSignal(
    localStorage.getItem("sidebar-collapsed") === "true"
  );

  function toggle() {
    const next = !collapsed();
    setCollapsed(next);
    localStorage.setItem("sidebar-collapsed", String(next));
    document.body.classList.toggle("sidebar-collapsed", next);
  }

  onMount(() => {
    document.body.classList.toggle("sidebar-collapsed", collapsed());
  });

  const isActive = (path: string) => {
    if (path === "/") return location.pathname === "/";
    return location.pathname.startsWith(path);
  };

  return (
    <nav class="nav" classList={{ collapsed: collapsed() }} aria-label="Main navigation">
      <div class="nav-brand">iplayer-arr</div>
      <div class="nav-links">
        <A href="/" class="nav-link" classList={{ active: isActive("/") }} aria-current={isActive("/") ? "page" : undefined}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <rect x="3" y="3" width="7" height="7" rx="1" />
            <rect x="14" y="3" width="7" height="7" rx="1" />
            <rect x="3" y="14" width="7" height="7" rx="1" />
            <rect x="14" y="14" width="7" height="7" rx="1" />
          </svg>
          Dashboard
        </A>
        <A href="/downloads" class="nav-link" classList={{ active: isActive("/downloads") }} aria-current={isActive("/downloads") ? "page" : undefined}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
            <polyline points="7 10 12 15 17 10" />
            <line x1="12" y1="15" x2="12" y2="3" />
          </svg>
          Downloads
        </A>
        <A href="/search" class="nav-link" classList={{ active: isActive("/search") }} aria-current={isActive("/search") ? "page" : undefined}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <circle cx="11" cy="11" r="8" />
            <line x1="21" y1="21" x2="16.65" y2="16.65" />
          </svg>
          Search
        </A>
        <A href="/logs" class="nav-link" classList={{ active: isActive("/logs") }} aria-current={isActive("/logs") ? "page" : undefined}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="4 17 10 11 4 5" />
            <line x1="12" y1="19" x2="20" y2="19" />
          </svg>
          Logs
        </A>
        <A href="/config" class="nav-link" classList={{ active: isActive("/config") }} aria-current={isActive("/config") ? "page" : undefined}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <circle cx="12" cy="12" r="3" />
            <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
          </svg>
          Config
        </A>
        <A href="/overrides" class="nav-link" classList={{ active: isActive("/overrides") }} aria-current={isActive("/overrides") ? "page" : undefined}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M12 20h9" />
            <path d="M16.5 3.5a2.121 2.121 0 0 1 3 3L7 19l-4 1 1-4L16.5 3.5z" />
          </svg>
          Overrides
        </A>
        <A href="/system" class="nav-link" classList={{ active: isActive("/system") }} aria-current={isActive("/system") ? "page" : undefined}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <rect x="2" y="3" width="20" height="14" rx="2" ry="2" />
            <line x1="8" y1="21" x2="16" y2="21" />
            <line x1="12" y1="17" x2="12" y2="21" />
          </svg>
          System
        </A>
      </div>
      <button class="sidebar-toggle" onClick={toggle} aria-label={collapsed() ? "Expand sidebar" : "Collapse sidebar"}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="15 18 9 12 15 6" />
        </svg>
      </button>
    </nav>
  );
}

export default Nav;
```

- [ ] **Step 3: Build and verify**

Run: `cd /home/lns/iplayer-arr/frontend && npm run build`

- [ ] **Step 4: Commit**

```bash
cd /home/lns/iplayer-arr
git add frontend/
git commit -m "feat(ui): collapsible sidebar with localStorage persistence"
```

### Task 4: Mobile hamburger menu

**Files:**
- Modify: `frontend/src/components/Nav.tsx`
- Modify: `frontend/src/styles.css`

- [ ] **Step 1: Add mobile CSS**

```css
.mobile-topbar {
  display: none;
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  height: 48px;
  background: var(--bg-surface);
  border-bottom: 1px solid var(--border);
  z-index: 100;
  align-items: center;
  padding: 0 16px;
}
.mobile-topbar .nav-brand {
  margin: 0;
  padding: 0;
}

.hamburger {
  background: none;
  border: none;
  color: var(--text-primary);
  cursor: pointer;
  padding: 8px;
}
.hamburger svg { width: 20px; height: 20px; }

.nav-overlay {
  display: none;
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.5);
  z-index: 199;
}
.nav-overlay.visible { display: block; }

@media (max-width: 768px) {
  .mobile-topbar { display: flex; }
  .nav {
    position: fixed;
    z-index: 200;
    transform: translateX(-100%);
    transition: transform 0.2s;
  }
  .nav.mobile-open {
    transform: translateX(0);
  }
  .nav.collapsed { width: var(--nav-width); }
  .main {
    margin-left: 0;
    padding-top: 64px;
  }
  body.sidebar-collapsed .main {
    margin-left: 0;
  }
  .sidebar-toggle { display: none; }
}
```

- [ ] **Step 2: Add mobile state to Nav.tsx**

Add to the Nav component:

```tsx
const [mobileOpen, setMobileOpen] = createSignal(false);

function closeMobile() { setMobileOpen(false); }
```

Wrap the nav return with:

```tsx
<>
  <div class="mobile-topbar">
    <button class="hamburger" onClick={() => setMobileOpen(!mobileOpen())} aria-label="Toggle navigation">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <line x1="3" y1="12" x2="21" y2="12" />
        <line x1="3" y1="6" x2="21" y2="6" />
        <line x1="3" y1="18" x2="21" y2="18" />
      </svg>
    </button>
    <div class="nav-brand">iplayer-arr</div>
  </div>
  <div class="nav-overlay" classList={{ visible: mobileOpen() }} onClick={closeMobile} />
  <nav class="nav" classList={{ collapsed: collapsed(), "mobile-open": mobileOpen() }} aria-label="Main navigation">
    {/* ... existing nav content ... */}
  </nav>
</>
```

Each `<A>` link also gets `onClick={closeMobile}` to auto-close on navigation.

- [ ] **Step 3: Build and verify**

Run: `cd /home/lns/iplayer-arr/frontend && npm run build`

- [ ] **Step 4: Commit**

```bash
cd /home/lns/iplayer-arr
git add frontend/
git commit -m "feat(ui): mobile hamburger menu with overlay"
```

---

## Phase 3: Backend Endpoints

### Task 5: Log ring buffer + API endpoint

**Files:**
- Create: `internal/api/logs.go`
- Create: `internal/api/logs_test.go`
- Modify: `internal/api/handler.go`
- Modify: `cmd/iplayer-arr/main.go`

- [ ] **Step 1: Write failing test for ring buffer**

Create `internal/api/logs_test.go`:

```go
package api

import (
	"testing"
)

func TestRingBuffer_Add(t *testing.T) {
	rb := NewRingBuffer(3)
	rb.Add(LogEntry{Level: "info", Message: "one"})
	rb.Add(LogEntry{Level: "info", Message: "two"})
	rb.Add(LogEntry{Level: "info", Message: "three"})
	rb.Add(LogEntry{Level: "warn", Message: "four"})

	entries := rb.All()
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].Message != "two" {
		t.Errorf("expected oldest='two', got %q", entries[0].Message)
	}
	if entries[2].Message != "four" {
		t.Errorf("expected newest='four', got %q", entries[2].Message)
	}
}

func TestRingBuffer_Filter(t *testing.T) {
	rb := NewRingBuffer(10)
	rb.Add(LogEntry{Level: "debug", Message: "d"})
	rb.Add(LogEntry{Level: "info", Message: "i"})
	rb.Add(LogEntry{Level: "warn", Message: "w"})
	rb.Add(LogEntry{Level: "error", Message: "e"})

	entries := rb.Filter("warn", "")
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (warn+error), got %d", len(entries))
	}

	entries = rb.Filter("", "w")
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry matching 'w', got %d", len(entries))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/lns/iplayer-arr && go test ./internal/api/ -run TestRingBuffer -v`
Expected: FAIL (types/functions not defined)

- [ ] **Step 3: Implement ring buffer and log handler**

Create `internal/api/logs.go`:

```go
package api

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
}

type RingBuffer struct {
	mu      sync.RWMutex
	entries []LogEntry
	size    int
	head    int
	count   int
}

func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		entries: make([]LogEntry, size),
		size:    size,
	}
}

func (rb *RingBuffer) Add(e LogEntry) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.entries[rb.head] = e
	rb.head = (rb.head + 1) % rb.size
	if rb.count < rb.size {
		rb.count++
	}
}

func (rb *RingBuffer) All() []LogEntry {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.snapshot()
}

func (rb *RingBuffer) Filter(level, query string) []LogEntry {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	all := rb.snapshot()

	levelPriority := map[string]int{"debug": 0, "info": 1, "warn": 2, "error": 3}
	minLevel := 0
	if p, ok := levelPriority[level]; ok {
		minLevel = p
	}

	var result []LogEntry
	for _, e := range all {
		if level != "" {
			if p, ok := levelPriority[e.Level]; ok && p < minLevel {
				continue
			}
		}
		if query != "" && !strings.Contains(strings.ToLower(e.Message), strings.ToLower(query)) {
			continue
		}
		result = append(result, e)
	}
	return result
}

func (rb *RingBuffer) snapshot() []LogEntry {
	if rb.count == 0 {
		return nil
	}
	result := make([]LogEntry, rb.count)
	start := (rb.head - rb.count + rb.size) % rb.size
	for i := 0; i < rb.count; i++ {
		result[i] = rb.entries[(start+i)%rb.size]
	}
	return result
}

func (h *Handler) handleLogs(w http.ResponseWriter, r *http.Request) {
	level := r.URL.Query().Get("level")
	query := r.URL.Query().Get("q")

	var entries []LogEntry
	if level != "" || query != "" {
		entries = h.logs.Filter(level, query)
	} else {
		entries = h.logs.All()
	}
	if entries == nil {
		entries = []LogEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}
```

- [ ] **Step 4: Add `logs` field to Handler**

In `internal/api/handler.go`, add `logs *RingBuffer` to Handler struct:

```go
type Handler struct {
	store       *store.Store
	hub         *Hub
	mgr         *download.Manager
	ibl         *bbc.IBL
	status      *RuntimeStatus
	logs        *RingBuffer
	downloadDir string
	startedAt   time.Time
}
```

Update `NewHandler` to accept and store the ring buffer, downloadDir, and startedAt:

```go
func NewHandler(st *store.Store, hub *Hub, mgr *download.Manager, ibl *bbc.IBL, status *RuntimeStatus, logs *RingBuffer, downloadDir string, startedAt time.Time) *Handler {
	return &Handler{
		store:       st,
		hub:         hub,
		mgr:         mgr,
		ibl:         ibl,
		status:      status,
		logs:        logs,
		downloadDir: downloadDir,
		startedAt:   startedAt,
	}
}
```

Add route in `ServeHTTP`:

```go
case path == "/api/logs" && r.Method == "GET":
	h.handleLogs(w, r)
```

- [ ] **Step 5: Wire ring buffer in main.go**

In `cmd/iplayer-arr/main.go`, before creating the handler:

```go
logBuffer := api.NewRingBuffer(1000)
startedAt := time.Now()
```

Update the `NewHandler` call:

```go
apiHandler := api.NewHandler(st, hub, mgr, ibl, runtimeStatus, logBuffer, downloadDir, startedAt)
```

Add a log writer that feeds slog output into the ring buffer. After creating `logBuffer`:

```go
slog.SetDefault(slog.New(slog.NewTextHandler(io.MultiWriter(os.Stderr, &logBridge{buf: logBuffer}), nil)))
```

Define `logBridge` in `main.go`:

```go
type logBridge struct {
	buf *api.RingBuffer
}

func (lb *logBridge) Write(p []byte) (int, error) {
	line := strings.TrimSpace(string(p))
	if line == "" {
		return len(p), nil
	}
	level := "info"
	lower := strings.ToLower(line)
	if strings.Contains(lower, "error") || strings.Contains(lower, "fatal") {
		level = "error"
	} else if strings.Contains(lower, "warn") {
		level = "warn"
	} else if strings.Contains(lower, "debug") {
		level = "debug"
	}
	lb.buf.Add(api.LogEntry{
		Timestamp: time.Now(),
		Level:     level,
		Message:   line,
	})
	return len(p), nil
}
```

- [ ] **Step 6: Add `log:line` SSE event**

In `cmd/iplayer-arr/main.go`, after adding the log entry in `logBridge.Write`, also broadcast via hub:

```go
hub.Broadcast("log:line", api.LogEntry{
	Timestamp: time.Now(),
	Level:     level,
	Message:   line,
})
```

Store a reference to `hub` in `logBridge`:

```go
type logBridge struct {
	buf *api.RingBuffer
	hub *api.Hub
}
```

- [ ] **Step 7: Fix existing tests for new NewHandler signature**

Update `internal/api/handler_test.go` -- all calls to `NewHandler` need the new params:

```go
h := NewHandler(st, hub, mgr, ibl, status, NewRingBuffer(100), "/tmp", time.Now())
```

- [ ] **Step 8: Run tests**

Run: `cd /home/lns/iplayer-arr && go test ./internal/api/ -v`
Expected: All pass including new ring buffer tests.

- [ ] **Step 9: Commit**

```bash
cd /home/lns/iplayer-arr
git add internal/api/logs.go internal/api/logs_test.go internal/api/handler.go cmd/iplayer-arr/main.go
git commit -m "feat: log ring buffer with API endpoint and SSE streaming"
```

### Task 6: System health endpoint

**Files:**
- Create: `internal/api/system.go`
- Create: `internal/api/system_test.go`
- Modify: `internal/api/handler.go`

- [ ] **Step 1: Write failing test**

Create `internal/api/system_test.go`:

```go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHandleSystem(t *testing.T) {
	st := testStore(t)
	hub := NewHub()
	status := &RuntimeStatus{FFmpegVersion: "7.1", GeoOK: true}
	h := NewHandler(st, hub, nil, nil, status, NewRingBuffer(10), "/downloads", time.Now())

	req := httptest.NewRequest("GET", "/api/system", nil)
	w := httptest.NewRecorder()
	h.handleSystem(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["ffmpeg_version"] != "7.1" {
		t.Errorf("expected ffmpeg 7.1, got %v", resp["ffmpeg_version"])
	}
	if resp["geo_ok"] != true {
		t.Errorf("expected geo_ok true, got %v", resp["geo_ok"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/lns/iplayer-arr && go test ./internal/api/ -run TestHandleSystem -v`
Expected: FAIL

- [ ] **Step 3: Implement system handler**

Create `internal/api/system.go`:

```go
package api

import (
	"net/http"
	"runtime"
	"syscall"
	"time"
)

func (h *Handler) handleSystem(w http.ResponseWriter, r *http.Request) {
	history, _ := h.store.ListHistory()
	var completed, failed int
	var totalBytes int64
	for _, dl := range history {
		switch dl.Status {
		case "completed":
			completed++
			totalBytes += dl.Size
		case "failed":
			failed++
		}
	}

	var diskTotal, diskFree uint64
	var stat syscall.Statfs_t
	if err := syscall.Statfs(h.downloadDir, &stat); err == nil {
		diskTotal = stat.Blocks * uint64(stat.Bsize)
		diskFree = stat.Bavail * uint64(stat.Bsize)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"version":               version,
		"go_version":            runtime.Version(),
		"uptime_seconds":        int(time.Since(h.startedAt).Seconds()),
		"geo_ok":                h.status.GeoOK,
		"geo_checked_at":        h.status.GeoCheckedAt,
		"ffmpeg_version":        h.status.FFmpegVersion,
		"ffmpeg_path":           h.status.FFmpegPath,
		"disk_total":            diskTotal,
		"disk_free":             diskFree,
		"disk_path":             h.downloadDir,
		"downloads_completed":   completed,
		"downloads_failed":      failed,
		"downloads_total_bytes": totalBytes,
		"last_indexer_request":  h.status.LastIndexerRequest,
	})
}

func (h *Handler) handleGeoRecheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	// Re-run the geo probe
	ok := false
	if h.ibl != nil {
		status, err := h.ibl.Client().Head("https://open.live.bbc.co.uk/mediaselector/6/select/version/2.0/mediaset/pc/vpid/bbc_one_hd/format/xml")
		if err == nil && status == 200 {
			ok = true
		}
	}
	h.status.GeoOK = ok
	h.status.GeoCheckedAt = time.Now()
	writeJSON(w, http.StatusOK, map[string]interface{}{"geo_ok": ok})
}

var version = "dev"
```

- [ ] **Step 4: Extend RuntimeStatus**

In `internal/api/handler.go`, update RuntimeStatus:

```go
type RuntimeStatus struct {
	FFmpegVersion      string
	FFmpegPath         string
	GeoOK              bool
	GeoCheckedAt       time.Time
	LastIndexerRequest time.Time
}
```

- [ ] **Step 5: Add routes in ServeHTTP**

```go
case path == "/api/system" && r.Method == "GET":
	h.handleSystem(w, r)
case path == "/api/system/geo-check" && r.Method == "POST":
	h.handleGeoRecheck(w, r)
```

- [ ] **Step 6: Update main.go for new RuntimeStatus fields**

```go
runtimeStatus := &api.RuntimeStatus{
	FFmpegVersion: ffVer,
	FFmpegPath:    ffPath, // from download.CheckFFmpeg() -- update to return path too
	GeoOK:         geoOK,
	GeoCheckedAt:  time.Now(),
}
```

Note: `download.CheckFFmpeg()` currently returns `(string, error)`. Either update it to also return the path, or just use `"/usr/bin/ffmpeg"` as the common Docker path. Prefer updating the function.

- [ ] **Step 7: Run tests**

Run: `cd /home/lns/iplayer-arr && go test ./internal/api/ -v`
Expected: All pass.

- [ ] **Step 8: Commit**

```bash
cd /home/lns/iplayer-arr
git add internal/api/system.go internal/api/system_test.go internal/api/handler.go cmd/iplayer-arr/main.go
git commit -m "feat: system health endpoint with disk, stats, geo recheck"
```

### Task 7: History pagination, filtering, and stats

**Files:**
- Modify: `internal/store/history.go`
- Create: `internal/store/history_test.go` (or add to existing `store_test.go`)
- Modify: `internal/api/downloads.go`
- Modify: `internal/api/handler_test.go`

- [ ] **Step 1: Write failing test for ListHistoryFiltered**

Add to `internal/store/store_test.go` (or create `internal/store/history_test.go`):

```go
func TestListHistoryFiltered(t *testing.T) {
	st := testStore(t)
	// Seed 5 history items
	for i := 0; i < 5; i++ {
		dl := &Download{
			ID:          fmt.Sprintf("dl-%d", i),
			Status:      "completed",
			CompletedAt: time.Now().Add(-time.Duration(i) * time.Hour),
			Size:        1024,
		}
		if i == 2 {
			dl.Status = "failed"
		}
		st.PutHistory(dl)
	}

	// Test pagination
	items, total, err := st.ListHistoryFiltered("", time.Time{}, "completed_at", "desc", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 {
		t.Errorf("expected total=5, got %d", total)
	}
	if len(items) != 2 {
		t.Errorf("expected 2 items, got %d", len(items))
	}

	// Test status filter
	items, total, err = st.ListHistoryFiltered("failed", time.Time{}, "completed_at", "desc", 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("expected total=1 failed, got %d", total)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/lns/iplayer-arr && go test ./internal/store/ -run TestListHistoryFiltered -v`
Expected: FAIL

- [ ] **Step 3: Implement ListHistoryFiltered**

Add to `internal/store/history.go`:

```go
func (s *Store) ListHistoryFiltered(status string, since time.Time, sortField, sortOrder string, page, perPage int) ([]*Download, int, error) {
	var all []*Download
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketHistory).ForEach(func(k, v []byte) error {
			var dl Download
			if err := json.Unmarshal(v, &dl); err != nil {
				return err
			}
			if status != "" && dl.Status != status {
				return nil
			}
			if !since.IsZero() && dl.CompletedAt.Before(since) {
				return nil
			}
			all = append(all, &dl)
			return nil
		})
	})
	if err != nil {
		return nil, 0, err
	}

	// Sort
	sort.Slice(all, func(i, j int) bool {
		ascending := sortOrder != "desc"
		switch sortField {
		case "title":
			if ascending {
				return all[i].Title < all[j].Title
			}
			return all[i].Title > all[j].Title
		case "status":
			if ascending {
				return all[i].Status < all[j].Status
			}
			return all[i].Status > all[j].Status
		default: // completed_at
			if ascending {
				return all[i].CompletedAt.Before(all[j].CompletedAt)
			}
			return all[i].CompletedAt.After(all[j].CompletedAt)
		}
	})

	total := len(all)

	// Paginate
	start := (page - 1) * perPage
	if start >= total {
		return []*Download{}, total, nil
	}
	end := start + perPage
	if end > total {
		end = total
	}
	return all[start:end], total, nil
}

func (s *Store) HistoryStats(since time.Time) (completed, failed int, totalBytes int64, err error) {
	err = s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketHistory).ForEach(func(k, v []byte) error {
			var dl Download
			if err := json.Unmarshal(v, &dl); err != nil {
				return err
			}
			if !since.IsZero() && dl.CompletedAt.Before(since) {
				return nil
			}
			switch dl.Status {
			case "completed":
				completed++
				totalBytes += dl.Size
			case "failed":
				failed++
			}
			return nil
		})
	})
	return
}
```

Add `"sort"` to imports.

- [ ] **Step 4: Update API handler for paginated history**

In `internal/api/downloads.go`, replace `handleListHistory`:

```go
func (h *Handler) handleListHistory(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	sinceStr := r.URL.Query().Get("since")
	sortField := r.URL.Query().Get("sort")
	sortOrder := r.URL.Query().Get("order")
	pageStr := r.URL.Query().Get("page")
	perPageStr := r.URL.Query().Get("per_page")

	if sortField == "" {
		sortField = "completed_at"
	}
	if sortOrder == "" {
		sortOrder = "desc"
	}
	page := 1
	if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
		page = p
	}
	perPage := 20
	if pp, err := strconv.Atoi(perPageStr); err == nil && pp > 0 && pp <= 100 {
		perPage = pp
	}

	var since time.Time
	if sinceStr != "" {
		since, _ = time.Parse("2006-01-02", sinceStr)
	}

	items, total, err := h.store.ListHistoryFiltered(status, since, sortField, sortOrder, page, perPage)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items": items,
		"total": total,
	})
}
```

Add `"strconv"` to imports.

- [ ] **Step 5: Add history stats endpoint**

Add to `internal/api/downloads.go`:

```go
func (h *Handler) handleHistoryStats(w http.ResponseWriter, r *http.Request) {
	sinceStr := r.URL.Query().Get("since")
	var since time.Time
	if sinceStr != "" {
		since, _ = time.Parse("2006-01-02", sinceStr)
	}
	completed, failed, totalBytes, err := h.store.HistoryStats(since)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"completed":   completed,
		"failed":      failed,
		"total_bytes": totalBytes,
	})
}
```

Add route in `ServeHTTP`:

```go
case path == "/api/history/stats" && r.Method == "GET":
	h.handleHistoryStats(w, r)
```

**Important:** This route must appear BEFORE the `strings.HasPrefix(path, "/api/history/")` DELETE route, or it will never match.

- [ ] **Step 6: Run all tests**

Run: `cd /home/lns/iplayer-arr && go test ./... -v`
Expected: All pass.

- [ ] **Step 7: Commit**

```bash
cd /home/lns/iplayer-arr
git add internal/store/history.go internal/api/downloads.go internal/api/handler.go
git commit -m "feat: paginated history with filtering, sorting, and stats endpoint"
```

### Task 8: Add disk and indexer tracking to status endpoint

**Files:**
- Modify: `internal/api/status.go`
- Modify: `internal/api/handler.go`
- Modify: `internal/newznab/handler.go`

- [ ] **Step 1: Update handleStatus to include disk info**

```go
func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	downloads, _ := h.store.ListDownloads()

	activeWorkers := 0
	queueDepth := 0
	for _, dl := range downloads {
		switch dl.Status {
		case "downloading", "resolving", "converting":
			activeWorkers++
		case "pending":
			queueDepth++
		}
	}

	var diskFree, diskTotal uint64
	var stat syscall.Statfs_t
	if err := syscall.Statfs(h.downloadDir, &stat); err == nil {
		diskTotal = stat.Blocks * uint64(stat.Bsize)
		diskFree = stat.Bavail * uint64(stat.Bsize)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ffmpeg":                h.status.FFmpegVersion,
		"geo_ok":                h.status.GeoOK,
		"active_workers":        activeWorkers,
		"queue_depth":           queueDepth,
		"paused":                h.mgr != nil && h.mgr.IsPaused(),
		"disk_free":             diskFree,
		"disk_total":            diskTotal,
		"last_indexer_request":  h.status.LastIndexerRequest,
	})
}
```

Add `"syscall"` to imports.

- [ ] **Step 2: Track last indexer request**

In the newznab handler, when a search request arrives, update `RuntimeStatus.LastIndexerRequest`. The newznab handler needs access to the RuntimeStatus. This requires threading it through -- the simplest approach is to expose it as a field on the newznab handler that the API handler also references, or use a shared atomic timestamp.

Add to `internal/api/handler.go`:

```go
func (rs *RuntimeStatus) RecordIndexerRequest() {
	rs.LastIndexerRequest = time.Now()
}
```

In `internal/newznab/handler.go`, accept `*api.RuntimeStatus` in `NewHandler` and call `RecordIndexerRequest()` at the start of search handling. (This requires adding the API package import or extracting RuntimeStatus to a shared location. Simplest: add a `func() time.Time` callback to the newznab handler.)

Alternative (simpler, no import cycle): pass a `func()` callback:

In `internal/newznab/handler.go`, add to the handler struct:

```go
type Handler struct {
	ibl       *bbc.IBL
	store     *store.Store
	ms        *bbc.MediaSelector
	onRequest func() // called on each indexer request
}
```

Update `NewHandler` to accept `onRequest func()` and call `h.onRequest()` at the start of `ServeHTTP`.

In `cmd/iplayer-arr/main.go`:

```go
mux.Handle("/newznab/", newznab.NewHandler(ibl, st, ms, func() {
	runtimeStatus.LastIndexerRequest = time.Now()
}))
```

- [ ] **Step 3: Run tests**

Run: `cd /home/lns/iplayer-arr && go test ./... -v`

- [ ] **Step 4: Commit**

```bash
cd /home/lns/iplayer-arr
git add internal/api/status.go internal/api/handler.go internal/newznab/handler.go cmd/iplayer-arr/main.go
git commit -m "feat: add disk space and last indexer request to status endpoint"
```

---

## Phase 4: New Frontend Pages

### Task 9: Frontend types and API methods

**Files:**
- Modify: `frontend/src/types.ts`
- Modify: `frontend/src/api.ts`
- Modify: `frontend/src/sse.ts`

- [ ] **Step 1: Add new types**

Add to `frontend/src/types.ts`:

```typescript
export interface LogEntry {
  timestamp: string;
  level: string;
  message: string;
}

export interface SystemInfo {
  version: string;
  go_version: string;
  uptime_seconds: number;
  geo_ok: boolean;
  geo_checked_at: string;
  ffmpeg_version: string;
  ffmpeg_path: string;
  disk_total: number;
  disk_free: number;
  disk_path: string;
  downloads_completed: number;
  downloads_failed: number;
  downloads_total_bytes: number;
  last_indexer_request: string;
}

export interface HistoryPage {
  items: Download[];
  total: number;
}

export interface HistoryStats {
  completed: number;
  failed: number;
  total_bytes: number;
}
```

- [ ] **Step 2: Add API methods**

Add to the `api` object in `frontend/src/api.ts`:

```typescript
// Logs
getLogs: (level?: string, q?: string) => {
  const params: Record<string, string> = {};
  if (level) params.level = level;
  if (q) params.q = q;
  return get<LogEntry[]>("/api/logs", params);
},

// System
getSystem: () => get<SystemInfo>("/api/system"),
recheckGeo: () => post<{ geo_ok: boolean }>("/api/system/geo-check", {}),

// History (paginated)
listHistoryPaged: (params: {
  status?: string;
  since?: string;
  sort?: string;
  order?: string;
  page?: number;
  per_page?: number;
}) => {
  const p: Record<string, string> = {};
  if (params.status) p.status = params.status;
  if (params.since) p.since = params.since;
  if (params.sort) p.sort = params.sort;
  if (params.order) p.order = params.order;
  if (params.page) p.page = String(params.page);
  if (params.per_page) p.per_page = String(params.per_page);
  return get<HistoryPage>("/api/history", p);
},
historyStats: (since?: string) => {
  const p: Record<string, string> = {};
  if (since) p.since = since;
  return get<HistoryStats>("/api/history/stats", p);
},
```

Add imports for the new types at the top of `api.ts`.

- [ ] **Step 3: Add `log:line` to SSE**

In `frontend/src/sse.ts`, add `"log:line"` to the supported event types (it should already work with the generic handler pattern -- just add the typing).

- [ ] **Step 4: Build**

Run: `cd /home/lns/iplayer-arr/frontend && npm run build`

- [ ] **Step 5: Commit**

```bash
cd /home/lns/iplayer-arr
git add frontend/src/types.ts frontend/src/api.ts frontend/src/sse.ts
git commit -m "feat(ui): add types and API methods for logs, system, paginated history"
```

### Task 10: Log Viewer page

**Files:**
- Create: `frontend/src/pages/Logs.tsx`
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/styles.css`

- [ ] **Step 1: Create Logs.tsx**

```tsx
import { createSignal, onMount, onCleanup, For, Show } from "solid-js";
import { api } from "../api";
import { connectSSE } from "../sse";
import type { LogEntry } from "../types";

const LEVELS = ["all", "debug", "info", "warn", "error"];

function Logs() {
  const [entries, setEntries] = createSignal<LogEntry[]>([]);
  const [level, setLevel] = createSignal("all");
  const [search, setSearch] = createSignal("");
  const [autoScroll, setAutoScroll] = createSignal(true);
  const [streaming, setStreaming] = createSignal(true);
  let logRef: HTMLDivElement | undefined;

  async function loadLogs() {
    const lvl = level() === "all" ? undefined : level();
    const q = search() || undefined;
    const logs = await api.getLogs(lvl, q);
    setEntries(logs);
  }

  function scrollToBottom() {
    if (logRef && autoScroll()) {
      logRef.scrollTop = logRef.scrollHeight;
    }
  }

  function onScroll() {
    if (!logRef) return;
    const atBottom = logRef.scrollHeight - logRef.scrollTop - logRef.clientHeight < 40;
    setAutoScroll(atBottom);
  }

  onMount(() => {
    loadLogs();

    const cleanup = connectSSE({
      "log:line": (data) => {
        if (!streaming()) return;
        const entry = data as LogEntry;
        setEntries((prev) => {
          const next = [...prev, entry];
          if (next.length > 1000) next.shift();
          return next;
        });
        requestAnimationFrame(scrollToBottom);
      },
    });

    onCleanup(cleanup);
  });

  function levelClass(lvl: string): string {
    switch (lvl) {
      case "error": return "log-error";
      case "warn": return "log-warn";
      case "debug": return "log-debug";
      default: return "";
    }
  }

  function filtered() {
    let items = entries();
    const lvl = level();
    if (lvl !== "all") {
      const priority: Record<string, number> = { debug: 0, info: 1, warn: 2, error: 3 };
      const min = priority[lvl] ?? 0;
      items = items.filter((e) => (priority[e.level] ?? 0) >= min);
    }
    const q = search().toLowerCase();
    if (q) {
      items = items.filter((e) => e.message.toLowerCase().includes(q));
    }
    return items;
  }

  return (
    <div>
      <h1 class="page-title">Logs</h1>

      <div class="log-toolbar">
        <select class="input search-quality" value={level()} onChange={(e) => { setLevel(e.target.value); loadLogs(); }} aria-label="Log level filter">
          <For each={LEVELS}>{(l) => <option value={l}>{l}</option>}</For>
        </select>
        <input class="input" type="text" placeholder="Filter logs..." value={search()} onInput={(e) => setSearch(e.target.value)} aria-label="Filter logs" />
        <button class="btn btn-sm" classList={{ "btn-primary": !streaming() }} onClick={() => setStreaming(!streaming())}>
          {streaming() ? "Pause" : "Resume"}
        </button>
      </div>

      <div class="log-panel" ref={logRef} onScroll={onScroll}>
        <For each={filtered()}>
          {(entry) => (
            <div class={`log-line ${levelClass(entry.level)}`}>
              <span class="log-time">{new Date(entry.timestamp).toLocaleTimeString()}</span>
              <span class="log-level">{entry.level.padEnd(5)}</span>
              <span class="log-msg">{entry.message}</span>
            </div>
          )}
        </For>
        <Show when={filtered().length === 0}>
          <div class="card-empty">No log entries</div>
        </Show>
      </div>

      <Show when={!autoScroll()}>
        <button class="log-jump" onClick={() => { setAutoScroll(true); scrollToBottom(); }}>Jump to bottom</button>
      </Show>
    </div>
  );
}

export default Logs;
```

- [ ] **Step 2: Add log page CSS**

```css
.log-toolbar {
  display: flex;
  gap: 8px;
  margin-bottom: 12px;
  align-items: center;
}

.log-panel {
  background: var(--bg-surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 12px;
  height: 70vh;
  overflow-y: auto;
  font-family: "JetBrains Mono", "Fira Code", monospace;
  font-size: 12px;
  line-height: 1.6;
}

.log-line {
  display: flex;
  gap: 8px;
  white-space: pre-wrap;
  word-break: break-all;
}

.log-time {
  color: var(--text-muted);
  flex-shrink: 0;
}

.log-level {
  flex-shrink: 0;
  width: 44px;
  text-transform: uppercase;
  font-weight: 600;
}

.log-msg {
  flex: 1;
}

.log-error { color: var(--danger); }
.log-error .log-level { color: var(--danger); }
.log-warn { color: var(--warning); }
.log-warn .log-level { color: var(--warning); }
.log-debug { color: var(--text-secondary); }

.log-jump {
  position: fixed;
  bottom: 24px;
  right: 24px;
  background: var(--accent);
  color: white;
  border: none;
  border-radius: var(--radius);
  padding: 8px 16px;
  cursor: pointer;
  font-size: 13px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.3);
}
```

- [ ] **Step 3: Add route in App.tsx**

```tsx
import Logs from "./pages/Logs";
// ...
<Route path="/logs" component={Logs} />
```

- [ ] **Step 4: Build**

Run: `cd /home/lns/iplayer-arr/frontend && npm run build`

- [ ] **Step 5: Commit**

```bash
cd /home/lns/iplayer-arr
git add frontend/
git commit -m "feat(ui): log viewer page with real-time streaming and filtering"
```

### Task 11: System Health page

**Files:**
- Create: `frontend/src/pages/System.tsx`
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/styles.css`

- [ ] **Step 1: Create System.tsx**

```tsx
import { createSignal, onMount, Show } from "solid-js";
import { api } from "../api";
import { addToast } from "../toast";
import type { SystemInfo } from "../types";

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const k = 1024;
  const sizes = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + " " + sizes[i];
}

function formatUptime(seconds: number): string {
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d > 0) return `${d}d ${h}h ${m}m`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

function relativeTime(iso: string): string {
  if (!iso) return "Never";
  const diff = Date.now() - new Date(iso).getTime();
  const mins = Math.floor(diff / 60000);
  if (mins < 1) return "Just now";
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return `${Math.floor(hrs / 24)}d ago`;
}

function System() {
  const [info, setInfo] = createSignal<SystemInfo | null>(null);
  const [recheckLoading, setRecheckLoading] = createSignal(false);

  onMount(async () => {
    setInfo(await api.getSystem());
  });

  async function recheckGeo() {
    setRecheckLoading(true);
    try {
      const result = await api.recheckGeo();
      setInfo((prev) => prev ? { ...prev, geo_ok: result.geo_ok, geo_checked_at: new Date().toISOString() } : prev);
      addToast(result.geo_ok ? "success" : "warning", result.geo_ok ? "Geo check passed" : "Geo check failed");
    } catch (e) {
      addToast("error", `Geo recheck failed: ${e instanceof Error ? e.message : "unknown"}`);
    }
    setRecheckLoading(false);
  }

  return (
    <Show when={info()} fallback={<p class="text-muted">Loading...</p>}>
      {(sys) => (
        <div>
          <h1 class="page-title">System</h1>

          <div class="system-grid">
            <div class="card">
              <div class="card-header">BBC iPlayer Status</div>
              <div class="card-body">
                <div class="system-row">
                  <span class="status-dot" classList={{ ok: sys().geo_ok, err: !sys().geo_ok }} />
                  <span>{sys().geo_ok ? "UK access confirmed" : "Geo-blocked"}</span>
                </div>
                <p class="text-muted">Last checked: {relativeTime(sys().geo_checked_at)}</p>
                <button class="btn btn-sm btn-primary" onClick={recheckGeo} disabled={recheckLoading()}>
                  {recheckLoading() ? "Checking..." : "Re-check"}
                </button>
              </div>
            </div>

            <div class="card">
              <div class="card-header">ffmpeg</div>
              <div class="card-body">
                <p><strong>Version:</strong> {sys().ffmpeg_version || "Not found"}</p>
                <p class="text-muted">{sys().ffmpeg_path}</p>
              </div>
            </div>

            <div class="card">
              <div class="card-header">Download Stats</div>
              <div class="card-body">
                <p><strong>{sys().downloads_completed}</strong> completed</p>
                <p><strong>{sys().downloads_failed}</strong> failed</p>
                <Show when={sys().downloads_completed + sys().downloads_failed > 0}>
                  <p class="text-muted">
                    {((sys().downloads_completed / (sys().downloads_completed + sys().downloads_failed)) * 100).toFixed(0)}% success rate
                  </p>
                </Show>
                <p class="text-muted">{formatBytes(sys().downloads_total_bytes)} total</p>
              </div>
            </div>

            <div class="card">
              <div class="card-header">Storage</div>
              <div class="card-body">
                <p class="text-muted">{sys().disk_path}</p>
                <div class="progress-bar" role="progressbar" aria-valuenow={Math.round(((sys().disk_total - sys().disk_free) / sys().disk_total) * 100)} aria-valuemin={0} aria-valuemax={100} aria-label="Disk usage">
                  <div class="progress-fill" style={{ width: `${((sys().disk_total - sys().disk_free) / sys().disk_total) * 100}%` }} />
                </div>
                <p class="text-muted">{formatBytes(sys().disk_free)} free of {formatBytes(sys().disk_total)}</p>
              </div>
            </div>

            <div class="card">
              <div class="card-header">Sonarr Integration</div>
              <div class="card-body">
                <p class="text-muted">Last indexer request: {relativeTime(sys().last_indexer_request)}</p>
              </div>
            </div>

            <div class="card">
              <div class="card-header">About</div>
              <div class="card-body">
                <p><strong>iplayer-arr</strong> {sys().version}</p>
                <p class="text-muted">{sys().go_version}</p>
                <p class="text-muted">Uptime: {formatUptime(sys().uptime_seconds)}</p>
              </div>
            </div>
          </div>
        </div>
      )}
    </Show>
  );
}

export default System;
```

- [ ] **Step 2: Add system page CSS**

```css
.system-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 16px;
}
.system-grid .card {
  margin-bottom: 0;
}
.system-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
```

- [ ] **Step 3: Add route in App.tsx**

```tsx
import System from "./pages/System";
// ...
<Route path="/system" component={System} />
```

- [ ] **Step 4: Build**

Run: `cd /home/lns/iplayer-arr/frontend && npm run build`

- [ ] **Step 5: Commit**

```bash
cd /home/lns/iplayer-arr
git add frontend/
git commit -m "feat(ui): system health page with geo recheck and disk usage"
```

---

## Phase 5: Dashboard Enhancements + History

### Task 12: Dashboard health strip

**Files:**
- Modify: `frontend/src/pages/Dashboard.tsx`
- Modify: `frontend/src/styles.css`
- Modify: `frontend/src/types.ts`

- [ ] **Step 1: Update StatusResponse type**

In `frontend/src/types.ts`, add the new fields to StatusResponse (or create it if not present):

```typescript
export interface StatusResponse {
  ffmpeg: string;
  geo_ok: boolean;
  active_workers: number;
  queue_depth: number;
  paused: boolean;
  disk_free: number;
  disk_total: number;
  last_indexer_request: string;
}
```

- [ ] **Step 2: Replace status bar with health strip**

In `Dashboard.tsx`, replace the status bar JSX with health pill cards:

```tsx
<Show when={status()}>
  {(st) => (
    <div class="health-strip">
      <div class="health-pill">
        <span class="status-dot" classList={{ ok: st().geo_ok, err: !st().geo_ok }} />
        <span class="health-label">Geo</span>
        <span class="health-value">{st().geo_ok ? "UK OK" : "Blocked"}</span>
      </div>
      <div class="health-pill">
        <span class="health-label">ffmpeg</span>
        <span class="health-value">{st().ffmpeg || "Not found"}</span>
      </div>
      <div class="health-pill">
        <span class="health-label">Sonarr</span>
        <span class="health-value">{st().last_indexer_request ? relativeTime(st().last_indexer_request) : "No requests"}</span>
      </div>
      <div class="health-pill">
        <span class="health-label">Disk</span>
        <span class="health-value" classList={{ "text-danger": st().disk_free < 1073741824 }}>
          {formatBytes(st().disk_free)} free
        </span>
      </div>
      <button class="btn btn-sm pause-btn" classList={{ "pause-btn--active": paused() || st().paused, "pause-btn--inactive": !(paused() || st().paused) }} onClick={togglePause}>
        {paused() ? "Resume" : "Pause"}
      </button>
    </div>
  )}
</Show>
```

Add the `relativeTime` and `formatBytes` helper functions (same as in System.tsx -- or extract to a shared `utils.ts`).

- [ ] **Step 3: Add health strip CSS**

```css
.health-strip {
  display: flex;
  gap: 12px;
  align-items: center;
  flex-wrap: wrap;
  margin-bottom: 20px;
  padding: 12px 16px;
  background: var(--bg-surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
}

.health-pill {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
}

.health-label {
  color: var(--text-secondary);
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.health-value {
  color: var(--text-primary);
  font-weight: 500;
}
```

- [ ] **Step 4: Build and verify**

Run: `cd /home/lns/iplayer-arr/frontend && npm run build`

- [ ] **Step 5: Commit**

```bash
cd /home/lns/iplayer-arr
git add frontend/
git commit -m "feat(ui): dashboard health strip with geo, ffmpeg, sonarr, disk pills"
```

### Task 13: Enhanced history section on dashboard

**Files:**
- Modify: `frontend/src/pages/Dashboard.tsx`
- Modify: `frontend/src/styles.css`

- [ ] **Step 1: Replace history section with paginated version**

Replace the history signals and loading in Dashboard with:

```tsx
const [history, setHistory] = createSignal<Download[]>([]);
const [historyTotal, setHistoryTotal] = createSignal(0);
const [historyPage, setHistoryPage] = createSignal(1);
const [historyFilter, setHistoryFilter] = createSignal("");
const [historyStats, setHistoryStats] = createSignal<HistoryStats | null>(null);
```

In `loadData`, replace the history fetch:

```tsx
const [hist, stats] = await Promise.all([
  api.listHistoryPaged({ page: 1, per_page: 20 }),
  api.historyStats(),
]);
setHistory(hist.items);
setHistoryTotal(hist.total);
setHistoryStats(stats);
```

Add a `loadHistory` function for filter/page changes:

```tsx
async function loadHistory() {
  const result = await api.listHistoryPaged({
    status: historyFilter() || undefined,
    page: historyPage(),
    per_page: 20,
  });
  setHistory(result.items);
  setHistoryTotal(result.total);
}
```

- [ ] **Step 2: Update history JSX**

Replace the "Recent History" card with:

```tsx
<div class="card">
  <div class="card-header">
    <span>History</span>
    <div class="history-controls">
      <select class="input search-quality" value={historyFilter()} onChange={(e) => { setHistoryFilter(e.target.value); setHistoryPage(1); loadHistory(); }} aria-label="Filter history by status">
        <option value="">All</option>
        <option value="completed">Completed</option>
        <option value="failed">Failed</option>
      </select>
    </div>
  </div>
  <Show when={historyStats()}>
    {(stats) => (
      <div class="history-stats">
        {stats().completed} completed / {stats().failed} failed / {formatBytes(stats().total_bytes)} total
      </div>
    )}
  </Show>
  {/* ... existing table ... */}
  <Show when={historyTotal() > 20}>
    <div class="pagination">
      <button class="btn btn-sm" disabled={historyPage() <= 1} onClick={() => { setHistoryPage((p) => p - 1); loadHistory(); }}>Prev</button>
      <span class="text-muted">Page {historyPage()} of {Math.ceil(historyTotal() / 20)}</span>
      <button class="btn btn-sm" disabled={historyPage() * 20 >= historyTotal()} onClick={() => { setHistoryPage((p) => p + 1); loadHistory(); }}>Next</button>
    </div>
  </Show>
</div>
```

- [ ] **Step 3: Add pagination and stats CSS**

```css
.history-controls {
  display: flex;
  gap: 8px;
  margin-left: auto;
}

.history-stats {
  padding: 8px 16px;
  font-size: 12px;
  color: var(--text-secondary);
  border-bottom: 1px solid var(--border);
}

.pagination {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
  padding: 12px;
}
```

- [ ] **Step 4: Add delete per row and Clear All**

In the history table, add a delete button to each row:

```tsx
<td>
  <button class="btn btn-sm btn-delete" onClick={async () => {
    await api.deleteHistory(dl.id);
    loadHistory();
    addToast("success", "Deleted");
  }} aria-label={`Delete ${dl.title}`}>Delete</button>
</td>
```

Add a "Clear All" button in the card header alongside the filter:

```tsx
<button class="btn btn-sm btn-delete" onClick={async () => {
  if (!confirm("Delete all history?")) return;
  for (const item of history()) {
    await api.deleteHistory(item.id);
  }
  loadHistory();
  api.historyStats().then(setHistoryStats);
  addToast("success", "History cleared");
}}>Clear All</button>
```

Add the "Actions" `<th>` header to the table.

- [ ] **Step 5: Add sortable column headers**

Add sort state:

```tsx
const [sortField, setSortField] = createSignal("completed_at");
const [sortOrder, setSortOrder] = createSignal("desc");
```

Update `loadHistory` to pass sort params:

```tsx
async function loadHistory() {
  const result = await api.listHistoryPaged({
    status: historyFilter() || undefined,
    page: historyPage(),
    per_page: 20,
    sort: sortField(),
    order: sortOrder(),
  });
  setHistory(result.items);
  setHistoryTotal(result.total);
}
```

Make column headers clickable:

```tsx
function toggleSort(field: string) {
  if (sortField() === field) {
    setSortOrder((o) => o === "desc" ? "asc" : "desc");
  } else {
    setSortField(field);
    setSortOrder("desc");
  }
  loadHistory();
}
```

```tsx
<th scope="col" class="sortable" onClick={() => toggleSort("title")}>
  Title {sortField() === "title" ? (sortOrder() === "asc" ? "\u25B2" : "\u25BC") : ""}
</th>
```

Add CSS:

```css
.sortable {
  cursor: pointer;
  user-select: none;
}
.sortable:hover {
  color: var(--text-primary);
}
```

- [ ] **Step 6: Add download speed display to active downloads**

In Dashboard.tsx, track previous progress for speed calculation:

```tsx
const prevProgress = new Map<string, { time: number; downloaded: number }>();

function calcSpeed(dl: Download): string {
  const prev = prevProgress.get(dl.id);
  const now = Date.now();
  if (prev && now - prev.time > 0) {
    const bytesPerSec = ((dl.downloaded - prev.downloaded) / ((now - prev.time) / 1000));
    prevProgress.set(dl.id, { time: now, downloaded: dl.downloaded });
    if (bytesPerSec > 0) return formatBytes(bytesPerSec) + "/s";
  } else {
    prevProgress.set(dl.id, { time: now, downloaded: dl.downloaded });
  }
  return "";
}
```

Call `calcSpeed(dl)` in the download:progress SSE handler and display it in the `dl-meta` span:

```tsx
<Show when={calcSpeed(dl)}>
  <span class="text-muted">{calcSpeed(dl)}</span>
</Show>
```

Note: `prevProgress` is a plain `Map` outside reactive state since it's a mutable cache for speed calculation, not displayed directly.

- [ ] **Step 7: Build and verify**

Run: `cd /home/lns/iplayer-arr/frontend && npm run build`

- [ ] **Step 8: Commit**

```bash
cd /home/lns/iplayer-arr
git add frontend/
git commit -m "feat(ui): paginated history with stats, sort, delete, and download speed"
```

---

## Phase 6: Setup Wizard

### Task 14: Setup wizard component

**Files:**
- Create: `frontend/src/components/SetupWizard.tsx`
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/pages/Config.tsx`
- Modify: `frontend/src/api.ts`
- Modify: `frontend/src/styles.css`
- Modify: `internal/api/config.go` (expose wizard_completed in config response)

- [ ] **Step 1: Add wizard_completed to config API**

In `internal/api/config.go`, ensure `handleGetConfig` includes `wizard_completed` in its response. The config endpoint already reads from BoltDB -- just add the key to the response map.

In `frontend/src/types.ts`, add to ConfigResponse:

```typescript
wizard_completed: string; // "true" or ""
```

In `frontend/src/api.ts`, add:

```typescript
setWizardCompleted: () => put<{ status: string }>("/api/config", { key: "wizard_completed", value: "true" }),
resetWizard: () => put<{ status: string }>("/api/config", { key: "wizard_completed", value: "" }),
```

- [ ] **Step 2: Create SetupWizard.tsx**

```tsx
import { createSignal, Show } from "solid-js";
import { api } from "../api";
import { addToast } from "../toast";
import type { ConfigResponse, StatusResponse } from "../types";

interface Props {
  config: ConfigResponse;
  status: StatusResponse;
  onComplete: () => void;
}

function SetupWizard(props: Props) {
  const [step, setStep] = createSignal(1);
  const [testResult, setTestResult] = createSignal<string | null>(null);

  function copyText(text: string) {
    navigator.clipboard.writeText(text);
    addToast("success", "Copied!");
  }

  async function testIndexer() {
    setTestResult(null);
    try {
      const res = await fetch(`/newznab/api?t=caps&apikey=${props.config.api_key}`);
      if (res.ok) {
        setTestResult("ok");
        addToast("success", "Indexer endpoint is working");
      } else {
        setTestResult("fail");
        addToast("error", `Indexer returned ${res.status}`);
      }
    } catch {
      setTestResult("fail");
      addToast("error", "Cannot reach indexer endpoint");
    }
  }

  async function testSabnzbd() {
    setTestResult(null);
    try {
      const res = await fetch(`/sabnzbd/api?mode=version&apikey=${props.config.api_key}`);
      if (res.ok) {
        setTestResult("ok");
        addToast("success", "Download client endpoint is working");
      } else {
        setTestResult("fail");
        addToast("error", `Download client returned ${res.status}`);
      }
    } catch {
      setTestResult("fail");
      addToast("error", "Cannot reach download client endpoint");
    }
  }

  async function finish() {
    await api.setWizardCompleted();
    props.onComplete();
  }

  return (
    <div class="wizard-overlay">
      <div class="wizard-modal">
        <div class="wizard-progress">
          <div class="wizard-step" classList={{ active: step() >= 1 }}>1</div>
          <div class="wizard-line" classList={{ active: step() >= 2 }} />
          <div class="wizard-step" classList={{ active: step() >= 2 }}>2</div>
          <div class="wizard-line" classList={{ active: step() >= 3 }} />
          <div class="wizard-step" classList={{ active: step() >= 3 }}>3</div>
        </div>

        <Show when={step() === 1}>
          <h2>Welcome to iplayer-arr</h2>
          <p class="text-secondary">Let's check your system is ready.</p>
          <div class="wizard-checks">
            <div class="wizard-check">
              <span class="status-dot" classList={{ ok: props.status.geo_ok, err: !props.status.geo_ok }} />
              <span>{props.status.geo_ok ? "UK geo access confirmed" : "Geo-blocked -- ensure your container routes through a UK VPN"}</span>
            </div>
            <div class="wizard-check">
              <span class="status-dot" classList={{ ok: !!props.status.ffmpeg, err: !props.status.ffmpeg }} />
              <span>{props.status.ffmpeg ? `ffmpeg ${props.status.ffmpeg}` : "ffmpeg not found -- install ffmpeg in the container"}</span>
            </div>
          </div>
          <div class="wizard-actions">
            <button class="btn btn-primary" onClick={() => { setStep(2); setTestResult(null); }} disabled={!props.status.geo_ok}>Next</button>
          </div>
        </Show>

        <Show when={step() === 2}>
          <h2>Add Indexer to Sonarr</h2>
          <p class="text-secondary">Settings &gt; Indexers &gt; + &gt; Newznab</p>
          <div class="wizard-field">
            <label class="text-secondary">URL</label>
            <div class="wizard-copy">
              <code>http://&lt;host&gt;:8191/newznab/api</code>
              <button class="btn btn-sm btn-primary" onClick={() => copyText(`http://<host>:8191/newznab/api`)}>Copy</button>
            </div>
          </div>
          <div class="wizard-field">
            <label class="text-secondary">API Key</label>
            <div class="wizard-copy">
              <code>{props.config.api_key}</code>
              <button class="btn btn-sm btn-primary" onClick={() => copyText(props.config.api_key)}>Copy</button>
            </div>
          </div>
          <button class="btn btn-sm" onClick={testIndexer}>Test Connection</button>
          <Show when={testResult()}>
            <span class={testResult() === "ok" ? "text-success" : "text-danger"}>{testResult() === "ok" ? " Working" : " Failed"}</span>
          </Show>
          <div class="wizard-actions">
            <button class="btn" onClick={() => setStep(1)}>Back</button>
            <button class="btn btn-primary" onClick={() => { setStep(3); setTestResult(null); }}>Next</button>
          </div>
        </Show>

        <Show when={step() === 3}>
          <h2>Add Download Client to Sonarr</h2>
          <p class="text-secondary">Settings &gt; Download Clients &gt; + &gt; SABnzbd</p>
          <div class="wizard-field">
            <label class="text-secondary">Host / Port / URL Base</label>
            <div class="wizard-copy">
              <code>&lt;host&gt; : 8191 /sabnzbd</code>
            </div>
          </div>
          <div class="wizard-field">
            <label class="text-secondary">API Key</label>
            <div class="wizard-copy">
              <code>{props.config.api_key}</code>
              <button class="btn btn-sm btn-primary" onClick={() => copyText(props.config.api_key)}>Copy</button>
            </div>
          </div>
          <div class="wizard-field">
            <label class="text-secondary">Category</label>
            <div class="wizard-copy">
              <code>sonarr</code>
              <button class="btn btn-sm btn-primary" onClick={() => copyText("sonarr")}>Copy</button>
            </div>
          </div>
          <button class="btn btn-sm" onClick={testSabnzbd}>Test Connection</button>
          <Show when={testResult()}>
            <span class={testResult() === "ok" ? "text-success" : "text-danger"}>{testResult() === "ok" ? " Working" : " Failed"}</span>
          </Show>
          <div class="wizard-actions">
            <button class="btn" onClick={() => setStep(2)}>Back</button>
            <button class="btn btn-primary" onClick={finish}>Done</button>
          </div>
        </Show>
      </div>
    </div>
  );
}

export default SetupWizard;
```

- [ ] **Step 3: Add wizard CSS**

```css
.wizard-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.6);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 300;
}
.wizard-modal {
  background: var(--bg-surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 32px;
  max-width: 520px;
  width: 90%;
}
.wizard-progress {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0;
  margin-bottom: 24px;
}
.wizard-step {
  width: 32px;
  height: 32px;
  border-radius: 50%;
  background: var(--bg-elevated);
  color: var(--text-secondary);
  display: flex;
  align-items: center;
  justify-content: center;
  font-weight: 600;
  font-size: 14px;
}
.wizard-step.active {
  background: var(--accent);
  color: white;
}
.wizard-line {
  width: 40px;
  height: 2px;
  background: var(--bg-elevated);
}
.wizard-line.active {
  background: var(--accent);
}
.wizard-checks {
  display: flex;
  flex-direction: column;
  gap: 12px;
  margin: 16px 0;
}
.wizard-check {
  display: flex;
  align-items: center;
  gap: 8px;
}
.wizard-field {
  margin: 12px 0;
}
.wizard-field label {
  display: block;
  margin-bottom: 4px;
  font-size: 12px;
}
.wizard-copy {
  display: flex;
  align-items: center;
  gap: 8px;
}
.wizard-copy code {
  flex: 1;
  background: var(--bg-elevated);
  padding: 6px 10px;
  border-radius: 4px;
  font-size: 13px;
}
.wizard-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 24px;
}
```

- [ ] **Step 4: Mount wizard in App.tsx**

In `App.tsx`, add wizard state and mount:

```tsx
import { createSignal, onMount, Show } from "solid-js";
import SetupWizard from "./components/SetupWizard";

// Inside App or Layout:
const [showWizard, setShowWizard] = createSignal(false);
const [wizardConfig, setWizardConfig] = createSignal(null);
const [wizardStatus, setWizardStatus] = createSignal(null);

onMount(async () => {
  try {
    const [config, status] = await Promise.all([api.getConfig(), api.getStatus()]);
    if (config.wizard_completed !== "true") {
      setWizardConfig(config);
      setWizardStatus(status);
      setShowWizard(true);
    }
  } catch {
    // API not ready
  }
});

// In JSX, before the Router:
<Show when={showWizard() && wizardConfig() && wizardStatus()}>
  <SetupWizard
    config={wizardConfig()!}
    status={wizardStatus()!}
    onComplete={() => setShowWizard(false)}
  />
</Show>
```

- [ ] **Step 5: Add "Re-run Setup" to Config page**

In `Config.tsx`, add a button:

```tsx
<button class="btn btn-sm" onClick={async () => {
  await api.resetWizard();
  window.location.reload();
}}>Re-run Setup Wizard</button>
```

- [ ] **Step 6: Build**

Run: `cd /home/lns/iplayer-arr/frontend && npm run build`

- [ ] **Step 7: Commit**

```bash
cd /home/lns/iplayer-arr
git add frontend/ internal/api/config.go
git commit -m "feat(ui): first-run setup wizard with health checks and Sonarr config"
```

---

## Phase 7: Final Polish

### Task 15: Extract shared utility functions

**Files:**
- Create: `frontend/src/utils.ts`
- Modify: `frontend/src/pages/Dashboard.tsx`
- Modify: `frontend/src/pages/System.tsx`
- Modify: `frontend/src/pages/Downloads.tsx`

- [ ] **Step 1: Create utils.ts with shared formatters**

```typescript
export function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const k = 1024;
  const sizes = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + " " + sizes[i];
}

export function formatDuration(seconds: number): string {
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m ${s}s`;
  return `${s}s`;
}

export function formatUptime(seconds: number): string {
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d > 0) return `${d}d ${h}h ${m}m`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

export function relativeTime(iso: string): string {
  if (!iso) return "Never";
  const diff = Date.now() - new Date(iso).getTime();
  const mins = Math.floor(diff / 60000);
  if (mins < 1) return "Just now";
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return `${Math.floor(hrs / 24)}d ago`;
}
```

- [ ] **Step 2: Update all pages to import from utils.ts**

Remove duplicate `formatBytes`, `formatDuration`, `relativeTime`, `formatUptime` from Dashboard.tsx, System.tsx, Downloads.tsx. Replace with imports from `../utils`.

- [ ] **Step 3: Build and verify**

Run: `cd /home/lns/iplayer-arr/frontend && npm run build`

- [ ] **Step 4: Commit**

```bash
cd /home/lns/iplayer-arr
git add frontend/
git commit -m "refactor(ui): extract shared formatters to utils.ts"
```

### Task 16: Full build and integration test

**Files:** None (verification only)

- [ ] **Step 1: Build backend**

Run: `cd /home/lns/iplayer-arr && go build ./cmd/iplayer-arr/`
Expected: Clean build, no errors.

- [ ] **Step 2: Run all Go tests**

Run: `cd /home/lns/iplayer-arr && go test ./... -v`
Expected: All pass.

- [ ] **Step 3: Build frontend**

Run: `cd /home/lns/iplayer-arr/frontend && npm run build`
Expected: Clean build.

- [ ] **Step 4: Run go vet**

Run: `cd /home/lns/iplayer-arr && go vet ./...`
Expected: Clean.

- [ ] **Step 5: Verify no inline styles remain**

Run: `grep -rn 'style=' frontend/src/pages/ frontend/src/components/`
Expected: Only the dynamic progress bar width (`style={{ width: ... }}`).

- [ ] **Step 6: Final commit if any fixes needed**

```bash
cd /home/lns/iplayer-arr
git add -A
git commit -m "fix: integration test cleanup"
```
