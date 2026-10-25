# Stage 1: Build frontend
# The bundle is the same on every platform, so it is built natively. Under
# QEMU, npm ci for linux/arm64 sometimes dies with SIGILL and then hangs
# until the job times out (GitHub rebuild runs of 2026-09-13 and 2026-10-25).
FROM --platform=$BUILDPLATFORM node:22-alpine AS frontend-build
WORKDIR /app/frontend
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm ci
COPY frontend/ .
RUN npm run build

# Stage 2: Build Go binary
# Cross-compiled on the build platform for the same reason; CGO is off.
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS go-build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
RUN apk add --no-cache git
COPY . .
COPY --from=frontend-build /app/frontend/dist ./internal/web/dist
ARG VERSION=""
ARG BUILD_DATE=""
RUN [ -z "$VERSION" ] && VERSION=$(git describe --tags --always 2>/dev/null || echo "dev"); \
    [ -z "$BUILD_DATE" ] && BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ); \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build \
      -ldflags "-X github.com/Will-Luck/iplayer-arr/internal/api.appVersion=${VERSION} \
                -X github.com/Will-Luck/iplayer-arr/internal/api.buildDate=${BUILD_DATE}" \
      -o /iplayer-arr ./cmd/iplayer-arr/

# Stage 3: Runtime (hotio base with optional VPN)
FROM ghcr.io/hotio/base:alpinevpn

LABEL org.opencontainers.image.title="iplayer-arr"
LABEL org.opencontainers.image.description="BBC iPlayer Newznab indexer and SABnzbd download client for Sonarr"
LABEL org.opencontainers.image.source="https://github.com/Will-Luck/iplayer-arr"
LABEL org.opencontainers.image.url="https://github.com/Will-Luck/iplayer-arr"
LABEL org.opencontainers.image.documentation="https://github.com/Will-Luck/iplayer-arr#readme"
LABEL org.opencontainers.image.licenses="GPL-3.0"

RUN apk add --no-cache ffmpeg

COPY --from=go-build /iplayer-arr /app/iplayer-arr
COPY ./s6/ /etc/s6-overlay/s6-rc.d/

ENV TZ=Europe/London
ENV WEBUI_PORTS="62001/tcp"

EXPOSE 62001
VOLUME ["/config", "/downloads"]
