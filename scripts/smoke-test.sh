#!/usr/bin/env bash
#
# scripts/smoke-test.sh -- isolated Docker smoke test for iplayer-arr.
#
# Subcommands:
#   build  Build a smoke-tagged image (iplayer-arr:smoke).
#   up     Start an isolated container on a random high port with tmpfs
#          volumes, scrape its generated api_key, persist state to
#          /tmp/iplayer-arr-smoke.state.
#   diag   Curl every /api/diag/<name> endpoint and assert verdict==pass.
#          On success, leaves the container running for inspection.
#   down   Stop the container and remove the state file.
#
# Conventions (LuckNet smoke_test_pattern):
#   * Random high port in 62000-63999 (avoids prod port 62945 et al).
#   * tmpfs /config + /downloads (no host bind, no persistent state).
#   * Distinct container name (no overlap with prod iplayer-arr).
#   * api_key scraped from unauthenticated /api/config; no env override.
#
# Usage:
#   scripts/smoke-test.sh build
#   scripts/smoke-test.sh up
#   scripts/smoke-test.sh diag
#   scripts/smoke-test.sh down
#
#   # one-shot:
#   make smoke

set -euo pipefail

STATE_FILE="${IPLAYER_ARR_SMOKE_STATE:-/tmp/iplayer-arr-smoke.state}"
IMAGE_TAG="${IPLAYER_ARR_SMOKE_IMAGE:-iplayer-arr:smoke}"
DIAG_ENDPOINTS=(sonarr-handshake ffmpeg bbc sab auth-paths)

cleanup_on_error() {
  local rc=$?
  if [ "$rc" -ne 0 ] && [ -f "$STATE_FILE" ]; then
    # shellcheck disable=SC1090
    . "$STATE_FILE" || true
    if [ -n "${NAME:-}" ]; then
      echo "smoke-test: error rc=$rc, tearing down $NAME"
      docker rm -f "$NAME" >/dev/null 2>&1 || true
    fi
    rm -f "$STATE_FILE"
  fi
  exit "$rc"
}
trap cleanup_on_error EXIT

require_jq() {
  command -v jq >/dev/null 2>&1 || {
    echo "smoke-test: jq is required" >&2
    exit 1
  }
}

random_high_port() {
  echo $((62000 + RANDOM % 1900))
}

cmd_build() {
  docker build -t "$IMAGE_TAG" .
}

cmd_up() {
  require_jq
  if [ -f "$STATE_FILE" ]; then
    # shellcheck disable=SC1090
    . "$STATE_FILE"
    if [ -n "${NAME:-}" ] && docker ps --format '{{.Names}}' | grep -qx "$NAME"; then
      echo "smoke-test: $NAME already running on port ${PORT:-?}"
      return 0
    fi
    rm -f "$STATE_FILE"
  fi

  local port name
  port=$(random_high_port)
  name="iplayer-arr-smoke-$RANDOM"

  echo "smoke-test: starting $name on port $port"
  docker run -d --rm \
    --name "$name" \
    --tmpfs /config:rw,size=64m \
    --tmpfs /downloads:rw,size=64m \
    -p "127.0.0.1:${port}:62001" \
    "$IMAGE_TAG" >/dev/null

  # Wait up to 30s for /api/status to return 200.
  local i
  for i in $(seq 1 30); do
    if curl -fsS "http://127.0.0.1:${port}/api/status" >/dev/null 2>&1; then
      break
    fi
    if [ "$i" -eq 30 ]; then
      echo "smoke-test: $name did not become ready within 30s" >&2
      docker logs "$name" || true
      docker rm -f "$name" >/dev/null 2>&1 || true
      exit 1
    fi
    sleep 1
  done

  local key
  key=$(curl -fsS "http://127.0.0.1:${port}/api/config" | jq -re '.api_key')
  if [ -z "$key" ] || [ "$key" = "null" ]; then
    echo "smoke-test: api_key missing from /api/config" >&2
    docker rm -f "$name" >/dev/null 2>&1 || true
    exit 1
  fi

  cat > "$STATE_FILE" <<EOF
NAME="$name"
PORT="$port"
APIKEY="$key"
EOF
  echo "smoke-test: $name ready on http://127.0.0.1:${port} (state: $STATE_FILE)"
}

cmd_diag() {
  require_jq
  if [ ! -f "$STATE_FILE" ]; then
    echo "smoke-test: no state file ($STATE_FILE); run 'up' first" >&2
    exit 1
  fi
  # shellcheck disable=SC1090
  . "$STATE_FILE"

  local fail=0
  printf '%-22s %-8s %s\n' "endpoint" "verdict" "detail"
  printf '%-22s %-8s %s\n' "----------------------" "--------" "------"
  for ep in "${DIAG_ENDPOINTS[@]}"; do
    local body verdict failed
    if ! body=$(curl -fsS "http://127.0.0.1:${PORT}/api/diag/${ep}?apikey=${APIKEY}" 2>&1); then
      printf '%-22s %-8s %s\n' "$ep" "ERROR" "curl failed: $body"
      fail=1
      continue
    fi
    verdict=$(echo "$body" | jq -r '.verdict // "missing"')
    failed=$(echo "$body" | jq -r '.checks_failed // [] | join(",")')
    printf '%-22s %-8s %s\n' "$ep" "$verdict" "${failed:-(none)}"
    if [ "$verdict" != "pass" ]; then
      fail=1
    fi
  done

  if [ "$fail" -ne 0 ]; then
    echo "smoke-test: one or more diag endpoints failed" >&2
    exit 1
  fi
  # On success: leave container running so the operator can inspect.
  echo "smoke-test: all diag endpoints pass (container still running, use 'down' to stop)"
}

cmd_down() {
  if [ ! -f "$STATE_FILE" ]; then
    echo "smoke-test: nothing to tear down (no state file)"
    return 0
  fi
  # shellcheck disable=SC1090
  . "$STATE_FILE"
  if [ -n "${NAME:-}" ]; then
    docker rm -f "$NAME" >/dev/null 2>&1 || true
    echo "smoke-test: stopped $NAME"
  fi
  rm -f "$STATE_FILE"
}

usage() {
  cat <<EOF
Usage: $0 <build|up|diag|down>

  build  Build the smoke image (${IMAGE_TAG}).
  up     Start an isolated container and scrape its api_key.
  diag   Hit every /api/diag/<name>; assert verdict==pass.
  down   Stop the container and clear state.

State file: $STATE_FILE
EOF
}

case "${1:-}" in
  build) cmd_build ;;
  up) cmd_up ;;
  diag) cmd_diag ;;
  down) cmd_down ;;
  -h|--help|help|"") usage ;;
  *) echo "smoke-test: unknown subcommand '$1'" >&2; usage; exit 2 ;;
esac
