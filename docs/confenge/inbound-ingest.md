# Inbound ingest contract `confenge.inbound.v1`

web-cfg is the public capture authority. Warmbly receives the durable
receipt and turns it into one commercial action. extra-cli stays the
fact/identity/reachability authority.

PII is not accepted on the query string. Send the body.

## Endpoint

`POST /api/v1/webhooks/confenge/inbound`

Auth: `X-Warmbly-Signature: t=<unix>,v1=<hex(hmac_sha256(secret, "<unix>." + body))>`

| Setting | Purpose |
| --- | --- |
| `CONFENGE_INBOUND_WEBHOOK_SECRET` | HMAC shared secret |
| `CONFENGE_INBOUND_ORG_ID` | Destination org (falls back to `CONFENGE_OPERATOR_ORG_ID`) |
| `CONFENGE_AUTO_SEND_ENABLED` | Must stay `false` |

`Idempotency-Key` is optional. The durable key is `lead_id` / `receipt_id`.

## Body

```json
{
  "lead_id": "webcfg-...",
  "receipt_id": "rcpt-...",
  "created_at": "2026-08-14T14:55:00Z",
  "source": "CONFENGE_WEB",
  "route_family": "inbound",
  "asset_id": "landing-segunda-leitura",
  "cta_id": "segunda-leitura-contrato",
  "landing_url": "https://confenge.com.br/contratos/norte",
  "contract_public_id": "CTR-NORTE-88",
  "entity_public_id": "extra-cli-account-id",
  "cnpj": "00.000.000/0001-00",
  "company": "Razao social",
  "name": "Nome fornecido pelo lead",
  "email": "lead@empresa.com",
  "phone": "+5541999999999",
  "consent": {
    "granted": true,
    "preferred_channel": "phone",
    "dnc": false
  },
  "utm": { "source": "google", "medium": "cpc" },
  "referrer": "https://www.google.com/",
  "message": "Quero uma segunda leitura do contrato",
  "correlation_id": "attr-..."
}
```

Shipped web-cfg (`mapLeadToInboundV1`) emits `source=CONFENGE_WEB` and omits
empty optionals. `source=web-cfg` is still accepted (no allowlist). `lead_id`
or `receipt_id` is required. Missing facts stay `UNKNOWN`. Warmbly does not
invent a name, role, email, or phone.

## Dedupe

1. Same `lead_id` / receipt: return the existing action. No second action.
2. Same account identity + contact inside 24h: persist the new event, keep
   one commercial action, set `dedupe_of_lead_id`.
3. A later distinct event is kept.

Enrichment failure does not drop the receipt. `enrichment_status` stays
`UNKNOWN`, `FAILED`, or `UNAVAILABLE`.

## Classification (facts only)

| Observed facts | `next_action` |
| --- | --- |
| DNC | `SUPPRESSED` (fail-closed) |
| Spam / explicit suppress | `SUPPRESSED` |
| Insufficient identity | `NEEDS_ENRICHMENT` |
| Valid phone + contract reread | `CALL` or `WHATSAPP` |
| High intent + extra-cli `VALIDATED` email | `SEND_EMAIL` in review, never auto-send |
| Known person, no direct channel | `ROUTED_CALL` / `MANUAL_OUTREACH` |
| Generic mailbox | manual, never named-human |

Email stays fail-closed: `VALIDATED` + `READY` + human review. Inbound
never dispatches.

## Cockpit

`GET /confenge/cockpit` includes `inbound_now`. Each row has `lead_id`,
`receipt_id`, empresa, pessoa when known, origem, asset, contrato/contexto,
por que agora, acao recomendada, canal, evidencias, owner or `UNKNOWN`,
idade, status, enrichment, latency stamps, proxima acao. It is a human
queue card, not a sendable message (`dispatchable=false`).

`POST /confenge/inbound/:leadId/outcome` records a human outcome.

## Latency (baseline only)

`lead_created_at`, `warmbly_ingested_at`, `enrichment_completed_at`,
`owner_assigned_at`, `first_action_at`, `conversation_at`, `proposal_at`,
`close_at`. No minute SLA is defined.

## Outcomes

Warmbly remains the authority. Export is `confenge.outcome.v1` keyed by
`source_lead_id` / idempotency key. extra-cli is not a second ledger.

Recordable: `CONTACTED`, `QUALIFIED_CONVERSATION`, `MEETING`, `PROPOSAL`,
`WON`/`CLIENT`, `LOST`, `FOLLOW_UP`, `DNC`, `NO_RESPONSE`. WON is never
inferred.

## Real blockers

- web-cfg PR #72 (merged) POSTs this contract from `CONFENGE_INBOUND_WEBHOOK_URL`
  after persist. `OPS_WEBHOOK_URL` is a different Slack-style `confenge.lead`
  HMAC. Do not retarget it. Until the two inbound env vars are set on both
  Netlify and the Warmbly process, capture stays local and INBOUND NOW stays
  empty.
- extra-cli has no per-lead live lookup in this repo. Enrichment uses
  already-imported material or stays `UNKNOWN`. FAILED/UNAVAILABLE receipts
  stay in the queue.
- A staging/prod synthetic lead is safe only with send disabled and the
  inbound secret configured. POST the Warmbly webhook directly. web-cfg skips
  `synthetic` / `qa` / `spam` / `internal` before the handoff.

## Live verdict (this tree)

**UNPROVEN.** Code and shipped-path tests exist. A production or VPS
synthetic POST has not been observed from this environment.

Probed 2026-08-15 from the implementer host against origin/main
`dab3ea3446348d1adcd76c6d5c98eba9dade1498`:

- `CONFENGE_INBOUND_WEBHOOK_SECRET` unset
- `CONFENGE_INBOUND_ORG_ID` unset
- `CONFENGE_INBOUND_WEBHOOK_URL` unset
- `api.warmbly.com` does not resolve
- `https://app.warmbly.com/api/v1/webhooks/confenge/inbound` OPTIONS 404
- `https://warmbly.com/api/v1/webhooks/confenge/inbound` OPTIONS 405
- `127.0.0.1:8080` connection refused
- no `.env.confenge` with a real secret

`make confenge-preflight` now surfaces `inbound_secret` / `inbound_org` /
`inbound_ready`. `GET /confenge/status` `readiness.inbound` is `ready` only
when secret + dest org are set. That is configuration, not a live POST.

## Human proof (remaining)

```bash
# 1. Warmbly process: set secret + dest org, keep auto-send off, restart on this SHA.
# 2. Caller: set CONFENGE_INBOUND_WEBHOOK_URL + the same secret.
scripts/confenge_inbound_live_preflight.sh
# 3. Open INBOUND NOW and confirm the printed lead_id. Do not contact.
```

A 201/200 from that script is a live webhook receipt. It is not a commercial
send and not a web-cfg form proof.
