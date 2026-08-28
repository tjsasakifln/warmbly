#!/usr/bin/env bash
# Roll the warmbly-confenge stack onto a release and prove which commit runs.
#
# Replaces the ad-hoc /root/deploy-warmbly.sh: that script ran
# `compose build backend consumer worker` on the VPS, which shipped the whole
# worktree (4.7 GB, mostly data/backups) into BuildKit on every deploy and
# eventually filled the root filesystem. This path pulls CI-built images pinned
# to the release SHA and never compiles here.
#
# Usage: release-deploy.sh [sha|origin/main]
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
# shellcheck source=lib.sh
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"
cd "$ROOT"

TARGET="${1:-origin/main}"

echo "== fetching =="
git fetch --quiet origin
if [[ "$TARGET" == "origin/main" ]]; then
  git checkout --quiet main
  git reset --quiet --hard origin/main
else
  git checkout --quiet "$TARGET"
fi
NEW_SHA="$(git rev-parse HEAD)"
echo "== release $NEW_SHA =="

ENVF="${CONFENGE_VPS_ENV:-$ROOT/deploy/confenge-vps/.env}"
if [[ ! -f "$ENVF" ]]; then
  echo "REFUSE: missing $ENVF" >&2
  exit 2
fi
cp -a "$ENVF" "$ENVF.bak.$(date -u +%Y%m%dT%H%M%SZ)"
if grep -q '^CONFENGE_REPOSITORY_SHA=' "$ENVF"; then
  sed -i "s|^CONFENGE_REPOSITORY_SHA=.*|CONFENGE_REPOSITORY_SHA=$NEW_SHA|" "$ENVF"
else
  printf 'CONFENGE_REPOSITORY_SHA=%s\n' "$NEW_SHA" >>"$ENVF"
fi
chmod 600 "$ENVF"

# up.sh runs the disk preflight, pulls and verifies the pinned images, recreates
# the stack, checks Postgres and the running SHA, and clears its own deploy pause.
WARMBLY_RELEASE_SHA="$NEW_SHA" bash "$ROOT/deploy/confenge-vps/up.sh"

printf '%s\n' "$NEW_SHA" >"$ROOT/.deployed_sha"

echo "== migrations =="
compose_cmd exec -T postgres psql -U "${POSTGRES_USER:-warmbly}" -d "${POSTGRES_DB:-warmbly_dev}" \
  -c 'select version, dirty from schema_migrations' || true

echo "== health =="
curl -sS http://127.0.0.1:8080/health; echo
curl -sS -o /dev/null -w 'ready:%{http_code}\n' http://127.0.0.1:8080/ready
echo "== deployed $NEW_SHA =="
