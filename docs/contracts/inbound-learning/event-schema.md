# inbound-learning event schema

Warmbly consumes `confenge.commercial_event.v1`. This is not a CRM
object and not an extra-cli or web-cfg truth record.

Goals 05-07 schemas are not published in this repo. When sibling
repos emit matching types, keep these field names and document any
mapping here. Do not silently rename.

## Envelope

| Field | Authority | Notes |
| --- | --- | --- |
| `event_id` | producer | Replay key |
| `version` / `schema` | producer | `confenge.commercial_event.v1` |
| `type` | producer | see types below |
| `occurred_at` | producer clock | required |
| `ingested_at` | Warmbly | set on ingest if omitted |
| `timezone` | producer | IANA name |
| `correlation_id` | web-cfg | attribution join |
| `idempotency_key` | producer | same key is a replay |
| `asset_family` | web-cfg | `market_answer` / `contract_analysis` / `b2g_xray` |
| `market_answer_id` | web-cfg | pointer only |
| `analysis_id` | web-cfg | pointer only |
| `source` / `referrer` / `query` / `intent_class` | web-cfg | no PII |
| `cta_id` / `offer_id` / `route` | web-cfg / Warmbly | IDs |
| `account_public_id` / `entity_public_id` | extra-cli | public refs; first account wins |
| `consent` / `suppression` | web-cfg | refs, not raw PII |
| `actor_ref` / `evidence_ref` | producer | pointers |
| `outcome_state` | Warmbly / human | WON/LOST need human_confirmed |
| `producer_sha` | producer | build identity |
| `pii_pointer` | producer | never copied into metrics |
| `lead_id` / `receipt_id` | web-cfg / Warmbly inbound | chain identity |
| `action_id` / `outcome_id` | Warmbly | execution IDs |
| `revenue_cents` + `revenue_document_id` | finance | both required to evidence |

## Types

`lead_received`, `lead_validated`, `lead_rejected`,
`handoff_accepted`, `handoff_exception`, `action_approved`,
`action_executed`, `reply`, `outcome_observed`, `meeting`,
`proposal`, `pipeline_created`, `pipeline_updated`, `won`, `lost`,
`unknown`, `revenue_evidenced`, `learning_candidate`, plus the
non-lead observations `xray_completed`, `page_view`, `citation` and
the late `correction`.

## What is not inferred

- page view, citation, X-Ray completion, and lead are not pipeline
- pipeline is not revenue
- WON/LOST without `human_confirmed` stay `UNKNOWN`
- `causal_proof` is always false
- contract text, trigger narrative, and public article copy are not stored
