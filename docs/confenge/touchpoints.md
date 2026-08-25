# Per-touchpoint human approval

Every CONFENGE outbound (email or WhatsApp) requires explicit human approval of
that exact message before queue/send. AI never writes `approved_by`.

States: PLANNED → DUE → DRAFTED/NEEDS_REVIEW → APPROVED → QUEUED → SENT.
Recoverable editorial states: AI_REWRITE_PENDING, ENRICHMENT_PENDING, and
REJECTED_REWRITE_PENDING. A copy rejection does not discard the lead.
Terminals: SKIPPED, REJECTED, REPLIED, DNC, BOUNCED, CANCELLED, FAILED.

Transport requires `approved_content_hash == content_hash` and human `approved_by`.
Edit/regenerate clears approval. CAS on APPROVED→QUEUED. Migration `000085`.
Cadence policy `confenge.cadence.v1` (no fixed multi-step campaign sequences).

Human approval atomically schedules the exact approved hash for the next
eligible business window. Scheduling is allowed while dispatch is paused; it
does not call a provider. The durable worker checks the business window, pause,
kill switch, DNC, policy authority, recipient and exact hash again before
transport. Transient transport failures retry with bounded backoff.

An institutional or departmental mailbox requires an explicit human
acknowledgement on the approval action. That acknowledgement does not turn the
mailbox into a named person and does not weaken any suppression gate.

## Fail-closed transport

`EnrollDraft` (email) and `SendApprovedWhatsApp` both call `requireTouchTransport`.
A draft with status `APPROVED` but **no** linked touchpoint is refused. Linked
touchpoints must have matching content hashes and human `approved_by`.

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
to NEEDS_REVIEW. AI cannot approve, queue or send.

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
Rascunhos). Each tick drains a bounded burst of up to 100 accounts; failures
use exponential retry from 15 minutes up to 24 hours. A versioned account is
leased only when its usable candidate belongs to the account's current import;
historical routes cannot keep the worker retrying after a feed refresh. The
CONFENGE VPS maintains up to 500 first-touch reviews, matching one bulk-review
request without approving or sending them.

`ENRICHMENT_PENDING` touchpoints are also retried by the editorial recovery
worker. After a complete feed import, newly fresh and eligible accounts with a
usable strict or controlled route have stale retry timers moved to `now`; stale,
suppressed, bounced and probabilistic-only routes stay untouched. The same
touchpoint can then return to `NEEDS_REVIEW`. Neither worker can approve,
schedule, queue or send a message.

After a complete feed refresh, a `NEEDS_REVIEW` first touch whose bound contact
is absent from the account's current import is retired as `CANCELLED`; its draft
is kept as `BLOCKED` for audit. The remaining unapproved cadence is retired with
it. If the same supplier has another current eligible route, the account returns
to `READY_TO_GENERATE` and a new cadence is bound only to that current route.
Approved, queued and sent states are outside this recovery path.
