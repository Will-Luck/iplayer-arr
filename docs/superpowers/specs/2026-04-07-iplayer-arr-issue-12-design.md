# Issue #12: Permission Mode Regression and Speculative 1080p Releases

**Date:** 2026-04-07
**Status:** Approved (revised after Codex review on 2026-04-07)
**Issue:** [GiteaLN/iplayer-arr#12](http://192.168.1.57:62400/GiteaLN/iplayer-arr/issues/12)
**Target release:** v1.1.0

## Revision history

- **2026-04-07 (initial):** First draft of the design.
- **2026-04-07 (post-review round 1):** Codex review surfaced four issues against the live code, all four verified as real:
  1. **Context propagation:** the proposed `context.WithTimeout` cap in the prober was decorative — `playlist.Resolve` and `MediaSelector.Resolve` accept no context, so the inner HTTP calls (60s playlist with retries × 3, plus 4 mediaselector attempts × 20s with retries × 3) ignore the parent context entirely. Real worst-case per probe could approach 4-7 minutes. Fix: add `*Ctx(ctx, ...)` variants to both resolvers and to `bbc.Client`.
  2. **Hidden FHD variant:** the prober walking only `streams.Video` heights from the mediaselector XML would never see the unlisted `video=12000000` 1080p variant that BBC hosts but omits from the manifest. The downloader already probes this hidden variant in `internal/download/ffmpeg.go::resolveHLSVariant`. Without the same probe, the fix would replace fake-1080p-for-everyone with no-1080p-for-anyone (strictly worse). Fix: extract a shared `Client.ProbeHiddenFHD(ctx, ...)` helper, used by both the existing downloader path and the new prober.
  3. **Phantom override path:** the spec built a `len(prog.Qualities) > 0` override-takes-precedence branch on a feature that does not exist. `ShowOverride` has no quality fields, `Programme.Qualities` is never set anywhere in the repo, and the existing `if len(prog.Qualities) > 0` branch in `search.go:163` is dead code with zero test coverage. Fix: drop the override branch entirely (there is no override path to preserve), drop `TestSearch_OverrideTakesPrecedenceOverProbe` from the test list, document the dead-code removal as part of the bug fix.
  4. **Bucket creation inconsistency:** the spec said "lazy on first Put" in one place and implied eager creation in `store.Open()` in another. The existing pattern in `internal/store/store.go:28-36` is eager — every bucket is added to a slice and `CreateBucketIfNotExists` is called at startup. Fix: follow the existing pattern, drop the lazy-creation claim, drop `TestQualityCache_BucketCreatedOnFirstUse` from the test list.
- **2026-04-07 (post-review round 2):** A second Codex pass against the revised spec surfaced four more issues. All four verified as real, plus one stale sentence left over from round 1:
  5. **`ProbeHiddenFHD` signature is too narrow.** The round-1 fix introduced the shared helper with a `bool` return — fine for the downloader (which has no cache and doesn't care why the probe failed), but wrong for the prober. Prober step 7 says "any error in steps 3-5 → result is nil, no cache write", so the prober needs to distinguish "definitive no 1080p" from "transient HEAD failure"; with a bool, a transient failure would be cached as "no 1080p" forever. The downloader also needs the actual `fhdURL` on success; `internal/download/ffmpeg.go:117-127` returns it directly and a bool-only API can't preserve that. **Fix:** change the return to `(fhdURL string, found bool, err error)`. Downloader uses `fhdURL` on success and falls back on either `!found` or `err != nil` (byte-for-byte the existing behaviour). Prober treats `found && err == nil` as "prepend 1080", `!found && err == nil` as "definitive no, safe to cache", and `err != nil` as "probe failure, no cache write". Two new tests cover the new error paths (`TestPrefetch_FHDProbeError_ReturnsNilNoCacheWrite`, `TestProbeHiddenFHD_MasterPlaylistFetchFails_ReturnsError`).
  6. **Duplicate 1080p releases.** Round 1 had the prober unconditionally prepend `1080` to the heights list if the FHD probe succeeded. Problem: `mediaselector` itself already reports `1080` for a minority of shows (prestige drama, recent BBC Studios productions). For those shows the heights list already contained `1080` after step 4's dedupe, and step 5's unconditional prepend made it `[1080, 1080, 720, 540]`. `heightsToTags` has no dedupe pass and the RSS writer emits one `<item>` per tag, so the RSS response would have contained two `1080p` items with the same GUID — broken by Sonarr. **Fix:** step 5 now short-circuits entirely if `1080` is already present in the step-4 heights. The manifest has already advertised it, the unlisted-1080p quirk doesn't apply, and the probe is skipped (which is also free latency on the hot path for every real-1080p show). New test `TestPrefetch_1080InManifest_SkipsFHDProbe` covers the regression.
  7. **Stale `main.go` snippet.** The file-change list correctly said `main.go` should construct the prober with `bbcClient` as the FHD prober, but the illustrative snippet was still 5-argument `NewQualityProber(playlist, ms, st, concurrency, timeout)`, which no longer compiles against the 6-argument `NewQualityProber(playlist, ms, fhd, st, concurrency, timeout)` signature introduced in round 1. **Fix:** snippet updated; explicit note added that `bbcClient` (already constructed at `cmd/iplayer-arr/main.go:62`) satisfies the `fhdProber` interface via its new method and needs no extra wiring.
  8. **Context-wrapper sample disagreed with its own prose.** The round-1 wrapper example showed `return r.ResolveCtx(context.Background(), pid)` but the prose said "the wrapper preserves the old behaviour by passing a 60s timeout context". Real code check confirmed the asymmetry Codex spotted and raised it further: `playlist.go:40` uses `client.GetWithTimeout(url, 60*time.Second)` (60s cap) but `mediaselector.go:89` uses bare `client.Get` (no per-call cap). The two wrappers therefore **cannot** share one sample without silently changing one method's behaviour. **Fix:** spec now shows two explicit wrappers — `PlaylistResolver.Resolve` wraps with a 60s `context.WithTimeout` (preserves existing cap), `MediaSelector.Resolve` wraps with bare `context.Background()` (preserves existing no-per-call-cap behaviour). Both are documented inline with the reason and the live-code line reference.
  9. **Stale "created lazily on first Put" in the migration section.** Round 1 fixed bucket creation to be eager (see issue 4 above) and updated the BoltDB section at line 110, but a single sentence in the Migration and rollout section at line 565 still said "created lazily on first `Put`". **Fix:** sentence rewritten to match the eager-creation path and reference the live-code pattern at `internal/store/store.go:28-36`.
- **2026-04-07 (post-review round 3):** A third Codex pass surfaced four more issues. Three were real and verified against the live code; one was a stale-line-number false positive caused by Codex reading a snapshot from before the round-2 edits landed.
  10. **Downloader-side refactor was un-implementable as written.** The round-2 spec said `resolveHLSVariant` should call `bbcClient.ProbeHiddenFHD(context.Background(), masterURL)` and showed an inline snippet that referenced a `bbcClient` variable. Verified against the live code: `FFmpegJob` (`ffmpeg.go:55-59`) has no client field, `RunFFmpeg` only takes `(ctx, job)` (`ffmpeg.go:136`), `resolveHLSVariant` is a package-level function that takes only `masterURL string` (`ffmpeg.go:65`), and the worker constructs jobs without a client (`worker.go:133`). There is no `bbcClient` variable in scope inside `resolveHLSVariant` and no plumbing path to get one there, so the snippet would not compile. The spec also used `context.Background()` instead of the existing `ctx` already threaded through `RunFFmpeg`, which would have silently dropped cancellation propagation through the new shared helper. **Fix:** explicit five-step plumbing change documented in the helper section: (1) add a narrow `downloaderFHDProber` local interface in `internal/download/ffmpeg.go` (so unit tests can inject a fake without importing `bbc`), (2) add a `FHDProber downloaderFHDProber` field to `FFmpegJob`, (3) change `resolveHLSVariant` signature to `(ctx context.Context, prober downloaderFHDProber, masterURL string) string` and use the passed ctx, (4) update `RunFFmpeg` to forward `ctx, job.FHDProber, streamURL` into the new signature, (5) populate `FHDProber: m.client` at `worker.go:133` (the manager already has `*bbc.Client` as `m.client` per `manager.go:28`, so no constructor change is needed). New test file `internal/download/ffmpeg_hls_test.go` (5 tests) covers the plumbing with an injected fake prober, including a regression test for the ctx-propagation requirement.
  11. **Prefetch over-probed for season/episode/date searches.** The round-2 spec applied only the show-name filter to the prefetch list and left `filterDate`/`filterSeason`/`filterEp` in the second (emit) pass. For a request like `Doctor.Who.S14E03` this meant the prober probed every Doctor Who episode IBL returned for the season — typically 12 — even though only S14E03 would emit an RSS row. Twelve probes per request to emit one item is a 12× tax on the latency budget the per-probe timeout was sized against, and undermines goal 3. **Fix:** extract a shared `matchesSearchFilter(prog, wantName, filterDate, filterSeason, filterEp) bool` helper. Replace the round-2 two-pass walk with a single-pass walk that applies *every* filter, builds a `filtered []filteredItem` list of survivors with their decoded `*bbc.Programme`, and constructs `probeItems` from the same list. The emit loop iterates `filtered` and never re-applies filters, so the two passes cannot drift out of sync. Three new handler tests cover the regression: `TestSearch_PrefetchOnlyForFilteredResults_NameFilter` (existing), `TestSearch_PrefetchOnlyForFilteredResults_SeasonEpisode` (new), `TestSearch_PrefetchOnlyForFilteredResults_DailyDate` (new). One direct table-driven test (`TestMatchesSearchFilter_TableDriven`) locks the helper independently.
  12. **`ProbeHiddenFHD` contract did not say which non-200 was cacheable.** The round-2 doc said `("", false, nil)` on "a definitive non-200" but didn't enumerate which status codes counted. Without a strict rule, an implementer could reasonably treat 503 the same as 404, and a transient BBC CDN blip during a season's first cold-cache search would lock that show into permanent lower quality until manual cache invalidation. **Fix:** the contract is now narrow and explicit. Only HTTP 404 and 410 on the HEAD return `("", false, nil)` (the BBC CDN explicitly telling us "this programme has no FHD variant", same semantics as on a normal web 404). Everything else non-200 — 429, 5xx, 401/403, network errors, parse errors, ctx cancellation — returns `("", false, err)` so the prober skips the cache write and the next search retries. The "no `video=N` line in the master playlist" case is also explicitly cacheable as `("", false, nil)` because that's a *structural* absence (the rewrite trick cannot apply at all), not a transient one. New tests `TestProbeHiddenFHD_Head404_ReturnsDefinitiveNoFound`, `TestProbeHiddenFHD_Head410_ReturnsDefinitiveNoFound`, `TestProbeHiddenFHD_Head429_ReturnsError`, and `TestProbeHiddenFHD_Head503_ReturnsError` lock the rule.
  13. **(False positive — no fix.)** Codex reported the `main.go` snippet was still 5-argument at line 393. Verified against the current spec file: line 393 is dead-code prose and the actual snippet at the current line 448 is correctly 6-argument with `bbcClient` — my round-2 edit landed and Codex was reading a stale copy of the file from before that edit. No action taken; recorded here so future revisions don't undo a fix that already exists.
- **2026-04-07 (post-review round 4):** A fourth Codex pass surfaced four more issues. All four verified as real against the live code via jcodemunch (per the mandatory code-exploration rule in CLAUDE.md).
  14. **Wrong Programme type in the round-3 snippet.** The single-pass `writeResultsRSS` rewrite I added in round 3 typed `matchesSearchFilter`'s `prog` parameter and the `filteredItem.prog` field as `*bbc.Programme`. There is no `bbc.Programme` type — verified via jcodemunch's `search_symbols` on the repo. The actual type is `*store.Programme`, defined in `internal/store/types.go:35`, and the existing `iblResultToProgramme` helper returns `*store.Programme` (see `search.go:231`). Following the round-3 spec literally would have produced a compile error on the first `matchesSearchFilter` call. **Fix:** all references updated to `*store.Programme`, with an inline comment explaining why (`Programme` is the persistence model, so it lives in `store`, not `bbc`). Added an explicit note on the `TestMatchesSearchFilter_TableDriven` test row that it takes `*store.Programme` and doubles as a compile-time guard for this finding.
  15. **Single-pass prefetch missed PID dedupe.** jcodemunch surfaced the full `IBL.Search` method at `internal/bbc/ibl.go:49-128`: for `type == "episode"` results it appends directly, and for brand/series results it expands via `ListEpisodes(r.ID)` and appends every episode, without any dedupe between the two paths. A popular-show search like `Doctor Who` can therefore return the same PID twice — once as a direct episode hit and once inside a brand expansion — and the round-3 prefetch design would have dispatched two prober workers against the same PID in parallel, racing them against the first cache write. The RSS response would also have emitted duplicate items with matching GUIDs, breaking Sonarr. **Fix:** added a `seen := map[string]struct{}` guard inside the single-pass walk in `writeResultsRSS`. Both `probeItems` and `filtered` are built from the first occurrence of each PID; duplicates are silently dropped. Cost: one map lookup per IBL result, no allocation for the common case where no duplicates exist (the dedupe map never grows past the size of the unique-PID set). New handler test `TestSearch_DuplicatePIDFromBrandAndEpisode_ProbesOnce` locks the behaviour with a mock IBL that injects the same PID via both paths.
  16. **`ProbeHiddenFHD` selection rule was looser than the inline path it replaces.** The round-3 doc said "finds any variant URL containing a `video=N` segment", but the live `resolveHLSVariant` at `internal/download/ffmpeg.go:80-93` (verified via jcodemunch) is stricter: it walks every `#EXT-X-STREAM-INF:` line, tracks `bestBW := 0; bestURL := ""`, and rewrites **only** the URL associated with the highest BANDWIDTH attribute seen. An implementer reading "any variant" literally could pick the first `video=N` line in the manifest, and on BBC CDN configurations where low-bandwidth and high-bandwidth variants live on different base paths, the rewrite would land on a path that doesn't host the hidden FHD at all — silently regressing the downloader's existing 1080p detection. **Fix:** the contract now enumerates the selection rule as a numbered six-step procedure that quotes the live-code logic line by line (master-playlist fetch via `c.GetCtx`, highest-BW selection, relative-URL resolution against the master playlist base, `video=N` absence check, regex rewrite, HEAD probe). The doc explicitly calls the downloader's inline code the source of truth and instructs implementers to match it byte-for-byte. Two new `fhdprobe_test.go` tests lock the rule: `TestProbeHiddenFHD_PicksHighestBandwidthVariant` (three variants in the manifest, asserts the highest-BW URL is the one rewritten) and `TestProbeHiddenFHD_RelativeVariantURL_ResolvedAgainstBase` (variant URL is a relative path, asserts the base resolution matches `ffmpeg.go:104-110`).
  17. **Migration section cost estimate was stale.** A single sentence in "Migration and rollout" still said "~6-30s for a 30-episode season", left over from an early draft before the per-probe cost budget was tightened in round 2 to include the FHD probe step (~4.5s per probe) and before the round-3 episode-specific-search optimisation. The cost table earlier in the document correctly says ~18s happy / ~80s worst case. **Fix:** the migration bullet now matches the cost table and additionally notes the round-3 collapse for episode-specific searches (a `SxxEyy` search probes exactly one PID thanks to `matchesSearchFilter` + PID dedupe, so the cold-cache cost is a single ~4.5s probe, not 6-30s).
- **2026-04-07 (post-review round 5):** Fifth Codex pass. Codex explicitly noted "I do not see any remaining high-severity design bugs in this revision"; all three findings are Medium/Low. All verified real via jcodemunch's `search_symbols` and `search_text` on the repo.
  18. **`DeleteQualityCacheByShow` didn't encode the repo's existing show-name normalisation rule.** jcodemunch surfaced `normaliseShowName(name) = strings.ToLower(strings.TrimSpace(name))` at `internal/store/overrides.go:10` and confirmed `PutOverride` calls it before writing the bucket key (`overrides.go:14`). The round-4 quality_cache spec stored `ShowName` raw, which means a future v1.2 refresh-button UI that normalises user input (reasonable) would silently fail to match cache entries that were written with `prog.Name` in its original casing. **Fix:** the `QualityCache` schema doc now explicitly states `ShowName` is stored normalised (lower-cased + trimmed) via the existing `normaliseShowName` helper. `PutQualityCache` applies the normalisation at write time; `DeleteQualityCacheByShow` normalises its argument at read time. An explicit "ShowName normalisation contract" subsection was added alongside the methods-list so implementers can't miss it. Two new tests in `quality_cache_test.go` (now 6 tests total, up from 4): `TestQualityCache_PutNormalisesShowName` locks the write-path normalisation, and `TestQualityCache_DeleteByShow_CaseInsensitive` exercises the refresh-button code path with three differently-cased inputs ("DOCTOR WHO", " doctor who ", "Doctor Who") and asserts all three succeed.
  19. **Round-4 dedupe test description asserted the wrong RSS invariant.** The description read "RSS emits one item for it", but the live search-handler loop at `internal/newznab/search.go:170` emits one `<item>` block **per quality tag** (verified by jcodemunch's `search_text` on `range qualities`): for a PID with heights `[1080, 720, 540]` the loop produces three items with GUIDs `EncodeGUID(pid, "1080p", "original")`, `EncodeGUID(pid, "720p", "original")`, `EncodeGUID(pid, "540p", "original")`. A test written to the round-4 description (`assert.Equal(t, 1, len(items))`) would fail after the PID-dedupe fix lands, not because the fix is wrong but because the assertion encodes the wrong invariant. **Fix:** the test description now explicitly asserts "exactly one set of quality items for the deduped PID (N items where N is the number of quality tags)" and "every GUID in the RSS response is unique" — the invariant being protected is *no duplicate GUIDs*, not *a specific item count*. The test body (not shown in the spec) should use `set := map[string]bool{}; for _, item := range items { if set[item.GUID] { t.Errorf("duplicate GUID: %s", item.GUID) }; set[item.GUID] = true }` to encode this directly. Referenced inline in the revised test row so the implementer can't miss the intent.
  20. **Stale file-count summary.** The "Total: N new + M modified" parenthetical at the end of the file-change section still said "10 new + 11 modified = 21 files" even though round 3 added `internal/download/ffmpeg_hls_test.go` and bumped new files to 11 in the preceding list. **Fix:** updated to "11 new + 11 modified = 22 files" and added the `ffmpeg_hls_test.go` file as the explicit reason for the increase, so a reader comparing the list to the summary can see why the count moved.
- **2026-04-07 (post-review round 6 — spec frozen):** Sixth Codex pass returned zero new correctness or compile findings. Codex's exact words: "No new correctness or compile findings in this revision. The earlier review issues now look addressed." Only residual observation was a drift risk from the two separate copies of the highest-bandwidth HLS variant selection logic (one inside `resolveHLSVariant`, one inside `ProbeHiddenFHD`). Codex explicitly framed this as "not a blocker" and "the main drift risk left if the implementation wants a stricter single source of truth". Findings-per-round curve across all six passes: 4 → 5 → 4 → 4 → 3 → 0. Decision: declare the spec frozen and move to the implementation plan. The drift risk is documented as an intentional v1.1.0 trade-off in the Non-Goals section, with the consolidation path (return `(bestURL, fhdURL, found, err)` from `ProbeHiddenFHD`) noted for any future release that decides the maintenance cost outweighs the shipping momentum. Total real bugs caught across six rounds: 20, plus one false positive. 95% precision.

## Problem

Issue #12 reports two unrelated bugs that surfaced together for an UNRAID user on first install. They share an issue thread because both contribute to "downloads land but Sonarr cannot import them", but they have nothing in common beyond that symptom and must be fixed separately.

### Bug 1: hardcoded `0o755` directory mode

`internal/download/worker.go:121` and `cmd/iplayer-arr/main.go:93` both call `os.MkdirAll(..., 0o755)`. The hotio base image ships `UMASK=002` so that *arr stack siblings (running as different users in the same group) can move and delete files after import. `os.MkdirAll` is subject to umask, but umask only **removes** bits — it cannot add a group-write bit that the literal mode does not contain. Result: per-show download directories come out `rwxr-xr-x` regardless of `UMASK` setting, and Sonarr running as a different user (e.g. UNRAID's `nobody:users` = 99:100 against iplayer-arr's default `1000:1000`) gets `r-x` on the directory and cannot delete the source after a copy-then-delete import.

### Bug 2: speculative 1080p releases

`internal/newznab/search.go:162` advertises a hardcoded `["1080p", "720p", "540p"]` quality list for every BBC programme returned by an IBL search, regardless of whether BBC actually delivers 1080p for that show. BBC's catalogue has 1080p only for a small slice of prestige drama, recent BBC Studios productions, and films. Soaps, current affairs, children's content, and most catch-up are 720p only.

The downstream effect is a permanent quality conflict on import:

1. `writeResultsRSS` emits `EastEnders.S42E01.1080p.WEB-DL...` and `EastEnders.S42E01.720p.WEB-DL...` to Sonarr.
2. Sonarr picks the (fake) 1080p release, queues it, and asks iplayer-arr to download it.
3. `processDownload` calls `pickStream` which falls back to "closest available" (720p, because nothing else exists).
4. ffmpeg downloads the 720p variant but writes it to a file named `...1080p.WEB-DL...mp4`.
5. Sonarr's import sees the `1080p` filename, attempts to qualify it as 1080p, finds the file is actually 720p height, and either rejects the import or imports it with a permanent quality mismatch flag.

## Goals

1. **Bug 1:** download directories must be group-writable when umask permits (`0o775` literal). Add a regression test that catches a future revert.
2. **Bug 2:** Newznab releases must accurately reflect the qualities BBC actually offers for each programme. No speculative 1080p.
3. **Bug 2:** search latency must remain acceptable for both manual searches (rare, user-facing) and automated Sonarr RSS sync (frequent, background).
4. **Bug 2:** must survive iplayer-arr restarts. The first cold-cache search of a show is the worst case; we should pay it once per show, ever.
5. Both bugs ship in a single PR with two commits, released as v1.1.0.

## Non-Goals

- Refresh button in the iplayer-arr UI for invalidating cached quality data (deferred to v1.2; the store-layer methods exist but no REST endpoint or UI wiring).
- Background re-probe job, TTL on cache entries, or any other automatic invalidation. BBC content masters are append-only and effectively immutable; the rare remaster case is handled by a future manual refresh, not by periodic background work.
- Per-VPID granularity. Cache is keyed by PID (programme identity), not VPID (version programme identity). PID is what Sonarr searches against; VPID is an implementation detail of how we get the answer.
- A per-show user-override path for advertised qualities. **Important context:** the existing `len(prog.Qualities) > 0` branch in `internal/newznab/search.go:163` is **dead code**. `Programme.Qualities` is declared on the type but never set anywhere in the repo, `ShowOverride` has no quality fields at all, and zero tests reference this branch. As part of this fix the dead branch is removed; the probe is the only quality source. If a future user-override feature is added, the right place is a new `ShowOverride.QualityOverride []string` field, not the orphan `Programme.Qualities` slice.
- Wiring up the latent `QualityOption.Synthetic bool` field at `internal/store/types.go:64`. The field is left exactly as it is — declared but unused. It is part of the `QualityOption` struct that lives on `Programme`, which the dead branch above also touches. Removing it would be unrelated cleanup.
- Probe metrics or observability beyond DEBUG/INFO/WARN log lines that flow through the existing ring buffer.
- An `IPLAYER_PROBE_DISABLED` env var. The prober's failure mode is graceful degradation to the safe `[720p, 540p]` fallback, so a misbehaving prober only ever degrades to the safest possible state, never to a worse one. A disable flag would create a code path that is only exercised in emergencies and is therefore likely to be broken when needed.
- Unifying the highest-bandwidth HLS variant selection between `resolveHLSVariant` (downloader path) and `Client.ProbeHiddenFHD` (search-time prober). The two live sites will keep their own copies of the `bestBW` walk for v1.1.0. Paired tests (`TestProbeHiddenFHD_PicksHighestBandwidthVariant` on the helper side and the existing resolveHLSVariant coverage on the downloader side) pin both implementations against the same selection rule, so divergence would fail at least one set. If this duplication becomes a maintenance burden in a later release, the consolidation path is to have `ProbeHiddenFHD` return `(bestURL, fhdURL, found, err)` and let `resolveHLSVariant` delegate the whole master-playlist pipeline; that refactor is intentionally deferred. Flagged by Codex review round 6 as a residual drift risk, accepted as a trade-off to ship v1.1.0.

## Design

### Architecture overview

A new component `bbc.QualityProber` owns one job: given a list of PIDs, return the set of available video heights for each, using a BoltDB-backed cache to avoid re-probing the same PID twice. It is instantiated once at startup, shared between any caller that needs probe results, and parallel-bounded by a configurable concurrency limit (default 8).

Call graph:

```
Sonarr
  → handleTVSearch
    → ibl.Search
      → []IBLResult
        → QualityProber.PrefetchPIDs(ctx, items)    ← parallel pool, concurrency 8
            for each PID:
              1. store.GetQualityCache(pid)              ← cache hit: 0 HTTP, return heights
              2. playlist.ResolveCtx(ctx, pid) → vpid   ← cache miss: 1 HTTP (ctx-bounded)
              3. ms.ResolveCtx(ctx, vpid) → streams     ← cache miss: up to 4 HTTP (ctx-bounded)
              4. dedupe + sort heights from streams
              5. if 1080 NOT already in heights AND best HLS variant exists:
                   _, found, err := client.ProbeHiddenFHD(ctx, hlsURL)  ← 1 GET + 1 HEAD (ctx-bounded)
                   if found && err == nil → prepend 1080 to heights
                   if err != nil          → propagate to step 7 (don't cache)
              6. store.PutQualityCache(...)              ← persist forever
              7. on any HTTP/probe error: log WARN, return nil ← graceful degradation, no cache write
        → map[pid] → []int heights
      → writeResultsRSS loop uses heights map
    → emits exactly the qualities BBC actually offers per episode
```

There is no per-show override path. The dead `len(prog.Qualities) > 0` branch in current `search.go` is removed as part of this fix (see Non-Goals).

**The hidden FHD step (5) is critical.** BBC's mediaselector XML manifest does **not** advertise 1080p for many shows that nonetheless have a 1080p stream available at an unlisted `video=12000000` URL. The existing downloader (`internal/download/ffmpeg.go::resolveHLSVariant`) already HEAD-probes this URL to find FHD streams. The prober must do the same probe, otherwise it would only ever cache the lower heights from the manifest and quietly drop real 1080p across the catalogue. This was caught in the Codex review pass.

### Why PID, not VPID

`IBLResult` carries `PID` (programme identity, e.g. an episode page reference) but not `VPID` (version programme identity, the playable thing). To get from PID to actual streams takes two hops: `playlist.Resolve(pid)` returns a `PlaylistInfo` containing the VPID, then `mediaselector.Resolve(vpid)` returns the streams.

If the cache were keyed by VPID, every search would still need to call `playlist.Resolve` just to know which cache key to look up, defeating the purpose of caching. Keying by PID means a cache hit is genuinely zero HTTP. The PID→VPID hop becomes part of the cold path only.

### Data schema

New type in `internal/store/types.go`:

```go
// QualityCache records the heights BBC actually offers for a single PID.
// Populated by bbc.QualityProber on first encounter and reused indefinitely.
// Manual refresh is handled by deletion, not by TTL — BBC content masters
// are append-only and effectively immutable once published.
//
// ShowName is stored in normalised form (lower-cased + trimmed via the
// existing store.normaliseShowName helper at internal/store/overrides.go:10)
// so that DeleteQualityCacheByShow's input matches stored values
// regardless of how the user typed the show name in the future v1.2
// refresh-button UI. The original casing is never needed — the cache
// is consulted by PID, and any display-time show name comes from the
// freshly-decoded *store.Programme, not from this field.
type QualityCache struct {
    PID      string    `json:"pid"`
    ShowName string    `json:"show_name"`     // normalised: lowercase + trimmed
    Heights  []int     `json:"heights"`       // sorted descending, e.g. [1080, 720, 540, 396]
    ProbedAt time.Time `json:"probed_at"`
}
```

New BoltDB bucket: `quality_cache`. Created **eagerly** in `store.Open()` by adding `bucketQualityCache` to the existing slice at `internal/store/store.go:28-36`, matching the established pattern for `bucketDownloads`, `bucketHistory`, etc. No migration needed for existing installs — `CreateBucketIfNotExists` is a no-op for already-existing buckets and creates the new one on first start of v1.1.0.

New methods on `*store.Store` in `internal/store/quality_cache.go`:

```go
GetQualityCache(pid string) (*QualityCache, error)         // returns nil, nil on miss
PutQualityCache(qc *QualityCache) error                    // upsert; normalises qc.ShowName before write
DeleteQualityCache(pid string) error                       // single-entry invalidation
DeleteQualityCacheByShow(showName string) error            // bulk invalidation; normalises input
```

**ShowName normalisation contract.** Both `PutQualityCache` and `DeleteQualityCacheByShow` MUST normalise the show-name string via the existing package-private helper:

```go
// internal/store/overrides.go:10 — already exists, do not redefine.
func normaliseShowName(name string) string {
    return strings.ToLower(strings.TrimSpace(name))
}
```

`PutQualityCache` rewrites `qc.ShowName = normaliseShowName(qc.ShowName)` immediately before the BoltDB write, so every entry in the bucket has a normalised value. `DeleteQualityCacheByShow` normalises its `showName` argument once, then iterates the bucket comparing each entry's stored `ShowName` against the normalised target. This is exactly the pattern `PutOverride`/`GetOverride` already use for `bucketOverrides` (`internal/store/overrides.go:14` and `:25`), so the two stores stay consistent with one another. Without this, a v1.2 refresh button that sends "Doctor Who" would silently fail to clear an entry written from `prog.Name == "doctor who"` (or vice versa) and the user would have no feedback that the refresh did nothing.

The two delete methods exist so a future v1.2 refresh button can wire to them without schema changes.

### Prerequisite: context plumbing in the BBC client and resolvers

Before the prober can enforce a per-probe deadline, the BBC HTTP client and its two resolvers must accept a `context.Context`. The current API does not. **Decision: add `*Ctx` variants alongside the existing methods, and have the existing methods become trivial wrappers that pass `context.Background()`.** This avoids breaking the five existing call sites (worker.go × 2, mediaselector_test.go × 2, playlist_test.go × 1) and lets the prober use the context-aware path directly.

New methods:

```go
// internal/bbc/client.go
func (c *Client) GetCtx(ctx context.Context, url string) ([]byte, error) {
    return c.doWithRetryCtx(ctx, url, c.maxRetry)
}

// internal/bbc/playlist.go
func (r *PlaylistResolver) ResolveCtx(ctx context.Context, pid string) (*PlaylistInfo, error)

// internal/bbc/mediaselector.go
func (ms *MediaSelector) ResolveCtx(ctx context.Context, vpid string) (*StreamSet, error)
```

`doWithRetryCtx` already exists in `client.go:73` — it correctly threads the context into `http.NewRequestWithContext` and aborts on `ctx.Err()` between attempts. We just need a public wrapper. The existing `GetWithTimeout(url, timeout)` creates its own background context and is unchanged.

The existing `Resolve(...)` methods remain as one-line wrappers. The two wrappers are **not** identical, because the two existing methods have different live-code timeout behaviours that must be preserved exactly:

```go
// PlaylistResolver preserves its existing 60s per-call cap.
// playlist.go:40 currently calls client.GetWithTimeout(url, 60*time.Second);
// ResolveCtx drops that constant and trusts the parent context, so the
// wrapper re-introduces the 60s deadline for any caller that hasn't
// migrated to ResolveCtx.
func (r *PlaylistResolver) Resolve(pid string) (*PlaylistInfo, error) {
    ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
    defer cancel()
    return r.ResolveCtx(ctx, pid)
}

// MediaSelector has no per-call timeout in live code today
// (mediaselector.go:89 uses bare client.Get), so its wrapper passes
// context.Background() and inherits only whatever deadline the
// underlying bbc.Client already enforces. Adding an arbitrary timeout
// here would be a behaviour change disguised as a refactor.
func (ms *MediaSelector) Resolve(vpid string) (*StreamSet, error) {
    return ms.ResolveCtx(context.Background(), vpid)
}
```

This is the minimum-impact way to make the timeout claim enforceable inside the prober without changing the behaviour of any existing call site. The search-time prober creates its own `context.WithTimeout(parentCtx, 20*time.Second)` via the `Ctx` variants and the 60s playlist cap never enters the picture there.

### Prerequisite: shared FHD probe helper

BBC's mediaselector XML omits 1080p variants for many shows; the actual stream exists at an unlisted `video=12000000` URL that the existing downloader HEAD-probes in `internal/download/ffmpeg.go::resolveHLSVariant`. The prober must do the same probe, otherwise it would never see real 1080p.

Extract the probe into a new file `internal/bbc/fhdprobe.go`:

```go
package bbc

// ProbeHiddenFHD reports whether BBC hosts an unlisted video=12000000
// 1080p variant for the given HLS master playlist URL.
//
// Selection rule (must match internal/download/ffmpeg.go:80-93
// byte-for-byte, otherwise the downloader's existing 1080p detection
// can silently regress on shows where variant URLs differ by more than
// just the `video=N` segment):
//
//  1. Fetch the master playlist via c.GetCtx(ctx, hlsMasterURL).
//  2. Walk every `#EXT-X-STREAM-INF:` line, extract its BANDWIDTH
//     attribute, and track the single variant URL with the highest
//     BANDWIDTH seen. Ties favour the first occurrence. This is the
//     same selection `resolveHLSVariant` does inline today via
//     `bestBW := 0; if bw > bestBW { bestBW = bw; bestURL = lines[i+1] }`.
//  3. Resolve that best variant URL relative to the master playlist's
//     base directory if it's not already absolute — identical to the
//     downloader path at ffmpeg.go:104-110. This matters because BBC
//     sometimes serves variant URLs as relative paths.
//  4. If the best variant URL does NOT contain a `video=N` segment
//     (DASH-style or legacy format), return ("", false, nil) — the
//     hidden-FHD rewrite trick cannot apply and never will. Do not HEAD.
//  5. Rewrite the `video=N` segment of the best variant URL to
//     `video=12000000` using the same regex the downloader uses:
//     `regexp.MustCompile(\`video=\d+\`).ReplaceAllString(bestURL, "video=12000000")`.
//  6. HEAD-probe the rewritten URL (ctx-honouring).
//
// The key constraint for implementers: the rewrite must be applied to
// the highest-BANDWIDTH variant URL, not the first `video=N` line
// encountered. "Any variant URL" is too loose — low-bandwidth and
// high-bandwidth variants on some BBC CDN configurations live on
// different base paths, and a naive "first match" implementation would
// probe a path that doesn't exist for the high tier. The downloader's
// inline code is the source of truth for this selection; the helper
// simply inlines the same logic.
//
// Returns:
//   - (fhdURL, true,  nil) on HEAD HTTP 200 — caller gets the concrete 1080p URL.
//   - ("",     false, nil) for the two "definitive absence" cases:
//       (a) the HEAD response is HTTP 404 (Not Found) or 410 (Gone) — BBC's
//           CDN is explicitly telling us this programme has no unlisted FHD
//           variant at video=12000000.
//       (b) the master playlist parses successfully but contains no
//           `video=N` BANDWIDTH line — structurally, there is nothing to
//           rewrite, so the hidden-FHD trick cannot apply to this programme
//           and never will (e.g. DASH-only master surfaced as HLS by some
//           quirk). Both sub-cases are safe to cache forever; next refresh
//           is manual via DeleteQualityCacheByShow.
//   - ("",     false, err) for every other failure mode. This includes:
//     HTTP 429 (rate limit), 5xx (upstream error), 401/403 (CDN glitch),
//     any network/transport error, context cancellation, master-playlist
//     GET failures, and master-playlist parse errors (truncated body,
//     garbage response, etc).
//
// Why this asymmetric split: a 404 on the video=12000000 HEAD is the
// BBC CDN explicitly telling us "this variant does not exist for this
// programme". A 429 or 503 is the CDN telling us "try again later".
// Caching the second as a permanent "no 1080p" answer would lock a
// show into lower quality for the lifetime of its cache entry, and
// would be indistinguishable from the real "no 1080p" case when we
// later tried to debug it. The narrow 404/410 rule is the only way
// to make the `("", false, nil)` path safely cacheable.
//
// The three-way return lets the search-time prober distinguish a *real*
// "no 1080p" answer (safe to cache) from a transient HEAD failure (must
// NOT be cached, retry on the next search). The downloader, which has
// no caching concern, simply uses fhdURL when found and falls back to
// the lower-quality stream on either false or err — exactly the
// behaviour of the inline path it replaces.
//
// Used by both internal/download/ffmpeg.go (downloader path) and
// internal/bbc/prober.go (search-time prefetch). Centralised here so
// BBC's hidden-1080p quirk is encoded in exactly one place.
func (c *Client) ProbeHiddenFHD(ctx context.Context, hlsMasterURL string) (fhdURL string, found bool, err error)
```

Refactor `internal/download/ffmpeg.go::resolveHLSVariant` to call the shared helper instead of inlining the probe with bare `http.Head`. This requires a plumbing change the round-2 spec skipped: `resolveHLSVariant` is a package-level function that currently takes only `masterURL string` (`ffmpeg.go:65`) and has no `*bbc.Client` in scope, and `FFmpegJob` (`ffmpeg.go:55-59`) has no client field either. The plumbing change:

1. **Narrow local interface in `internal/download/ffmpeg.go`** — not a dependency on `*bbc.Client` directly, so unit tests in the download package can inject a fake without importing `httptest`:

    ```go
    // downloaderFHDProber is the single method resolveHLSVariant needs
    // from bbc.Client. Kept as a local interface so ffmpeg_test.go can
    // inject a fake; *bbc.Client satisfies this automatically.
    type downloaderFHDProber interface {
        ProbeHiddenFHD(ctx context.Context, hlsMasterURL string) (fhdURL string, found bool, err error)
    }
    ```

2. **Add a `FHDProber` field to `FFmpegJob`** (and therefore to the worker-side construction):

    ```go
    type FFmpegJob struct {
        StreamURL  string
        OutputPath string
        OnProgress func(FFmpegProgress)
        FHDProber  downloaderFHDProber // NEW — satisfied by *bbc.Client
    }
    ```

3. **Change `resolveHLSVariant` signature** to accept ctx + prober (no `context.Background()` — use the existing download ctx that already threads through `RunFFmpeg(ctx, job)`):

    ```go
    func resolveHLSVariant(ctx context.Context, prober downloaderFHDProber, masterURL string) string {
        // ... existing master-playlist fetch and bestURL selection unchanged ...

        if prober != nil && strings.Contains(bestURL, "video=") {
            fhdURL, found, err := prober.ProbeHiddenFHD(ctx, masterURL)
            switch {
            case err != nil:
                log.Printf("1080p probe error: %v", err)
            case found:
                log.Printf("HLS 1080p variant found (unlisted): %s", fhdURL[:min(len(fhdURL), 120)])
                return fhdURL
            }
        }
        return bestURL
    }
    ```

    The `prober != nil` guard keeps the function resilient to job structs constructed in tests (or any future caller) that don't wire the prober through — it falls back to the same `bestURL` the non-probe branch already returns.

4. **Update `RunFFmpeg`** to forward ctx and `job.FHDProber` into the new signature:

    ```go
    // inside RunFFmpeg, replacing the current line 142
    if strings.Contains(streamURL, ".m3u8") {
        streamURL = resolveHLSVariant(ctx, job.FHDProber, streamURL)
    }
    ```

5. **Worker populates the field** at `internal/download/worker.go:133` from the manager's existing `*bbc.Client` (already stored as `m.client` at `manager.go:28`):

    ```go
    job := FFmpegJob{
        StreamURL:  stream.URL,
        OutputPath: outputFile,
        OnProgress: func(p FFmpegProgress) { /* ... unchanged ... */ },
        FHDProber:  m.client, // NEW — *bbc.Client satisfies downloaderFHDProber
    }
    ```

Behaviour is unchanged for the downloader: any error logs and falls through to `bestURL`, exactly the same as the old `if err != nil { log.Printf(...) }` branch at `ffmpeg.go:118-119`. Bonus correctness improvement: the existing inline path uses bare `http.Get`/`http.Head` with no User-Agent rotation and no timeout; the shared helper uses `bbc.Client` so it gets random user-agent and a 20s HTTP timeout for free, and the refactor picks up ctx cancellation (e.g. when the download is stopped from the UI) that the inline path ignored.

### The QualityProber

New file `internal/bbc/prober.go`:

```go
package bbc

// QualityProber resolves BBC iPlayer programmes to their available video
// heights (including BBC's hidden 1080p variant), caching results in the
// store to avoid re-probing the same PID twice. Goroutine-safe; share one
// instance across the process.
type QualityProber struct {
    playlist    pidToVPIDResolver
    ms          vpidToStreamsResolver
    fhdProber   fhdProber
    store       qualityCacheStore
    concurrency int
    timeout     time.Duration   // per-probe deadline, ENFORCED via ctx propagation
}

// pidToVPIDResolver, vpidToStreamsResolver, and fhdProber are narrow local
// interfaces satisfied by *PlaylistResolver, *MediaSelector, and *Client
// via Go's structural typing. Defining them here lets unit tests inject
// fakes without live HTTP or BoltDB.
type pidToVPIDResolver interface {
    ResolveCtx(ctx context.Context, pid string) (*PlaylistInfo, error)
}
type vpidToStreamsResolver interface {
    ResolveCtx(ctx context.Context, vpid string) (*StreamSet, error)
}
type fhdProber interface {
    ProbeHiddenFHD(ctx context.Context, hlsMasterURL string) (fhdURL string, found bool, err error)
}

type qualityCacheStore interface {
    GetQualityCache(pid string) (*store.QualityCache, error)
    PutQualityCache(*store.QualityCache) error
}

// ProbeItem is one input to PrefetchPIDs.
type ProbeItem struct {
    PID      string
    ShowName string
}

// NewQualityProber wires the prober up. concurrency defaults to 8 if <= 0.
// timeout defaults to 20 * time.Second if zero.
func NewQualityProber(
    playlist pidToVPIDResolver,
    ms vpidToStreamsResolver,
    fhd fhdProber,
    st qualityCacheStore,
    concurrency int,
    timeout time.Duration,
) *QualityProber

// PrefetchPIDs probes the given items in parallel and returns a map of
// PID → heights (sorted descending, deduped). Cache hits skip the probe
// entirely. Probe failures map to a nil entry (caller decides how to fall
// back). Honours ctx for cancellation across the entire fetch.
func (qp *QualityProber) PrefetchPIDs(ctx context.Context, items []ProbeItem) map[string][]int
```

**Worker pool semantics.** A buffered channel of `ProbeItem` feeds N workers. Each worker:

1. `store.GetQualityCache(pid)` — hit returns immediately, no HTTP. Logged at DEBUG.
2. Miss: derive `probeCtx, cancel := context.WithTimeout(parentCtx, qp.timeout)` (default 20s). This is now genuinely enforceable because every HTTP call below honours the context.
3. `playlist.ResolveCtx(probeCtx, pid)` — on success, extract VPID.
4. `ms.ResolveCtx(probeCtx, vpid)` — on success, walk `streams.Video`, dedupe heights, sort descending.
5. **FHD probe.** First short-circuit: if the deduped height list from step 4 already contains `1080`, skip this step entirely. The manifest already advertised 1080p, the unlisted-1080p quirk is irrelevant, and an extra probe would only risk emitting a duplicate `1080p` release (heightsToTags has no dedupe pass and the RSS writer emits one item per tag — see Codex finding 6 in Revision history). Otherwise, find the best HLS stream URL in `streams.Video` (highest bitrate, `format == "hls"`) and call `fhdURL, found, err := fhdProber.ProbeHiddenFHD(probeCtx, hlsURL)`:
    - `found && err == nil` → prepend `1080` to the heights list (safe — we just verified `1080` was not already present, so no duplicate is possible).
    - `!found && err == nil` → leave heights unchanged. This is a *definitive* "no 1080p for this show" answer and is safe to cache.
    - `err != nil` → propagate the error to step 7. This was a transient HEAD failure, **not** a "no 1080p" answer, and must not be cached or the next search will get the wrong heights forever.
   `fhdURL` itself is intentionally discarded by the prober — it has no use for the URL, only for the boolean and the error. The downloader is the only consumer that needs the URL.
   If no HLS stream is in the result (DASH-only programme), skip the FHD probe — DASH-only content doesn't have the unlisted-1080p quirk and there's nothing to probe.
6. Persist via `PutQualityCache(&QualityCache{PID: item.PID, ShowName: item.ShowName, Heights: heights, ProbedAt: time.Now()})`. The prober passes the raw `ShowName` from the `ProbeItem` (which came from `prog.Name` on the search side); `PutQualityCache` itself normalises the field via `normaliseShowName` before the BoltDB write, so callers don't need to know about the normalisation rule.
7. On any error in steps 3-5 (including a non-nil `err` from `ProbeHiddenFHD`), log at WARN, write nil into the result map for that PID, do **not** write to cache. The next search retries.

Results are accumulated into a mutex-guarded `map[string][]int` and returned as a regular map.

**Configuration knobs** (env vars, read at startup in `cmd/iplayer-arr/main.go`):

| Var | Default | Purpose |
|---|---|---|
| `IPLAYER_PROBE_CONCURRENCY` | 8 | Worker pool size |
| `IPLAYER_PROBE_TIMEOUT_SEC` | 20 | Per-probe wall-time deadline (now enforceable) |

**Why 20s per-probe timeout, and why it actually works now.** With ctx propagation in place, a 20s deadline genuinely caps the combined cost of `playlist.ResolveCtx` + `ms.ResolveCtx` + `ProbeHiddenFHD` for a single probe. Before the ctx refactor this was decorative (the inner `http.Client` 20s timeout × 3 retries × 4 mediaselector attempts could blow past it without the parent ever knowing). The cancel on timeout aborts all in-flight HTTP calls for that probe and frees the worker for the next item.

**Cold-cache wall time estimates** (30-episode season search, concurrency 8, after the FHD probe was added to the cost budget):

| Probe | Happy path per probe | Notes |
|---|---|---|
| `playlist.ResolveCtx` | ~1.5s | 1 HTTP, single attempt usually succeeds |
| `ms.ResolveCtx` (first attempt) | ~1.5s | 1 HTTP, iptv-all v6 is the happy path |
| Master playlist fetch | ~1s | inside `ProbeHiddenFHD` |
| FHD HEAD probe | ~0.5s | inside `ProbeHiddenFHD` |
| **Combined per probe** | **~4.5s** | (was ~3s before FHD probe was added) |

| Concurrency | Cold-cache season search (30 PIDs) |
|---|---|
| 8 | ~18s happy path (4 batches × ~4.5s), capped at ~80s worst case (4 × 20s timeout) |

Sonarr's default indexer timeout is 100s, so worst case is within budget. Steady state (cache warm): sub-millisecond per PID.

### Search-handler integration

Three changes to `internal/newznab/`:

**1. `handler.go`** — `Handler` gains a `prober` field and `NewHandler` gains a fourth parameter:

```go
type Handler struct {
    ibl       *bbc.IBL
    store     *store.Store
    ms        *bbc.MediaSelector
    prober    *bbc.QualityProber   // new
    onRequest func()
}

func NewHandler(ibl *bbc.IBL, st *store.Store, ms *bbc.MediaSelector, prober *bbc.QualityProber) *Handler
```

**2. `search.go::writeResultsRSS`** — prefetch loop before the existing emit loop, plus a new quality-decision switch inside the emit loop:

```go
// matchesSearchFilter applies every filter that the emit loop applies,
// in the same order. Extracted into a shared helper so the prefetch pass
// and the emit pass cannot drift out of sync. Returns true if the
// programme should appear in the RSS response.
//
// All filters are unchanged from the round-2 spec; this helper just
// hoists them out of the emit loop so the prefetch loop can call them
// too. See Codex round 3 finding 2.
//
// The Programme type is *store.Programme — that's what
// iblResultToProgramme returns (see search.go:231) — not *bbc.Programme.
// There is no bbc.Programme type; Programme is the persistence model and
// lives in the store package.
func matchesSearchFilter(prog *store.Programme, wantName, filterDate string, filterSeason, filterEp int) bool {
    if wantName != "" && !strings.EqualFold(strings.TrimSpace(prog.Name), wantName) {
        return false
    }
    if filterDate != "" {
        return prog.AirDate == filterDate
    }
    if filterSeason > 0 && prog.Series != filterSeason {
        return false
    }
    if filterEp > 0 && prog.EpisodeNum != filterEp {
        return false
    }
    return true
}

func (h *Handler) writeResultsRSS(w http.ResponseWriter, r *http.Request,
    results []bbc.IBLResult, filterSeason, filterEp int, filterDate, filterName string) {

    var items []string
    wantName := strings.TrimSpace(filterName)

    // Single pass: apply *every* filter the emit loop will apply, dedupe
    // by PID, collect the survivors into filteredResults, and build the
    // prefetch list from those exact survivors. Probe budget is therefore
    // spent only on items that will actually emit RSS rows, and never
    // twice on the same PID. For a search like `Doctor.Who.S14E03` this
    // means we probe one PID, not the twelve same-name episodes IBL
    // returned for the season.
    //
    // Why the PID dedupe: IBL.Search (internal/bbc/ibl.go:49) appends
    // direct "episode"-type hits and brand/series-expanded episode lists
    // into `results` without any dedupe (see lines 87-124). A search for
    // a popular show can therefore receive the same PID via two paths —
    // a direct episode match AND a brand-expansion match whose
    // ListEpisodes result contains the same episode. Without the
    // `seen[pid]` guard below, the prober would race two workers
    // against the same PID before the first cache write lands, and the
    // RSS response would emit two items with duplicate GUIDs. The
    // dedupe is cheap (one map lookup per result) and fixes both
    // symptoms in one place.
    type filteredItem struct {
        res  bbc.IBLResult
        prog *store.Programme // store.Programme, not bbc.Programme — the
                              // latter does not exist. iblResultToProgramme
                              // returns *store.Programme (see search.go:231)
                              // because Programme is the persistence model
                              // and lives in internal/store/types.go:35.
    }
    var filtered []filteredItem
    var probeItems []bbc.ProbeItem
    seen := make(map[string]struct{}, len(results))
    for _, res := range results {
        if _, dup := seen[res.PID]; dup {
            continue
        }
        prog := iblResultToProgramme(res)
        if !matchesSearchFilter(prog, wantName, filterDate, filterSeason, filterEp) {
            continue
        }
        seen[res.PID] = struct{}{}
        filtered = append(filtered, filteredItem{res: res, prog: prog})
        probeItems = append(probeItems, bbc.ProbeItem{PID: res.PID, ShowName: prog.Name})
    }

    var probedHeights map[string][]int
    if h.prober != nil && len(probeItems) > 0 {
        probedHeights = h.prober.PrefetchPIDs(r.Context(), probeItems)
    }

    for _, it := range filtered {
        res, prog := it.res, it.prog

        // No need to re-apply filters here — every entry in `filtered`
        // already passed matchesSearchFilter above.

        // GetOverride is still called for the show-name custom-name field
        // and other override-driven behaviour in GenerateTitle. We just
        // don't read prog.Qualities here any more — see below.
        var override *store.ShowOverride
        if h.store != nil {
            override, _ = h.store.GetOverride(prog.Name)
        }

        // Quality decision: probe result > graceful fallback.
        //
        // The previous `if len(prog.Qualities) > 0 { ... }` branch is
        // removed: Programme.Qualities was never set anywhere in the repo
        // and the branch was dead code. If a per-show user override of
        // qualities is added in the future, the right place is a new
        // ShowOverride.QualityOverride []string field, not Programme.
        var qualities []string
        if probedHeights[res.PID] != nil {
            // Successful probe (even if it returned an empty slice — BBC
            // genuinely has no streams for this programme, in which case
            // heightsToTags returns []string{} and we emit zero items for
            // this result. We never lie about a 720p fallback when we know
            // BBC has nothing.)
            qualities = heightsToTags(probedHeights[res.PID])
        } else {
            // No prober wired, OR probe failed (nil result entry). Emit
            // only what BBC universally delivers, never the speculative
            // 1080p that started this bug. The next search will re-probe.
            qualities = []string{"720p", "540p"}
        }
        // ... rest of the loop unchanged (GenerateTitle, GUID, item XML) ...
    }
    // ... rest of writeResultsRSS unchanged ...
}
```

The single-pass design replaces the round-2 spec's two-pass walk. The previous version applied only the show-name filter to the prefetch list, so a `Doctor.Who.S14E03` search would probe every Doctor Who episode IBL returned for the season — twelve probes to emit one RSS item — and undermine the latency budget the per-probe timeout was sized against. The new design walks `results` once, applies *every* filter via `matchesSearchFilter`, and builds the prefetch list from the exact survivors. The emit loop then iterates `filtered`, which is guaranteed to be the same set of items the prefetch pass probed. The two passes cannot drift out of sync because the filter function is the same call site, and the helper itself is independently unit-tested.

**Dead-code removal note:** the branch being deleted is `if len(prog.Qualities) > 0 { ... }`. `Programme.Qualities` is declared on the type at `internal/store/types.go:49` but no code path anywhere in the repo writes to it (verified by grepping `Qualities[: ]*=`). The branch has zero test coverage. Removing it is not a behaviour change — it cannot have ever been entered. The struct field itself is left in place to avoid touching an unrelated type (and because removing it would force a no-op rewrite of `iblResultToProgramme`).

**3. `search.go::heightsToTags`** — new package-private helper:

```go
func heightsToTags(heights []int) []string {
    out := make([]string, 0, len(heights))
    for _, h := range heights {
        switch {
        case h >= 2160:
            out = append(out, "2160p")
        case h >= 1080:
            out = append(out, "1080p")
        case h >= 720:
            out = append(out, "720p")
        case h >= 540:
            out = append(out, "540p")
        case h >= 396:
            out = append(out, "396p")
        }
    }
    return out
}
```

**4. `cmd/iplayer-arr/main.go`** — five new lines to construct the prober and pass it to `NewHandler`:

```go
probeConcurrency := envIntDefault("IPLAYER_PROBE_CONCURRENCY", 8)
probeTimeout := time.Duration(envIntDefault("IPLAYER_PROBE_TIMEOUT_SEC", 20)) * time.Second
prober := bbc.NewQualityProber(playlist, ms, bbcClient, st, probeConcurrency, probeTimeout)
nzbHandler := newznab.NewHandler(ibl, st, ms, prober)
```

`bbcClient` is the existing `*bbc.Client` already constructed at `cmd/iplayer-arr/main.go:62` (`bbcClient := bbc.NewClient()`). It satisfies the `fhdProber` interface via its new `ProbeHiddenFHD` method, so no extra wiring is needed beyond passing the variable through.

`envIntDefault` is a tiny helper added to the same file (or to a small env helper file if there are already several).

### Bug 1 fix

New file `internal/download/dirs.go`:

```go
package download

import "os"

// downloadDirMode is the directory mode used for per-show download dirs.
// Must include the group-write bit so that an *arr stack running as a
// different user in the same group (e.g. UNRAID with PUID=99 PGID=100
// against hotio's UMASK=002) can move and delete files after import.
// See issue #12.
const downloadDirMode = 0o775

// EnsureDownloadDir creates path with downloadDirMode (subject to umask).
// Centralised so tests can assert the mode and so we never accidentally
// regress to a non-group-writable default.
func EnsureDownloadDir(path string) error {
    return os.MkdirAll(path, downloadDirMode)
}
```

Both call sites switch to the helper:

```go
// internal/download/worker.go:121
- if err := os.MkdirAll(dl.OutputDir, 0o755); err != nil {
+ if err := EnsureDownloadDir(dl.OutputDir); err != nil {

// cmd/iplayer-arr/main.go:93
- if err := os.MkdirAll(downloadDir, 0755); err != nil {
+ if err := download.EnsureDownloadDir(downloadDir); err != nil {
```

The refactor exists in service of testability: testing the constant directly is straightforward; testing through `processDownload` would require ffmpeg.

## Tests

### Bug 1

`internal/download/dirs_test.go` (new file, 3 tests):

| Test | What it asserts |
|---|---|
| `TestEnsureDownloadDir_ModeAtLeast0o775AfterUmask` | Set umask to 0o002 (hotio default), call `EnsureDownloadDir`, stat, assert `mode & 0o020 != 0` (group-write preserved). |
| `TestEnsureDownloadDir_AlreadyExists_NoOp` | Call twice on same path, second call returns nil and does not change mode. |
| `TestEnsureDownloadDir_NestedPath` | Multi-segment path creates all intermediate dirs with the same mode. |

`internal/download/ffmpeg_hls_test.go` (new file, 5 tests). All tests inject a `fakeFHDProber` that satisfies `downloaderFHDProber` and serves a master playlist via `httptest`:

| Test | What it asserts |
|---|---|
| `TestResolveHLSVariant_FHDFound_ReturnsFHDURL` | fakeFHDProber returns `(fhdURL, true, nil)` → `resolveHLSVariant` returns `fhdURL`, not `bestURL`. The downloader gets the unlisted 1080p stream. |
| `TestResolveHLSVariant_FHDDefinitiveNo_ReturnsBestVariant` | fakeFHDProber returns `("", false, nil)` → `resolveHLSVariant` returns `bestURL`. The downloader gracefully degrades to the highest manifest variant. |
| `TestResolveHLSVariant_FHDProberError_ReturnsBestVariant` | fakeFHDProber returns `("", false, err)` → `resolveHLSVariant` logs and returns `bestURL`. Errors must NOT crash the download path; behaviour matches the existing inline-probe error branch. **Regression test for Codex round 3 finding 1.** |
| `TestResolveHLSVariant_NilProber_ReturnsBestVariant` | `resolveHLSVariant(ctx, nil, masterURL)` returns `bestURL` without panicking. Defensive guard for any future caller (or test) that constructs a `FFmpegJob` without wiring the prober. |
| `TestResolveHLSVariant_RespectsContextCancel` | Parent ctx is cancelled before the call → fakeFHDProber receives the cancelled ctx and returns `ctx.Err()` → `resolveHLSVariant` returns `bestURL`. Confirms the ctx is actually threaded through (not silently replaced with `context.Background()`). **Regression test for Codex round 3 finding 1's "use existing ctx" requirement.** |

### Bug 2

`internal/bbc/prober_test.go` (new file, 13 tests):

| Test | What it asserts |
|---|---|
| `TestPrefetch_CacheHit_NoHTTP` | Pre-populated PID returns cached heights, no HTTP call to playlist, ms, or fhdProber. |
| `TestPrefetch_CacheMiss_PopulatesAndPersists` | Cold PID → playlist + ms + fhdProber all called → cache populated → second call is a hit. |
| `TestPrefetch_PlaylistError_ReturnsNilNoCacheWrite` | playlist.ResolveCtx returns error → result entry is nil, cache not written, fhdProber NOT called. |
| `TestPrefetch_MediaSelectorError_ReturnsNilNoCacheWrite` | ms.ResolveCtx returns error → result entry is nil, cache not written, fhdProber NOT called. |
| `TestPrefetch_DetectsHiddenFHD` | mediaselector returns `[720, 540]` and fhdProber returns `(url, true, nil)` → cached heights are `[1080, 720, 540]`. **Headline test for the Codex finding 2 fix.** |
| `TestPrefetch_FHDDefinitiveNo_KeepsLowerHeights` | mediaselector returns `[720, 540]` and fhdProber returns `("", false, nil)` → cached heights are `[720, 540]` (unchanged). This is the *cacheable* "no 1080p" path. |
| `TestPrefetch_FHDProbeError_ReturnsNilNoCacheWrite` | mediaselector returns `[720, 540]` and fhdProber returns `("", false, err)` → result entry is nil, cache not written. **Regression test for Codex round 2 finding 1: a transient FHD HEAD failure must not be cached as "no 1080p" forever.** |
| `TestPrefetch_1080InManifest_SkipsFHDProbe` | mediaselector returns `[1080, 720, 540]` → fhdProber is NOT called, cached heights are `[1080, 720, 540]` unchanged, no duplicate `1080` entry. **Regression test for Codex round 2 finding 2: prevents duplicate 1080p RSS items on shows where BBC already advertises 1080p in the manifest.** |
| `TestPrefetch_DASHOnlyResult_SkipsFHDProbe` | mediaselector returns DASH-only streams (no HLS) → fhdProber is NOT called → heights are exactly the manifest result. |
| `TestPrefetch_ConcurrentDispatch_AllPIDsHandled` | 30 PIDs with concurrency 8 → all 30 results in map, no deadlock. |
| `TestPrefetch_ContextCancel_StopsEarly` | Cancelled parent ctx mid-fetch → unfinished probes abort cleanly via the propagated context, partial results returned. |
| `TestPrefetch_PerProbeTimeout_AbortsHangingProbe` | Fake playlist resolver blocks on ctx → 20s timeout fires → result is nil, worker freed. Validates that the timeout claim is enforceable. |
| `TestPrefetch_DeduplicatesAndSortsHeights` | BBC's multi-bitrate variants at the same height collapse to one entry; output is sorted descending. |

`internal/bbc/fhdprobe_test.go` (new file, 11 tests):

| Test | What it asserts |
|---|---|
| `TestProbeHiddenFHD_VariantExists_ReturnsFoundWithURL` | httptest server serves a master playlist with `video=N` lines and HTTP 200 on the `video=12000000` HEAD → returns `(fhdURL, true, nil)`; `fhdURL` is the concrete rewritten URL, not empty. |
| `TestProbeHiddenFHD_PicksHighestBandwidthVariant` | Master playlist has three variants: `BANDWIDTH=320000, video=320000`, `BANDWIDTH=1500000, video=1500000`, `BANDWIDTH=2700000, video=2700000`. Helper must rewrite the 2700000 URL (highest BANDWIDTH) to `video=12000000`, not the first one in the list. Fake HEAD server asserts the requested URL came from the highest-BW variant, not any of the others. **Regression test for Codex round 4 finding 3: the selection rule must match `resolveHLSVariant`'s `bestBW` logic byte-for-byte.** |
| `TestProbeHiddenFHD_RelativeVariantURL_ResolvedAgainstBase` | Master playlist variant URL is a relative path (`index-1500000.m3u8?audio=...&video=2700000`), not absolute. Helper must resolve it against the master playlist's base directory before rewriting, identical to `ffmpeg.go:104-110`. Asserts the final HEAD URL is `base + variant` with the `video=12000000` swap. |
| `TestProbeHiddenFHD_Head404_ReturnsDefinitiveNoFound` | HEAD returns HTTP 404 → returns `("", false, nil)`. This is the canonical "BBC has no hidden FHD for this programme" path; the prober caches this answer forever. **Regression test for Codex round 3 finding 3: 404 is the only HEAD status (along with 410) that triggers the definitive-no branch.** |
| `TestProbeHiddenFHD_Head410_ReturnsDefinitiveNoFound` | HEAD returns HTTP 410 Gone → returns `("", false, nil)`, same cacheable branch as 404. |
| `TestProbeHiddenFHD_Head429_ReturnsError` | HEAD returns HTTP 429 Too Many Requests → returns `("", false, err)` with the status code surfaced in the error. **Regression test for Codex round 3 finding 3: a rate-limit blip must not be cached as permanent no-FHD.** |
| `TestProbeHiddenFHD_Head503_ReturnsError` | HEAD returns HTTP 503 Service Unavailable → returns `("", false, err)`. Same rationale: upstream downtime is transient. |
| `TestProbeHiddenFHD_NoVariantsInPlaylist_ReturnsDefinitiveNoFound` | Master playlist parses successfully but has no `video=N` lines (no BANDWIDTH variants with the rewrite target) → returns `("", false, nil)` without issuing the HEAD. This is structural — the rewrite cannot possibly succeed — so it's safe to cache. |
| `TestProbeHiddenFHD_MasterPlaylistFetchFails_ReturnsError` | GET on the master playlist returns 503 → returns `("", false, err)`. Transport-level failure on the playlist fetch is always transient. |
| `TestProbeHiddenFHD_HeadProbeNetworkError_ReturnsError` | Master playlist GET succeeds but the HEAD of `video=12000000` returns a network error (connection reset, DNS failure) → returns `("", false, err)`. |
| `TestProbeHiddenFHD_ContextCancel_ReturnsError` | Parent ctx is cancelled mid-probe → returns `("", false, ctx.Err())`, no leaked goroutines. A cancelled context is never a "definitive no" — the search itself was aborted. |

`internal/store/quality_cache_test.go` (new file, 6 tests):

| Test | What it asserts |
|---|---|
| `TestQualityCache_PutGetRoundtrip` | Put then Get returns identical struct. |
| `TestQualityCache_GetMiss_NilNilNoError` | Missing PID returns `nil, nil` (not an error). |
| `TestQualityCache_Delete` | Put → Delete → Get returns nil. |
| `TestQualityCache_DeleteByShow` | Put 3 entries (2 same show, 1 other) → DeleteByShow removes the 2, leaves the 1. |
| `TestQualityCache_PutNormalisesShowName` | `Put(&QualityCache{ShowName: "Doctor Who"})` → Get returns a struct whose stored `ShowName == "doctor who"` (lower-cased and trimmed). **Regression test for Codex round 5 finding 1: the write path must apply `normaliseShowName` so the bucket contents stay consistent with the existing `bucketOverrides` convention.** |
| `TestQualityCache_DeleteByShow_CaseInsensitive` | Put entries with ShowName `"Doctor Who"` (written-through, so normalised to `"doctor who"` by the previous test's fix). Then call `DeleteByShow("DOCTOR WHO")`, `DeleteByShow(" doctor who ")`, and `DeleteByShow("Doctor Who")` separately (each on fresh fixtures) — all three must remove the entries. The refresh button must work regardless of how the user typed the name. **Regression test for Codex round 5 finding 1's headline risk.** |

(The previously-listed `TestQualityCache_BucketCreatedOnFirstUse` is removed because the new bucket is now created eagerly in `store.Open()` to match the existing pattern in `internal/store/store.go:28-36`. There is no lazy-creation behaviour to test.)

`internal/newznab/handler_test.go` (extend existing file with mock prober, 9 new tests):

| Test | What it asserts |
|---|---|
| `TestSearch_ProbedPIDWith1080p_Emits1080p` | Mock returns `[1080,720,540]` → RSS contains a `1080p` item. |
| `TestSearch_ProbedPIDWith720pOnly_OmitsFake1080p` | Mock returns `[720,540]` → RSS contains NO `1080p` item. **Headline regression test for the EastEnders bug.** |
| `TestSearch_ProbeFailure_Emits720pAnd540pFallback` | Mock returns `nil` for the PID → RSS contains exactly `[720p, 540p]`. |
| `TestSearch_PrefetchOnlyForFilteredResults_NameFilter` | 10 IBL results, 8 fail show-name filter → mock prefetch receives only 2 PIDs. |
| `TestSearch_PrefetchOnlyForFilteredResults_SeasonEpisode` | 12 IBL results all matching the show-name filter, but only 1 matches `S14E03` → mock prefetch receives exactly 1 PID. **Regression test for Codex round 3 finding 2: season/episode filters must apply to the prefetch list, not just the emit list.** |
| `TestSearch_PrefetchOnlyForFilteredResults_DailyDate` | 7 IBL results all matching the show-name filter, but only 1 matches the daily date `2026-04-05` → mock prefetch receives exactly 1 PID. Same regression as above for the daily-search code path. |
| `TestSearch_NoProberConfigured_OmitsExtraQualities` | `prober == nil` → falls back to `[720p, 540p]` (safe default). |
| `TestSearch_DuplicatePIDFromBrandAndEpisode_ProbesOnce` | Mock IBL returns the same PID twice (once as a direct `episode` hit, once inside a brand expansion) → mock prefetch receives the PID exactly once. RSS emits exactly one set of quality items for that PID (N items where N is the number of quality tags returned by the probe, per the live per-quality emission loop at `internal/newznab/search.go:170`), and **every GUID in the RSS response is unique** — the dedupe must not leave two `<item>` blocks sharing the same `EncodeGUID(PID, quality, "original")` value. **Regression test for Codex round 4 finding 2: IBL.Search appends without dedupe, so the single-pass walk must dedupe by PID. The invariant is "no duplicate GUIDs", not "len(items) == 1", because a single PID legitimately produces multiple items (one per quality tag) after round 5 finding 2's clarification.** |
| `TestMatchesSearchFilter_TableDriven` | Direct table-driven test of `matchesSearchFilter`: name-only, name+date, name+season, name+season+ep, no-filters-all-match, mismatching-name-rejects-everything, etc. Takes `*store.Programme` (not `*bbc.Programme`, which does not exist) — this test is also a compile-time guard for Codex round 4 finding 1. Locks the helper independently of the search-handler integration. |

(The previously-listed `TestSearch_OverrideTakesPrecedenceOverProbe` is removed: there is no override path to test, see Codex finding 3 in Revision history.)

`internal/newznab/heights_test.go` (new file, 4 tests):

| Test | What it asserts |
|---|---|
| `TestHeightsToTags_KnownHeights` | `[1080,720,540,396]` → `[1080p,720p,540p,396p]`. |
| `TestHeightsToTags_4K` | `[2160]` → `[2160p]`. |
| `TestHeightsToTags_UnknownHeightsDropped` | `[860]` → `[]`. |
| `TestHeightsToTags_PreservesOrder` | Input order = output order. |

**Total new tests:** 51 across 7 test files (6 new, 1 extension). All unit-level. No live HTTP, no live BBC, no ffmpeg. Test count breakdown:
- `dirs_test.go` — 3 (Bug 1)
- `ffmpeg_hls_test.go` — 5 (downloader-side prober plumbing for Bug 2 fix; injected fake satisfies `downloaderFHDProber`)
- `prober_test.go` — 13 (cache, FHD detection, definitive-no vs error split, 1080-in-manifest skip, DASH-only skip, ctx timeout enforcement, concurrency, dedup)
- `fhdprobe_test.go` — 11 (shared helper coverage; explicit 404/410 cacheable branches, explicit 429/503 transient branches, transport-error paths, highest-BW selection rule, relative variant URL resolution)
- `quality_cache_test.go` — 6 (Bug 2 store CRUD + ShowName normalisation write-path test + case-insensitive DeleteByShow test)
- `handler_test.go` — 9 new (search-handler integration; includes name/season/date filter coverage of `matchesSearchFilter` via the prefetch path, PID-dedupe regression, and a direct table-driven helper test)
- `heights_test.go` — 4 (helper)

## Files changed

**New files (11):**

```
internal/bbc/prober.go
internal/bbc/prober_test.go
internal/bbc/fhdprobe.go                                              (NEW — shared FHD probe helper)
internal/bbc/fhdprobe_test.go
internal/store/quality_cache.go
internal/store/quality_cache_test.go
internal/download/dirs.go
internal/download/dirs_test.go
internal/download/ffmpeg_hls_test.go                                  (NEW — downloader-side plumbing tests for resolveHLSVariant with injected prober)
internal/newznab/heights_test.go
docs/superpowers/specs/2026-04-07-iplayer-arr-issue-12-design.md      (this file)
```

**Modified files (11):**

```
internal/bbc/client.go             — add GetCtx(ctx, url) wrapper around doWithRetryCtx
internal/bbc/playlist.go           — add ResolveCtx(ctx, pid); existing Resolve becomes 1-line wrapper
internal/bbc/mediaselector.go      — add ResolveCtx(ctx, vpid); existing Resolve becomes 1-line wrapper
internal/store/types.go            — add QualityCache struct
internal/store/store.go            — add bucketQualityCache to the eager-create slice in Open()
internal/newznab/handler.go        — add prober field, NewHandler signature gains 4th arg
internal/newznab/search.go         — single-pass filter+prefetch+emit walk, extract matchesSearchFilter helper, heightsToTags, new quality switch, dead override branch removed
internal/newznab/handler_test.go   — extend with 9 new mock-prober tests (includes filter-helper coverage and PID-dedupe regression)
internal/download/ffmpeg.go        — add downloaderFHDProber local interface, add FHDProber field to FFmpegJob, change resolveHLSVariant signature to (ctx, prober, masterURL), replace inline http.Head with prober.ProbeHiddenFHD, forward job.FHDProber + ctx from RunFFmpeg
internal/download/worker.go        — call EnsureDownloadDir; populate job.FHDProber from m.client when constructing the FFmpegJob
cmd/iplayer-arr/main.go            — construct prober (with playlist + ms + bbcClient + store), pass to NewHandler, call EnsureDownloadDir, read 2 new env vars
CHANGELOG.md                       — v1.1.0 entry
```

(Total: 11 new + 11 modified = 22 files. Up from 16 before the Codex review revisions because of the ctx-plumbing wrappers, the FHD probe extraction, and the round-3 downloader-plumbing test file `internal/download/ffmpeg_hls_test.go`. **The downstream test files `playlist_test.go` and `mediaselector_test.go` do NOT need updating** — the existing `Resolve` methods are preserved as 1-line wrappers around the new `*Ctx` versions, so their old call signatures continue to work.)

## Configuration

Two new environment variables, both optional with sensible defaults:

| Var | Default | Purpose |
|---|---|---|
| `IPLAYER_PROBE_CONCURRENCY` | 8 | Worker pool size for parallel prefetch |
| `IPLAYER_PROBE_TIMEOUT_SEC` | 20 | Per-probe wall-time deadline |

Both are read once at startup. Changing them requires a container restart.

## Migration and rollout

- **No data migration.** The new BoltDB bucket is created eagerly in `store.Open()` by adding `bucketQualityCache` to the existing bucket slice at `internal/store/store.go:28-36`. `CreateBucketIfNotExists` makes this a no-op for any future upgrade and creates the new bucket on first start of v1.1.0. Existing installs upgrade in place with no manual action.
- **No breaking API changes.** The Newznab `<item>` shape Sonarr already consumes is identical. Only the qualities advertised change (correctly, downward-only, no fake 1080p).
- **First-run cost.** First search of any show triggers cold-cache probes. Happy-path cost for a 30-episode season at concurrency 8 is ~18s (4 batches × ~4.5s per probe, per the cost table earlier in this document); worst case is ~80s if probes repeatedly stretch to the 20s per-probe ctx deadline. Both numbers are inside Sonarr's default 100s indexer timeout. Episode-specific searches (SxxEyy or daily date) probe only the single matching PID thanks to the round-3 `matchesSearchFilter` + dedupe fix, so the cold-cache cost collapses to a single ~4.5s probe. All subsequent searches of the same content are sub-millisecond (BoltDB point lookup).
- **Rollback.** Deploy v1.0.2 image (the `MkdirAll` regression returns, but the speculative-1080p bug also returns to its pre-fix state, which is exactly the v1.0.2 baseline).

## Observability

- **Cache hit:** DEBUG-level log `quality cache hit pid=<pid>`.
- **Cold probe success:** INFO-level log `quality probe pid=<pid> heights=<heights> took=<duration>`.
- **Probe failure:** WARN-level log `quality probe failed pid=<pid> err=<err>`.

All flow through the existing ring buffer and surface in the `/logs` UI page. No metrics endpoint or Prometheus exporter is added in this release.

## Risks and mitigations

| Risk | Likelihood | Mitigation |
|---|---|---|
| BBC mediaselector rate-limits a high-concurrency probe burst | Low (no documented rate limit, BBC client uses random user-agent rotation) | Default concurrency is 8 (low). Easy to tune via env var. |
| BBC remasters an episode after we cached it (cache becomes stale) | Very low | Manual refresh via `DeleteQualityCacheByShow` (store-layer method exists, UI wiring deferred to v1.2). Worst-case impact: missing real 1080p until refresh. |
| Cold-cache season search exceeds Sonarr's indexer timeout | Low (default 100s vs ~80s absolute worst case) | Per-probe timeout (now genuinely enforceable via ctx propagation) caps individual probes at 20s. Concurrency 8 limits total fan-out. Headroom: 20s. |
| Probe failure for a real show silently degrades to 720p+540p | Low | The fallback IS the safest possible state (BBC universally delivers 720p). WARN log surfaces the issue in `/logs`. The next search retries (no cache write on failure). |
| Cache bucket grows unbounded over years of use | Negligible | One entry per ever-seen PID, ~200 bytes each. Even 10,000 episodes is ~2MB. BoltDB handles this trivially. |
| FHD probe HEAD races BBC's CDN behaviour and gives a false positive | Low | The downloader's existing logic (now shared) has been in production for the v1.x line and is the source of truth for FHD detection. If the downloader can grab 1080p from `video=12000000`, the prober's positive answer is correct by construction. |
| `ResolveCtx` refactor breaks an existing call site | Very low | Existing `Resolve(...)` methods are preserved as 1-line wrappers. Test files that call `resolver.Resolve("b039d07m")` continue to compile and run unchanged. |
| FHD probe in the prober is slower than expected, blowing the per-probe budget | Low | Per-probe ctx timeout (20s) caps total cost. Worst case is "some probes return nil with WARN logs", which falls back to safe `[720p, 540p]`. Never blocks search response longer than the timeout. |

## Open questions resolved during brainstorming and review

- **Cache key: PID, VPID, show, or show+series?** PID. VPID would cost an extra HTTP per search just to know which key to look up. Show or show+series mishandles mixed-quality shows. PID is the unit Sonarr searches against and the natural cache key.
- **Cache TTL?** None. BBC content masters are immutable; manual refresh is the right tool for the rare remaster case.
- **Per-probe timeout?** 20s combined (covers playlist + mediaselector + FHD probe together). Now actually enforceable thanks to the ctx-propagation refactor; was decorative in the pre-Codex draft.
- **Disable knob?** No. The graceful fallback means a misbehaving prober only ever degrades to safe defaults; a disable flag would be a code path only exercised in emergencies.
- **Refresh button in UI?** Deferred to v1.2. Store methods exist, no UI wiring yet.
- **Wire up `Synthetic bool`?** No. That field belongs to a (currently nonexistent) override schema, not the cache. Failed probes write nothing to cache, so cache entries are always real.

### Resolved during Codex review (2026-04-07)

- **How do we make `context.WithTimeout` actually enforceable?** Add `*Ctx(ctx, ...)` variants to `Client.Get`, `PlaylistResolver.Resolve`, and `MediaSelector.Resolve`. Existing call sites stay on the no-context wrappers; the prober uses `*Ctx` directly. The existing internal `doWithRetryCtx` already threads context correctly into `http.NewRequestWithContext`, so this is just exposing it.
- **How do we detect BBC's hidden 1080p variant?** Extract `Client.ProbeHiddenFHD(ctx, hlsMasterURL)` from the existing `internal/download/ffmpeg.go::resolveHLSVariant`, share between the downloader and the prober. Single source of truth for the BBC `video=12000000` quirk.
- **What about the `if len(prog.Qualities) > 0` override branch?** Delete it. It is dead code today: `Programme.Qualities` is never written anywhere in the repo, `ShowOverride` has no quality fields, zero tests cover it. Removing it is not a behaviour change. A future per-show user override should land in `ShowOverride.QualityOverride []string`, not by reviving this orphan field.
- **Lazy or eager bucket creation?** Eager. The existing pattern in `internal/store/store.go:28-36` adds every bucket to a slice and creates them all in `Open()`. Follow the established convention; do not introduce a second pattern.
