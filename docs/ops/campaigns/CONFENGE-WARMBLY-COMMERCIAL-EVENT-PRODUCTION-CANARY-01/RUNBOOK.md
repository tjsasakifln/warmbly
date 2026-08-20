# Runbook — re-sign and POST SYNTHETIC `confenge.commercial_event.v1`

Authorized host: Netcup `v2202607385716487230` `/opt/warmbly-confenge`.
Do not enable auto-send. Do not charge. Do not insert SQL rows.

## Read health / SHA / migrations

```bash
ssh ec-prod 'cat /opt/warmbly-confenge/.deployed_sha; git -C /opt/warmbly-confenge rev-parse HEAD'
curl -sS http://127.0.0.1:8080/api/v1/webhooks/confenge/inbound/health
curl -sS https://api.confenge.com.br/api/v1/webhooks/confenge/inbound/health
docker exec warmbly-confenge-postgres-1 psql -U warmbly -d warmbly_dev -c 'select version, dirty from schema_migrations'
```

Expect: SHA `93dd039d7b9b310458beff8a6bd8819a61da6399`, health READY, `auto_send_enabled=false`, `accepted_event_versions` contains `confenge.commercial_event.v1`, migrations `106` dirty `f`.

## Sign and POST (loopback, secret stays on the VPS)

HMAC: `X-Warmbly-Signature: t=<unix>,v1=<hex(hmac_sha256(secret, "<unix>." + body))>`.
Invalid `t=1,v1=deadbeef` must be 401.

```bash
# on the VPS, with CONFENGE_INBOUND_WEBHOOK_SECRET already in deploy/confenge-vps/.env
source /opt/warmbly-confenge/deploy/confenge-vps/.env
URL=http://127.0.0.1:8080/api/v1/webhooks/confenge/inbound
BODY='{"schema":"confenge.commercial_event.v1","version":"confenge.commercial_event.v1","event_id":"evt_SYNTHETIC_...","type":"offer_selected","occurred_at":"2026-08-20T16:00:00Z","offer_id":"CFG-DIAG-EXP-v1","offer_version":"v1","terms_version":"CFG-TERMS-B2B-2026-08-17-v1","external_reference":"SYNTHETIC-...","provider_event_id":"asaas_SYNTHETIC_...","amount_cents":800000,"currency":"BRL","source":"CONFENGE_WEB","revenue":false,"synthetic":true}'
T=$(date +%s)
SIG=$(printf '%s.%s' "$T" "$BODY" | openssl dgst -sha256 -hmac "$CONFENGE_INBOUND_WEBHOOK_SECRET" | awk '{print $2}')
curl -sS -D- -X POST "$URL" -H 'Content-Type: application/json' -H "X-Warmbly-Signature: t=$T,v1=$SIG" --data "$BODY"
# replay: same BODY, new T/SIG -> HTTP 200 replay=true
```

Minimum sequence (same `external_reference`, unique `event_id` / `provider_event_id`):
`offer_selected` → `eligibility_submitted` → `capacity_approved` → `terms_accepted` → `checkout_created` → `payment_pending` → `payment_received`.

Mark every body `"synthetic": true`. IDs must contain `SYNTHETIC`.

## Executive default excludes synthetic

```bash
# operator session on loopback
TOKEN=$(curl -sS -X POST http://127.0.0.1:8080/v1/auth/confenge-operator/session -H 'Content-Type: application/json' | python3 -c 'import sys,json; print(json.load(sys.stdin)["access_token"])')
curl -sS -H "Authorization: Bearer $TOKEN" 'http://127.0.0.1:8080/v1/confenge/intel/executive?include_synthetic=0'
# CFG-DIAG-EXP-v1 from this canary must be absent
curl -sS -H "Authorization: Bearer $TOKEN" 'http://127.0.0.1:8080/v1/confenge/intel/executive?include_synthetic=1'
# by_offer_version includes CFG-DIAG-EXP-v1
curl -sS -H "Authorization: Bearer $TOKEN" 'http://127.0.0.1:8080/v1/confenge/inbound'
# default INBOUND NOW has no commercial-event lead
```

## Store unavailable

Do not take production Postgres down. The shipped path is `IngestCommercialEvent` → `JoinUnavailable` → HTTP 503. Health remains distinct from 401.
