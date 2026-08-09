#!/usr/bin/env bash
# Start or update the isolated warmbly-confenge stack (always-on).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
# shellcheck source=lib.sh
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"
load_vps_env
cd "$ROOT"

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
if [[ "${CONFENGE_GREEN_AUTORUN_ENABLED:-false}" == "true" ]]; then
  echo "REFUSE: CONFENGE_GREEN_AUTORUN_ENABLED=true is not allowed on VPS execution plane bootstrap" >&2
  exit 3
fi
if [[ "${CONFENGE_AUTO_SEND_ENABLED:-false}" == "true" ]]; then
  echo "REFUSE: CONFENGE_AUTO_SEND_ENABLED=true is not allowed (human approval path only)" >&2
  exit 3
fi
if [[ "${CONFENGE_WHATSAPP_ENABLED:-false}" == "true" ]]; then
  echo "REFUSE: WhatsApp must stay OFF in this PR profile" >&2
  exit 3
fi

echo "Bringing up project=$COMPOSE_PROJECT_NAME ..."
compose_cmd up -d --remove-orphans

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

# Optional seed on first boot
if [[ "${CONFENGE_VPS_SEED:-false}" == "true" ]]; then
  compose_cmd --profile seed run --rm seed || true
fi

echo "Stack up. Operator UI via SSH tunnel (see docs/confenge/vps-execution-plane.md):"
echo "  ssh -L 5173:127.0.0.1:5173 -L 8080:127.0.0.1:8080 -p 2222 root@<vps>"
echo "  open http://127.0.0.1:5173"
echo "Connect Hostinger: deploy/confenge-vps/connect-hostinger.sh"
echo "Status: deploy/confenge-vps/status.sh"
