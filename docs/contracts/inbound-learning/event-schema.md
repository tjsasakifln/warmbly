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
| `account_public_id` / `entity_public_id` | extra-cli | public refs |
| `consent` / `suppression` | web-cfg | refs, not raw PII |
| `actor_ref` / `evidence_ref` | producer | pointers |
| `outcome_state` | Warmbly / human | WON/LOST need human_confirmed |
| `producer_sha` | producer | build identity |
| `pii_pointer` | producer | never copied into metrics |
| `lead_id` / `receipt_id` | web-cfg / Warmbly inbound | chain identity |
| `action_id` / `outcome_id` | Warmbly | execution IDs |
| `revenue_cents` + `revenue_document_id` | finance | both required to evidence |
| `offer_version` / `terms_version` / `scope_version` | web-cfg | producer aliases; copied onto the offer snapshot |
| `amount_cents` / `currency` | web-cfg | snapshot only; not received revenue |
| `external_reference` / `provider_event_id` | web-cfg / Asaas | replay and join when `lead_id` is absent |
| `provider_raw_status` / `canonical_status` | producer | raw preserved; canonical never erases evidence |
| `financial_confirmation` / `received_revenue` / `revenue` | producer claims | never cash authority. `revenue` is always false on create |
| `exception_code` | producer | held on the exception queue |

HMAC ingest: `POST /api/v1/webhooks/confenge/inbound` with the same envelope.

## Types

`lead_received`, `lead_validated`, `lead_rejected`,
`handoff_accepted`, `handoff_exception`, `action_approved`,
`action_executed`, `reply`, `outcome_observed`, `meeting`,
`proposal`, `pipeline_created`, `pipeline_updated`, `won`, `lost`,
`unknown`, `revenue_evidenced`, `learning_candidate`, plus the
non-lead observations `xray_completed`, `page_view`, `citation` and
the late `correction`. Offer/capacity/payment types from web-cfg
`scripts/offers/events.cjs`: `offer_viewed`, `offer_selected`,
`eligibility_submitted`, `capacity_approved`, `capacity_rejected`,
`capacity_waitlisted`, `terms_accepted`, `checkout_created`,
`payment_created`, `payment_pending`, `payment_confirmed`,
`payment_received`, `payment_overdue`, `payment_refunded`,
`payment_unknown`, `subscription_active`, `subscription_ended`,
`subscription_canceled`, `onboarding_started`, `service_activated`,
`renewal_due`, `renewed`, `commercial_exception` /
`commercial_exception_opened` / `commercial_exception_resolved`.

## What is not inferred

- page view, citation, X-Ray completion, and lead are not pipeline
- pipeline is not revenue
- WON/LOST without `human_confirmed` stay `UNKNOWN`
- `causal_proof` is always false
- contract text, trigger narrative, and public article copy are not stored
