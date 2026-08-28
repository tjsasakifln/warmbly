#!/usr/bin/env bash
# Resume CONFENGE outbound after pause.sh.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
# shellcheck source=lib.sh
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"
load_vps_env
cd "$ROOT"

if ! TOKEN="$(ops_access_token 2>/dev/null)"; then
  echo "REFUSE: operator authentication failed; kill switch remains engaged" >&2
  exit 3
fi
API="$(api_base)"
ORG="${CONFENGE_FEED_SYNC_ORG_ID:-22222222-0000-0000-0000-000000000001}"
curl -fsS -X POST "$API/v1/organization/switch/$ORG" -H "Authorization: Bearer $TOKEN" >/dev/null
CODE="$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$API/v1/confenge/dispatch/resume" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{}' || true)"
echo "governor_resume_http=$CODE"
if [[ "$CODE" != "200" ]]; then
  echo "REFUSE: resume API failed; kill switch remains engaged" >&2
  exit 1
fi

# The authoritative switch is the file on the shared ops volume: the backend
# reads CONFENGE_KILL_SWITCH_PATH=/data/confenge-ops/kill-switch, which is that
# volume. Clearing only the host mirror reported DISPATCH=ACTIVE while every
# deploy preflight pause stayed engaged, so outbound silently stayed dead.
OPS_VOLUME="${COMPOSE_PROJECT_NAME:-warmbly-confenge}_confenge_ops"
if ! compose_cmd exec -T backend sh -c 'rm -f /data/confenge-ops/kill-switch' 2>/dev/null; then
  docker run --rm -v "$OPS_VOLUME:/data" alpine sh -c 'rm -f /data/kill-switch' >/dev/null
fi
if docker run --rm -v "$OPS_VOLUME:/data:ro" alpine test -f /data/kill-switch; then
  echo "REFUSE: transport kill switch still engaged after resume" >&2
  exit 1
fi

HOST_KS="${CONFENGE_KILL_SWITCH_HOST_PATH:-$ROOT/data/confenge-kill-switch}"
rm -f "$HOST_KS"

if [[ "${CONFENGE_SENDING_PAUSED:-false}" == "true" ]]; then
  echo "NOTE: CONFENGE_SENDING_PAUSED=true in env still blocks sending until .env is updated and backend recreated."
fi

echo "DISPATCH=ACTIVE (if env pause flag is false)"
