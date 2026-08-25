# Per-touchpoint authorization

Every CONFENGE outbound requires exact, durable authorization before queue/send.
Human approval remains the general path. `CFG-FIRST-TOUCH-ROUTING-v1` may use a
delegated agent decision for the first routing email only. It never writes a
human `approved_by`.

States: PLANNED → DUE → DRAFTED/NEEDS_REVIEW → APPROVED → QUEUED → SENT.
Recoverable editorial states: AI_REWRITE_PENDING, ENRICHMENT_PENDING, and
REJECTED_REWRITE_PENDING. A copy rejection does not discard the lead.
Terminals: SKIPPED, REJECTED, REPLIED, DNC, BOUNCED, CANCELLED, FAILED.

Transport requires `approved_content_hash == content_hash` and either human
`approved_by` or a matching `DELEGATED_POLICY_APPROVE` row backed by a live
founder-authorized policy grant.
Edit/regenerate clears approval. CAS on APPROVED→QUEUED. Migration `000085`.
Cadence policy `confenge.cadence.v1` (no fixed multi-step campaign sequences).

Human approval atomically schedules the exact approved hash for the next
eligible business window. Scheduling is allowed while dispatch is paused; it
does not call a provider. The durable worker checks the business window, pause,
kill switch, DNC, policy authority, recipient and exact hash again before
transport. Transient transport failures retry with bounded backoff.

An institutional or departmental mailbox requires either explicit human
acknowledgement or the delegated first-touch evidence and QA gates. Neither path
turns the mailbox into a named person or weakens any suppression gate.

## Fail-closed transport

`EnrollDraft` (email) and `SendApprovedWhatsApp` both call `requireTouchTransport`.
A draft with status `APPROVED` but **no** linked touchpoint is refused. Linked
touchpoints must have matching hashes and an allowed authorization binding.

## Delegated first-touch policy

The CLI reserves an idempotent manifest batch, proves `lead == supplier` and
`lead != buyer`, reconciles datalake identity with original web sources, selects
one evidenced mailbox per CNPJ root, validates short routing copy, and records
the adversarial QA result. Copy-only failures may be rewritten at most three
times. Identity, role, recipient, or provenance failures stay `HOLD`.

Passing entries record `approved_by_type=delegated_agent`, authority
`founder-approved-first-touch-policy`, evidence and copy hashes, then call the
existing queue. A message is `QUEUED` only after an exact readback from
`confenge_dispatch_queue`; otherwise it is `APPROVED_NOT_SCHEDULED`.

PLANNED touches with `due_at <= now` are promoted to `DUE` when listing the
review queue (`PromoteDuePlannedTouchpoints`), so spaced follow-ups enter the
human queue after the prior touch is SENT/SKIPPED and the delay elapses.

## Prior-touch release

The next ordinal is never promoted to `DUE` (and cannot be approved/queued)
while any lower ordinal is still open. Only `SENT`, `SKIPPED`, or `REJECTED`
on every prior step releases the next. `PromoteDuePlannedTouchpoints` respects
this filter.

## Content bind

Transport re-hashes the live draft subject/body/recipient against the touchpoint
`approved_content_hash`. Editing the draft after approval clears touchpoint
approval and blocks send until re-approve.

## Editorial recovery and learning

The semantic composer runs first. If its output remains suboptimal and an AI
provider is configured, a bounded evidence-only rewrite may run synchronously
or through the editorial recovery worker. A successful rewrite always returns
to NEEDS_REVIEW before any new decision. The drafting/recovery AI cannot
approve, queue or send; the separate policy evaluator may subsequently record
`DELEGATED_POLICY_APPROVE` for an eligible first touch.

Failures and human rejection reasons are aggregated without recipient PII in
`confenge_editorial_signals`. The same transaction creates a redacted GitHub
issue outbox item. Operators can inspect or publish those items with
`confenge editorial issues list|sync`; `sync` requires `--confirm`. Structured,
versioned guideline proposals are managed with
`confenge editorial guidelines list|propose|activate`; activation also requires
explicit confirmation.

## Continuous generation into Comercial -> Rascunhos

Accounts in `READY_TO_GENERATE` are leased with `FOR UPDATE SKIP LOCKED` and
processed asynchronously when they have either a strict send-ready mailbox or
an explicitly controlled DIRECT/ROLE/GENERIC/FREEMAIL route. A missing named
person is not a generation blocker, while target fit, suppression, bounce and
opt-out remain fail-closed. The worker creates the idempotent cadence, generates
only its first due touchpoint, and stops at `NEEDS_REVIEW` (Comercial ->
Rascunhos) as an intermediate state, not as the expected terminal state for an
eligible policy item. Each tick drains a bounded burst of up to 100 accounts; failures
use exponential retry from 15 minutes up to 24 hours. A versioned account is
leased only when its usable candidate belongs to the account's current import;
historical routes cannot keep the worker retrying after a feed refresh. The
CONFENGE VPS maintains up to 500 prepared first touches. The policy evaluator
may drain the fully eligible subset; the remainder is the human exception
queue. This target grants no approval or send authority by itself.

`ENRICHMENT_PENDING` touchpoints are also retried by the editorial recovery
worker. After a complete feed import, newly fresh and eligible accounts with a
usable strict or controlled route have stale retry timers moved to `now`; stale,
suppressed, bounced and probabilistic-only routes stay untouched. The same
touchpoint can then return to `NEEDS_REVIEW`. Neither preparation worker can
approve, schedule, queue or send a message; authorization remains a separate
human or delegated-policy decision.

After a complete feed refresh, a `NEEDS_REVIEW` first touch whose bound contact
is absent from the account's current import is retired as `CANCELLED`; its draft
is kept as `BLOCKED` for audit. The remaining unapproved cadence is retired with
it. If the same supplier has another current eligible route, the account returns
to `READY_TO_GENERATE` and a new cadence is bound only to that current route.
Approved, queued and sent states are outside this recovery path.
