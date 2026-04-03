# iplayer-arr UI/UX Polish for Public Release

**Date:** 2026-04-03
**Status:** Draft
**Target:** Sonarr power users (configure once, monitor occasionally)
**Approach:** Incremental polish -- enhance existing pages, add 3 new features, restyle with distinctive identity

---

## 1. Visual Identity & Theme

### Colour Palette

| Token | Value | Usage |
|-------|-------|-------|
| `--bg-base` | `#0f1117` | Page background |
| `--bg-surface` | `#161921` | Cards, sidebar |
| `--bg-elevated` | `#1e2230` | Inputs, hover states |
| `--accent` | `#c73e64` | Primary actions, active nav, progress fills |
| `--accent-hover` | `#d4516f` | Button hover |
| `--success` | `#22c55e` | Healthy status, completed badges |
| `--warning` | `#f59e0b` | Paused state, warnings |
| `--danger` | `#ef4444` | Failed badges, delete actions |
| `--text-primary` | `#e8eaed` | Main text |
| `--text-secondary` | `#8b8fa3` | Labels, metadata |
| `--border` | `#2a2e3d` | Card borders, dividers |

The rose/magenta accent (`#c73e64`) is inspired by BBC iPlayer's brand colour and is distinct from Sonarr (blue), Radarr (orange), and SABnzbd (yellow).

### Typography

Inter with system fallback: `-apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif`. No web font download to keep the Docker image lean.

### Design Tokens

Complete the CSS extraction started in the recent audit. All inline `style=` attributes in components must be replaced with CSS classes or CSS custom properties. Zero inline styles in the final state.

### Card Refinements

- 1px `var(--border)` border instead of background-only differentiation
- 8px border-radius
- Subtle `box-shadow` on hover for interactive cards (search results, download items)
- Consistent 16px internal padding

---

## 2. Collapsible Sidebar

### States

- **Expanded** (default): icons + labels, 220px wide
- **Collapsed**: icons only, 56px wide
- Toggle: chevron button at the bottom of the sidebar
- Preference persisted in `localStorage` key `sidebar-collapsed`

### Nav Items (7)

1. Dashboard (grid icon)
2. Downloads (download icon)
3. Search (magnifying glass)
4. Logs (terminal/scroll icon) -- NEW
5. Config (gear icon)
6. Overrides (pencil icon)
7. System (info/server icon) -- NEW

### Brand

- Expanded: "iplayer-arr" text with accent colour
- Collapsed: accent-coloured dot or "ia" monogram

### Transitions

- 200ms CSS transition on width, no layout jumps
- Main content area uses `margin-left` that transitions with the sidebar

### Mobile (< 768px)

- Sidebar hidden by default
- Hamburger icon in a top bar triggers a slide-in overlay
- Overlay dismissed by tapping outside or selecting a nav item

---

## 3. Enhanced Dashboard

### Health Strip (top)

Replaces the current status bar. Four status pills in a horizontal row:

| Pill | Data Source | Good State | Bad State |
|------|------------|------------|-----------|
| Geo Check | `/api/status` `geo_ok` | Green dot + "UK OK" | Red dot + "Geo Blocked" |
| ffmpeg | `/api/status` `ffmpeg` | Version string | "Not Found" in red |
| Sonarr | New: last indexer request timestamp | "Connected" + relative time | "No requests yet" in amber |
| Disk Space | New: `GET /api/status` gains `disk_free` field | Free space value | Red when < 1 GB |

Pause/Resume button remains right-aligned in this strip.

### Active Downloads (middle)

Largely unchanged. Additions:
- **Download speed** per item: calculated from progress delta over time (frontend-side, using SSE progress events). Displayed as "X MB/s" in the metadata row.
- **Queue items** get a subtle grip icon on the left (visual only, no drag functionality yet -- placeholder for future reorder).

### Recent Activity (bottom)

Enhanced from current state:
- **Stats row** above the table: "24 completed / 2 failed / 1.2 GB today"
- **Status filter** dropdown: All / Completed / Failed
- **Pagination**: 20 per page, prev/next buttons, "Page X of Y" indicator
- **Relative timestamps**: "2 hours ago" with full date on hover (`title` attribute)

Backend changes:
- `GET /api/history` gains query params: `?status=completed|failed`, `?page=1`, `?per_page=20`
- Response adds `total_count` field for pagination
- New `GET /api/history/stats` endpoint: `{ completed: number, failed: number, total_bytes: number }`

---

## 4. First-Run Setup Wizard

### Trigger

Appears automatically when `wizard_completed` is not `true` in BoltDB config bucket. On first run this flag is absent, so the wizard shows.

Can be re-triggered manually via a "Re-run Setup" button on the Config page (resets the flag to `false`).

### Implementation

Modal overlay (not a separate page/route). Three steps with a progress indicator:

#### Step 1: Welcome & Health Check

- Auto-runs: geo check, ffmpeg detection
- Displays results with green/red status indicators
- If geo check fails: shows actionable message ("iplayer-arr must be able to reach BBC iPlayer. Ensure your container routes through a UK VPN.")
- If ffmpeg missing: shows install guidance
- "Next" button (enabled only when geo check passes)

#### Step 2: Sonarr Indexer Setup

- Displays Newznab URL: `http://<host>:<port>/newznab/api`
- Displays API key with copy button
- Screenshot-style visual showing where these go in Sonarr's UI (static image or ASCII diagram)
- "Test Connection" button: hits our own Newznab caps endpoint (`/newznab/api?t=caps`) to verify the indexer is serving correctly. This confirms our side works; Sonarr connectivity is verified when Sonarr actually queries us.
- "Next" button

#### Step 3: Sonarr Download Client Setup

- Displays SABnzbd connection details: host, port, URL base (`/sabnzbd`), API key, category (`sonarr`)
- Copy buttons for each field
- "Test Connection" button: hits the SABnzbd version endpoint
- "Done" button: marks wizard as completed, dismisses modal

### Wizard State

- Progress through steps is not persisted (refreshing restarts the wizard)
- Only completion state is stored in BoltDB
- Wizard cannot be skipped without completing all health checks

---

## 5. Log Viewer Page

### Route

`/logs`

### Backend

- **Ring buffer**: 1000-line capacity, held in memory in Go
- **API endpoint**: `GET /api/logs` returns the current buffer contents as JSON array
  - Query params: `?level=info|warn|error|debug`, `?q=searchterm`
  - Each entry: `{ timestamp: string, level: string, message: string }`
- **SSE event**: `log:line` pushes each new log line in real-time
- **Log capture**: redirect Go `slog` output to both stderr and the ring buffer

### Frontend

- Scrollable log panel with monospace font
- **Auto-scroll**: pinned to bottom unless user scrolls up. "Jump to bottom" button appears when not pinned.
- **Level filter**: dropdown (All / Debug / Info / Warn / Error). Each higher level includes levels above it (e.g. "Warn" shows warn + error).
- **Search**: free-text input that filters displayed lines client-side
- **Colour coding**: debug = `var(--text-secondary)`, info = `var(--text-primary)`, warn = `var(--warning)`, error = `var(--danger)`
- **Clear button**: clears the frontend display (not the ring buffer)
- **Pause streaming** toggle: stops consuming SSE events temporarily for reading

### Constraints

- No persistence beyond the ring buffer. Full logs require `docker logs`.
- Buffer size (1000 lines) is not configurable in the UI. Could be made an env var if needed.

---

## 6. System Health Page

### Route

`/system`

### Backend

New endpoint: `GET /api/system` returns the full system detail view. Note: `GET /api/status` remains as the lightweight health check (used by the Dashboard health strip); `/api/system` is the comprehensive version for the dedicated System page.

`GET /api/system` returns:

```json
{
  "version": "0.3.0",
  "go_version": "go1.24.1",
  "uptime_seconds": 86400,
  "build_date": "2026-04-03T00:00:00Z",
  "geo_ok": true,
  "geo_checked_at": "2026-04-03T12:00:00Z",
  "ffmpeg_version": "7.1",
  "ffmpeg_path": "/usr/bin/ffmpeg",
  "disk_total": 1099511627776,
  "disk_free": 549755813888,
  "disk_path": "/downloads",
  "downloads_completed": 142,
  "downloads_failed": 4,
  "downloads_total_bytes": 13312344064,
  "last_indexer_request": "2026-04-03T11:45:00Z"
}
```

### Frontend Cards

| Card | Content |
|------|---------|
| BBC iPlayer Status | Geo check result, last check time, "Re-check" button (triggers `POST /api/system/geo-check`) |
| ffmpeg | Version, path |
| Download Stats | Total completed, failed, success rate %, total bytes formatted |
| Storage | Download dir path, free/total space, usage bar |
| Sonarr Integration | Indexer URL, last request timestamp, "Test" button |
| About | Version, Go version, uptime (formatted), build date |

Each card uses the same `.card` component as elsewhere. Status indicators use the health strip colour scheme (green/amber/red dots).

---

## 7. History Improvements

Enhancements to the existing Dashboard history section and Downloads page:

### Filter Bar

Above the history table:
- **Status dropdown**: All / Completed / Failed
- **Date range**: Today / 7 days / 30 days / All
- Filters trigger API re-fetch with query params

### Pagination

- 20 items per page (matches current slice)
- Prev/Next buttons with "Page X of Y"
- Total count displayed: "Showing 20 of 142"

### Stats Row

"138 completed / 4 failed / 12.4 GB total" -- displayed above the table, updates with filters.

### Table Enhancements

- **Delete button** per row (API already exists: `DELETE /api/history/:id`)
- **Clear All History** button with confirmation dialog
- **Sortable columns**: Title, Status, Date -- click header to toggle asc/desc
- Sort state held in component (not persisted)

### Backend Changes

`GET /api/history` updated:
- `?status=completed|failed` -- filter by status
- `?since=2026-04-01` -- date filter
- `?page=1&per_page=20` -- pagination
- `?sort=completed_at&order=desc` -- sorting
- Response shape changes from `Download[]` to `{ items: Download[], total: number }`
- **Breaking change:** Dashboard and any other callers of `GET /api/history` must adapt to the new envelope. Update `api.listHistory()` to unwrap `.items`.

New: `GET /api/history/stats`:
- `{ completed: number, failed: number, total_bytes: number }`
- Accepts same `?since=` filter as history

---

## Scope Summary

### New Pages
- Log Viewer (`/logs`)
- System Health (`/system`)

### New Backend Endpoints
- `GET /api/logs` (ring buffer contents)
- `GET /api/system` (system health data)
- `POST /api/system/geo-check` (trigger geo re-check)
- `GET /api/history/stats` (download statistics)

### New SSE Events
- `log:line` (real-time log streaming)

### Modified Backend Endpoints
- `GET /api/status` gains `disk_free`, `disk_total`, `last_indexer_request` fields
- `GET /api/history` gains pagination, filtering, sorting query params; response becomes `{ items: [], total: number }`

### Frontend Changes
- New colour palette (CSS variables only)
- Collapsible sidebar (new Nav component)
- Mobile hamburger overlay
- Setup wizard modal
- Enhanced Dashboard health strip
- Download speed display
- History filter/pagination/stats/sort
- Log viewer page
- System health page
- All inline styles extracted to CSS

### Not In Scope
- Drag-to-reorder queue (visual placeholder only)
- Light theme toggle
- Notification system (push/email)
- Multi-language / i18n
- User accounts / auth beyond API key
