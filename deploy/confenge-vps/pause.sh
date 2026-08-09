#!/usr/bin/env bash
# Emergency kill switch: pause CONFENGE outbound on the VPS.
# Works without extra-cli / AI. Prefer durable governor API; always engage file switch.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
# shellcheck source=lib.sh
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"
load_vps_env
cd "$ROOT"

REASON="${1:-operator_ssh_pause}"

# 1) File kill-switch on shared ops volume (backend + worker see it after remount/checks)
compose_cmd exec -T backend sh -c \
  'mkdir -p /data/confenge-ops && printf "paused\nreason=%s\n" "'"$REASON"'" > /data/confenge-ops/kill-switch' \
  2>/dev/null || true

# Also write host-side mirror for offline inspection
HOST_KS="${CONFENGE_KILL_SWITCH_HOST_PATH:-$ROOT/data/confenge-kill-switch}"
mkdir -p "$(dirname "$HOST_KS")"
printf 'paused\nreason=%s\nat=%s\n' "$REASON" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$HOST_KS"
chmod 644 "$HOST_KS"

# 2) Governor API pause when auth available (durable in Postgres)
if [[ -n "${CONFENGE_OPS_PASSWORD:-}" ]]; then
  if TOKEN="$(ops_access_token 2>/dev/null)"; then
    API="$(api_base)"
    ORG="${CONFENGE_FEED_SYNC_ORG_ID:-22222222-0000-0000-0000-000000000001}"
    curl -sS -X POST "$API/v1/organization/switch/$ORG" -H "Authorization: Bearer $TOKEN" >/dev/null || true
    CODE="$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$API/v1/confenge/dispatch/pause" \
      -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
      -d "{\"reason\":\"$REASON\"}" || true)"
    echo "governor_pause_http=$CODE"
  else
    echo "governor_pause=skipped (auth failed; file kill-switch engaged)"
  fi
else
  echo "governor_pause=skipped (set CONFENGE_OPS_PASSWORD for API path; file kill-switch engaged)"
fi

echo "DISPATCH=PAUSED reason=$REASON"
echo "Resume with: deploy/confenge-vps/resume.sh"
