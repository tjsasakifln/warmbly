#!/usr/bin/env bash
# Install the bounded Docker GC timer and the host-level BuildKit cache cap.
#
# Without this, nothing bounds Docker growth between deploys and the operator
# has to remember to prune. That is how ec-prod reached 100% root usage with
# ~174 GB of builder cache.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
PACK="$ROOT/deploy/confenge-vps"

BUILDER_KEEP_GB="${CONFENGE_HOST_BUILDER_KEEP_GB:-10}"
DAEMON_JSON="${CONFENGE_DOCKER_DAEMON_JSON:-/etc/docker/daemon.json}"

if [[ "$(id -u)" -ne 0 ]]; then
  echo "REFUSE: run as root (installs systemd units and /etc/docker config)" >&2
  exit 3
fi

echo "== bounded GC timer =="
install -m 0644 "$PACK/systemd/confenge-docker-gc.service" /etc/systemd/system/
install -m 0644 "$PACK/systemd/confenge-docker-gc.timer" /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now confenge-docker-gc.timer
systemctl list-timers confenge-docker-gc.timer --no-pager || true

echo "== host BuildKit cache cap =="
# Host-level, not Warmbly-level: Warmbly, Extra Consultoria and the control
# center share one root filesystem, so the cap has to sit on the daemon or one
# stack's builds can still consume the whole host.
python3 - "$DAEMON_JSON" "$BUILDER_KEEP_GB" <<'PY'
import json, os, sys

path, keep_gb = sys.argv[1], int(sys.argv[2])
cfg = {}
if os.path.exists(path):
    with open(path, encoding="utf-8") as fh:
        text = fh.read().strip()
    if text:
        cfg = json.loads(text)

builder = cfg.setdefault("builder", {})
gc = builder.setdefault("gc", {})
gc["enabled"] = True
gc["defaultKeepStorage"] = f"{keep_gb}GB"
gc["policy"] = [
    {"keepStorage": "2GB", "filter": ["unused-for=24h"]},
    {"keepStorage": f"{keep_gb}GB", "all": True},
]
# Cap container logs host-wide; the Warmbly compose files already do this per
# service, but co-tenant stacks may not.
cfg.setdefault("log-driver", "json-file")
cfg.setdefault("log-opts", {"max-size": "20m", "max-file": "5"})

os.makedirs(os.path.dirname(path), exist_ok=True)
tmp = path + ".confenge-tmp"
with open(tmp, "w", encoding="utf-8") as fh:
    json.dump(cfg, fh, indent=2, sort_keys=True)
    fh.write("\n")
os.replace(tmp, path)
print(f"wrote {path}: builder.gc.defaultKeepStorage={keep_gb}GB")
PY

echo
echo "NOTE: the BuildKit GC policy applies when dockerd next starts. This script"
echo "does not restart Docker, because that would restart every container on the"
echo "host including production. The daily timer bounds the cache meanwhile."
echo "Apply at the next maintenance window with: systemctl restart docker"
