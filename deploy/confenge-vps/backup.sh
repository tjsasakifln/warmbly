#!/usr/bin/env bash
# Backup Warmbly CONFENGE operational data without mutating the Asaas queue.
# Never includes Hostinger plaintext password. Never prints secret values.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
# shellcheck source=lib.sh
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"
load_vps_env
cd "$ROOT"

umask 077
TS="$(date -u +%Y%m%dT%H%M%SZ)"
CREATED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
OUT_DIR="${CONFENGE_BACKUP_DIR:-$ROOT/data/backups/confenge-vps}"
SECRETS_DIR="${CONFENGE_SECRETS_BACKUP_DIR:-$OUT_DIR/secrets}"
STAGE="$OUT_DIR/stage-$TS"
ARCHIVE="$OUT_DIR/warmbly-confenge-$TS.tar.gz"
ARCHIVE_TMP="$OUT_DIR/.warmbly-confenge-$TS.tar.gz.tmp"
MANIFEST_SIDECAR="$ARCHIVE.manifest.json"
CHECKSUM_SIDECAR="$ARCHIVE.sha256"
SEC_BUNDLE="$SECRETS_DIR/keys-$TS.env"
SEC_BUNDLE_TMP=""
RECEIPT_TMP=""
COMPLETE=false

if [[ -e "$STAGE" || -e "$ARCHIVE" || -e "$MANIFEST_SIDECAR" || \
      -e "$CHECKSUM_SIDECAR" || -e "$SEC_BUNDLE" ]]; then
  echo "BACKUP=FAIL reason=timestamp output collision" >&2
  exit 1
fi
mkdir -p "$OUT_DIR" "$SECRETS_DIR" "$STAGE"

cleanup() {
  rm -rf -- "$STAGE"
  rm -f -- "$ARCHIVE_TMP"
  if [[ -n "$SEC_BUNDLE_TMP" ]]; then
    rm -f -- "$SEC_BUNDLE_TMP"
  fi
  if [[ -n "$RECEIPT_TMP" ]]; then
    rm -f -- "$RECEIPT_TMP"
  fi
  if [[ "$COMPLETE" != "true" ]]; then
    rm -f -- "$ARCHIVE" "$MANIFEST_SIDECAR" "$CHECKSUM_SIDECAR"
    rm -f -- "$SEC_BUNDLE" "$SEC_BUNDLE.sha256"
  fi
}
trap cleanup EXIT

# Fail before the heavier pg_dump if a present queue is corrupt or unknown.
ASAAS_DB="${ASAAS_ADAPTER_DB:-/var/lib/confenge-asaas-adapter/events.sqlite3}"
if [[ -f "$ASAAS_DB" ]]; then
  echo "Preflighting Asaas queue schema (read-only)..."
  ASAAS_ADAPTER_DB="$ASAAS_DB" python3 \
    "$ROOT/deploy/confenge-vps/asaas-adapter/adapter.py" preflight \
    >"$STAGE/asaas-preflight.json"
fi

echo "Backing up postgres (warmbly_dev)..."
PG_DUMP_VERSION="$(compose_cmd exec -T postgres pg_dump --version | head -1)"
compose_cmd exec -T postgres pg_dump \
  -U warmbly --no-owner --no-privileges warmbly_dev \
  >"$STAGE/warmbly_dev.sql"
if ! grep -q '^-- PostgreSQL database dump complete' "$STAGE/warmbly_dev.sql"; then
  echo "POSTGRES_BACKUP=FAIL reason=pg_dump completion marker missing" >&2
  exit 1
fi

# sqlite3.Connection.backup reads a WAL-consistent snapshot from a mode=ro
# connection. It never initializes Queue or writes backup metadata to the source.
if [[ -f "$ASAAS_DB" ]]; then
  ASAAS_ADAPTER_DB="$ASAAS_DB" python3 \
    "$ROOT/deploy/confenge-vps/asaas-adapter/adapter.py" backup \
    "$STAGE/asaas-events.sqlite3" >"$STAGE/asaas-backup-proof.json"
fi

# Copy encryption roots into a separate 0600 bundle, never into the archive.
SEC_BUNDLE_TMP="$(mktemp "$SECRETS_DIR/.keys-$TS.XXXXXX")"
{
  echo "# CONFENGE VPS key material backup $TS"
  echo "# Store offline and encrypted, separately from SQL dumps."
  for k in KMS_LOCAL_MASTER_KEY CREDENTIALS_ENCRYPTION_KEY AUTH_SECRET INTERNAL_API_TOKEN CONFENGE_OUTCOME_WEBHOOK_SECRET; do
    v="${!k:-}"
    if [[ -n "$v" ]]; then
      printf '%s=%s\n' "$k" "$v"
    fi
  done
} >"$SEC_BUNDLE_TMP"
chmod 600 "$SEC_BUNDLE_TMP"

# Snapshot configuration while removing secrets, credentials, and operator PII.
CONFIG_SOURCE="${CONFENGE_VPS_ENV:-$ROOT/deploy/confenge-vps/.env}"
python3 - "$CONFIG_SOURCE" "$STAGE/env.redacted" <<'PY'
import re
import sys

source, destination = sys.argv[1], sys.argv[2]
sensitive_keys = re.compile(
    r"(PASS|SECRET|TOKEN|KEY|CREDENTIAL|KMS_|AUTH|BEARER|PRIVATE|"
    r"DATABASE_URL|PRIMARY_DB|DSN|COOKIE|SESSION)",
    re.I,
)
pii_keys = re.compile(r"(EMAIL|MAILBOX_NAME|USER_ID|ORG_ID|USERNAME)", re.I)
userinfo = re.compile(r"([a-z][a-z0-9+.-]*://)[^/@\s]+@", re.I)
url_query = re.compile(r"[a-z][a-z0-9+.-]*://[^\s?]+\?", re.I)
email_value = re.compile(r"[^\s@=]+@[^\s@]+")

try:
    lines = open(source, encoding="utf-8").read().splitlines()
except FileNotFoundError:
    open(destination, "w", encoding="utf-8").write("# no .env present\n")
    raise SystemExit(0)

output = ["# CONFENGE configuration snapshot with secrets and PII redacted"]
for line in lines:
    stripped = line.strip()
    if not stripped or stripped.startswith("#"):
        continue
    if "=" not in line:
        continue
    key, _, value = line.partition("=")
    if (
        sensitive_keys.search(key)
        or pii_keys.search(key)
        or email_value.search(value)
        or url_query.search(value)
    ):
        output.append(f"{key}=***REDACTED***")
        continue
    redacted_value = userinfo.sub(r"\1***REDACTED***@", value)
    output.append(f"{key}={redacted_value}")
open(destination, "w", encoding="utf-8").write("\n".join(output) + "\n")
PY

GIT_SHA="$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || echo unknown)"
python3 - "$STAGE" "$CREATED_AT" "$GIT_SHA" "$PG_DUMP_VERSION" <<'PY'
import hashlib
import json
import sys
from pathlib import Path

stage = Path(sys.argv[1])
created_at, git_sha, pg_dump_version = sys.argv[2:5]

def component(name):
    path = stage / name
    return {
        "file": name,
        "sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
        "size_bytes": path.stat().st_size,
    }

components = {
    "postgres": component("warmbly_dev.sql"),
    "redacted_config": component("env.redacted"),
    "encryption_roots": {
        "included_in_archive": False,
        "separate_bundle_required": True,
    },
}
component_files = ["env.redacted", "warmbly_dev.sql"]
proof_path = stage / "asaas-backup-proof.json"
if proof_path.is_file():
    proof = json.loads(proof_path.read_text(encoding="utf-8"))
    components["asaas_queue"] = {
        **component("asaas-events.sqlite3"),
        "included": True,
        "schema": proof["schema"],
        "schema_version": proof["schema_version"],
        "sqlite_version": proof["sqlite_version"],
        "table_counts": proof["table_counts"],
        "queue_state_counts": proof["queue_state_counts"],
    }
    components["asaas_backup_proof"] = component("asaas-backup-proof.json")
    components["asaas_preflight"] = component("asaas-preflight.json")
    component_files.extend(
        ["asaas-backup-proof.json", "asaas-events.sqlite3", "asaas-preflight.json"]
    )
else:
    components["asaas_queue"] = {"included": False, "reason": "source_absent"}

manifest = {
    "format_version": "confenge.backup-manifest.v2",
    "created_at": created_at,
    "git_sha": git_sha,
    "versions": {"pg_dump": pg_dump_version},
    "components": components,
    "excluded": [
        "plaintext_mailbox_password",
        "node_modules",
        "build_cache",
        "unbounded_logs",
        "extra_cli_datalake",
    ],
}
(stage / "MANIFEST.json").write_text(
    json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8"
)
component_files.append("MANIFEST.json")
checksums = [
    f"{hashlib.sha256((stage / name).read_bytes()).hexdigest()}  {name}"
    for name in sorted(component_files)
]
(stage / "SHA256SUMS").write_text("\n".join(checksums) + "\n", encoding="utf-8")
PY

tar -C "$STAGE" -czf "$ARCHIVE_TMP" .
chmod 600 "$ARCHIVE_TMP"
install -m 600 "$STAGE/MANIFEST.json" "$MANIFEST_SIDECAR"
ARCHIVE_SHA256="$(sha256sum "$ARCHIVE_TMP" | awk '{print $1}')"
printf '%s  %s\n' "$ARCHIVE_SHA256" "$(basename "$ARCHIVE")" >"$CHECKSUM_SIDECAR"
chmod 600 "$CHECKSUM_SIDECAR"

mv "$SEC_BUNDLE_TMP" "$SEC_BUNDLE"
SEC_BUNDLE_TMP=""
SEC_SHA256="$(sha256sum "$SEC_BUNDLE" | awk '{print $1}')"
printf '%s  %s\n' "$SEC_SHA256" "$(basename "$SEC_BUNDLE")" >"$SEC_BUNDLE.sha256"
chmod 600 "$SEC_BUNDLE" "$SEC_BUNDLE.sha256"
mv "$ARCHIVE_TMP" "$ARCHIVE"

# Keep health freshness outside SQLite so the queue database stays unchanged.
if [[ -f "$ASAAS_DB" ]]; then
  RECEIPT_PATH="${ASAAS_ADAPTER_BACKUP_RECEIPT:-$ASAAS_DB.backup-receipt.json}"
  RECEIPT_TMP="$(mktemp "$(dirname "$RECEIPT_PATH")/.asaas-backup-receipt.XXXXXX")"
  python3 - "$RECEIPT_TMP" "$CREATED_AT" "$ARCHIVE_SHA256" "$STAGE/asaas-backup-proof.json" <<'PY'
import json
import sys

destination, created_at, archive_sha256, proof_path = sys.argv[1:5]
proof = json.load(open(proof_path, encoding="utf-8"))
receipt = {
    "format_version": "confenge.asaas-backup-receipt.v1",
    "created_at": created_at,
    "archive_sha256": archive_sha256,
    "queue_schema": proof["schema"],
}
open(destination, "w", encoding="utf-8").write(
    json.dumps(receipt, sort_keys=True) + "\n"
)
PY
  chmod 644 "$RECEIPT_TMP"
  mv "$RECEIPT_TMP" "$RECEIPT_PATH"
  RECEIPT_TMP=""
fi
COMPLETE=true

echo "Backup archive: $ARCHIVE"
echo "Manifest: $MANIFEST_SIDECAR"
echo "Archive checksum: $CHECKSUM_SIDECAR"
echo "Secrets bundle: $SEC_BUNDLE (0600; store separately)"
echo "Excluded: node_modules, build cache, unlimited logs, extra-cli datalake, plaintext mailbox password"
