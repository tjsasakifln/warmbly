#!/usr/bin/env bash
# Public inbound edge probe. No PII. Never prints or stores the HMAC secret.
# Writes node_exporter textfile metrics and a local ALERT flag.
set -euo pipefail

HOST_NAME="${CONFENGE_INBOUND_PUBLIC_HOST:-api.confenge.com.br}"
PUBLIC_IP="${CONFENGE_INBOUND_PUBLIC_IP:-159.195.18.88}"
LOOPBACK_HEALTH="${CONFENGE_INBOUND_LOOPBACK_HEALTH:-http://127.0.0.1:8080/api/v1/webhooks/confenge/inbound/health}"
PUBLIC_HEALTH="https://${HOST_NAME}/api/v1/webhooks/confenge/inbound/health"
ACCESS_LOG="${CONFENGE_INBOUND_ACCESS_LOG:-/var/log/nginx/${HOST_NAME}.access.log}"
METRICS="${CONFENGE_INBOUND_METRICS:-/var/lib/prometheus/node-exporter/confenge_inbound.prom}"
ALERT_FILE="${CONFENGE_INBOUND_ALERT:-/var/lib/confenge-inbound/ALERT}"
STATE_DIR="${CONFENGE_INBOUND_STATE:-/var/lib/confenge-inbound}"

mkdir -p "$STATE_DIR" "$(dirname "$METRICS")"

dns_ok=0
tls_ok=0
health_ready=0
auto_send=0
loopback_ready=0
latency_ms=0
http_code=0

resolved="$(python3 - <<PY
import socket
try:
    addrs = sorted({i[4][0] for i in socket.getaddrinfo("${HOST_NAME}", None, socket.AF_INET)})
    print(",".join(addrs) if addrs else "")
except OSError:
    print("")
PY
)"
if [[ "$resolved" == *"${PUBLIC_IP}"* ]]; then
  dns_ok=1
fi

if [[ "$dns_ok" -eq 1 ]]; then
  tls_out="$(echo | openssl s_client -servername "$HOST_NAME" -connect "${HOST_NAME}:443" -verify_return_error 2>&1 || true)"
  if printf '%s' "$tls_out" | grep -qi 'Verify return code: 0' &&
     printf '%s' "$tls_out" | grep -q "$HOST_NAME"; then
    tls_ok=1
  fi
fi

start_ns="$(date +%s%N)"
body="$(mktemp)"
http_code="$(curl -sS -o "$body" -w '%{http_code}' --max-time 10 "$PUBLIC_HEALTH" 2>/dev/null || true)"
http_code="${http_code:-000}"
end_ns="$(date +%s%N)"
latency_ms=$(( (end_ns - start_ns) / 1000000 ))

status=""
if [[ "$http_code" == "200" ]] && [[ -s "$body" ]]; then
  status="$(python3 - "$body" <<'PY'
import json, sys
try:
    d = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception:
    sys.exit(0)
print(d.get("status", ""))
print("1" if d.get("auto_send_enabled") else "0")
PY
)"
  health_status="$(printf '%s\n' "$status" | sed -n '1p')"
  auto_send="$(printf '%s\n' "$status" | sed -n '2p')"
  auto_send="${auto_send:-1}"
  if [[ "$health_status" == "READY" ]]; then
    health_ready=1
  fi
fi
rm -f "$body"

lb_body="$(mktemp)"
lb_code="$(curl -sS -o "$lb_body" -w '%{http_code}' --max-time 5 "$LOOPBACK_HEALTH" 2>/dev/null || true)"
lb_code="${lb_code:-000}"
if [[ "$lb_code" == "200" ]] && grep -q '"status":"READY"' "$lb_body"; then
  loopback_ready=1
fi
rm -f "$lb_body"

count_status() {
  local code="$1"
  local method="${2:-}"
  if [[ ! -f "$ACCESS_LOG" ]]; then
    echo 0
    return
  fi
  if [[ -n "$method" ]]; then
    awk -v c="$code" -v m="$method" '$2==m && $4==c {n++} END{print n+0}' "$ACCESS_LOG"
  else
    awk -v c="$code" '$4==c {n++} END{print n+0}' "$ACCESS_LOG"
  fi
}

count_class() {
  local prefix="$1"
  if [[ ! -f "$ACCESS_LOG" ]]; then
    echo 0
    return
  fi
  awk -v p="$prefix" '$4 ~ ("^" p) {n++} END{print n+0}' "$ACCESS_LOG"
}

http_4xx="$(count_class 4)"
http_5xx="$(count_class 5)"
hmac_fail="$(count_status 401 POST)"
replay="$(count_status 200 POST)"
created="$(count_status 201 POST)"
rate_limited="$(count_status 429)"

# Latency histogram substitute: last public health sample in ms.
# Access-log request_time is also aggregated as a running max of POST rt.
post_rt_max="$(
  if [[ -f "$ACCESS_LOG" ]]; then
    awk '$2=="POST" {gsub(/^rt=/, "", $6); if ($6+0>m) m=$6+0} END{printf "%.3f", m+0}' "$ACCESS_LOG"
  else
    echo 0
  fi
)"

tmp="${METRICS}.tmp"
cat >"$tmp" <<EOF
# HELP confenge_inbound_dns_ok 1 if ${HOST_NAME} resolves to the VPS public IPv4.
# TYPE confenge_inbound_dns_ok gauge
confenge_inbound_dns_ok ${dns_ok}
# HELP confenge_inbound_tls_ok 1 if the public certificate verifies and SAN includes the hostname.
# TYPE confenge_inbound_tls_ok gauge
confenge_inbound_tls_ok ${tls_ok}
# HELP confenge_inbound_health_ready 1 if public GET /health is HTTP 200 status=READY.
# TYPE confenge_inbound_health_ready gauge
confenge_inbound_health_ready ${health_ready}
# HELP confenge_inbound_auto_send 1 if public health reports auto_send_enabled.
# TYPE confenge_inbound_auto_send gauge
confenge_inbound_auto_send ${auto_send:-0}
# HELP confenge_inbound_loopback_ready 1 if loopback health is READY.
# TYPE confenge_inbound_loopback_ready gauge
confenge_inbound_loopback_ready ${loopback_ready}
# HELP confenge_inbound_health_http_code Last public health HTTP status (0 if unreachable).
# TYPE confenge_inbound_health_http_code gauge
confenge_inbound_health_http_code ${http_code}
# HELP confenge_inbound_health_latency_ms Last public health probe latency.
# TYPE confenge_inbound_health_latency_ms gauge
confenge_inbound_health_latency_ms ${latency_ms}
# HELP confenge_inbound_http_4xx_total Access-log 4xx count (path only, no query/PII).
# TYPE confenge_inbound_http_4xx_total counter
confenge_inbound_http_4xx_total ${http_4xx}
# HELP confenge_inbound_http_5xx_total Access-log 5xx count.
# TYPE confenge_inbound_http_5xx_total counter
confenge_inbound_http_5xx_total ${http_5xx}
# HELP confenge_inbound_hmac_fail_total Access-log POST 401 count (HMAC class).
# TYPE confenge_inbound_hmac_fail_total counter
confenge_inbound_hmac_fail_total ${hmac_fail}
# HELP confenge_inbound_replay_total Access-log POST 200 count (duplicate/replay class).
# TYPE confenge_inbound_replay_total counter
confenge_inbound_replay_total ${replay}
# HELP confenge_inbound_created_total Access-log POST 201 count.
# TYPE confenge_inbound_created_total counter
confenge_inbound_created_total ${created}
# HELP confenge_inbound_rate_limited_total Access-log 429 count.
# TYPE confenge_inbound_rate_limited_total counter
confenge_inbound_rate_limited_total ${rate_limited}
# HELP confenge_inbound_post_rt_max_seconds Max POST request_time seen in the access log.
# TYPE confenge_inbound_post_rt_max_seconds gauge
confenge_inbound_post_rt_max_seconds ${post_rt_max}
EOF
chmod 0644 "$tmp"
mv "$tmp" "$METRICS"

reasons=()
if [[ "$loopback_ready" -ne 1 ]]; then reasons+=("loopback_health_not_ready"); fi
if [[ "$dns_ok" -ne 1 ]]; then reasons+=("dns_missing_or_wrong"); fi
if [[ "$tls_ok" -ne 1 ]]; then reasons+=("tls_unverified"); fi
if [[ "$health_ready" -ne 1 ]]; then reasons+=("public_health_not_ready"); fi
if [[ "${auto_send:-0}" == "1" ]]; then reasons+=("auto_send_enabled"); fi

if [[ ${#reasons[@]} -gt 0 ]]; then
  msg="CONFENGE inbound edge ALERT ${reasons[*]} dns=${resolved:-none} http=${http_code}"
  printf '%s\n' "$msg" >"$ALERT_FILE"
  logger -t confenge-inbound-edge "$msg" || true
  echo "$msg"
  exit 1
fi

rm -f "$ALERT_FILE"
echo "CONFENGE inbound edge OK dns=${resolved} tls=1 health=READY auto_send=0"
exit 0
