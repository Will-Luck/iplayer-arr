# Changelog

All notable changes to iplayer-arr will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Four new auth-gated `/api/diag/*` regression-anchor endpoints.** Each synthesises an integration round-trip in-process via `httptest.NewRecorder` and returns a `verdict: pass|fail` JSON report with per-component detail. Modelled on the existing `/api/diag/sonarr-handshake` from v1.5.6 and intended as permanent CI assertions, not throwaway probes.
  - `GET /api/diag/ffmpeg`: invokes `ffmpeg -version`, parses a synthetic 8.x `KiB`-form progress line through `download.ParseProgress`, and asserts the regex matches both `kB` and `KiB` units. The exact assertion that would have failed v1.5.5 in CI. Regression anchor: `TestDiagFfmpeg_DetectsRegression` reproduces the `kB`-only regex and asserts `verdict: fail`.
  - `GET /api/diag/bbc`: drives the live IBL search via an injectable `bbcProbe` (production wires the real `bbc.Client`; tests inject a fake) with a known-good tvdbid and asserts the response shape carries both brand and episode information. Catches BBC API shape changes early.
  - `GET /api/diag/sab`: synthesises each SABnzbd-compat mode (`version`, `queue`, `history`, `get_cats`, `get_config`, `fullstatus`) against the live SAB handler both with and without the apikey. Asserts `version` is the only unauthenticated carve-out and every other mode rejects key-less requests while accepting keyed ones. Catches the v1.5.5-class apikey-threading regressions (`get_config` leaking `complete_dir` on the LAN was the worst case).
  - `GET /api/diag/auth-paths`: drives three synthetic requests through `authenticate()` and asserts `?apikey=`, `Authorization: Bearer`, and `X-Api-Key` all resolve identically. Catches the test/prod auth drift that hid the v1.5.5 chain (tests relied on `X-Api-Key`; production rejected it).
- **`authenticate()` widened to accept `X-Api-Key`.** Three accepted mechanisms now: `?apikey=` query parameter, `Authorization: Bearer <key>`, and `X-Api-Key: <key>` header. Closes the test/prod drift: prior to this change, integration tests passed against a code path production rejected. The `/api/diag/auth-paths` endpoint asserts the invariant going forward.
- **Gitea Actions `unit` + `diag-suite` jobs.** `.gitea/workflows/ci.yml` runs `go test ./... -race` then builds the container, brings it up on a random high port, and curls every `/api/diag/*` endpoint asserting `verdict == "pass"`. Branch protection requiring both jobs green is enabled in a follow-up commit; see `docs/testing.md` for the framework rationale.

## [1.5.6] - 2026-05-17

Bundled release. Closes out the v1.5.5 regression chain (issue #40, both the apparent `ffmpeg exit status 251` symptom and the follow-up Sonarr `401 Invalid API Key`), folds in the post-v1.5.5 audit findings that were validated against real code (~23% of the agent-flagged items were false positives; only the verified ones are listed below), and adds the first iplayer-arr diagnostic endpoint so this entire regression class becomes catchable in CI from now on.

### Added

- **`GET /api/diag/sonarr-handshake` — single-call Sonarr round-trip simulator (auth-required).** Synthesises a `t=tvsearch` against the live newznab handler in-process via `httptest.NewRecorder`, parses the returned RSS, asserts that every `<guid>`/`<link>`/`<enclosure>` URL carries `&apikey=`, then follows the first enclosure URL as `t=get` and verifies it returns an `application/x-nzb` body. Sanity-checks ffmpeg/geo/store for context. Returns a single JSON `DiagSonarrReport` with per-component `ok` flags and an aggregated `verdict` of `pass` or `fail`. The `feed_apikey` check is the exact assertion that would have failed v1.5.5 in CI; from v1.5.6 onwards `curl http://host/api/diag/sonarr-handshake?apikey=$KEY` is the one-shot smoke test for operators and the integration test for CI. Wired via a new `Handler.SetNewznabHandler` setter (`cmd/iplayer-arr/main.go` calls it after both handlers exist). Regression anchor: `TestDiagSonarrHandshake_DetectsRegression` reproduces the v1.5.5 feed shape and asserts `verdict: fail`.

### Fixed

- **SAB-compat shim `mode=get_cats|get_config|fullstatus` now require apikey.** Pre-v1.5.6 the SAB shim's `ServeHTTP` returned the response body for these three modes BEFORE consulting the stored `api_key`, so any LAN host could hit `http://host/sabnzbd/api?mode=get_config` and read the on-disk `complete_dir` path (on a typical homelab this is the real NFS mount). Same regression shape as the v1.5.0 Newznab apikey enforcement fix — defence was added on `/newznab/` and the parallel surface on `/sabnzbd/` was missed. The auth check now runs above the mode switch and allow-lists only `version` (Sonarr probes `version` before attaching the key, identical to the Newznab `t=caps` probe). Regression anchor: `TestAuthRequiredForInfoModes` covers all three modes; `TestAuthRequiredForInfoModes_WrongKey` covers the present-but-wrong-key case.

- **FHD quality probe transient errors preserve the resolved heights.** `internal/bbc/prober.go::probeOne` resolves mediaselector heights first (e.g. `[720, 540]`) then tries the FHD HEAD probe. Pre-v1.5.6 a transient FHD probe failure (429, 5xx, 401/403, transport error) returned `nil` and discarded the heights — a single throttle from BBC's USP wiped out a perfectly valid 720p/540p probe and forced the wildcard browse caller to fall back to the safe-default `[720p, 540p]` constants. The probe now logs the transient error and falls through to step 6 with the already-resolved heights intact. It still skips the cache write on transient err (per the caching contract documented on `fhdprobe.go::ProbeHiddenFHD`: "Callers that cache must NOT cache this branch") so the next probe will retry the FHD HEAD and may discover 1080p once the throttle clears. Regression anchor: `TestPrefetch_FHDProbeError_PreservesExistingHeights` (renamed from the misleading `..._ReturnsNilNoCacheWrite` that pinned the broken behaviour).

- **ffmpeg stderr-tail filter no longer pollutes diagnostics with partial-progress lines.** The v1.5.5 stderr-tail capture (`internal/download/ffmpeg.go::appendDiagLine`) added every line that `parseProgress` rejected to the diagnostic ring. `parseProgress` requires BOTH `reTime` AND `reSize` to match, so a line shaped `time=00:00:02.00 bitrate=128.0kbits/s` (no `size=` field, e.g. audio-only segment or a partial flush before size materialises) would fall through and pollute the tail. With an 8-entry ring, a single 403 sandwiched between 8 such progress-shaped lines would be evicted before the operator ever saw it. `appendDiagLine` now consults a `looksLikeProgressLine` whitelist (`frame=`, `fps=`, `size=`, `time=`, `bitrate=`, `speed=`, `out_time=`, `out_time_ms=`, `out_time_us=`, `dup=`, `drop=`) and drops any line whose first token matches, regardless of parse outcome. Real errors (`Conversion failed!`, HTTP status lines, `Permission denied`) still land in the tail and surface in the exit error. Regression anchor: `TestAppendDiagLine_SkipsPartialProgressLines`.

- **EXDEV partial-copy cleans both ends, not just the destination.** `internal/download/manager.go::finaliseDownload` falls back to `copyDir + remove` when `os.Rename` returns EXDEV (incomplete/ on a different filesystem than the final downloadDir — common on tmpfs staging or split NFS exports). On `copyDir` failure pre-v1.5.6 the code removed the partial destination (`finalDir`) but left the source (`dl.OutputDir`) intact. A worker retry would then re-run ffmpeg into the still-populated `incomplete/<title>/` and on success move it up — silently shipping a mixed-state artefact (old fragments plus new content). The cleanup is now symmetric: both `finalDir` AND `dl.OutputDir` are removed on `copyDir` failure so the retry starts from a clean slate.

- **`finaliseDownload` race-free target collision check.** Pre-v1.5.6 the move was `os.Stat(finalDir) -> os.Rename(...)`, a textbook TOCTOU: two finalises landing on the same `safeTitle` (e.g. an alternate-cut PID and an original PID for the same episode) could both pass the Stat check and both call `os.Rename`, with the second silently clobbering the first's just-moved directory on Linux's tmpfs/ext4/xfs. The check now goes through `relocateNoReplace` (the same `unix.Renameat2(..., RENAME_NOREPLACE)` helper that `worker.go:583` already uses for the reconcile step), so kernel-atomic EEXIST surfaces as a clean "target already exists" error instead of a data-loss race. Falls back to the Stat+Rename path on filesystems that don't support `RENAME_NOREPLACE` (kernel < 3.15 or unsupported FS), so exotic-mount behaviour is unchanged. Regression anchor: `TestFinaliseDownload_TargetExists_RefusesToClobber`.

- **`error` and `fatal` log entries bypass the SSE broadcast token bucket.** `RingBuffer.Add` rate-limits SSE broadcasts at 20/s to keep a chatter burst from saturating the dashboard. Pre-v1.5.6 the bucket also throttled lines written through `log.SetOutput(multiWriter)` in `cmd/iplayer-arr/main.go`, so a startup burst (>20 lines/second) could silently drop a panic stack — the operator's last clue before death. `Add` now consults `isUrgentLevel(e.Level)` and bypasses the bucket for `error`/`fatal`. `warn` stays metered because a warn-flood is a known shape (a single failing operation retried in a tight loop) and dropping the tail is right for that case. Regression anchor: `TestRingBufferUrgentBypassesBucket`.

- **Dashboard pause/delete/clear-all buttons gain double-click guards.** Pre-v1.5.6 only the Cancel button had an in-flight guard (`cancelling()` Set); a fast double-click on Pause or a per-row Delete on the history table would fire two DELETEs against the API. The new `pendingActions` signal generalises the pattern: each mutating handler marks its action key (`pause`, `history:<id>`, `clear-all`) on entry and unmarks on exit, and the corresponding button reads `isPending(key)` for its `disabled` state.

### Changed

- **`rebuild.yml` gains a 25-minute job-level `timeout-minutes` cap (issue #40).** The `docker/build-push-action@v6` step hung indefinitely on two separate dispatches (2026-05-10: 6h05m before user cancel; 2026-05-17: ~40m before user cancel). GitHub Actions / Packages / Docker Hub all reported `operational` on both occasions, so this is not an upstream incident. The most likely culprit is `cache-from: type=gha,scope=rebuild` / `cache-to: ...,mode=max`: when the GHA cache scope falls into a bad state, buildx retries layer pulls indefinitely without surfacing the loop as step output, so the step appears to be running normally while burning hours of compute. The cap is a defensive measure independent of root-cause investigation: healthy actual-rebuild runs (e.g. 2026-04-06 at 6m03s, 2026-04-19 at 5m54s) finish in 5-6 minutes, so 25 minutes is generous headroom for legitimate slow builds while bounding any future hang to a single billable instance. Subsequent mitigations (purge the `rebuild` GHA cache scope; if recurrence, drop the `cache-from`/`cache-to` block entirely) are tracked under issue #40 and are not part of this patch — this is the runaway-safety net that needs to land first regardless of which mitigation the cache investigation lands on.

### Fixed

- **ffmpeg progress parser handles 8.x `KiB` size unit; watchdog no longer kills healthy downloads.** ffmpeg 7.x emitted progress lines as `size=NkB` (SI-style misnomer for kibibytes); ffmpeg 8.x switched to the proper IEC unit `size=NKiB`. Same value, new spelling. The `reSize` regex was written against ffmpeg 7.x and matched only `kB`, so under ffmpeg 8.x (which the alpine VPN base image is now shipping) every progress line failed the parse. Two simultaneous knock-ons: (a) `OnProgress` never fired, so the SAB-API queue UI stuck at `0%` for every download even though ffmpeg was actually pulling at 8-14x realtime, (b) `lastProgressNanos` never advanced, so the 60s "no progress" watchdog cancelled every download at exactly 60s — often right after ffmpeg had finished pulling the full episode. The original v1.5.5 `7a7fcbc fix(download): surface ffmpeg stderr tail on failure (issue #40)` triaged the symptom (kills with no useful error context) but the captured stderr tail itself was the smoking gun, with `size=  680448KiB` lines visible in every "failed" download report. `reSize` now matches `(?:KiB|kB)` so the same parser code path works under both ffmpeg generations; conversion stays `* 1024` because ffmpeg's pre-8 `kB` was always actually kibibytes anyway. Regression anchor: `TestParseProgress` extended with verbatim ffmpeg 8.0.1 stderr lines captured from a prod Andy's Dino Island download.

- **Sonarr grab now succeeds: RSS feed URLs self-include the apikey.** The Newznab auth gate (added in the v1.1.x security hardening) required an `apikey` query param on every endpoint except `t=caps`, but the `<link>`, `<guid>` and `<enclosure>` URLs published inside the RSS feed by `writeResultsRSS` were never updated to carry one. Sonarr's grab-time HTTP fetch follows the URL straight from the feed without re-injecting credentials from its own indexer config, so every grab attempt hit a `401 Invalid API Key` and every result Sonarr "found" was silently dropped with a `Couldn't add release '<title>' from Indexer iplayer-arr to download queue` log line. To the user this looked like "iplayer-arr disconnects after N matches" because the search succeeded but no download ever started. `writeResultsRSS` now reads the seeded api_key from the store once and appends `&apikey=<key>` to all three published URLs. The empty-store case (used by tests with an unseeded key, which `authenticate()` short-circuits to allow anyway) emits the same key-less URLs as before, so no test or production behaviour is altered when no key is in use. Regression anchor: `TestFeedURLsIncludeAPIKey`.

## [1.5.5] - 2026-05-16

Two GitHub bug reports filed against the v1.5.4 train. Both fixes
small, both with regression coverage.

### Fixed

- **Subtitle timestamps now canonicalise to strict SRT (GitHub issue #41).** `bbc.toSRTTime` previously did a single `strings.Replace(".", ",", 1)` to convert TTML timestamps to SRT form. BBC's TTML drops the `.000` fractional part when milliseconds are exactly zero, so a cue like `<p begin="00:00:01" end="00:00:02">` emitted `00:00:01 --> 00:00:02` (no comma, no milliseconds) instead of the strict `00:00:01,000 --> 00:00:02,000`. Strict SRT parsers (some embedded renderers, certain Plex/Jellyfin paths) reject the bare form, and if it lands on the first cue the whole file fails to render. The function now handles all three TTML shapes (period-based, frame-based, bare HH:MM:SS) and always emits canonical `HH:MM:SS,mmm` with exactly three digits. Tests cover bare timestamps, zero/short/long fractions, frame-based input, and an end-to-end TTML → SRT round-trip.
- **ffmpeg exit-status errors now carry stderr context (GitHub issue #40).** When ffmpeg died with a bare `exit status 251` (Linux's `EIO` wrapping is the common cause on Docker/WSL2 with mismatched bind-mount permissions), the actual diagnostic line ffmpeg wrote to stderr was discarded by the progress scanner. Users got an opaque exit code with nothing to act on. The scanner now keeps a ring of the most recent 8 non-progress stderr lines and attaches them to the returned error, so failures surface as `ffmpeg: exit status 251 | stderr: [hls @ 0x55] HTTP error 403 Forbidden | ...`. Loglevel bumped from `fatal` to `error` so non-fatal-but-failed events also reach stderr. The behaviour-change is additive; a clean exit returns the same `nil` as before. Regression anchor: `TestAppendDiagLine_TailsAndTrims`.

## [1.5.4] - 2026-05-16

Phase 5 hygiene from the audit-driven cleanup chain. The v1.4.0 audit
flagged six items here; one (item 35, retention-bucket "doc drift")
turned out to be a false positive (the comment is correct historical
context for why the 90 s sleep was removed) and was rejected after a
validation read. The remaining five are small but real.

### Fixed

- **BBC client rotates its User-Agent per request (item 36).** `bbc.Client` previously picked a UA once at `NewClient()` and reused it for every request the process ever made. With nine UAs in the pool, the rotation could only happen via process restart. The `userAgent` field is removed; `doWithRetryCtx`, `HeadCtx`, and the FHD probe now call `RandomUserAgent()` at request-build time. Test coverage adds `TestClientRotatesUserAgent` (50 requests, asserts at least 2 distinct UAs seen).
- **Log SSE broadcasts are now token-bucketed (item 37).** `RingBuffer.Add` used to broadcast every entry synchronously, so a noisy startup (worker init, BBC playlist resolves, Watchtower polling) could fan out hundreds of `log:line` events to every subscribed dashboard in a single tick. A 20-events-per-second token bucket caps the broadcast rate; excess entries still land in the ring (consumers can replay via GET /api/logs) but they do not flood the SSE stream. Bucket refills at the start of each one-second window. Two new tests cover the burst cap and the refill semantics.
- **Rebuild workflow verifies the VPN scaffold survived a base bump (item 38).** `.github/workflows/rebuild.yml` previously rebuilt and pushed whenever `ghcr.io/hotio/base:alpinevpn` advanced its digest, with no smoke check on the resulting image. A new step pulls the freshly-pushed tag and asserts that `openvpn` and `wg` are both present in `PATH`; if either is missing, the workflow fails before the stored digest advances, so the next nightly run will rebuild and recheck instead of pinning a no-VPN image as latest.
- **`incomplete/` staging dir name centralised in `internal/download` (item 39).** Both the producer (`Manager.Enqueue` writes to `<downloadDir>/incomplete/<title>`) and the consumer (`/api/directory` hides any top-level entry called `incomplete`) referenced the literal `"incomplete"` independently. Either side could rename without breaking compilation, but the UI would then leak partial files into the dashboard. The new `download.IncompleteDirName` constant binds both sides, and `directory.go` imports the download package to consume it. Tests intentionally keep the literal `"incomplete"` so they catch an accidental rename of the constant.
- **Claim is released even if `processDownload` panics (item 40).** Audit item 25 (v1.5.2) wrapped `processNext` in `safeProcessNext` with a `defer recover`, but the recover sits two frames up from `processDownload`, past the `dlCancel()` and `m.release(dl.ID)` cleanup lines. A panic inside the download pipeline therefore left an entry pinned in `m.claimed`, and the next `CancelDownload` for that id wasted the full 15 s `cancelWaitTimeout` polling for a release that would never happen. The claimed window is now wrapped in an anonymous function whose `defer` calls `dlCancel + release` first, so the panic still propagates to `safeProcessNext` but the worker state is consistent before it does. New `TestClaimReleasedOnInnerPanic` regression-anchors the pattern.

### Rejected

- **Item 35** (retention-bucket doc drift, LOW). The comment at `worker.go:251-254` explains why the 90 s `downloads` bucket sleep was removed (a `MoveToHistory` race against Sonarr's `mode=queue&name=delete`). The audit framed this as drift between memory and code; a validation read confirmed the comment is correctly marked as historical context and is exactly the kind of "answers a non-obvious WHY" comment the project keeps. Closed as a false positive.

## [1.5.3] - 2026-05-16

Phase 4 frontend polish from the audit-driven cleanup chain. Nine
items, all UX-facing: cancel-button safety, SSE reconnect behaviour,
silent-failure removal, search-race fixes, and a handful of resource
leaks. Backend untouched. Browser smoke verified on an isolated
binary (port 63998, empty config) before merge: SPA mounts, all routes
render, zero console errors or warnings.

### Fixed

- **Cancel button no longer permits double-click + reverts on failure (items 26, 27).** `cancelDownload` tracks an in-flight set keyed by download id. A second click while the cancel is pending is dropped at the guard, and the IconButton renders `disabled` so the user gets visual feedback. The row is optimistically removed from active and queue on confirm; if the DELETE API call throws, the canonical state is refetched via `loadData()` so the row reappears with whatever the server still has. Without the revert, a network blip silently hid a download that was actually still running.
- **SSE reconnect grows exponentially with jitter (item 28).** The fixed 5 s reconnect delay is replaced with `computeReconnectDelay(attempts)`: 1 s base, doubling per failure to a 30 s cap, with +/- 25 % symmetric jitter. The attempt counter resets on a successful open. Without jitter a fleet of clients synchronises on a server restart and reconnect-storms the SSE endpoint at the 5 s mark; with jitter the herd spreads across the window. Pure function exported and unit-tested.
- **Silent error catches replaced with appropriate surfacing (item 29).** `togglePause` and `deleteHistoryItem` are user-initiated, so their failures now show a toast. `loadData` and the two `refreshHistory` background fetches log to `console.error` instead, because toasting on every server hiccup during startup is just noise; the page has empty-state fallbacks that render fine on stale data.
- **Search aborts superseded queries (item 30).** Adds an `AbortController` per debounced search. A new keystroke aborts the previous request and discards its results, so an older slow query cannot race a fresher one into the result list. Clearing the input under the 2-character floor also aborts any in-flight query, and `onCleanup` aborts on unmount. `AbortError` and superseded `TimeoutError` are silently dropped; a `TimeoutError` on the still-active controller surfaces a clear toast.
- **clearAllHistory no longer pretends to succeed when it could not (item 31).** The previous per-row fallback only iterated the visible 20 rows, so a failure against the bulk endpoint quietly left older history intact while reporting success. Drops the fallback; a failure now surfaces an error toast and the user can retry. The bulk endpoint is the only path going forward.
- **api.request() gains AbortSignal support + 30 s default timeout (item 32).** Every `api.*` helper accepts an optional `ApiOptions { signal, timeoutMs }`. Internally `request()` composes the external signal with a 30 s timeout `AbortController` so a request never hangs indefinitely when the server never responds. The external signal is also wired so a caller-driven abort propagates into `fetch`.
- **`speedMap` is bounded across long sessions (item 33).** The module-level speed sample map now drops its entry when a download leaves active state: on a successful cancel, on `download:complete`, and on the `download:failed` removal that fires after the 3 s grace. Previously the map only grew, leaking O(downloads ever seen) entries per browser session.
- **`download:failed` timer cleared on unmount (item 34).** The 3 s `setTimeout` handle that defers the row removal is now tracked in a `pendingTimers` Set; `onCleanup` clears every pending handle on unmount so no `setActive` runs against an unmounted component when the user navigates away within the grace window.

## [1.5.2] - 2026-05-16

Phase 3 tail of the audit-driven cleanup chain. Eight backend items
that did not fit in v1.5.1's window; no frontend or release-shape
changes here, those are Phase 4 (v1.5.x train continues).

### Fixed

- **ffmpeg shutdown gains a SIGTERM grace and progress watchdog (item 11).** `os/exec.CommandContext` defaults to SIGKILL on cancel, which truncates the MP4 mid-write and leaves an unplayable moov-less file behind. `cmd.Cancel` now sends SIGTERM, then `WaitDelay: 5s` escalates to SIGKILL if ffmpeg ignores the soft signal. A new progress watchdog cancels the run if no progress line appears for 60 s (ffmpeg normally emits one every 1 to 3 s on a healthy HLS pull), so a CDN-stalled ffmpeg cannot block a worker indefinitely.
- **Download workers survive a panic (item 25).** Each tick's `processNext` is now wrapped in `safeProcessNext` with a `defer recover`. A panic in the download pipeline (a nil deref, a third-party library bug) is logged with a stack trace and the worker continues with the next job, instead of silently dying and underprovisioning the manager until restart. `max_workers` is documented as start-time-only; resizing the pool at runtime is out of scope for v1.5.x.
- **`bbc.Client.Get` and `Head` now route through their context-aware variants (item 12).** `doWithRetry` was a duplicate of `doWithRetryCtx` that ignored caller cancellation. `Get` and `Head` now delegate to `GetCtx` and `HeadCtx` with `context.Background()`, removing ~40 lines of duplication and letting any future call site swap to the Ctx form without behaviour change.
- **SSE Hub no longer closes channels on `Unsubscribe` (item 15).** Closing a channel that another goroutine might still hold a reference to is a Go anti-pattern; the RWMutex serialised the map mutation correctly but a misbehaving subscriber could in principle race the close. `Unsubscribe` now just deletes from the map and lets the runtime reclaim the channel once the subscriber drops its own reference.
- **`BrowseFresh` deadline back to 5 s and `errors.Join` on total failure (item 18).** The v1.3.0 changelog committed to a 5 s deadline derived from the request context. The code had drifted to 10 s, eating a third of Sonarr's 30 s RSS budget on a slow pool. Restored. When all three editorial pools fail, the handler now returns `errors.Join` of every pool's error rather than only the lowest-priority slot, so the caller can distinguish a deadline (all three time out at once) from a single-pool 404 or an upstream-wide outage.
- **SAB delete preserves the cancelled row in history (item 20).** The `mode=queue&name=delete` path called `Manager.CancelDownload` (which since v1.4.1 ends with `DeleteDownload`) and then `MoveToHistory`, which always failed because the row was already gone. The fallback `DeleteDownload` was a no-op and Sonarr never saw the entry in history, so it kept rediscovering the same release on every RSS sync. The handler now snapshots the row before the cancel and writes the snapshot to history with `Status=failed` afterwards so Sonarr can mark it as a rejected grab and move on.
- **`Enqueue` gains a mutex over its lookup and insert window (item 21).** Sonarr's RSS sync plus an interactive search firing the same release in under 1 ms could both observe "no existing row" and both insert, producing duplicate downloads pointing at the same `incomplete/` directory. `enqueueMu` serialises the `FindDownload` + `FindHistory` + `PutDownload` sequence; uncontended in steady state, only matters under search-storm conditions.
- **Newznab handler honours Sonarr's `limit=N` query parameter (item 24).** The caps XML advertises `max="100"` but the server previously returned whatever the filter and probe pipeline produced (up to ~100 items), leaving Sonarr to truncate on its end. `parseLimitParam` validates `?limit=N`, clamps to the advertised 100, and `writeResultsRSS` trims the rendered item list before emit.

### Known issues remaining from the audit

Phase 4 frontend polish (items 26 to 34) and Phase 5 hygiene (items
35 to 40) remain outstanding for the v1.5.x train. Codex Criticals 4
(wire `authenticate()` into all `/api/*` routes) and 5 (remove
`api_key` from `/api/config` GET response) are deferred to v1.6.0
because both depend on a SPA setup-wizard rework to capture and persist
the apikey in localStorage.

## [1.5.1] - 2026-05-15

### Fixed

- **Release titles with accented characters survive sanitisation (item 16).** `sanitiseForTitle` previously stripped every non-ASCII letter via an ASCII-only regex, so titles like "Beyoncé Live" or "Hôtel du Nord" emerged as "Beyonc.Live" or "Htel.du.Nord" and Sonarr failed to name-match. Adds a Unicode-fold pass for ~70 common Western-European characters (Latin ligatures, accented vowels, smart quotes, en/em dashes) before reUnsafe runs.
- **HTTP server gains slowloris and idle-timeout protection (item 14).** `ReadHeaderTimeout: 10s` and `IdleTimeout: 120s` are now set explicitly. `WriteTimeout` stays 0 because `/api/events` is a long-lived SSE stream.
- **Shutdown order corrected and bounded (item 13).** `srv.Shutdown` now runs before worker cancellation so no new requests are accepted while workers drain. `mgr.Stop` is wrapped in a 15s `waitWithTimeout` so a hung ffmpeg cannot block the container from exiting indefinitely.
- **`CancelDownload` clears its cancelled-map entry (item 10).** Previously the map grew one entry per cancel for the lifetime of the process. The synchronous wait introduced in v1.4.1 already closed the rezombie window the flag protected against, so the entry only needs to live during the short worker-shutdown handshake.
- **Newznab GUIDs survive a colon in the iBL version field (item 17).** `EncodeGUID` now packs `(pid, quality, version)` through `url.Values` inside base64 instead of a colon-separated layout. Legacy colon-format GUIDs still decode so Sonarr's NZB cache from earlier versions keeps resolving.
- **`finaliseDownload` falls back to copy+remove on EXDEV (item 9).** `os.Rename` raises EXDEV when `incomplete/` and the final `downloadDir` sit on different filesystems (tmpfs staging, NFS sub-mounts, bind-mounts to separate volumes). Cross-device renames now copy the file across and remove the source, preserving the v1.4.0 incomplete/complete folder promise on those layouts.
- **PUT `/api/config` caps request body at 64 KiB and rejects unknown fields (item 22).** Defends against an OOM via a megabyte payload and rejects extra keys that try to wedge past the JSON decoder.
- **`sanitiseFilename` strips leading dots from titles (item 23).** A title like `.ssh` can no longer produce a dot-prefixed (hidden) directory under `<downloadDir>/incomplete/`. Mid-string dots stay (e.g. `My.Show.S01E01`).
- **Country-tag whitelist widened (item 19).** The v1.4.0 fixed list (UK/US/AU/CA/NZ/IE) missed GB, IN, ZA, and other ISO codes TVDB uses for disambiguation. Now covers ~30 common country and region codes; arbitrary 2-letter parens like `(XY)` still survive.

### Known issues remaining from the audit

Items 11 (ffmpeg SIGTERM grace before SIGKILL), 12 (`bbc.Client` non-context retry path), 15 (SSE Hub close race), 18 (BrowseFresh deadline + errors.Join), 20 (SAB-shim cancel routing), 21 (Enqueue concurrency lock), 24 (caps `limit` query param honour), 25 (worker pool resize + panic recovery) are tracked for a follow-up cleanup release. Phase 4 frontend polish (items 26-34) and Phase 5 hygiene (items 35-40) are also outstanding.

## [1.5.0] - 2026-05-15

### Added

- **Newznab apikey enforcement.** Every Newznab operation except `t=caps` (which Sonarr probes before attaching the key) now requires a valid `apikey` query param or `Authorization: Bearer` header. Wrong or absent key returns a 401 with a Newznab-standard `<error code="100" description="Invalid API Key"/>` envelope. Closes the still-open Codex C3 finding from 2026-04-04.
- **CSRF origin check on mutating `/api/*` routes.** State-changing methods (`POST`, `PUT`, `PATCH`, `DELETE`) now compare the request `Origin` header to the listening host and refuse cross-origin browser requests. Same-origin browsers, origin-less clients (`curl`, Sonarr, SABnzbd), and safe methods (`GET`, `HEAD`, `OPTIONS`) pass through unchanged. This is defence-in-depth against browser CSRF; full apikey-based protection on `/api/*` is tracked for v1.6.0.

### Changed

- **API key startup log no longer leaks the secret suffix.** Previously every startup logged `apiKey[:4] + "..." + apiKey[-4:]` (8 of 32 hex chars, 25% of the secret). Since `/api/logs` is served without authentication, that gave any LAN visitor a quarter of the key on read. The log now emits only `prefix=<first 4 chars>` as a configuration-presence breadcrumb. Closes the Codex C2 follow-on log-leak.

### Known issues

- **`/api/*` is still unauthenticated for read endpoints.** Wiring the existing `authenticate()` helper into every route requires a frontend setup wizard rework so the SPA can capture, persist, and send the apikey on every request. Tracked as a v1.6.0 backlog item. The CSRF Origin check above is the interim mitigation for browser-based attackers; LAN-direct API attackers are still gated by the host-level UFW rules.
- **`/api/config` GET still includes `api_key`.** The frontend `Settings` page renders the key for the operator to copy into Sonarr's indexer config. Removing it depends on the SPA being able to authenticate against a separate apikey endpoint, which is the same v1.6.0 setup-wizard work.

## [1.4.1] - 2026-05-15

### Fixed

- **Sonarr indexer regression after v1.4.0 upgrade ([#39](https://github.com/Will-Luck/iplayer-arr/issues/39)).** v1.4.0 reused the existing `quality` config key but flipped its meaning from "download quality picker" to "max quality advertised to Sonarr". Upgraders whose persisted value was no longer in the option set (legacy labels like `Default`, or any non-current value) had their RSS fallback set silently clamped to one quality variant per PID, starving Sonarr's discovery pass. A new startup migration (`migrateQualityConfig` in `cmd/iplayer-arr/main.go`) normalises any persisted `quality` value that isn't in `QUALITY_CEILING_OPTIONS` (`any`, `1080p`, `720p`, `540p`, `396p`) to `any`. Empty values are left alone so the defaults table applies at read time. Idempotent; runs once per process start. The Config page also self-corrects on mount as a belt-and-braces fallback for stale state.
- **Cancel button left orphan partial files and raced the worker.** `CancelDownload` previously called `cancel()` on the worker context then `DeleteDownload` immediately, returning before ffmpeg had exited. The DB row vanished while the worker was still mid-write to `incomplete/<title>/*.mp4`, accumulating partial mp4s on the NFS mount on every cancel. Cancel now polls until the worker releases its claim (up to 15s) and then removes the output directory, but only when it sits under `<downloadDir>/incomplete/`. A completed download whose dir was already moved by `finaliseDownload` is preserved.

## [1.4.0] - 2026-05-11

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
