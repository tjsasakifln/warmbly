# Inbound ingest contract `confenge.inbound.v1`

web-cfg is the public capture authority. Warmbly receives the durable
receipt and turns it into one commercial action. extra-cli stays the
fact/identity/reachability authority.

PII is not accepted on the query string. Send the body.

## Endpoint

`POST /api/v1/webhooks/confenge/inbound`

Auth: `X-Warmbly-Signature: t=<unix>,v1=<hex(hmac_sha256(secret, "<unix>." + body))>`

`GET /api/v1/webhooks/confenge/inbound/health` is a public, PII-free,
secret-free probe. It always returns HTTP 200 with `status=READY|BLOCKED`
for the receive gate (web-cfg uses that field), plus an additive
`health_matrix` (`READY|DEGRADED|BLOCKED` per plane) and
`real_event_ready`. Timeout (process unreachable) stays distinct from
POST `401` (HMAC) and POST `5xx` (persist). A lying READY when secret,
dest org, or auto-send are wrong is a defect. `real_event_ready` is false
until money-asset, Netlify env, capture/handoff, attribution keys, and
`public_read` freshness are all observed healthy. Computing it creates no
lead.

Authenticated `GET /confenge/ops/health` returns the same matrix plus
actionable alerts and UNKNOWN-until-measured technical SLOs. Incident
steps: [inbound-incident-runbook.md](./inbound-incident-runbook.md).

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
or `receipt_id` is required. `public_contract_id` is accepted as the
web-cfg store name and stored as `contract_public_id`. `entity_public_id`
is the canonical extra-cli account id. Missing facts stay `UNKNOWN`.
Warmbly does not invent a name, role, email, or phone.

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
`receipt_id`, empresa, pessoa when known, origem, asset, query, CTA,
trigger, oferta, reachability, freshness, confidence, contrato/contexto,
por que agora, acao humana recomendada, canal, evidencias, owner or
`UNKNOWN`, idade, status, enrichment, latency stamps, copy sugerido
sujeito a revisao. Missing facts stay `UNKNOWN`. It is a human queue
card, not a sendable message (`dispatchable=false`). Email is not
required. Synthetic, qa, and internal receipts persist but stay SKIPPED
from the commercial queue.

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

**PUBLIC HTTPS READY.**

Proved 2026-08-16 on the Netcup VPS (`159.195.18.88`) at production SHA
`deabc11e715d508a68ee231376148b52ee4b8aca`:

- Process env: secret + dest org set, `CONFENGE_AUTO_SEND_ENABLED=false`
- Loopback `GET /api/v1/webhooks/confenge/inbound/health` → 200 `READY`, `auto_send_enabled=false`
- Official `scripts/confenge_inbound_live_preflight.sh` against
  `http://127.0.0.1:8080/api/v1/webhooks/confenge/inbound` → `VERDICT=LIVE_WEBHOOK`
  (201 then replay 200). Commercial INBOUND NOW stayed empty.
  `include_synthetic=0` denominators did not increment. One
  `SYNTHETIC-INBOUND-*` row persisted and stayed SKIPPED.
- Loopback POST unsigned / `t=1,v1=deadbeef` → 401. Invalid JSON and
  missing `lead_id` (valid HMAC) → 400. Query `?email=` → 400. Persist
  500 / missing dest org 503 covered by shipped tests at this SHA.
- Host nginx on public `:80` allowlists only the inbound path + health.
  `/admin` and `/confenge` → 404. Query strings → 400. POST burst → 429
  (20/min, burst 10). Health/POST without query → 301 to
  `https://api.confenge.com.br/...` (plaintext webhook is not served).
- Backend remains `127.0.0.1:8080`. Postgres remains `127.0.0.1:15432`.
  SSH remains `2222`. extra-cli remains `:8443`.

`confenge.com.br` is delegated from Hostinger to Cloudflare
(`grannbo.ns.cloudflare.com` / `kai.ns.cloudflare.com`). The canonical
DNS-only record answers publicly:

```text
api.confenge.com.br.  300  IN  A  159.195.18.88
```

Host nginx presents a valid Let's Encrypt certificate for
`api.confenge.com.br`; external health returns HTTP 200 `READY` with
`auto_send_enabled=false`. External unsigned and invalid-signature POSTs both
return 401. No commercial lead was created by these fail-closed probes.
Handoff: [inbound-edge.md](./inbound-edge.md) and GitHub issue #78.

## Live handoff command

```bash
#   CONFENGE_INBOUND_WEBHOOK_URL=https://api.confenge.com.br/api/v1/webhooks/confenge/inbound
#   CONFENGE_INBOUND_WEBHOOK_SECRET=<same as process>
#   CONFENGE_AUTO_SEND_ENABLED=false
scripts/confenge_inbound_live_preflight.sh
# Open INBOUND NOW only to confirm the printed lead_id. Do not contact.
```

A 201/200 from that script is a live webhook receipt. Run it only with the
existing synthetic guard; it is not a commercial send and not a web-cfg form
proof.
