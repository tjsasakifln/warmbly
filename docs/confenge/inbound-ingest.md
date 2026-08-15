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
  "source": "web-cfg",
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

`lead_id` or `receipt_id` is required. Optional fields stay empty. Missing
facts stay `UNKNOWN`. Warmbly does not invent a name, role, email, or phone.

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

`GET /confenge/cockpit` includes `inbound_now`. Each row has empresa, pessoa
when known, origem, asset, contrato/contexto, por que agora, acao
recomendada, canal, evidencias, owner or `UNKNOWN`, idade, status, proxima
acao.

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

- web-cfg must POST this contract (`OPS_WEBHOOK_URL` today is a thinner
  Slack-style payload). Until that pointer exists, Monday-queue arrival
  of a live confenge.com.br lead stays ops work.
- extra-cli has no per-lead live lookup in this repo. Enrichment uses
  already-imported material or stays `UNKNOWN`.
- A staging/prod synthetic lead is safe only with send disabled and the
  inbound secret configured.
