# CONFENGE acquisition engines

Internal engineering notes for the four-engine acquisition surface. Operator and
backend facing. The customer-visible behavior of these lanes is documented in
`docs/content/docs/guides/confenge-outreach.mdx`; this page records the parts an
operator or an engineer needs and a customer does not: the INTEL_WATCH
replayability boundary, the `GET /confenge/interrupt-budget` contract, and what
engine attribution does and does not currently claim.

## The four engines

| `engine_lane` | Engine | Consent basis |
| --- | --- | --- |
| `outbound_first_touch` | First touch (the fast lane) | Cold outreach |
| `intel_seed` | INTEL_SEED monitor offer | Cold outreach |
| `intel_watch` | INTEL_WATCH subscription mail | Explicit subscription |
| `confenge_web` | Inbound web leads | Inbound |
| `` (empty) | Unattributed | Not claimed by any engine |

The two consent bases never substitute for each other in either direction.
INTEL_SEED is admitted on the cold-outreach basis (DNC, opt-out, bounce,
recipient suppression, commercial authority, target fit) and never reads a
subscription record. INTEL_WATCH is admitted on its subscription record and
never reads cold-outreach admission. Nothing in the repo converts an accepted
INTEL_SEED into a subscription automatically; `CreateOrReactivateSubscription`
has no production caller, so consent can only arrive through an explicit act.

None of the additive lanes takes a dispatch reservation, a touchpoint, a cohort
slot, or any first-touch queue state. Both intelligence lanes send through the
same `FirstTouchTransport` and the same mailbox-resolution rules as first touch.
There is one SMTP implementation in the system.

## INTEL_WATCH replayability boundary

This is the load-bearing caveat on the INTEL_WATCH recovery story. Read it
before treating automatic reprocessing as a guarantee.

**Dedup buys at-most-once. Replayability buys at-least-once. Only the first is
source-independent.**

The delivery ledger keys dedup on `(subscription, event identity, semantic
content hash)`. That property holds no matter what the upstream does: a
duplicate is refused, a replay of an already-delivered notification is a no-op,
and a failure after the irreversible handoff is parked as `AMBIGUOUS` and never
resent.

Automatic resumption of a `PENDING` row is a different property. It is
*completeness*, not safety, and it holds only because of the current event
source. `WATCH_PENDING_HAS_AUTOMATIC_REPROCESSING=YES` means exactly this and
nothing more:

> The reclaim worker automatically resumes PENDING delivery ONLY because the
> current event source (the fixture-backed `EventProducer` in
> `internal/app/confenge/liveintel/events_fixture.go`) is replayable and
> deterministic: it re-emits the same immutable event set on every `Subscribe`,
> so a delivery identity can be re-derived from the source rather than read back
> from the ledger. The ledger deliberately stores delivery identity, not event
> content.

The contract with a real extra-cli producer has **not** been proven cross-repo.
If the real upstream is at-most-once or otherwise non-replayable, a `PENDING`
row whose event is never re-emitted is permanently undelivered, and the reclaim
worker cannot detect it: there is nothing in the ledger to rebuild the message
from. A durable event-payload inbox is required before the reprocessing
guarantee holds against such an upstream.

That inbox is a **deliberate deferred decision** pending cross-repo convergence
with the real extra-cli producer. It is not an oversight and not a known defect.
Nothing in this repo should be read as a guarantee against an at-most-once
upstream, and no comment, docstring, or doc page may be written to imply one.

Boundary stated in code at:

- `internal/app/confenge/liveintel/watch_reclaim_worker.go` (package doc block)
- `internal/app/confenge/liveintel/events_fixture.go` (package doc block and the
  `FixtureEventProducer` doc)

Related bounds, which are separate from the boundary above: a reclaim pass runs
every five minutes while the lane is enabled, parks handoffs whose worker
disappeared, and gives each event its own two-minute budget so one unreachable
provider cannot head-of-line block the events behind it.

## `GET /confenge/interrupt-budget`

The Founder Interrupt Budget. A read-only projection over the open commercial
actions the Control Center already owns, not a second queue and not a new
entity. It reserves nothing, sends nothing, and mutates no row.

Implementation: `internal/app/confenge/interrupt_budget.go`
(`FounderInterruptBudget`), handler `GetConfengeInterruptBudget` in
`internal/api/handler/confenge.go`, route registered in
`internal/api/routes.go`.

### Request

```
GET /api/v1/confenge/interrupt-budget?limit=50
```

Auth and gating, identical to the rest of the read side of the CONFENGE group:

- requires an organization context
- JWT callers need the `view_contacts` organization permission
- API-key callers need the `READ_CONTACTS` API permission
- the CONFENGE feature gate applies (`requireEnabled`), so a disabled
  installation gets the standard disabled error rather than an empty projection

| Query | Type | Default | Notes |
| --- | --- | --- | --- |
| `limit` | integer | `50` | Must be 1 to 200 inclusive. A non-integer or out-of-range value returns `400`, it is not clamped or ignored. |

### What it filters

The projection reads up to 500 open commercial actions for the organization
(`ListCommercialActions` with `openOnly`), then keeps only the rows that are
actually waiting on a person. `COMPLETED`, `SKIPPED`, and `FAILED` rows are
never interruptions and are dropped. Each surviving row is classified into
exactly one bucket, first match wins, in this order:

| Bucket | Meaning |
| --- | --- |
| `hand_raiser_without_next_action` | A hand-raiser with no next-action type or no due time |
| `meeting_or_proposal_request` | Next action is a meeting or a proposal |
| `reply_awaiting_human` | A conversation exists, or the outcome is interested/replied |
| `review_request` | Cockpit lane is a review lane, the action type is inferred-email-review, or an unconverged hand-raiser fell through every branch above |

`hand_raiser_without_next_action` is checked first and surfaced first on
purpose. A hand-raiser with no due time is invisible in every due-date-ordered
view precisely because it has no due date, so it is the one that silently rots.
Nothing else may claim it.

Surface order: bucket severity, then overdue before not-overdue, then longest
wait. `limit` is applied after sorting, so the most dangerous rows survive the
truncation. `Overdue` is true only when a due time exists and has passed; a row
with no due time is never overdue, it is `hand_raiser_without_next_action`,
which is worse.

### Response

`200` with `{"data": {...}}`:

| Field | Type | Notes |
| --- | --- | --- |
| `generated_at` | timestamp | Projection time |
| `total` | integer | Rows classified as interruptions, before `limit` truncation |
| `by_bucket` | object | Every bucket, including zeros |
| `by_engine` | object | Every attributable engine, including zeros. Excludes unattributed. |
| `unattributed` | integer | Rows with no engine of origin, kept out of `by_engine` so an aggregate cannot absorb them |
| `oldest_waiting_since` | timestamp, optional | Earliest `created_at` among classified rows |
| `items` | array | At most `limit` items, in surface order |

Each item carries `action_id`, `account_id`, optional `candidate_id`, `bucket`,
`engine_lane`, `person_name`, `company_name`, `lane`, `state`,
`next_action_type`, optional `next_action_at`, `overdue`, `why_now`,
`created_at`, and `waiting_seconds`.

`by_bucket` and `by_engine` are always fully populated, zeros included, so a
reader can distinguish "no hand-raisers from INTEL_SEED" from "INTEL_SEED is
not a dimension this projection knows about".

### Reading `by_engine` today

Known limitation, stated here so the numbers are not misread. No production code
path currently writes `engine_lane` on `outreach_commercial_actions`. The column
exists (migration `000142`), the repository reads and writes it, and
`ConvergeHandRaise` stamps it, but `ConvergeHandRaise` has no production caller
yet and the existing first-touch action-creation paths were deliberately left
untouched. In production today, therefore:

- every row reads back `engine_lane = ''`
- `by_engine` is all zeros
- `unattributed` equals `total`

`outbound_first_touch: 0` means "nothing stamps this lane yet", **not** "first
touch produced no hand-raisers". The endpoint is honest about this because
`unattributed` is its own field rather than a hidden remainder, but the zero is
easy to misread. Wiring the writers means touching first-touch action creation,
which is out of scope for the closure of this campaign.

The same applies on the intel side. `intel.Chain.EngineLane` is populated from
`ObserveFromInbound` (which stamps `confenge_web`, the one engine knowable from
the record's kind) and from `ObserveFromAction` (which inherits whatever the
action row carries, so empty today). `ProjectEngineLaneProgression` has no
production caller.

## Engine attribution is not queue routing, and not a feedback dimension

`engine_lane` is deliberately three separate things away from what it might be
mistaken for:

- it is **not** the cockpit `lane` (`EMAIL_NEEDS_REVIEW`, `INBOUND_NOW`, and so
  on). Several code paths switch on `lane` for queue placement; overloading it
  would corrupt that placement.
- it is **not** `intel.RouteFamily` (inbound, outbound, partner,
  customer_expansion). Three of the four engines are outbound and one is
  inbound, so folding them in would break `isInboundAcquisitionChain` and
  re-split every existing chain.
- it is **not** part of `ChainIdentity`, `MetricKey`, or `feedbackDimensionKey`.
  The shipped acquisition outcome-feedback rollup projects byte-identically to
  before engine attribution existed, and its cohorts and withheld-small-cell
  behavior are unchanged. Per-engine reporting lives in the separate
  `ProjectEngineLaneProgression` projection instead, which keeps unattributed
  chains as their own visible row rather than spreading or dropping them.

An unknown value normalizes to unattributed rather than to a default engine. A
wrong attribution makes a working engine look like it produced someone else's
result, which is worse than a missing one.

## Known boundaries, out of scope by decision

- **Durable event-payload inbox.** See the replayability boundary above.
  Deferred pending cross-repo convergence with the real extra-cli producer.
- **INTEL_SEED cap policy.** `CONFENGE_INTEL_SEED_ENABLED` stays dormant and no
  cap, window, admission, or queue default was changed for it. `GateIntelSeed`
  has no production caller.
- **No `engine_lane` writers.** See "Reading `by_engine` today".
- **Watch mail and cold-outreach suppression are separate.** The INTEL_WATCH
  dispatcher checks the subscription's own active state, not the cold-outreach
  suppression list, because consulting the cold-outreach list for subscription
  mail would itself be the consent crossing this design refuses. A hard bounce
  terminates that one delivery as `WatchPermanent` but does not suppress future
  events for that address. Relevant to shared mailbox reputation, and a
  deliberate boundary rather than a gap to close silently.
- **No frontend.** `GET /confenge/interrupt-budget` is an internal operator
  endpoint with no dashboard surface yet.
