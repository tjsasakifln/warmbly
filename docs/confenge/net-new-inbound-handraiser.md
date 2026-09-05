# NET_NEW_INBOUND_HANDRAISER consumer (MV-03)

Warmbly consumes the versioned multi-vertical hand-raiser on the existing
authenticated inbound route. Governance owns policy and admission; web-cfg is
the `CONFENGE_WEB` producer and receipt origin; Warmbly owns the opportunity,
manual action and outcome. This is not a second CRM or an outbound authority.

## Runtime authority

- Contract: `NET_NEW_INBOUND_HANDRAISER/1.0.0-draft.20260904`
- Governance source: PR #172, commit
  `990c6ae237c3f7188728e97283bc69c130f6028d`
- Policy hash:
  `sha256:405ac86064a90641b843352d21cd21703744115de9592558e100671d92276df7`
- Source: `CONFENGE_WEB`; acquisition lane: `NET_NEW_INBOUND`
- Runtime admission requires an exact contract/version/hash match. Local
  conformance fixtures are drift checks only and are never runtime authority.

HTTP 2xx is not acceptance. The producer must inspect `outcome` and retain the
receipt, then use readback of the same `logical_id` as durable confirmation.

## Compatible intake

The parser accepts the official Governance request names and the existing B2G
aliases. MV-03 adds `technical_triage_review`, `technical_triage_v1` and
`other_technical_need` without removing the private-readiness offer/asset or
the five existing nuclei.

The actionable contact is carried only in the authenticated HMAC body as
`protected_contact`. Either a valid email or a valid WhatsApp/phone is enough;
Warmbly never invents a placeholder email. `preferred_channel` is persisted on
the receipt and candidate. The protected contact is removed from raw payload,
readback and metrics; operational contact fields remain in Warmbly's existing
access-controlled contact record.

## Admission and qualification

- `CLEAR`/legacy `NONE`: eligible for normal qualification.
- `UNKNOWN` or `NOT_SCREENED`: accepted for manual review as
  `CONFLICT_CHECK_REQUIRED`; they are never coerced to clear.
- `HIT` or legacy `DECLINE`: `REJECTED_WITH_REASON`, without account, action or
  provider mutation.
- `other_technical_need`: always `NEEDS_CONTEXT`, including when conflict is
  `NOT_SCREENED`.
- A request claiming `outbound_eligible`, `auto_send` or dispatch is rejected.

Accepted intake creates or reuses one inbound representation and one manual
commercial action. A pre-existing outbound-eligible account does not transfer
that authority to the inbound receipt. All returned and persisted contract
flags remain `outbound_eligible=false`, `auto_send=false` and
`dispatch_attempted=false`.

## Handoff, readback and telemetry

The manual action is non-sendable and non-dispatchable. Operator visibility is
kept in cockpit/browser channels, while SMTP is disabled for this contract.
No inbound submission creates follow-up or outbound eligibility.

`GET /confenge/inbound/handraisers/:logicalId` returns the same receipt,
logical ID, decision, qualification, preferred channel and safety flags without
contact PII. Reusing an idempotency key with different admission material is
rejected; exact retry returns the original durable receipt.

Metrics contain only nucleus, state and reason. Contact name, email, phone,
conflict corpus and protected payload are excluded.

Rollback is to stop producer submissions or revert the consumer pin/code while
retaining receipts. No producer in MV-03 merges or deploys this change.
