# Changelog

All notable changes to iplayer-arr will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
