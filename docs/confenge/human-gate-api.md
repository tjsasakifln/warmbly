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
- `POST /v1/confenge/cohorts/{version_id}/candidates/{candidate_id}/adjust`
  forks the version into N+1 carrying a human copy edit for exactly that one
  candidate. See "Adjust" below.
- `GET /v1/confenge/cohorts/{version_id}` returns the exact candidate/message
  preview, validations, reviews, invalidations and GO/NO-GO receipt.
- `GET /v1/confenge/cohorts/{version_id}/candidates/{candidate_id}` returns one
  progressive detail.
- `POST .../validation` requests pre-send syntax/MX/SMTP verification.
- `POST .../review` records `APPROVE`, `REJECT` or `HOLD` with a reason;
  `APPROVE` also requires `"acknowledged": true`.
- `POST /v1/confenge/cohorts/{version_id}/decision` records `GO` or `NO_GO`
  only when `confirmation` exactly names the immutable version (for example
  `v3`).

All POSTs require `Idempotency-Key`. Reusing a key with the same payload returns
the original resource/receipt; reusing it with another payload returns 409.
Responses include source, `as_of`, freshness, policy/contract version, reason,
correlation id and receipt. List responses use `data` plus `pagination`.
The acknowledgement and typed version are included in the idempotent intent
hash, so an ambiguous retry cannot silently change the human confirmation.

Reads require read-contacts. Cohort/validation/review/adjust writes require
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

## Derivation taxonomy

Every version row records how it came to exist. "There is a version N+1" cannot,
by itself, tell an auditor whether the bytes are identical, machine-recomposed
or human-edited, so the distinction is durable in Postgres
(`confenge_cohort_versions.derivation`, migration 000117) and is projected on
every read as `derivation` plus `parent_version`.

| derivation  | meaning                                                              |
| ----------- | -------------------------------------------------------------------- |
| `CREATE`    | first freeze of a cohort from a completed canonical source run.       |
| `REPRODUCE` | `frozen_manifest` copied byte for byte from `parent_version`.         |
| `RECOMPOSE` | **reserved.** Re-run the composer against the current source, policy and composer version. No endpoint implements it yet; the taxonomy is durable first so today's rows never have to be reinterpreted later. |
| `ADJUST`    | a human edited `subject`/`body_text` for exactly one candidate.       |

`parent_version` is `null` only for `CREATE`. Rows that predate 000117 are
backfilled: `reproduced_from_version IS NOT NULL` becomes `REPRODUCE`, all
others become `CREATE`.

## Adjust

`POST /v1/confenge/cohorts/{version_id}/candidates/{candidate_id}/adjust`

Adjust is an **operator** action. It requires manage-contacts + write-contacts,
exactly like review; it is deliberately *not* an admin action, and GO stays
manage-campaigns. Like every other write it requires `Idempotency-Key` and
carries the same correlation id header as the other human-gate writes.

**Adjust neither queues, dispatches, sends nor resumes anything.** It creates an
immutable draft version and nothing else. The new version is not authorized, not
approved and not queue-ready; reaching a real send still requires validation,
per-candidate APPROVE and a fresh GO on the new version, and the bounded
transport authority still revalidates everything at the last mile.

Request body — subject and body_text are the only mutable facts:

```json
{
  "subject": "…",
  "body_text": "…",
  "reason": "…",
  "confirmation": "v1",
  "expected_frozen_hash": "…"
}
```

`reason` must be at least 8 characters after trimming. `confirmation` must be
exactly `"v"` + the current version number. `expected_frozen_hash` must equal
the current version's `frozen_hash`; together they make an adjustment
impossible to apply to a version the operator was not actually looking at.

Success is `201` with:

```json
{
  "contract_version": "confenge.human-gate.v1",
  "cohort": { "…": "the full cohort payload of the NEW version" },
  "adjustment": {
    "id": "…", "cohort_id": "…", "from_version": 1, "to_version": 2,
    "candidate_id": "…",
    "before_content_hash": "…", "after_content_hash": "…",
    "before_frozen_hash": "…", "after_frozen_hash": "…",
    "diff": [{"field": "subject", "before": "…", "after": "…"},
             {"field": "body_text", "before": "…", "after": "…"}],
    "revoked_authorization_id": null,
    "actor_id": "…", "correlation_id": "…", "receipt": "…", "created_at": "…"
  }
}
```

### What adjust guarantees

- The prior version is **never** updated. Its `frozen_manifest` stays byte
  identical and readable forever; adjust only ever inserts.
- Version N+1 lives under the same `cohort_id`, carries the adjusted copy for
  exactly the addressed candidate, and copies every other member unchanged.
- The member `content_hash`, the manifest `cohort_hash`/`frozen_hash`, the
  recipient-set hash and the preview samples are recomputed with the same
  helpers the freeze path uses. There is no second hashing implementation.
- The adjusted copy is re-run through the freeze-time copy QA
  (`ValidateCopyForRouteClass`) and is refused if it fails.
- The new version is born with **no validation, no review and no GO**. Nothing
  is inherited: nothing that was approved about the old copy is evidence about
  the new copy.
- Replaying the same `Idempotency-Key` returns the same adjustment and does not
  create another version. A different key against a now-superseded version fails
  `version_superseded` rather than silently forking the cohort.
- Two simultaneous adjusts of the same version cannot both create N+1. The
  parent row is locked `FOR UPDATE` and `UNIQUE (organization_id, cohort_id,
  version)` is the backstop.
- If the prior version carries a live bounded authority, it is revoked inside
  the same transaction that creates N+1, and `revoked_authorization_id` names
  it. If that revocation cannot be proven inside the transaction, the whole
  adjustment is refused with `authority_active`. There is never a moment with
  two valid authorities.

### Immutable fields

`mailbox`, `recipient*`, `evidence*`, `source*`, `policy_version`,
`route_class` and `composer_version` are canonical-source evidence or
machine-derived bindings. Sending any of them in the body is a hard `422`
`immutable_field` naming the offending key, checked against the raw request body
so a protected key can never be silently dropped by a typed struct. These are
correctable only at the canonical source, followed by a fresh composition — not
by typing over them here.

### Error codes

| status | code                          | when                                                        |
| ------ | ----------------------------- | ----------------------------------------------------------- |
| 400    | `idempotency_key_required`    | no `Idempotency-Key`.                                        |
| 400    | `adjust_copy_required`        | empty `subject` or `body_text`.                              |
| 400    | `adjust_reason_required`      | `reason` shorter than 8 characters after trimming.           |
| 400    | `adjust_confirmation_required`| `confirmation` missing.                                      |
| 400    | `adjust_expected_frozen_hash_required` | `expected_frozen_hash` missing.                     |
| 404    | `candidate_not_found`         | the candidate is not in this immutable version.              |
| 404    | `cohort_version_not_found`    | the version does not exist for this organization.            |
| 409    | `frozen_hash_mismatch`        | `expected_frozen_hash` != the current `frozen_hash`.         |
| 409    | `confirmation_mismatch`       | `confirmation` != `"v"` + current version.                   |
| 409    | `version_superseded`          | the addressed version is not the latest for its cohort, including when a concurrent adjust won the race. |
| 409    | `authority_active`            | the prior version carries a GO authority that could not be atomically revoked. |
| 409    | `source_stale`                | the version's canonical source evidence expired. Adjust cannot refresh evidence. |
| 409    | `composer_drift`              | the version was composed by another composer version; recomposing a content hash onto a composer that did not produce the manifest would invent a second source of truth. |
| 409    | `idempotency_payload_conflict`| the key was already used with another payload.               |
| 422    | `immutable_field`             | the body tried to set a protected field. The message names it. |
| 422    | `copy_qa_failed`              | the adjusted copy fails copy QA. The message lists the reason codes. |
| 422    | `cohort_bounds_violation`     | the cohort size or daily cap is outside the bounded-cohort limits. |

Live recipient state (late suppression, opt-out, hard bounce, removal) is not an
adjust gate: it is read on every detail view and re-enforced at GO and at
dispatch. Adjust edits a draft; it cannot make anything sendable. The live
candidate is read only to give copy QA the same recipient context the freeze
path had, and an unreadable live source degrades that context rather than
blocking the edit.
