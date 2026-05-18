# Testing and regression safety

This document covers how iplayer-arr is tested in CI, how the regression-anchor pattern works, and how to extend it when you ship a new feature or fix a new bug.

It is half operator reference (how to use the diag endpoints to validate a deployment) and half contributor reference (how to add new diag endpoints and regression anchors).

## The regression-anchor pattern

Between v1.4.0 (2026-05-11) and v1.5.6 (2026-05-17), iplayer-arr shipped eight releases in six days. Two of those releases were regressions that reached production:

- **v1.5.5 ffmpeg `kB` -> `KiB`.** The download progress regex was anchored on `kB`. When the base image rolled to ffmpeg 8.x, the unit was renamed to `KiB` and the regex stopped matching. `OnProgress` never fired, downloads appeared to hang, and the watchdog culled them.
- **v1.5.5 newznab apikey threading.** Sonarr received a feed whose `<link>` and `<enclosure>` URLs were missing `&apikey=`. Sonarr's grab requests came back 401 and every search failed silently.

Both regressions had a common shape: the unit tests passed, the symptom only appeared when the real binary or the real Sonarr made the real call, and the bug was caught by a human noticing downstream breakage rather than by CI.

The rule going forward is:

> Every shipped regression gets a permanent test before the fix lands. The test must fail on the broken commit and pass on the fix.

`/api/diag/*` endpoints are how we implement that rule. Each diag endpoint synthesises an integration round-trip in-process (using `httptest.NewRecorder` against the production handlers, not against mocks) and returns a structured `verdict: pass|fail` report. They exercise the same code paths production traffic does, which is why they catch the class of bug unit tests miss.

Diag endpoints are not throwaway debug probes. They survive code reshuffling. When the integration surface changes shape, the diag endpoint and its paired regression test get updated alongside the change, in the same commit.

## The diag endpoints

| Endpoint | What it asserts | Class of bug it catches |
|---|---|---|
| `GET /api/diag/sonarr-handshake` | Synthesises a Sonarr `t=tvsearch` against the live newznab handler, parses the returned RSS, asserts every `<guid>`/`<link>`/`<enclosure>` URL carries `&apikey=`, then follows the first enclosure URL as `t=get` and verifies it returns `application/x-nzb`. | v1.5.5-class apikey threading regressions. The `feed_apikey` check is the exact assertion that would have failed v1.5.5 in CI. |
| `GET /api/diag/ffmpeg` | Invokes `ffmpeg -version` and parses the version line. Runs a synthetic 8.x `KiB`-form progress line through `download.ParseProgress` and asserts the regex matches. | v1.5.5-class ffmpeg unit regressions (`kB` -> `KiB`) and any future progress-line format change. Will fail if ffmpeg is missing from the image. |
| `GET /api/diag/bbc` | Drives the IBL search via an injectable probe with a known tvdbid. Asserts the endpoint is reachable, returns more than zero results, and the response carries both brand and episode information. | BBC API shape changes. The fake-probe injection point (`bbcProbe`) means CI does not need real BBC access to assert handler integrity, while operators hitting the endpoint against a deployed instance get a real round-trip check. |
| `GET /api/diag/sab` | Synthesises a SAB request for each compat mode (`version`, `queue`, `history`, `get_cats`, `get_config`, `fullstatus`). For every auth-gated mode it sends both an unkeyed and a keyed request; asserts `version` is the only unauthenticated carve-out, every other mode rejects key-less requests, and every other mode accepts keyed requests. | v1.5.5-class apikey-threading regressions on the download-client side. The `get_config` mode leaking `complete_dir` to unkeyed callers on the LAN was the worst-case version of this bug. |
| `GET /api/diag/auth-paths` | Builds three synthetic requests and pushes each through `authenticate()`: `?apikey=` query parameter, `Authorization: Bearer <key>`, and `X-Api-Key: <key>` header. Asserts all three resolve to the same accept verdict. | Test/prod auth drift. Prior to v1.5.6 the integration tests relied on `X-Api-Key` while production only accepted `?apikey=` and Bearer. Tests passed against a code path production rejected. |

Every endpoint returns the same JSON envelope shape:

```json
{
  "verdict": "pass",
  "checks_failed": [],
  ...endpoint-specific fields...
}
```

`verdict` is `pass` when `checks_failed` is empty. CI asserts on `.verdict == "pass"`; operators reading the report get the per-component detail in `checks_failed` and the per-mode fields.

All five endpoints require authentication. They are not intended for unauthenticated probes from outside the deployment.

## Running the diag suite

Set an API key first (the wizard or `/api/config`). Then any of these will work:

```bash
KEY=<your-api-key>
HOST=http://localhost:62XXX

# Query parameter
curl -fsS "$HOST/api/diag/ffmpeg?apikey=$KEY" | jq .verdict

# Authorization header
curl -fsS -H "Authorization: Bearer $KEY" "$HOST/api/diag/bbc" | jq .verdict

# X-Api-Key header
curl -fsS -H "X-Api-Key: $KEY" "$HOST/api/diag/sab" | jq .verdict
```

Replace `62XXX` with whatever port the container is published on.

For a quick all-green check on a deployment:

```bash
for ep in sonarr-handshake ffmpeg bbc sab auth-paths; do
  v=$(curl -fsS "$HOST/api/diag/$ep?apikey=$KEY" | jq -r .verdict)
  printf '%-20s %s\n' "$ep" "$v"
done
```

Any `fail` row indicates a regression. The endpoint's `checks_failed` array names the specific assertion that failed and is the first thing to read.

## The Tier A pipeline

`.gitea/workflows/ci.yml` runs two jobs on every push and every PR to `main`:

1. `unit` runs `go test ./... -race`. Catches anything the standard test surface covers.
2. `diag-suite` builds the container, brings it up on a random high port with tmpfs volumes (no production state touched), seeds an API key, then curls each `/api/diag/*` endpoint and asserts `.verdict == "pass"`.

Branch protection on `main` blocks merge when either job fails. The intent is that a regression of the v1.5.5 shape is caught at PR time, not at production deploy time.

See `.gitea/workflows/ci.yml` for the workflow definition and `scripts/smoke-test.sh` for the parameterised container probe `diag-suite` runs locally.

## The Tier B pipeline (auto-mirror gate)

Tier B adds two jobs that run on push to `main` (not on PRs):

3. `smoke` runs on the self-hosted `57-smoke` runner (`gitea-runner-smoke` container on `.57`). It builds the production image, brings up a container, and curls every `/api/diag/*` endpoint against the live binary. Logically identical to `diag-suite`, but executed on a separately-labelled runner that can be moved or hardened in the future.
4. `mirror-to-github` runs only after `unit`, `diag-suite`, and `smoke` are all green. It pushes the gitea ref to the GitHub mirror via a PAT stored as the Gitea Actions secret `GH_PAT`. This replaces the manual `git push github main` step and means the GitHub mirror reflects whatever last passed the full CI pipeline — never an unverified push.

The split is deliberate: PRs run only the code-level gates (`unit`, `diag-suite`); `smoke` + `mirror-to-github` only run on main push, after the merge has already happened. If smoke fails on a main push, the gitea ref is updated but the github mirror is not — the fix is to roll forward (push another commit that passes smoke) or revert, never to bypass the gate manually.

### What Tier B uniquely provides

- **Auto-mirror gate.** The `needs: [unit, diag-suite, smoke]` dependency means gitea cannot push to github unless every gate is green. Removes the manual `git push github main` step from the release workflow — which was the original trigger for this framework after the v1.5.5 regression chain.
- **Label-scoped runner.** `57-smoke` is its own runner instance with its own attack surface, ready to be moved off `.57` (e.g. to a test-cluster node) or scoped to genuinely-different probes (real outbound network from a different host) without disturbing Tier A.

### What Tier B does NOT uniquely provide (since the `/api/diag/*` endpoints are real-binary probes)

The diag endpoints in `internal/api/diag_extra.go` invoke real binaries against the running container (e.g. `/api/diag/ffmpeg` calls `exec.Command("ffmpeg", "-version")`). Tier A's `diag-suite` builds the production image and runs those endpoints, so it already catches the class of failure originally framed as Tier B's domain:

- Base image rolls that drop a binary (e.g. ffmpeg, get_iplayer).
- Dockerfile drift that silently breaks an `apk add` line.
- `get_iplayer` version drift, when the diag probe checks the version range.

If a future regression needs genuinely-different probes — e.g. real outbound network from a different host, or DNS-resolution coverage that the runner sibling network can't provide — those would be net-new endpoints (`/api/smoke/*`) only the smoke runner exercises.

### Adversarial verification

Tier B was validated 2026-05-18 by dropping `ffmpeg` from the Dockerfile on a throw-away branch. Result: `unit` green, `diag-suite` RED at `/api/diag/ffmpeg` (correctly: `verdict: fail`, `error: exec: "ffmpeg": executable file not found in $PATH`), `smoke` skipped (didn't run because diag-suite was in its `needs`), `mirror-to-github` skipped. The GitHub mirror stayed at the previous good SHA. End-to-end protection works as designed; the failure surfaced one layer earlier than originally framed because the diag endpoints already probe real binaries.

## Adding a new diag endpoint

When a new external integration lands (a new download client, a new metadata source, a new auth mechanism), add a diag endpoint for it alongside the feature, in the same commit. The existing endpoints in `internal/api/diag_extra.go` are the template:

1. **Add the handler.** Put it in `internal/api/diag_extra.go` (or a sibling `diag_*.go` if `diag_extra.go` is getting long). Follow the established shape:
   - Auth-gate first (`if !h.authenticate(r) { ...401... }`).
   - Build a `Diag<Thing>Report` struct with a `Verdict string`, `ChecksFailed []string`, and whatever per-component fields the integration needs.
   - Use `httptest.NewRecorder` to synthesise the round-trip against the live handler in-process. Do not call the upstream over real HTTP from inside the handler; that breaks CI hermeticity.
   - For integrations that genuinely need a network call (BBC IBL), use an injectable probe field on the `Handler` (see `bbcProbe` in `diag_extra.go`). Production wires the real probe; tests inject a fake.
   - Set `verdict = "pass"` when `checks_failed` is empty, `"fail"` otherwise.
   - End with `writeJSON(w, http.StatusOK, report)`. Diag endpoints always return 200; the JSON body carries the verdict.

2. **Mount the route.** Add a case in `internal/api/handler.go` under the existing `/api/diag/*` block:
   ```go
   case path == "/api/diag/<name>" && r.Method == "GET":
       h.handleDiag<Name>(w, r)
   ```

3. **Add the paired regression test.** In `internal/api/diag_extra_test.go`, add `TestDiag<Name>_DetectsRegression`. Model it on the existing tests: stand up a `Handler` whose state reproduces the broken-shape of a known regression, hit the endpoint, assert `verdict == "fail"` and that the specific `checks_failed` entry naming the regression is present. This is the test that proves the diag endpoint actually catches what it claims to catch.

4. **Wire it into CI.** Add the URL suffix to the curl loop in `.gitea/workflows/ci.yml` so `diag-suite` exercises it on every push.

5. **Update this doc and `CHANGELOG.md`.** A new diag endpoint is a public-facing capability; record it.

## Adding a regression anchor for a new bug

When a regression reaches production and you roll it back:

1. Before writing the fix, write the test (or the diag endpoint) that captures the bug class. The test must fail on the broken commit and pass on the fix.
2. Land the test and the fix together. Not the test first in one PR and the fix in another. They are one change.
3. If the bug class is one that unit tests would never have caught (a unit-format change, an apikey threading shape, an upstream API contract drift), the test belongs as a diag endpoint, not as a `_test.go` assertion against an internal function.

This is the discipline that prevents the same class of bug from happening twice. Both v1.5.5 regressions were preventable: there were no anchors in CI for "does the feed carry the apikey?" or "does the progress regex match the current ffmpeg's output?" so the broken behaviour shipped. The four diag endpoints added in the Tier A landing exist to make that specific failure mode impossible going forward.

## Further reading

The framework this fits into is described in the project's testing/regression-safety planning document. Tier A (the diag endpoints, the `unit + diag-suite` workflow, branch protection on `main`) is what this doc describes. Tier B (isolated `.57` smoke as a pre-mirror gate before the GitHub mirror push) and Tier C (a test-cluster acceptance run on tagged releases) are deferred and will get their own sections here once they land.
