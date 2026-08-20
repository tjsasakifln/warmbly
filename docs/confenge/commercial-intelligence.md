# Commercial intelligence `confenge.commercial_intel.v1`

Warmbly closes the commercial learning loop without becoming a CRM or a
second ledger.

```
source/query/asset
  → inbound lead_id / receipt_id / correlation_id
  → extra-cli account / event / person / version
  → Warmbly action_id / outcome_id / outbox event_id
  → observed association
  → LEARNING CANDIDATE (local only)
```

## Authority

| Plane | Owns |
| --- | --- |
| web-cfg | source, query, asset, CTA, consent, lead attribution |
| extra-cli | facts, entity, person, event, target-fit / activation versions |
| Warmbly | approved action, delivery, human outcome, next action |

UNKNOWN stays UNKNOWN. WON is never inferred. "Came via page X" plus
"won contract Y" is an observed association, not causal proof.

## Join keys

Idempotent identity prefers `lead_id`, then `receipt_id`, then
`action_id`, then outbox `idempotency_key`. Replay of the same IDs
returns the first chain.

Metric keys are SHA-256 of those IDs. Email, phone, name, and company
never enter a metric key.

## Exception queue

`orphan`, `duplicate`, `conflicting_account`, `missing_version`,
`stale_attribution`, plus `out_of_order` (held, not reordered) and
`unconfirmed_won` / `unconfirmed_lost` (stay UNKNOWN). Store unavailable
is fail-closed. Additive IDs (action, then outcome) merge onto the first
chain. Outcomes join only by `lead_id` / `receipt_id` / `action_id` /
`outcome_id`, never by email or CNPJ.

Operators list, open, and filter by type, lane, age, source, and
severity. Each item shows evidence, event history, and a next action, or
stays nominally open. Legal resolutions are only `link`, `defer`,
`reject`, and `mark_external_evidence_required`. They are replay-safe,
record before/after plus actor/reason, and refuse any move that would
invent WON, LOST, revenue, or identity, or overwrite a conflicting
account, or reorder an out-of-order path.

```
GET  /confenge/intel/exceptions
GET  /confenge/intel/exceptions/:id
POST /confenge/intel/exceptions/:id/resolve
```

CLI without a UI:

```
go run ./cmd/confenge intel-exceptions list --fixture --format json
go run ./cmd/confenge intel-exceptions show <id> --fixture
go run ./cmd/confenge intel-exceptions resolve <id> --action defer --actor op --reason "wait" --fixture
```

`--fixture` is the labeled SYNTHETIC set. Live use requires `PRIMARY_DB`.

## Executive view

`GET /confenge/intel/scoreboard?include_synthetic=0` is the seven-stage
inbound truth placar. Stages 1-2 stay BLOCKED until a GSC or URL-index
ingest exists. CTA, lead, conversation, proposal, and receita stay
distinct. Contracted pipeline, MRR, charge created, and cash received
are `separate_metrics`. Default excludes synthetic.

`GET /confenge/intel/organic-scoreboard` and
`GET /confenge/intel/organic-feedback` are the organic learning residual
of this plane (`confenge.organic_attribution.v1`). Source taxonomy is
`organic_search|direct|referral|ai_referral|partner|outbound|unknown`.
Individual GSC queries never join a lead. Discovery layers stay BLOCKED
without a persisted `confenge.search_observation.v1` aggregate. Feedback is
REPEAT/CHANGE/STOP/NEED_MORE_DATA with empty `upstream_writes`. This does
not close #47.

`GET /confenge/intel/executive?month=YYYY-MM&include_synthetic=0`

Monthly payload, families kept separate (`inbound` / `outbound` /
`partner` / `expansion`):

- `inbound_qualified_pipeline`
- `qco`
- `conversations`
- `meetings`
- `proposals`
- `pipeline`
- `won` / `lost` / `unknown`
- breakdowns by `source` / `asset` / `trigger` / `offer` / `route`
- denominators, latency stamps, freshness / version watermarks

`include_synthetic=1` is the labeled fixture path. Real metrics stay
empty/UNKNOWN until a human-approved action exists.

## Learning candidates

Corrections and recorded outcomes emit a local `LEARNING CANDIDATE` for
demand / asset / offer / content / distribution. Status is `PENDING`.
The verdict is exactly `REPEAT` | `CHANGE` | `STOP` | `NEED_MORE_DATA`.
This capability never writes extra-cli, web-cfg, or SmartLic.

## Versioned events

`POST /confenge/intel/events` and `confenge intel-report` consume
`confenge.commercial_event.v1`. Fixtures and future real events share
`IngestEvent`. Asset families `market_answer`, `contract_analysis`, and
`b2g_xray` stay in separate slices. Pipeline opens only on
`pipeline_created` / `pipeline_updated`. Revenue increments only on
`revenue_evidenced` with a document id. Negative or overlapping
durations fail reconciliation. Fixture-only latency is
`BASELINE_SYNTHETIC`. A real event labels `BASELINE_OBSERVED`.

See `docs/contracts/inbound-learning/` and `INTEGRATION_NOTES.md`.

## Offer, capacity, and finance (same plane)

`confenge.commercial_event.v1` also carries offer/capacity/checkout/payment/onboarding
events. Created provider objects are not received revenue. Contracted revenue, MRR,
and received revenue stay separate fields. Capacity limit 50 is `capacity.policy.v1`.
Holds expire in 72h. Diagnóstico one-offs keep SLA 10-15 business days. Extra R$10k
is a private historical exception, never a catalog row. The provider adapter is
sandbox/disabled only. Manual-first writes go through
`POST /confenge/intel/commercial`.

## What this is not

No CRM board, forecast, magic score, auto-send, or second truth plane.
The execution ledger (`MemoryLedger`) and the outcome outbox stay where
they are. This module reads their IDs.
