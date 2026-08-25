#!/usr/bin/env bash
# Restore Warmbly CONFENGE DB from backup.sh SQL dump.
# Requires keys already present in deploy/confenge-vps/.env (from secrets bundle).
# DESTRUCTIVE to current warmbly_dev contents.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
# shellcheck source=lib.sh
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"
load_vps_env
cd "$ROOT"

FILE="${1:-${FILE:-}}"
ADAPTER_FILE=""
if [[ -z "$FILE" ]]; then
  echo "Usage: deploy/confenge-vps/restore.sh /path/to/warmbly_dev.sql" >&2
  echo "   or: FILE=... deploy/confenge-vps/restore.sh" >&2
  exit 2
fi
if [[ ! -f "$FILE" ]]; then
  echo "missing SQL file: $FILE" >&2
  exit 2
fi

# If archive given, extract SQL
if [[ "$FILE" == *.tar.gz ]]; then
  TMP="$(mktemp -d)"
  tar -xzf "$FILE" -C "$TMP"
  if [[ -f "$TMP/warmbly_dev.sql" ]]; then
    FILE="$TMP/warmbly_dev.sql"
    if [[ -f "$TMP/asaas-events.sqlite3" ]]; then
      ADAPTER_FILE="$TMP/asaas-events.sqlite3"
    fi
  else
    echo "archive missing warmbly_dev.sql" >&2
    exit 1
  fi
fi

echo "Restoring $FILE into warmbly_dev (destructive)..."
compose_cmd exec -T postgres pg_isready -U warmbly >/dev/null
# Drop connections and recreate schema content via psql
compose_cmd exec -T postgres psql -U warmbly -d warmbly_dev -v ON_ERROR_STOP=1 < "$FILE"
echo "Restore complete. Restart backend/worker if needed: deploy/confenge-vps/up.sh"
echo "Verify encryption keys match the dump era or sealed credentials will not decrypt."

if [[ -n "$ADAPTER_FILE" ]]; then
  echo "Restoring durable Asaas transport queue..."
  ASAAS_WAS_ACTIVE=false
  if systemctl is-active --quiet confenge-asaas-adapter.service 2>/dev/null; then
    systemctl stop confenge-asaas-adapter.service
    ASAAS_WAS_ACTIVE=true
  fi
  python3 "$ROOT/deploy/confenge-vps/asaas-adapter/adapter.py" restore "$ADAPTER_FILE"
  python3 "$ROOT/deploy/confenge-vps/asaas-adapter/adapter.py" permissions
  if [[ "$ASAAS_WAS_ACTIVE" == "true" ]]; then
    systemctl start confenge-asaas-adapter.service
  fi
  echo "Asaas queue restore complete; counts are available from its health endpoint."
fi
