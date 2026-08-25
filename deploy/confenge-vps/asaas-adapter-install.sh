#!/usr/bin/env bash
# Install/update the versioned Asaas adapter. Does not create a webhook or charge.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
ENV_FILE="/etc/confenge/asaas-adapter.env"
UNIT="confenge-asaas-adapter.service"

install -d -m 0700 /etc/confenge
if [[ ! -f "$ENV_FILE" ]]; then
  install -m 0600 "$ROOT/deploy/confenge-vps/asaas-adapter.env.example" "$ENV_FILE"
  echo "Fill $ENV_FILE before starting $UNIT" >&2
  exit 2
fi
chmod 0600 "$ENV_FILE"
install -m 0644 "$ROOT/deploy/confenge-vps/systemd/$UNIT" "/etc/systemd/system/$UNIT"
systemctl daemon-reload
systemctl enable "$UNIT"
systemctl restart "$UNIT"
systemctl --no-pager --full status "$UNIT"
echo "ASAAS_ADAPTER_SHA=$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || echo unknown)"
