# iplayer-arr v1.1.1 - design spec

**Spec date**: 2026-04-08
**Target version**: v1.1.1
**Target branch**: `main` (single PR, squash merge)
**Prerequisites**: v1.1.0 merged (commit `1803ffd`)
**Author**: Will Luck

---

## Summary

v1.1.1 is a multi-issue bug fix release that also hardens the project's legal posture and adds structured issue reporting. It closes four open GitHub issues (#15, #16, #18, #19) and introduces project governance files that have been missing since the repo was made public.

The release is structured as six phases (Phase 0-5), landing as a single PR with one squash-merge to `main` and a single annotated tag `v1.1.1`. Phase 1 introduces one breaking default-port change (8191 -> 62001) which must be called out prominently in the release notes.

Issue #14 (STV Player support) is explicitly **out of scope** and will be closed as "won't fix". The reasoning: the project's name, architecture, and existing BBC-specific implementation (IBL, mediaselector, PIDs, the `video=12000000` HLS variant) are all BBC-iPlayer-specific. Adding a second provider would require a large Provider interface refactor for a niche service (STV Player is substantially smaller than ITVX and most of its catalogue overlaps with ITVX anyway). It would also expand the project's legal surface area from "personal BBC iPlayer tool" to "multi-broadcaster UK catch-up aggregator", which is a stronger target. See issue #14 for the closing reply.

## Scope

**In scope (v1.1.1)**:
- Legal hardening: DISCLAIMER, SECURITY, README Legal section, neutral rewrite of `docs/bbc-streaming-internals.md`
- Documentation hygiene: CHANGELOG.md v1.1.0 backfill entry (currently missing)
- Issue governance: GitHub Issue Forms (bug + feature request) with all fields optional + issue chooser config pointing to private vulnerability reporting
- Issue #19 fix: change default `PORT` from `8191` to `62001` across `main.go`, `Dockerfile`, and `README.md`
- Issue #16 fix: plumb `Handler.DownloadDir` through `/api/config`, the directory listing handlers, and the SABnzbd compat handler so the env-var value is actually surfaced in the UI
- Issue #15 fix: add composite-date detection to `internal/newznab/titles.go` and `internal/bbc/ibl.go` so BBC subtitles like `"2025/26: 22/03/2026"` (Match of the Day) produce clean daily-format titles instead of malformed triple-dated filenames
- Issue #18 fix: add year-range disambiguation via `bareName`, `extractYearRange`, `nameMatchesWithYear`, and `disambiguateByYear` helpers so TVDB ID lookups for shows with year-suffixed BBC brand titles (Doctor Who classic, Doctor Who 2005-2022, Casualty reboots, etc.) route to the correct brand
- Test additions: approximately 35 new unit and integration tests (detailed breakdown in the Test plan section)

**Out of scope (deferred)**:
- Issue #14 STV Player support: closed as won't-fix
- Wiki updates: the GitHub wiki (Configuration Reference, Installation, Sonarr Integration pages) references `8191` and `DOWNLOAD_DIR` behaviour - will be updated in a separate wiki sync after v1.1.1 ships, not in this PR
- Generalised composite-subtitle handling for shows beyond Match of the Day - deferred to a v1.2.0 hardening pass
- UI label "(set via DOWNLOAD_DIR env var)" next to the read-only field - cosmetic nice-to-have, deferred
- The "Settings persistence on env var change" migration case in Phase 2 (user starts without env var, downloads accumulate to `/downloads`, then sets env var) - out of scope for this PR; a separate migration guide can be written later
- The secondary `parseSubtitleNumbers` day-of-month extraction bug is partially addressed in Phase 3 but only for the date-only subtitle case; a broader cleanup is deferred

## Release shape

- **Version**: v1.1.1 (patch release)
- **Commits**: one commit per phase, six total, landing on a single feature branch
- **PR**: single PR from `dev` (or similarly-named feature branch) to `main`, squash-merged
- **Tag**: annotated `v1.1.1`
- **Container publish**: GHCR + Docker Hub multi-arch via existing release workflow (triggered by tag push)
- **Gitea mirror**: tag pushed to Gitea for local CI verification before GitHub

### SemVer note on the breaking change

Phase 1 changes the default value of the `PORT` environment variable from `8191` to `62001`. Strictly, this is a breaking change for users who rely on the default (no explicit `PORT=` override, using `-p 8191:8191` in docker-compose). Under strict SemVer, a breaking change would warrant a minor-version bump to v1.2.0.

The decision to keep this as v1.1.1 is deliberate: the "breaking" impact is narrow (docker-compose files lose connectivity until the port mapping is updated), the migration is a one-line change, and the fix ships alongside four unrelated bug fixes. A minor-version bump would disproportionately signal "new features" when the bulk of the release is bug fixes.

**Mitigation**: the v1.1.1 release notes include a "## Breaking changes" section as the first content after the title, with an explicit migration recipe.

## Verification gates

Each phase has a verification gate that must pass before proceeding to the next phase. The Phase 5 final gate runs the global checks.

| Phase | Local gate |
|---|---|
| 0 | Phase 0a checklist passes (branch-local file existence and content checks - see Phase 0 verification gate section). Phase 0b post-merge state is **not** part of this gate. |
| 1 | `go build ./...`, `go test ./cmd/iplayer-arr -v`, Task 5.4 grep gate returns zero results |
| 2 | `go build ./...`, `go test ./internal/api -v`, `go test ./internal/sabnzbd -v` |
| 3 | `go build ./...`, `go test ./internal/newznab -v`, `go test ./internal/bbc -v` |
| 4 | `go build ./...`, `go test ./internal/newznab -v`, `go vet -copylocks -loopclosure ./internal/newznab/` |
| 5 | `go test ./...`, `go vet ./...`, `gofmt -l .` (all must be clean) |

**No tests run on `192.168.1.57`** (production). Local `go test` only.

---

# Phase 0 - Legal hardening, documentation, issue governance

## Rationale

iplayer-arr is a public GitHub project that integrates with BBC iPlayer's public APIs and is published on GHCR and Docker Hub under a GPL-3.0 software licence. The project currently has zero project-specific legal text: no disclaimer, no trademark notice, no TV Licence requirement statement, no DMCA or abuse contact channel. It also has no structured issue reporting - users write free-form bug reports which vary wildly in quality and completeness.

Phase 0 closes these gaps with cheap, high-leverage project-governance files. None of them require Go code changes. None of them run tests. The entire phase is Markdown, YAML, and repository settings.

## Files

**New files**:
- `DISCLAIMER.md` - legal disclaimer (TV Licence requirement, BBC trademark, personal use, no warranty)
- `SECURITY.md` - security reporting policy pointing at GitHub's Private Vulnerability Reporting feature
- `.github/ISSUE_TEMPLATE/bug_report.yml` - structured bug report form, all fields optional
- `.github/ISSUE_TEMPLATE/feature_request.yml` - structured feature request form, all fields optional
- `.github/ISSUE_TEMPLATE/config.yml` - issue chooser configuration (security contact link, wiki link, blank issues allowed)

**Modified files**:
- `README.md` - add Legal section, footer disclaimer, TV Licence note in Quick Start
- `docs/bbc-streaming-internals.md` - replaced with neutral reference content (verbatim new Markdown in Task 0.4)
- `CHANGELOG.md` - add missing v1.1.0 entry (file currently ends at v1.0.2)

**Repository settings** (not file commits, and **not part of Phase 0a branch-local acceptance** - these are Phase 0b post-merge state items, applied by the maintainer after the PR is merged. They do not block Phase 5 or PR readiness. See the Phase 0 acceptance section below for the 0a/0b split.):
- Enable **Settings -> Security -> Private vulnerability reporting**
- Create GitHub label `bug` (colour `#d73a4a`, description "Something isn't working")
- Create GitHub label `enhancement` (colour `#a2eeef`, description "New feature or request")

## Task 0.1 - Create `DISCLAIMER.md`

A single markdown file at the repository root. Contents should cover:

1. **Not affiliated with the BBC** - standard trademark disclaimer. One sentence.
2. **TV Licence requirement** - UK users must hold a valid TV Licence to legally access BBC iPlayer content. iplayer-arr does not verify this.
3. **Personal use only** - users are not granted any right to redistribute downloaded content.
4. **Jurisdiction and geographic restriction** - BBC iPlayer is geo-locked to the UK. Users outside the UK should not use this tool against BBC iPlayer.
5. **No warranty** - reaffirm the GPL-3.0 warranty disclaimer in plain English.
6. **Reporting concerns** - rights holders and security researchers should use GitHub's Private Vulnerability Reporting feature (link).

The exact wording is decided during implementation but should be plain English, not legalese, and should be polite rather than defensive. See the writing-plans phase for the verbatim markdown.

## Task 0.2 - Create `SECURITY.md`

A single markdown file at the repository root. It should:
- Explain that security vulnerabilities should be reported privately via GitHub's Private Vulnerability Reporting feature
- Provide a direct link to `https://github.com/Will-Luck/iplayer-arr/security/advisories/new`
- Explicitly discourage public issues for security problems
- Thank reporters

No email address. No PGP key. The file is intentionally minimal - it exists to satisfy GitHub's Security tab expectation and route reports to a private channel.

## Task 0.3 - Add a Legal section to `README.md`

Three modifications to the existing README:

1. Under the existing Quick Start section (above the docker-run snippet), add a prominent note: "You must hold a valid UK TV Licence to legally access BBC iPlayer content via this tool. See DISCLAIMER.md for full legal terms."
2. Add a new Markdown section near the end (between "Documentation" and "Licence"), titled `## Legal`, with:
   - Link to DISCLAIMER.md
   - Link to SECURITY.md
   - One-sentence summary: "iplayer-arr is not affiliated with the BBC. See DISCLAIMER.md for full legal terms."
3. Add a footer line at the very end of the file (below the existing Licence section): `---` followed by `*iplayer-arr is not affiliated with, endorsed by, or sponsored by the BBC. iPlayer is a trademark of the British Broadcasting Corporation.*`

## Task 0.4 - Rewrite `docs/bbc-streaming-internals.md`

The existing `docs/bbc-streaming-internals.md` uses framing language that is legally unhelpful in a public repo. This task replaces the entire file with neutral technical reference content.

**Implementation**: overwrite the file contents with the verbatim Markdown below. Do not merge with existing content.

```markdown
# BBC streaming internals

Implementation reference for the BBC streaming layer. Technical description only, not a user guide.

## Overview

iplayer-arr fetches BBC media via a two-stage resolution chain:

1. **IBL search** (`internal/bbc/ibl.go`) queries BBC's public programme index to find matching brands and episodes.
2. **Mediaselector resolution** (`internal/bbc/mediaselector.go`) exchanges a media version ID (VPID) for a set of stream URLs spanning multiple bitrates and delivery formats.

Once a stream URL is selected, the downloader in `internal/download/ffmpeg.go` passes it to ffmpeg for segment-level retrieval.

## HLS variant structure

BBC's HLS master playlists list variant streams, each tagged with a `BANDWIDTH` attribute and a URL containing a `video=<bitrate>` query parameter. The variant with the highest `BANDWIDTH` attribute is normally selected by the downloader.

The `QualityProber` in `internal/bbc/prober.go` inspects the master playlist at search time to determine which qualities are available for each programme. Results are cached per-PID in the BoltDB `quality_cache` bucket (see `internal/store/quality_cache.go`).

## Higher-bitrate variants

Some BBC HLS master playlists reference variants up to `video=5000000` (approximately 720p). Higher-bitrate content is served from the same CDN at `video=12000000` (approximately 1080p). `Client.ProbeHiddenFHD` in `internal/bbc/fhdprobe.go` HEAD-probes the `video=12000000` URL form and reports whether that variant is retrievable for a given master playlist. When it is, the downloader's `resolveHLSVariant` (`internal/download/ffmpeg.go`) uses the higher-bitrate URL in place of the highest-listed variant.

## DASH streams

BBC also serves content via DASH (`.mpd` manifests) for devices that do not use HLS. iplayer-arr does not currently use DASH streams - HLS is preferred because ffmpeg handles HLS variant URLs directly. DASH support would require a separate manifest parser.

## DRM

Some premium BBC content (certain films, occasional drama series) uses Widevine DRM. Widevine-protected streams cannot be retrieved by iplayer-arr and are skipped during IBL filtering.

## Implementation files

| Concern | File |
|---|---|
| IBL search / episode listing | `internal/bbc/ibl.go` |
| Mediaselector (VPID -> streams) | `internal/bbc/mediaselector.go` |
| HLS master playlist + variant probing | `internal/bbc/fhdprobe.go` |
| Quality detection / cache | `internal/bbc/prober.go` |
| BoltDB quality cache | `internal/store/quality_cache.go` |
| ffmpeg invocation | `internal/download/ffmpeg.go` |
```

**Scope**: only the content of `docs/bbc-streaming-internals.md`. Code comments in `internal/bbc/fhdprobe.go`, `internal/download/ffmpeg.go`, and similar files are not in scope for this task - they can be audited and softened in a follow-up pass if needed. The repository does contain both `internal/bbc/prober.go` (quality prober struct + narrow local interfaces) and `internal/bbc/fhdprobe.go` (`Client.ProbeHiddenFHD` implementation + `pickHighestBandwidthVariant` helper). Both files are referenced in the new document above.

## Task 0.5 - Add v1.1.0 entry to `CHANGELOG.md`

The CHANGELOG currently ends at v1.0.2 and has no v1.1.0 entry despite v1.1.0 shipping on 2026-04-08. The v1.1.0 entry should follow the existing format (Fixed, Tests, Verified end-to-end sections) and cover:

- **Fixed** (Bug 1): `EnsureDownloadDir` now uses `0o775` so container PUID/PGID can write to host-mounted download directories under umask `0o002`
- **Fixed** (Bug 2): Search-time quality probe prefetch prevents fake 1080p advertising for BBC shows that only have 720p variants (EastEnders, soaps, most catch-up)
- **Tests**: 51 new unit tests across 6 new files and 1 extension
- **Configuration**: `IPLAYER_PROBE_CONCURRENCY` (default 8), `IPLAYER_PROBE_TIMEOUT_SEC` (default 20)

The entry should reference the design spec at `docs/superpowers/specs/2026-04-07-iplayer-arr-issue-12-design.md` and PR #17.

## Task 0.6 - Create `.github/ISSUE_TEMPLATE/bug_report.yml`

A GitHub Issue Form (YAML schema) with the following fields, **all optional** (`validations.required: false`):

| Field | Type | Notes |
|---|---|---|
| "What happened?" | textarea | placeholder with example |
| "What did you expect to happen?" | textarea | |
| iplayer-arr version | input | placeholder `v1.1.0` or `latest` |
| Docker image source | dropdown | GHCR / Docker Hub / Source / Other / Not sure |
| Host OS / platform | input | placeholder `Ubuntu 22.04, Unraid 6.12, TrueNAS Scale, Synology DSM` |
| Sonarr version (if relevant) | input | placeholder `v4.0.17` |
| BBC show(s) affected | input | placeholder `Doctor Who, Match of the Day, EastEnders` |
| Relevant logs | textarea | render: text, drag-and-drop support |
| Additional context | textarea | |

Top of the form: a markdown block thanking the reporter and emphasising "all fields are optional - fill in what you can, leave blank what you can't".

Auto-apply label: `bug`
Default title prefix: `[Bug] `

## Task 0.7 - Create `.github/ISSUE_TEMPLATE/feature_request.yml`

Simpler form than the bug report. Fields (all optional):

| Field | Type |
|---|---|
| "What problem would this solve?" | textarea |
| "Proposed solution" | textarea |
| "Alternatives you've considered" | textarea |
| "Additional context" | textarea |

Top of the form: markdown thanking the reporter.
Auto-apply label: `enhancement`
Default title prefix: `[Feature] `

## Task 0.8 - Create `.github/ISSUE_TEMPLATE/config.yml`

The issue chooser configuration that routes security reports away from public issues and allows blank issues as a fallthrough:

```yaml
blank_issues_enabled: true
contact_links:
  - name: Security vulnerability
    url: https://github.com/Will-Luck/iplayer-arr/security/advisories/new
    about: Please report security vulnerabilities privately via GitHub's security advisories rather than opening a public issue.
  - name: Documentation & Wiki
    url: https://github.com/Will-Luck/iplayer-arr/wiki
    about: Installation, configuration, Sonarr integration, and troubleshooting guides
```

The `blank_issues_enabled: true` is deliberate - per the user preference, "a report with some data is better than no report at all" - so users who don't fit either template can still open a blank issue.

## Task 0.9 - Enable GitHub Private Vulnerability Reporting

Via the GitHub UI: **Settings -> Security -> Private vulnerability reporting -> Enable**.

Once enabled, users can file private security reports via the repo's Security tab. The SECURITY.md file (Task 0.2) points at the URL this feature exposes.

Verification: after enabling, visit `https://github.com/Will-Luck/iplayer-arr/security/advisories/new` - it should present a private report form rather than a 404.

## Task 0.10 - Create or update GitHub labels (idempotent)

Two labels, created or updated via `gh label create --force`. The `--force` flag makes the command idempotent: if the label already exists (likely for `bug` and `enhancement` on any repo that had defaults enabled), the existing label is updated with the specified colour and description rather than the command failing.

```bash
gh label create bug \
  --description "Something isn't working" \
  --color d73a4a \
  --force \
  --repo Will-Luck/iplayer-arr

gh label create enhancement \
  --description "New feature or request" \
  --color a2eeef \
  --force \
  --repo Will-Luck/iplayer-arr
```

Colours match GitHub's default label scheme. These labels are referenced by the Issue Forms in Tasks 0.6 and 0.7.

**Why --force**: GitHub repos created before issue templates sometimes auto-create a default set of labels including `bug` and `enhancement`. Without `--force`, the `gh label create` command fails with a "label already exists" error on those repos, blocking Phase 0 completion for a non-functional reason. With `--force`, the command is safe to run whether the label exists or not.

## Phase 0 verification gate

Phase 0 has two verification tiers because some of its deliverables are branch-local (committed files) and some are remote-state (GitHub repo settings and labels that exist on the server, not in the working tree). The pre-commit tier is what the implementer runs before committing Phase 0. The post-merge tier is what the maintainer runs after the PR is squash-merged and repo settings have been changed via the GitHub UI.

### Phase 0a - Pre-commit gate (branch-local, runs from the implementation branch)

- [ ] All 5 new files exist in the repo with the expected content
- [ ] `README.md` has the Legal section, footer disclaimer, and TV Licence note
- [ ] `docs/bbc-streaming-internals.md` content matches the verbatim new Markdown specified in Task 0.4 (check via byte-level file comparison, not keyword greps)
- [ ] `CHANGELOG.md` has a v1.1.0 entry
- [ ] No Go tests run for this phase (Markdown/YAML/settings only)
- [ ] `git status` shows only the expected Phase 0 files

### Phase 0b - Post-merge gate (remote state, runs after PR is merged and settings changed)

These checks cannot be validated from the feature branch - they require the PR to be merged to `main` and repository settings to be changed via the GitHub UI or `gh` CLI. Task them to the maintainer who merges the PR.

- [ ] `gh label list --repo Will-Luck/iplayer-arr` shows `bug` and `enhancement` (created in Task 0.10, idempotent so safe to re-run)
- [ ] GitHub Settings -> Security -> Private vulnerability reporting is enabled (manual step via GitHub UI; enable via **Settings -> Security -> Private vulnerability reporting -> Enable** on the repo's settings page)
- [ ] `https://github.com/Will-Luck/iplayer-arr/security/advisories/new` renders the private report form (browser check, confirms PVR is actually active)

**Note**: Phase 0a failing blocks the commit. Phase 0b failing does NOT block the sprint - it can be remediated post-merge without re-running the sprint. The post-merge gate is informational and operational, not a code-quality gate.

## Phase 0 acceptance

Phase 0 acceptance is split to match the 0a/0b gate split. **Only the branch-local acceptance items below are required to mark Phase 0 complete and proceed to Phase 1.** The post-merge items are not blockers for the sprint - they get checked off after the PR is merged and the maintainer changes the repo settings.

### Phase 0a acceptance (branch-local, required for Phase 0 completion)

Phase 0a is complete when:
1. The README has a Legal section linking to DISCLAIMER.md and SECURITY.md
2. A `DISCLAIMER.md` file exists at the repo root with the required content
3. A `SECURITY.md` file exists at the repo root pointing at GitHub's Private Vulnerability Reporting URL
4. `.github/ISSUE_TEMPLATE/bug_report.yml`, `feature_request.yml`, and `config.yml` exist with all fields marked `validations.required: false` and `blank_issues_enabled: true`
5. The `docs/bbc-streaming-internals.md` file matches the verbatim replacement content specified in Task 0.4
6. The CHANGELOG.md file has a v1.1.0 entry documenting what shipped in the previous release

**These are the only Phase 0 items that block Phase 5.** A branch with all of the above passing can proceed to Phase 1 implementation, then through to Phase 5 commit, and is ready for PR review.

### Phase 0b post-merge state (informational, NOT required for Phase 0 completion)

After the PR is merged and the maintainer has changed the repo settings, the following additional state should exist:
1. A visitor landing on the repo sees a Security tab with a working private reporting flow
2. Anyone opening a new issue is presented with Bug / Feature / Blank choices plus the security-reporting and wiki contact links (this depends on the issue templates being on `main`, which happens automatically once the PR merges)
3. `gh label list` shows the `bug` and `enhancement` labels (created via `gh label create --force` from Task 0.10)

These items live on the post-merge checklist - the maintainer ticks them off after merging the PR. They are not gating Phase 5 acceptance and they cannot be verified from the feature branch.

---

# Phase 1 - Issue #19: Default PORT change

## Root cause

`cmd/iplayer-arr/main.go:31`:
```go
port := envOr("PORT", "8191")
```

Default value `8191` is identical to FlareSolverr's default port. Users running both in the same *arr stack hit a port-binding conflict on first launch. Reported via Reddit community feedback.

The fix is a one-line change in `main.go` plus three-location update to the Dockerfile and README examples.

## Fix strategy

Change the default from `8191` to `62001` in all five locations that reference `8191`:

1. `cmd/iplayer-arr/main.go:31` - the `envOr` default string
2. `Dockerfile:35` - `ENV WEBUI_PORTS="8191/tcp"` (hotio base image webUI advertisement)
3. `Dockerfile:37` - `EXPOSE 8191` directive
4. `README.md:49` - `-p 8191:8191` in the Quick Start docker run example
5. `README.md:64` - `- 8191:8191` in the Quick Start docker-compose example
6. `README.md:78` - `http://localhost:8191` in the "Open this URL" line

A `grep -rn "8191" .` after the change should return zero results.

## Rationale for port choice: `62001`

The port choice is somewhat arbitrary. The selection criteria:

- **Avoids known *arr-stack defaults**: Sonarr 8989, Radarr 7878, Prowlarr 9696, Bazarr 6767, qBittorrent 8080, SABnzbd 8080, FlareSolverr 8191, Jackett 9117, NZBGet 6789, Lidarr 8686. 62001 collides with none of these.
- **IANA range**: 62001 is in the Dynamic/Private range (49152-65535), so not registered to any well-known service.
- **Memorable**: 62001 is sequential and easy to recall.
- **Consistent with lucknet internal range**: the LuckNet homelab uses 62000-63999 for its high-port services, so 62001 fits that convention for the author's own deployment.

Alternatives considered and rejected:
- `8290` (close to 8191, mnemonic) - still within the *arr port cluster, higher collision risk with unusual tools
- `9117` - Jackett's default, direct collision
- `62100` - further from FlareSolverr but less memorable than 62001

**This does not eliminate all possible collisions.** A user with an unusual stack might still hit something else. The fix addresses the documented FlareSolverr collision; users with other collisions should still set `PORT` explicitly.

## Files

| File | Line | Change |
|---|---|---|
| `cmd/iplayer-arr/main.go` | 31 | `envOr("PORT", "8191")` -> `envOr("PORT", "62001")` |
| `Dockerfile` | 35 | `ENV WEBUI_PORTS="8191/tcp"` -> `ENV WEBUI_PORTS="62001/tcp"` |
| `Dockerfile` | 37 | `EXPOSE 8191` -> `EXPOSE 62001` |
| `README.md` | 49 | `-p 8191:8191 \` -> `-p 62001:62001 \` |
| `README.md` | 64 | `      - 8191:8191` -> `      - 62001:62001` |
| `README.md` | 78 | `` `http://localhost:8191` `` -> `` `http://localhost:62001` `` |

## Migration impact

**Breaking change for users who rely on the default port.** A user whose docker-compose file uses `-p 8191:8191` and does not set `-e PORT=8191` explicitly will find the container bound to port 62001 internally while the host mapping still targets 8191. Sonarr/browsers hitting `http://host:8191` will get connection refused.

**Migration recipe** (must appear front-and-centre in the release notes):

```
v1.1.0 -> v1.1.1 upgrade: change the port mapping in your docker-compose.yml
from "-p 8191:8191" to "-p 62001:62001", or set "-e PORT=8191" to keep the
old port. No other action required.
```

Users who already set `PORT` explicitly (via `-e PORT=8191` or similar) are unaffected - the env var still wins.

## Task 1.1 - Change `main.go:31`

Surgical one-line edit:
```go
port := envOr("PORT", "62001")
```

## Task 1.2 - Change `Dockerfile:35` and `:37`

Two-line edit:
```dockerfile
ENV WEBUI_PORTS="62001/tcp"
```
```dockerfile
EXPOSE 62001
```

## Task 1.3 - Update `README.md` Quick Start

Three-line edit (lines 49, 64, 78 per the file table above).

## Task 1.4 - Extract port default to a constant + helper, add regression test

**Problem with a naive "test the default" approach**: a test that hardcodes `envOr("PORT", "62001")` only tests `envOr` itself and passes even if `main()` later reverts to `envOr("PORT", "8191")`. A real regression test must exercise the same code path `main()` does.

**Fix**: extract the default port to a package-level constant and wrap the `envOr` call in a small helper that `main()` calls. Test the helper directly.

### Code changes to `cmd/iplayer-arr/main.go`

1. Add a package-level constant near the top of the file (below the imports):
```go
// defaultPort is the TCP port iplayer-arr listens on when the PORT
// environment variable is not set. Chosen to avoid FlareSolverr's
// default of 8191. See docs/superpowers/specs/2026-04-08-iplayer-arr-v1.1.1-design.md.
const defaultPort = "62001"
```

2. Extract a tiny helper that wraps the `envOr` call:
```go
// resolvePort returns the port main() should bind to, applying
// PORT env-var override with fallback to defaultPort.
func resolvePort() string {
    return envOr("PORT", defaultPort)
}
```

3. Change `main()` at line 31 to call the helper:
```go
port := resolvePort()
```

### Test in `cmd/iplayer-arr/main_test.go`

```go
func TestResolvePort_DefaultWhenUnset(t *testing.T) {
    t.Setenv("PORT", "")
    if got := resolvePort(); got != defaultPort {
        t.Errorf("resolvePort() = %q, want %q", got, defaultPort)
    }
    if defaultPort != "62001" {
        t.Errorf("defaultPort = %q, want 62001 (FlareSolverr collision fix)", defaultPort)
    }
}

func TestResolvePort_EnvOverride(t *testing.T) {
    t.Setenv("PORT", "9999")
    if got := resolvePort(); got != "9999" {
        t.Errorf("resolvePort() with PORT=9999 = %q, want 9999", got)
    }
}
```

**Why the double assertion in the first test**: the first assertion (`resolvePort() == defaultPort`) catches reverts in `main.go` that change which helper is called or bypass `resolvePort` entirely. The second assertion (`defaultPort == "62001"`) catches reverts that change the constant back to 8191. Together, they're a belt-and-braces regression guard.

**Count impact**: Phase 1 goes from 1 test to 2 tests. The test totals table and Phase 5 gate need to reflect this.

## Phase 1 verification gate

```bash
go build ./...
go test ./cmd/iplayer-arr -v
grep -rn "8191" \
  --include="*.go" \
  --include="*.md" \
  --include="Dockerfile" \
  --include="*.yml" \
  --include="*.yaml" \
  --exclude="CHANGELOG.md" \
  --exclude-dir=superpowers \
  .
```

Expected:
- Clean build
- `TestResolvePort_DefaultWhenUnset` and `TestResolvePort_EnvOverride` pass (plus all existing tests)
- grep returns **zero results** in code and user-facing docs (README.md, Dockerfile, YAML configs, Go files)

**Why `--exclude-dir=superpowers` (not `docs/superpowers`)**: GNU grep's `--exclude-dir` matches directory **basenames**, not paths. Passing a nested path like `docs/superpowers` is a no-op because no directory has a slash in its name. The basename `superpowers` is safe in this repo because no other directory shares that name; if that ever changes, the gate can switch to `find -prune` or a ripgrep equivalent.

**Why the exclusions exist**: `CHANGELOG.md` and the `superpowers` directory (which holds this spec plus the writing-plans output) are allowed to reference `8191` because they legitimately document the migration (e.g. "change `-p 8191:8191` to `-p 62001:62001`"). The grep's purpose is to catch stray binding references like `EXPOSE 8191` in the Dockerfile or `-p 8191:8191` in a forgotten docker-compose example, not to scrub all historical mentions of the old value.

## Phase 1 acceptance

- A fresh `docker run -d -p 62001:62001 ghcr.io/will-luck/iplayer-arr:v1.1.1` binds cleanly alongside a default-config FlareSolverr container
- Existing users who set `-e PORT=8191` continue to work unchanged
- The v1.1.1 release notes prominently document the migration recipe
- No `8191` string appears in committed code or user-facing docs (verified by the grep gate above). `CHANGELOG.md` and the `docs/superpowers/` spec directory are explicitly allowed to contain `8191` because they document the migration; the gate excludes them on purpose.

---

# Phase 2 - Issue #16: `DOWNLOAD_DIR` variable not surfaced in UI

## Root cause (verified by investigation)

The bug has three observable symptoms but only one root cause:

1. User sets `DOWNLOAD_DIR=/data` via Docker env var. `echo $DOWNLOAD_DIR` inside the container correctly returns `/data`.
2. Files **actually download to `/data`** - the download manager uses the env value correctly. `cmd/iplayer-arr/main.go:30` reads the env via `envOr`, passes it to `download.NewManager`, and the manager's `OutputDir` is built from the correct path.
3. The web UI settings page shows `/downloads` (the hardcoded default) and the field is disabled.
4. Directory listings via `/api/downloads/directory` and `/api/downloads/directory/{folder}` show the wrong path.
5. The SABnzbd compat endpoint returns the wrong path.

The single root cause: **three handler paths read `download_dir` from the BoltDB config store instead of consulting the runtime env-derived value**. The env var is only stored in local variables at startup and is never written back to the config store:

- `internal/api/config.go::handleGetConfig` iterates `configKeys`, reads each from `h.store.GetConfig(key)`, falls back to `configDefaults["download_dir"] = "/downloads"`. Never consults `h.DownloadDir`.
- `internal/api/directory.go` at lines 25 and 94: `downloadDir := h.store.GetConfig("download_dir")`, falls back to `/downloads` if empty. Same pattern.
- `internal/sabnzbd/handler.go` at lines 63 and 81: same pattern.

The frontend field at `frontend/src/pages/Config.tsx:70` is correctly disabled (`disabled` attribute, unconditional) because `download_dir` is genuinely meant to be env-controlled not UI-controlled - see `internal/api/config.go:43` where `readOnly := map[string]bool{"api_key": true, "download_dir": true}` enforces this server-side as well. **The UI field is correct; it's just displaying the wrong value.**

## Fix strategy

Introduce a single helper on `*Handler` that encodes the correct precedence rule, and use it at every call site that currently reads `download_dir` from the store:

**Precedence rule**:
```
download_dir resolution priority (high to low):
  1. h.DownloadDir (env var, set at startup from DOWNLOAD_DIR)
  2. store.GetConfig("download_dir") (BoltDB persisted value)
  3. configDefaults["download_dir"] = "/downloads" (hardcoded fallback)
```

Extract this into `(h *Handler) ResolveDownloadDir() string` on the api Handler, and add an equivalent method on the sabnzbd handler (or plumb `DownloadDir` to it from main).

Having a single helper means future drift between the three call sites is impossible - they all delegate to one function.

## Files

**Modified**:

| File | Change |
|---|---|
| `internal/api/handler.go` | Add `ResolveDownloadDir()` method on `Handler` (already has `DownloadDir string` field at line 55) |
| `internal/api/config.go` | `handleGetConfig` uses `h.ResolveDownloadDir()` to override `cfg["download_dir"]` before returning JSON |
| `internal/api/directory.go` | Replace lines 25, 94 store reads with `h.ResolveDownloadDir()` |
| `internal/sabnzbd/handler.go` | Add `DownloadDir` field to the handler, add `ResolveDownloadDir()` method, replace store reads at lines 63, 81 |
| `cmd/iplayer-arr/main.go` | Pass the env-derived `downloadDir` value when constructing the sabnzbd handler (near line 70 where `download.NewManager` is constructed and line 119 where `apiHandler.DownloadDir` is set) |

**New test files**:

| File | New tests |
|---|---|
| `internal/api/config_test.go` (may exist or be new) | 3 tests on `handleGetConfig` behaviour |
| `internal/api/resolve_test.go` (new) | 2 tests on `ResolveDownloadDir` helper directly |
| `internal/api/directory_test.go` (may exist or be new) | 1 test on `handleListDirectory` with env set |
| `internal/sabnzbd/handler_test.go` (may exist or be new) | 1 test on the SABnzbd compat path with env set |

**Total new tests**: 7

## Task 2.1 - Add `ResolveDownloadDir` helper

In `internal/api/handler.go` (the struct that already holds `DownloadDir string`), add:

```go
// ResolveDownloadDir returns the active download directory path,
// honouring precedence: env-var > store > default.
func (h *Handler) ResolveDownloadDir() string {
    if h.DownloadDir != "" {
        return h.DownloadDir
    }
    if stored, err := h.store.GetConfig("download_dir"); err == nil && stored != "" {
        return stored
    }
    return configDefaults["download_dir"]
}
```

**Note on `GetConfig` signature**: `store.GetConfig` returns `(string, error)` per `internal/store/config.go:5`. The error is non-nil only on BoltDB-level failures (missing bucket, IO error, etc.) - not on missing keys, which return an empty string with nil error. The pattern above treats any error as "no stored value, fall through to default".

## Task 2.2 - Use `ResolveDownloadDir` in `handleGetConfig`

In `internal/api/config.go::handleGetConfig` (around line 18), after the `for _, key := range configKeys` loop builds `cfg`, add an explicit override:

```go
cfg["download_dir"] = h.ResolveDownloadDir()
```

This ensures the `/api/config` response returns the correct active value regardless of what the store contains.

## Task 2.3 - Use `ResolveDownloadDir` in `directory.go`

At `internal/api/directory.go:25`, replace:

```go
downloadDir := h.store.GetConfig("download_dir")
if downloadDir == "" {
    downloadDir = "/downloads"
}
```

with:

```go
downloadDir := h.ResolveDownloadDir()
```

Same replacement at line 94 (second handler function in the same file).

## Task 2.4 - Plumb `DownloadDir` to the sabnzbd handler

`internal/sabnzbd/handler.go` is in a different package and currently reads the download dir from the BoltDB store directly. Two options:

**Option A**: Add `DownloadDir string` field to the sabnzbd handler struct, set it from `main.go` at construction time, and add a `ResolveDownloadDir()` method that mirrors the api handler's.

**Option B**: Accept a reference to the api handler (or a `DownloadDirResolver` interface) and delegate.

**Recommendation**: Option A. Lower coupling, simpler to test. The sabnzbd handler already takes the store at construction, so adding one more constructor parameter is minimal surgery.

`cmd/iplayer-arr/main.go` at the sabnzbd handler construction site: pass `downloadDir` (the variable already in scope from line 30) into the constructor.

## Task 2.5 - Tests

**Test helper**: `internal/api/handler_test.go` already defines `testAPI(t) (*Handler, *store.Store)` at the top of the file. It creates a temp-dir BoltDB, pre-sets `api_key`, and returns a wired-up `*Handler`. Phase 2 tests reuse this helper - do NOT introduce a separate `testStore` in the api package.

**Test A** (`ResolveDownloadDir` with env set):
```go
func TestResolveDownloadDir_EnvWins(t *testing.T) {
    h, _ := testAPI(t)
    h.DownloadDir = "/data"
    if got := h.ResolveDownloadDir(); got != "/data" {
        t.Errorf("got %q, want /data", got)
    }
}
```

**Test B** (`ResolveDownloadDir` fallback to store):
```go
func TestResolveDownloadDir_StoreFallback(t *testing.T) {
    h, st := testAPI(t)
    st.SetConfig("download_dir", "/stored")
    h.DownloadDir = ""
    if got := h.ResolveDownloadDir(); got != "/stored" {
        t.Errorf("got %q, want /stored", got)
    }
}
```

**Test C** (`ResolveDownloadDir` fallback to default):
```go
func TestResolveDownloadDir_DefaultFallback(t *testing.T) {
    h, _ := testAPI(t)
    h.DownloadDir = ""
    if got := h.ResolveDownloadDir(); got != "/downloads" {
        t.Errorf("got %q, want /downloads", got)
    }
}
```

**Test D** (`handleGetConfig` returns env value):
Build a handler with `DownloadDir: "/data"`, call `handleGetConfig` via `httptest.NewRecorder`, assert the JSON response contains `"download_dir":"/data"`.

**Test E** (`handleGetConfig` returns fallback when env unset):
Same setup with `DownloadDir: ""`, assert response contains the store or default value.

**Test F** (directory listing handler uses env value):
Build a handler, set up a temp directory as `DownloadDir`, populate with known files, call `handleListDirectory`, assert the returned list matches the temp directory contents (not `/downloads`).

**Test G** (sabnzbd handler uses env value):
Similar to F but for the SABnzbd compat endpoint.

## Phase 2 verification gate

```bash
go build ./...
go test ./internal/api -v
go test ./internal/sabnzbd -v
```

Expected: clean build, all new tests pass, no regressions in existing api/sabnzbd tests.

## Phase 2 acceptance

- Setting `-e DOWNLOAD_DIR=/data` on a fresh container makes `/api/config` return `"download_dir":"/data"`
- The web UI settings page shows `/data` (still uneditable, as intended)
- Directory listings via `/api/downloads/directory` and `/api/downloads/directory/{folder}` reflect `/data` contents
- SABnzbd compat endpoint reports `/data`
- Files continue to actually download to `/data` (this already worked; fix only addresses the UI display and related consumers)

---

# Phase 3 - Issue #15: Match of the Day malformed daily title

## Root cause (verified by investigation)

BBC returns the subtitle for Match of the Day episodes in a composite format: `"2025/26: 22/03/2026"` (football season identifier prefix followed by broadcast date). This subtitle is then processed by the v1.1.0 title generation pipeline in `internal/newznab/titles.go`:

1. `isDateSubtitle` at `titles.go:~20` uses regex `reDateSubtitle` with pattern `^\s*\d{1,2}[/.\-]\d{1,2}[/.\-]\d{4}\s*$` - this requires 1-2 digit start, which fails on `"2025/26: 22/03/2026"` (starts with 4 digits).
2. The Tier 1.5 (daily soap) guard is skipped.
3. Code falls through to Tier 3 `buildDateTitle` which takes `(name, episode, airDate, quality)` and appends the sanitised episode string as an additional segment.
4. `sanitiseForTitle` strips unsafe characters (`[^a-zA-Z0-9.\- ]`), reducing `"2025/26: 22/03/2026"` to `"202526.22032026"`.
5. Final output: `Match.of.the.Day.2026.03.22.202526.22032026.1080p.WEB-DL.AAC.H264-iParr`.

This contains the air date three times: once in YYYY.MM.DD, once as a mangled season label, once as a DDMMYYYY date-run. Sonarr's daily-series parser cannot make sense of this string and returns "Unknown series".

**Secondary bug identified**: `internal/bbc/ibl.go::parseSubtitleNumbers` (lines 310-323). The current parser only attempts episode-number extraction when the subtitle contains `": "` (a colon followed by a space) - it splits on `": "` and runs `reEpisodeNum` on the part AFTER the split. This means bare-date subtitles like `"22/03/2026"` already return `(0, 0)` because they have no `": "` to split on.

**The actual defect** is with composite subtitles like `"2025/26: 22/03/2026"`:
1. `reSeriesNum` is tried on the whole subtitle - no match (`reSeriesNum` matches `Series|Cyfres|Season \d+`, not `2025/26`), so `series = 0`.
2. Split on `": "` yields `["2025/26", "22/03/2026"]`, `len(parts) == 2`, so episode extraction runs.
3. `epPart = "22/03/2026"`.
4. `reEpisodeNum` matches the leading `22` and extracts it as `episode = 22`.

So for Match of the Day, the function returns `(0, 22)` where 22 is the day-of-month, not any real episode number. This can cause Sonarr filter mismatches (the user's search for episode 3 would never match a Programme with `EpisodeNum=22` extracted from the date).

The fix needs to guard specifically the **composite** case, not the bare-date case. The bare-date case is already handled correctly by the existing split-on-`": "` logic.

## Fix strategy

Two narrow, anchored guards. One in `titles.go` to drop the episode segment when the subtitle is (effectively) a composite date. One in `ibl.go::parseSubtitleNumbers` to skip episode-number extraction when the subtitle is a bare date.

**Narrow, not broad**: only the specific composite-date patterns observed in the wild are matched. No general "composite subtitle parser" - that is deferred to v1.2.0. The goal of v1.1.1 is to fix the Match of the Day case without touching the working soap-title behaviour.

**Patterns to match** (anchored, whole-subtitle):

1. Bare date: `^\s*\d{1,2}[/.\-]\d{1,2}[/.\-]\d{4}\s*$` - already matched by `reDateSubtitle`, no change here
2. Composite with prefix: `^[^:]+:\s*\d{1,2}[/.\-]\d{1,2}[/.\-]\d{4}\s*$` - NEW pattern, catches Match of the Day format

Both patterns must be **fully anchored** so they don't false-positive on real episode titles like `"Series 22, Episode 3"`, `"Episode 3 (aired 22/03/2026)"`, or `"Series 1: 2. The Cave of Skulls"`.

**Action when matched**: drop the episode segment entirely. The air date already carries the identifying information; an additional string appended to the filename would only corrupt it.

## False-positive considerations

The regex must not match these subtitles (all are legitimate episode titles):

| Subtitle | Why it must not match |
|---|---|
| `"Series 22, Episode 3"` | Contains `22` and `3` but not in `D/M/Y` format |
| `"Episode 3 (aired 22/03/2026)"` | Contains a date but surrounded by other text |
| `"Series 1: 2. The Cave of Skulls"` | Colon-prefixed like the composite format but no trailing date |
| `"2024 World Cup Final"` | Starts with 4 digits but no date component |
| `"The 22nd Century"` | Contains `22` but no `D/M/Y` |

The anchored pattern `^[^:]+:\s*\d{1,2}[/.\-]\d{1,2}[/.\-]\d{4}\s*$` rejects all of the above because:
- `Series 1: 2. The Cave of Skulls` has `"2. The Cave of Skulls"` after the colon, not a date
- `Episode 3 (aired 22/03/2026)` has no colon
- `Series 22, Episode 3` has no date component at all
- `2024 World Cup Final` has no colon
- `The 22nd Century` has no colon and no date

## Files

| File | Change |
|---|---|
| `internal/newznab/titles.go` | Add `reCompositeDateSubtitle` regex constant, modify `isDateSubtitle` or `buildDateTitle` to use it |
| `internal/bbc/ibl.go` | In `parseSubtitleNumbers`, add an early return if the subtitle matches the bare-date pattern |
| `internal/newznab/titles_test.go` | Add Match of the Day positive test + 3 false-positive prevention tests |
| `internal/bbc/ibl_test.go` | Add date-only subtitle test for `parseSubtitleNumbers` |

**Total new tests**: 5

## Task 3.1 - Add composite-date regex and modify `buildDateTitle`

In `internal/newznab/titles.go`, near the existing `reDateSubtitle` definition:

```go
// reCompositeDateSubtitle matches subtitles where a prefix is followed
// by a date, e.g. "2025/26: 22/03/2026" (BBC Match of the Day format).
// Anchored so it does not false-positive on episode titles that
// merely contain a date substring.
var reCompositeDateSubtitle = regexp.MustCompile(
    `^[^:]+:\s*\d{1,2}[/.\-]\d{1,2}[/.\-]\d{4}\s*$`,
)

// isDateLikeSubtitle reports whether a subtitle is either a bare date
// (YYYY/MM/YYYY) or a composite prefix-and-date format. Returns true
// for cases where the subtitle carries no episode information worth
// preserving in the output filename.
func isDateLikeSubtitle(s string) bool {
    return reDateSubtitle.MatchString(s) || reCompositeDateSubtitle.MatchString(s)
}
```

Then in `buildDateTitle` (near line 117 of the current titles.go), add a guard at the top:

```go
func buildDateTitle(name, episode, airDate, quality string) string {
    if isDateLikeSubtitle(episode) {
        episode = ""
    }
    // ... existing body ...
}
```

This drops the episode segment when the subtitle is itself date-dominant.

## Task 3.2 - Add composite-date guard to `parseSubtitleNumbers`

In `internal/bbc/ibl.go::parseSubtitleNumbers` (lines 310-323), add a guard that prevents the day-of-month from being extracted as an episode number when the `epPart` (the string after the `": "` split) is itself a date.

Current code shape:
```go
func parseSubtitleNumbers(subtitle string) (series, episode int) {
    if m := reSeriesNum.FindStringSubmatch(subtitle); len(m) > 1 {
        series, _ = strconv.Atoi(m[1])
    }

    parts := strings.SplitN(subtitle, ": ", 3)
    if len(parts) >= 2 {
        epPart := parts[len(parts)-1]
        if m := reEpisodeNum.FindStringSubmatch(epPart); len(m) > 1 {
            episode, _ = strconv.Atoi(m[1])
        }
    }

    return series, episode
}
```

Add a new package-level regex and an early-continue inside the `if len(parts) >= 2` block:

```go
// reDateEpPart matches an epPart that is itself a bare date like
// "22/03/2026" or "22.03.2026". When the composite split yields a
// date as the trailing part, the leading digits are day-of-month,
// not an episode number, and must not be extracted.
var reDateEpPart = regexp.MustCompile(`^\s*\d{1,2}[/.\-]\d{1,2}[/.\-]\d{4}\s*$`)

func parseSubtitleNumbers(subtitle string) (series, episode int) {
    if m := reSeriesNum.FindStringSubmatch(subtitle); len(m) > 1 {
        series, _ = strconv.Atoi(m[1])
    }

    parts := strings.SplitN(subtitle, ": ", 3)
    if len(parts) >= 2 {
        epPart := parts[len(parts)-1]
        if reDateEpPart.MatchString(epPart) {
            // epPart is itself a date; the leading digits are day-of-month,
            // not episode number. Leave episode = 0.
            return series, 0
        }
        if m := reEpisodeNum.FindStringSubmatch(epPart); len(m) > 1 {
            episode, _ = strconv.Atoi(m[1])
        }
    }

    return series, episode
}
```

This is a single-regex guard at exactly the right point in the function. No other behaviour changes.

## Task 3.3 - Tests in `titles_test.go`

**Note on conventions**: the existing tests in `titles_test.go` (e.g. `TestGenerateTitleChristmasSpecial` at line 97) call `GenerateTitle(p, "1080p", nil)` with a bare string literal for quality and compare the returned tier against `store.TierFull`/`store.TierDate` (fully qualified). The snippets below follow that convention.

**Positive test** - Match of the Day produces clean daily title:
```go
func TestGenerateTitle_SportsDateSubtitle(t *testing.T) {
    prog := &store.Programme{
        Name:       "Match of the Day",
        Episode:    "2025/26: 22/03/2026",
        Series:     0,
        EpisodeNum: 0,
        AirDate:    "2026-03-22",
    }
    title, tier := GenerateTitle(prog, "1080p", nil)
    if tier != store.TierDate {
        t.Errorf("tier = %q, want store.TierDate", tier)
    }
    want := "Match.of.the.Day.2026.03.22.1080p.WEB-DL.AAC.H264-iParr"
    if title != want {
        t.Errorf("title = %q, want %q", title, want)
    }
    if strings.Contains(title, "202526") || strings.Contains(title, "22032026") {
        t.Errorf("title contains garbled date tail: %q", title)
    }
}
```

**Negative test 1** - Colon-prefixed series title is NOT matched:
```go
func TestGenerateTitle_SeriesEpisodeTitle_NotMatched(t *testing.T) {
    prog := &store.Programme{
        Name:       "Doctor Who",
        Episode:    "Series 1: 2. The Cave of Skulls",
        Series:     1,
        EpisodeNum: 2,
    }
    title, _ := GenerateTitle(prog, "1080p", nil)
    if !strings.Contains(title, "Cave") {
        t.Errorf("expected episode title preserved, got %q", title)
    }
}
```

**Negative test 2** - Date in parens is NOT matched:
```go
func TestGenerateTitle_DateInParens_NotMatched(t *testing.T) {
    prog := &store.Programme{
        Name:    "Horizon",
        Episode: "Episode 3 (aired 22/03/2026)",
        Series:  1,
        EpisodeNum: 3,
    }
    title, _ := GenerateTitle(prog, "1080p", nil)
    if !strings.Contains(title, "Episode.3") {
        t.Errorf("expected Episode.3 preserved, got %q", title)
    }
}
```

**Negative test 3** - Bare text subtitle is unchanged:
```go
func TestGenerateTitle_PlainSubtitle_NotMatched(t *testing.T) {
    prog := &store.Programme{
        Name:    "Newsnight",
        Episode: "Climate Change Special",
        Series:  1,
        EpisodeNum: 42,
    }
    title, _ := GenerateTitle(prog, "1080p", nil)
    if !strings.Contains(title, "Climate.Change.Special") {
        t.Errorf("expected episode title preserved, got %q", title)
    }
}
```

## Task 3.4 - Tests in `ibl_test.go`

Two tests - the first is the actual fix, the second is a regression guard that locks in the existing correct behaviour for bare dates.

**Test 1** (the actual fix - currently fails, must pass after the guard is added):
```go
func TestParseSubtitleNumbers_CompositeDateDoesNotExtractEpisode(t *testing.T) {
    // "2025/26: 22/03/2026" is BBC's Match of the Day composite format.
    // Without the guard, the "22" at the start of "22/03/2026" is
    // extracted as EpisodeNum. With the guard, it's correctly skipped.
    series, episode := parseSubtitleNumbers("2025/26: 22/03/2026")
    if series != 0 || episode != 0 {
        t.Errorf("parseSubtitleNumbers(composite) = (%d, %d), want (0, 0)", series, episode)
    }
}
```

**Test 2** (regression guard for existing correct behaviour):
```go
func TestParseSubtitleNumbers_BareDateReturnsZero(t *testing.T) {
    // Bare dates have no ": " so the existing split-on-colon logic
    // already skips episode extraction. This test locks in that
    // behaviour so the new guard in Test 1 doesn't accidentally
    // regress it.
    series, episode := parseSubtitleNumbers("22/03/2026")
    if series != 0 || episode != 0 {
        t.Errorf("parseSubtitleNumbers(\"22/03/2026\") = (%d, %d), want (0, 0)", series, episode)
    }
}
```

**Note on the bare-date test**: this test already passes against the current code (bare dates already correctly return `(0, 0)` because the parser splits on `": "` and bare dates have no colon). It is included as a regression guard, not as part of the fix.

## Phase 3 verification gate

```bash
go build ./...
go test ./internal/newznab -v
go test ./internal/bbc -v
```

Expected: clean build, all existing tests (including the v1.0.2 Little Britain / EastEnders / Cunk on Britain cases) still pass, all 5 new tests pass.

## Phase 3 acceptance

- A Sonarr tvsearch for Match of the Day produces `Match.of.the.Day.2026.03.22.1080p.WEB-DL.AAC.H264-iParr` (clean single-date format)
- Sonarr's Daily-series parser accepts the filename
- "Unknown series" error is gone for Match of the Day
- Existing v1.0.2 daily soap handling (EastEnders, Holby City, etc.) continues to work unchanged
- Episode titles containing incidental date substrings are not stripped

---

# Phase 4 - Issue #18: Doctor Who duplicate-name disambiguation

## Root cause (verified by live BBC + Skyhook API calls)

BBC iPlayer currently has **three** Doctor Who programmes in its catalogue:

| BBC title | BBC PID | Notes |
|---|---|---|
| `Doctor Who` | `p0gglvqn` | Current 2024+ Ncuti Gatwa era |
| `Doctor Who (1963-1996)` | `p0ggwr8l` | Classic series, note en-dash in actual title |
| `Doctor Who (2005-2022)` | `b006q2x0` | Legacy modern era, note en-dash in actual title |

(The year separators in BBC's titles are the **en-dash** character U+2013, not the ASCII hyphen. This matters for regex construction.)

Skyhook (the TheTVDB-to-BBC title resolver used by iplayer-arr) returns the following for the two Doctor Who TVDB IDs:

| TVDB ID | Show | Skyhook title | Skyhook firstAired |
|---|---|---|---|
| 76107 | Classic Doctor Who | `"Doctor Who"` (no suffix) | `1963-11-23` |
| 78804 | Modern Doctor Who | `"Doctor Who (2005)"` (start-year only, no en-dash) | `2005-03-26` |

**The v1.0.2 strict-equality name filter is broken for both TVDB IDs**:

- For TVDB 76107 (classic), `filterName = "Doctor Who"`. The strict `EqualFold` check matches only BBC's bare `"Doctor Who"` (the current 2024+ series), missing the actual classic. User gets Ncuti Gatwa episodes when searching for Hartnell.
- For TVDB 78804 (modern), `filterName = "Doctor Who (2005)"`. This does not string-match BBC's `"Doctor Who (2005-2022)"` (different suffix format, different punctuation), so the filter rejects the actual modern series. User likely gets either nothing or the wrong show.

The v1.1.0 `writeResultsRSS` rewrite is orthogonal to this bug and does not address it.

## Mixed-fragment filename pattern

The reported filename `Doctor.Who.19631996.S01E02.Season.1.2.The.Devils.Chord.1080p.WEB-DL.AAC.H264-iParr` mixes the classic series brand name (`Doctor Who (1963-1996)` -> sanitised to `Doctor.Who.19631996`) with a modern series episode subtitle (`Season 1: 2. The Devils Chord` -> sanitised to `Season.1.2.The.Devils.Chord`). Either:

- **Hypothesis A**: There is a state-mixing bug in `writeResultsRSS` where one Programme's Name and another Programme's Episode subtitle end up on the same emitted item
- **Hypothesis B**: BBC's `ListEpisodes` for one of the Doctor Who brands returns episodes that belong to a different programme, and the title was built from a single coherent (but semantically wrong) Programme struct

A dedicated state-mixing investigation is part of Task 4.7 and its findings are folded into the implementation.

### State-mixing investigation findings

A dedicated code-explorer pass was run over `writeResultsRSS`, `iblResultToProgramme`, `ibl.Search`, `ibl.ListEpisodes`, and every goroutine/closure/loop in the relevant packages. The agent also cross-referenced against the existing test fixtures in `internal/bbc/ibl_test.go`.

**Verdict: Hypothesis B confirmed. No state-mixing bug exists in the Go code.**

**Evidence for no-bug:**

1. **Loop variable capture**: `writeResultsRSS` uses `for _, it := range filtered` where `it` is a `filteredItem` struct (value, not pointer). At `search.go:155`, the loop body immediately extracts `res, prog := it.res, it.prog` as local variables. The inner loop `for _, qual := range qualities` uses a string value. No closures are created, no goroutines are spawned inside the emit loop. The module declares `go 1.24.13` in `go.mod`, which has the post-1.22 per-iteration loop variable semantics, ruling out pre-1.22 capture bugs regardless.

2. **Programme pointer aliasing**: `iblResultToProgramme` at `search.go:237-249` returns a freshly-allocated `*store.Programme` per call via composite literal (`return &store.Programme{...}`). No aliasing is possible between different results' `prog` pointers. The only post-construction mutation is `prog.IdentityTier = tier` at `search.go:189`, which only affects the current iteration's programme.

3. **Brand expansion in `ibl.Search`**: at `ibl.go:109-124`, brand-type results are expanded via `ListEpisodes(r.ID)`. The loop is purely sequential with no goroutines. Lines 115-122 patch `Channel` and `Thumbnail` from the parent brand's search entry for episodes that lack them, but `Title` is NOT patched - it comes entirely from `e.Title` in BBC's episodes API response.

4. **`PrefetchPIDs` concurrency**: the quality prober at `prober.go:86` uses goroutines and a `sync.Mutex`, but it is fully awaited via `wg.Wait()` before `writeResultsRSS` proceeds to the emit loop. The goroutines only write into a pre-allocated `map[string][]int` keyed by PID, with no interaction with `Programme` structs.

5. **Package-level state**: the only package-level variables in `internal/newznab/` are read-only regex patterns. No shared caches, no `sync.Once` initialisation that could carry data between invocations.

6. **BBC API test fixture evidence**: `internal/bbc/ibl_test.go:60-64` (`TestListEpisodesNormalisesLooseAirDate`) shows the test fixture uses `"title": "EastEnders"` for every episode in the `/programmes/{pid}/episodes` response - confirming that BBC's episodes endpoint returns the brand name as every episode's `title` field, not per-episode titles. For classic Doctor Who brand `p0ggwr8l`, every episode returned by `ListEpisodes` is expected to have `Title = "Doctor Who (1963-1996)"`.

**What the mixed-fragment filename actually means:**

The reported filename `Doctor.Who.19631996.S01E02.Season.1.2.The.Devils.Chord.1080p.WEB-DL.AAC.H264-iParr` was produced by a single `*store.Programme` struct that legitimately received `Name = "Doctor Who (1963-1996)"` and `Episode = "Season 1: 2. The Devils Chord"` from a single `IBLResult`. The code faithfully copied BBC's API data into the Programme struct. The mismatch exists inside BBC's own metadata:

- **Scenario B1 (most likely)**: BBC's `/programmes/p0ggwr8l/episodes` endpoint returned a 2024-era Ncuti Gatwa episode (with subtitle `"Season 1: 2. The Devils Chord"`) under the classic brand PID. This would be a BBC metadata-catalogue error where a modern episode was incorrectly associated with the classic brand at the `tleo_id` level.

- **Scenario B2**: BBC's `/new-search?q=Doctor+Who` endpoint returned an `episode`-type result where `title` was set to the classic brand's long-form name `"Doctor Who (1963-1996)"` but `subtitle` came from a modern series episode. BBC's search index sometimes cross-references brands in ways that produce this inconsistency.

Both scenarios are BBC-side data quality problems, not iplayer-arr bugs.

### Implications for the Phase 4 fix

**The disambiguation fix (Tasks 4.1-4.5) is still necessary and valuable:**
- It correctly routes Sonarr requests for classic / 2005-2022 / current-era Doctor Who to the right BBC brand via the `filterYear` year-range check
- It eliminates the common-case failure where the v1.0.2 strict-equality filter misroutes Sonarr to the wrong brand
- It handles shows beyond Doctor Who where BBC has multiple year-suffixed brands (Casualty reboots, Top of the Pops archive, etc.)

**But the disambiguation fix does NOT fully eliminate the reported bug:**
- It operates on programme names, not BBC PID identities
- If BBC's `/programmes/p0ggwr8l/episodes` response genuinely contains modern-era episodes (Scenario B1), those episodes will still be returned and will still emit RSS items with `Name = "Doctor Who (1963-1996)"` and a modern subtitle
- The v1.1.1 release notes must honestly disclose this residual risk

**No code change is made in Task 4.6.** The task closes as "investigated, no Go-level bug found" and the residual risk is documented in the release notes.

## Fix strategy

Regardless of the state-mixing investigation outcome, the disambiguation fix is needed: the current filter can't distinguish between BBC's three Doctor Who programmes. Introduce four helper functions and update `lookupTVDBTitle`, `matchesSearchFilter`, and `writeResultsRSS`:

**1. `bareName(s string) string`** - strip a trailing year suffix from a programme name. Only year-shaped suffixes are stripped (`(YYYY)`, `(YYYY-YYYY)`, `(YYYY YYYY)` with ASCII hyphen or en-dash). Non-year parenthesised suffixes like `(Special Edition)` are preserved.

```go
var reYearSuffix = regexp.MustCompile(`\s*\((\d{4})(?:[-\x{2013}]\d{4})?\)\s*$`)

func bareName(s string) string {
    return reYearSuffix.ReplaceAllString(s, "")
}
```

**2. `extractYearRange(s string) (start, end int)`** - parse the year suffix from a programme name. Returns `(0, 0)` if no suffix is present. Returns `(Y, Y)` for single-year suffixes `(YYYY)`. Returns `(S, E)` for range suffixes `(YYYY-YYYY)` (either dash character).

```go
var reYearRange = regexp.MustCompile(`\((\d{4})(?:[-\x{2013}](\d{4}))?\)\s*$`)

func extractYearRange(s string) (start, end int) {
    m := reYearRange.FindStringSubmatch(s)
    if m == nil {
        return 0, 0
    }
    start, _ = strconv.Atoi(m[1])
    if m[2] != "" {
        end, _ = strconv.Atoi(m[2])
    } else {
        end = start
    }
    return start, end
}
```

**3. `nameMatchesWithYear(progName, wantName string, yearHint int) bool`** - case-insensitive bare-name equality plus per-candidate year overlap check. Returns `true` only when the bare names match AND the programme's year range covers the year hint (or no year hint is provided).

```go
func nameMatchesWithYear(progName, wantName string, yearHint int) bool {
    if !strings.EqualFold(bareName(progName), bareName(wantName)) {
        return false
    }
    if yearHint == 0 {
        return true
    }
    start, end := extractYearRange(progName)
    if start == 0 && end == 0 {
        // No year suffix on the candidate. Whether this is a match
        // depends on whether other candidates DO have year suffixes -
        // that tiebreak lives in disambiguateByYear, not here.
        return true
    }
    return yearHint >= start && yearHint <= end
}
```

**4. `disambiguateByYear(progs []Programme, yearHint int) []Programme`** - set-level tiebreak. When the candidate list contains both year-suffixed matches (that include `yearHint`) and bare-name matches (no suffix), prefer the year-suffixed ones. When the candidate list is all bare-name matches, return all of them (no info to disambiguate). When `yearHint == 0`, return the input unchanged.

```go
func disambiguateByYear(progs []Programme, yearHint int) []Programme {
    if yearHint == 0 || len(progs) <= 1 {
        return progs
    }
    var suffixed, bare []Programme
    for _, p := range progs {
        start, end := extractYearRange(p.Name)
        switch {
        case start == 0 && end == 0:
            bare = append(bare, p)
        case yearHint >= start && yearHint <= end:
            suffixed = append(suffixed, p)
        }
    }
    if len(suffixed) > 0 {
        return suffixed
    }
    return bare
}
```

## Scope boundary: episode-type IBL results with episode titles

BBC's `/new-search` endpoint can return two kinds of result for a broad query like `q=Doctor Who`:

1. **Brand-level results** where `r.Title` is the show name - e.g. `"Doctor Who (1963-1996)"`, `"Doctor Who"`, or `"Doctor Who (2005-2022)"`. These are the entries Phase 4 is designed to disambiguate.
2. **Episode-level results** where `r.Title` is the individual episode title - e.g. `"The Unquiet Dead"` (a Series 1 episode of the 2005 reboot). The repo's own `internal/bbc/testdata/ibl_search.json` fixture shows examples of this shape.

**Phase 4 does not match episode-level results at all**. `nameMatchesWithYear(prog.Name, wantName, yearHint)` does a bare-name equality check against the show name the user asked for (`"Doctor Who"`), so `"The Unquiet Dead"` is rejected before reaching `disambiguateByYear`.

**This is the same behaviour as v1.0.2**. The pre-Phase-4 filter uses `strings.EqualFold(prog.Name, wantName)` which also rejects episode-titled results. Phase 4 does not make this worse and does not make it better - it preserves the existing filter semantics and layers year disambiguation on top of them.

**Why this is correct for TVDB-driven searches**: when Sonarr supplies a TVDB ID, the user is explicitly asking for a specific series. The correct candidate set is "all episodes of the brand(s) matching that show name". Direct episode-title matches from IBL's search are usually the first episode of a brand that's already going to be expanded via `ListEpisodes(brandPID)`, so the expanded episodes cover those cases anyway. Rejecting the episode-title match as a direct candidate is not a loss.

**What Phase 4 does NOT cover**: if a user performs a free-text search like `?t=search&q=The+Unquiet+Dead` (no TVDB ID), the name filter will reject episode results with titles that don't equal the query string. This is a pre-existing v1.0.2 limitation, not something Phase 4 changes. A future v1.2.0 hardening pass could add brand identity tracking via `tleo_id` to resolve IBL results to their parent brand regardless of the result `type`, which would address this properly. That refactor is explicitly out of scope for v1.1.1.

## Worked example - TVDB 76107 (classic Doctor Who)

1. Sonarr hits `/newznab/api?t=tvsearch&tvdbid=76107&season=1&ep=2`
2. `handleTVSearch` calls `lookupTVDBShow("76107")` which hits Skyhook and returns `("Doctor Who", 1963, nil)`
3. `filterName = "Doctor Who"`, `filterYear = 1963`
4. `ibl.Search("Doctor Who")` returns the three BBC brands (bare, classic, 2005-2022) plus other matches
5. `writeResultsRSS` calls `iblResultToProgramme` for each result, collecting Programme structs
6. `matchesSearchFilter` uses `nameMatchesWithYear("Doctor Who", "Doctor Who", 1963)` on each:
   - `"Doctor Who"` -> bare name match, no year suffix, returns `true` (kept for tiebreak)
   - `"Doctor Who (1963-1996)"` -> bare name match, year range `[1963, 1996]`, 1963 in range, returns `true`
   - `"Doctor Who (2005-2022)"` -> bare name match, year range `[2005, 2022]`, 1963 not in range, returns `false`
7. After per-candidate filtering, two matches remain: bare `"Doctor Who"` and `"Doctor Who (1963-1996)"`
8. `disambiguateByYear([bare, classic], 1963)` sees one suffixed match covering 1963 and keeps only that one
9. Only classic-series episodes flow through to title generation

## Worked example - TVDB 78804 (modern Doctor Who, 2005-2022)

1. Sonarr hits `/newznab/api?t=tvsearch&tvdbid=78804&season=1&ep=2`
2. Skyhook returns `("Doctor Who (2005)", 2005, nil)`
3. `filterName = "Doctor Who (2005)"`, `filterYear = 2005`
4. `ibl.Search` returns the same three BBC brands plus noise
5. `nameMatchesWithYear` is called with `wantName = "Doctor Who (2005)"`:
   - `bareName("Doctor Who (2005)")` strips to `"Doctor Who"`
   - `bareName("Doctor Who")` is `"Doctor Who"` (already bare)
   - `bareName("Doctor Who (1963-1996)")` is `"Doctor Who"`
   - `bareName("Doctor Who (2005-2022)")` is `"Doctor Who"`
   - All three match on bare name
6. Year overlap with 2005:
   - `"Doctor Who"` -> no year suffix, kept for tiebreak
   - `"Doctor Who (1963-1996)"` -> 2005 not in [1963, 1996], rejected
   - `"Doctor Who (2005-2022)"` -> 2005 in [2005, 2022], kept
7. `disambiguateByYear` keeps only the year-suffixed match
8. Only `"Doctor Who (2005-2022)"` episodes flow through

Both cases resolve to the correct single programme. The current 2024+ `Doctor Who` bare-name programme does not have a corresponding TVDB ID in this worked example, so it is excluded from both lookups (correctly).

## Files

**New**:

| File | Contents |
|---|---|
| `internal/newznab/disambiguate.go` | The four helper functions + the two regexes |
| `internal/newznab/disambiguate_test.go` | ~16 unit tests covering the helpers |

**Modified**:

| File | Change |
|---|---|
| `internal/newznab/search.go:278` (`lookupTVDBTitle`) | Rename to `lookupTVDBShow`, change return type to `(title string, year int, err error)`, parse `firstAired` from Skyhook JSON |
| `internal/newznab/search.go:38` (`handleTVSearch`) | Capture both `filterName` and `filterYear` from the lookup |
| `internal/newznab/search.go:92` (writeResultsRSS call) | Pass `filterYear` through |
| `internal/newznab/search.go:121` (`writeResultsRSS`) | Collect candidate Programmes, call `disambiguateByYear`, then iterate into title generation |
| `internal/newznab/search.go:~333` (`matchesSearchFilter`) | Use `nameMatchesWithYear` instead of `EqualFold` |
| `internal/newznab/handler_test.go` | Add integration tests for classic + modern Doctor Who |

## Task 4.1 - Create `disambiguate.go`

File contents per the four functions defined in the fix strategy above.

## Task 4.2 - Rename `lookupTVDBTitle` -> `lookupTVDBShow` and add Skyhook injection seam

**Current code** at `search.go:278` uses `http.Get("https://skyhook.sonarr.tv/v1/tvdb/shows/en/" + tvdbid)` with a hardcoded URL. This cannot be mocked by tests without global HTTP transport hacks. Part of this task adds a package-level indirection so `httptest.NewServer` can be injected cleanly.

Existing signature:
```go
func lookupTVDBTitle(tvdbid string) string
```

New signature + injection seam:
```go
// skyhookBaseURL is the base URL for TheTVDB-to-BBC title resolution
// via the Sonarr Skyhook service. Overridable in tests to point at
// httptest.NewServer without touching global HTTP transport.
var skyhookBaseURL = "https://skyhook.sonarr.tv"

func lookupTVDBShow(tvdbid string) (title string, year int, err error) {
    resp, err := http.Get(skyhookBaseURL + "/v1/tvdb/shows/en/" + tvdbid)
    if err != nil {
        return "", 0, err
    }
    defer resp.Body.Close()
    if resp.StatusCode != 200 {
        return "", 0, fmt.Errorf("skyhook returned %d", resp.StatusCode)
    }

    var show struct {
        Title      string `json:"title"`
        FirstAired string `json:"firstAired"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&show); err != nil {
        return "", 0, err
    }

    title = show.Title
    if len(show.FirstAired) >= 4 {
        year, _ = strconv.Atoi(show.FirstAired[:4])
    }
    log.Printf("[tvsearch] resolved TVDB %s -> %q (year %d)", tvdbid, title, year)
    return title, year, nil
}
```

The Skyhook JSON response includes a `firstAired` field (verified by API call: returns `"1963-11-23"` for TVDB 76107, `"2005-03-26"` for TVDB 78804). The first four characters are always `YYYY`.

**How tests inject a fake Skyhook**:
```go
func TestSearch_DoctorWhoClassicTVDB_OnlyMatchesClassicBrand(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Return mock Skyhook JSON for TVDB 76107
        w.Header().Set("Content-Type", "application/json")
        fmt.Fprint(w, `{"title":"Doctor Who","firstAired":"1963-11-23"}`)
    }))
    defer srv.Close()

    oldBase := skyhookBaseURL
    skyhookBaseURL = srv.URL
    t.Cleanup(func() { skyhookBaseURL = oldBase })

    // ... rest of the test exercises handleTVSearch normally ...
}
```

Package-level var swap in tests is the simplest injection pattern in Go and avoids a Handler struct refactor. The `t.Cleanup` ensures the original value is restored even if the test panics.

**Error handling**: if Skyhook fails (network, non-200 status, malformed JSON), return `("", 0, err)`. Callers at `handleTVSearch` check for empty title and fall back to the query string as `filterName` with `filterYear = 0`, preserving v1.0.2 behaviour for the offline-Skyhook case.

## Task 4.3 - Extend `SeriesMapping` with `Year` and update `handleTVSearch` to capture year (cache-aware)

**This task is larger than a simple rename because the TVDB mapping cache is persisted to BoltDB and currently stores `ShowName` only.** Without the extension below, the warm-cache path at `search.go:49` would bypass `lookupTVDBShow` on repeat searches, returning only the cached name with no year hint - meaning `disambiguateByYear` would never receive the input it needs and the Phase 4 fix would silently regress after the first lookup for each series.

### Task 4.3a - Add `Year int` to `store.SeriesMapping`

In `internal/store/types.go` (around line 67), extend the struct:

```go
type SeriesMapping struct {
    TVDBId    string    `json:"tvdb_id"`
    ShowName  string    `json:"show_name"`
    Year      int       `json:"year"`        // NEW - start year from Skyhook firstAired
    UpdatedAt time.Time `json:"updated_at"`
}
```

**Backward compatibility**: BoltDB stores `SeriesMapping` as JSON. Existing records written before v1.1.1 have no `year` field. JSON unmarshaling in Go silently leaves missing fields at their zero value, so old records deserialise with `Year = 0`. This is the signal for "no year known - fall through to Skyhook and refresh".

### Task 4.3b - Update the warm-cache path in `handleTVSearch`

Current code at `search.go:46-62`:

```go
if q == "" && tvdbid != "" {
    // Try stored mapping first
    if h.store != nil {
        mapping, _ := h.store.GetSeriesMapping(tvdbid)
        if mapping != nil {
            q = mapping.ShowName
        }
    }
    // Fall back to Skyhook (Sonarr's TVDB lookup service)
    if q == "" {
        q = lookupTVDBTitle(tvdbid)
        if q != "" && h.store != nil {
            h.store.PutSeriesMapping(&store.SeriesMapping{TVDBId: tvdbid, ShowName: q})
        }
    }
}
```

New logic (cache-aware, with gradual backfill):

```go
var filterYear int
if q == "" && tvdbid != "" {
    // Try stored mapping first
    var cachedMapping *store.SeriesMapping
    if h.store != nil {
        cachedMapping, _ = h.store.GetSeriesMapping(tvdbid)
        if cachedMapping != nil && cachedMapping.Year > 0 {
            // Warm cache hit with year - use it, skip Skyhook
            q = cachedMapping.ShowName
            filterYear = cachedMapping.Year
        }
    }
    // Fall back to Skyhook when: (1) no mapping, or (2) mapping exists
    // but has Year == 0 (pre-v1.1.1 record that needs backfill)
    if q == "" {
        title, year, err := lookupTVDBShow(tvdbid)
        if err == nil && title != "" {
            q = title
            filterYear = year
            if h.store != nil {
                h.store.PutSeriesMapping(&store.SeriesMapping{
                    TVDBId:   tvdbid,
                    ShowName: title,
                    Year:     year,
                })
            }
        }
    }
}
```

**Behaviour summary**:
- **New install, first tvdbid search**: no mapping exists, Skyhook is called, mapping is written with `Year` set. Subsequent searches use the warm cache directly.
- **Existing install upgrading from v1.1.0, first tvdbid search after upgrade**: existing mapping exists but has `Year == 0` (pre-v1.1.1 record). The warm-cache branch is skipped (because `Year > 0` is false), Skyhook is called, the mapping is rewritten with `Year` now populated. Subsequent searches are warm.
- **Existing install, subsequent tvdbid search after backfill**: warm cache returns both `ShowName` and `Year`, no Skyhook call needed.
- **Skyhook unavailable on first lookup**: `lookupTVDBShow` returns an error. The outer code falls back to treating `q` as empty, the writeResultsRSS path receives `filterYear == 0`, and `disambiguateByYear` returns the candidate set unchanged (same as v1.0.2 behaviour - no regression).

### Task 4.3c - Pass `filterYear` to `writeResultsRSS`

Modify the `writeResultsRSS` signature (current `search.go:121`) to accept `filterYear int` as a new parameter. Update both call sites (`search.go:35` from `handleSearch` which passes `0`, and `search.go:92` from `handleTVSearch` which passes the captured `filterYear`).

```go
// In handleSearch (text search, no TVDB info):
h.writeResultsRSS(w, r, results, 0, 0, "", filterName, 0)

// In handleTVSearch (may have TVDB-derived year):
h.writeResultsRSS(w, r, results, season, ep, filterDate, filterName, filterYear)
```

### Task 4.3d - Regression test for the cache warm path

Add a test in `internal/newznab/handler_test.go` (alongside the existing `TestHandle*` tests). The newznab package already has its own test helpers - `fakeBBCSearchServer(t, payload)` for mocking BBC, `newHandlerWithBBC(t, payload)` for wiring a Handler, `mockProber` for quality prefetch, plus JSON payload constants like `eastendersOneEpisodePayload`. The warm-cache test needs a slight extension because the existing helpers pass `nil` for the store, and we need a real store to pre-populate the `SeriesMapping`.

**Extend the existing helper set**: add a new package-test helper `newHandlerWithBBCAndStore(t, payload) (*Handler, *store.Store)` that mirrors `newHandlerWithBBC` but wires a real temp-dir BoltDB store. Place it next to the existing helpers near the top of `handler_test.go`.

```go
// newHandlerWithBBCAndStore is a variant of newHandlerWithBBC that also
// wires a real BoltDB store so tests can pre-populate SeriesMapping
// records for cache-warm-path assertions.
func newHandlerWithBBCAndStore(t *testing.T, payload string) (*Handler, *store.Store) {
    t.Helper()
    srv := fakeBBCSearchServer(t, payload)
    // The existing newHandlerWithBBC does the same IBL wiring; copy
    // its pattern. If the IBL client already has a base-URL override
    // mechanism (check existing newHandlerWithBBC body), use that.
    ibl := bbc.NewIBLWithBaseURL(srv.URL)  // or equivalent

    st, err := store.Open(filepath.Join(t.TempDir(), "newznab-test.db"))
    if err != nil {
        t.Fatalf("store.Open: %v", err)
    }
    t.Cleanup(func() { st.Close() })

    h := NewHandler(ibl, st, nil, nil)
    return h, st
}
```

**Note on IBL wiring**: the exact form of the BBC IBL constructor depends on what's already in `internal/bbc/ibl.go`. If there's no base-URL override today, Task 4.3d becomes slightly larger - either add a `NewIBLWithBaseURL` constructor (same pattern as the `skyhookBaseURL` injection seam in Task 4.2) or use the approach the existing `newHandlerWithBBC` helper already takes. Implementors should read `newHandlerWithBBC` at the top of `handler_test.go` and mirror whatever pattern it uses today. Do NOT introduce a new injection pattern in this task - reuse what's there.

### Warm-cache test

**Test fixture choice**: the synthetic IBL payload below uses `type: "episode"` rather than `type: "programme"`. This is deliberate. `type: "programme"` triggers `IBL.Search` to call `ListEpisodes(brandPID)` for each result (`internal/bbc/ibl.go:111`), which hits `/programmes/{pid}/episodes`. The existing `fakeBBCSearchServer` (`internal/newznab/handler_test.go:18`) is a path-agnostic test double - it returns the **same JSON payload for every request URL** regardless of path. So a `programme`-type fixture would not get a 404 from the test server; instead `ListEpisodes` would receive the search-shaped JSON when it expected an episodes-shaped response, fail to parse the structure it expected, and return zero episodes (or an error). Either way, the test produces no RSS items and the assertions never run.

`type: "episode"` results bypass brand expansion - they flow directly from `IBL.Search` into `iblResultToProgramme` and then into the filter chain. That's exactly the path the warm-cache regression test needs to exercise, because the disambiguation logic operates on `prog.Name` regardless of how the Programme was constructed. The episode-level shortcut is therefore the correct fixture for unit-testing the cache + disambiguation interaction without dragging brand expansion into scope.

The titles in the synthetic payload (`"Doctor Who"`, `"Doctor Who (1963-1996)"`, `"Doctor Who (2005-2022)"`) deliberately mirror BBC's actual brand-level titles. In production, these come either from search results that already carry the brand name in their `title` field, or from `ListEpisodes` expansions where every episode inherits the parent brand's title. The fact that the synthetic test fixture skips expansion does not affect the validity of the disambiguation assertion.

**Subtitle format choice**: the subtitles below use `"Series 1: Episode 2"` (with the colon-and-space separator) rather than `"Series 1 Episode 2"`. This matters because `parseSubtitleNumbers` in `internal/bbc/ibl.go:303` only attempts episode-number extraction *after* splitting on `": "`. Without the colon, `EpisodeNum` would stay 0 and the test's `&ep=2` filter would reject all three candidates before the disambiguation logic ran. The `"Series N: Episode M"` form is the same one v1.0.2 fixed for Little Britain (see CHANGELOG v1.0.2 entry); the parser handles it correctly via `reEpisodeNum`.

**Test design - asserting cache-was-used, not just disambiguation-was-correct**: a naive version of this test could pre-populate the cache, run the request, and assert the right RSS comes out. The problem with that design: an implementation that ignores the cache and re-fetches from Skyhook would also pass the assertion, because Skyhook would return the same `("Doctor Who", 1963)` for TVDB 76107. The naive test would be redundant with the existing Phase 4 integration test (`TestSearch_DoctorWhoClassicTVDB_OnlyMatchesClassicBrand`) and would not actually guard the warm-cache invariant.

The fix is to point `skyhookBaseURL` (the injection seam from Task 4.2) at a fail-fast `httptest.NewServer` that records every request and `t.Errorf`s if it's called at all. The test then asserts both: (a) the RSS contains the classic brand and not the 2005-2022 brand (disambiguation correctness), AND (b) the fail-fast Skyhook server received zero hits (cache-was-used invariant). If a future change to `handleTVSearch` skips the warm-cache branch and falls through to Skyhook, assertion (b) fails immediately - even though Skyhook would still produce data that satisfies (a).

```go
const doctorWhoThreeBrandsPayload = `{
    "new_search": {
        "results": [
            {"id": "ep_modern", "type": "episode", "title": "Doctor Who", "subtitle": "Series 1: Episode 2", "release_date": "2024-05-18", "parent_position": 2},
            {"id": "ep_classic", "type": "episode", "title": "Doctor Who (1963-1996)", "subtitle": "Series 1: Episode 2", "release_date": "1963-11-30", "parent_position": 2},
            {"id": "ep_legacy", "type": "episode", "title": "Doctor Who (2005-2022)", "subtitle": "Series 1: Episode 2", "release_date": "2005-04-02", "parent_position": 2}
        ]
    }
}`

func TestSearch_DoctorWhoClassicTVDB_WarmCacheRetainsYear(t *testing.T) {
    // Scenario: TVDB 76107 has been looked up previously and the
    // mapping was cached with Year=1963. A new Sonarr search with
    // tvdbid=76107 must reuse the cached name AND the cached year,
    // so disambiguateByYear still routes to the classic brand WITHOUT
    // calling Skyhook at all.

    // Fail-fast Skyhook: any HTTP hit on this server is a test failure.
    // If the implementation correctly uses the warm cache, this server
    // receives zero requests. Tracks hits for an explicit final assertion
    // in case a hit somehow doesn't trigger t.Errorf in time.
    var skyhookHits int32
    failSkyhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        atomic.AddInt32(&skyhookHits, 1)
        t.Errorf("warm-cache test: Skyhook must not be called, but got %s %s", r.Method, r.URL.Path)
        http.Error(w, "warm-cache test: Skyhook unexpected", http.StatusServiceUnavailable)
    }))
    defer failSkyhook.Close()

    // Inject the fail-fast Skyhook via the package-level variable from Task 4.2
    oldSkyhookBase := skyhookBaseURL
    skyhookBaseURL = failSkyhook.URL
    t.Cleanup(func() { skyhookBaseURL = oldSkyhookBase })

    h, st := newHandlerWithBBCAndStore(t, doctorWhoThreeBrandsPayload)

    // Pre-populate the cache as if a previous Skyhook lookup had run
    if err := st.PutSeriesMapping(&store.SeriesMapping{
        TVDBId:   "76107",
        ShowName: "Doctor Who",
        Year:     1963,
    }); err != nil {
        t.Fatalf("PutSeriesMapping: %v", err)
    }

    // Hit the tvsearch endpoint
    req := httptest.NewRequest("GET", "/newznab/api?t=tvsearch&tvdbid=76107&season=1&ep=2", nil)
    w := httptest.NewRecorder()
    h.ServeHTTP(w, req)

    if w.Code != 200 {
        t.Fatalf("status = %d", w.Code)
    }
    body := w.Body.String()

    // Disambiguation correctness: classic brand emitted, modern brand not
    if !strings.Contains(body, "Doctor.Who.19631996") {
        t.Errorf("warm-cache test: expected classic brand name in RSS, got:\n%s", body)
    }
    if strings.Contains(body, "20052022") {
        t.Errorf("warm-cache test: unexpected 2005-2022 brand leaked through, got:\n%s", body)
    }

    // Cache-was-used invariant: Skyhook must not have been called
    if hits := atomic.LoadInt32(&skyhookHits); hits != 0 {
        t.Errorf("warm-cache test: expected 0 Skyhook hits, got %d", hits)
    }
}
```

This test is the regression guard for the warm-cache hole. It fires on **two** independent failure modes:

1. If a future change to `handleTVSearch` forgets to carry `filterYear` through the warm-cache branch, `disambiguateByYear` would receive `yearHint=0`, all three brands would pass, and the second body assertion (no 2005-2022 leakage) would fail.

2. If a future change to `handleTVSearch` skips the warm-cache branch entirely and falls through to Skyhook, the fail-fast Skyhook server records the hit, the inline `t.Errorf` fires, AND the final hit-count assertion fails. (The Skyhook hit produces the same data, so the body assertions would still pass - which is exactly why finding (1) alone is insufficient and finding (2) is necessary.)

**Required imports**: only `sync/atomic` is new. `net/http` and `net/http/httptest` are already imported in `internal/newznab/handler_test.go` (lines 6-7) by the existing test helpers. The test uses `atomic.AddInt32`/`atomic.LoadInt32` because the `httptest.Server` runs the handler in a goroutine - the inline `t.Errorf` is safe (testing.T is goroutine-safe per spec) but the int counter needs atomic access.

## Task 4.4 - Update `matchesSearchFilter` to use `bareName` and year

Signature change:
```go
func matchesSearchFilter(prog *store.Programme, wantName, filterDate string, filterSeason, filterEp int, filterYear int) bool {
    // ... existing date and season/episode checks ...
    if wantName != "" {
        if !nameMatchesWithYear(prog.Name, wantName, filterYear) {
            return false
        }
    }
    return true
}
```

## Task 4.5 - Update `writeResultsRSS` to call `disambiguateByYear`

Current shape (pseudo-code):
```go
for _, res := range results {
    prog := iblResultToProgramme(res)
    if !matchesSearchFilter(prog, ...) {
        continue
    }
    // emit RSS item
}
```

New shape:
```go
// First pass: collect all candidates that pass the per-item filter
var candidates []*store.Programme
for _, res := range results {
    prog := iblResultToProgramme(res)
    if !matchesSearchFilter(prog, ..., filterYear) {
        continue
    }
    candidates = append(candidates, prog)
}

// Set-level tiebreak: when year hint is provided, prefer year-suffixed matches
candidates = disambiguateByYear(candidates, filterYear)

// Second pass: emit RSS items for the filtered candidate set
for _, prog := range candidates {
    // emit RSS item
}
```

Note that `disambiguateByYear` as defined operates on `[]Programme` (value slice). The call site here uses `[]*store.Programme`. The function signature should be `func disambiguateByYear(progs []*store.Programme, yearHint int) []*store.Programme` to match - adjust the helper accordingly.

## Task 4.6 - State-mixing investigation closure

**Investigation complete. No code change required.**

The investigation findings are documented in full under the "State-mixing investigation findings" heading above. Summary:

- **Hypothesis B confirmed**: no state-mixing bug exists in the Go code. The pipeline is clean - no closure captures, no pointer aliasing, no shared Programme mutations, no concurrent emission.
- **Root cause of the mixed-fragment filename**: BBC's own metadata catalogue contains inconsistencies where modern episodes can appear under the classic brand PID (or an episode-type search result can have mismatched title and subtitle fields). This is a BBC data quality issue, not an iplayer-arr bug.
- **Task 4.6 is a documentation-only task**: no code changes. The investigation findings are recorded in the spec, the residual risk is disclosed in the release notes, and the disambiguation fix (Tasks 4.1-4.5) ships as the practical mitigation.

**What the implementor does for Task 4.6**: nothing beyond ensuring the investigation findings section above is accurate and the release notes include the residual-risk disclosure. No Go files are modified by this task.

**What the implementor does NOT do for Task 4.6**: attempt to add heuristic subtitle-vs-brand matching, attempt to cache PID-level cross-references, or otherwise compensate for BBC's data quirks. These are out of scope for v1.1.1 and would require a much larger design pass to do properly.

## Task 4.7 - Create `disambiguate_test.go`

~16 unit tests organised by helper:

**`bareName` (4 tests)**:
- `"Doctor Who"` -> `"Doctor Who"`
- `"Doctor Who (1963-1996)"` -> `"Doctor Who"` (ASCII hyphen)
- `"Doctor Who (1963\u20131996)"` -> `"Doctor Who"` (en-dash U+2013)
- `"Doctor Who (2005)"` -> `"Doctor Who"`
- `"Newsround (Special Edition)"` -> `"Newsround (Special Edition)"` (non-year suffix preserved)

**`extractYearRange` (4 tests)**:
- `"Doctor Who"` -> `(0, 0)`
- `"Doctor Who (2005)"` -> `(2005, 2005)`
- `"Doctor Who (1963-1996)"` -> `(1963, 1996)`
- `"Doctor Who (1963\u20131996)"` -> `(1963, 1996)` (en-dash)

**`nameMatchesWithYear` (4 tests)**:
- `("Doctor Who (1963-1996)", "Doctor Who", 1963)` -> `true`
- `("Doctor Who (2005-2022)", "Doctor Who", 1963)` -> `false`
- `("Doctor Who", "Doctor Who", 1963)` -> `true` (bare-name kept for caller-side tiebreak)
- `("Doctor Who", "Doctor Who", 0)` -> `true` (no year hint)

**`disambiguateByYear` (4 tests)**:
- Single match: `([{Doctor Who (1963-1996)}], 1963)` -> unchanged
- Multi with year hint matching one suffixed: `([{Doctor Who}, {DW 1963-1996}, {DW 2005-2022}], 1963)` -> only `{DW 1963-1996}`
- Multi with year hint matching none: `([{Doctor Who}, {DW 1963-1996}], 2030)` -> `[{Doctor Who}]` (falls back to bare)
- No year hint: `([{bare}, {suffixed}], 0)` -> unchanged

## Task 4.8 - Integration tests in `handler_test.go`

Two tests that exercise the full pipeline with mocked Skyhook and mocked IBL search:

**Test A** (classic Doctor Who):
```go
func TestSearch_DoctorWhoClassicTVDB_OnlyMatchesClassicBrand(t *testing.T) {
    // Mock Skyhook to return ("Doctor Who", 1963) for TVDB 76107
    // Mock IBL to return 3 Doctor Who brands (bare, 1963-1996, 2005-2022)
    // Call handleTVSearch with tvdbid=76107&season=1&ep=2
    // Assert the RSS response contains only episodes from the 1963-1996 brand
    // Assert no episodes from the modern 2005-2022 or bare current brands
}
```

**Test B** (modern Doctor Who):
```go
func TestSearch_DoctorWhoModernTVDB_OnlyMatchesModernBrand(t *testing.T) {
    // Mock Skyhook to return ("Doctor Who (2005)", 2005) for TVDB 78804
    // Same IBL mock
    // Assert only 2005-2022 brand episodes pass through
}
```

## Phase 4 verification gate

```bash
go build ./...
go test ./internal/newznab -v
go vet -copylocks -loopclosure ./internal/newznab/
```

Expected: clean build, all new tests pass, vet reports no issues.

## Phase 4 acceptance

- A Sonarr tvsearch with `tvdbid=76107` returns only classic Doctor Who (1963-1996) episodes, EXCEPT where BBC's own metadata mislabels a modern episode under the classic brand PID (see residual risk below)
- A Sonarr tvsearch with `tvdbid=78804` returns only modern Doctor Who (2005-2022) episodes, subject to the same BBC-data-quality caveat
- A direct text search for `q=Doctor+Who` with no TVDB ID returns all matching brands (no disambiguation applied)
- If Skyhook is unreachable, the filter falls back to the v1.0.2 bare-name behaviour (no regression for the offline case)
- The state-mixing investigation is documented in the spec and has been confirmed to not require code changes (Hypothesis B: BBC data quirk, not a Go-level bug)

**Residual risk**: if BBC's `/programmes/{pid}/episodes` endpoint returns modern-era episodes under a classic brand PID (Scenario B1), or the search endpoint returns episode-type results with mismatched title/subtitle fields (Scenario B2), the disambiguation fix cannot detect the inconsistency because it operates on programme names, not PID identities. An affected release will appear in the RSS feed with the classic brand name but a modern subtitle. This is disclosed in the release notes.

---

# Phase 5 - Final verification + commit

## Task 5.1 - Full test suite

```bash
go test ./...
```

Expected: all packages pass. Estimated test count after v1.1.1: approximately 212 (currently 177 after v1.1.0 + approximately 35 new).

## Task 5.2 - `go vet`

```bash
go vet ./...
```

Expected: no output, no warnings.

## Task 5.3 - `gofmt`

```bash
gofmt -l .
```

Expected: no output. If any files are listed, run `gofmt -w` on them.

## Task 5.4 - Residual `8191` check

```bash
grep -rn "8191" \
  --include="*.go" \
  --include="*.md" \
  --include="Dockerfile" \
  --include="*.yml" \
  --include="*.yaml" \
  --exclude="CHANGELOG.md" \
  --exclude-dir=superpowers \
  .
```

Expected: zero results (Phase 1 should have already achieved this). `CHANGELOG.md` and the `superpowers` directory are intentionally excluded because they contain legitimate migration documentation referencing the old port value. See the Phase 1 verification gate for why `--exclude-dir` uses the basename `superpowers` rather than the nested path `docs/superpowers`.

## Task 5.5 - Add v1.1.1 entry to `CHANGELOG.md`

Add a new section at the top of `CHANGELOG.md` following the same format as existing entries:

```markdown
## [1.1.1] - 2026-04-08

### Breaking changes

- **Default PORT changed from 8191 to 62001** to avoid collision with FlareSolverr.
  Users with `-p 8191:8191` in their docker-compose must update to `-p 62001:62001`
  or set `-e PORT=8191` to keep the old port.

### Fixed

- **#15 Match of the Day daily title**: BBC composite-format subtitles like
  `"2025/26: 22/03/2026"` no longer produce malformed triple-dated filenames.
  Sonarr's Daily-series parser now accepts Match of the Day releases.
- **#16 DOWNLOAD_DIR variable not surfaced in UI**: the env-derived value is now
  consistently returned by `/api/config`, directory listing endpoints, and the
  SABnzbd compat handler. Files already downloaded to the correct location;
  only the UI display was wrong.
- **#18 Doctor Who duplicate-name disambiguation**: Sonarr searches for shows
  with year-suffixed BBC brand titles (classic Doctor Who, 2005-2022 era, etc.)
  now route to the correct brand. Adds year-range-aware filtering via new
  `bareName`, `extractYearRange`, `nameMatchesWithYear`, and `disambiguateByYear`
  helpers.
- **#19 Default PORT collides with FlareSolverr**: see Breaking changes above.

### Closed as out of scope

- **#14 STV Player support**: iplayer-arr is intentionally a BBC-iPlayer-only
  tool. See the issue reply for the full reasoning.

### Project governance

- Added `DISCLAIMER.md` and `SECURITY.md`
- Added GitHub Issue Forms (bug report + feature request) with all fields optional
- Enabled GitHub Private Vulnerability Reporting
- Neutral rewrite of `docs/bbc-streaming-internals.md`

### Tests

- Approximately 35 new unit and integration tests across `internal/newznab/`,
  `internal/bbc/`, `internal/api/`, `internal/sabnzbd/`, `internal/store/`,
  and `cmd/iplayer-arr/`
```

## Task 5.6 - Commit (Phase 5 only)

**Branch shape**: six commits on the feature branch, one per phase. This matches the v1.1.0 precedent (which had one commit per phase on the feature branch, squash-merged to main). Each phase's verification gate passes before its commit is made.

Expected feature-branch history:

```
Phase 5 commit: v1.1.1 changelog + final verification
Phase 4 commit: fix(#18) year-range disambiguation for duplicate BBC brand names
Phase 3 commit: fix(#15) composite-date subtitles produce clean daily titles
Phase 2 commit: fix(#16) DOWNLOAD_DIR env var surfaced in /api/config
Phase 1 commit: fix(#19) default PORT from 8191 to 62001
Phase 0 commit: legal hardening, disclaimer, security, issue forms
```

Phase 5's own commit contains:
- The v1.1.1 CHANGELOG.md entry
- Any last-minute gofmt/vet/test fixes discovered by the global gate
- Nothing else

The PR is then **squash-merged** to `main`, collapsing the six per-phase commits into a single `v1.1.1` commit on main. The squash-merge commit message is the one below (GitHub uses the PR title + description as the squash body):

**Squash commit message** (used when the PR is merged):
```
v1.1.1: multi-issue bug fixes + legal hardening

- fix(#15): Match of the Day daily title no longer contains triple dates
- fix(#16): DOWNLOAD_DIR env var surfaced correctly in config/directory/sabnzbd
- fix(#18): year-range disambiguation for shows with duplicate BBC brand names
- fix(#19): default PORT changed from 8191 to 62001 to avoid FlareSolverr collision
- docs: add DISCLAIMER.md, SECURITY.md, README legal section, neutral rewrite
        of bbc-streaming-internals.md, backfill v1.1.0 CHANGELOG entry
- .github: structured issue forms (bug + feature) with all fields optional,
           contact links to private vulnerability reporting and wiki

Breaking change: default PORT changed from 8191 to 62001. Users with
-p 8191:8191 in docker-compose must update to -p 62001:62001 or set
-e PORT=8191 to keep the old port. See CHANGELOG.md for full migration
guide.

Closes #15, closes #16, closes #18, closes #19.
Closes #14 as out of scope (STV Player support - iplayer-arr is
intentionally BBC-iPlayer-only; see the issue reply for full reasoning).
```

No `Co-Authored-By` lines, no AI references. ASCII hyphens only. The `Closes #N` lines auto-close the referenced issues when the PR is merged.

## Phase 5 verification gate (global)

All of:
- `go build ./...` clean
- `go test ./...` all passing, approximately 212 tests (177 from v1.1.0 + approximately 35 new, including the warm-cache regression test added via Task 4.3d)
- `go vet ./...` no warnings
- `gofmt -l .` empty
- The Task 5.4 grep gate (full command in that section) returns zero results
- CHANGELOG.md has entries for both v1.1.0 and v1.1.1

## Phase 5 acceptance

All individual phase acceptance criteria met + the global verification gate passes + the commit is ready to push to the feature branch and open a PR.

---

# Test plan

## Test totals by phase

| Phase | New tests | Breakdown | Target files |
|---|---|---|---|
| 0 | 0 | markdown and YAML only, no Go tests | |
| 1 | 2 | `TestResolvePort_DefaultWhenUnset` + `TestResolvePort_EnvOverride` | `cmd/iplayer-arr/main_test.go` |
| 2 | 7 | 3 `ResolveDownloadDir` + 2 `handleGetConfig` + 1 directory listing + 1 sabnzbd | `internal/api/resolve_test.go` (new), `internal/api/config_test.go`, `internal/api/directory_test.go`, `internal/sabnzbd/handler_test.go` |
| 3 | 6 | 1 positive `SportsDateSubtitle` + 3 false-positive prevention + 2 `parseSubtitleNumbers` guards | `internal/newznab/titles_test.go`, `internal/bbc/ibl_test.go` |
| 4 | 20 | 5 `bareName` + 4 `extractYearRange` + 4 `nameMatchesWithYear` + 4 `disambiguateByYear` + 2 handler integration (classic + modern) + 1 warm-cache regression (Task 4.3d) | `internal/newznab/disambiguate_test.go` (new), `internal/newznab/handler_test.go` |
| **Total** | **35** | | |

Suite size: currently 177 tests (after v1.1.0). Target: **~212 tests** after v1.1.1 (177 + 35).

These counts are estimates and may drift by a few tests during implementation - the exact number will be recorded in the Phase 5 commit message.

## Test categories covered

- **Unit tests**: all four disambiguation helpers in Phase 4, regex guards in Phase 3, config resolver in Phase 2
- **Integration tests**: Phase 4 handler-level tests mocking Skyhook and IBL, Phase 2 config API tests with httptest
- **Regression tests**: Phase 3 false-positive prevention (series/episode titles that should NOT be stripped)
- **Live API tests**: none. All external API calls (Skyhook, BBC IBL, mediaselector) are mocked via `httptest.NewServer`.

## Test running policy

- **Local only**. No tests run on `192.168.1.57` (production).
- **No real BBC traffic**. All BBC API interactions are mocked in tests.
- **Test data included in the repo**. No live API responses cached or fetched at test time.

---

# Release notes draft (v1.1.1)

```markdown
## Breaking changes

**Default PORT changed from 8191 to 62001** to avoid collision with FlareSolverr
(which also defaults to 8191). If your docker-compose.yml or docker run command
uses `-p 8191:8191`, you need one of:

- Update the port mapping to `-p 62001:62001` (recommended)
- Set `-e PORT=8191` to keep the old port explicitly

No other manual action is required.

## What's new

### Bug fixes

- **#15 Match of the Day**: daily-format titles are now clean. Previously, BBC's
  composite subtitle `"2025/26: 22/03/2026"` produced malformed filenames with
  the air date repeated three times, which Sonarr's Daily-series parser couldn't
  match. Now the title is clean `Match.of.the.Day.2026.03.22.1080p.WEB-DL.AAC.H264-iParr`.

- **#16 DOWNLOAD_DIR env var**: the UI Config page and directory listing
  endpoints now correctly reflect the env-derived download directory. Previously
  they showed the hardcoded default even when `DOWNLOAD_DIR` was set. (Files were
  always downloading to the correct location; this was a display bug only.)

- **#18 Duplicate BBC brand names**: shows where BBC has multiple brands with
  the same name (classic Doctor Who, 2005-2022 Doctor Who, Casualty reboots, etc.)
  now route Sonarr searches to the correct era. The filter now uses year-range
  disambiguation via a new set of helper functions. **Known limitation**: if
  BBC's own metadata catalogue mislabels an episode (e.g. a modern Doctor Who
  episode catalogued under the 1963-1996 brand PID), iplayer-arr cannot detect
  the inconsistency and will emit the release with the mislabelled brand name.
  This is a BBC data quality issue, not an iplayer-arr bug. An investigation
  into the Go pipeline confirmed no state-mixing bug exists - the mismatch
  originates in BBC's API responses.

- **#19 Port collision**: see Breaking changes above.

### Project governance

- Added `DISCLAIMER.md` with TV Licence requirement, BBC trademark disclaimer,
  and personal-use-only statement
- Added `SECURITY.md` pointing at GitHub's Private Vulnerability Reporting
- Added structured GitHub Issue Forms (bug report + feature request) with all
  fields optional to reduce reporting friction
- Backfilled the v1.1.0 CHANGELOG entry that was missing

## Closed as out of scope

- **#14 STV Player support**: iplayer-arr is intentionally a BBC-iPlayer-only
  tool. Supporting additional broadcasters would require a Provider interface
  refactor and expand the project's scope beyond its current focus. See the
  issue reply for the full reasoning.

## Upgrade notes

Patch release. Other than the Breaking changes section above, no manual action
is required. Existing BoltDB stores, persisted settings, and download queues
carry over unchanged.

## Configuration

No new configuration options introduced in v1.1.1. The v1.1.0 options
(`IPLAYER_PROBE_CONCURRENCY`, `IPLAYER_PROBE_TIMEOUT_SEC`) are unchanged.

## Full changelog

See PR #<PR_NUMBER_AT_RELEASE_TIME> for the full diff. The design spec is at
`docs/superpowers/specs/2026-04-08-iplayer-arr-v1.1.1-design.md`.
```

> **Note for the implementor publishing the release**: the `<PR_NUMBER_AT_RELEASE_TIME>` placeholder above must be replaced with the actual PR number assigned by GitHub at the time the PR is opened. The release notes should not be published with the placeholder text intact. Run `gh pr view --json number` after creating the PR to get the number, or just open the PR's web page and read the URL.

---

# Self-review checklist

This section mirrors the v1.1.0 spec's self-review checklist. It gets ticked off as the writing-plans phase turns this spec into a task-level plan, and again before the final commit.

- [ ] Every phase's file list matches what the fix actually needs (verified against code via grep/jcodemunch)
- [ ] Function signatures and line numbers are current as of commit `1803ffd`
- [ ] Every Task has a verbatim code block (added in the writing-plans phase, not the spec)
- [ ] The breaking change is called out in (1) the spec, (2) the commit message, (3) the CHANGELOG, and (4) the release notes
- [ ] The v1.1.0 CHANGELOG backfill entry is present before v1.1.1 ships (the CHANGELOG file currently ends at v1.0.2, this spec adds the missing v1.1.0 entry in Phase 0)
- [ ] All fields in the GitHub Issue Forms are marked `validations.required: false`
- [ ] `docs/bbc-streaming-internals.md` matches the verbatim replacement content from Task 0.4 (byte-level comparison, no keyword grep)
- [ ] The Phase 4 state-mixing investigation finding is documented in Task 4.6 before the commit
- [ ] The Task 5.4 grep gate returns zero results before the Phase 5 commit. The full runnable command is in Task 5.4 - it includes `--include` filters for `*.go`, `*.md`, `Dockerfile`, `*.yml`, `*.yaml` and a trailing `.` path argument; it excludes `CHANGELOG.md` and the `superpowers` directory (which holds the spec and writing-plans output) because those legitimately contain migration references. Do not paraphrase the command - copy it from Task 5.4 verbatim.
- [ ] `go test ./...`, `go vet ./...`, and `gofmt -l .` all pass before the Phase 5 commit
- [ ] ASCII hyphens only in every committed file (no em dashes, no en dashes except where they appear in BBC source data like `Doctor Who (1963\u20131996)` - those must be preserved exactly)
- [ ] No `Co-Authored-By` lines in the commit message
- [ ] No AI references in **user-facing release artifacts**: commit messages, the v1.1.1 CHANGELOG entry, the GitHub release notes body, README, DISCLAIMER.md, SECURITY.md, issue templates, code comments, and any container labels. **This rule does NOT apply to internal design docs** like this spec file or the writing-plans output - those legitimately record review history including which review tools were used (e.g. "Codex review round 3 folded in" entries in the document history below). The boundary is "would a user pulling the v1.1.1 image see this string?" - if no, the rule doesn't apply.

---

# Known deviations from spec

This section will be populated during the implementation phase with any deviations the implementor needed to make from this design. Typical reasons for deviation:
- Line numbers shifted slightly between this spec and the implementation (expected)
- A fix turned out to need a slightly different shape than described
- A test case was found to need extending
- The Phase 4 state-mixing investigation found a real bug that changed the Phase 4 structure

(Empty at spec-write time. Implementor fills this in before the final commit.)

---

# Document history

- 2026-04-08 (initial): Brainstormed against live BBC + Skyhook APIs, with verified file:line citations against commit `1803ffd`. Self-reviewed once before Codex review (see the conversation log in the sprint discussion for the self-review findings and how they were folded in).
- 2026-04-08 (Phase 4 investigation folded in): Code-explorer agent completed the state-mixing investigation. Hypothesis B confirmed - no Go-level bug. Residual BBC-data-quirk risk disclosed in Phase 4 findings section, the Phase 4 acceptance criteria, and the release notes. Task 4.6 reduced to documentation-only.
- 2026-04-08 (Codex review round 1 folded in): Five findings from a Codex second-pass review were addressed. (1) The `grep -rn "8191"` gate was narrowed to exclude `CHANGELOG.md` and `docs/superpowers/` so it no longer contradicts the legitimate migration references in the changelog and release notes. (2) Task 5.6 was rewritten to match the release-shape section: six commits on the feature branch (one per phase), squash-merged to `main`. (3) Task 4.2 was expanded to add a package-level `skyhookBaseURL` injection seam so Phase 4 integration tests can point at `httptest.NewServer` without HTTP transport hacks. (4) References to `/api/directory/*` were corrected to the actual router paths `/api/downloads/directory` and `/api/downloads/directory/{folder}`. (5) The Phase 3 parseSubtitleNumbers root-cause writeup was rewritten - the bug is only in the composite-date form, not the bare-date form (bare dates already return `(0, 0)` because the current parser splits on `": "` first). Task 3.2 and Task 3.4 were updated accordingly.
- 2026-04-08 (Codex review round 2 folded in): Four more findings addressed. (1) **HIGH**: the warm-cache hole in `handleTVSearch`. The existing v1.0.2 cache stores `SeriesMapping.ShowName` but no year, so subsequent tvdbid searches reused the cached name with `filterYear == 0` and skipped year disambiguation entirely. Task 4.3 was expanded to Task 4.3a-4.3d: (a) add `Year int` to `store.SeriesMapping` with backward-compatible JSON deserialisation, (b) update the warm-cache path to use cached year when `Year > 0` and fall through to Skyhook for gradual backfill when `Year == 0`, (c) thread `filterYear` through to `writeResultsRSS` via a new parameter, (d) add a regression test `TestSearch_DoctorWhoClassicTVDB_WarmCacheRetainsYear`. (2) **MEDIUM**: explicit "scope boundary" paragraph added to Phase 4 explaining that episode-type IBL results with episode titles (e.g. `"The Unquiet Dead"`) are rejected by the existing name filter and Phase 4 preserves that behaviour - not regressed but also not addressed. A proper fix via brand identity / tleo_id tracking is noted as out of scope for v1.1.1. (3) **MEDIUM**: verbatim code snippets updated to match current conventions - `GetConfig` call corrected to handle `(string, error)` return, Phase 2 test snippets switched from `testStore(t)` to the real `testAPI(t)` helper at `internal/api/handler_test.go:17`, Phase 3 test snippets corrected to use `"1080p"` string literal and fully-qualified `store.TierDate`. (4) **LOW**: stale `~203-205` test count reference updated to the canonical `~211` (~210 + warm-cache regression test). Total new tests bumped from 33 to 34.
- 2026-04-08 (Codex review round 3 folded in): Four more findings addressed, one of which was rejected as factually incorrect. (1) **HIGH**: the spec itself reproduced loaded framing language it said to remove from `docs/bbc-streaming-internals.md`, undercutting Phase 0's purpose since this spec is committed to the public repo. Task 0.4 was rewritten to provide the verbatim **replacement** content for `bbc-streaming-internals.md` rather than describing what to strip. The "Rationale" section and all verification checklists were rewritten to reference Task 0.4's replacement content by byte-level comparison rather than reproducing the old phrases as a strip-list. (2) **MEDIUM**: Task 1.4's original port default test only exercised `envOr` itself and would have passed even if `main()` reverted to the old port literal. Refactored to extract `const defaultPort = "62001"` and a `resolvePort()` helper in `main.go`, with two tests (default-when-unset + env-override) that exercise the real code path. Phase 1 test count bumped from 1 to 2. (3) **MEDIUM**: Phase 0's verification gate mixed branch-local checks (file existence, content) with remote-state checks (`gh label list`, PVR enabled, advisory URL live). Split into Phase 0a (pre-commit, branch-local, blocks the commit if failing) and Phase 0b (post-merge, remote state, informational only, can be remediated after the PR lands). (4) **LOW**: `gh label create` was not idempotent. Changed to `gh label create --force` which creates or updates as needed, added explicit rationale note. (5) **REJECTED**: Codex claimed `internal/bbc/fhdprobe.go` does not exist. It does. Verified via `ls internal/bbc/` and `grep -n "^func" internal/bbc/fhdprobe.go` which shows `func (c *Client) ProbeHiddenFHD` at line 41 and `pickHighestBandwidthVariant` at line 87. Both `prober.go` (quality prober struct + local interfaces) and `fhdprobe.go` (Client.ProbeHiddenFHD implementation) exist. No spec change required; added an explicit note in Task 0.4's scope section clarifying which file contains what. Total new tests bumped from 34 to 35.
- 2026-04-08 (Codex review round 4 folded in): Four more findings addressed, all valid. (1) **MEDIUM**: the 8191 grep gate's nested-path exclude was a silent no-op because GNU grep's `--exclude-dir` matches directory basenames, not paths. Verified by running the exact command at HEAD - still returned spec hits. Fixed by switching to a basename-only exclude; verified the fix works by running it and confirming only the 6 legitimate code/docs matches appear. Updated in all 4 spec locations. (2) **MEDIUM**: Task 4.3d's warm-cache regression test used `testAPI(t)` which exists in `internal/api/handler_test.go:17` but NOT in the `newznab` package. The newznab package has its own helper set. Task 4.3d rewritten to propose a new helper `newHandlerWithBBCAndStore` that mirrors `newHandlerWithBBC` but wires a real BoltDB store, plus a full test snippet that uses the correct helpers. (3) **LOW**: the Phase 1 verification gate still referenced the old test name after Task 1.4 was rewritten in round 3. Checklist updated. (4) **LOW**: Phase 1 acceptance criterion contradicted the gate exclusions. Rewritten to match. Test count unchanged at 35.
- 2026-04-08 (Codex review round 5 folded in): Four more findings addressed, all valid. (1) **MEDIUM**: the round-4 warm-cache test fixture used `type: "programme"` IBL results, which trigger `IBL.Search` to call `ListEpisodes(brandPID)` against `/programmes/{pid}/episodes` - an endpoint the existing `fakeBBCSearchServer` does not mock. The test would have failed to produce any RSS items at all. Fixed by switching the synthetic payload to `type: "episode"` results that bypass brand expansion entirely. (2) **LOW**: the round-3 history entry was self-contradictory - rewritten without quoting the phrases. (3) **LOW**: the release notes draft contained a `See PR #XX` placeholder that would ship broken if copied mechanically. Replaced and a maintainer note added. (4) **LOW**: the Phase 5 verification gate and self-review checklist used abbreviated forms of the grep command that would behave differently from the actual gate. Rewritten to delegate to "the Task 5.4 grep gate" by reference. Test count unchanged at 35.
- 2026-04-08 (Codex review round 6 folded in): Three more findings addressed, all valid. (1) **MEDIUM**: the round-5 warm-cache test fixture used subtitles like `"Series 1 Episode 2"` (no colon-space separator). Verified against `parseSubtitleNumbers` at `internal/bbc/ibl.go:303` and `reEpisodeNum` at `ibl.go:46` - the parser only attempts episode-number extraction *after* splitting on `": "`, so a colon-less subtitle leaves `EpisodeNum=0`. Fixed by switching to `"Series 1: Episode 2"` (the same form v1.0.2 fixed for Little Britain) and added an inline note explaining the parser dependency. (2) **MEDIUM**: the round-3 Phase 0a/0b split was incomplete - Phase 0 acceptance still required the post-merge state to be true even though Phase 0b was described as informational. Fixed by splitting Phase 0 acceptance into Phase 0a-acceptance (branch-local, gates Phase 5) and Phase 0b post-merge state (informational). (3) **LOW**: the self-review checklist's "No AI references" rule was too broad. Narrowed to "user-facing release artifacts" with an explicit allow list and a carve-out for internal design docs. Test count unchanged at 35.
- 2026-04-08 (Codex review round 7 folded in): Two findings addressed; **Codex confirmed the warm-cache test fixture is now correct** ("the type:'episode' switch plus the 'Series 1: Episode 2' subtitle fix now make that test exercise the intended warm-cache + year-disambiguation path"). (1) **MEDIUM**: the verification gates summary table near the top of the spec still listed pre-split Phase 0 gate language as a "local gate", reintroducing the remote-state-as-local contradiction the round-6 rewrite resolved. Fixed. (2) **LOW**: the round-5 explanatory note for the warm-cache test misstated `fakeBBCSearchServer`'s actual behaviour. The server is path-agnostic and returns the same JSON for every request URL, not 404. Note rewritten to describe the actual failure mode. Test count unchanged at 35.
- 2026-04-08 (Codex review round 8 folded in): One finding addressed. **Codex confirmed the Phase 0 acceptance split was consistent across the gate table, the Phase 0 section, and Phase 5 acceptance** - though round 9 later found one stale leak in the Phase 0 Files section that the round 8 review had not flagged. (1) **MEDIUM**: the warm-cache test as written did not actually prove the warm-cache path was taken. The test pre-populated `SeriesMapping{Year: 1963}` and asserted disambiguation routed to the classic brand - but an implementation that ignored the cache and re-fetched from Skyhook would also pass, because Skyhook returns the same `("Doctor Who", 1963)` for TVDB 76107. The test was redundant with the existing `TestSearch_DoctorWhoClassicTVDB_OnlyMatchesClassicBrand` and did not guard the warm-cache invariant. Fixed by introducing a fail-fast `httptest.NewServer` for Skyhook (using the `skyhookBaseURL` injection seam from Task 4.2), which records every hit and `t.Errorf`s if it's called at all. The test now asserts both (a) disambiguation correctness via the RSS body, AND (b) the cache-was-used invariant via a final `if hits != 0` check. If a future change to `handleTVSearch` skips the warm-cache branch and falls through to Skyhook, the body assertions would still pass (Skyhook produces the same data) but the hit-count assertion fails immediately. The test now has two independent failure modes corresponding to the two ways the warm-cache invariant can be broken. Added required `sync/atomic` import (`net/http` and `net/http/httptest` were already in `internal/newznab/handler_test.go` from the existing test helpers). Test count unchanged at 35 (the change is to an existing test, not a new test). This is the design observation Codex is uniquely good at - the kind of test-redundancy subtlety that's easy to miss when writing tests against your own design.
- 2026-04-08 (Codex review round 9 folded in): One finding addressed. **Codex confirmed the warm-cache test now tests both the output and the "no Skyhook hit" invariant** - the second consecutive round confirming an iterated-on element is correct. (1) **MEDIUM**: the Phase 0 Files section still had an inline note saying repo settings were "part of Phase 0 acceptance", which contradicted the round 6-8 0a/0b acceptance split that correctly placed those items in Phase 0b post-merge. The leak was in a different paragraph than the ones the round 8 review checked. Fixed by rewriting the Files section note to explicitly delegate to "the Phase 0 acceptance section below for the 0a/0b split" and call out that repo-settings items are Phase 0b (post-merge, not blocking Phase 5). The round 8 history note was also softened to acknowledge that the split had a stale leak round 8 hadn't caught. Test count unchanged at 35.
- 2026-04-08 (Codex review round 10 folded in - **convergence pass**): One LOW finding addressed. **Codex explicitly confirmed: "No new substantive findings on this pass. The Phase 0 split and the warm-cache regression test now both look structurally correct."** This is the convergence signal - Codex's wording shifted from "I don't see another blocker in X" (rounds 7-9, finding-specific) to "No new substantive findings" (round 10, full-pass coverage). (1) **LOW**: the round 8 import note for the warm-cache test overstated which imports were new. The note said "added required `net/http`, `net/http/httptest`, and `sync/atomic` imports" but verification against `internal/newznab/handler_test.go:6-7` shows `net/http` and `net/http/httptest` are already imported by the existing test helpers - only `sync/atomic` is genuinely new. Fixed in both the Task 4.3d note and the round 8 history entry. Test count unchanged at 35. **This is the spec's final review-driven amendment.** No further Codex passes are planned before the spec hands off to `superpowers:writing-plans`.
