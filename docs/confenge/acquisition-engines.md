# CONFENGE acquisition engines

Internal engineering notes for the four-engine acquisition surface. Operator and
backend facing. The customer-visible behavior of these lanes is documented in
`docs/content/docs/guides/confenge-outreach.mdx`; this page records the parts an
operator or an engineer needs and a customer does not: the INTEL_WATCH
replayability boundary and the durable inbox that resolves it, the INTEL_SEED
cap, the `GET /confenge/interrupt-budget` and `GET /confenge/sales-context`
contracts, and what engine attribution does and does not claim.

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
INTEL_SEED into a subscription automatically. The one production caller of
`CreateOrReactivateSubscription` is CONFENGE_WEB intake, which acts on a
`MONITOR_COMPANY` or `MONITOR_OPPORTUNITY` envelope carrying its own consent
provenance, so consent still arrives only through an explicit act by the person
whose address it is.

None of the additive lanes takes a dispatch reservation, a touchpoint, a cohort
slot, or any first-touch queue state. Both intelligence lanes send through the
same `FirstTouchTransport` and the same mailbox-resolution rules as first touch.
There is one SMTP implementation in the system.

## INTEL_WATCH replayability boundary, and how it is resolved

This was the load-bearing caveat on the INTEL_WATCH recovery story. It is
resolved by the durable inbox described at the end of this section. The
distinction it rests on still matters and is stated first.

**Dedup buys at-most-once. Replayability buys at-least-once. Only the first is
source-independent.**

The delivery ledger keys dedup on `(subscription, event identity, semantic
content hash)`. That property holds no matter what the upstream does: a
duplicate is refused, a replay of an already-delivered notification is a no-op,
and a failure after the irreversible handoff is parked as `AMBIGUOUS` and never
resent.

Automatic resumption of a `PENDING` row is a different property. It is
*completeness*, not safety, and it holds only if the event can still be
obtained. The delivery ledger deliberately stores delivery identity, not event
content, so it cannot rebuild a notification on its own.

The real upstream (extra-cli) posts once, over HTTP, and cannot be asked to post
again. Warmbly therefore does not depend on the upstream being replayable. It
persists the envelope itself.

### The durable inbox

`confenge_intel_watch_inbox` (migration `000143`) stores every accepted
`CONFENGE_OPPORTUNITY_EVENT/1.0` envelope at the moment of ingestion, before
anything else happens to it, so a crash after the HTTP `200` cannot lose it.
`PostgresEventProducer` in
`internal/app/confenge/liveintel/events_postgres.go` is the production
`EventProducer` and replays from that table.

Two properties of the table are load bearing:

- **Emission never consumes a row.** The inbox is append-only inside a bounded
  replay window (24 hours by default). Marking a row consumed when it is handed
  to the consumer would lose the event whenever the consumer failed straight
  afterwards, which is precisely the failure the inbox exists to prevent.
  Re-emission is free because the delivery ledger refuses a duplicate by primary
  key.
- **The organization comes from the column, not the payload.** The column is
  what Warmbly's own webhook authentication resolved. Any `org_id` inside the
  posted body is overwritten and never read, so an external caller cannot write
  into another organization's watch pipeline.

`emit_lease_until` bounds duplicate *work* between two producer instances. It is
not a correctness boundary: with the lease cleared, the same rows are simply
offered again and the ledger dedups them.

`Subscribe` emits the currently replayable batch and closes the channel, exactly
as the fixture producer does. That is the contract the reclaim worker was built
against: it ranges to close, so a producer that held a live tail open would hold
one reclaim pass open forever.

`FixtureEventProducer` is unchanged and still available. It is selected only
when an operator explicitly sets `CONFENGE_INTEL_WATCH_EVENTS_FILE`, which is a
rehearsal and test path; otherwise the durable inbox is the source. With neither
configured the lane is dormant: no producer is built, the reclaim worker is
never started, and first-touch startup and sending are untouched.

Stated in code at:

- `internal/app/confenge/liveintel/events_postgres.go` (package doc block)
- `internal/app/confenge/liveintel/watch_reclaim_worker.go` (package doc block)
- `internal/app/confenge/liveintel/events_fixture.go` (package doc block)

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

### Reading `by_engine`

`engine_lane` now has production writers. There is exactly one:
`PersistHandRaise` in
`internal/app/confenge/handraise_persist.go`, which converges every signal
through `ConvergeHandRaise` and files it idempotently on
`HandRaiseIdempotencyKey`. Stamping the lane in one place is why two engines
cannot disagree about attribution.

Its callers:

| Engine | Call site | Signal |
| --- | --- | --- |
| `confenge_web` | `IngestWebIntent`, on a `REQUEST_DEEP_DIVE` or `REQUEST_HUMAN_REVIEW` envelope. A valid contact with no account is admitted inbound-only and still lands on this queue | `REQUEST_DEEP_DIVE` / `REQUEST_HUMAN_REVIEW` |
| `outbound_first_touch` | `convergeReplyHandRaise`, from `ProcessInboundHandoff` on a positive reply correlated to a first-touch touchpoint | `POSITIVE_REPLY_FIRST_TOUCH` |
| `intel_seed` | the same reply path, when the caller declares `InboundHandoff.EngineLane` | `INTEL_SEED_RESPONSE` |

Attribution is derived, never guessed. `ReplyEngineLane` uses the lane the
caller declared; failing that, a correlated touchpoint, which is first-touch
cadence state no other engine writes. With neither, the row stays unattributed
and is counted in `unattributed` rather than assigned to whichever engine
happens to be running.

Two gaps remain, stated so a zero is not misread:

- **`intel_watch` has no hand-raise signal.** The closed `HandRaiseSignal` set
  has a first-touch reply and an INTEL_SEED response and nothing for
  subscription mail. A reply to watch mail therefore cannot converge under a
  signal that names it. Adding one is a policy decision, not something the
  mapping may invent, so `by_engine["intel_watch"]` stays zero.
- **`intel_seed` needs its caller to declare the lane.** Replies arrive through
  the same IMAP path as first touch and carry no lane marker of their own. The
  mapping is wired and tested; it fires once a caller populates
  `InboundHandoff.EngineLane`.

On the intel side, `intel.Chain.EngineLane` is populated from
`ObserveFromInbound` (which stamps `confenge_web`) and from `ObserveFromAction`
(which inherits whatever the action row carries, so it now inherits a real lane
for hand-raiser rows). `ProjectEngineLaneProgression` still has no production
caller.

## `GET /confenge/sales-context`

The `CONFENGE_SALES_CONTEXT/1.0` artifact. A flat, versioned projection of the
hand-raisers the engines produced, so a downstream sales tool can pick a
conversation up without a second data model and without calling back per row.

Implementation: `internal/app/confenge/sales_context.go`
(`ExportSalesContext`), handler `GetConfengeSalesContext`, same auth, gating and
`limit` rules as the interrupt budget, and the same read-only guarantee: it
reserves nothing, sends nothing and mutates no row.

It reads the same open commercial actions the interrupt budget reads and keeps
only the hand-raisers. Every field is copied from a row that already exists.
Nothing here scores, ranks or infers intent, and where the underlying row has no
answer the field is absent, because an empty field is honest and a filled one
would not be.

Per item: `acquisition_channel` (the engine lane), `company_ref` and
`opportunity_id`, `company_name`, `person_name`, `intent_reason` (the converged
signal, read back off the row's own idempotency key), `reply_reason`,
`conversation_started`, a `facts` block carrying `why_now`, `factual_hook`,
`evidence_ids` as provenance and `confidence` plus `stated_limits` as the limits
travelling with them, the account's `touchpoints`, and the next action.

There is no claim-safety field. No live claim-safety or attestation concept
exists in the repo to source one from, and inventing one would be a claim the
system cannot back.

The subject a request pointed at is recorded as a `subject_key:` entry in the
action's `evidence_ids`. The action row has no subject column, and overloading
one that already means something else would corrupt it; provenance is where an
identity the claim points at legitimately belongs.

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

## INTEL_SEED cap

INTEL_SEED takes no dispatch reservation, no queue row and no cohort slot, so
before this change nothing counted an INTEL_SEED send at all.

`confenge_intel_seed_sends` (migration `000143`) is the lane's own counter and
its no-resend record at the same time. Two things follow:

- **The cap is additional, never a carve-out.** A seed touch must pass every
  first-touch admission predicate `GateIntelSeed` already applies AND fit inside
  `CONFENGE_INTEL_SEED_DAILY_CAP` for its organization that UTC day. The cap
  counts rows in this table. It never reads, spends or reduces the first-touch
  dispatch governor's budget.
- **The row is written before the handoff.** It is the fence as well as the
  counter, so a crash between recording and sending cannot re-send the recipient
  or under-count the cap.

Both opt-ins are required and both fail closed. `CONFENGE_INTEL_SEED_ENABLED`
stays `false` by default, and `CONFENGE_INTEL_SEED_DAILY_CAP` defaults to `0`,
which sends nothing. A missing, malformed or negative cap is `0`; it never falls
back to a first-touch number budgeted for a different lane. An unwired ledger is
an uncountable cap, which is not a cap, so the lane stays dormant.
`CONFENGE_INTEL_SEED_ORG_ID` scopes the loop to one organization; unset means
the loop does not run.

A contact this lane has written to is skipped for 90 days. A seed offer is a
cold email and repeating it is the failure mode this lane must not have.

Deliberate divergence from the fast lane, named here so it can be overruled: a
non-accepted send keeps its ledger row, so a transient SMTP failure permanently
spends that cap slot and that contact is not retried. A duplicate cold email is
worse than a missed one, but this is stricter than how first touch treats a
transient.

`internal/app/confenge/intel_seed_worker.go` (`ProcessIntelSeedOnce`,
`IntelSeedHeadroom`), gate in `internal/app/confenge/intel_seed.go`.

## Known boundaries, out of scope by decision

- **INTEL_WATCH hand-raise signal.** See "Reading `by_engine`". A reply to
  subscription mail has no member of the closed signal set, so it cannot be
  attributed. This is a deliberate refusal to invent a mapping, not a gap to
  close silently.
- **INTEL_SEED reply attribution.** The mapping exists; the reply path does not
  yet declare the lane. See "Reading `by_engine`".
- **Watch mail and cold-outreach suppression are separate.** The INTEL_WATCH
  dispatcher checks the subscription's own active state, not the cold-outreach
  suppression list, because consulting the cold-outreach list for subscription
  mail would itself be the consent crossing this design refuses. A hard bounce
  terminates that one delivery as `WatchPermanent` but does not suppress future
  events for that address. Relevant to shared mailbox reputation, and a
  deliberate boundary rather than a gap to close silently.
- **No frontend.** `GET /confenge/interrupt-budget` and
  `GET /confenge/sales-context` are internal operator endpoints with no
  dashboard surface yet.
