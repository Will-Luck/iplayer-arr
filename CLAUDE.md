# iplayer-arr

BBC iPlayer DVR that presents as a Newznab indexer + SABnzbd download client for Sonarr integration. Downloads programmes via BBC media APIs with ffmpeg.

## Build & Run

```bash
# Prerequisites: Go 1.24+, Node 22+, ffmpeg (runtime)

# Build Go binary (frontend must be built first for embed)
cd frontend && npm ci && npm run build    # -> frontend/dist/
go build -o bin/iplayer-arr ./cmd/iplayer-arr/

# Run locally
./bin/iplayer-arr

# Tests
go test -v -race ./...

# Docker image (multi-stage: Node frontend + Go binary + hotio/base:alpinevpn with ffmpeg)
docker build -t iplayer-arr:dev .
```

## Architecture

```
cmd/iplayer-arr/
  main.go                     Entry point, config loading, server startup

internal/
  api/                        HTTP server, REST API, SSE hub, config/status endpoints
  bbc/                        BBC API clients
    client.go                 HTTP client with retry/backoff
    ibl.go                    iPlayer Business Layer (schedule, search, programme metadata)
    mediaselector.go          Media Selector (stream URLs, quality selection)
    playlist.go               Playlist parsing
    prober.go                 Quality probing (SD/HD/FHD detection)
    subtitles.go              Subtitle download + conversion
    useragent.go              Browser UA rotation
  download/                   Download manager
    manager.go                Queue management, concurrent workers
    worker.go                 Per-download lifecycle (probe, fetch, mux, rename)
    ffmpeg.go                 ffmpeg HLS download + subtitle muxing
    cleanup.go                Temp file cleanup
  newznab/                    Newznab-compatible indexer API (for Sonarr)
    handler.go                Caps + search endpoints
    search.go                 Programme-to-NZB mapping
    disambiguate.go           Multi-episode disambiguation
    titles.go                 Title normalisation
  sabnzbd/                    SABnzbd-compatible download API (for Sonarr)
    handler.go                Queue, history, add-NZB endpoints
  store/                      BoltDB persistence (config, downloads, history, overrides)

frontend/                     Solid.js SPA (Vite + TypeScript)
  src/pages/                  Dashboard, Downloads, Config, Logs, Search, Overrides, System
  src/components/             Nav, SetupWizard, Toast, Brand
  src/lib/                    Sonarr setup helper, clipboard utils
```

## Conventions

- **Storage:** BoltDB at `/config/iplayer-arr.db`
- **Frontend:** Solid.js + TypeScript + Vite. Built to `frontend/dist/`, embedded via `//go:embed` in `internal/web/`.
- **SSE:** Real-time download progress via `/api/events`
- **Config:** Environment variables. See `cmd/iplayer-arr/main.go` for flag definitions.
- **Module:** `github.com/Will-Luck/iplayer-arr`
- **CI:** `.github/workflows/ci.yml` (lint + test), `release.yml` (GHCR image + GitHub Release on tag), `.gitea/workflows/ci.yml` (local Gitea runner)
- **Runtime deps:** ffmpeg required for HLS download + subtitle muxing
- **Base image:** `ghcr.io/hotio/base:alpinevpn` (s6-overlay, optional VPN support)
- **Sonarr integration:** Presents as both a Newznab indexer and SABnzbd client to Sonarr

## Release workflow

Gitea is the squash authority. All merges happen via Gitea PRs. Never squash-merge on GitHub — it creates duplicate-SHA drift against Gitea's squash and the next PR will conflict on `CHANGELOG.md`.

1. Open the PR on Gitea, get CI green, merge (squash).
2. Smoke-test the merged `main` in an isolated Docker container on `.57` (high port, tmpfs for state, no shared network). Hit the actual affected endpoint and check the response shape.
3. Fast-forward GitHub: `git push origin gitea/main:main`. No GitHub PR needed.
4. Cut the release tag: move `[Unreleased]` → `[X.Y.Z] - YYYY-MM-DD` in `CHANGELOG.md`, commit on `main`, `git tag -a vX.Y.Z -m "..."`, push both. The `release.yml` workflow then builds the GHCR image, syncs Docker Hub description, and publishes the GitHub Release using the tag annotation as the body.
5. Post-deploy: promote the production container on `.57` to the new tag and run `/lucknet-ops:post-deploy` to cascade `docker-run-commands.md`, `state.md`, `networklayout.md`, `changes.md`.

External GitHub contributor? Pull their branch, push to Gitea, open a Gitea PR, squash there, fast-forward GitHub. Their GitHub PR auto-closes as "merged".
