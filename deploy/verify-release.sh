#!/usr/bin/env bash
# Reconcile repository SHA -> image label -> running container for the Warmbly
# backend. HTTP 200 on a health endpoint proves a process answered, not which
# code answered, so this is the acceptance gate for a release.
#
# Usage: verify-release.sh <expected-sha> [container]
set -euo pipefail

EXPECTED="${1:?usage: verify-release.sh <expected-sha> [container]}"
CONTAINER="${2:-warmbly-confenge-backend-1}"

if ! docker inspect "$CONTAINER" >/dev/null 2>&1; then
  echo "FAIL: container ${CONTAINER} not found"
  exit 1
fi

image_id=$(docker inspect "$CONTAINER" --format '{{.Image}}')
running=$(docker inspect "$CONTAINER" --format '{{.State.Running}}')
label=$(docker inspect "$image_id" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' 2>/dev/null || echo "")
envsha=$(docker inspect "$CONTAINER" --format '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^WARMBLY_RELEASE_SHA=//p' | head -1)

echo "container   : ${CONTAINER} (running=${running})"
echo "image id    : ${image_id}"
echo "image label : ${label:-<none>}"
echo "runtime env : ${envsha:-<none>}"

if [ "$running" != "true" ]; then
  echo "-> FAIL: container is not running"
  exit 1
fi
if [ -z "$label" ] || [ "$label" = "local" ]; then
  echo "-> FAIL: image carries no release revision label; the running code cannot be proven"
  exit 1
fi
case "$EXPECTED" in
  "$label"*) ;;
  *) echo "-> FAIL: image label ${label} does not match expected ${EXPECTED}"; exit 1 ;;
esac
if [ -n "$envsha" ]; then
  case "$EXPECTED" in
    "$envsha"*) ;;
    *) echo "-> FAIL: runtime WARMBLY_RELEASE_SHA ${envsha} does not match expected ${EXPECTED}"; exit 1 ;;
  esac
fi
echo "-> OK: repo ${EXPECTED} reconciles with image label and runtime"
