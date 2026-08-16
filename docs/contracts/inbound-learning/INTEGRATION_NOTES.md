# INTEGRATION_NOTES (web-cfg)

Warmbly #47 is the residual consumer of web-cfg inbound attribution
and of Market Answer / Contract Analysis / B2G X-Ray pointers.

## What to send

POST the same `confenge.commercial_event.v1` envelope that the
synthetic fixtures use (`internal/app/confenge/intel` `IngestEvent`,
HTTP `POST /confenge/intel/events`, CLI `confenge intel-report`).

Minimum for a real inbound lead:

- `event_id`, `version` or `schema`, `type=lead_received`
- `occurred_at`, `timezone`
- `lead_id` (stable), `correlation_id`
- `source`, `query`, `asset_id`, `cta_id` when known
- `asset_family` when the surface is Market Answer, Contract Analysis,
  or B2G X-Ray, plus `market_answer_id` or `analysis_id`
- `consent` as a pointer, never a raw email/phone/CNPJ
- `pii_pointer` if a PII vault row exists
- `producer_sha`

Follow-up events (`lead_validated`, `handoff_accepted`,
`action_approved`, `action_executed`, `reply`, `meeting`, `proposal`,
`pipeline_created`, `won` / `lost` / `unknown`, `revenue_evidenced`)
must reuse `lead_id` (or `receipt_id`) so they join the first chain.

## What not to send

- contract text, article body, public narrative
- raw email, phone, name, CNPJ in metric fields
- inferred WON/LOST or inferred revenue
- page view / citation / X-Ray completion labeled as a lead

## Authority

| Plane | Owns |
| --- | --- |
| extra-cli | facts, entities, events, evidence, intelligence |
| web-cfg | source, query, asset, CTA, consent, lead attribution |
| Warmbly | approved action, delivery, reply, outcome, next action |
| finance / delivery | revenue and service evidence |

Warmbly never rederives contract, trigger, evidence, public
narrative, revenue, or causal attribution. Learning verdicts
(`REPEAT` / `CHANGE` / `STOP` / `NEED_MORE_DATA`) stay local.
`upstream_writes` is always empty.

## Swap fixture for real event

1. Keep `CONFENGE_AUTO_SEND_ENABLED=false`.
2. POST one real `lead_received` (not `synthetic`, not qa/internal).
3. Query `GET /confenge/intel/report?include_synthetic=0`.
4. Expect the chain to reconstruct from `lead_id`. Latency baseline
   becomes `BASELINE_OBSERVED` only after a non-synthetic event.
5. Do not close Warmbly #47 until a real action/outcome/pipeline or
   an observed rejection exists.

Idempotent replacement: the same `event_id` is a replay. A new real
`event_id` with the same `lead_id` merges onto the first chain.
