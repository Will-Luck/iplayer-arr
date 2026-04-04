# Hotio VPN Base Image Integration - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace iplayer-arr's plain Alpine runtime with hotio's alpinevpn base image to provide optional built-in WireGuard VPN with kill switch.

**Architecture:** The multi-stage Dockerfile keeps its existing frontend (node) and backend (golang) build stages unchanged. Only the final runtime stage switches from `alpine:3.21` to `ghcr.io/hotio/base:alpinevpn`. An s6-overlay service definition tells hotio how to run the Go binary. VPN is off by default; users opt in with `VPN_ENABLED=true`.

**Tech Stack:** Docker, s6-overlay, WireGuard (via hotio base), nftables, PIA

**Spec:** `docs/superpowers/specs/2026-04-04-hotio-vpn-base-image-design.md`

---

## File Map

| Action | Path | Responsibility |
|--------|------|----------------|
| Create | `s6/service-iplayer-arr/run` | s6 service run script (exec with privilege drop) |
| Create | `s6/service-iplayer-arr/type` | s6 service type declaration |
| Create | `s6/service-iplayer-arr/dependencies.d/init-wireguard` | Dependency on VPN init |
| Create | `s6/user/contents.d/service-iplayer-arr` | Register service in s6 bundle |
| Modify | `Dockerfile` | Replace runtime stage with hotio base |
| Create | `.github/workflows/docker.yml` | GitHub Actions image build + GHCR push |
| Unchanged | `.gitea/workflows/ci.yml` | Go vet + test (no Docker build -- runner lacks DinD) |

---

### Task 1: Create s6 Service Files

**Files:**
- Create: `s6/service-iplayer-arr/run`
- Create: `s6/service-iplayer-arr/type`
- Create: `s6/service-iplayer-arr/dependencies.d/init-wireguard`
- Create: `s6/user/contents.d/service-iplayer-arr`

- [ ] **Step 1: Create the service run script**

Create `s6/service-iplayer-arr/run`:

```bash
#!/usr/bin/env bash
exec s6-setuidgid hotio /app/iplayer-arr
```

This drops privileges to the `hotio` user (created by hotio's `init-setup` from `PUID`/`PGID` env vars) then execs the Go binary. s6 will restart the process if it exits unexpectedly.

- [ ] **Step 2: Create the service type file**

Create `s6/service-iplayer-arr/type` with exactly this content (no trailing newline):

```
longrun
```

This tells s6-overlay this is a long-running daemon, not a one-shot init script.

- [ ] **Step 3: Create the VPN dependency**

Create `s6/service-iplayer-arr/dependencies.d/init-wireguard` as an empty file:

```bash
touch s6/service-iplayer-arr/dependencies.d/init-wireguard
```

This empty file tells s6-rc to wait for `init-wireguard` to complete before starting `service-iplayer-arr`. When VPN is disabled, `init-wireguard` is a no-op so the app starts immediately.

- [ ] **Step 4: Register service in s6 user bundle**

Create `s6/user/contents.d/service-iplayer-arr` as an empty file:

```bash
touch s6/user/contents.d/service-iplayer-arr
```

This registers the service in the s6-overlay user bundle so it gets started during container boot.

- [ ] **Step 5: Make run script executable**

```bash
chmod +x s6/service-iplayer-arr/run
```

- [ ] **Step 6: Verify directory structure**

```bash
find s6/ -type f | sort
```

Expected output:
```
s6/service-iplayer-arr/dependencies.d/init-wireguard
s6/service-iplayer-arr/run
s6/service-iplayer-arr/type
s6/user/contents.d/service-iplayer-arr
```

- [ ] **Step 7: Commit**

```bash
git add s6/
git commit -m "feat: add s6-overlay service definition for hotio base"
```

---

### Task 2: Rewrite Dockerfile Runtime Stage

**Files:**
- Modify: `Dockerfile` (lines 18-46, the entire "Stage 3: Runtime" section)

- [ ] **Step 1: Read the current Dockerfile**

Verify the current state matches what the plan expects. The file should have three stages:
1. `FROM node:22-alpine AS frontend-build` (lines 1-7)
2. `FROM golang:1.24-alpine AS go-build` (lines 9-16)
3. `FROM alpine:3.21` (lines 18-46) -- this is what we replace

- [ ] **Step 2: Replace the runtime stage**

Replace everything from line 18 (`# Stage 3: Runtime`) to end of file with:

```dockerfile
# Stage 3: Runtime (hotio base with optional VPN)
FROM ghcr.io/hotio/base:alpinevpn

RUN apk add --no-cache ffmpeg

COPY --from=go-build /iplayer-arr /app/iplayer-arr
COPY ./s6/ /etc/s6-overlay/s6-rc.d/

ENV TZ=Europe/London
ENV WEBUI_PORTS="8191/tcp"

EXPOSE 8191
VOLUME ["/config", "/downloads"]
```

What changed:
- Base image: `alpine:3.21` to `ghcr.io/hotio/base:alpinevpn`
- Removed: `tzdata` (in hotio base), `su-exec` (replaced by `s6-setuidgid`)
- Removed: User creation (`addgroup`/`adduser`) -- hotio's `init-setup` handles this
- Removed: Inline entrypoint script -- s6-overlay is the entrypoint
- Removed: `ENTRYPOINT` directive -- hotio base provides its own
- Added: `COPY ./s6/` for the s6 service definition from Task 1
- Added: `WEBUI_PORTS` env var for the kill switch
- Preserved: `ENV TZ=Europe/London` (overrides hotio's default `Etc/UTC`)
- Binary path changed: `/usr/local/bin/iplayer-arr` to `/app/iplayer-arr` (hotio convention)

- [ ] **Step 3: Verify the complete Dockerfile reads correctly**

The full Dockerfile should now be:

```dockerfile
# Stage 1: Build frontend
FROM node:22-alpine AS frontend-build
WORKDIR /app/frontend
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm ci
COPY frontend/ .
RUN npm run build

# Stage 2: Build Go binary
FROM golang:1.24-alpine AS go-build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend-build /app/frontend/dist ./internal/web/dist
RUN CGO_ENABLED=0 GOOS=linux go build -o /iplayer-arr ./cmd/iplayer-arr/

# Stage 3: Runtime (hotio base with optional VPN)
FROM ghcr.io/hotio/base:alpinevpn

RUN apk add --no-cache ffmpeg

COPY --from=go-build /iplayer-arr /app/iplayer-arr
COPY ./s6/ /etc/s6-overlay/s6-rc.d/

ENV TZ=Europe/London
ENV WEBUI_PORTS="8191/tcp"

EXPOSE 8191
VOLUME ["/config", "/downloads"]
```

- [ ] **Step 4: Commit**

```bash
git add Dockerfile
git commit -m "feat: switch runtime to hotio alpinevpn base image

Replaces alpine:3.21 with ghcr.io/hotio/base:alpinevpn for optional
built-in WireGuard VPN with kill switch. VPN disabled by default.
s6-overlay handles process supervision and PUID/PGID."
```

---

### Task 3: Build and Test Locally (No VPN)

**Files:** None modified -- this is a validation task.

This task verifies the image builds and the app starts correctly without VPN enabled, confirming no regression for the default use case.

- [ ] **Step 1: Build the image**

```bash
cd /home/lns/iplayer-arr
docker build -t iplayer-arr:vpn-test .
```

Expected: Build completes successfully. Watch for:
- Stage 1 and 2 should be unchanged (cached if deps haven't changed)
- Stage 3 pulls `ghcr.io/hotio/base:alpinevpn` (first build will download ~100MB)
- `apk add ffmpeg` succeeds
- `COPY ./s6/` succeeds
- No errors

- [ ] **Step 2: Run the test container without VPN**

```bash
docker run -d \
  --name iplayer-arr-vpn-test \
  -p 63000:8191/tcp \
  -v /tmp/iplayer-test-config:/config \
  -v /tmp/iplayer-test-downloads:/downloads \
  -e PUID=1000 \
  -e PGID=1000 \
  iplayer-arr:vpn-test
```

Uses a temporary port (63000) and temp volumes to avoid touching production.

- [ ] **Step 3: Verify container starts and s6 initialises**

```bash
docker logs iplayer-arr-vpn-test 2>&1 | head -30
```

Expected: s6-overlay init messages followed by the iplayer-arr startup log. Look for:
- `s6-rc: info: service init-setup: starting` (hotio PUID/PGID setup)
- No errors about missing services or permissions
- The app's normal startup output (listening on port 8191)

- [ ] **Step 4: Verify the app responds**

```bash
curl -s http://localhost:63000/health
```

Expected: `ok`

- [ ] **Step 5: Verify file ownership**

```bash
docker exec iplayer-arr-vpn-test ls -la /config /downloads
```

Expected: Both directories owned by `hotio:hotio` (or the PUID:PGID equivalent).

- [ ] **Step 6: Clean up test container**

```bash
docker stop iplayer-arr-vpn-test && docker rm iplayer-arr-vpn-test
docker rmi iplayer-arr:vpn-test
rm -rf /tmp/iplayer-test-config /tmp/iplayer-test-downloads
```

- [ ] **Step 7: Commit any fixes if needed**

If any steps above failed, diagnose from `docker logs iplayer-arr-vpn-test`, fix the relevant file (likely `s6/service-iplayer-arr/run` or `Dockerfile`), rebuild with `docker build -t iplayer-arr:vpn-test .`, and re-run from Step 2. Once passing:

```bash
git add -A
git commit -m "fix: correct s6/Dockerfile issues found during no-VPN testing"
```

If all steps passed, skip this step.

---

### Task 4: Build and Test with VPN Enabled

**Files:** None modified -- this is a validation task.

This task verifies the VPN tunnel works, the kill switch is active, and Sonarr can still reach the API through the Docker bridge network.

- [ ] **Step 1: Get PIA credentials**

```bash
export PIA_USER=$(op item get "pia-vpn" --vault "Server-Keys" --field username --reveal)
export PIA_PASS=$(op item get "pia-vpn" --vault "Server-Keys" --field password --reveal)
```

- [ ] **Step 2: Build the image (skip if already built from Task 3)**

```bash
cd /home/lns/iplayer-arr
docker build -t iplayer-arr:vpn-test .
```

- [ ] **Step 3: Run with VPN enabled**

```bash
docker run -d \
  --name iplayer-arr-vpn-test \
  --cap-add=NET_ADMIN \
  --sysctl net.ipv4.conf.all.src_valid_mark=1 \
  --network media-station_default \
  -p 63000:8191/tcp \
  -v /tmp/iplayer-test-config:/config \
  -v /tmp/iplayer-test-downloads:/downloads \
  -e PUID=1000 \
  -e PGID=1000 \
  -e TZ=Europe/London \
  -e VPN_ENABLED=true \
  -e VPN_PROVIDER=pia \
  -e VPN_LAN_NETWORK=192.168.1.0/24 \
  -e VPN_PIA_USER="$PIA_USER" \
  -e VPN_PIA_PASS="$PIA_PASS" \
  -e VPN_PIA_PREFERRED_REGION=uk \
  iplayer-arr:vpn-test
```

- [ ] **Step 4: Wait for VPN tunnel to establish and check logs**

```bash
sleep 15
docker logs iplayer-arr-vpn-test 2>&1 | tail -40
```

Expected: WireGuard handshake messages, nftables rules applied, then iplayer-arr startup. Look for:
- `[init-wireguard]` log lines showing WireGuard config applied
- No `ERROR` lines
- iplayer-arr listening message

- [ ] **Step 5: Verify UK exit IP**

```bash
docker exec iplayer-arr-vpn-test wget -qO- https://ipinfo.io/country
```

Expected: `GB`

- [ ] **Step 6: Verify the app responds via host port**

```bash
curl -s http://localhost:63000/health
```

Expected: `ok`

This confirms the container port mapping works through Docker's NAT. It does not test LAN access through the kill switch (that requires a request from a different host on 192.168.1.0/24).

- [ ] **Step 6a: Verify LAN access through the kill switch**

From the .58 Tailscale VM (a different host on the LAN):

```bash
ssh tailscale-server "curl -s http://192.168.1.57:63000/health"
```

Expected: `ok`

This confirms `WEBUI_PORTS` + `VPN_LAN_NETWORK` allows inbound access from the home LAN through the nftables kill switch. If this fails but Step 6 passes, the kill switch is blocking LAN traffic and `VPN_LAN_NETWORK` needs checking.

- [ ] **Step 7: Verify Docker bridge access (Sonarr path)**

From another container on `media-station_default`, confirm the API is reachable:

```bash
docker exec sonarr wget -qO- http://iplayer-arr-vpn-test:8191/health
```

Expected: `ok`

This confirms hotio's nftables auto-allows Docker bridge traffic without needing it in `VPN_LAN_NETWORK`.

- [ ] **Step 8: Verify geo-check passes**

```bash
curl -s http://localhost:63000/api/status | python3 -c "import sys,json; print(json.load(sys.stdin).get('geo_ok', 'key not found'))"
```

Expected: `true`

- [ ] **Step 9: Clean up test container**

```bash
docker stop iplayer-arr-vpn-test && docker rm iplayer-arr-vpn-test
docker rmi iplayer-arr:vpn-test
rm -rf /tmp/iplayer-test-config /tmp/iplayer-test-downloads
```

---

### Task 5: Deploy to Production

**Files:** None in the repo -- this is a deployment task.

- [ ] **Step 1: Get PIA credentials**

```bash
export PIA_USER=$(op item get "pia-vpn" --vault "Server-Keys" --field username --reveal)
export PIA_PASS=$(op item get "pia-vpn" --vault "Server-Keys" --field password --reveal)
```

- [ ] **Step 2: Build the production image**

```bash
cd /home/lns/iplayer-arr
docker build -t iplayer-arr:latest .
```

- [ ] **Step 3: Stop and remove the current container**

**APPROVAL GATE: Display the following before proceeding and wait for explicit permission.**

Current container details:
```bash
docker inspect iplayer-arr --format '{{.Config.Image}} | Ports: {{range $p, $conf := .NetworkSettings.Ports}}{{$p}}->{{(index $conf 0).HostPort}} {{end}}'
```

Changes:
- Base image: `alpine:3.21` to `ghcr.io/hotio/base:alpinevpn`
- New capabilities: `NET_ADMIN` (for WireGuard/nftables)
- New env vars: `VPN_ENABLED`, `VPN_PROVIDER`, `VPN_LAN_NETWORK`, `VPN_PIA_USER`, `VPN_PIA_PASS`, `VPN_PIA_PREFERRED_REGION`
- Port mapping unchanged: 62932:8191
- Volumes unchanged: `iplayer-arr-config`, `/mnt/media/downloads/iplayer`

```bash
docker stop iplayer-arr && docker rm iplayer-arr
```

- [ ] **Step 4: Start the new container**

```bash
docker run -d \
  --name iplayer-arr \
  --cap-add=NET_ADMIN \
  --sysctl net.ipv4.conf.all.src_valid_mark=1 \
  --network media-station_default \
  --network-alias iplayer-arr.internal \
  -p 62932:8191/tcp \
  -v iplayer-arr-config:/config \
  -v /mnt/media/downloads/iplayer:/downloads \
  -e PUID=1000 \
  -e PGID=1000 \
  -e TZ=Europe/London \
  -e VPN_ENABLED=true \
  -e VPN_PROVIDER=pia \
  -e VPN_LAN_NETWORK=192.168.1.0/24 \
  -e VPN_PIA_USER="$PIA_USER" \
  -e VPN_PIA_PASS="$PIA_PASS" \
  -e VPN_PIA_PREFERRED_REGION=uk \
  iplayer-arr:latest
```

- [ ] **Step 5: Verify VPN and app health**

```bash
sleep 15
docker logs iplayer-arr 2>&1 | tail -30
curl -s http://localhost:62932/health
docker exec iplayer-arr wget -qO- https://ipinfo.io/country
```

Expected: Logs show clean s6 init + WireGuard handshake, health returns `ok`, country returns `GB`.

- [ ] **Step 5a: Verify LAN access through kill switch**

From the .58 Tailscale VM:

```bash
ssh tailscale-server "curl -s http://192.168.1.57:62932/health"
```

Expected: `ok`. Confirms `VPN_LAN_NETWORK=192.168.1.0/24` allows inbound LAN access.

- [ ] **Step 6: Verify Sonarr integration**

```bash
docker exec sonarr wget -qO- http://iplayer-arr.internal:8191/health
```

Expected: `ok`

- [ ] **Step 7: Verify existing config/data survived**

The `/api/status` endpoint only returns runtime state (ffmpeg, geo_ok, queue_depth). To verify the database persisted, check config and history endpoints:

```bash
# Verify API key survived (config bucket)
curl -s http://localhost:62932/api/config | python3 -c "import sys,json; d=json.load(sys.stdin); print('api_key:', d.get('api_key','MISSING')[:8] + '...')"

# Verify history survived (history bucket)
curl -s http://localhost:62932/api/history | python3 -c "import sys,json; d=json.load(sys.stdin); print('history_count:', len(d) if isinstance(d, list) else 'unexpected format')"

# Verify overrides survived (overrides bucket)
curl -s http://localhost:62932/api/overrides | python3 -c "import sys,json; d=json.load(sys.stdin); print('overrides_count:', len(d) if isinstance(d, list) else 'unexpected format')"
```

Expected: API key starts with the same prefix as before the redeploy, history count matches previous state, overrides are present. If any return empty/MISSING, the volume mount failed.

---

### Task 6: Post-Deploy Documentation

**Files:**
- Modify: `/home/lns/claude/containers/docker-run-commands.md`
- Modify: `/home/lns/claude/state.md`
- Modify: `/home/lns/claude/networklayout.md`
- Modify: `/home/lns/claude/changes.md`

- [ ] **Step 1: Run post-deploy cascade**

Use the `/lucknet-ops:post-deploy` skill or manually update:

1. Update `containers/docker-run-commands.md` with the new `docker run` command from Task 5 Step 4
2. Update `state.md` with the new image base (`ghcr.io/hotio/base:alpinevpn`)
3. Check `networklayout.md` -- port mapping is unchanged (62932:8191) so only the image description needs updating if listed
4. Append to `changes.md`:
   ```
   ## 2026-04-04 - iplayer-arr: hotio VPN base image integration
   - Switched runtime base from alpine:3.21 to ghcr.io/hotio/base:alpinevpn
   - Added optional WireGuard VPN with nftables kill switch (VPN_ENABLED=true)
   - PIA UK region configured for BBC iPlayer geo-compliance
   - s6-overlay replaces custom entrypoint for process management
   - No application code changes -- VPN is infrastructure-only
   ```

- [ ] **Step 2: Commit and push LuckNet docs**

```bash
cd /home/lns/claude
git add containers/docker-run-commands.md state.md networklayout.md changes.md
git commit -m "post-deploy: iplayer-arr hotio VPN base image integration"
git push
```

- [ ] **Step 3: Commit and push iplayer-arr repo**

```bash
cd /home/lns/iplayer-arr
git push gitea master
```

---

### Task 7: GitHub Actions Docker Workflow (Deferred)

**Files:**
- Create: `.github/workflows/docker.yml`

This task is for when the repo goes public. It can be implemented now or deferred.

- [ ] **Step 1: Create the workflow file**

Create `.github/workflows/docker.yml`:

```yaml
name: Docker Image

on:
  push:
    branches: [main, master]
    tags: ['v*']
  schedule:
    - cron: '0 4 * * 1'

jobs:
  build-and-push:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write

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
            type=raw,value=latest,enable={{is_default_branch}}
            type=semver,pattern=v{{version}}
            type=semver,pattern=v{{major}}.{{minor}}

      - uses: docker/build-push-action@v6
        with:
          context: .
          push: true
          tags: ${{ steps.meta.outputs.tags }}
          labels: ${{ steps.meta.outputs.labels }}
          cache-from: type=gha
          cache-to: type=gha,mode=max
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/docker.yml
git commit -m "ci: add GitHub Actions Docker image build and GHCR push"
```
