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
RUN apk add --no-cache git
COPY . .
COPY --from=frontend-build /app/frontend/dist ./internal/web/dist
ARG VERSION=""
ARG BUILD_DATE=""
RUN [ -z "$VERSION" ] && VERSION=$(git describe --tags --always 2>/dev/null || echo "dev"); \
    [ -z "$BUILD_DATE" ] && BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ); \
    CGO_ENABLED=0 GOOS=linux go build \
      -ldflags "-X github.com/Will-Luck/iplayer-arr/internal/api.appVersion=${VERSION} \
                -X github.com/Will-Luck/iplayer-arr/internal/api.buildDate=${BUILD_DATE}" \
      -o /iplayer-arr ./cmd/iplayer-arr/

# Stage 3: Runtime (hotio base with optional VPN)
FROM ghcr.io/hotio/base:alpinevpn

RUN apk add --no-cache ffmpeg

COPY --from=go-build /iplayer-arr /app/iplayer-arr
COPY ./s6/ /etc/s6-overlay/s6-rc.d/

ENV TZ=Europe/London
ENV WEBUI_PORTS="62001/tcp"

EXPOSE 62001
VOLUME ["/config", "/downloads"]
