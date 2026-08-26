# Mailbox capacity readiness (sanitized)

Captured read-only from the CONFENGE runtime on 2026-08-26 at 13:35 UTC. No resume, dispatch, SMTP, credential read, database write, or mailbox creation was performed.

## Runtime identity and transport state

| Item | Readback |
| --- | --- |
| Deployed SHA | `36253ca2f1e57947dc8801be31b12f9ddd0e7b24` |
| Schema version | `129` |
| Worktree | Clean |
| Backend / worker / consumer | PASS / PASS / PASS |
| Postgres / Redis / NATS | PASS / PASS / PASS |
| Hostinger SMTP / IMAP reachability | PASS / PASS |
| Provider plan class | `BUSINESS_EMAIL_STARTER` |
| Dispatch control row | `paused=false` |
| Process pause env | `false` |
| Host kill-switch file | absent |
| Persistent volume kill switch | **engaged** |
| Effective transport state | **PAUSED** |

The volume kill switch is the dominant source. The database row alone must not be interpreted as permission to send.

## Configured mailbox inventory

Exactly one email account has a real SMTP/IMAP or OAuth credential record. No seed, fixture, inferred, or uncredentialed account is included.

| Field | Mailbox 1 |
| --- | --- |
| Enabled / status | yes / `active` |
| Provider | `smtp_imap`, operational provider Hostinger |
| Credentials | stored |
| Worker | assigned |
| Created | 2026-08-09 |
| DNS auth | `passing`; SPF, DKIM, and DMARC present |
| Auth checked | 2026-08-25 15:30 UTC |
| Configured daily cap | 50 attempts/day |
| Minimum wait | 600 seconds |
| Derived mailbox cap | 6 attempts/hour |
| Business window | Mon–Fri, 09:00–18:00, `America/Sao_Paulo` |
| Warmup evidence | start unknown, `warmup_days=0` |
| Provider advertised ceiling | 1000 outgoing messages/rolling 24h/mailbox, from the operator-confirmed hPanel evidence recorded in #43 |
| Scheduler envelope | 50/day and 6/hour, which is stricter than the advertised provider ceiling |

The provider ceiling is external operational evidence and is not rewritten as a database fact by this change. Until a typed per-mailbox provider source exists, the API reports the provider cap as unknown and enforces the stricter configured envelope.

## Observed throughput and deliverability

| Signal | Readback |
| --- | --- |
| CONFENGE campaign tasks created / completed | none |
| Latest provider attempt | unknown |
| Latest provider acceptance | unknown |
| Latest bounce | unknown |
| Latest complaint | unknown |
| Latest reply | unknown |
| Unresolved mailbox provider errors | 0 |
| Email touchpoints queued | 1 |
| Dispatch queue email rows queued | 1, representing the same approved draft |

`unknown` means the current schema has no mailbox-bound factual observation. It does not mean zero. Migration `000130` adds the binding needed for later attempts, acceptances, and provider failures to be attributed to the real mailbox.

## Derived forecast at capture time

The forecast basis is one ready mailbox, 50/day, 600-second minimum wait, 6/hour, business days only, 09:00–18:00 São Paulo, no recorded attempt, and one deduplicated queued message.

| Forecast | Value |
| --- | --- |
| Effective slots next 24h | 0, because transport is paused |
| Effective slots next 7d | 0, because transport is paused |
| Potential slots next 24h | 55 |
| Potential slots next 7d | 255 |
| Estimated days to drain queued | unknown while paused |
| Delivery promised | no |

The potential figures are time-sensitive and were derived at 10:35 in `America/Sao_Paulo`. They can cross two mailbox calendar days inside a 24-hour horizon. They are not a delivery estimate. If the kill switch remains engaged, the queue may grow indefinitely while the send rate remains zero.

## Readiness conclusion

The existing transport inventory is one real mailbox. The code must accept that constraint and let backlog wait. No additional mailbox, sender identity, warmup history, provider success, or deliverability signal was inferred. After this change is deployed, `GET /confenge/dispatch/status` will calculate the same classes of evidence continuously and expose the exact pause source, mailbox health, next slot, alerts, and forecast.
