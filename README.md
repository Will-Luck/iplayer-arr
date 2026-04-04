# iplayer-arr

BBC iPlayer download manager with a web UI, Sonarr integration, and built-in VPN support.

![Dashboard](docs/screenshots/dashboard.png)

## Features

- BBC iPlayer search and browse (via BBC IBL API)
- Automatic HLS stream download with quality selection (1080p/720p/540p/396p)
- Download queue with configurable worker pool
- Newznab-compatible indexer (works with Sonarr)
- SABnzbd-compatible download API
- Real-time dashboard with SSE live progress
- Built-in WireGuard VPN via hotio base image (off by default)
- Setup wizard for first-run configuration
- System health monitoring (disk usage, FFmpeg status)

## Quick Start

```bash
docker run -d \
  --name iplayer-arr \
  -p 8191:8191 \
  -v iplayer-arr-config:/config \
  -v /path/to/downloads:/downloads \
  -e TZ=Europe/London \
  ghcr.io/will-luck/iplayer-arr:latest
```

iPlayer requires a UK IP. Use the VPN section below or run behind an existing UK VPN/proxy.

## VPN Configuration

Built on [hotio/base:alpinevpn](https://hotio.dev/containers/base/) with WireGuard, nftables kill switch, and s6-overlay service management. VPN is **off by default**. Enable with `VPN_ENABLED=true`. Requires `--cap-add=NET_ADMIN` and `--sysctl net.ipv4.conf.all.src_valid_mark=1`.

### PIA Example

```bash
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

### Generic WireGuard

Mount your WireGuard configuration into `/config/wireguard/wg0.conf`, then set `VPN_ENABLED=true` and `VPN_PROVIDER=generic`.

### VPN Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `VPN_ENABLED` | `false` | Enable WireGuard VPN |
| `VPN_PROVIDER` | `generic` | VPN provider: `generic`, `pia`, or `proton` |
| `VPN_LAN_NETWORK` | - | LAN CIDR for direct access to the web UI (e.g. `192.168.1.0/24`) |
| `VPN_PIA_USER` | - | PIA username (when provider is `pia`) |
| `VPN_PIA_PASS` | - | PIA password (when provider is `pia`) |
| `VPN_PIA_PREFERRED_REGION` | - | PIA region (e.g. `uk`) |
| `VPN_HEALTHCHECK_ENABLED` | `false` | Bring down container if VPN connectivity fails |
| `VPN_AUTO_PORT_FORWARD` | `false` | Auto-retrieve forwarded port (PIA/Proton) |
| `WEBUI_PORTS` | - | Ports to allow through the kill switch (e.g. `8191/tcp`) |

See the [hotio documentation](https://hotio.dev/containers/base/) for the full variable list.

## Environment Variables

### Application Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `CONFIG_DIR` | `/config` | BoltDB and config storage |
| `DOWNLOAD_DIR` | `/downloads` | Download output directory |
| `PORT` | `8191` | HTTP server listen port |

The API key is auto-generated on first run and visible in the Config page.

### Container Variables

Handled by the hotio base image:

| Variable | Default | Description |
|----------|---------|-------------|
| `PUID` | `1000` | User ID for file permissions |
| `PGID` | `1000` | Group ID for file permissions |
| `TZ` | `Europe/London` | Container timezone |
| `UMASK` | `002` | File permission mask |

## Sonarr Integration

**Indexer:** Add as a Newznab custom indexer. URL: `http://iplayer-arr:8191/newznab/api`. API key from the Config page. Categories: 5000 (TV).

**Download client:** Add as SABnzbd. Host: `iplayer-arr`, port: `8191`, URL base: `/sabnzbd`, category: `sonarr`. API key from the Config page.

## Development

```bash
# Frontend (writes to frontend/dist/)
cd frontend && npm ci && npm run build

# Copy frontend assets into Go embed directory
cp -r frontend/dist/* internal/web/dist/

# Backend
go build ./cmd/iplayer-arr/
go test ./...
```

The copy step is needed because the Go binary embeds `internal/web/dist/` (via `//go:embed`), not `frontend/dist/`. The Dockerfile handles this automatically.

## Licence

GPL-3.0. See [LICENSE](LICENSE).
