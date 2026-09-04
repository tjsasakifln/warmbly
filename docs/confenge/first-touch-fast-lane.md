# CONFENGE first-touch fast lane

## Why this exists

One first touch used to cross four processes and about seventeen state writes:
the backend enrolled the draft into a Warmbly campaign, the campaign scheduler
picked a contact, Cloud Tasks called back, the backend published to Kafka, a
worker opened the SMTP session, and a consumer finally recorded the result.
Every one of those steps could fail independently, and several of them could
turn a message the provider had already accepted back into pending work.

The queue row and the send ledger did not even share an identity. First-touch
work is now keyed `email:first-touch:account:<account_id>` through queue,
reservation, SMTP handoff and ledger. Replacing a draft cannot create a second
logical initial email for the same account.

The fast lane keeps two authoritative concepts and nothing else:

- `confenge_dispatch_queue` is the work to send.
- `confenge_dispatch_sends` is what was actually sent.

## The loop

`ProcessFastLaneOnce` (`internal/app/confenge/fast_lane.go`) does one thing:

1. resolve transport state (kill switch, durable pause, business window);
2. claim the next due row with `FOR UPDATE SKIP LOCKED` plus a lease;
3. close the row from the ledger if that key was already sent;
4. apply the pre-send gates that protect a recipient or a mailbox;
5. reserve under the queue's own message key (cap, min-gap, mailbox envelope);
6. after `MAIL` and `RCPT` succeed, atomically mark the reservation and queue as
   handed off immediately before SMTP `DATA`;
7. send synchronously over the mailbox's authenticated SMTP session;
8. commit the send, the reservation and the queue row in one transaction;
9. reconcile the legacy projections, best effort.

Step 8 is the only acceptance authority. Step 9 cannot undo it. If the process
dies after step 6, the row remains `attempted` and is never made sendable by a
lease timeout. This chooses at-most-once behavior for the interval SMTP cannot
make transactional with PostgreSQL.

## What the transport path no longer consults

These still exist and still run elsewhere. They are simply not asked again at
transport time, because they were already authoritative when the row was
approved into the queue:

- release SHA and runtime binding
- population snapshot, source run id and membership revision
- composer, template and prompt versions
- target-fit re-evaluation
- `campaign_contact_progress` as send authority
- the campaign scheduler, Cloud Tasks, Kafka and send-time optimisation

Re-deciding provenance at transport is what cancelled 3168 approved rows in a
single day in production, in four bursts, all with the reason
`delegated_authority_or_source_binding_advanced`.

## Gates that remain

Kill switch and durable pause, business window and business days, per-mailbox
daily cap, hourly cap and min-gap, mailbox enabled with SMTP configured,
approved-content binding recomputed from the live payload, recipient present
and syntactically usable, explicit commercial deactivation or party-role
conflict, live account/candidate DNC and bounce flags, non-empty approved
payload, and hard-bounce / complaint / opt-out suppression. Follow-ups
(`purpose` other than `INITIAL` or `ordinal` other than 1) are cancelled with
`FOLLOW_UP_NOT_AUTHORIZED` and never handed to the provider. A replied account
(queue state or a recorded `REPLIED` touchpoint) is the same class of stop.
Unsubscribe, complaint and hard-bounce suppression are first-class stops, not
aliases of each other. An inbound-only commercial representation is cancelled
with `account_inbound_only` and never handed to the provider. A missing or
unclassifiable provider event parks as `UNKNOWN` (`unknown_provider_result`)
and is never treated as accepted or delivered. An unreadable safety source defers the row fail-closed instead of
cancelling durable work. A stop that arrives after reservation still wins in
the post-reserve recheck and in `BeforeHandoff` immediately before SMTP `DATA`.

## Failure model

| Outcome | Meaning | Action |
| --- | --- | --- |
| Accepted | provider took the message | record send, close row |
| Permanent | 5xx at `RCPT TO` | terminal `failed`, suppress recipient, continue |
| Transient | network, 4xx, unreachable | bounded retry with backoff (5 attempts), unless a live stop arrived |
| Ambiguous | failure at end of `DATA` | park as `attempted`, never resend |
| Unrecognized | provider answer is not in the closed set | park as `attempted` (same as ambiguous), never resend, never `NO_RESPONSE` |

Ambiguous (and unrecognized) stay deliberately unresolved. The message may
already be in flight, so it is correlated by a deterministic `Message-ID`
derived from the logical message key rather than retried. A newly arrived stop
(DNC, suppression, opt-out, complaint, reply, pause) still beats a transient
retry: the row is cancelled or deferred instead of sent.

A cap, min-gap or window deferral is scheduling, not failure: `DeferQueue`
returns the row without consuming a retry attempt.

## Duplicate prevention

`confenge_dispatch_sends` has a `UNIQUE (message_key)` index, and the fast lane
reserves and commits under the account-scoped initial key. `attempted` and
`failed` rows are terminal on producer upsert. Legacy `SENT` touchpoints count
as accepted only when both `sent_at` and `provider_message_id` provide external
evidence.

## Cutover

The two paths must never drain the same queue at once.
`CONFENGE_FAST_LANE_ENABLED` selects exactly one owner at boot.

1. Pause dispatch: `deploy/confenge-vps/pause.sh <reason>`.
2. Confirm the durable control row and the kill-switch file both read paused.
3. Deploy the release carrying the fast lane.
4. Set `CONFENGE_FAST_LANE_ENABLED=true` and recreate the backend. The log line
   `CONFENGE first-touch fast lane owns transport` confirms the legacy dispatch
   queue worker did not start.
5. Reconcile: no queue row whose key already appears in `confenge_dispatch_sends`
   may remain selectable.
6. Dry run: confirm which row is next by `due_at ASC, priority DESC, created_at ASC`
   among `status='queued' AND due_at <= now()`.
7. Resume inside the business window and watch one canary send.
8. Verify Hostinger acceptance and exactly one new ledger row carrying
   `provider_message_id`, `recipient` and `queue_id`.
9. Allow normal cadence and observe consecutive sends.

Rollback is the same switch: pause, set `CONFENGE_FAST_LANE_ENABLED=false`,
recreate the backend, resume. The queue is untouched by the flag, so no work is
lost in either direction.

## Steady state

The fast lane runs continuously. Business hours are a scheduling gate inside a
running process, not an operational pause, so nothing about nights or weekends
needs an operator.

Normal production state, at every hour of every day:

```
CONFENGE_FAST_LANE_ENABLED=true
durable dispatch pause = false
file kill switch = absent
backend container up and healthy
```

Outside 09:00-18:00 America/Sao_Paulo on a business day, `ProcessFastLaneOnce`
resolves transport state before it claims anything, sees `send_window` blocking,
and returns having done nothing. No row is claimed, no lease is taken, no
attempt is consumed, no failure is recorded, and no campaign is auto-paused.
Queued rows simply stay queued with their `due_at` intact and are claimed on the
next tick that falls inside the window.

At Friday 18:00 the correct state is therefore "running, window closed, next
eligible slot Monday 09:00", not "paused". Read it as:

```
state    = PAUSED
blockers = ["send_window:outside_send_window"]
```

`state` is the resolved answer to "may a message leave right now?", and the
resolver labels any non-sending state `PAUSED`. The single `send_window` blocker
is what distinguishes automatic window gating from an operator stop: an
operator stop also lists `durable_control` or `file_kill_switch`. If
`send_window` is the only blocker, the system is healthy and unattended.

Monday at 09:00 the window source flips to ACTIVE on its own and sending
resumes with no human action.

### When to actually pause

`pause.sh` and `resume.sh` are for emergency stop, deliberate maintenance or
cutover, deployment safety, and explicit operator intervention. They are not a
weekly schedule. Do not add cron jobs, timers, or an evening/weekend pause
habit around them: doing so replaces an authoritative gate with a manual one
and creates a Monday morning where sending silently does not start.

After any maintenance pause, clear it as soon as the maintenance is done, even
if that is outside the business window. Clearing it outside the window produces
zero sends by construction.

## Runway

The delegated producer selects only candidates from the account's current,
completely applied import. Institutional mailboxes require the explicit
`controlled_email_eligible` and `preferred_initial` contract plus observed,
fresh, company-owned provenance and the allowed route classes. The separate
named-person `email_send_ready` projection is not substituted for that
institutional contract. The producer materialises to the configured runway
horizon and assigns `due_at` across business days; the fast lane only consumes.
