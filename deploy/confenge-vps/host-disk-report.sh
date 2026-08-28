#!/usr/bin/env bash
# Read-only shared-host storage report. Deletes nothing, ever.
#
# Warmbly is a minority tenant on this root filesystem. Any automatic cleanup
# is scoped to artifacts Warmbly created and can reconstruct; everything else is
# reported so a human can decide, because "large" is not the same as "safe to
# delete" and co-tenant outputs have loss semantics we do not own.
set -euo pipefail

TOP="${CONFENGE_HOST_REPORT_TOP:-12}"

echo "== filesystem =="
df -h / | sed -n '1p;2p'
echo

echo "== largest trees =="
du -shx /var/lib/* /opt/* 2>/dev/null | sort -rh | head -n "$TOP"
echo

echo "== docker =="
docker system df 2>/dev/null || echo "docker unavailable"
echo

echo "== classification =="
cat <<'TXT'
Warmbly-owned, bounded automatically by this pack:
  /var/lib/docker builder cache   reconstructible  disk-guard.sh retain (daily timer)
  dangling images                 reconstructible  disk-guard.sh retain
  superseded warmbly release imgs reconstructible  disk-guard.sh retain, keeps current + previous
  data/backups/confenge-vps       BACKUP           backup.sh keeps newest CONFENGE_BACKUP_KEEP (default 10)

Warmbly-owned, deliberately never touched:
  docker named volumes            PERSISTENT       postgres data, confenge_ops, confenge_keys, blobs
  data/GeoLite2-City.mmdb         runtime input    bind-mounted by the backend
  ops/feed-tls                    credential       private CA for the same-host feed

Co-tenant, NOT touched by any Warmbly automation. Retention is the owning
stack's decision; this report only surfaces size and count:
  /var/lib/extra-consultoria      unknown semantics; largest consumer on the host
  /var/lib/postgresql             host Postgres data (not the Warmbly container's)
  /opt/extra-consultoria-releases release trees, one per SHA; likely duplicated across releases
  /opt/confenge-plane             feed/plane payloads
  /opt/*-releases                 per-SHA checkouts; count grows without bound
  /swapfile, /swapfile2           fixed allocation, not reclaimable by cleanup
TXT
echo

echo "== per-SHA release trees (count and size; nothing is removed) =="
for d in /opt/*-releases; do
  [[ -d "$d" ]] || continue
  printf '%-45s %6s entries  %s\n' "$d" "$(find "$d" -mindepth 1 -maxdepth 1 | wc -l)" "$(du -shx "$d" 2>/dev/null | cut -f1)"
done
