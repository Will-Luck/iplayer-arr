# Changelog

All notable changes to iplayer-arr will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Cancel button on active and queued downloads ([#27](https://github.com/Will-Luck/iplayer-arr/issues/27)).** The dashboard's Active downloads and Queue cards now show a red close icon next to each row, gated by a confirmation dialog. Click it and the worker context is cancelled (so any in-flight `ffmpeg` exits cleanly), the row is moved straight to history, and a toast confirms the cancel. Backed by a new `DELETE /api/downloads/:id` route on the internal API; the existing SABnzbd `mode=queue&name=delete` path Sonarr uses is unchanged.

### Changed

- **Subtitle sidecar filenames now include the `.en` language tag (#38)**: BBC TTML captions are written as `<title>.en.srt` instead of `<title>.srt`. Plex and Jellyfin do pick up untagged `.srt` files but label them as "Unknown" language until you set it manually; the explicit code matches the [documented convention](https://support.plex.tv/articles/200471133-adding-local-subtitles-to-your-media/) and lets the player tag the track as English on first scan. BBC iPlayer only ships English captions on the captions CDN, so a fixed code is safe.

- **`Default quality` config setting now caps what the indexer advertises to Sonarr ([#28](https://github.com/Will-Luck/iplayer-arr/issues/28)).** The setting was previously persisted but never read by the download or indexer path, so a value of `720p` had no effect on Sonarr-driven downloads. It now acts as a ceiling on the RSS `tvsearch` output: probed heights above the configured cap are dropped before `heightsToTags` runs, and the no-probe `[720p, 540p]` fallback is clamped too. The UI label has been renamed to **Maximum quality** with an explanatory hint, and a new `any` option opts out of the cap entirely. The store default ships as `any` so existing installs see no change until the operator explicitly picks a ceiling.

- **Downloads stage in an `incomplete/` subdirectory and atomic-move on completion ([#29](https://github.com/Will-Luck/iplayer-arr/issues/29)).** `ffmpeg` now writes to `<downloadDir>/incomplete/<safeTitle>/` and the worker performs an `os.Rename` to `<downloadDir>/<safeTitle>/` only once probe, reconcile and subtitles all succeed. The SABnzbd history `storage`/`path` field reports the final path. Watch-folder import flows will no longer see partial `.mp4` files mid-download. The dashboard's Downloads page hides the `incomplete/` directory from its folder listing.

## [1.3.0] - 2026-05-07

### Added

- **Editorial-pool merging in RSS sync.** `t=tvsearch` and `t=search` requests with no query now fan out across BBC's `/groups/popular` (~150 items) and `/groups/m001bm54` ("New & Trending", ~45 items) editorial pools alongside the existing `q="BBC"` browse, deduped by PID and capped to 50 unique PIDs per response (×2 qualities = 100 items, matching the advertised Newznab cap). Sonarr's RSS sync now picks up high-profile and popular new drops without waiting for the per-show scheduled search. Per-show searches and tvdbid-resolved searches are unchanged. Probe phase is bounded by a 5s deadline derived from the request context to stay inside Sonarr's 30s budget. Pool failures are fail-soft per-pool (logged via `slog.Warn`, partial results still emit).

### Fixed

- **Country-tag disambiguation in name matching ([#37](https://github.com/Will-Luck/iplayer-arr/issues/37)).** TVDB / Skyhook returns titles like `The Apprentice (UK)` to disambiguate same-named shows across territories, but BBC programme names are bare (`The Apprentice`). The `nameMatches` comparison filtered every BBC episode out, leaving Sonarr with no results. `bareName` now strips trailing `(UK)`, `(US)`, `(AU)`, `(CA)`, `(NZ)`, `(IE)` country tags alongside the existing year-suffix strip. Non-country two-letter parens like `(XY)` are preserved.

## [1.2.0] - 2026-05-04

### Added

- **Tailwind v4 + Kobalte design system.** Token block lives in `frontend/src/styles.css` under `@theme` (page, surface, elevated, raised, accent, success, warning, danger, info, text-primary/secondary/tertiary, border, border-subtle). Vite plugin: `@tailwindcss/vite`. Legacy `:root` aliases continue to point to the new tokens so any unmigrated CSS keeps rendering.
- **12 shared UI primitives in `frontend/src/ui/`:** `Card` (Header / Body / Toolbar / Footer), `Table` (THead / TBody / TR / TH / TD with sortable headers and `collapse=card` mobile mode), `Badge` (completed / imported / failed / pending / neutral), `Button` (primary / secondary / ghost / danger / warning / link, sm / md, loading state), `IconButton` (default / danger / primary tone), `Progress` (default / paused / failed variants), `EmptyState`, `Pagination`, `Select` (Kobalte), `Dialog` + `Dialog.Confirm` (Kobalte), `ToastViewport`, plus `icons.tsx` with 15 Lucide-style SVG renderers.

### Changed

- **Dashboard rebuilt on the new primitives.** History card now uses the `Table` primitive's per-column `width` prop, Trash becomes a `tone=danger` `IconButton`, Completed becomes a `Badge`, and dates render via a UK-locale formatter, fixing the cramped Completed column and the off-centre date wrap from v1.1.x. Active downloads gain the new `Progress` bar; Queue and History each switch to mobile-friendly `collapse=card` table mode.
- **Search** results become horizontal cards with a semantic tier `Badge` (S/E green, Position blue, AirDate yellow, Manual grey), Channel pill, and an action row (`Select` quality + `Button` download) that aligns right at `>=sm`. Empty state is now an iconographic `EmptyState`.
- **Downloads** moves Refresh into the Card header actions, swaps Delete for a `tone=danger` `IconButton`, replaces the owner column text with a `Badge`, and shows unique file extensions under the file count. Empty directory uses `EmptyState` with the archive icon.
- **Logs** pulls the controls into a `Card.Toolbar`, applies per-level colour classes to a mono-font panel on the page-bg surface, switches Pause to the `warning` button variant when paused, and pins a Jump-to-bottom button to the bottom-right when the viewer scrolls away from the tail.
- **Config** API-key card collapses to a single flex row with reveal/hide and a copy button that swaps to a check icon on success. Settings card moves to a 2-column grid that collapses on mobile. Newznab and SABnzbd sections share an inline `CopyRow` helper which displays a masked API key but copies the real value via a `copyValue` override.
- **Overrides** uses `collapse=card` so each override turns into a labelled stack on mobile, replaces Edit and Delete buttons with `IconButton`s (settings + trash), and renders Date-Based as a coloured `Badge` instead of plain text.
- **System** rebuilds the six diagnostic cards into a responsive 1/2/3-column grid. Geo check status is a `Badge` (UK OK / Blocked) and the disk usage gauge swaps the custom progress bar for the shared `Progress` primitive with `default` / `paused` / `failed` variants at 80% and 90% thresholds.
- **NotFound** swaps the bare card for an iconographic `EmptyState` with a Return-to-dashboard action.
- **Setup Wizard** body migrates to `Card`, `Button`, and `Badge` while keeping the `wizard-overlay`/`wizard-modal`/`wizard-progress` scaffolding intact for its bespoke layout. Status indicators move from status-dot + text to coloured Badges.
- **App shell** swaps the legacy `components/Toast.tsx` and `components/ConfirmDialog.tsx` for the shared `ToastViewport` and a `ConfirmHost` that bridges the existing pending/resolvePending signal to Kobalte's `Dialog.Confirm`. `ErrorBoundary` fallback also moves to `Card` + `Button`.

### Removed

- **`frontend/src/components/Toast.tsx`** and **`frontend/src/components/ConfirmDialog.tsx`** (replaced by `ui/Toast.tsx` and `ui/Dialog.tsx`).

## [1.1.9] - 2026-05-04

### Added

- **Shared `<ConfirmDialog>` component** with `role=alertdialog`, `aria-modal`, focus trap, Esc-to-cancel, and backdrop-click-to-cancel. Replaces three native `confirm()` sites: `Dashboard.clearAllHistory`, `Downloads.deleteFolder`, `Overrides.remove`. Promise-based API: `confirmDialog({ title, message, confirmLabel, danger })`.
- **`NotFound` page** registered at `path="*"` so unknown URLs render a real not-found page with a return-to-dashboard link instead of an empty layout.
- **Top-level Solid `<ErrorBoundary>`** around the routed page so a render exception shows a fallback card with a `Try again` button instead of a blank screen.
- **Skip-to-main-content link** as the first focusable element in `Layout`. Visually hidden until focused.
- **Route-change focus management**: `Layout` watches `location.pathname` and focuses `<main>` after every navigation so SPA route changes announce the new page.
- **Reveal toggle on Setup Wizard API key fields** (Sonarr panel and SAB panel). Keys are masked by default (`XXXX...XXXX`).
- **`.progress-fill.healthy / .caution / .heavy` semantic states** for storage-style usage bars; `System.tsx` selects based on disk percentage so a healthy 42 % no longer reads as the destructive pink accent.

### Changed

- **Mobile media block (`@media (max-width: 768px)`)** in `frontend/src/styles.css`: `min-height: 44px` on `.btn-sm`, `.copy-btn`, `.hamburger`, `.nav-link` (WCAG 2.5.5); `.main` padding tightened to 16 px; `.system-grid` collapsed to single column to remove +73 / +92 / +29 px horizontal overflow on Dashboard / Config / System.
- **Setup Wizard modal** is now scrollable (`max-height: calc(100dvh - 32px)`, `overflow-y: auto`) so the Done button stays in view on Step 2. Adds an X close button at top-right and treats Esc / backdrop click as Complete.
- **Toast a11y semantics**: `<ToastContainer>` gains `aria-live=polite` on the wrapper. Success and warning toasts switch to `role=status` (implicitly polite); only error toasts retain `role=alert` (assertive). Stops screen readers from interrupting on every successful save.
- **`.input:focus-visible`** gains a 2 px `box-shadow` ring so focus is visibly distinct from hover (the previous 1 px border swap was too subtle).
- **`<th>` sortable headers in Dashboard history table** are now keyboard-accessible: `aria-sort` (`ascending` / `descending` / `none`), `role=button`, `tabindex=0`, `onKeyDown` for Enter and Space.
- **Hamburger button** gains `aria-controls="primary-nav"` (matching `id` on the `<nav>`) so screen readers can associate it with the panel it toggles.
- **Page heading hierarchy**: Search, Config, and Overrides gain `<h1 class="page-title">` (the four other pages already had one).
- **`.field-error`, `.config-hint`, `.dl-file-types`** bumped from 11 → 12 px so validation messages and hints are legible at 1× density. Directly fixes the Overrides validation-message-clipping bug.
- **`.mobile-topbar`** adds `env(safe-area-inset-*)` so iPhone notch / Dynamic Island devices do not obscure the hamburger or brand.
- **`@media (prefers-reduced-motion: reduce)`** neutralises animations and transitions for users who request it.

### Notes

Source for this set of UI/UX changes is the audit filed as issue #19 on 2026-05-03. PRs #20–#23 form the four-cluster sweep against that audit. Functional findings from the same audit (Pause not actually pausing, "Determining quality…" stuck at 70 %, Downloads page being a folder browser instead of a queue, SSE `ERR_NETWORK_CHANGED` reconnection noise) were intentionally deferred to their own scoped issues.

## [1.1.8] - 2026-05-01

### Fixed

- **Active Downloads no longer advertises a fake size estimate.** During download the dashboard previously showed `formatBytes(downloaded) / formatBytes(estimate)`, where the estimate came from a fixed-bitrate calculation against the *requested* quality. For shows where BBC silently delivers a lower quality than the prober advertised (Catherine Tate Show was the reporter's example: ~1GB estimate vs ~350MB on disk), this denominator was a lie. The active row now shows bytes-downloaded only and a literal `Determining quality…` muted span; the truthful values land in the History row once the download completes.
- **Filenames and history rows now reflect the actual encoded quality.** When BBC delivers a lower quality than the prober advertised, the worker now runs `ffprobe` against the completed file, atomically relocates the file and its containing directory to a name carrying the actual quality tag (e.g. `…540p.WEB-DL…` instead of `…1080p.WEB-DL…`), and exposes the truth to the frontend and to the SABnzbd-compatible history endpoint Sonarr polls. The truncation gate now uses the *actual*-quality threshold instead of the requested-quality one, fixing a class of false positives where 540p downloads at the lower end of BBC's bitrate ladder were rejected for "looking too small for 1080p". Symmetric upgrades (e.g. FHD-prober promoting a 720p pick to a hidden 1080p variant) are also captured.

### Added

- **`actual_quality` field on download records.** New `ActualQuality string `json:"actual_quality,omitempty"`` field on `store.Download` (and matching optional `actual_quality?: string` on the frontend's `Download` type). Populated post-`ffprobe` for v1.1.8+ downloads; remains empty for v1.1.7-and-earlier records, where the frontend's `actual_quality || quality` fallback keeps the display correct without a backfill.
- **`relocateNoReplace` helper using `unix.Renameat2(... RENAME_NOREPLACE)`** for kernel-atomic no-overwrite rename on supported filesystems, with a best-effort `os.Stat + os.Rename` fallback when the underlying filesystem returns `EINVAL`/`ENOSYS`. The fallback emits a distinguishing log line on entry so operators on filesystems outside the man page's explicit support list (notably NFS and overlayfs) can detect the degraded mode via log monitoring. Pre-release smoke on a Linux 6.17 host writing to a QNAP NFSv4.0 export confirmed: NFS does not expose the kernel-atomic flag and transparently uses the `Stat + Rename` fallback (functional behaviour identical, one log line per reconciliation). Local btrfs, ext4, and xfs (kernel ≥ 3.15) take the kernel-atomic path with no log line.

## [1.1.7] - 2026-04-17

### Fixed

- **Position-based episode identity now survives Sonarr's tvsearch filter (#32)**: BBC long-runners with no "Series N" subtitle prefix (Casualty, One Piece 1999) parsed to `Series=0, EpisodeNum=N` and were rejected by `matchesSearchFilter` because Sonarr always queries `season=1` for these shows. `iblResultToProgramme` now promotes `Series=1` whenever the subtitle gives a real episode number without a series prefix. Position alone does not trigger promotion (one-offs and specials also carry `Position>0`), so topical shows and genuine specials keep their existing date-tier and manual-tier handling.

### Changed

- **Registry pages now carry README and OCI metadata.** Added `org.opencontainers.image.*` labels to the Dockerfile (title, description, source, url, documentation, licenses) so GHCR auto-links back to the repository. The release workflow now syncs `README.md` to Docker Hub's description field via `peter-evans/dockerhub-description@v4` on every `v*` tag push. Docker Hub's short description is set to "BBC iPlayer Newznab indexer and SABnzbd download client for Sonarr".

## [1.1.6] - 2026-04-16

### Fixed

- **Sonarr follow-up episode searches now carry `tvdbid` attribute (#31)**: When Sonarr sends a tvsearch with `q=ShowName` and an empty `tvdbid` (the shape it uses for episode-level follow-ups after an initial tvdbid-only lookup), iplayer-arr now reverse-looks-up the tvdbid in its series mapping store so the `<newznab:attr name="tvdbid">` echo keeps firing on every item. Previously Sonarr could not match these items back to the correct series for shows with duplicate BBC brand names or where the `q` string alone was ambiguous.
- **Store reverse lookup now matches year-suffixed titles**: `GetSeriesMappingByName` adds a fallback that matches a bare title like "Doctor Who" against a stored name like "Doctor Who (2005)" (Skyhook's year disambiguator). Without this, the rehydration added above would silently skip for most post-2005 TVDB shows.

## [1.1.5] - 2026-04-14

### Fixed

- **TVDB ID echoed in RSS responses**: iplayer-arr now includes `<newznab:attr name="tvdbid" />` in search results, giving Sonarr a definitive series match. Previously, Sonarr relied on title parsing which failed for ambiguous names like "Return to Paradise" vs "Paradise".
- **Redundant `Series.N.M` in release titles confuses Sonarr**: the episode subtitle (e.g. `Series 2: 1. Apex Predator`) was included verbatim after the `SxxExx` tag, producing titles like `S02E01.Series.2.1.Apex.Predator` that Sonarr misparses. The `Series N: M.` prefix is now stripped since the numbering is already in `SxxExx`.
- **Episode titles containing colons break subtitle parser**: subtitles like `Series 2: 5. Chapter One: Murder` were split at all `": "` delimiters (limit 3), causing the episode number to be lost. Changed to split at the first colon only so episode titles with colons are handled correctly.
- **Dashboard overflow**: active downloads and queue sections now cap at 400px/300px with thin internal scrollbars. Removed `overflow-x: hidden` from `.main` which was preventing natural page scroll.
- **Queue section not appearing dynamically**: SSE events for new pending downloads were routed to the active list instead of the queue, so the Queue card never appeared without a page refresh.
- **Long titles in dashboard**: download titles and history table now truncate with ellipsis instead of wrapping across multiple lines.

## [1.1.4] - 2026-04-12

### Fixed

- **BBC subtitle name mismatch breaks Sonarr searches**: shows where BBC iPlayer appends a subtitle to the brand name (e.g. "Talking Tom Heroes: Suddenly Super" vs TVDB's "Talking Tom Heroes") returned zero results because the newznab name filter required an exact match after year-suffix stripping. Added `nameMatches` with a subtitle-prefix path: if the BBC name starts with the search query followed by `: `, it counts as a match. 52 episodes of Talking Tom Heroes now surface correctly.
- **"Unknown episode or series" red marker on iplayer-arr results**: the generated release title included the full BBC name with subtitle (`Talking.Tom.Heroes.Suddenly.Super.S01E38...`), which Sonarr's title parser couldn't map back to its TVDB entry. When the search matched via subtitle prefix, the title now uses the Sonarr-provided name (`Talking.Tom.Heroes.S01E38...`) so TVDB mapping succeeds.
- **Language shown as "Unknown" in Sonarr manual search**: RSS items had no language attribute, so Sonarr defaulted to Unknown. Added `<newznab:attr name="language" value="en" />` to every RSS item. All BBC iPlayer content is English.
- **Batch downloads overwhelm BBC CDN**: grabbing a full series caused all 10 workers to hit the CDN simultaneously through a single VPN, triggering rate-limiting (ffmpeg exit 187, master playlist EOF, truncated files). Three changes:
  - Default worker count reduced from 10 to 4.
  - Retryable failures now use exponential backoff (30s, 90s, 270s) instead of immediate retry. Downloads stay as "Queued" in Sonarr during backoff rather than being marked "Failed".
  - Truncated downloads are now retryable (they were previously permanent failures, but truncation is usually caused by rate-limiting, not missing content).

### Tests

- 4 new cases in the `matchesSearchFilter` table test: subtitle prefix match, case-insensitive subtitle match, partial-word rejection ("Tom" must not match "Tom Jones: Live"), and full colon-title exact match.
- Updated default worker count test and truncated retryability test.
- 231 Go tests pass across 8 packages.

## [1.1.3] - 2026-04-12

### Fixed

- **#27 Downloads marked completed when ffmpeg produces a truncated file**: older SD-only BBC content (e.g. The Catherine Tate Show at 704x396) was being downloaded as audio-only (~27MB) and marked as completed. Two root causes addressed:
  - **FHD probe false positive on SD content**: `ProbeHiddenFHD` rewrote HLS variant URLs to `video=12000000` and HEAD-probed them. BBC's Unified Streaming Platform returns HTTP 200 for non-existent bitrates, generating a manifest with only the audio stream. Added a resolution guard: if the master playlist's max RESOLUTION height is below 720p, the HEAD probe is skipped and definitive absence is returned.
  - **No post-download validation**: after ffmpeg exits 0, `processDownload` now stats the output file and compares it against the estimated size. If actual size is below 30% of expected, the download is failed with a new `FailCodeTruncated` error code and the partial file is removed. This catches all truncation causes (FHD false positives, CDN throttling, network interruptions).

### Performance

- **Quality probe skips FHD check for SD-only content**: `probeOne` now checks the mediaselector heights before calling `ProbeHiddenFHD`. If the best available height is below 720p, the FHD probe is skipped entirely, saving an HTTP round-trip per episode.
- **Show-level probe deduplication**: `PrefetchPIDs` now groups items by ShowName and probes one representative PID per show. The result is reused for all siblings via cache writes, reducing BBC API calls from 3 per PID to 3 per show. A 200-episode show search drops from ~600 API calls to 3, cutting first-time search latency from ~120s to ~2-5s. Falls back to individual probing if the leader PID fails.

### Tests

- `TestMaxPlaylistHeight` unit test for the resolution guard helper.
- `TestProbeHiddenFHD_SDOnlyPlaylist_ReturnsDefinitiveAbsence` verifies the HEAD probe is never called for SD-only master playlists.
- `TestProbeHiddenFHD_720pPlaylist_StillProbes` verifies 720p+ content still gets the FHD probe.
- `TestFailDownloadRetryability` extended with truncated-not-retryable case.
- `TestPrefetch_ShowGroupDedup_ProbesOncePerShow` verifies one probe per show, not per PID.
- `TestPrefetch_ShowGroupDedup_CacheHitCoversGroup` verifies zero HTTP calls when any sibling is cached.
- `TestPrefetch_ShowGroupDedup_FirstFails_FallsBackToIndividual` verifies fallback on leader failure.
- `TestPrefetch_ShowGroupDedup_AllFail_ReturnsNil` verifies nil result when every probe fails.
- 227 Go tests pass across 8 packages.

## [1.1.2] - 2026-04-09

### Fixed

- **#20 Topical shows not matched by Sonarr searches**: weekly topical shows like Question Time and Newsnight that BBC iPlayer reports with no series/episode numbering (Series=0, EpisodeNum=0) were silently dropped from Sonarr integer-S/E searches because the newznab filter rejected them outright. The filter now accepts zero-numbered programmes that have a valid air date, so the existing date-tier release generator emits a `Show.Name.YYYY.MM.DD` title. **Sonarr configuration note:** set the series type to "Daily" for topical shows, Sonarr only accepts date-based releases for series flagged Daily. This is the same mechanism the BBC daily soaps (EastEnders, Casualty, Doctors) already rely on.
- **#21 Copy buttons silently fail on plain HTTP origins**: `navigator.clipboard.writeText()` only works in a secure context, so every Copy button on the Config page and Setup wizard silently rejected when iplayer-arr was reached at `http://<lan-ip>:<port>` (non-secure context). Added `frontend/src/lib/clipboard.ts` with a hidden-textarea `execCommand('copy')` fallback. All 8 copy buttons verified in a real Chromium browser over plain HTTP.
- **#21 Manual download Delete button inert**: the s6 service run script used `#!/usr/bin/env bash`, launching the binary outside the hotio base image's `with-contenv` envdir. None of `CONFIG_DIR`, `DOWNLOAD_DIR`, `PORT`, or `TZ` reached the process when set in docker-compose, so the writer path used hardcoded fallbacks while the Downloads page scanner read its own source of truth, and the Delete button rendered disabled because the ownership map never matched. Switched the run script to `#!/command/with-contenv bash`, persisted the resolved DOWNLOAD_DIR to the config store on startup so writers and readers share a single source of truth, and added `filepath.Clean` normalisation in `ListHistoryOutputDirs` as belt-and-braces safety for legacy entries. Verified end-to-end: real manual download to `/data/tv`, Delete click in a real Chromium, folder removed from both the directory listing and the filesystem. Side-benefit: log timestamps now honour TZ.

### Tests

- `TestHandleTVSearchTopicalWeeklyFallbackToDate` covers the full newznab handler path with a Question Time payload.
- Two new cases in the `matchesSearchFilter` table test cover the topical fallback with and without an air date.
- `TestListHistoryOutputDirsCleansPaths` regression-tests the ownership map normalisation for trailing slash, dot segment, clean path, and empty OutputDir variants.
- 216 Go tests pass across 8 packages. Frontend vitest suite passes.

## [1.1.1] - 2026-04-08

### Breaking changes

- **Default PORT changed from 8191 to 62001** to avoid collision with FlareSolverr (which also defaults to 8191). Users with `-p 8191:8191` in their docker-compose must update to `-p 62001:62001`, or set `-e PORT=8191` to keep the old port. Users who already set `PORT` explicitly are unaffected.

### Fixed

- **#15 Match of the Day daily title**: BBC composite-format subtitles like `"2025/26: 22/03/2026"` no longer produce malformed triple-dated filenames. Sonarr's Daily-series parser now accepts Match of the Day releases.
- **#16 DOWNLOAD_DIR variable not surfaced in UI**: the env-derived value is now consistently returned by `/api/config`, the directory listing endpoints, and the SABnzbd compat handler. Files were already downloading to the correct location; only the UI display was wrong.
- **#18 Doctor Who duplicate-name disambiguation**: Sonarr searches for shows with year-suffixed BBC brand titles (classic Doctor Who, 2005-2022 era, Casualty reboots, etc.) now route to the correct brand via year-range matching. Adds new `bareName`, `extractYearRange`, `nameMatchesWithYear`, and `disambiguateByYear` helpers. Known limitation: if BBC's own metadata catalogue mislabels an episode (e.g. a modern Doctor Who episode catalogued under the 1963-1996 brand PID), iplayer-arr cannot detect the inconsistency. This is a BBC data quality issue, not an iplayer-arr bug.
- **#19 Default PORT collides with FlareSolverr**: see Breaking changes above.

### Closed as out of scope

- **#14 STV Player support**: iplayer-arr is intentionally a BBC iPlayer-only tool. See the issue reply for the full reasoning.

### Project governance

- Added `DISCLAIMER.md` with TV Licence requirement, BBC trademark disclaimer, personal-use restriction
- Added `SECURITY.md` pointing at GitHub Private Vulnerability Reporting
- Added structured GitHub Issue Forms (bug report + feature request) with all fields optional, and a `config.yml` that routes security reports to Private Vulnerability Reporting
- Backfilled the v1.1.0 CHANGELOG entry that was missing

### Tests

- Approximately 35 new unit and integration tests across `internal/newznab/`, `internal/bbc/`, `internal/api/`, `internal/sabnzbd/`, `internal/store/`, and `cmd/iplayer-arr/`. All BBC and Skyhook API calls mocked - no live network calls in tests.

### Design spec

See `docs/superpowers/specs/2026-04-08-iplayer-arr-v1.1.1-design.md` for the full design rationale (10 review rounds applied).

## [1.1.0] - 2026-04-08

### Fixed

- **Download directory permissions**: `EnsureDownloadDir` now creates download directories with mode `0o775` instead of `0o755`, so a container's PUID/PGID can write to host-mounted download directories under the default umask of `0o002`. Previously, downloads would fail at the first file write because the group-write bit had been stripped. This affected UNRAID users running iplayer-arr alongside Sonarr with hotio's `UMASK=002` convention.
- **No more fake 1080p in RSS responses**: the Newznab search response no longer advertises `1080p` for shows BBC does not actually offer in 1080p. Previously, Sonarr would see a `1080p` item in the RSS feed for shows like EastEnders, try to grab it, and receive a 720p file at best. v1.1.0 probes BBC's mediaselector at search time and only advertises quality tags that match what BBC actually delivers. The probe results are cached per-PID in a new BoltDB `quality_cache` bucket and reused indefinitely (BBC content masters are effectively immutable once published).

### Configuration (optional)

- `IPLAYER_PROBE_CONCURRENCY` (default `8`) - worker pool size for parallel quality prefetch
- `IPLAYER_PROBE_TIMEOUT_SEC` (default `20`) - per-probe wall-time deadline

### Tests

- 51 new unit tests across 6 new files (`internal/bbc/fhdprobe_test.go`, `internal/bbc/prober_test.go`, `internal/store/quality_cache_test.go`, `internal/newznab/heights_test.go`, `internal/download/ffmpeg_hls_test.go`, plus `internal/bbc/ibl_test.go` extension) and 1 extension to `internal/newznab/handler_test.go`. All BBC and ffmpeg interactions mocked - no live network calls in tests.

### Design spec

See `docs/superpowers/specs/2026-04-07-iplayer-arr-issue-12-design.md` for the full design rationale and PR #17 for the diff.

## [1.0.2] - 2026-04-06

### Fixed

- **BBC shows whose iPlayer subtitle uses the `"Series N: Episode M"` form (Little Britain, Cunk on Britain, and any other show that doesn't number episodes as `"M. Title"`) now reach Sonarr correctly.** The Newznab `tvsearch` filter compares Sonarr's `season`/`ep` against the parsed `Series`/`EpisodeNum` extracted from each iPlayer subtitle. The episode-number regex was anchored to the numbered-list form `^(\d+)\.\s*` and silently failed on the named form, so `EpisodeNum` stayed at 0 and every release was filtered out. End-to-end: Sonarr saw zero candidates for these shows, fell back to whatever it could parse, and Sonarr's manual import had to clean up after the file landed on disk without `S01E01` in the name. Issue #13.
  - `internal/bbc/ibl.go`: `reEpisodeNum` now matches both layouts via `(?i)(?:^|(?:Episode|Pennod)\s+)(\d+)`. Welsh `Pennod` added for parity with the existing `Cyfres` series alias. The numbered-list form (`"1. Pilot"`, `"12. Christmas Special"`) still works unchanged.

- **Sonarr's interactive search no longer floods with releases from unrelated BBC shows.** BBC iPlayer's IBL search is relevance-ranked across the whole catalogue, so a query like `little britain` returns ~24 programmes whose titles merely contain "Britain": Cunk on Britain, Drugs Map of Britain, A History of Ancient Britain, Inside Britain's National Parks, A History of Britain by Simon Schama, Glow Up: Britain's Next Make-Up Star, and so on. iplayer-arr previously expanded every one of those into episodes and matched them against Sonarr's S/E filter, surfacing dozens of false positives in the manual search UI. The new show-name filter drops any episode whose BBC programme title doesn't case-insensitively match the resolved query name (whether that came from Sonarr's `q=` or a `tvdbid` -> Skyhook lookup). Wildcard browse mode (`q=""` and `tvdbid=""`) is exempt so the iplayer-arr web UI still lists everything.
  - `internal/newznab/search.go`: `writeResultsRSS` gains a `filterName` parameter; `handleSearch` and `handleTVSearch` capture the resolved query name *before* the BBC fallback so wildcard browses don't inherit a filter.

### Tests

- +3 new tests bringing the suite from 109 to 112:
  - `bbc/ibl_test.go::TestParseSubtitleNumbers`: 12 cases covering both subtitle layouts, Welsh, mixed case, multi-digit episodes, and edge cases (unnumbered, no series part).
  - `newznab/handler_test.go::TestHandleTVSearchFiltersOtherShowsByName`: payload of four "Britain" shows; only Little Britain releases survive the filter.
  - `newznab/handler_test.go::TestHandleSearchBrowseHasNoNameFilter`: verifies the wildcard browse mode is exempt from the filter so the iplayer-arr web UI still lists every show.

### Verified end-to-end

Live container on a real BBC iPlayer feed:

| Search | Before v1.0.2 | After v1.0.2 |
|---|---|---|
| `tvdbid=72135&season=1&ep=1` (Little Britain S01E01) | Only `Drugs.Map.of.Britain.S01E01.*` (Little Britain rejected by EpisodeNum filter) | Three `Little.Britain.S01E01.*` quality variants and nothing else |
| `q=little+britain&season=1&ep=1` | Drugs Map of Britain only | Three `Little.Britain.S01E01.*` quality variants |
| `t=search` (browse) | All BBC content | All BBC content (filter correctly disabled) |
| EastEnders date query (v1.0.1 daily-soap fix) | `EastEnders.2026.03.30.*` | `EastEnders.2026.03.30.*` (no regression) |

### Container images

```
docker pull ghcr.io/will-luck/iplayer-arr:1.0.2
docker pull willluck/iplayer-arr:1.0.2
```

## [1.0.1] - 2026-04-06

### Fixed

- **BBC daily soaps now reach Sonarr correctly.** EastEnders, Casualty, Holby City, Doctors, Coronation Street, Neighbours and any other BBC show whose iPlayer subtitle is just a date were silently broken end-to-end:
  - Newznab releases were emitted as `EastEnders.S01E7307.06042026.1080p...` because Tier 2 used `parent_position` as the episode number. Sonarr's parser interpreted this as season 1 episode 7307, found no matching episode in TVDB, and rejected every release.
  - The Newznab `tvsearch` filter compared Sonarr's TVDB-style `season`/`ep` against iPlayer's internal `Series`/`EpisodeNum`, which are both 0 for these shows. Every interactive search returned an empty RSS feed.
- `internal/newznab/titles.go`: added Tier 1.5 — when the iPlayer subtitle parses as a bare date (DD/MM/YYYY, DD-MM-YYYY, DD.MM.YYYY) and an air date is available, the release title is now generated in date form: `EastEnders.2026.04.06.1080p.WEB-DL.AAC.H264-iParr`. Sonarr's daily-series parser maps these to the correct `S{season}E{episode}` automatically. No per-show overrides required.
- `internal/newznab/search.go`: `handleTVSearch` now recognises Sonarr's daily-series query convention (`season=YYYY&ep=MM/DD`) and filters by air date instead of integer season/episode. The standard integer compare remains for normal numbered shows.
- `internal/bbc/ibl.go`: `IBLResult.AirDate` is now normalised to canonical `YYYY-MM-DD` at parse time via the new `normaliseAirDate` helper, regardless of whether BBC iBL returned `"6 Apr 2026"` or `"2026-04-09"`. Both `Search` and `ListEpisodes` paths covered. Downstream consumers (filters, title generation, RSS pubDate) can rely on a single shape.

### Tests

- +8 new tests bringing the suite from 84 to 109:
  - `bbc/ibl_test.go`: `TestListEpisodesNormalisesLooseAirDate`, `TestSearchNormalisesLooseAirDate`
  - `newznab/titles_test.go`: `TestGenerateTitleSubtitleIsBareDate`, `TestGenerateTitleSubtitleDateAlternateSeparators`, `TestGenerateTitleNumberedShowNotPromoted` (regression guard)
  - `newznab/handler_test.go`: `TestHandleTVSearchDailyMatchByDate`, `TestHandleTVSearchDailyMismatchByDate`, `TestHandleTVSearchStandardSEStillWorks` plus shared `fakeBBCSearchServer` helper. Closes the long-standing gap where the only handler test covered the `caps` endpoint.

### Verified end-to-end

- Sonarr `/api/v3/release?episodeId=49265` (live EastEnders S42E54): now returns 3 releases (1080p / 720p / 540p), all mapped to `S42E54`, `rejected: false`. Previously returned zero.
- Future episodes that haven't aired yet (e.g. S42E55, S42E56) correctly return zero items — the filter is precise, not just always returning the latest.
- Octonauts S1E1, In the Night Garden S1E1 (Tier 1 numbered shows): unchanged, still produce `S01E01.<episode-title>...` titles via Tier 1.

### Documentation

- Added Docker Hub and pkgbadge stats badges to README (b1bb865).

### CI

- Added weekly base image rebuild workflow that watches the hotio base image digest (0f3b805, c92dabd).
- Added multi-arch (amd64 + arm64) builds and Docker Hub publishing to release workflow (6f08605).

### Container images

```
docker pull ghcr.io/will-luck/iplayer-arr:1.0.1
docker pull willluck/iplayer-arr:1.0.1
```

## [1.0.0] - 2026-04-06

First stable release of iplayer-arr — a BBC iPlayer download manager that plugs into Sonarr as an indexer and download client.

### Added

- Full Sonarr integration via Newznab indexer and SABnzbd-compatible download API
- Built-in VPN support via hotio base image (WireGuard)
- Dashboard with download monitoring, history, and system health
- Setup wizard for guided Sonarr configuration
- Multi-arch images: `linux/amd64` and `linux/arm64`
- Published to both GHCR and Docker Hub
- Weekly automatic rebuild when the hotio base image updates

[1.0.1]: https://github.com/Will-Luck/iplayer-arr/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/Will-Luck/iplayer-arr/releases/tag/v1.0.0
