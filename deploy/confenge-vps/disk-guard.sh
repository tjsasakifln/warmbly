#!/usr/bin/env bash
# CONFENGE VPS disk safety: preflight, bounded cleanup, retention.
#
# Postgres and Docker share the root filesystem. A deploy that eats the last
# free gigabytes stops Postgres extending files and the backend enters a
# migration restart loop, so the measurement runs BEFORE production mutates and
# fails the deploy instead of the database.
#
# Usage:
#   disk-guard.sh report
#   disk-guard.sh preflight [release-sha]
#   disk-guard.sh retain [release-sha]
set -euo pipefail

# Deploy may consume this much; Postgres headroom below is never part of the budget.
DEPLOY_BUDGET_GB="${CONFENGE_DISK_DEPLOY_BUDGET_GB:-20}"
PG_RESERVED_GB="${CONFENGE_DISK_PG_RESERVED_GB:-20}"
MIN_FREE_PCT="${CONFENGE_DISK_MIN_FREE_PCT:-12}"
WARN_FREE_GB="${CONFENGE_DISK_WARN_FREE_GB:-80}"
# Build cache is fully reconstructible: capped in steady state, dropped when a
# deploy would otherwise not fit.
BUILDER_CACHE_MAX_GB="${CONFENGE_BUILDER_CACHE_MAX_GB:-8}"
BUILDER_CACHE_MAX_AGE="${CONFENGE_BUILDER_CACHE_MAX_AGE:-168h}"
BUILDER_CACHE_EMERGENCY_GB="${CONFENGE_BUILDER_CACHE_EMERGENCY_GB:-0}"
# Current release plus this many previous releases stay pullable-free for rollback.
RELEASE_KEEP="${CONFENGE_RELEASE_KEEP:-2}"
IMAGE_PREFIX="${CONFENGE_IMAGE_PREFIX:-ghcr.io/tjsasakifln/warmbly}"

_dg_root() { cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd; }

release_state_file() {
  local dir="${CONFENGE_RELEASE_STATE_DIR:-/var/lib/confenge}"
  if mkdir -p "$dir" 2>/dev/null && [[ -w "$dir" ]]; then
    echo "$dir/release-history"
  else
    echo "$(_dg_root)/deploy/confenge-vps/.release-history"
  fi
}

docker_root() {
  local root
  root="$(docker info --format '{{.DockerRootDir}}' 2>/dev/null || true)"
  [[ -n "$root" && -d "$root" ]] && { echo "$root"; return; }
  echo /
}

# df in MB so a sub-GB reading never rounds to zero.
_df_field() {
  df -P -k "$1" 2>/dev/null | awk 'NR==2 {print $2, $3, $4}'
}

fs_free_mb()  { local f; f="$(_df_field "$1")"; echo $(( $(echo "$f" | awk '{print $3}') / 1024 )); }
fs_total_mb() { local f; f="$(_df_field "$1")"; echo $(( $(echo "$f" | awk '{print $1}') / 1024 )); }

free_floor_mb() { echo $(( (DEPLOY_BUDGET_GB + PG_RESERVED_GB) * 1024 )); }

docker_usage() {
  docker system df 2>/dev/null || echo "docker system df unavailable"
}

builder_cache_bytes() {
  # `docker buildx du` totals are human strings; the reclaimable line is enough
  # for reporting and the prune below is what actually bounds the cache.
  docker buildx du 2>/dev/null | awk '/^Total:/ {print $2}' | tail -1
}

report() {
  local path free_mb total_mb pct_free floor_mb
  path="$(docker_root)"
  free_mb="$(fs_free_mb "$path")"
  total_mb="$(fs_total_mb "$path")"
  floor_mb="$(free_floor_mb)"
  pct_free=0
  [[ "$total_mb" -gt 0 ]] && pct_free=$(( free_mb * 100 / total_mb ))
  echo "DISK_PATH=$path"
  echo "DISK_TOTAL_GB=$(( total_mb / 1024 ))"
  echo "DISK_FREE_GB=$(( free_mb / 1024 ))"
  echo "DISK_FREE_PCT=$pct_free"
  echo "DISK_USED_PCT=$(( 100 - pct_free ))"
  echo "DISK_REQUIRED_FREE_GB=$(( floor_mb / 1024 ))"
  echo "DISK_PG_RESERVED_GB=$PG_RESERVED_GB"
  echo "DISK_DEPLOY_BUDGET_GB=$DEPLOY_BUDGET_GB"
  echo "BUILDER_CACHE_TOTAL=$(builder_cache_bytes)"
  echo "BUILDER_CACHE_MAX_GB=$BUILDER_CACHE_MAX_GB"
  if [[ "$free_mb" -lt "$floor_mb" ]]; then
    echo "DISK_STATE=CRITICAL"
  elif [[ "$free_mb" -lt $(( WARN_FREE_GB * 1024 )) ]]; then
    echo "DISK_STATE=WARN"
  else
    echo "DISK_STATE=OK"
  fi
  echo "--- docker system df ---"
  docker_usage
}

# Reconstructible artifacts only. Named volumes, Postgres data, the CONFENGE ops
# and key volumes, and blobs are never arguments to anything below: this script
# contains no `volume rm`, no `volume prune`, and no `system prune`.
clean_reconstructible() {
  local keep_gb="${1:-$BUILDER_CACHE_MAX_GB}" sha="${2:-}"
  echo "cleanup: builder cache -> drop older than ${BUILDER_CACHE_MAX_AGE}, then cap at ${keep_gb}GB"
  # Two passes, in this order. Combining them into one call makes the age filter
  # gate the size cap: entries younger than the age are then exempt from the cap
  # and the cache sits above it indefinitely, which is what an earlier version
  # did (17.83 GB against an 8 GB cap after a sweep reported success).
  docker builder prune --force --filter "until=${BUILDER_CACHE_MAX_AGE}" >/dev/null 2>&1 || true
  docker builder prune --force --keep-storage "${keep_gb}GB" >/dev/null 2>&1 || true
  echo "cleanup: dangling images (daemon refuses any image a container references)"
  docker image prune --force >/dev/null 2>&1 || true
  prune_stale_release_images "$sha"
}

# Delete Warmbly release images outside the keep set. `docker image rm` without
# -f refuses an image any container references, so the running release and the
# rollback release cannot be removed even if the keep set were wrong.
prune_stale_release_images() {
  local current="${1:-}" keep line repo tag sha
  keep=" $current "
  if [[ -f "$(release_state_file)" ]]; then
    while read -r line; do
      [[ -n "$line" ]] && keep+="$line "
    done < <(tail -n "$RELEASE_KEEP" "$(release_state_file)" | awk '{print $1}')
  fi
  while read -r repo tag; do
    [[ -z "$repo" || "$tag" == "<none>" ]] && continue
    sha="${tag%-minprofile}"
    [[ "$sha" == *[!0-9a-f]* || ${#sha} -ne 40 ]] && continue
    if [[ "$keep" == *" $sha "* ]]; then continue; fi
    echo "cleanup: removing superseded release image $repo:$tag"
    docker image rm "$repo:$tag" >/dev/null 2>&1 || true
  done < <(docker image ls --format '{{.Repository}} {{.Tag}}' 2>/dev/null | grep -F "$IMAGE_PREFIX/" || true)
}

record_release() {
  local sha="$1" file
  file="$(release_state_file)"
  printf '%s %s\n' "$sha" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >>"$file"
  # Bounded history; the file is the rollback keep list, not an audit log.
  tail -n 20 "$file" >"$file.tmp" && mv "$file.tmp" "$file"
}

preflight() {
  local sha="${1:-}" path free_mb total_mb floor_mb pct_free
  path="$(docker_root)"
  floor_mb="$(free_floor_mb)"
  echo "== disk preflight =="
  report
  free_mb="$(fs_free_mb "$path")"
  total_mb="$(fs_total_mb "$path")"
  pct_free=0
  [[ "$total_mb" -gt 0 ]] && pct_free=$(( free_mb * 100 / total_mb ))

  if [[ "$free_mb" -ge "$floor_mb" && "$pct_free" -ge "$MIN_FREE_PCT" ]]; then
    echo "DISK_PREFLIGHT=PASS free=$(( free_mb / 1024 ))GB floor=$(( floor_mb / 1024 ))GB"
    return 0
  fi

  echo "DISK_PREFLIGHT=RECLAIM free=$(( free_mb / 1024 ))GB below floor=$(( floor_mb / 1024 ))GB"
  clean_reconstructible "$BUILDER_CACHE_EMERGENCY_GB" "$sha"

  free_mb="$(fs_free_mb "$path")"
  pct_free=0
  [[ "$total_mb" -gt 0 ]] && pct_free=$(( free_mb * 100 / total_mb ))
  if [[ "$free_mb" -ge "$floor_mb" && "$pct_free" -ge "$MIN_FREE_PCT" ]]; then
    echo "DISK_PREFLIGHT=PASS after reclaim free=$(( free_mb / 1024 ))GB"
    return 0
  fi

  echo "REFUSE: insufficient disk headroom for a safe deploy." >&2
  echo "  free=$(( free_mb / 1024 ))GB free_pct=${pct_free}% required=$(( floor_mb / 1024 ))GB (${DEPLOY_BUDGET_GB}GB deploy + ${PG_RESERVED_GB}GB Postgres reserve, min ${MIN_FREE_PCT}%)" >&2
  echo "  No service was recreated. Reclaim space by hand and retry; persistent volumes were not touched." >&2
  return 4
}

retain() {
  local sha="${1:-}"
  echo "== retention sweep =="
  [[ -n "$sha" ]] && record_release "$sha"
  clean_reconstructible "$BUILDER_CACHE_MAX_GB" "$sha"
  report
}

case "${1:-report}" in
  report)    report ;;
  preflight) preflight "${2:-}" ;;
  retain)    retain "${2:-}" ;;
  *) echo "usage: disk-guard.sh {report|preflight|retain} [release-sha]" >&2; exit 2 ;;
esac
