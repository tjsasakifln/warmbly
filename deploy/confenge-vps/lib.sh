#!/usr/bin/env bash
# Shared helpers for CONFENGE VPS ops scripts. Never prints secrets.
# shellcheck shell=bash

set -euo pipefail

_CONFENGE_VPS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

confenge_vps_root() {
  echo "$_CONFENGE_VPS_DIR"
}

# Resolve repo root: deploy/confenge-vps/../..
confenge_repo_root() {
  cd "$_CONFENGE_VPS_DIR/../.." && pwd
}

load_vps_env() {
  local root envf caller_release_sha
  root="$(confenge_repo_root)"
  envf="${CONFENGE_VPS_ENV:-$root/deploy/confenge-vps/.env}"
  # A caller that named the release explicitly outranks the file, and the
  # checkout outranks a value the file kept from an earlier deploy.
  caller_release_sha="${WARMBLY_RELEASE_SHA:-}"
  if [[ -f "$envf" ]]; then
    set -a
    # shellcheck disable=SC1090
    . "$envf"
    set +a
  fi
  if [[ -f "$root/.env.confenge" ]]; then
    set -a
    # shellcheck disable=SC1091
    . "$root/.env.confenge"
    set +a
  fi
  if [[ -n "$caller_release_sha" ]]; then
    WARMBLY_RELEASE_SHA="$caller_release_sha"
  else
    WARMBLY_RELEASE_SHA="$(git -C "$root" rev-parse HEAD 2>/dev/null || echo local)"
  fi
  export WARMBLY_RELEASE_SHA
}

bind_release_identity() {
  local release_sha="${1:-${WARMBLY_RELEASE_SHA:-}}"
  if [[ -z "$release_sha" || "$release_sha" == *[!0-9a-f]* ]] ||
     { [[ ${#release_sha} -ne 40 ]] && [[ ${#release_sha} -ne 64 ]]; }; then
    echo "REFUSE: immutable release SHA must be a full 40- or 64-character lowercase hex digest" >&2
    return 3
  fi
  WARMBLY_RELEASE_SHA="$release_sha"
  CONFENGE_REPOSITORY_SHA="$release_sha"
  export WARMBLY_RELEASE_SHA CONFENGE_REPOSITORY_SHA
}

# registry: production runs CI-built images pinned to the release SHA and never
# compiles on the VPS. `build` is the legacy local-compile path, kept only for
# an emergency where the registry is unreachable; it is not the normal deploy.
release_mode() {
  echo "${CONFENGE_RELEASE_MODE:-registry}"
}

compose_cmd() {
  local root
  root="$(confenge_repo_root)"
  # Compose maintenance must carry the same immutable decision-audit identity as a full deploy.
  bind_release_identity "${WARMBLY_RELEASE_SHA:-}"
  local envf="${CONFENGE_VPS_ENV:-$root/deploy/confenge-vps/.env}"
  local -a args
  args=(docker compose)
  args+=(-f "$root/docker-compose.yml")
  args+=(-f "$root/deploy/confenge-vps/docker-compose.override.yml")
  if [[ "$(release_mode)" == "registry" ]]; then
    args+=(-f "$root/deploy/confenge-vps/docker-compose.release.yml")
  fi
  if [[ -f "$envf" ]]; then
    args+=(--env-file "$envf")
  fi
  COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-warmbly-confenge}" "${args[@]}" "$@"
}

# Services whose image carries application code. Anything here must exist in the
# registry for the release SHA before production mutates.
release_services() {
  echo "${CONFENGE_RELEASE_SERVICES:-backend consumer worker tracking realtime web}"
}

# Profile-gated services. Their image is pinned the same way, but a missing one
# must not block a deploy of the always-on stack.
release_optional_services() {
  echo "${CONFENGE_RELEASE_OPTIONAL_SERVICES:-admin}"
}

image_prefix() {
  echo "${CONFENGE_IMAGE_PREFIX:-ghcr.io/tjsasakifln/warmbly}"
}

# The Go services pin the minprofile variant; the rest have a single build.
release_image_ref() {
  local svc="$1" sha="${2:-$WARMBLY_RELEASE_SHA}"
  case "$svc" in
    backend|consumer|worker|seed)
      echo "$(image_prefix)/${svc/seed/backend}:${sha}-minprofile" ;;
    *)
      echo "$(image_prefix)/${svc}:${sha}" ;;
  esac
}

disk_guard() {
  bash "$_CONFENGE_VPS_DIR/disk-guard.sh" "$@"
}

api_base() {
  local base="${CONFENGE_API_BASE:-http://${CONFENGE_API_HOST:-127.0.0.1:8080}}"
  base="${base%/}"
  case "$base" in
    http://*|https://*) ;;
    *) base="http://$base" ;;
  esac
  echo "$base"
}

pass_fail() {
  local name="$1" ok="$2"
  if [[ "$ok" == "1" || "$ok" == "true" || "$ok" == "PASS" ]]; then
    printf '%-16s PASS\n' "$name"
  elif [[ "$ok" == "STALE" ]]; then
    printf '%-16s STALE\n' "$name"
  elif [[ "$ok" == "ACTIVE" || "$ok" == "PAUSED" || "$ok" == "OFF" || "$ok" == "ENABLED" ]]; then
    printf '%-16s %s\n' "$name" "$ok"
  else
    printf '%-16s FAIL\n' "$name"
  fi
}

# Session helper: uses the dedicated loopback operator bootstrap first.
ops_access_token() {
  local base email pass login session otp token confirm operator
  base="$(api_base)"
  operator="$(curl -sS -X POST "$base/v1/auth/confenge-operator/session" -H 'Content-Type: application/json' || true)"
  token="$(printf '%s' "$operator" | python3 -c 'import sys,json; print(json.load(sys.stdin).get("access_token",""))' 2>/dev/null || true)"
  if [[ -n "$token" ]]; then
    printf '%s' "$token"
    return 0
  fi

  # Compatibility fallback for installations that have not enabled operator mode.
  email="${CONFENGE_OPS_EMAIL:-dev@warmbly.com}"
  pass="${CONFENGE_OPS_PASSWORD:-}"
  if [[ -z "$pass" ]]; then
    echo "ops_access_token: set CONFENGE_OPS_PASSWORD" >&2
    return 2
  fi
  login="$(curl -sS -X POST "$base/v1/auth/login" -H 'Content-Type: application/json' \
    -d "{\"email\":\"$email\",\"password\":\"$pass\"}" || true)"
  session="$(printf '%s' "$login" | python3 -c 'import sys,json; print(json.load(sys.stdin).get("session",""))' 2>/dev/null || true)"
  token="$(printf '%s' "$login" | python3 -c 'import sys,json; print(json.load(sys.stdin).get("access_token",""))' 2>/dev/null || true)"
  if [[ -n "$token" ]]; then
    printf '%s' "$token"
    return 0
  fi
  if [[ -z "$session" ]]; then
    echo "ops_access_token: login failed" >&2
    return 1
  fi
  sleep 1
  otp="$(python3 - <<'PY'
import json, re, urllib.request
try:
    msgs = json.load(urllib.request.urlopen("http://127.0.0.1:18025/api/v1/messages?limit=8", timeout=3))
except Exception:
    print("")
    raise SystemExit
for m in msgs.get("messages") or []:
    d = json.load(urllib.request.urlopen("http://127.0.0.1:18025/api/v1/message/" + m["ID"], timeout=3))
    codes = re.findall(r"\b(\d{6})\b", (d.get("Text") or "") + (d.get("HTML") or ""))
    if codes:
        print(codes[0])
        break
PY
)"
  if [[ -n "$otp" ]]; then
    confirm="$(curl -sS -X POST "$base/v1/auth/login/confirm" -H 'Content-Type: application/json' \
      -d "{\"email\":\"$email\",\"code\":\"$otp\",\"session\":\"$session\"}" || true)"
    token="$(printf '%s' "$confirm" | python3 -c 'import sys,json; print(json.load(sys.stdin).get("access_token",""))' 2>/dev/null || true)"
  fi
  if [[ -z "$token" ]]; then
    echo "ops_access_token: no access token" >&2
    return 1
  fi
  printf '%s' "$token"
}

tcp_check() {
  local host="$1" port="$2"
  timeout 5 bash -c "echo >/dev/tcp/${host}/${port}" 2>/dev/null
}
