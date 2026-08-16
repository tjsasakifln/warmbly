#!/usr/bin/env bash
# Poll public DNS for api.confenge.com.br. When the DNS-only A record
# answers this VPS, run inbound-edge-install.sh so certbot can mint TLS.
# Stops itself after the leaf exists. Does not invent DNS credentials.
set -euo pipefail

HOST_NAME="${CONFENGE_INBOUND_PUBLIC_HOST:-api.confenge.com.br}"
PUBLIC_IP="${CONFENGE_INBOUND_PUBLIC_IP:-159.195.18.88}"
CERT_LIVE="/etc/letsencrypt/live/${HOST_NAME}/fullchain.pem"
INSTALLER="${CONFENGE_INBOUND_INSTALLER:-/opt/warmbly-confenge/deploy/confenge-vps/inbound-edge-install.sh}"

if [[ "$(id -u)" -ne 0 ]]; then
  echo "FAIL inbound-edge-wait-dns must run as root" >&2
  exit 1
fi

if [[ -f "$CERT_LIVE" ]]; then
  echo "OK cert already present"
  exit 0
fi

points_here() {
  python3 - <<PY
import socket, sys
want = "${PUBLIC_IP}"
try:
    addrs = {i[4][0] for i in socket.getaddrinfo("${HOST_NAME}", None, socket.AF_INET)}
except OSError:
    sys.exit(1)
sys.exit(0 if want in addrs else 1)
PY
}

if points_here; then
  echo "DNS ${HOST_NAME} -> ${PUBLIC_IP}; running inbound-edge-install.sh"
  exec "$INSTALLER"
fi

echo "WAIT ${HOST_NAME} does not yet resolve to ${PUBLIC_IP}"
exit 2
