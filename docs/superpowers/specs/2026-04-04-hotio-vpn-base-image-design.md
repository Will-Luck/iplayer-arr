# Hotio VPN Base Image Integration

**Date:** 2026-04-04
**Status:** Approved
**Approach:** Single image on `ghcr.io/hotio/base:alpinevpn` with optional VPN

## Summary

Replace iplayer-arr's plain `alpine:3.21` runtime stage with hotio's `alpinevpn` base image to provide built-in WireGuard VPN support with a kill switch. VPN is disabled by default -- users opt in via `VPN_ENABLED=true`. This gives iplayer-arr the same VPN infrastructure as the other hotio containers (qbittorrent, sonarr, etc.) with near-zero ongoing maintenance since hotio carries the VPN stack.

## Motivation

iplayer-arr needs a UK exit IP to access BBC iPlayer content. The current container has no VPN support -- users must handle VPN routing externally. By adopting hotio's base image, the container gains WireGuard, an nftables kill switch, PIA/Proton/generic provider support, DNS handling, and health monitoring out of the box.

## Dockerfile Changes

### Build stages (unchanged)

- **Stage 1** (`node:22-alpine`): Frontend Solid.js build
- **Stage 2** (`golang:1.24-alpine`): Go binary build with embedded frontend

### Runtime stage (changed)

**Before:**
```dockerfile
FROM alpine:3.21
RUN apk add --no-cache ffmpeg tzdata su-exec
# custom PUID/PGID handling, inline entrypoint
```

**After:**
```dockerfile
FROM ghcr.io/hotio/base:alpinevpn
RUN apk add --no-cache ffmpeg

COPY --from=backend /iplayer-arr /app/iplayer-arr
COPY ./s6/ /etc/s6-overlay/s6-rc.d/

ENV WEBUI_PORTS="8191/tcp"
EXPOSE 8191
VOLUME ["/config", "/downloads"]
```

**Removed packages:** `tzdata`, `su-exec` (included in hotio base).
**Removed:** Inline entrypoint script (hotio s6-overlay handles PUID/PGID, process management).
**Added:** `WEBUI_PORTS` env var (tells the kill switch which ports to allow inbound LAN access on, so Sonarr can reach the Newznab/SABnzbd endpoints).

## s6 Service Definition

Three files added in `s6/` at the repo root:

```
s6/
  service-iplayer-arr/
    run              # #!/usr/bin/env bash\nexec /app/iplayer-arr
    type             # longrun
    dependencies.d/
      init-wireguard # empty file (start after VPN is ready)
  user/
    contents.d/
      service-iplayer-arr  # empty file (registers in s6 bundle)
```

### Behaviour

- s6-overlay starts `service-iplayer-arr` after `init-wireguard` completes
- When VPN is disabled, `init-wireguard` is a no-op -- the app starts immediately
- s6 handles restart-on-crash and graceful shutdown
- Environment variables (`CONFIG_DIR`, `DOWNLOAD_DIR`, `PORT`, `PUID`, `PGID`, `TZ`) pass through unchanged

## Docker Run Command

### With VPN (production on .57)

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
  -e VPN_LAN_NETWORK=<media-station_default CIDR> \
  -e VPN_PIA_USER=<from 1password pia-vpn> \
  -e VPN_PIA_PASS=<from 1password pia-vpn> \
  -e VPN_PIA_PREFERRED_REGION=uk \
  iplayer-arr:latest
```

`VPN_LAN_NETWORK` must be set to the Docker network subnet (from `docker network inspect media-station_default | grep Subnet`) so Sonarr can reach iplayer-arr through the kill switch.

### Without VPN (public users)

```bash
docker run -d \
  --name iplayer-arr \
  -p 8191:8191 \
  -v iplayer-arr-config:/config \
  -v /path/to/downloads:/downloads \
  -e PUID=1000 \
  -e PGID=1000 \
  iplayer-arr:latest
```

Identical to the current experience. No `--cap-add`, no VPN env vars.

## CI/CD

### Gitea Actions (existing, unchanged)

`.gitea/workflows/ci.yml` continues to run `go vet` and `go test`.

### GitHub Actions (new, for when repo goes public)

`.github/workflows/docker.yml`:

- **Triggers:** push to main, tag push (`v*`), weekly scheduled rebuild (Monday 04:00)
- **Builds and pushes** to GHCR with tags `:latest` (main) and `:vX.Y.Z` (tags)
- **Weekly rebuild** picks up hotio base image updates (Alpine patches, WireGuard, nftables) automatically

### Local builds (unchanged)

`docker build -t iplayer-arr:latest .` on .57 for production deployment. No registry dependency.

## Documentation Updates

### README (when repo goes public)

- **Basic usage section:** Simple docker run without VPN (default, primary documentation)
- **VPN section:** Explains `--cap-add=NET_ADMIN`, `VPN_ENABLED=true`, provider env vars, `VPN_LAN_NETWORK` for container-to-container access
- **Links to hotio VPN docs** for the full env var reference rather than duplicating 20+ vars
- **Environment variable table:** Extended with VPN vars marked as optional

### LuckNet docs (post-deploy)

- `containers/docker-run-commands.md` updated with new run command
- `networklayout.md`, `state.md`, `changes.md` via standard post-deploy cascade

## No Application Code Changes

The Go application is completely unaware of the VPN layer. It makes HTTP requests to BBC endpoints (`open.live.bbc.co.uk`, `ibl.api.bbc.co.uk`, BBC CDN) and either gets UK content or doesn't. The existing setup wizard geo-check UI hint remains accurate.

## Trade-offs

| Aspect | Impact |
|--------|--------|
| Image size | ~50MB to ~180MB (ffmpeg + hotio base with WireGuard/nftables/s6) |
| Runtime (VPN off) | No change -- hotio services are no-ops when VPN_ENABLED is unset |
| Runtime (VPN on) | Requires `--cap-add=NET_ADMIN` and sysctl |
| Maintenance | Near-zero -- hotio maintains the VPN stack, weekly CI rebuild picks up updates |
| Dependency | Couples to hotio/base lifecycle (acceptable -- 6 hotio containers already in production) |
