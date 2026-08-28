#!/usr/bin/env bash
# Start or update the isolated warmbly-confenge stack (always-on).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
# shellcheck source=lib.sh
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"
load_vps_env
cd "$ROOT"

# Captured before the private .env is sourced below because it can carry
# release identity written by an earlier deploy.
RELEASE_SHA_RESOLVED="${WARMBLY_RELEASE_SHA:-}"

ENVF="${CONFENGE_VPS_ENV:-$ROOT/deploy/confenge-vps/.env}"
if [[ ! -f "$ENVF" ]]; then
  echo "Missing $ENVF — run deploy/confenge-vps/gen-secrets.sh first" >&2
  exit 2
fi
chmod 600 "$ENVF" || true

export COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-warmbly-confenge}"

# Fail closed on unsafe profile flips
# shellcheck disable=SC1090
set -a; . "$ENVF"; set +a
# The checkout/caller release is authoritative for both image provenance and
# the CONFENGE decision audit. A stale private env value cannot outrank it.
bind_release_identity "$RELEASE_SHA_RESOLVED"
echo "Release SHA for build and decision audit: $WARMBLY_RELEASE_SHA"

if [[ "${CONFENGE_GREEN_AUTORUN_ENABLED:-false}" == "true" ]]; then
  echo "REFUSE: CONFENGE_GREEN_AUTORUN_ENABLED=true is not allowed on VPS execution plane bootstrap" >&2
  exit 3
fi
if [[ "${CONFENGE_AUTO_SEND_ENABLED:-false}" == "true" ]]; then
  echo "REFUSE: CONFENGE_AUTO_SEND_ENABLED=true is not allowed; use the narrow delegated first-touch policy" >&2
  exit 3
fi
if [[ "${CONFENGE_REQUIRE_HUMAN_APPROVAL:-true}" != "true" ]]; then
  echo "REFUSE: CONFENGE_REQUIRE_HUMAN_APPROVAL must stay true on the VPS execution plane" >&2
  exit 3
fi
if [[ "${CONFENGE_WHATSAPP_ENABLED:-false}" == "true" ]]; then
  echo "REFUSE: WhatsApp must stay OFF in this PR profile" >&2
  exit 3
fi
if [[ "${CONFENGE_OPERATOR_MODE:-true}" == "true" ]]; then
  if [[ -z "${CONFENGE_OPERATOR_USER_ID:-}" || -z "${CONFENGE_OPERATOR_ORG_ID:-}" ]]; then
    echo "REFUSE: operator mode requires CONFENGE_OPERATOR_USER_ID and CONFENGE_OPERATOR_ORG_ID" >&2
    exit 3
  fi
  case "${API_PUBLIC_URL:-}" in
    http://127.0.0.1:*|http://localhost:*) ;;
    *) echo "REFUSE: operator mode requires a loopback API_PUBLIC_URL" >&2; exit 3 ;;
  esac
  case "${APP_URL:-}" in
    http://127.0.0.1:*|http://localhost:*) ;;
    *) echo "REFUSE: operator mode requires a loopback APP_URL" >&2; exit 3 ;;
  esac
fi

# ── 1. disk preflight ───────────────────────────────────────────────────────
# Runs before the first mutation of any kind, including creating the ops volume.
# Postgres shares the root filesystem with Docker: when a deploy ate the last
# free gigabytes, the first symptom was Postgres failing to extend files and the
# backend restart-looping on migrations. Refusing here leaves the healthy
# release running and untouched.
if ! disk_guard preflight "$WARMBLY_RELEASE_SHA"; then
  echo "REFUSE: aborting before any service was stopped or recreated" >&2
  exit 4
fi

# ── 2. acquire the release ──────────────────────────────────────────────────
RELEASE_MODE="$(release_mode)"
echo "Release mode: $RELEASE_MODE"
if [[ "$RELEASE_MODE" == "registry" ]]; then
  echo "Pulling immutable images for $WARMBLY_RELEASE_SHA ..."
  if ! compose_cmd pull --quiet $(release_services); then
    echo "REFUSE: could not pull the release images for $WARMBLY_RELEASE_SHA" >&2
    echo "  CI publishes one image per service per commit on main; check that Build and Push succeeded for this SHA." >&2
    echo "  Production was not touched and the current release is still running." >&2
    exit 5
  fi

  # ── 3. verify the artifacts exist and carry this revision ─────────────────
  # A pull that resolved a stale local tag, or an image built from another
  # commit, is caught here rather than after production has been recreated.
  for svc in $(release_services); do
    ref="$(release_image_ref "$svc")"
    if ! docker image inspect "$ref" >/dev/null 2>&1; then
      echo "REFUSE: release image missing after pull: $ref" >&2
      exit 5
    fi
    rev="$(docker image inspect "$ref" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' 2>/dev/null || true)"
    if [[ "$rev" != "$WARMBLY_RELEASE_SHA" ]]; then
      echo "REFUSE: $ref carries revision '${rev:-<none>}', expected $WARMBLY_RELEASE_SHA" >&2
      exit 5
    fi
    echo "  ok $svc -> $ref"
  done

  # Profile-gated extras: pinned the same way, but never a deploy blocker.
  for svc in $(release_optional_services); do
    if compose_cmd --profile "$svc" pull --quiet "$svc" >/dev/null 2>&1; then
      echo "  ok $svc -> $(release_image_ref "$svc")"
    else
      echo "  skip $svc (optional profile image not available for this release)"
    fi
  done
else
  echo "WARNING: CONFENGE_RELEASE_MODE=$RELEASE_MODE compiles on this host."
  echo "  Local production builds are what filled the root filesystem; use registry mode unless GHCR is unreachable."
  compose_cmd build $(release_services)
fi

# ── 4. engage the deploy safety pause ───────────────────────────────────────
# Written before any app or worker container starts, so a new/empty Docker
# volume cannot fail open. Cleared automatically once the release verifies.
OPS_VOLUME="${COMPOSE_PROJECT_NAME:-warmbly-confenge}_confenge_ops"
docker volume create "$OPS_VOLUME" >/dev/null
docker run --rm -v "$OPS_VOLUME:/data" alpine \
  sh -c 'printf "paused\nreason=deploy_preflight\n" > /data/kill-switch && chown 1000:1000 /data/kill-switch && chmod 600 /data/kill-switch' >/dev/null

if [[ "${CONFENGE_VPS_SEED:-false}" == "true" ]]; then
  echo "Preparing first boot with operator mode temporarily disabled..."
  CONFENGE_OPERATOR_MODE=false compose_cmd up -d --no-build postgres redis nats mailpit backend
  for i in $(seq 1 60); do
    if curl -sS -o /dev/null -w '%{http_code}' --max-time 2 "http://127.0.0.1:8080/health" 2>/dev/null | grep -q 200; then
      break
    fi
    sleep 2
    if [[ "$i" -eq 60 ]]; then
      echo "backend not healthy for first-boot seed" >&2
      exit 1
    fi
  done
  CONFENGE_OPERATOR_MODE=false compose_cmd --profile seed run --rm --no-deps seed
fi

# ── 5. recreate the affected services ───────────────────────────────────────
echo "Bringing up project=$COMPOSE_PROJECT_NAME ..."
# --no-build is the guarantee: in registry mode compose cannot quietly fall back
# to compiling here, which is the behaviour that accumulated the builder cache.
compose_cmd up -d --no-build --remove-orphans


# Keep the kill-switch volume private and writable by the backend user (uid 1000).
docker run --rm -v "${COMPOSE_PROJECT_NAME:-warmbly-confenge}_confenge_ops:/data" alpine \
  sh -c "chown 1000:1000 /data && chmod 700 /data" >/dev/null 2>&1 || true

# ── 6. verify backend health ────────────────────────────────────────────────
echo "Waiting for backend health..."
for i in $(seq 1 60); do
  if curl -sS -o /dev/null -w '%{http_code}' --max-time 2 "http://127.0.0.1:8080/health" 2>/dev/null | grep -q 200; then
    echo "backend healthy"
    break
  fi
  sleep 2
  if [[ "$i" -eq 60 ]]; then
    echo "backend not healthy after wait" >&2
    compose_cmd ps
    exit 1
  fi
done

# ── 7. verify Postgres and the exact running release ────────────────────────
echo "Verifying database..."
if ! compose_cmd exec -T postgres pg_isready -U "${POSTGRES_USER:-warmbly}" >/dev/null 2>&1; then
  echo "REFUSE: postgres is not accepting connections after deploy" >&2
  exit 1
fi
echo "  postgres accepting connections"

echo "Verifying running release $WARMBLY_RELEASE_SHA ..."
for svc in backend consumer worker; do
  if ! bash "$ROOT/deploy/verify-release.sh" "$WARMBLY_RELEASE_SHA" "${COMPOSE_PROJECT_NAME}-${svc}-1"; then
    echo "REFUSE: $svc is not running the expected release" >&2
    exit 1
  fi
done

# ── 8. clear the deploy pause immediately ───────────────────────────────────
# Only a switch this deploy wrote is cleared. An operator emergency pause has a
# different reason and survives, so a deploy can never silently re-arm sending
# that a human deliberately stopped. Outbound is then gated by the business
# send window alone, which is the intended steady state at any hour.
KS="$(docker run --rm -v "$OPS_VOLUME:/data:ro" alpine cat /data/kill-switch 2>/dev/null || true)"
if [[ -z "$KS" ]]; then
  echo "DISPATCH_PAUSE=absent"
elif grep -q '^reason=deploy_preflight$' <<<"$KS"; then
  docker run --rm -v "$OPS_VOLUME:/data" alpine sh -c 'rm -f /data/kill-switch' >/dev/null
  if docker run --rm -v "$OPS_VOLUME:/data:ro" alpine test -f /data/kill-switch; then
    echo "REFUSE: deploy pause could not be cleared" >&2
    exit 1
  fi
  echo "DISPATCH_PAUSE=cleared (deploy_preflight)"
else
  echo "DISPATCH_PAUSE=held (operator pause, not this deploy; clear with resume.sh)"
fi

# ── 9. bounded retention, so nobody has to remember to prune ────────────────
disk_guard retain "$WARMBLY_RELEASE_SHA" || echo "WARNING: retention sweep did not complete"

echo "Stack up. Operator UI via SSH tunnel (see docs/confenge/vps-execution-plane.md):"
if [[ "${CONFENGE_OPERATOR_MODE:-true}" == "true" ]]; then
  WEB_CONFIG="$(curl -fsS --max-time 5 "http://127.0.0.1:5173/config.js")"
  if ! grep -Fq 'CONFENGE_OPERATOR_MODE: "true"' <<<"$WEB_CONFIG"; then
    echo "REFUSE: web runtime config did not preserve CONFENGE_OPERATOR_MODE=true" >&2
    exit 1
  fi
  if ! grep -Fq "API_URL: \"${API_PUBLIC_URL}\"" <<<"$WEB_CONFIG"; then
    echo "REFUSE: web runtime API_URL does not match API_PUBLIC_URL=${API_PUBLIC_URL}" >&2
    exit 1
  fi
  echo "operator runtime config verified"
fi
echo "  deploy/confenge-vps/tunnel.sh"
echo "  open http://127.0.0.1:5173"
echo "Connect Hostinger: deploy/confenge-vps/connect-hostinger.sh"
echo "Status: deploy/confenge-vps/status.sh"
