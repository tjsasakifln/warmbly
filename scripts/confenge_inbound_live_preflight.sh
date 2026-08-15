#!/usr/bin/env bash
# Prove (or refuse to claim) a live confenge.inbound.v1 POST.
# Does not send commercial contact. Does not enable auto-send.
#
# Exit 0: one SYNTHETIC-INBOUND POST returned 201/200 and replay was 200.
# Exit 2: UNPROVEN. Printed steps are the remaining human actions.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
if [ -f .env.confenge ]; then set -a; # shellcheck disable=SC1091
  . ./.env.confenge
  set +a
fi

SECRET="${CONFENGE_INBOUND_WEBHOOK_SECRET:-}"
URL="${CONFENGE_INBOUND_WEBHOOK_URL:-}"
ORG="${CONFENGE_INBOUND_ORG_ID:-${CONFENGE_OPERATOR_ORG_ID:-}}"
AUTO="${CONFENGE_AUTO_SEND_ENABLED:-}"
LEAD_ID="${CONFENGE_INBOUND_SYNTHETIC_LEAD_ID:-SYNTHETIC-INBOUND-$(date -u +%Y%m%dT%H%M%SZ)}"

echo "INBOUND_PREFLIGHT=start"
echo "HEAD_HINT=$(git rev-parse HEAD 2>/dev/null || echo unknown)"
echo "URL=${URL:-UNSET}"
echo "SECRET_SET=$([ -n "$SECRET" ] && echo true || echo false)"
echo "ORG=${ORG:-UNSET}"
echo "CONFENGE_AUTO_SEND_ENABLED=${AUTO:-UNSET}"

HEALTH_URL=""
if [ -n "$URL" ]; then
  HEALTH_URL="${URL%/}/health"
  if [[ "$URL" == *"/inbound" ]]; then
    HEALTH_URL="${URL}/health"
  fi
  echo "HEALTH_URL=$HEALTH_URL"
  HEALTH_BODY="$(mktemp)"
  HEALTH_CODE=$(curl -sS -o "$HEALTH_BODY" -w '%{http_code}' --connect-timeout 5 --max-time 10 "$HEALTH_URL" || echo 000)
  echo "HEALTH_HTTP=$HEALTH_CODE"
  echo "HEALTH_BODY=$(tr '\n' ' ' < "$HEALTH_BODY" | head -c 300)"
  rm -f "$HEALTH_BODY"
fi

blocker=""
if [ -z "$URL" ]; then blocker="${blocker}CONFENGE_INBOUND_WEBHOOK_URL unset. "; fi
if [ -z "$SECRET" ]; then blocker="${blocker}CONFENGE_INBOUND_WEBHOOK_SECRET unset. "; fi
if [ -z "$ORG" ]; then blocker="${blocker}CONFENGE_INBOUND_ORG_ID/CONFENGE_OPERATOR_ORG_ID unset. "; fi
if [ "$AUTO" = "true" ] || [ "$AUTO" = "1" ]; then
  blocker="${blocker}CONFENGE_AUTO_SEND_ENABLED is on; refuse live POST. "
fi

if [ -z "$URL" ]; then
  echo
  echo "UNPROVEN: no inbound URL in this environment."
  echo "api.warmbly.com does not resolve from typical operator laptops."
  echo "VPS/operator API is loopback (127.0.0.1). A secret with no public URL is UNPROVEN."
fi

if [ -n "$blocker" ]; then
  echo
  echo "VERDICT=UNPROVEN"
  echo "BLOCKERS=$blocker"
  echo
  echo "Remaining human actions (in order):"
  echo "1. On the Warmbly process that should receive inbound, set:"
  echo "     CONFENGE_INBOUND_WEBHOOK_SECRET=<shared HMAC>"
  echo "     CONFENGE_INBOUND_ORG_ID=<dest org uuid>   # or CONFENGE_OPERATOR_ORG_ID"
  echo "     CONFENGE_AUTO_SEND_ENABLED=false"
  echo "   Restart that process on the exact SHA. Do not merge or change DNS."
  echo "2. On the caller (this shell or Netlify), set:"
  echo "     CONFENGE_INBOUND_WEBHOOK_URL=https://<host>/api/v1/webhooks/confenge/inbound"
  echo "     CONFENGE_INBOUND_WEBHOOK_SECRET=<same shared HMAC>"
  echo "   Host must be reachable from here. Loopback 127.0.0.1:8080 only works on that host."
  echo "3. Re-run: scripts/confenge_inbound_live_preflight.sh"
  echo "4. After 201/200, open INBOUND NOW (GET /confenge/cockpit or the dashboard) and find lead_id."
  echo "5. Replay is automatic in this script. Do not contact the synthetic lead."
  echo
  echo "Manual curl once env is set (script will do this for you):"
  echo "  BODY='{\"lead_id\":\"$LEAD_ID\",\"receipt_id\":\"$LEAD_ID\",\"source\":\"CONFENGE_WEB\",\"company\":\"SYNTHETIC-INBOUND\",\"email\":\"synthetic-inbound@example.com\",\"message\":\"SYNTHETIC-INBOUND do not contact\"}'"
  echo "  T=\$(date +%s)"
  echo "  SIG=\$(printf '%s.%s' \"\$T\" \"\$BODY\" | openssl dgst -sha256 -hmac \"\$SECRET\" | awk '{print \$2}')"
  echo "  curl -sS -D- -X POST \"\$URL\" -H 'Content-Type: application/json' -H \"X-Warmbly-Signature: t=\$T,v1=\$SIG\" -H \"Idempotency-Key: $LEAD_ID\" --data \"\$BODY\""
  echo
  echo "Do not POST through web-cfg form: synthetic/qa records are SKIPPED before Warmbly."
  echo "Do not point OPS_WEBHOOK_URL at this path."
  exit 2
fi

if ! command -v openssl >/dev/null 2>&1; then
  echo "VERDICT=UNPROVEN"
  echo "BLOCKERS=openssl missing; cannot sign HMAC here."
  exit 2
fi

BODY=$(printf '%s' "{\"lead_id\":\"${LEAD_ID}\",\"receipt_id\":\"${LEAD_ID}\",\"source\":\"CONFENGE_WEB\",\"company\":\"SYNTHETIC-INBOUND\",\"email\":\"synthetic-inbound@example.com\",\"message\":\"SYNTHETIC-INBOUND do not contact\"}")
sign() {
  local t="$1"
  local mac
  mac=$(printf '%s.%s' "$t" "$BODY" | openssl dgst -sha256 -hmac "$SECRET" | awk '{print $2}')
  printf 't=%s,v1=%s' "$t" "$mac"
}

BODY_FILE="$(mktemp)"
cleanup() { rm -f "$BODY_FILE"; }
trap cleanup EXIT

post() {
  local t
  t=$(date +%s)
  curl -sS -o "$BODY_FILE" -w '%{http_code}' \
    --connect-timeout 8 --max-time 20 \
    -X POST "$URL" \
    -H 'Content-Type: application/json' \
    -H 'Accept: application/json' \
    -H "X-Warmbly-Signature: $(sign "$t")" \
    -H "Idempotency-Key: ${LEAD_ID}" \
    --data "$BODY"
}

echo "LEAD_ID=$LEAD_ID"
echo "POST 1 (create or duplicate)..."
CODE1=$(post) || CODE1="000"
echo "HTTP1=$CODE1"
echo "BODY1=$(tr '\n' ' ' < "$BODY_FILE" | head -c 400)"
if [ "$CODE1" != "201" ] && [ "$CODE1" != "200" ]; then
  echo "VERDICT=UNPROVEN"
  echo "BLOCKERS=first POST HTTP $CODE1 (want 201 or 200). URL may be loopback-only, DNS missing, or secret/org wrong."
  exit 2
fi

echo "POST 2 (same lead_id replay)..."
CODE2=$(post) || CODE2="000"
echo "HTTP2=$CODE2"
echo "BODY2=$(tr '\n' ' ' < "$BODY_FILE" | head -c 400)"
if [ "$CODE2" != "200" ]; then
  echo "VERDICT=UNPROVEN"
  echo "BLOCKERS=replay HTTP $CODE2 (want 200 duplicate). Do not treat as live."
  exit 2
fi

echo "VERDICT=LIVE_WEBHOOK"
echo "NOTE=synthetic/qa/internal persist but stay SKIPPED from commercial INBOUND NOW."
echo "NEXT=confirm receipt persisted for lead_id=$LEAD_ID. Do not treat it as a commercial lead. Do not contact."
echo "Cockpit: GET /confenge/cockpit (JWT) or dashboard #inbound-agora"
exit 0
