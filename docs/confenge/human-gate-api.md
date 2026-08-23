# CONFENGE human gate API

Contract: `confenge.human-gate.v1`. This is an additive control-plane API over
the existing immutable `confenge.frozen_cohort.v1` snapshot and
`bounded-cohort-policy.v1`; it does not replace the CLI contract and exposes no
send operation.

## Resources

- `GET /v1/confenge/cohorts?limit=&cursor=` lists versions with cursor
  pagination.
- `POST /v1/confenge/cohorts` creates a 1–10 member version from a completed
  canonical import run.
- `POST /v1/confenge/cohorts/{version_id}/reproduce` copies the exact immutable
  snapshot into the next version.
- `GET /v1/confenge/cohorts/{version_id}` returns the exact candidate/message
  preview, validations, reviews, invalidations and GO/NO-GO receipt.
- `GET /v1/confenge/cohorts/{version_id}/candidates/{candidate_id}` returns one
  progressive detail.
- `POST .../validation` requests pre-send syntax/MX/SMTP verification.
- `POST .../review` records `APPROVE`, `REJECT` or `HOLD` with a reason.
- `POST /v1/confenge/cohorts/{version_id}/decision` records `GO` or `NO_GO`.

All POSTs require `Idempotency-Key`. Reusing a key with the same payload returns
the original resource/receipt; reusing it with another payload returns 409.
Responses include source, `as_of`, freshness, policy/contract version, reason,
correlation id and receipt. List responses use `data` plus `pagination`.

Reads require read-contacts. Cohort/validation/review writes require
manage-contacts. GO/NO-GO requires manage-campaigns, not send-campaigns. The
authenticated API principal is audited by Warmbly; the edge separately audits
the Authelia human identity and never accepts a browser actor header.

## Invalidation and transport

APPROVE is effective only while recipient, content, policy, evidence and the
exact VALID result remain bound and unexpired. Live account/candidate state is
read on detail/decision; late suppression, opt-out, bounce, removal or unknown
live state invalidates approval. RISKY, INVALID, UNKNOWN and STALE cannot be
approved. GO fails for empty cohorts, stale source evidence or any member
without an effective APPROVE.

GO is a human decision receipt, not an email send. It materializes the existing
bounded authorization against the exact touchpoints as
`READY_FOR_LIVE_PREFLIGHT`; it does not queue them. The existing bounded cohort
transport authority remains responsible for queue admission and revalidates
approval, suppression, opt-out, cap, window, kill switch, TTL and policy at the
last mile. `CONFENGE_AUTO_SEND_ENABLED` and GREEN autorun remain false.

The authorization TTL is narrowed to the earliest of the frozen snapshot TTL,
canonical source-freshness expiry and every candidate-validation expiry. A
failed NO_GO revocation fails closed and does not record a misleading NO_GO
receipt.
