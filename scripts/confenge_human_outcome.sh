#!/usr/bin/env bash
# Record one founder action/outcome on the shipped human-outcome path.
# Does not invent IDs. Does not enable auto-send. WON/LOST/receita need evidence.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
if [ -f .env.confenge ]; then set -a; # shellcheck disable=SC1091
  . ./.env.confenge
  set +a
fi

BASE="${CONFENGE_API_BASE:-http://127.0.0.1:8080}"
TOKEN="${WARMBLY_API_TOKEN:-${CONFENGE_OPERATOR_TOKEN:-}}"
ACTION="${1:-}"
LEAD="${2:-}"
ENVELOPE="${3:-EXTRA}"

if [ -z "$ACTION" ]; then
  cat <<'EOF'
Usage:
  scripts/confenge_human_outcome.sh <action> [lead_id] [envelope]

Actions:
  attempted reached not_reached routed wrong_route reply
  meeting_scheduled meeting_held follow_up disqualified
  proposal_emitted won lost revenue_received

Envelopes: EXTRA ACCOUNT_1 ACCOUNT_2 ACCOUNT_3
Leave lead_id empty until a real id exists. Do not invent ACCOUNT_1 as an id.

WON/LOST require:
  HUMAN_EVIDENCE_REF=evidence:... HUMAN_CONFIRMED=true
Receita requires:
  HUMAN_REVENUE_DOCUMENT_ID=... HUMAN_REVENUE_CENTS=... HUMAN_CONFIRMED=true
EOF
  exit 2
fi

if [ -z "$TOKEN" ]; then
  echo "WARMBLY_API_TOKEN unset. Cannot POST. Template only."
  echo "ACTION=$ACTION LEAD=${LEAD:-EMPTY} ENVELOPE=$ENVELOPE"
  exit 2
fi

KEY="${HUMAN_IDEMPOTENCY_KEY:-envelope:${ENVELOPE}:${ACTION}:${LEAD:-unbound}}"
BODY=$(python3 - <<PY
import json, os
body = {
  "envelope_id": os.environ.get("ENVELOPE", "$ENVELOPE"),
  "idempotency_key": os.environ.get("KEY", "$KEY"),
  "action": "$ACTION",
}
lead = """$LEAD""".strip()
if lead:
    body["lead_id"] = lead
if os.environ.get("HUMAN_ACCOUNT_ID"):
    body["account_id"] = os.environ["HUMAN_ACCOUNT_ID"]
if os.environ.get("HUMAN_FOLLOW_UP_AT"):
    body["follow_up_at"] = os.environ["HUMAN_FOLLOW_UP_AT"]
if os.environ.get("HUMAN_EVIDENCE_REF"):
    body["evidence_ref"] = os.environ["HUMAN_EVIDENCE_REF"]
if os.environ.get("HUMAN_REVENUE_DOCUMENT_ID"):
    body["revenue_document_id"] = os.environ["HUMAN_REVENUE_DOCUMENT_ID"]
if os.environ.get("HUMAN_REVENUE_CENTS"):
    body["revenue_cents"] = int(os.environ["HUMAN_REVENUE_CENTS"])
if os.environ.get("HUMAN_CONFIRMED", "").lower() in ("1", "true", "yes"):
    body["human_confirmed"] = True
print(json.dumps(body))
PY
)

echo "POST $BASE/api/v1/confenge/intel/human-outcomes action=$ACTION envelope=$ENVELOPE lead=${LEAD:-EMPTY}"
curl -sS -D- -X POST "$BASE/api/v1/confenge/intel/human-outcomes" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $KEY" \
  --data "$BODY"
