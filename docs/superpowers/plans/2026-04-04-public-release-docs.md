# iplayer-arr Public Release Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prepare iplayer-arr for public GitHub release with README, licence, CI, GHCR publishing, screenshots, and clean git history.

**Architecture:** All work happens in the existing `/home/lns/iplayer-arr` repo. Tasks 1-6 add/modify files on the current branch. Task 7 captures screenshots via Playwright. Task 8 creates the clean orphan branch and pushes to GitHub. Task ordering matters -- module path change (Task 1) must happen before Dockerfile ldflags (Task 3) since ldflags reference the module path.

**Tech Stack:** Go 1.24, Node 22, GitHub Actions, Docker, Playwright (screenshots)

---

### Task 1: Update Go module path from GiteaLN to Will-Luck

**Files:**
- Modify: `go.mod:1`
- Modify: All `.go` files containing `github.com/GiteaLN/iplayer-arr` (32 import lines across 16 files)

- [ ] **Step 1: Update go.mod module declaration**

In `go.mod`, change line 1:
```
module github.com/GiteaLN/iplayer-arr
```
to:
```
module github.com/Will-Luck/iplayer-arr
```

- [ ] **Step 2: Find-and-replace all Go imports**

Run:
```bash
cd /home/lns/iplayer-arr && find . -name '*.go' -exec sed -i 's|github.com/GiteaLN/iplayer-arr|github.com/Will-Luck/iplayer-arr|g' {} +
```

- [ ] **Step 3: Verify no old imports remain**

Run:
```bash
cd /home/lns/iplayer-arr && grep -r 'GiteaLN/iplayer-arr' --include='*.go' .
```
Expected: no output.

- [ ] **Step 4: Verify the build passes**

Run:
```bash
cd /home/lns/iplayer-arr && go vet ./... && go test ./... -race -count=1
```
Expected: all pass, no errors.

- [ ] **Step 5: Commit**

```bash
cd /home/lns/iplayer-arr && git add go.mod $(find . -name '*.go' -newer go.mod) && git add -u '*.go' && git commit -m "chore: update module path to github.com/Will-Luck/iplayer-arr"
```

---

### Task 2: Fix frontend licence metadata

**Files:**
- Modify: `frontend/package.json:12`

- [ ] **Step 1: Update licence field**

In `frontend/package.json`, change line 12:
```json
  "license": "ISC",
```
to:
```json
  "license": "GPL-3.0-only",
```

- [ ] **Step 2: Commit**

```bash
cd /home/lns/iplayer-arr && git add frontend/package.json && git commit -m "chore: update frontend licence from ISC to GPL-3.0-only"
```

---

### Task 3: Update Dockerfile with version injection via ldflags

**Files:**
- Modify: `Dockerfile:16`

- [ ] **Step 1: Replace the go build line**

In `Dockerfile`, replace line 16:
```dockerfile
RUN CGO_ENABLED=0 GOOS=linux go build -o /iplayer-arr ./cmd/iplayer-arr/
```
with:
```dockerfile
ARG VERSION=dev
ARG BUILD_DATE=unknown
RUN CGO_ENABLED=0 GOOS=linux go build \
  -ldflags "-X github.com/Will-Luck/iplayer-arr/internal/api.appVersion=${VERSION} \
            -X github.com/Will-Luck/iplayer-arr/internal/api.buildDate=${BUILD_DATE}" \
  -o /iplayer-arr ./cmd/iplayer-arr/
```

- [ ] **Step 2: Verify Docker build still works**

Run:
```bash
cd /home/lns/iplayer-arr && docker build -t iplayer-arr:test --build-arg VERSION=test-build --build-arg BUILD_DATE=2026-04-04 . 2>&1 | tail -5
```
Expected: build succeeds.

- [ ] **Step 3: Verify version injection works**

Run:
```bash
docker run --rm -e PORT=9999 iplayer-arr:test /app/iplayer-arr &
sleep 3
curl -s http://localhost:9999/api/system | grep -o '"version":"[^"]*"'
docker stop $(docker ps -q --filter ancestor=iplayer-arr:test) 2>/dev/null
```
Expected: `"version":"test-build"` (not `"version":"dev"`).

- [ ] **Step 4: Clean up test image**

```bash
docker rmi iplayer-arr:test 2>/dev/null
```

- [ ] **Step 5: Commit**

```bash
cd /home/lns/iplayer-arr && git add Dockerfile && git commit -m "build: inject version and build date via ldflags in Dockerfile"
```

---

### Task 4: Add GPL-3.0 LICENSE file

**Files:**
- Create: `LICENSE`

- [ ] **Step 1: Download the GPL-3.0 licence text**

```bash
cd /home/lns/iplayer-arr && curl -sL https://www.gnu.org/licenses/gpl-3.0.txt -o LICENSE
```

- [ ] **Step 2: Verify the file**

```bash
head -5 /home/lns/iplayer-arr/LICENSE
```
Expected: starts with "GNU GENERAL PUBLIC LICENSE" and "Version 3".

- [ ] **Step 3: Commit**

```bash
cd /home/lns/iplayer-arr && git add LICENSE && git commit -m "chore: add GPL-3.0 licence"
```

---

### Task 5: Add GitHub Actions CI workflow

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: Create the workflow file**

```bash
mkdir -p /home/lns/iplayer-arr/.github/workflows
```

Write `.github/workflows/ci.yml`:
```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  frontend:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: '22'
      - run: npm ci
        working-directory: frontend
      - run: npm run build
        working-directory: frontend
      - uses: actions/upload-artifact@v4
        with:
          name: frontend-dist
          path: frontend/dist/

  backend:
    runs-on: ubuntu-latest
    needs: frontend
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.24'
      - uses: actions/download-artifact@v4
        with:
          name: frontend-dist
          path: internal/web/dist/
      - run: go vet ./...
      - run: go test ./... -v -race
      - run: CGO_ENABLED=0 go build ./cmd/iplayer-arr/
```

- [ ] **Step 2: Commit**

```bash
cd /home/lns/iplayer-arr && git add .github/workflows/ci.yml && git commit -m "ci: add GitHub Actions workflow with frontend build"
```

---

### Task 6: Add GitHub Actions release workflow for GHCR

**Files:**
- Create: `.github/workflows/release.yml`

- [ ] **Step 1: Create the release workflow**

Write `.github/workflows/release.yml`:
```yaml
name: Release

on:
  push:
    tags: ['v*']

permissions:
  contents: read
  packages: write

jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: docker/setup-buildx-action@v3

      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - uses: docker/metadata-action@v5
        id: meta
        with:
          images: ghcr.io/${{ github.repository }}
          tags: |
            type=semver,pattern={{version}}
            type=raw,value=latest

      - uses: docker/build-push-action@v6
        with:
          context: .
          push: true
          tags: ${{ steps.meta.outputs.tags }}
          labels: ${{ steps.meta.outputs.labels }}
          build-args: |
            VERSION=${{ github.ref_name }}
            BUILD_DATE=${{ github.event.head_commit.timestamp }}
          cache-from: type=gha
          cache-to: type=gha,mode=max
```

- [ ] **Step 2: Commit**

```bash
cd /home/lns/iplayer-arr && git add .github/workflows/release.yml && git commit -m "ci: add GHCR release workflow triggered by version tags"
```

---

### Task 7: Capture screenshots with Playwright

**Files:**
- Create: `docs/screenshots/dashboard.png`
- Create: `docs/screenshots/search.png`
- Create: `docs/screenshots/system.png`

Prerequisites: iplayer-arr must be running at `http://192.168.1.57:62932`.

- [ ] **Step 1: Create screenshots directory**

```bash
mkdir -p /home/lns/iplayer-arr/docs/screenshots
```

- [ ] **Step 2: Capture Dashboard screenshot**

Use Playwright to navigate to `http://192.168.1.57:62932/` and take a full-page screenshot. Save to `/home/lns/iplayer-arr/docs/screenshots/dashboard.png`. Aim for 1280px width.

- [ ] **Step 3: Capture Search screenshot**

Navigate to `http://192.168.1.57:62932/search`, type a search term (e.g. "bbc"), wait for results to load, and capture. Save to `/home/lns/iplayer-arr/docs/screenshots/search.png`.

- [ ] **Step 4: Capture System screenshot**

Navigate to `http://192.168.1.57:62932/system` and capture. Save to `/home/lns/iplayer-arr/docs/screenshots/system.png`.

- [ ] **Step 5: Commit**

```bash
cd /home/lns/iplayer-arr && git add docs/screenshots/ && git commit -m "docs: add screenshots for README"
```

---

### Task 8: Write README.md

**Files:**
- Create: `README.md`

- [ ] **Step 1: Write the README**

Create `README.md` in the repo root following the spec sections 1.1-1.8 exactly. Use the screenshots from Task 7 as relative paths (`docs/screenshots/dashboard.png` etc.). Key content:

1. **Hero:** Project name, one-liner, hero screenshot (dashboard.png)
2. **Features:** 9-item bullet list from spec section 1.2
3. **Quick Start:** Minimal docker run with `ghcr.io/will-luck/iplayer-arr:latest`, no VPN
4. **VPN Configuration:** Hotio explanation, PIA example docker run, generic WireGuard instructions, VPN env var table, link to hotio docs
5. **Environment Variables:** Split into "Application variables" (CONFIG_DIR, DOWNLOAD_DIR, PORT) and "Container variables" (PUID, PGID, TZ, UMASK) tables. Note about auto-generated API key.
6. **Sonarr Integration:** Newznab indexer at `/newznab/api`, SABnzbd client with URL base `/sabnzbd` and category `sonarr`
7. **Development:** Frontend build (`npm ci && npm run build`), copy step (`cp -r frontend/dist/* internal/web/dist/`), Go build and test
8. **Licence:** GPL-3.0 with link to LICENSE file

- [ ] **Step 2: Commit**

```bash
cd /home/lns/iplayer-arr && git add README.md && git commit -m "docs: add README for public release"
```

---

### Task 9: Clean import to GitHub

**Files:**
- Remove: `docs/superpowers/` (entire directory)
- Modify: git remotes and branches

This task creates the squashed public history and pushes to GitHub. **This is destructive to the GitHub remote** -- it force-pushes an orphan branch.

- [ ] **Step 1: Remove internal docs from working tree**

```bash
cd /home/lns/iplayer-arr && rm -rf docs/superpowers/
git add -A && git commit -m "chore: remove internal dev plans before public release"
```

- [ ] **Step 2: Create orphan branch with squashed history**

```bash
cd /home/lns/iplayer-arr
git checkout --orphan public-release
git add -A
git commit -m "Initial public release

BBC iPlayer download manager with web UI, Sonarr integration,
and built-in WireGuard VPN support via hotio base image.

Features:
- BBC iPlayer search and download via IBL API
- HLS stream download with quality selection
- Newznab indexer and SABnzbd download API (Sonarr compatible)
- Real-time dashboard with SSE progress
- Built-in WireGuard VPN (off by default)
- Setup wizard and system health monitoring"
```

- [ ] **Step 3: Push to GitHub**

```bash
cd /home/lns/iplayer-arr && git push origin public-release:main --force
```

- [ ] **Step 4: Tag v0.1.0 and push (triggers GHCR publish)**

```bash
cd /home/lns/iplayer-arr
git tag v0.1.0
git push origin v0.1.0
```

- [ ] **Step 5: Verify release workflow triggers**

Check https://github.com/Will-Luck/iplayer-arr/actions for the Release workflow run.

- [ ] **Step 6: Return to master branch**

```bash
cd /home/lns/iplayer-arr && git checkout master
```

The `public-release` orphan branch can be deleted locally after confirming GitHub looks correct.

- [ ] **Step 7: Clean up remotes (optional)**

```bash
cd /home/lns/iplayer-arr
git remote remove github
```

Keep `gitea` remote locally for internal use. The `origin` remote points to the canonical public repo.
