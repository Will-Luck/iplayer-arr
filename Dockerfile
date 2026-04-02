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

# Stage 3: Runtime
FROM alpine:3.21
RUN apk add --no-cache ffmpeg tzdata su-exec && \
    addgroup -g 1000 iplayer && \
    adduser -u 1000 -G iplayer -D iplayer

COPY --from=go-build /iplayer-arr /usr/local/bin/iplayer-arr
COPY <<'EOF' /entrypoint.sh
#!/bin/sh
PUID=${PUID:-1000}
PGID=${PGID:-1000}
if [ "$(id -u iplayer)" != "$PUID" ]; then
  deluser iplayer 2>/dev/null
  delgroup iplayer 2>/dev/null
  addgroup -g "$PGID" iplayer
  adduser -u "$PUID" -G iplayer -D iplayer
fi
chown iplayer:iplayer /config /downloads 2>/dev/null
exec su-exec iplayer:iplayer iplayer-arr "$@"
EOF
RUN chmod +x /entrypoint.sh

EXPOSE 8191
VOLUME ["/config", "/downloads"]

ENV TZ=Europe/London

ENTRYPOINT ["/entrypoint.sh"]
