#!/usr/bin/env bash
# Restore a Warmbly CONFENGE backup. Use isolated targets for restore proofs.
# Requires keys already present in deploy/confenge-vps/.env for live recovery.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
# shellcheck source=lib.sh
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"
load_vps_env
cd "$ROOT"

FILE="${1:-${FILE:-}}"
ADAPTER_FILE=""
TMP=""
if [[ -z "$FILE" ]]; then
  echo "Usage: deploy/confenge-vps/restore.sh /path/to/backup.tar.gz" >&2
  echo "   or: FILE=... deploy/confenge-vps/restore.sh" >&2
  exit 2
fi
if [[ ! -f "$FILE" ]]; then
  echo "missing backup file" >&2
  exit 2
fi

cleanup() {
  if [[ -n "$TMP" ]]; then
    rm -rf -- "$TMP"
  fi
}
trap cleanup EXIT

if [[ "$FILE" == *.tar.gz ]]; then
  TMP="$(mktemp -d)"
  while IFS= read -r entry; do
    case "$entry" in
      /*|../*|*/../*)
        echo "archive contains an unsafe path" >&2
        exit 1
        ;;
    esac
  done < <(tar -tzf "$FILE")
  tar -xzf "$FILE" -C "$TMP"
  if [[ ! -f "$TMP/warmbly_dev.sql" || ! -f "$TMP/MANIFEST.json" || ! -f "$TMP/SHA256SUMS" ]]; then
    echo "archive is missing SQL, manifest, or checksums" >&2
    exit 1
  fi
  (cd "$TMP" && sha256sum -c SHA256SUMS)
  python3 - "$TMP/MANIFEST.json" <<'PY'
import json
import sys

manifest = json.load(open(sys.argv[1], encoding="utf-8"))
if manifest.get("format_version") != "confenge.backup-manifest.v2":
    raise SystemExit("unsupported backup manifest version")
postgres = manifest.get("components", {}).get("postgres", {})
if postgres.get("file") != "warmbly_dev.sql":
    raise SystemExit("backup manifest does not identify the PostgreSQL dump")
PY
  FILE="$TMP/warmbly_dev.sql"
  if [[ -f "$TMP/asaas-events.sqlite3" ]]; then
    ADAPTER_FILE="$TMP/asaas-events.sqlite3"
  fi
fi

if ! grep -q '^-- PostgreSQL database dump complete' "$FILE"; then
  echo "restore refused: pg_dump completion marker missing" >&2
  exit 1
fi

TARGET_DATABASE="${CONFENGE_RESTORE_DATABASE:-warmbly_dev}"
if [[ ! "$TARGET_DATABASE" =~ ^[a-zA-Z_][a-zA-Z0-9_]*$ ]]; then
  echo "restore refused: invalid PostgreSQL target name" >&2
  exit 2
fi
if [[ "$TARGET_DATABASE" == "warmbly_dev" ]]; then
  echo "Restoring into warmbly_dev (destructive to current database contents)..."
else
  echo "Restoring into isolated PostgreSQL database $TARGET_DATABASE..."
fi
compose_cmd exec -T postgres pg_isready -U warmbly >/dev/null
TARGET_TABLES="$(
  compose_cmd exec -T postgres psql \
    -U warmbly -d "$TARGET_DATABASE" -Atqc \
    "SELECT count(*) FROM pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema')"
)"
if [[ "$TARGET_TABLES" != "0" ]]; then
  echo "restore refused: PostgreSQL target is not empty" >&2
  exit 1
fi
compose_cmd exec -T postgres psql \
  -U warmbly -d "$TARGET_DATABASE" -v ON_ERROR_STOP=1 <"$FILE"
RESTORED_DATABASE="$(
  compose_cmd exec -T postgres psql \
    -U warmbly -d "$TARGET_DATABASE" -Atqc 'SELECT current_database()'
)"
if [[ "$RESTORED_DATABASE" != "$TARGET_DATABASE" ]]; then
  echo "restore verification failed: PostgreSQL target did not reopen" >&2
  exit 1
fi

if [[ -n "$ADAPTER_FILE" ]]; then
  LIVE_ADAPTER_DB="${ASAAS_ADAPTER_DB:-/var/lib/confenge-asaas-adapter/events.sqlite3}"
  TARGET_ADAPTER_DB="${CONFENGE_RESTORE_ASAAS_DB:-$LIVE_ADAPTER_DB}"
  ASAAS_WAS_ACTIVE=false
  if [[ "$TARGET_ADAPTER_DB" == "$LIVE_ADAPTER_DB" ]] && \
     systemctl is-active --quiet confenge-asaas-adapter.service 2>/dev/null; then
    systemctl stop confenge-asaas-adapter.service
    ASAAS_WAS_ACTIVE=true
  fi
  ASAAS_ADAPTER_DB="$TARGET_ADAPTER_DB" python3 \
    "$ROOT/deploy/confenge-vps/asaas-adapter/adapter.py" restore "$ADAPTER_FILE" \
    >"${TMP:-$(dirname "$TARGET_ADAPTER_DB")}/asaas-restore-proof.json"
  ASAAS_ADAPTER_DB="$TARGET_ADAPTER_DB" python3 \
    "$ROOT/deploy/confenge-vps/asaas-adapter/adapter.py" permissions
  ASAAS_ADAPTER_DB="$TARGET_ADAPTER_DB" python3 \
    "$ROOT/deploy/confenge-vps/asaas-adapter/adapter.py" preflight >/dev/null
  if [[ "$ASAAS_WAS_ACTIVE" == "true" ]]; then
    systemctl start confenge-asaas-adapter.service
  fi
fi

echo "RESTORE_PROOF=PASS postgres_database=$TARGET_DATABASE"
echo "Verify that encryption roots match the backup era before reopening sealed credentials."
