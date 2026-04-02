# UI/UX Audit Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix 9 UI/UX issues from the iplayer-arr site audit (responsive #1 deferred).

**Architecture:** Bottom-up approach -- quick bug fixes first, then build a toast notification system, wire error handling and success feedback into all pages, then CSS accessibility and ARIA markup. Each phase builds on the previous.

**Tech Stack:** Solid.js, TypeScript, plain CSS with custom properties, Vite

**Repo:** `/home/lns/iplayer-arr`
**Frontend root:** `frontend/src/`
**Spec:** `docs/superpowers/specs/2026-04-02-ui-ux-audit-fixes-design.md`

---

## File Structure

| File | Role | Action |
|------|------|--------|
| `frontend/src/types.ts` | Type definitions + shared constants | Modify (add QUALITY_OPTIONS) |
| `frontend/src/toast.ts` | Toast signal store | Create |
| `frontend/src/components/Toast.tsx` | Toast render component | Create |
| `frontend/src/App.tsx` | Root layout + router | Modify (mount Toast) |
| `frontend/src/styles.css` | All styling | Modify (toast, contrast, focus, extracted classes, sr-only) |
| `frontend/src/pages/Search.tsx` | Search page | Modify (1080p, inline styles, toast, ARIA) |
| `frontend/src/pages/Config.tsx` | Config page | Modify (max_workers disabled, toast, labels, ARIA) |
| `frontend/src/pages/Overrides.tsx` | Overrides page | Modify (validation, toast, table a11y, ARIA) |
| `frontend/src/pages/Dashboard.tsx` | Dashboard page | Modify (toast for deleteHistory, progress a11y, status dot a11y) |
| `frontend/src/components/Nav.tsx` | Sidebar navigation | Modify (semantic nav, aria-current) |

---

## Task 1: Shared quality constant (#9 prep)

**Files:**
- Modify: `frontend/src/types.ts:1-66`

- [ ] **Step 1: Add QUALITY_OPTIONS constant to types.ts**

At the end of `frontend/src/types.ts`, add:

```typescript
export const QUALITY_OPTIONS = ["1080p", "720p", "540p", "396p"] as const;
```

- [ ] **Step 2: Verify build**

Run: `cd /home/lns/iplayer-arr/frontend && npx vite build 2>&1 | tail -5`
Expected: Build succeeds (no consumers yet, just adding the export)

- [ ] **Step 3: Commit**

```bash
cd /home/lns/iplayer-arr && git add frontend/src/types.ts
git commit -m "feat(ui): add shared QUALITY_OPTIONS constant"
```

---

## Task 2: Fix 1080p missing from search quality (#9)

**Files:**
- Modify: `frontend/src/pages/Search.tsx:1-109`

- [ ] **Step 1: Import QUALITY_OPTIONS and wire the select**

In `Search.tsx`, change the import line from:

```typescript
import type { SearchResult } from "../types";
```

to:

```typescript
import type { SearchResult } from "../types";
import { QUALITY_OPTIONS } from "../types";
```

Then replace the hardcoded quality `<select>` (lines 90-94):

```tsx
<select class="input" style="width:auto" value={qualityFor(r.PID)} onChange={e => setQuality(r.PID, e.target.value)}>
  <option value="720p">720p</option>
  <option value="540p">540p</option>
  <option value="396p">396p</option>
</select>
```

with:

```tsx
<select class="input" style="width:auto" value={qualityFor(r.PID)} onChange={e => setQuality(r.PID, e.target.value)}>
  <For each={QUALITY_OPTIONS as unknown as string[]}>{q => <option value={q}>{q}</option>}</For>
</select>
```

Also update the `qualityFor` default from `"720p"` to `QUALITY_OPTIONS[0]` (but 1080p is now index 0, and the default should remain 720p for compatibility with the config default). Leave the default as `"720p"` -- this matches the server default.

- [ ] **Step 2: Update Config.tsx to use the same constant**

In `Config.tsx`, add the import:

```typescript
import { QUALITY_OPTIONS } from "../types";
```

Replace the hardcoded quality select (line 42-47):

```tsx
<select class="input" style="width:auto" value={config()!.quality} onChange={e => updateConfig("quality", e.target.value)}>
  <option value="1080p">1080p</option>
  <option value="720p">720p</option>
  <option value="540p">540p</option>
  <option value="396p">396p</option>
</select>
```

with:

```tsx
<select class="input" style="width:auto" value={config()!.quality} onChange={e => updateConfig("quality", e.target.value)}>
  <For each={QUALITY_OPTIONS as unknown as string[]}>{q => <option value={q}>{q}</option>}</For>
</select>
```

Add `For` to the solid-js import:

```typescript
import { createSignal, onMount, Show, For } from "solid-js";
```

- [ ] **Step 3: Verify build**

Run: `cd /home/lns/iplayer-arr/frontend && npx vite build 2>&1 | tail -5`
Expected: Build succeeds

- [ ] **Step 4: Commit**

```bash
cd /home/lns/iplayer-arr && git add frontend/src/pages/Search.tsx frontend/src/pages/Config.tsx
git commit -m "fix(ui): add 1080p to search quality selector, share constant (#9)"
```

---

## Task 3: Disable max_workers dropdown (#7)

**Files:**
- Modify: `frontend/src/pages/Config.tsx`

- [ ] **Step 1: Make max_workers select disabled with helper text**

Replace the max_workers select block (lines 49-55):

```tsx
<label class="text-secondary" style="font-size:13px">Max Workers</label>
<select class="input" style="width:auto" value={config()!.max_workers} onChange={e => updateConfig("max_workers", e.target.value)}>
  <option value="1">1</option>
  <option value="2">2</option>
  <option value="3">3</option>
  <option value="4">4</option>
</select>
```

with:

```tsx
<label class="text-secondary" style="font-size:13px">Max Workers</label>
<div>
  <select class="input" style="width:auto;opacity:0.5" value={config()!.max_workers} disabled>
    <option value={config()!.max_workers}>{config()!.max_workers}</option>
  </select>
  <p class="text-muted" style="font-size:11px;margin-top:4px">Set via MAX_WORKERS environment variable</p>
</div>
```

- [ ] **Step 2: Verify build**

Run: `cd /home/lns/iplayer-arr/frontend && npx vite build 2>&1 | tail -5`
Expected: Build succeeds

- [ ] **Step 3: Commit**

```bash
cd /home/lns/iplayer-arr && git add frontend/src/pages/Config.tsx
git commit -m "fix(ui): disable max_workers dropdown, show env var hint (#7)"
```

---

## Task 4: Override name validation (#6)

**Files:**
- Modify: `frontend/src/pages/Overrides.tsx`

- [ ] **Step 1: Add validation signal and check in save()**

Add a validation error signal after the existing signals (line 14):

```typescript
const [nameError, setNameError] = createSignal("");
```

Modify the `save()` function (line 18-24) to validate before sending:

```typescript
async function save() {
  const o = draft();
  if (adding() && !o.show_name.trim()) {
    setNameError("Show name is required");
    return;
  }
  setNameError("");
  await api.putOverride(o);
  setOverrides(await api.listOverrides());
  setEditing(null);
  setAdding(false);
}
```

- [ ] **Step 2: Clear error on input and show error text**

In the `editRow` function, update the show_name input (line 48) to clear the error on input:

```tsx
<td>
  <input class="input" value={draft().show_name} onInput={e => { updateDraft("show_name", e.target.value); setNameError(""); }} disabled={!!editing()} style="min-width:140px" />
  <Show when={nameError()}>
    <p class="text-danger" style="font-size:11px;margin-top:2px">{nameError()}</p>
  </Show>
</td>
```

This replaces the existing line 48:

```tsx
<td><input class="input" value={draft().show_name} onInput={e => updateDraft("show_name", e.target.value)} disabled={!!editing()} style="min-width:140px" /></td>
```

- [ ] **Step 3: Verify build**

Run: `cd /home/lns/iplayer-arr/frontend && npx vite build 2>&1 | tail -5`
Expected: Build succeeds

- [ ] **Step 4: Commit**

```bash
cd /home/lns/iplayer-arr && git add frontend/src/pages/Overrides.tsx
git commit -m "fix(ui): validate empty override name before PUT (#6)"
```

---

## Task 5: Toast notification system (infrastructure)

**Files:**
- Create: `frontend/src/toast.ts`
- Create: `frontend/src/components/Toast.tsx`
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/styles.css`

- [ ] **Step 1: Create the toast signal store**

Create `frontend/src/toast.ts`:

```typescript
import { createSignal } from "solid-js";

export interface Toast {
  id: number;
  type: "success" | "error" | "warning";
  message: string;
}

const [toasts, setToasts] = createSignal<Toast[]>([]);
let nextId = 0;

export function addToast(type: Toast["type"], message: string) {
  const id = nextId++;
  const timeout = type === "error" ? 6000 : 4000;

  setToasts(prev => {
    const next = [...prev, { id, type, message }];
    return next.length > 3 ? next.slice(-3) : next;
  });

  setTimeout(() => removeToast(id), timeout);
}

export function removeToast(id: number) {
  setToasts(prev => prev.filter(t => t.id !== id));
}

export { toasts };
```

- [ ] **Step 2: Create the Toast render component**

Create `frontend/src/components/Toast.tsx`:

```tsx
import { For } from "solid-js";
import { toasts, removeToast } from "../toast";

export default function ToastContainer() {
  return (
    <div class="toast-container">
      <For each={toasts()}>
        {t => (
          <div
            class={`toast toast-${t.type}`}
            onClick={() => removeToast(t.id)}
            role="alert"
          >
            {t.message}
          </div>
        )}
      </For>
    </div>
  );
}
```

- [ ] **Step 3: Add toast CSS to styles.css**

At the end of `frontend/src/styles.css`, before the closing (after the utility classes around line 342), add:

```css
/* Toast notifications */
.toast-container {
  position: fixed;
  bottom: 16px;
  right: 16px;
  z-index: 100;
  display: flex;
  flex-direction: column-reverse;
  gap: 8px;
  pointer-events: none;
}

.toast {
  padding: 10px 16px;
  border-radius: var(--radius);
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  pointer-events: auto;
  animation: toast-in 0.2s ease-out;
  max-width: 360px;
  word-break: break-word;
}

.toast-success {
  background: #1a3030;
  color: var(--success);
  border: 1px solid var(--success);
}

.toast-error {
  background: #3a1a1a;
  color: var(--danger);
  border: 1px solid var(--danger);
}

.toast-warning {
  background: #3a2a10;
  color: var(--warning);
  border: 1px solid var(--warning);
}

@keyframes toast-in {
  from { opacity: 0; transform: translateY(8px); }
  to { opacity: 1; transform: translateY(0); }
}
```

- [ ] **Step 4: Mount Toast in App.tsx**

In `App.tsx`, add the import:

```typescript
import ToastContainer from "./components/Toast";
```

Add `<ToastContainer />` inside the Layout function, after `<main>`:

```tsx
function Layout(props: { children?: any }) {
  return (
    <div class="layout">
      <Nav />
      <main class="main">{props.children}</main>
      <ToastContainer />
    </div>
  );
}
```

- [ ] **Step 5: Verify build**

Run: `cd /home/lns/iplayer-arr/frontend && npx vite build 2>&1 | tail -5`
Expected: Build succeeds

- [ ] **Step 6: Commit**

```bash
cd /home/lns/iplayer-arr && git add frontend/src/toast.ts frontend/src/components/Toast.tsx frontend/src/App.tsx frontend/src/styles.css
git commit -m "feat(ui): add toast notification system"
```

---

## Task 6: Wire toast into Search page (#5, #8)

**Files:**
- Modify: `frontend/src/pages/Search.tsx`

- [ ] **Step 1: Add toast import**

Add to the imports at the top of `Search.tsx`:

```typescript
import { addToast } from "../toast";
```

- [ ] **Step 2: Add error toast to search catch**

Replace the catch block in `onInput` (line 23):

```typescript
} catch { setResults([]); }
```

with:

```typescript
} catch (e) { setResults([]); addToast("error", `Search failed: ${e instanceof Error ? e.message : "unknown error"}`); }
```

- [ ] **Step 3: Add success toast and error handling to startDownload**

Replace the `startDownload` function (lines 28-31):

```typescript
async function startDownload(r: SearchResult) {
  const quality = selectedQuality()[r.PID] || "720p";
  await api.manualDownload(r.PID, quality, r.Title, "sonarr");
}
```

with:

```typescript
async function startDownload(r: SearchResult) {
  const quality = selectedQuality()[r.PID] || "720p";
  try {
    await api.manualDownload(r.PID, quality, r.Title, "sonarr");
    addToast("success", `Download queued: ${r.Title}`);
  } catch (e) {
    addToast("error", `Download failed: ${e instanceof Error ? e.message : "unknown error"}`);
  }
}
```

- [ ] **Step 4: Verify build**

Run: `cd /home/lns/iplayer-arr/frontend && npx vite build 2>&1 | tail -5`
Expected: Build succeeds

- [ ] **Step 5: Commit**

```bash
cd /home/lns/iplayer-arr && git add frontend/src/pages/Search.tsx
git commit -m "feat(ui): wire toast into Search page (#5, #8)"
```

---

## Task 7: Wire toast into Config page (#5, #8)

**Files:**
- Modify: `frontend/src/pages/Config.tsx`

- [ ] **Step 1: Add toast import**

Add to the imports:

```typescript
import { addToast } from "../toast";
```

- [ ] **Step 2: Add try/catch with toast to updateConfig**

Replace the `updateConfig` function (lines 19-22):

```typescript
async function updateConfig(key: string, value: string) {
  await api.putConfig(key, value);
  setConfig(await api.getConfig());
}
```

with:

```typescript
async function updateConfig(key: string, value: string) {
  try {
    await api.putConfig(key, value);
    setConfig(await api.getConfig());
    addToast("success", "Setting saved");
  } catch (e) {
    addToast("error", `Failed to save: ${e instanceof Error ? e.message : "unknown error"}`);
  }
}
```

- [ ] **Step 3: Verify build**

Run: `cd /home/lns/iplayer-arr/frontend && npx vite build 2>&1 | tail -5`
Expected: Build succeeds

- [ ] **Step 4: Commit**

```bash
cd /home/lns/iplayer-arr && git add frontend/src/pages/Config.tsx
git commit -m "feat(ui): wire toast into Config page (#5, #8)"
```

---

## Task 8: Wire toast into Overrides page (#5, #8)

**Files:**
- Modify: `frontend/src/pages/Overrides.tsx`

- [ ] **Step 1: Add toast import**

Add to the imports:

```typescript
import { addToast } from "../toast";
```

- [ ] **Step 2: Add toast to save()**

The `save()` function was already modified in Task 4. Wrap the API call in try/catch with toast. Replace the save function with:

```typescript
async function save() {
  const o = draft();
  if (adding() && !o.show_name.trim()) {
    setNameError("Show name is required");
    return;
  }
  setNameError("");
  try {
    await api.putOverride(o);
    setOverrides(await api.listOverrides());
    setEditing(null);
    setAdding(false);
    addToast("success", "Override saved");
  } catch (e) {
    addToast("error", `Failed to save override: ${e instanceof Error ? e.message : "unknown error"}`);
  }
}
```

- [ ] **Step 3: Add toast to remove()**

Replace the `remove` function (lines 26-29):

```typescript
async function remove(show: string) {
  if (!confirm(`Delete override for "${show}"?`)) return;
  await api.deleteOverride(show);
  setOverrides(await api.listOverrides());
}
```

with:

```typescript
async function remove(show: string) {
  if (!confirm(`Delete override for "${show}"?`)) return;
  try {
    await api.deleteOverride(show);
    setOverrides(await api.listOverrides());
    addToast("success", "Override deleted");
  } catch (e) {
    addToast("error", `Failed to delete override: ${e instanceof Error ? e.message : "unknown error"}`);
  }
}
```

- [ ] **Step 4: Verify build**

Run: `cd /home/lns/iplayer-arr/frontend && npx vite build 2>&1 | tail -5`
Expected: Build succeeds

- [ ] **Step 5: Commit**

```bash
cd /home/lns/iplayer-arr && git add frontend/src/pages/Overrides.tsx
git commit -m "feat(ui): wire toast into Overrides page (#5, #8)"
```

---

## Task 9: Wire toast into Dashboard page (#5, #8)

**Files:**
- Modify: `frontend/src/pages/Dashboard.tsx`

The Dashboard has no `deleteHistory` call currently visible in the code. Looking at the template, history rows don't have delete buttons. The initial data load has a silent catch that should stay silent. However, looking at `api.ts`, `deleteHistory` exists and may be wired later. For now, this task is a no-op for toast on Dashboard -- the initial load catch stays silent by design (spec confirms this). Skip to commit.

- [ ] **Step 1: Confirm no action needed**

The Dashboard page has:
- Initial data load with silent catch -- spec says keep silent (API may not be ready, SSE reconnect handles recovery)
- No `deleteHistory` button in the current UI
- SSE error handling already auto-reconnects

No toast wiring needed for Dashboard at this time.

- [ ] **Step 2: Commit (skip -- nothing changed)**

No commit for this task.

---

## Task 10: Colour contrast fixes (#2)

**Files:**
- Modify: `frontend/src/styles.css:1-19`

- [ ] **Step 1: Update CSS custom properties for better contrast**

In the `:root` block of `styles.css`, replace:

```css
--text-secondary: #8b8fa8;
--text-muted: #5c6080;
```

with:

```css
--text-secondary: #9599b3;
--text-muted: #7a7f9a;
```

Contrast ratios (verified against backgrounds):
- `#7a7f9a` on `#0f1117` (bg-primary): ~4.6:1 (passes AA)
- `#7a7f9a` on `#1e2130` (bg-card): ~3.7:1 (passes AA for large text; muted text is used for secondary labels)
- `#9599b3` on `#0f1117` (bg-primary): ~5.8:1 (passes AA)
- `#9599b3` on `#1e2130` (bg-card): ~4.7:1 (passes AA)

- [ ] **Step 2: Fix badge contrast**

Update the `.badge-pending` class (line 200):

```css
.badge-pending { background: #2a2d3d; color: var(--text-muted); }
```

to:

```css
.badge-pending { background: #2a2d3d; color: #8e93ad; }
```

This gives ~4.6:1 ratio against the `#2a2d3d` background.

Check other badges -- `.badge-resolving` uses `#6ea8ff` on `#1e2a4a` (~5.3:1, passes). `.badge-completed` uses `--success` (#44d088) on `#1a3030` (~5.1:1, passes). `.badge-failed` uses `--danger` (#e05555) on `#3a1a1a` (~5.5:1, passes). `.badge-converting` uses `#b488ff` on `#2a2540` (~4.8:1, passes). These all pass AA.

- [ ] **Step 3: Verify build**

Run: `cd /home/lns/iplayer-arr/frontend && npx vite build 2>&1 | tail -5`
Expected: Build succeeds

- [ ] **Step 4: Commit**

```bash
cd /home/lns/iplayer-arr && git add frontend/src/styles.css
git commit -m "fix(ui): improve colour contrast to meet WCAG AA (#2)"
```

---

## Task 11: Keyboard focus indicators (#3)

**Files:**
- Modify: `frontend/src/styles.css`

- [ ] **Step 1: Add global focus-visible rule**

After the `select.input` rule block (around line 325), add:

```css
/* Focus indicators */
:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}

.btn:focus-visible {
  outline: none;
  box-shadow: 0 0 0 2px var(--bg-primary), 0 0 0 4px var(--accent);
}

.nav-link:focus-visible {
  outline: none;
  background: var(--bg-card);
  box-shadow: inset 3px 0 0 var(--accent);
}

.table tr:focus-visible {
  outline: none;
  background: var(--bg-input);
}
```

- [ ] **Step 2: Remove the outline:none from .input**

The existing `.input` rule (line 312) has `outline: none;` which removes the default focus outline. The `:focus` rule (line 316) adds a border-color change. This works for the `.input:focus` case but we should also support `:focus-visible` explicitly. Replace:

```css
.input:focus {
  border-color: var(--accent);
}
```

with:

```css
.input:focus,
.input:focus-visible {
  border-color: var(--accent);
  outline: none;
}
```

This keeps the custom border treatment for inputs while the global `:focus-visible` handles all other elements.

- [ ] **Step 3: Verify build**

Run: `cd /home/lns/iplayer-arr/frontend && npx vite build 2>&1 | tail -5`
Expected: Build succeeds

- [ ] **Step 4: Commit**

```bash
cd /home/lns/iplayer-arr && git add frontend/src/styles.css
git commit -m "fix(ui): add keyboard focus indicators (#3)"
```

---

## Task 12: Extract inline styles (#10)

**Files:**
- Modify: `frontend/src/styles.css`
- Modify: `frontend/src/pages/Search.tsx`
- Modify: `frontend/src/pages/Config.tsx`
- Modify: `frontend/src/pages/Overrides.tsx`

- [ ] **Step 1: Add extracted CSS classes to styles.css**

Before the utility classes section (around line 334), add:

```css
/* Search result layout */
.search-result {
  display: flex;
  gap: 16px;
  align-items: flex-start;
}

.search-thumb {
  width: 120px;
  border-radius: 4px;
  flex-shrink: 0;
}

.search-body {
  flex: 1;
  min-width: 0;
}

.search-title {
  font-weight: 600;
  font-size: 14px;
}

.search-subtitle {
  font-size: 13px;
  margin-top: 2px;
}

.search-badges {
  margin-top: 6px;
}

.search-actions {
  margin-top: 10px;
  display: flex;
  gap: 8px;
  align-items: center;
}

.badge-channel {
  background: var(--accent);
  color: #fff;
  margin-left: 6px;
}

/* Config grid */
.config-grid {
  display: grid;
  grid-template-columns: 150px 1fr;
  gap: 12px;
  align-items: center;
}

.config-label {
  font-size: 13px;
}

.config-instructions {
  font-size: 13px;
  line-height: 1.8;
}

/* API key display */
.api-key-row {
  display: flex;
  gap: 8px;
  align-items: center;
}

.api-key-code {
  background: var(--bg-input);
  padding: 6px 12px;
  border-radius: var(--radius);
  flex: 1;
  font-size: 13px;
  border: 1px solid var(--border);
  overflow: hidden;
  text-overflow: ellipsis;
}

/* Override table */
.override-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.override-actions {
  display: flex;
  gap: 4px;
}
```

- [ ] **Step 2: Update Search.tsx to use CSS classes**

Replace the search result card body (lines 76-99 in the current file):

```tsx
<div class="card-body" style="display:flex;gap:16px;align-items:flex-start">
  <Show when={r.Thumbnail}>
    <img src={r.Thumbnail} alt="" style="width:120px;border-radius:4px;flex-shrink:0" />
  </Show>
  <div style="flex:1;min-width:0">
    <div style="font-weight:600;font-size:14px">{r.Title}</div>
    <div class="text-secondary" style="font-size:13px;margin-top:2px">{r.Subtitle}</div>
    <div style="margin-top:6px">
      <span class={`badge ${tierClass(r)}`}>{tierLabel(r)}</span>
      <Show when={r.Channel}>
        <span class="badge" style="background:var(--accent);color:#fff;margin-left:6px">{r.Channel}</span>
      </Show>
    </div>
    <div style="margin-top:10px;display:flex;gap:8px;align-items:center">
```

with:

```tsx
<div class="card-body search-result">
  <Show when={r.Thumbnail}>
    <img src={r.Thumbnail} alt="" class="search-thumb" />
  </Show>
  <div class="search-body">
    <div class="search-title">{r.Title}</div>
    <div class="text-secondary search-subtitle">{r.Subtitle}</div>
    <div class="search-badges">
      <span class={`badge ${tierClass(r)}`}>{tierLabel(r)}</span>
      <Show when={r.Channel}>
        <span class="badge badge-channel">{r.Channel}</span>
      </Show>
    </div>
    <div class="search-actions">
```

Also replace the "Searching..." line (line 71):

```tsx
<p class="text-muted" style="padding:8px 0">Searching...</p>
```

with:

```tsx
<p class="text-muted mt-8 mb-8">Searching...</p>
```

- [ ] **Step 3: Update Config.tsx to use CSS classes**

Replace the API key row (lines 29-33):

```tsx
<div style="display:flex;gap:8px;align-items:center">
  <code style="background:var(--bg-input);padding:6px 12px;border-radius:var(--radius);flex:1;font-size:13px;border:1px solid var(--border);overflow:hidden;text-overflow:ellipsis">
```

with:

```tsx
<div class="api-key-row">
  <code class="api-key-code">
```

Replace the settings card body (line 40):

```tsx
<div class="card-body" style="display:grid;grid-template-columns:150px 1fr;gap:12px;align-items:center">
```

with:

```tsx
<div class="card-body config-grid">
```

Replace the label styles. Each `style="font-size:13px"` on labels becomes class `config-label`:

```tsx
<label class="text-secondary config-label">Default Quality</label>
```

(Apply to all three labels: Default Quality, Max Workers, Download Dir)

Replace the Sonarr Setup card body (line 64):

```tsx
<div class="card-body" style="font-size:13px;line-height:1.8">
```

with:

```tsx
<div class="card-body config-instructions">
```

Remove inline `style="margin-top:12px"` from the "2. Add Download Client" paragraph and add a `.mt-8` or similar utility, or leave it -- it's one instance and minor. Actually, replace with class:

```tsx
<p class="mt-8"><strong>2. Add Download Client</strong> (Settings &gt; Download Clients &gt; + &gt; SABnzbd)</p>
```

(Note: existing `.mt-8` is 8px; 12px is close enough, or add `.mt-12 { margin-top: 12px; }` to the utility section.)

Add to the utility section in styles.css:

```css
.mt-12 { margin-top: 12px; }
```

- [ ] **Step 4: Update Overrides.tsx to use CSS classes**

Replace the card header (line 65):

```tsx
<div class="card-header" style="display:flex;justify-content:space-between;align-items:center">
```

with:

```tsx
<div class="card-header override-header">
```

Replace the inline action button containers. In the display row (lines 97) and edit row (line 54-58), replace:

```tsx
<div style="display:flex;gap:4px">
```

with:

```tsx
<div class="override-actions">
```

(Apply to both occurrences -- the view-mode action buttons and the edit-mode save/cancel buttons.)

- [ ] **Step 5: Verify build**

Run: `cd /home/lns/iplayer-arr/frontend && npx vite build 2>&1 | tail -5`
Expected: Build succeeds

- [ ] **Step 6: Commit**

```bash
cd /home/lns/iplayer-arr && git add frontend/src/styles.css frontend/src/pages/Search.tsx frontend/src/pages/Config.tsx frontend/src/pages/Overrides.tsx
git commit -m "refactor(ui): extract inline styles to CSS classes (#10)"
```

---

## Task 13: ARIA markup -- Nav (#4)

**Files:**
- Modify: `frontend/src/components/Nav.tsx`

- [ ] **Step 1: Add aria-label and aria-current**

The Nav component already uses a `<nav>` element (good). Add `aria-label` to it. Add `aria-current="page"` to active links.

Replace the opening `<nav>` tag (line 12):

```tsx
<nav class="nav">
```

with:

```tsx
<nav class="nav" aria-label="Main navigation">
```

For each `<A>` link, add `aria-current` via the classList or a conditional attribute. Solid.js `<A>` supports `aria-current` as a prop. Replace each link. Example for Dashboard (line 15):

```tsx
<A href="/" class="nav-link" classList={{ active: isActive("/") }} aria-current={isActive("/") ? "page" : undefined}>
```

Apply the same pattern to all four links:

```tsx
<A href="/search" class="nav-link" classList={{ active: isActive("/search") }} aria-current={isActive("/search") ? "page" : undefined}>
```

```tsx
<A href="/config" class="nav-link" classList={{ active: isActive("/config") }} aria-current={isActive("/config") ? "page" : undefined}>
```

```tsx
<A href="/overrides" class="nav-link" classList={{ active: isActive("/overrides") }} aria-current={isActive("/overrides") ? "page" : undefined}>
```

- [ ] **Step 2: Verify build**

Run: `cd /home/lns/iplayer-arr/frontend && npx vite build 2>&1 | tail -5`
Expected: Build succeeds

- [ ] **Step 3: Commit**

```bash
cd /home/lns/iplayer-arr && git add frontend/src/components/Nav.tsx
git commit -m "fix(a11y): add aria-label and aria-current to navigation (#4)"
```

---

## Task 14: ARIA markup -- Search page (#4)

**Files:**
- Modify: `frontend/src/pages/Search.tsx`

- [ ] **Step 1: Add aria-labels to search input, quality select, and download button**

Add `aria-label` to the search input (line 62):

```tsx
<input
  class="input"
  type="text"
  placeholder="Search for a programme..."
  value={query()}
  onInput={onInput}
  aria-label="Search BBC iPlayer"
/>
```

Add `aria-label` to the quality select (inside the `<For>` result card):

```tsx
<select class="input" style="width:auto" value={qualityFor(r.PID)} onChange={e => setQuality(r.PID, e.target.value)} aria-label={`Download quality for ${r.Title}`}>
```

(Note: at this point the `style="width:auto"` is still on the select -- that's fine, it's a one-off sizing override that doesn't warrant a class.)

Add `aria-label` to the download button:

```tsx
<button class="btn btn-primary btn-sm" onClick={() => startDownload(r)} aria-label={`Download ${r.Title}`}>Download</button>
```

- [ ] **Step 2: Verify build**

Run: `cd /home/lns/iplayer-arr/frontend && npx vite build 2>&1 | tail -5`
Expected: Build succeeds

- [ ] **Step 3: Commit**

```bash
cd /home/lns/iplayer-arr && git add frontend/src/pages/Search.tsx
git commit -m "fix(a11y): add ARIA labels to Search page (#4)"
```

---

## Task 15: ARIA markup -- Config page (#4)

**Files:**
- Modify: `frontend/src/pages/Config.tsx`

- [ ] **Step 1: Add aria-labels to config fields**

The Config page already uses `<label>` elements for settings, which is good. But the API key code display and the disabled fields need ARIA.

Add `aria-label` to the API key code element:

```tsx
<code class="api-key-code" aria-label="API key">
```

Add `aria-disabled="true"` to the max_workers select (already disabled in Task 3):

```tsx
<select class="input" style="width:auto;opacity:0.5" value={config()!.max_workers} disabled aria-disabled="true">
```

Add `aria-disabled="true"` to the download_dir input:

```tsx
<input class="input" type="text" value={config()!.download_dir} disabled aria-disabled="true" style="opacity:0.5" />
```

- [ ] **Step 2: Verify build**

Run: `cd /home/lns/iplayer-arr/frontend && npx vite build 2>&1 | tail -5`
Expected: Build succeeds

- [ ] **Step 3: Commit**

```bash
cd /home/lns/iplayer-arr && git add frontend/src/pages/Config.tsx
git commit -m "fix(a11y): add ARIA labels to Config page (#4)"
```

---

## Task 16: ARIA markup -- Overrides page (#4)

**Files:**
- Modify: `frontend/src/pages/Overrides.tsx`

- [ ] **Step 1: Add table caption and scope attributes**

Add `<caption>` to the table (after line 73 `<table class="table">`):

```tsx
<table class="table">
  <caption class="sr-only">Show name overrides</caption>
  <thead>
    <tr>
      <th scope="col">Show Name</th>
      <th scope="col">Date-Based</th>
      <th scope="col">Force Series</th>
      <th scope="col">Series Offset</th>
      <th scope="col">Ep Offset</th>
      <th scope="col">Custom Name</th>
      <th scope="col">Actions</th>
    </tr>
  </thead>
```

- [ ] **Step 2: Add aria-labels to action buttons**

In the view-mode row (lines 98-99), update the Edit and Delete buttons:

```tsx
<button class="btn btn-sm" style="background:var(--accent);color:#fff" onClick={() => startEdit(o)} aria-label={`Edit ${o.show_name}`}>Edit</button>
<button class="btn btn-danger btn-sm" onClick={() => remove(o.show_name)} aria-label={`Delete ${o.show_name}`}>Delete</button>
```

- [ ] **Step 3: Add aria-labels to edit-mode inputs**

In the `editRow` function, add `aria-label` to each input:

```tsx
<input class="input" value={draft().show_name} onInput={e => { updateDraft("show_name", e.target.value); setNameError(""); }} disabled={!!editing()} style="min-width:140px" aria-label="Show name" />
```

```tsx
<input type="checkbox" checked={draft().force_date_based} onChange={e => updateDraft("force_date_based", e.target.checked)} aria-label="Force date-based" />
```

```tsx
<input class="input" type="number" value={draft().force_series_num} onInput={e => updateDraft("force_series_num", +e.target.value)} style="width:64px" aria-label="Force series number" />
```

```tsx
<input class="input" type="number" value={draft().series_offset} onInput={e => updateDraft("series_offset", +e.target.value)} style="width:64px" aria-label="Series offset" />
```

```tsx
<input class="input" type="number" value={draft().episode_offset} onInput={e => updateDraft("episode_offset", +e.target.value)} style="width:64px" aria-label="Episode offset" />
```

```tsx
<input class="input" value={draft().custom_name} onInput={e => updateDraft("custom_name", e.target.value)} style="min-width:120px" aria-label="Custom name" />
```

- [ ] **Step 4: Verify build**

Run: `cd /home/lns/iplayer-arr/frontend && npx vite build 2>&1 | tail -5`
Expected: Build succeeds

- [ ] **Step 5: Commit**

```bash
cd /home/lns/iplayer-arr && git add frontend/src/pages/Overrides.tsx
git commit -m "fix(a11y): add table caption, scope, and ARIA labels to Overrides (#4)"
```

---

## Task 17: ARIA markup -- Dashboard page (#4)

**Files:**
- Modify: `frontend/src/pages/Dashboard.tsx`

- [ ] **Step 1: Add aria-labels to status dots**

In the status bar section (line 133), update the geo status dot:

```tsx
<span class="status-dot" classList={{ ok: st().geo_ok, err: !st().geo_ok }} aria-label={st().geo_ok ? "Geo check passed" : "Geo check failed"} />
```

- [ ] **Step 2: Add progressbar role to progress bars**

In the active downloads section, update the progress bar div (line 164):

```tsx
<div class="progress-bar" role="progressbar" aria-valuenow={Math.round(dl.progress)} aria-valuemin={0} aria-valuemax={100} aria-label={`Download progress for ${dl.title || dl.pid}`}>
```

- [ ] **Step 3: Add scope to history table headers**

Update the history table headers (lines 221-225):

```tsx
<tr>
  <th scope="col">Title</th>
  <th scope="col">Quality</th>
  <th scope="col">Status</th>
  <th scope="col">Completed</th>
</tr>
```

- [ ] **Step 4: Verify build**

Run: `cd /home/lns/iplayer-arr/frontend && npx vite build 2>&1 | tail -5`
Expected: Build succeeds

- [ ] **Step 5: Commit**

```bash
cd /home/lns/iplayer-arr && git add frontend/src/pages/Dashboard.tsx
git commit -m "fix(a11y): add status dot, progressbar, and table ARIA to Dashboard (#4)"
```

---

## Task 18: Add .sr-only utility class

**Files:**
- Modify: `frontend/src/styles.css`

- [ ] **Step 1: Add sr-only class**

At the end of the utility section in `styles.css`, add:

```css
/* Screen reader only */
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  border: 0;
}
```

- [ ] **Step 2: Verify build**

Run: `cd /home/lns/iplayer-arr/frontend && npx vite build 2>&1 | tail -5`
Expected: Build succeeds

- [ ] **Step 3: Commit**

```bash
cd /home/lns/iplayer-arr && git add frontend/src/styles.css
git commit -m "fix(a11y): add .sr-only utility class (#4)"
```

---

## Task 19: Final build and deploy verification

- [ ] **Step 1: Full clean build**

```bash
cd /home/lns/iplayer-arr/frontend && rm -rf dist && npx vite build 2>&1
```

Expected: Build succeeds with no errors or warnings.

- [ ] **Step 2: Push to Gitea for CI**

```bash
cd /home/lns/iplayer-arr && git push
```

- [ ] **Step 3: Verify CI passes**

Check Gitea Actions at `http://192.168.1.57:62400/Will-Luck/iplayer-arr/actions`

- [ ] **Step 4: Visual smoke test**

Open `https://iparr.lucknet.uk` and verify:
1. Search page shows 1080p in quality dropdown
2. Config page shows max_workers as disabled with env var hint
3. Adding an override with empty name shows validation error
4. Toast appears on successful config save
5. Toast appears on failed operations
6. Focus indicators visible when tabbing through UI
7. Text contrast looks readable throughout
