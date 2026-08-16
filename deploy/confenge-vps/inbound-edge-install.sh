#!/usr/bin/env bash
# Install the public HTTPS edge for CONFENGE inbound on the existing VPS.
# Host nginx + certbot only. Does not expose the loopback backend, UI, DB,
# or extra-cli :8443. Does not enable auto-send. Does not invent DNS.
set -euo pipefail

PACK="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh
source "$PACK/lib.sh"

HOST_NAME="${CONFENGE_INBOUND_PUBLIC_HOST:-api.confenge.com.br}"
PUBLIC_IP="${CONFENGE_INBOUND_PUBLIC_IP:-159.195.18.88}"
ACME_ROOT="${CONFENGE_INBOUND_ACME_ROOT:-/var/www/acme}"
NGINX_AVAIL="${CONFENGE_INBOUND_NGINX_AVAIL:-/etc/nginx/sites-available}"
NGINX_ENABL="${CONFENGE_INBOUND_NGINX_ENABLED:-/etc/nginx/sites-enabled}"
CERT_LIVE="/etc/letsencrypt/live/${HOST_NAME}/fullchain.pem"
EMAIL="${CONFENGE_INBOUND_ACME_EMAIL:-ops@confenge.com.br}"
HTTP_SITE="$NGINX_AVAIL/${HOST_NAME}-http"
HTTPS_SITE="$NGINX_AVAIL/${HOST_NAME}-https"

need_root() {
  if [[ "$(id -u)" -ne 0 ]]; then
    echo "FAIL inbound-edge-install must run as root" >&2
    exit 1
  fi
}

install_packages() {
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  # nginx + certbot only. Never postfix/exim.
  apt-get install -y -qq nginx certbot
}

install_files() {
  mkdir -p /etc/nginx/conf.d /etc/nginx/snippets "$ACME_ROOT" \
    /var/lib/confenge-inbound /var/lib/prometheus/node-exporter \
    /etc/systemd/system
  chmod 755 "$ACME_ROOT"

  install -m 0644 "$PACK/nginx/http-params.conf" /etc/nginx/conf.d/confenge-inbound.conf
  install -m 0644 "$PACK/nginx/proxy-params.conf" /etc/nginx/snippets/confenge-inbound-proxy.conf
  install -m 0644 "$PACK/nginx/site-http.conf" "$HTTP_SITE"
  install -m 0644 "$PACK/nginx/site-https.conf" "$HTTPS_SITE"

  # Drop Debian default site so we own :80.
  rm -f "$NGINX_ENABL/default"
  ln -sfn "$HTTP_SITE" "$NGINX_ENABL/${HOST_NAME}-http"

  if [[ -f "$CERT_LIVE" ]]; then
    ln -sfn "$HTTPS_SITE" "$NGINX_ENABL/${HOST_NAME}-https"
  else
    rm -f "$NGINX_ENABL/${HOST_NAME}-https"
  fi

  install -m 0755 "$PACK/inbound-edge-monitor.sh" /usr/local/sbin/confenge-inbound-edge-monitor
  install -m 0755 "$PACK/inbound-edge-wait-dns.sh" /usr/local/sbin/confenge-inbound-edge-wait-dns
  install -m 0644 "$PACK/systemd/confenge-inbound-edge-monitor.service" \
    /etc/systemd/system/confenge-inbound-edge-monitor.service
  install -m 0644 "$PACK/systemd/confenge-inbound-edge-monitor.timer" \
    /etc/systemd/system/confenge-inbound-edge-monitor.timer
  install -m 0644 "$PACK/systemd/confenge-inbound-edge-wait-dns.service" \
    /etc/systemd/system/confenge-inbound-edge-wait-dns.service
  install -m 0644 "$PACK/systemd/confenge-inbound-edge-wait-dns.timer" \
    /etc/systemd/system/confenge-inbound-edge-wait-dns.timer
}

open_http_ports() {
  if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -qi active; then
    ufw allow 80/tcp comment "CONFENGE inbound ACME + HTTPS redirect" || true
    ufw allow 443/tcp comment "CONFENGE inbound TLS" || true
  fi
}

enable_textfile_collector() {
  local def=/etc/default/prometheus-node-exporter
  if [[ -f "$def" ]] && ! grep -q 'collector.textfile.directory' "$def"; then
    if grep -q '^ARGS=""$' "$def"; then
      sed -i 's|^ARGS=""$|ARGS="--collector.textfile.directory=/var/lib/prometheus/node-exporter"|' "$def"
    else
      echo 'ARGS="--collector.textfile.directory=/var/lib/prometheus/node-exporter"' >>"$def"
    fi
    systemctl restart prometheus-node-exporter || true
  fi
}

dns_points_here() {
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

request_cert() {
  if [[ -f "$CERT_LIVE" ]]; then
    echo "OK cert already present for ${HOST_NAME}"
    return 0
  fi
  if ! dns_points_here; then
    echo "SKIP certbot: ${HOST_NAME} does not resolve to ${PUBLIC_IP}"
    return 1
  fi
  certbot certonly --webroot -w "$ACME_ROOT" \
    -d "$HOST_NAME" \
    --agree-tos --non-interactive \
    --email "$EMAIL" \
    --keep-until-expiring
}

reload_nginx() {
  nginx -t
  systemctl enable nginx
  systemctl reload nginx || systemctl restart nginx
}

enable_monitor() {
  systemctl daemon-reload
  systemctl enable --now confenge-inbound-edge-monitor.timer
  if [[ ! -f "$CERT_LIVE" ]]; then
    systemctl enable --now confenge-inbound-edge-wait-dns.timer
  else
    systemctl disable --now confenge-inbound-edge-wait-dns.timer 2>/dev/null || true
  fi
  /usr/local/sbin/confenge-inbound-edge-monitor || true
}

need_root
install_packages
install_files
open_http_ports
enable_textfile_collector
reload_nginx

CERT_OK=0
if request_cert; then
  ln -sfn "$HTTPS_SITE" "$NGINX_ENABL/${HOST_NAME}-https"
  reload_nginx
  CERT_OK=1
fi

enable_monitor

echo "INBOUND_EDGE_HOST=${HOST_NAME}"
echo "INBOUND_EDGE_UPSTREAM=127.0.0.1:8080"
echo "INBOUND_EDGE_CERT=${CERT_OK}"
echo "INBOUND_EDGE_UFW=80,443"
echo "INBOUND_EDGE_SSH_UNCHANGED=2222"
echo "INBOUND_EDGE_AUTO_SEND_UNTOUCHED=true"
