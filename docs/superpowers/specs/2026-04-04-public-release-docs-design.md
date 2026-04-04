# iplayer-arr Public Release Documentation

**Date:** 2026-04-04
**Status:** Approved
**Scope:** Documentation, repo hygiene, CI, GHCR publishing, and clean git import for public GitHub release

## Overview

Prepare iplayer-arr for its first public release on GitHub. The app is feature-complete and the codebase is clean of sensitive content. This spec covers the documentation, licence, CI, image publishing, screenshots, and cleanup needed before pushing.

The public GitHub repo will receive a **squashed single commit** (not a push of existing history) to avoid exposing internal dev plans in git history.

## 1. README.md

Concise README (~150 lines) with these sections in order:

### 1.1 Hero
- Project name and one-line description: "BBC iPlayer download manager with a web UI, Sonarr integration, and built-in VPN support."
- Hero screenshot of the Dashboard page showing the status bar and active downloads area.

### 1.2 Features
Bullet list:
- BBC iPlayer search and browse (via BBC IBL API)
- Automatic HLS stream download with quality selection (1080p/720p/540p/396p)
- Download queue with configurable worker pool
- Newznab-compatible indexer (works with Sonarr)
- SABnzbd-compatible download API
- Real-time dashboard with SSE live progress
- Built-in WireGuard VPN via hotio base image (off by default)
- Setup wizard for first-run configuration
- System health monitoring (disk usage, FFmpeg status)

### 1.3 Quick Start
Minimal `docker run` example -- no VPN, just the essentials:
```
docker run -d \
  --name iplayer-arr \
  -p 8191:8191 \
  -v iplayer-arr-config:/config \
  -v /path/to/downloads:/downloads \
  -e TZ=Europe/London \
  ghcr.io/will-luck/iplayer-arr:latest
```
Note that iPlayer requires a UK IP -- use the VPN section below or run behind an existing UK VPN/proxy.

### 1.4 VPN Configuration
Dedicated section explaining the hotio integration:

- Built on [hotio/base:alpinevpn](https://hotio.dev/containers/base/) -- includes WireGuard, nftables kill switch, and s6-overlay service management.
- VPN is **off by default**. Enable with `VPN_ENABLED=true`.
- Requires `--cap-add=NET_ADMIN` and `--sysctl net.ipv4.conf.all.src_valid_mark=1`.

Two sub-sections:

**PIA provider example:**
```
docker run -d \
  --name iplayer-arr \
  --cap-add NET_ADMIN \
  --sysctl net.ipv4.conf.all.src_valid_mark=1 \
  -p 8191:8191 \
  -v iplayer-arr-config:/config \
  -v /path/to/downloads:/downloads \
  -e TZ=Europe/London \
  -e VPN_ENABLED=true \
  -e VPN_PROVIDER=pia \
  -e VPN_PIA_USER=your_pia_username \
  -e VPN_PIA_PASS=your_pia_password \
  -e VPN_PIA_PREFERRED_REGION=uk \
  -e VPN_LAN_NETWORK=192.168.1.0/24 \
  -e WEBUI_PORTS=8191/tcp \
  ghcr.io/will-luck/iplayer-arr:latest
```

**Generic WireGuard:**
- Mount your own `wg0.conf` into `/config/wireguard/wg0.conf`
- Set `VPN_ENABLED=true` and `VPN_PROVIDER=generic`

**VPN environment variables table:**

| Variable | Default | Description |
|----------|---------|-------------|
| `VPN_ENABLED` | `false` | Enable WireGuard VPN |
| `VPN_PROVIDER` | `generic` | VPN provider: `generic`, `pia`, or `proton` |
| `VPN_LAN_NETWORK` | -- | LAN CIDR for direct access to the web UI (e.g. `192.168.1.0/24`) |
| `VPN_PIA_USER` | -- | PIA username (when provider is `pia`) |
| `VPN_PIA_PASS` | -- | PIA password (when provider is `pia`) |
| `VPN_PIA_PREFERRED_REGION` | -- | PIA region (e.g. `uk`) |
| `VPN_HEALTHCHECK_ENABLED` | `false` | Bring down container if VPN connectivity fails |
| `VPN_AUTO_PORT_FORWARD` | `false` | Auto-retrieve forwarded port (PIA/Proton) |
| `WEBUI_PORTS` | -- | Ports to allow through the kill switch (e.g. `8191/tcp`) |

Link to [hotio VPN documentation](https://hotio.dev/containers/base/) for the full variable list.

### 1.5 Environment Variables
**Application variables** (read by iplayer-arr):

| Variable | Default | Description |
|----------|---------|-------------|
| `CONFIG_DIR` | `/config` | BoltDB and config storage |
| `DOWNLOAD_DIR` | `/downloads` | Download output directory |
| `PORT` | `8191` | HTTP server listen port |

Note: API key is auto-generated on first run and visible in the Config page.

**Container variables** (handled by the hotio base image):

| Variable | Default | Description |
|----------|---------|-------------|
| `PUID` | `1000` | User ID for file permissions |
| `PGID` | `1000` | Group ID for file permissions |
| `TZ` | `Europe/London` | Container timezone |
| `UMASK` | `002` | File permission mask |

### 1.6 Sonarr Integration
Brief instructions:
- **Indexer:** Add as Newznab custom indexer. URL: `http://iplayer-arr:8191/newznab/api`. API key from Config page. Categories: 5000 (TV).
- **Download client:** Add as SABnzbd. Host: `iplayer-arr`, port: `8191`, URL base: `/sabnzbd`, category: `sonarr`. API key from Config page.

### 1.7 Development
```
# Frontend (writes to frontend/dist/)
cd frontend && npm ci && npm run build

# Copy frontend assets into Go embed directory
cp -r frontend/dist/* internal/web/dist/

# Backend
go build ./cmd/iplayer-arr/
go test ./...
```

The copy step is needed because the Go binary embeds `internal/web/dist/` (via `//go:embed`), not `frontend/dist/`. The Dockerfile handles this automatically, but local development requires the manual copy.

### 1.8 Licence
GPL-3.0 -- see LICENSE file.

## 2. LICENSE

Full GPL-3.0 licence text. Standard `LICENSE` file in repo root.

Also update `frontend/package.json` licence field from `"ISC"` to `"GPL-3.0-only"` to avoid conflicting licence metadata.

## 3. GitHub Actions CI

Two workflow files in `.github/workflows/`:

### 3.1 ci.yml -- Test and build
- Trigger: push to main, pull requests
- Jobs:
  1. **Frontend:** `npm ci && npm run build` in `frontend/`, upload `dist/` as artifact
  2. **Backend:** Download frontend artifact into `internal/web/dist/`, then `go vet`, `go test -race`, `go build`
- Go version: 1.24
- Node version: 22

### 3.2 release.yml -- GHCR publish
- Trigger: push of version tags (`v*`)
- Steps: checkout, Docker buildx setup, login to ghcr.io, build and push `ghcr.io/will-luck/iplayer-arr:<tag>` + `:latest`
- Pass `--build-arg VERSION=<tag> BUILD_DATE=<timestamp>` so the Dockerfile can inject them via `-ldflags`
- Uses the existing multi-stage Dockerfile (handles frontend + backend + runtime in one build)

### 3.3 Dockerfile version injection (CODE CHANGE -- must be applied during implementation)
The current Dockerfile at line 16 uses a plain `go build` with no version metadata. This must be updated to accept build args and pass them via `-ldflags`:
```dockerfile
ARG VERSION=dev
ARG BUILD_DATE=unknown
RUN CGO_ENABLED=0 GOOS=linux go build \
  -ldflags "-X github.com/Will-Luck/iplayer-arr/internal/api.appVersion=${VERSION} \
            -X github.com/Will-Luck/iplayer-arr/internal/api.buildDate=${BUILD_DATE}" \
  -o /iplayer-arr ./cmd/iplayer-arr/
```
The ldflags target package is `internal/api` where `appVersion` and `buildDate` are declared (`system.go:113-114`). Without build args, the defaults remain `dev`/`unknown` for local builds -- which is correct behaviour.

The `release.yml` workflow passes `--build-arg VERSION=${{ github.ref_name }} --build-arg BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)` so tagged releases show the correct version in the System page and API.

## 4. Screenshots

Capture with Playwright from the running instance at `http://192.168.1.57:62932`:
- **Dashboard** -- status bar visible, ideally with some history
- **Search** -- showing BBC iPlayer search results
- **System** -- system health page with disk and version info

Store in `docs/screenshots/` as PNG files. Reference from README with relative paths.

## 5. Repo Cleanup and Publishing

### 5.1 Clean import to GitHub
The existing git history contains internal dev plans (`docs/superpowers/`) that should not appear in public history. Rather than rewriting history, we create a clean squashed first commit:

1. Complete all documentation work on the existing local branch
2. Delete `docs/superpowers/` from the working tree
3. Create a fresh orphan branch with all files as a single commit
4. Force-push the orphan branch to the GitHub `origin` remote as `main`
5. Tag the commit as `v0.1.0` and push the tag -- this triggers `release.yml` to publish `ghcr.io/will-luck/iplayer-arr:v0.1.0` + `:latest`

This gives a clean public repo with no internal dev history. The full history remains in the local repo and Gitea for reference.

### 5.2 Canonical owner and remote cleanup
Canonical public home: **Will-Luck/iplayer-arr** (`origin` remote).

- Update `go.mod` module path from `github.com/GiteaLN/iplayer-arr` to `github.com/Will-Luck/iplayer-arr`
- Update all internal Go imports to match the new module path
- Remove `github` remote (points to GiteaLN org, no longer canonical)
- Remove `gitea` remote from the public-facing clone (keep it locally if desired)

### 5.3 Files to keep
- `docs/bbc-streaming-internals.md` (public knowledge, useful technical reference)
- `.gitea/workflows/ci.yml` (no harm, useful if someone forks to Gitea)

## Out of Scope

These are deferred until there is community interest:
- CONTRIBUTING.md
- CHANGELOG.md
- GitHub issue/PR templates
- Badges (CI status, licence, etc.) -- can be added to README later
