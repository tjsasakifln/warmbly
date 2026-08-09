#!/usr/bin/env bash
# Resume CONFENGE outbound after pause.sh.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
# shellcheck source=lib.sh
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"
load_vps_env
cd "$ROOT"

compose_cmd exec -T backend sh -c 'rm -f /data/confenge-ops/kill-switch' 2>/dev/null || true

HOST_KS="${CONFENGE_KILL_SWITCH_HOST_PATH:-$ROOT/data/confenge-kill-switch}"
rm -f "$HOST_KS"

if [[ -n "${CONFENGE_OPS_PASSWORD:-}" ]]; then
  if TOKEN="$(ops_access_token 2>/dev/null)"; then
    API="$(api_base)"
    ORG="${CONFENGE_FEED_SYNC_ORG_ID:-22222222-0000-0000-0000-000000000001}"
    curl -sS -X POST "$API/v1/organization/switch/$ORG" -H "Authorization: Bearer $TOKEN" >/dev/null || true
    CODE="$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$API/v1/confenge/dispatch/resume" \
      -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{}' || true)"
    echo "governor_resume_http=$CODE"
  else
    echo "governor_resume=skipped (auth failed; file kill-switch cleared)"
  fi
else
  echo "governor_resume=skipped (file kill-switch cleared)"
fi

if [[ "${CONFENGE_SENDING_PAUSED:-false}" == "true" ]]; then
  echo "NOTE: CONFENGE_SENDING_PAUSED=true in env still blocks sending until .env is updated and backend recreated."
fi

echo "DISPATCH=ACTIVE (if env pause flag is false)"
