#!/usr/bin/env bash
# Deterministic CONFENGE min-profile SBOM.
# Usage: scripts/confenge_min_profile_sbom.sh [out.json]
# Re-running the same command on the same tree must yield the same SHA-256.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

OUT="${1:-/dev/stdout}"
TAGS="${GO_MINPROFILE_TAGS:-minprofile}"
BINS="${CONFENGE_MIN_BINS:-./cmd/backend ./cmd/consumer ./cmd/worker ./cmd/confenge}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

{
  echo "{"
  echo "  \"profile\": \"minprofile\","
  echo "  \"go_tags\": \"${TAGS}\","
  echo "  \"go_version\": \"$(go version | tr -d '"')\","
  echo "  \"modules\": ["
  first=1
  # Module list of packages that actually link into the min-profile binaries.
  # Sorted + unique so two runs hash identically.
  for pkg in $BINS; do
    go list -deps -f '{{if .Module}}{{.Module.Path}} {{.Module.Version}}{{end}}' -tags "$TAGS" "$pkg"
  done | awk 'NF && $1 != "github.com/warmbly/warmbly" {print}' | sort -u > "$tmp/mods.txt"
  while read -r path ver; do
    [ -n "$path" ] || continue
    if [ "$first" -eq 1 ]; then first=0; else echo ","; fi
    printf '    {"path": "%s", "version": "%s"}' "$path" "$ver"
  done < "$tmp/mods.txt"
  echo
  echo "  ]"
  echo "}"
} > "$tmp/sbom.json"

# Normalize trailing whitespace; keep stable JSON.
python3 - "$tmp/sbom.json" "$OUT" <<'PY'
import json, sys
src, dest = sys.argv[1], sys.argv[2]
with open(src) as f:
    data = json.load(f)
data["modules"] = sorted(data["modules"], key=lambda m: (m["path"], m["version"]))
text = json.dumps(data, indent=2, sort_keys=True) + "\n"
if dest == "/dev/stdout":
    sys.stdout.write(text)
else:
    with open(dest, "w") as f:
        f.write(text)
PY
