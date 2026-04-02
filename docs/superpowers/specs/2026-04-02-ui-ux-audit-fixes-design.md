# iplayer-arr UI/UX Audit Fixes

**Date:** 2026-04-02
**Scope:** 9 issues from site audit (responsive/mobile #1 deferred)
**Approach:** Bottom-up -- bug fixes, then shared infrastructure, then CSS/a11y polish

## Context

The frontend is a Solid.js SPA (~823 lines across 8 files) with a single `styles.css` using CSS custom properties as a design system. No existing toast/notification infrastructure. No responsive breakpoints (mobile deferred by design).

**Files in scope:**
- `frontend/src/styles.css` (343 lines) -- all CSS changes
- `frontend/src/pages/Search.tsx` (110 lines)
- `frontend/src/pages/Config.tsx` (76 lines)
- `frontend/src/pages/Overrides.tsx` (115 lines)
- `frontend/src/pages/Dashboard.tsx` (249 lines)
- `frontend/src/components/Nav.tsx` (49 lines)
- `frontend/src/types.ts` (67 lines) -- shared quality constant
- **New:** `frontend/src/components/Toast.tsx` -- toast notification component
- **New:** `frontend/src/toast.ts` -- toast signal store

## Phase 1: Bug Fixes (#6, #7, #9)

### #6 -- Empty override name causes 404

**Problem:** `Overrides.tsx` sends PUT to `/api/overrides/${encodeURIComponent(o.show_name)}`. Empty `show_name` produces `/api/overrides/` which misroutes to the list endpoint.

**Fix:** Client-side validation before the PUT. If `show_name` is empty or whitespace-only, show inline red error text below the field and block the request. Clear the error when the user starts typing.

### #7 -- Max Workers dropdown editable but read-only

**Problem:** The backend marks `max_workers` as read-only (set via `MAX_WORKERS` env var). The frontend renders it as an editable `<select>` that silently fails on change.

**Fix:** Render `max_workers` as a disabled `<select>` with a helper note: "Set via MAX_WORKERS environment variable". Matches the existing pattern for `download_dir` which is already disabled.

### #9 -- 1080p missing from search quality selector

**Problem:** Config page offers 1080p/720p/540p/396p. Search page per-result selector only offers 720p/540p/396p.

**Fix:** Add "1080p" as the first option in the Search page selector. Extract quality options into a shared constant in `types.ts`:
```typescript
export const QUALITY_OPTIONS = ["1080p", "720p", "540p", "396p"] as const;
```

Both Config.tsx and Search.tsx import from this constant.

## Phase 2: Toast/Notification System

### Architecture

Module-level Solid.js signals (no context/provider needed for a single SPA).

**`toast.ts`** -- signal store:
- `createSignal<Toast[]>` holding active toasts
- `addToast(type: 'success' | 'error' | 'warning', message: string)` -- adds toast, schedules auto-removal
- Auto-dismiss: 4s for success/warning, 6s for errors
- Max 3 visible (oldest evicted when exceeded)
- Each toast gets a unique ID for removal

**`Toast.tsx`** -- render component:
- Fixed position: bottom-right, `z-index: 100` (above sidebar's `z-index: 10`)
- `flex-direction: column-reverse` with `gap: 8px`
- Three variants using existing CSS variables: `--success`, `--danger`, `--warning`
- Click to dismiss
- CSS `@keyframes` fade-in animation
- Mounted in `App.tsx` alongside the router

**Toast interface:**
```typescript
interface Toast {
  id: number;
  type: 'success' | 'error' | 'warning';
  message: string;
}
```

### Why module-level signals

Pages need to call `addToast` after API operations. With context, `api.ts` or non-component code cannot access the toast. Module-level signals are reactive singletons in Solid.js -- any component importing `toasts()` will re-render, and any code can call `addToast()`.

## Phase 3: API Error Handling + Action Feedback (#5, #8)

### #5 -- Error handling on API calls

Toast wiring per page. Errors call `addToast('error', <contextual message>)`.

| Page | Call | Current handling | Fix |
|------|------|-----------------|-----|
| Dashboard | `getStatus`, `listDownloads`, `listHistory` (initial load) | Silent catch | **Keep silent** -- API may not be ready, SSE reconnect handles recovery |
| Dashboard | `deleteHistory` | Silent catch | Toast error |
| Search | `search` | Silent catch | Toast error + empty-state message for zero results |
| Config | `putConfig` | Silent catch | Toast error |
| Overrides | `listOverrides` (initial load) | Silent catch | **Keep silent** -- same reasoning as Dashboard |
| Overrides | `putOverride` | Silent catch | Toast error |
| Overrides | `deleteOverride` | Silent catch | Toast error |

### #8 -- Success feedback

| Action | Toast |
|--------|-------|
| Download initiated (Search) | `success`: "Download queued: {title}" |
| Config setting saved | `success`: "Setting saved" |
| Override saved | `success`: "Override saved" |
| Override deleted | `success`: "Override deleted" |
| History item deleted | `success`: "History cleared" |
| API key copied (Config) | **No change** -- existing "Copied!" button text works well |

### Implementation approach

Toast calls go in page components, not in `api.ts`. The page knows what the user was trying to do and can provide a contextual message ("Failed to save override" vs generic "PUT failed").

## Phase 4: CSS Accessibility (#2, #3, #10)

### #2 -- Colour contrast (WCAG AA)

Adjust CSS custom properties to meet 4.5:1 ratio for normal text, 3:1 for large text.

| Variable | Current | Issue | Fix |
|----------|---------|-------|-----|
| `--text-muted` | `#5c6080` | ~3.1:1 on `--bg-primary` | Bump to ~`#7a7f9a` |
| `--text-secondary` | `#8b8fa8` | ~3.8:1 on `--bg-card` | Bump to ~`#9599b3` |
| Badge text/bg pairs | Various | Some fail AA | Check each `.badge-*` class; if text-on-background ratio < 4.5:1, lighten the text or darken the background |

Variables-only change for `--text-muted` and `--text-secondary` -- all consuming elements inherit the fix. Badge classes may need per-class adjustments since they use unique background colours. All final values verified with contrast checker during implementation.

### #3 -- Keyboard focus indicators

Global `:focus-visible` rule:
```css
:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}
```

Element-specific overrides:
- **Buttons:** `box-shadow` instead of outline (respects `border-radius`)
- **Nav links:** accent left border or background highlight on focus
- **Table rows:** subtle background shift

Uses `:focus-visible` (not `:focus`) so mouse clicks don't trigger outlines.

### #10 -- Inline styles and CSS architecture

Extract all `style=` attributes to named classes in `styles.css`:
- Search.tsx flex layouts become `.search-result-row`, `.search-meta`, etc.
- Any other inline styles found during implementation

No visual change -- purely moving styles to the stylesheet.

## Phase 5: Accessibility Markup (#4)

### Search.tsx
- Search input: `aria-label="Search BBC iPlayer"`
- Quality select per result: `aria-label="Download quality for {title}"`
- Download button: `aria-label="Download {title}"`

### Config.tsx
- Wrap each field in `<label>` elements associating text with input
- API key display: `aria-label="API key"`
- Disabled fields: `aria-disabled="true"` alongside `disabled`

### Overrides.tsx
- Table: add `<caption>Show name overrides</caption>`
- Table headers: `scope="col"`
- Edit/delete buttons: `aria-label="Edit {show_name}"` / `aria-label="Delete {show_name}"`
- Edit-mode inputs: `aria-label` associated with column meaning

### Dashboard.tsx
- Status dots: `aria-label` with status text (colour-only meaning otherwise)
- Progress bars: `role="progressbar"`, `aria-valuenow`, `aria-valuemin="0"`, `aria-valuemax="100"`
- Action buttons: `aria-label` with download title context

### Nav.tsx
- Change wrapper from `<div>` to `<nav aria-label="Main navigation">`
- Active link: add `aria-current="page"`

### Global (styles.css)
Add `.sr-only` utility:
```css
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

## Deferred

- **#1 -- Responsive/mobile sidebar:** Deferred by user decision. Can be revisited as a separate spec.

## File Change Summary

| File | Changes |
|------|---------|
| `frontend/src/types.ts` | Add `QUALITY_OPTIONS` constant |
| `frontend/src/toast.ts` | **New** -- toast signal store |
| `frontend/src/components/Toast.tsx` | **New** -- toast render component |
| `frontend/src/App.tsx` | Mount `<Toast />` component |
| `frontend/src/styles.css` | Contrast variables, focus-visible, toast styles, extracted classes, `.sr-only` |
| `frontend/src/pages/Search.tsx` | 1080p option, quality constant, toast wiring, ARIA, extract inline styles |
| `frontend/src/pages/Config.tsx` | Disable max_workers, toast wiring, labels, ARIA |
| `frontend/src/pages/Overrides.tsx` | Empty name validation, toast wiring, table a11y, ARIA |
| `frontend/src/pages/Dashboard.tsx` | Toast wiring for deleteHistory, progress bar a11y, status dot a11y |
| `frontend/src/components/Nav.tsx` | `<nav>` semantic element, `aria-current="page"` |
